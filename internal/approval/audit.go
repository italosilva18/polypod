package approval

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// -------------------------------------------------------------------
// AuditEntry / AuditLogger — append-only trail for approval decisions
// -------------------------------------------------------------------

// AuditEntry captures a single approval decision.
//
// Timestamps are recorded in UTC at the moment the decision is made.
// Input is whatever argument map was attached to the ApprovalRequest;
// callers should be mindful that it may contain sensitive data.
type AuditEntry struct {
	Timestamp time.Time      `json:"timestamp"`
	ToolName  string         `json:"tool_name"`
	RiskLevel string         `json:"risk_level"`
	Decision  string         `json:"decision"`
	Reason    string         `json:"reason,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
}

// AuditLogger is the interface for any audit sink. Implementations MUST
// be safe for concurrent use by multiple goroutines.
type AuditLogger interface {
	Log(entry AuditEntry)
}

// -------------------------------------------------------------------
// FileAuditLogger — JSON-lines to a local file, mutex-guarded
// -------------------------------------------------------------------

// FileAuditLogger appends one JSON object per line to a local file.
// The file is opened with O_CREATE|O_APPEND|O_WRONLY and chmod 0600
// so only the owner may read the audit trail.
type FileAuditLogger struct {
	mu   sync.Mutex
	path string
	f    *os.File
}

// NewFileAuditLogger opens (or creates) the audit file for appending.
//
// Any intermediate directories are created with 0700 permissions.
func NewFileAuditLogger(path string) (*FileAuditLogger, error) {
	if path == "" {
		return nil, fmt.Errorf("audit log path must not be empty")
	}
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("creating audit dir %q: %w", dir, err)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening audit file %q: %w", path, err)
	}
	return &FileAuditLogger{path: path, f: f}, nil
}

// Log appends the entry as a JSON line. Errors are surfaced via slog
// (not returned) because Log is called on security-sensitive paths
// where we do not want to propagate I/O failures into the approval
// decision — we prefer to deny-and-warn instead of crash.
func (l *FileAuditLogger) Log(entry AuditEntry) {
	if l == nil || l.f == nil {
		return
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}
	data, err := json.Marshal(entry)
	if err != nil {
		slog.Error("approval.audit: marshal failed",
			"tool", entry.ToolName,
			"err", err)
		return
	}
	data = append(data, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.f.Write(data); err != nil {
		slog.Error("approval.audit: write failed",
			"path", l.path,
			"tool", entry.ToolName,
			"err", err)
	}
}

// Close releases the underlying file handle. Subsequent Log calls
// become no-ops.
func (l *FileAuditLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// Path returns the on-disk location of the audit file.
func (l *FileAuditLogger) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// -------------------------------------------------------------------
// NoopAuditLogger — the safe default for tests and no-config binaries
// -------------------------------------------------------------------

// NoopAuditLogger discards every entry. It satisfies AuditLogger so
// that callers can always invoke CurrentAuditLogger().Log(...) without
// a nil check.
type NoopAuditLogger struct{}

// Log is a no-op.
func (NoopAuditLogger) Log(AuditEntry) {}

// -------------------------------------------------------------------
// DefaultAuditLogPath — XDG-aware default location
// -------------------------------------------------------------------

// DefaultAuditLogPath returns the preferred audit file path.
//
// Resolution order:
//  1. $XDG_STATE_HOME/polypod/approval-audit.jsonl
//  2. $HOME/.polypod/approval-audit.jsonl
//  3. ./approval-audit.jsonl (fallback when no home is resolvable)
func DefaultAuditLogPath() string {
	const file = "approval-audit.jsonl"
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "polypod", file)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".polypod", file)
	}
	return file
}

// -------------------------------------------------------------------
// Global audit logger — lock-free reads via atomic.Value
// -------------------------------------------------------------------

// auditLoggerHolder wraps the interface so atomic.Value always sees a
// consistent concrete type.
type auditLoggerHolder struct {
	logger AuditLogger
}

var currentAuditLogger atomic.Value

func init() {
	currentAuditLogger.Store(auditLoggerHolder{logger: NoopAuditLogger{}})
}

// SetAuditLogger installs l as the process-wide audit sink. Passing
// nil resets to the no-op logger (secure default — never leaves the
// global in a nil state).
func SetAuditLogger(l AuditLogger) {
	if l == nil {
		l = NoopAuditLogger{}
	}
	currentAuditLogger.Store(auditLoggerHolder{logger: l})
}

// CurrentAuditLogger returns the active logger. Guaranteed non-nil.
func CurrentAuditLogger() AuditLogger {
	h, ok := currentAuditLogger.Load().(auditLoggerHolder)
	if !ok || h.logger == nil {
		return NoopAuditLogger{}
	}
	return h.logger
}

// decisionString renders a boolean decision as a stable audit token.
func decisionString(allowed bool) string {
	if allowed {
		return "allowed"
	}
	return "denied"
}
