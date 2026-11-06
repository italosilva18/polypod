package audit

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const maxCheckpointBytes = 64 << 20

var checkpointIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

// Checkpoint stores only metadata in JSON. File bytes live in a private backup
// next to the metadata and are never returned by the audit tool.
type Checkpoint struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	SHA256    string    `json:"sha256"`
	Size      int64     `json:"size"`
	Mode      uint32    `json:"mode"`
	CreatedAt time.Time `json:"created_at"`
}

// Inspection reports the current target hash to make restore a two-step,
// compare-and-swap operation. "missing" is used when the target was removed.
type Inspection struct {
	Checkpoint    Checkpoint `json:"checkpoint"`
	CurrentSHA256 string     `json:"current_sha256"`
	CurrentSize   int64      `json:"current_size,omitempty"`
}

// CreateCheckpoint snapshots one regular file with bounded memory and fsync.
func (s *Store) CreateCheckpoint(ctx context.Context, path string) (Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return Checkpoint{}, err
	}
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil || strings.TrimSpace(path) == "" {
		return Checkpoint{}, errors.New("caminho de checkpoint invalido")
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("inspecionando arquivo: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Checkpoint{}, errors.New("checkpoint aceita apenas arquivo regular, sem link simbolico")
	}
	if info.Size() > maxCheckpointBytes {
		return Checkpoint{}, fmt.Errorf("arquivo excede %d MiB", maxCheckpointBytes>>20)
	}
	f, err := os.Open(abs)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("abrindo arquivo: %w", err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return Checkpoint{}, errors.New("arquivo mudou durante a criacao do checkpoint")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxCheckpointBytes+1))
	if err != nil {
		return Checkpoint{}, fmt.Errorf("lendo arquivo: %w", err)
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return Checkpoint{}, errors.New("arquivo mudou durante a leitura do checkpoint")
	}
	if len(data) > maxCheckpointBytes {
		return Checkpoint{}, fmt.Errorf("arquivo excede %d MiB", maxCheckpointBytes>>20)
	}
	if err := ctx.Err(); err != nil {
		return Checkpoint{}, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Checkpoint{}, fmt.Errorf("criando identificador: %w", err)
	}
	id := hex.EncodeToString(random[:])
	sum := sha256.Sum256(data)
	cp := Checkpoint{ID: id, Path: abs, SHA256: hex.EncodeToString(sum[:]),
		Size: int64(len(data)), Mode: uint32(info.Mode().Perm()), CreatedAt: time.Now().UTC()}
	s.mu.Lock()
	defer s.mu.Unlock()
	backup := filepath.Join(s.checkpointDir, id+".bin")
	if err := writeAtomic(backup, data, 0o600); err != nil {
		return Checkpoint{}, fmt.Errorf("gravando checkpoint: %w", err)
	}
	meta, err := json.Marshal(cp)
	if err != nil {
		_ = os.Remove(backup)
		return Checkpoint{}, err
	}
	if err := writeAtomic(filepath.Join(s.checkpointDir, id+".json"), meta, 0o600); err != nil {
		_ = os.Remove(backup)
		return Checkpoint{}, fmt.Errorf("gravando metadados: %w", err)
	}
	if err := s.appendLocked(ctx, Event{Kind: "checkpoint", Status: "created", CheckpointID: id, Path: abs}); err != nil {
		return cp, fmt.Errorf("checkpoint criado; auditoria indisponivel: %w", err)
	}
	return cp, nil
}

