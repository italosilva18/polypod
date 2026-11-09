package sessionmem

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	maxStoredEntries = 64
	maxStoredBytes   = 1 << 20
	maxEntryValue    = 600
)

// FileStore keeps a small, private memory for each persisted conversation ID.
// Branches use different IDs, so their later decisions cannot overwrite one
// another. Files are addressed by a hash; the raw ID is never a path component.
type FileStore struct {
	mu  sync.Mutex
	dir string
}

func NewFileStore(dataDir string) (*FileStore, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("sessionmem: diretorio de dados vazio")
	}
	dir := filepath.Join(dataDir, "session-memory.d")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("sessionmem: criando diretorio: %w", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, fmt.Errorf("sessionmem: protegendo diretorio: %w", err)
	}
	return &FileStore{dir: dir}, nil
}

func (s *FileStore) path(scope string) (string, error) {
	if strings.TrimSpace(scope) == "" || len(scope) > 1024 {
		return "", errors.New("sessionmem: ID da sessao invalido")
	}
	digest := sha256.Sum256([]byte(scope))
	return filepath.Join(s.dir, hex.EncodeToString(digest[:])+".json"), nil
}

func (s *FileStore) loadLocked(scope string) ([]Entry, error) {
	path, err := s.path(scope)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sessionmem: abrindo sessao: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxStoredBytes+1))
	closeErr := file.Close()
	if err != nil {
		return nil, fmt.Errorf("sessionmem: lendo sessao: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("sessionmem: fechando sessao: %w", closeErr)
	}
	if len(data) > maxStoredBytes {
		return nil, errors.New("sessionmem: arquivo excede o limite")
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("sessionmem: decodificando sessao: %w", err)
	}
	if len(entries) > maxStoredEntries {
		entries = entries[len(entries)-maxStoredEntries:]
	}
	return entries, nil
}

func (s *FileStore) Load(scope string) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.loadLocked(scope)
	if err != nil {
		return nil, err
	}
	return append([]Entry(nil), entries...), nil
}

// Merge writes validated entries. Repeated statements share a stable key;
// different decisions remain separate until a later reconciliation step.
func (s *FileStore) Merge(scope string, additions []Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.loadLocked(scope)
	if err != nil {
		return err
	}
	for _, raw := range additions {
		entry, ok := cleanEntry(raw)
		if !ok {
			continue
		}
		for i := range current {
			if current[i].Key == entry.Key {
				current = append(current[:i], current[i+1:]...)
				break
			}
		}
		if entry.CreatedAt.IsZero() {
			entry.CreatedAt = time.Now()
		}
		current = append(current, entry)
	}
	if len(current) > maxStoredEntries {
		current = current[len(current)-maxStoredEntries:]
	}
	return s.writeLocked(scope, current)
}

// Fork copies a branch's memory snapshot. Later updates remain independent.
func (s *FileStore) Fork(parent, child string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if parent == child {
		return nil
	}
	entries, err := s.loadLocked(parent)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	return s.writeLocked(child, entries)
}

// Delete removes the memory snapshot of a branch that was never published.
// It is also safe when the branch has no memory file yet.
func (s *FileStore) Delete(scope string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path(scope)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("sessionmem: removendo sessao: %w", err)
	}
	return nil
}

func (s *FileStore) writeLocked(scope string, entries []Entry) error {
	path, err := s.path(scope)
	if err != nil {
		return err
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("sessionmem: codificando memoria: %w", err)
	}
	if len(data) > maxStoredBytes {
		return errors.New("sessionmem: memoria excede o limite")
	}
	file, err := os.CreateTemp(s.dir, ".memory-*")
	if err != nil {
		return fmt.Errorf("sessionmem: criando arquivo temporario: %w", err)
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("sessionmem: publicando memoria: %w", err)
	}
	dir, err := os.Open(s.dir)
	if err != nil {
		return fmt.Errorf("sessionmem: abrindo diretorio para sincronizacao: %w", err)
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil {
		return fmt.Errorf("sessionmem: sincronizando diretorio: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("sessionmem: fechando diretorio: %w", closeErr)
	}
	return nil
}
