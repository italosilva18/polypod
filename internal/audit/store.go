// Package audit keeps a private, durable trail of tool effects and file checkpoints.
package audit

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Event contains operation metadata only. Tool arguments, responses and file
// contents are deliberately omitted because they may contain credentials.
type Event struct {
	Sequence     uint64    `json:"sequence"`
	At           time.Time `json:"at"`
	Kind         string    `json:"kind"`
	Tool         string    `json:"tool,omitempty"`
	Status       string    `json:"status,omitempty"`
	SessionID    string    `json:"session_id,omitempty"`
	AgentID      string    `json:"agent_id,omitempty"`
	DurationMS   int64     `json:"duration_ms,omitempty"`
	CheckpointID string    `json:"checkpoint_id,omitempty"`
	Path         string    `json:"path,omitempty"`
	PreviousHash string    `json:"previous_hash,omitempty"`
	Hash         string    `json:"hash"`
}

var ErrCorruptLog = errors.New("trilha de auditoria corrompida")

// Store is owned by one process. Its mutex serializes appends and reads; the
// hash chain detects accidental changes but is not a cryptographic signature.
type Store struct {
	mu            sync.Mutex
	dir           string
	logPath       string
	checkpointDir string
	sequence      uint64
	lastHash      string
}

// Open creates or validates a private audit directory and its existing trail.
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("diretorio de auditoria obrigatorio")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("criando auditoria: %w", err)
	}
	if info, err := os.Lstat(abs); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("diretorio de auditoria invalido ou link simbolico")
	}
	if err := os.Chmod(abs, 0o700); err != nil {
		return nil, fmt.Errorf("protegendo auditoria: %w", err)
	}
	cpDir := filepath.Join(abs, "checkpoints")
	if err := os.MkdirAll(cpDir, 0o700); err != nil {
		return nil, fmt.Errorf("criando checkpoints: %w", err)
	}
	if info, err := os.Lstat(cpDir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("diretorio de checkpoints invalido ou link simbolico")
	}
	if err := os.Chmod(cpDir, 0o700); err != nil {
		return nil, fmt.Errorf("protegendo checkpoints: %w", err)
	}
	s := &Store{dir: abs, logPath: filepath.Join(abs, "events.jsonl"), checkpointDir: cpDir}
	if err := s.scan(context.Background(), 0, nil); err != nil {
		return nil, err
	}
	return s, nil
}

// Append records one operation durably before returning. Callers should send
// only metadata and never put raw tool inputs or outputs into an Event.
func (s *Store) Append(ctx context.Context, e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendLocked(ctx, e)
}

func (s *Store) appendLocked(ctx context.Context, e Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.Kind == "" {
		return errors.New("tipo de evento obrigatorio")
	}
	e.Sequence = s.sequence + 1
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	e.PreviousHash = s.lastHash
	e.Hash = eventHash(e)
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if info, err := os.Lstat(s.logPath); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: arquivo de eventos irregular", ErrCorruptLog)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(s.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("abrindo trilha de auditoria: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return fmt.Errorf("protegendo trilha de auditoria: %w", err)
	}
	before, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if n, err := f.Write(line); err != nil || n != len(line) {
		_ = f.Truncate(before.Size())
		_ = f.Close()
		if err == nil {
			err = errors.New("gravacao incompleta")
		}
		return fmt.Errorf("gravando auditoria: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Truncate(before.Size())
		_ = f.Close()
		return fmt.Errorf("sincronizando auditoria: %w", err)
	}
	s.sequence, s.lastHash = e.Sequence, e.Hash
	if err := f.Close(); err != nil {
		return fmt.Errorf("fechando auditoria: %w", err)
	}
	return nil
}

// Recent returns the last events, in chronological order, after verifying the
// entire chain. A corrupt trail is reported rather than silently skipped.
func (s *Store) Recent(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Event, 0, limit)
	if err := s.scan(ctx, limit, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) scan(ctx context.Context, limit int, result *[]Event) error {
	if info, err := os.Lstat(s.logPath); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: arquivo de eventos irregular", ErrCorruptLog)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.Open(s.logPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lendo auditoria: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: arquivo irregular", ErrCorruptLog)
	}
	if info.Size() > 0 {
		var end [1]byte
		if _, err := f.ReadAt(end[:], info.Size()-1); err != nil {
			return fmt.Errorf("lendo final da auditoria: %w", err)
		}
		if end[0] != '\n' {
			return fmt.Errorf("%w: linha incompleta", ErrCorruptLog)
		}
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var sequence uint64
	var previous string
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var e Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			return fmt.Errorf("%w: linha %d: %v", ErrCorruptLog, sequence+1, err)
		}
		if e.Sequence != sequence+1 || e.PreviousHash != previous || e.Hash == "" || e.Hash != eventHash(e) {
			return fmt.Errorf("%w: linha %d", ErrCorruptLog, sequence+1)
		}
		sequence, previous = e.Sequence, e.Hash
		if result != nil {
			if len(*result) == limit {
				copy((*result)[0:], (*result)[1:])
				(*result)[limit-1] = e
			} else {
				*result = append(*result, e)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("lendo auditoria: %w", err)
	}
	s.sequence, s.lastHash = sequence, previous
	return nil
}

func eventHash(e Event) string {
	e.Hash = ""
	raw, _ := json.Marshal(e)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
