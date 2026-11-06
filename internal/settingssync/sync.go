// Package settingssync syncs user settings (preferences, keybindings, themes)
// across devices via a local file store with optional remote synchronisation.
//
// Ported from src/services/settingsSync (TypeScript).
package settingssync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

// SyncConfig holds the parameters needed to create a Store.
type SyncConfig struct {
	StorePath string       // Local file path for the settings state.
	RemoteURL string       // Optional HTTP(S) endpoint for push/pull sync.
	AuthToken string       // Bearer token for the remote endpoint.
	Logger    *slog.Logger // If nil, a no-op logger is used.
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

// SettingsState is the on-disk / on-wire representation of a settings snapshot.
type SettingsState struct {
	Version      int            `json:"version"`
	Settings     map[string]any `json:"settings"`
	LastModified time.Time      `json:"lastModified"`
	Checksum     string         `json:"checksum"`
}

// computeChecksum returns the SHA-256 hex digest of the JSON-encoded Settings map.
func computeChecksum(settings map[string]any) (string, error) {
	data, err := json.Marshal(settings)
	if err != nil {
		return "", fmt.Errorf("marshal settings for checksum: %w", err)
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}

// ---------------------------------------------------------------------------
// Store
// ---------------------------------------------------------------------------

// Store is a local file-based settings store with optional remote sync.
type Store struct {
	cfg SyncConfig
	log *slog.Logger
	mu  sync.Mutex // protects file operations
}

// NewStore creates a new Store from the given config.
func NewStore(cfg SyncConfig) *Store {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Store{
		cfg: cfg,
		log: logger,
	}
}

// ---------------------------------------------------------------------------
// Load / Save
// ---------------------------------------------------------------------------

// Load reads the settings state from StorePath. If the file does not exist it
// returns a zero-value state with an empty Settings map (no error).
func (s *Store) Load() (*SettingsState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.cfg.StorePath)
	if err != nil {
		if os.IsNotExist(err) {
			s.log.Info("settings file not found, returning empty state", "path", s.cfg.StorePath)
			return &SettingsState{
				Settings:     make(map[string]any),
				LastModified: time.Now(),
			}, nil
		}
		return nil, fmt.Errorf("read settings file: %w", err)
	}

	var state SettingsState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode settings file: %w", err)
	}
	if state.Settings == nil {
		state.Settings = make(map[string]any)
	}
	return &state, nil
}

// Save writes the state to StorePath atomically (write to .tmp then rename).
// It recomputes the Checksum before writing.
func (s *Store) Save(state *SettingsState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Recompute checksum to keep it consistent.
	cs, err := computeChecksum(state.Settings)
	if err != nil {
		return err
	}
	state.Checksum = cs
	state.LastModified = time.Now()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings state: %w", err)
	}

	dir := filepath.Dir(s.cfg.StorePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create settings dir: %w", err)
	}

	tmp := s.cfg.StorePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write temp settings file: %w", err)
	}
	if err := os.Rename(tmp, s.cfg.StorePath); err != nil {
		// Best-effort cleanup of the temp file.
		os.Remove(tmp)
		return fmt.Errorf("rename temp settings file: %w", err)
	}

	s.log.Info("settings saved", "path", s.cfg.StorePath, "version", state.Version)
	return nil
}

// ---------------------------------------------------------------------------
// Merge
// ---------------------------------------------------------------------------

// Merge combines two states. The state with the higher Version wins wholesale.
// If versions are equal, the one with the later LastModified wins. If both are
// identical, local is returned.
func (s *Store) Merge(local, remote *SettingsState) *SettingsState {
	if remote.Version > local.Version {
		s.log.Info("merge: remote wins", "localVer", local.Version, "remoteVer", remote.Version)
		return remote
	}
	if local.Version > remote.Version {
		s.log.Info("merge: local wins", "localVer", local.Version, "remoteVer", remote.Version)
		return local
	}
	// Same version — prefer the more recent modification.
	if remote.LastModified.After(local.LastModified) {
		s.log.Info("merge: same version, remote more recent")
		return remote
	}
	s.log.Info("merge: same version, local wins or equal")
	return local
}

// ---------------------------------------------------------------------------
// Remote sync
// ---------------------------------------------------------------------------

const (
	syncTimeout       = 10 * time.Second
	remoteContentType = "application/json"
)

// SyncRemote pushes the local state to and pulls the remote state from
// cfg.RemoteURL, merging as needed. It is a no-op when RemoteURL is empty.
func (s *Store) SyncRemote(ctx context.Context) error {
	if s.cfg.RemoteURL == "" {
		s.log.Debug("sync skipped: no remote URL configured")
		return nil
	}

	local, err := s.Load()
	if err != nil {
		return fmt.Errorf("load local state for sync: %w", err)
	}

	remote, err := s.pull(ctx)
	if err != nil {
		s.log.Warn("pull from remote failed, continuing with local", "err", err)
		// Fail-open: continue with local-only state.
		return s.push(ctx, local)
	}

	merged := s.Merge(local, remote)
	if err := s.Save(merged); err != nil {
		return fmt.Errorf("save merged state: %w", err)
	}

	return s.push(ctx, merged)
}

// pull fetches the remote settings state. Returns a zero state on 404.
func (s *Store) pull(ctx context.Context) (*SettingsState, error) {
	ctx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.RemoteURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build pull request: %w", err)
	}
	s.setAuthHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pull request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		s.log.Info("remote settings not found (404), treating as empty")
		return &SettingsState{
			Settings:     make(map[string]any),
			LastModified: time.Time{},
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pull: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read pull response: %w", err)
	}

	var state SettingsState
	if err := json.Unmarshal(body, &state); err != nil {
		return nil, fmt.Errorf("decode pull response: %w", err)
	}
	if state.Settings == nil {
		state.Settings = make(map[string]any)
	}
	return &state, nil
}

// push sends the local state to the remote endpoint.
func (s *Store) push(ctx context.Context, state *SettingsState) error {
	ctx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()

	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode state for push: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.cfg.RemoteURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("build push request: %w", err)
	}
	req.Header.Set("Content-Type", remoteContentType)
	s.setAuthHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("push request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("push: unexpected status %d", resp.StatusCode)
	}

	s.log.Info("settings pushed to remote", "version", state.Version)
	return nil
}

func (s *Store) setAuthHeaders(req *http.Request) {
	if s.cfg.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.AuthToken)
	}
}

// ---------------------------------------------------------------------------
// Watch
// ---------------------------------------------------------------------------

const defaultWatchInterval = 2 * time.Second

// Watch polls StorePath for changes and invokes onChange when the file is
// modified. It blocks until ctx is cancelled.
func (s *Store) Watch(ctx context.Context, onChange func(*SettingsState)) {
	var lastMod time.Time

	// Seed with the current mod-time so we don't fire immediately.
	if info, err := os.Stat(s.cfg.StorePath); err == nil {
		lastMod = info.ModTime()
	}

	s.log.Info("watching settings file", "path", s.cfg.StorePath)

	ticker := time.NewTicker(defaultWatchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := os.Stat(s.cfg.StorePath)
			if err != nil {
				continue
			}
			if info.ModTime().After(lastMod) {
				lastMod = info.ModTime()
				state, err := s.Load()
				if err != nil {
					s.log.Warn("failed to load settings after change", "err", err)
					continue
				}
				s.log.Info("settings file changed", "version", state.Version)
				onChange(state)
			}
		}
	}
}
