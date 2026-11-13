// Package workdir keeps a tool working directory per conversation without
// changing the process working directory shared by other channels.
package workdir

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	ErrScopeUnavailable = errors.New("escopo da conversa indisponível")
	ErrAlreadyEntered   = errors.New("a conversa já está em uma worktree")
	ErrNotEntered       = errors.New("a conversa não está em uma worktree")
)

type Entry struct {
	Dir        string `json:"dir"`
	Previous   string `json:"previous"`
	BaseBranch string `json:"base_branch,omitempty"`
	BasePinned bool   `json:"base_pinned,omitempty"`
}

type storeFile struct {
	Version int              `json:"version"`
	Entries map[string]Entry `json:"entries"`
}

// Store persists conversation-specific directories. Its mutex serializes
// updates and atomic file replacement so concurrent channels cannot overwrite
// each other's state.
type Store struct {
	mu      sync.Mutex
	path    string
	entries map[string]Entry
}

func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolvendo diretórios de trabalho: %w", err)
	}
	s := &Store{path: abs, entries: make(map[string]Entry)}
	data, err := os.ReadFile(abs)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lendo diretórios de trabalho: %w", err)
	}
	var saved storeFile
	if err := json.Unmarshal(data, &saved); err != nil || saved.Version != 1 {
		return nil, fmt.Errorf("arquivo de diretórios de trabalho inválido")
	}
	if saved.Entries != nil {
		s.entries = saved.Entries
	}
	if err := os.Chmod(abs, 0o600); err != nil {
		return nil, fmt.Errorf("protegendo diretórios de trabalho: %w", err)
	}
	if err := inheritSharedOwner(abs, abs); err != nil {
		return nil, fmt.Errorf("ajustando dono dos diretórios de trabalho: %w", err)
	}
	return s, nil
}

type scopeKey struct{}
type fixedDirKey struct{}