// InspectCheckpoint returns metadata and the current target hash, never bytes.
func (s *Store) InspectCheckpoint(ctx context.Context, id string) (Inspection, error) {
	if err := ctx.Err(); err != nil {
		return Inspection{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp, err := s.loadCheckpointLocked(id)
	if err != nil {
		return Inspection{}, err
	}
	hash, size, err := fileHash(cp.Path)
	if err != nil {
		return Inspection{}, err
	}
	return Inspection{Checkpoint: cp, CurrentSHA256: hash, CurrentSize: size}, nil
}

// ListCheckpoints returns recent checkpoint metadata without file contents.
func (s *Store) ListCheckpoints(ctx context.Context, limit int) ([]Checkpoint, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.checkpointDir)
	if err != nil {
		return nil, err
	}
	result := make([]Checkpoint, 0, len(entries)/2)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		cp, err := s.loadCheckpointLocked(id)
		if err != nil {
			return nil, err
		}
		result = append(result, cp)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

// RestoreCheckpoint checks the current hash before atomically replacing the
// file. External writers are not coordinated, so a write between the check
// and rename can still be overwritten.
func (s *Store) RestoreCheckpoint(ctx context.Context, id, expectedCurrentSHA256 string) (Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return Checkpoint{}, err
	}
	if expectedCurrentSHA256 == "" {
		return Checkpoint{}, errors.New("informe current_sha256 obtido em inspect")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp, err := s.loadCheckpointLocked(id)
	if err != nil {
		return Checkpoint{}, err
	}
	current, _, err := fileHash(cp.Path)
	if err != nil {
		return Checkpoint{}, err
	}
	if current != expectedCurrentSHA256 {
		return Checkpoint{}, errors.New("arquivo mudou apos a inspecao; execute inspect novamente")
	}
	backup, err := os.ReadFile(filepath.Join(s.checkpointDir, id+".bin"))
	if err != nil {
		return Checkpoint{}, fmt.Errorf("lendo backup: %w", err)
	}
	sum := sha256.Sum256(backup)
	if int64(len(backup)) != cp.Size || hex.EncodeToString(sum[:]) != cp.SHA256 {
		return Checkpoint{}, errors.New("backup de checkpoint corrompido")
	}
	if err := s.appendLocked(ctx, Event{Kind: "checkpoint_restore", Status: "started", CheckpointID: id, Path: cp.Path}); err != nil {
		return Checkpoint{}, fmt.Errorf("auditoria indisponivel; restauracao nao iniciada: %w", err)
	}
	if err := writeAtomic(cp.Path, backup, os.FileMode(cp.Mode)&0o777); err != nil {
		_ = s.appendLocked(context.Background(), Event{Kind: "checkpoint_restore", Status: "failed", CheckpointID: id, Path: cp.Path})
		return Checkpoint{}, fmt.Errorf("restaurando arquivo: %w", err)
	}
	if err := s.appendLocked(context.Background(), Event{Kind: "checkpoint_restore", Status: "completed", CheckpointID: id, Path: cp.Path}); err != nil {
		return cp, fmt.Errorf("arquivo restaurado; registro final de auditoria falhou: %w", err)
	}
	return cp, nil
}

func (s *Store) loadCheckpointLocked(id string) (Checkpoint, error) {
	if !checkpointIDPattern.MatchString(id) {
		return Checkpoint{}, errors.New("ID de checkpoint invalido")
	}
	path := filepath.Join(s.checkpointDir, id+".json")
	info, err := os.Lstat(path)
	if err != nil {
		return Checkpoint{}, err
	}
	if !info.Mode().IsRegular() {
		return Checkpoint{}, errors.New("metadados de checkpoint irregulares")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Checkpoint{}, err
	}
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return Checkpoint{}, err
	}
	if cp.ID != id || !filepath.IsAbs(cp.Path) || cp.Size < 0 || cp.Size > maxCheckpointBytes ||
		len(cp.SHA256) != 64 || cp.Mode&^uint32(0o777) != 0 {
		return Checkpoint{}, errors.New("metadados de checkpoint invalidos")
	}
	return cp, nil
}

func fileHash(path string) (string, int64, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "missing", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, errors.New("destino atual nao e arquivo regular")
	}
	if info.Size() > maxCheckpointBytes {
		return "", 0, fmt.Errorf("destino atual excede %d MiB", maxCheckpointBytes>>20)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".polypod-audit-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
