package session

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// SessionKind describes the type of session.
type SessionKind string

const (
	KindInteractive SessionKind = "interactive"
	KindBackground  SessionKind = "bg"
	KindDaemon      SessionKind = "daemon"
)

// ConcurrentSessionStatus describes the live activity state.
type ConcurrentSessionStatus string

const (
	PeerStatusBusy    ConcurrentSessionStatus = "busy"
	PeerStatusIdle    ConcurrentSessionStatus = "idle"
	PeerStatusWaiting ConcurrentSessionStatus = "waiting"
)

// PeerSession describes another running polypod instance.
type PeerSession struct {
	PID        int                     `json:"pid"`
	SessionID  string                  `json:"session_id"`
	Cwd        string                  `json:"cwd"`
	StartedAt  int64                   `json:"started_at"`
	Kind       SessionKind             `json:"kind"`
	Name       string                  `json:"name,omitempty"`
	Status     ConcurrentSessionStatus `json:"status,omitempty"`
	WaitingFor string                  `json:"waiting_for,omitempty"`
	UpdatedAt  int64                   `json:"updated_at,omitempty"`
	Entrypoint string                  `json:"entrypoint,omitempty"`
}

// ConcurrentSessionManager manages registration and discovery of running
// polypod instances using PID files in the config directory.
type ConcurrentSessionManager struct {
	mu       sync.Mutex
	sessDir  string
	pid      int
	pidFile  string
	logger   *slog.Logger
	onSwitch []func(string) // sessionID switch callbacks
}

// NewConcurrentSessionManager creates a manager. Call Register() to write
// this process's PID file.
func NewConcurrentSessionManager(configDir string, logger *slog.Logger) *ConcurrentSessionManager {
	if logger == nil {
		logger = slog.Default()
	}
	dir := filepath.Join(configDir, "sessions")
	return &ConcurrentSessionManager{
		sessDir: dir,
		pid:     os.Getpid(),
		logger:  logger,
	}
}

// Register writes a PID file for this process and returns a cleanup function
// that removes it. Call cleanup on shutdown.
func (m *ConcurrentSessionManager) Register(sessionID, cwd string, kind SessionKind) (cleanup func(), err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := os.MkdirAll(m.sessDir, 0700); err != nil {
		return nil, fmt.Errorf("creating sessions dir: %w", err)
	}

	m.pidFile = filepath.Join(m.sessDir, fmt.Sprintf("%d.json", m.pid))

	peer := PeerSession{
		PID:       m.pid,
		SessionID: sessionID,
		Cwd:       cwd,
		StartedAt: time.Now().UnixMilli(),
		Kind:      kind,
	}

	if err := m.writePeerFile(peer); err != nil {
		return nil, err
	}

	return func() {
		os.Remove(m.pidFile)
	}, nil
}

// UpdateSessionID updates the PID file when the session ID changes
// (e.g. after --resume).
func (m *ConcurrentSessionManager) UpdateSessionID(sessionID string) error {
	m.mu.Lock()
	err := m.patchPidFile(map[string]interface{}{"session_id": sessionID})
	callbacks := append([]func(string){}, m.onSwitch...)
	m.mu.Unlock()
	if err != nil {
		return err
	}
	for _, callback := range callbacks {
		callback(sessionID)
	}
	return nil
}

// UpdateName updates the session name in the PID file.
func (m *ConcurrentSessionManager) UpdateName(name string) error {
	if name == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.patchPidFile(map[string]interface{}{"name": name})
}

// UpdateActivity pushes live status into the PID file for `polypod ps`.
func (m *ConcurrentSessionManager) UpdateActivity(status ConcurrentSessionStatus, waitingFor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.patchPidFile(map[string]interface{}{
		"status":      status,
		"waiting_for": waitingFor,
		"updated_at":  time.Now().UnixMilli(),
	})
}

// LockSession attempts to acquire an advisory file lock for a session.
// Returns a release function, or an error if the session is already locked.
func (m *ConcurrentSessionManager) LockSession(sessionID string) (release func(), err error) {
	lockPath := filepath.Join(m.sessDir, sessionID+".lock")

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening lock file: %w", err)
	}

	// Non-blocking exclusive lock.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("session %s is locked by another process", sessionID)
	}

	// Write our PID for diagnostics.
	fmt.Fprintf(f, "%d\n", m.pid)

	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		os.Remove(lockPath)
	}, nil
}