// WithFixedDir binds an ephemeral directory override to ctx (for example, an
// isolated temporary git worktree in Best-of-N arena runs) without mutating
// persistent session workdir entries.
func WithFixedDir(ctx context.Context, dir string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if dir == "" {
		return ctx
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return context.WithValue(ctx, fixedDirKey{}, dir)
}

type scope struct {
	store *Store
	key   string
}

// Bound reports whether this tool call belongs to a persisted conversation.
func Bound(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	_, ok := ctx.Value(scopeKey{}).(scope)
	return ok
}

// WithScope binds the same workspace to all tools in one conversation. The
// session ID separates CLI branches while channel and user separate gateways.
func WithScope(ctx context.Context, store *Store, channel, user, sessionID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if store == nil || channel == "" || user == "" {
		return ctx
	}
	sum := sha256.Sum256([]byte(channel + "\x00" + user + "\x00" + sessionID))
	return context.WithValue(ctx, scopeKey{}, scope{store: store, key: hex.EncodeToString(sum[:])})
}

func Current(ctx context.Context) (Entry, bool) {
	if ctx == nil {
		return Entry{}, false
	}
	sc, ok := ctx.Value(scopeKey{}).(scope)
	if !ok {
		return Entry{}, false
	}
	sc.store.mu.Lock()
	defer sc.store.mu.Unlock()
	// Atomic replacement lets concurrent processes refresh without taking the
	// write lock. Keep the last valid snapshot if a read fails.
	if err := sc.store.reloadLocked(); err != nil {
		return Entry{}, false
	}
	entry, found := sc.store.entries[sc.key]
	return entry, found
}

func Dir(ctx context.Context, fallback string) string {
	if ctx != nil {
		if fixed, ok := ctx.Value(fixedDirKey{}).(string); ok && fixed != "" {
			return fixed
		}
	}
	if entry, ok := Current(ctx); ok {
		if info, err := os.Stat(entry.Dir); os.IsNotExist(err) || err == nil && !info.IsDir() {
			return entry.Previous
		}
		return entry.Dir
	}
	return fallback
}

// SetBase pins an existing project directory to a conversation. It is used by
// protocol clients that supply a workspace when they create a session.
func SetBase(ctx context.Context, dir string) error {
	if ctx == nil {
		return ErrScopeUnavailable
	}
	sc, ok := ctx.Value(scopeKey{}).(scope)
	if !ok {
		return ErrScopeUnavailable
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("diretório de trabalho indisponível: %s", abs)
	}
	s := sc.store
	s.mu.Lock()
	defer s.mu.Unlock()
	return withProcessLock(s.path+".lock", func() error {
		if err := s.reloadLocked(); err != nil {
			return err
		}
		if existing, found := s.entries[sc.key]; found {
			if existing.BasePinned && existing.Previous == abs {
				return nil
			}
			return fmt.Errorf("diretório da sessão já definido")
		}
		s.entries[sc.key] = Entry{Dir: abs, Previous: abs, BasePinned: true}
		if err := s.saveLocked(); err != nil {
			delete(s.entries, sc.key)
			return err
		}
		return nil
	})
}

// Enter records a newly created worktree after git has added it. A caller
// must remove the worktree if this operation fails.
func Enter(ctx context.Context, dir, previous, baseBranch string) error {
	if ctx == nil {
		return ErrScopeUnavailable
	}
	sc, ok := ctx.Value(scopeKey{}).(scope)
	if !ok {
		return ErrScopeUnavailable
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	previous, err = filepath.Abs(previous)
	if err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("worktree não encontrada: %s", dir)
	}
	s := sc.store
	s.mu.Lock()
	defer s.mu.Unlock()
	return withProcessLock(s.path+".lock", func() error {
		if err := s.reloadLocked(); err != nil {
			return err
		}
		current, exists := s.entries[sc.key]
		if exists && (!current.BasePinned || current.Dir != current.Previous || current.Dir != previous) {
			return ErrAlreadyEntered
		}
		s.entries[sc.key] = Entry{Dir: dir, Previous: previous, BaseBranch: baseBranch, BasePinned: current.BasePinned}
		if err := s.saveLocked(); err != nil {
			if exists {
				s.entries[sc.key] = current
			} else {
				delete(s.entries, sc.key)
			}
			return err
		}
		return nil
	})
}

// Exit restores the previous directory only for the currently active
// worktree. The caller removes the Git worktree before clearing this state.
func Exit(ctx context.Context, expectedDir string) (string, error) {
	if ctx == nil {
		return "", ErrScopeUnavailable
	}
	sc, ok := ctx.Value(scopeKey{}).(scope)
	if !ok {
		return "", ErrScopeUnavailable
	}
	expectedDir, err := filepath.Abs(expectedDir)
	if err != nil {
		return "", err
	}
	s := sc.store
	s.mu.Lock()
	defer s.mu.Unlock()
	var previous string
	err = withProcessLock(s.path+".lock", func() error {
		if err := s.reloadLocked(); err != nil {
			return err
		}
		entry, exists := s.entries[sc.key]
		if !exists || entry.Dir != expectedDir || entry.BasePinned && entry.Dir == entry.Previous {
			return ErrNotEntered
		}
		if entry.BasePinned {
			s.entries[sc.key] = Entry{Dir: entry.Previous, Previous: entry.Previous, BasePinned: true}
		} else {
			delete(s.entries, sc.key)
		}
		if err := s.saveLocked(); err != nil {
			s.entries[sc.key] = entry
			return err
		}
		previous = entry.Previous
		return nil
	})
	return previous, err
}

func (s *Store) reloadLocked() error {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		s.entries = make(map[string]Entry)
		return nil
	}
	if err != nil {
		return fmt.Errorf("lendo estado de diretórios de trabalho: %w", err)
	}
	var saved storeFile
	if err := json.Unmarshal(data, &saved); err != nil || saved.Version != 1 {
		return fmt.Errorf("estado de diretórios de trabalho inválido")
	}
	if saved.Entries == nil {
		saved.Entries = make(map[string]Entry)
	}
	s.entries = saved.Entries
	return nil
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".workdirs-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if err := inheritSharedOwner(f.Name(), s.path); err != nil {
		f.Close()
		return err
	}
	if err := json.NewEncoder(f).Encode(storeFile{Version: 1, Entries: s.entries}); err != nil {
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
	if err := os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