// UnlockSession is a convenience alias — the preferred pattern is to call
// the release function returned by LockSession.
func (m *ConcurrentSessionManager) UnlockSession(sessionID string) {
	lockPath := filepath.Join(m.sessDir, sessionID+".lock")
	os.Remove(lockPath)
}

// DetectOtherSessions returns all live peer sessions (excluding this process).
// Stale PID files (process no longer running) are cleaned up.
func (m *ConcurrentSessionManager) DetectOtherSessions() ([]PeerSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entries, err := os.ReadDir(m.sessDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading sessions dir: %w", err)
	}

	var peers []PeerSession
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		// Only consider <pid>.json files.
		pidStr := strings.TrimSuffix(name, ".json")
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}
		if pid == m.pid {
			continue
		}

		path := filepath.Join(m.sessDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var peer PeerSession
		if err := json.Unmarshal(data, &peer); err != nil {
			continue
		}

		if isProcessRunning(pid) {
			peers = append(peers, peer)
		} else {
			// Stale PID file — clean up.
			os.Remove(path)
			m.logger.Debug("cleaned up stale session PID file", "pid", pid)
		}
	}

	return peers, nil
}

// CountConcurrentSessions returns the number of live sessions (including this one).
func (m *ConcurrentSessionManager) CountConcurrentSessions() (int, error) {
	peers, err := m.DetectOtherSessions()
	if err != nil {
		return 0, err
	}
	return len(peers) + 1, nil // +1 for this process
}

// SessionConflictResolution decides what to do when another session is
// operating on the same project. Returns a human-readable warning if there
// is a conflict, empty string otherwise.
func (m *ConcurrentSessionManager) SessionConflictResolution(projectRoot string) string {
	peers, err := m.DetectOtherSessions()
	if err != nil {
		return ""
	}

	var conflicts []PeerSession
	for _, p := range peers {
		if p.Cwd == projectRoot {
			conflicts = append(conflicts, p)
		}
	}
	if len(conflicts) == 0 {
		return ""
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "WARNING: %d other session(s) running in this project:\n", len(conflicts))
	for _, p := range conflicts {
		started := time.UnixMilli(p.StartedAt).Format("15:04:05")
		fmt.Fprintf(&sb, "  - PID %d (%s, started %s)", p.PID, p.Kind, started)
		if p.Name != "" {
			fmt.Fprintf(&sb, " [%s]", p.Name)
		}
		sb.WriteString("\n")
	}
	sb.WriteString("Concurrent edits to the same files may cause conflicts.\n")
	return sb.String()
}

// OnSessionSwitch registers a callback that fires when UpdateSessionID is called.
func (m *ConcurrentSessionManager) OnSessionSwitch(fn func(string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onSwitch = append(m.onSwitch, fn)
}

// --- internal helpers ---

func (m *ConcurrentSessionManager) writePeerFile(peer PeerSession) error {
	data, err := json.Marshal(peer)
	if err != nil {
		return fmt.Errorf("marshaling peer data: %w", err)
	}
	return m.writePeerFileAtomic(data)
}

func (m *ConcurrentSessionManager) patchPidFile(patch map[string]interface{}) error {
	if m.pidFile == "" {
		return nil
	}
	data, err := os.ReadFile(m.pidFile)
	if err != nil {
		return fmt.Errorf("reading PID file: %w", err)
	}
	var existing map[string]interface{}
	if err := json.Unmarshal(data, &existing); err != nil {
		return fmt.Errorf("parsing PID file: %w", err)
	}
	for k, v := range patch {
		existing[k] = v
	}
	out, err := json.Marshal(existing)
	if err != nil {
		return fmt.Errorf("marshaling PID file: %w", err)
	}
	return m.writePeerFileAtomic(out)
}

func (m *ConcurrentSessionManager) writePeerFileAtomic(data []byte) error {
	tmp, err := os.CreateTemp(m.sessDir, ".session-peer-*")
	if err != nil {
		return fmt.Errorf("creating temporary session record: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), m.pidFile); err != nil {
		return fmt.Errorf("publishing session record: %w", err)
	}
	return nil
}

// isProcessRunning checks if a process with the given PID exists.
func isProcessRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds. Send signal 0 to check.
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}
