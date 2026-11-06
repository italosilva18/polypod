// Package diagnostics provides event collection and diagnostic tracking,
// migrated from the TypeScript services/diagnosticTracking.ts and utils/diagLogs.ts.
package diagnostics

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Severity levels for diagnostic events.
type Severity string

const (
	SeverityError   Severity = "Error"
	SeverityWarning Severity = "Warning"
	SeverityInfo    Severity = "Info"
	SeverityHint    Severity = "Hint"
)

// Category groups diagnostic events for filtering and display.
type Category string

const (
	CategoryAPI         Category = "api"
	CategoryTool        Category = "tool"
	CategoryPermission  Category = "permission"
	CategorySession     Category = "session"
	CategoryPerformance Category = "performance"
	CategoryMCP         Category = "mcp"
	CategoryConfig      Category = "config"
	CategoryCompact     Category = "compact"
)

// DiagnosticEvent is a single recorded event.
type DiagnosticEvent struct {
	Timestamp time.Time      `json:"timestamp"`
	Category  Category       `json:"category"`
	Severity  Severity       `json:"severity"`
	Message   string         `json:"message"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// DiagnosticRange describes a source location range.
type DiagnosticRange struct {
	StartLine int `json:"start_line"`
	StartChar int `json:"start_char"`
	EndLine   int `json:"end_line"`
	EndChar   int `json:"end_char"`
}

// Diagnostic is a single code diagnostic (error/warning) from a file.
type Diagnostic struct {
	Message  string          `json:"message"`
	Severity Severity        `json:"severity"`
	Range    DiagnosticRange `json:"range"`
	Source   string          `json:"source,omitempty"`
	Code     string          `json:"code,omitempty"`
}

// DiagnosticFile groups diagnostics for one file URI.
type DiagnosticFile struct {
	URI         string       `json:"uri"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// maxDiagnosticsSummaryChars is the max length for formatted diagnostic summaries.
const maxDiagnosticsSummaryChars = 4000

// --- DiagnosticBuffer ---

// DiagnosticBuffer is a bounded buffer collecting DiagnosticEvents.
type DiagnosticBuffer struct {
	mu      sync.Mutex
	events  []DiagnosticEvent
	maxSize int
}

// NewDiagnosticBuffer creates a buffer with the given capacity.
func NewDiagnosticBuffer(maxSize int) *DiagnosticBuffer {
	if maxSize <= 0 {
		maxSize = 10000
	}
	return &DiagnosticBuffer{
		events:  make([]DiagnosticEvent, 0, maxSize),
		maxSize: maxSize,
	}
}

// Add appends an event, evicting the oldest if at capacity.
func (b *DiagnosticBuffer) Add(ev DiagnosticEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.events) >= b.maxSize {
		// Drop oldest 10%
		drop := b.maxSize / 10
		if drop < 1 {
			drop = 1
		}
		b.events = b.events[drop:]
	}
	b.events = append(b.events, ev)
}

// Events returns a copy of all buffered events.
func (b *DiagnosticBuffer) Events() []DiagnosticEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]DiagnosticEvent, len(b.events))
	copy(out, b.events)
	return out
}

// Clear removes all events.
func (b *DiagnosticBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = b.events[:0]
}

// Len returns the number of buffered events.
func (b *DiagnosticBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.events)
}

// --- DiagnosticTracker ---

// DiagnosticTracker collects diagnostic events and manages file diagnostic baselines.
type DiagnosticTracker struct {
	mu       sync.Mutex
	buffer   *DiagnosticBuffer
	baseline map[string][]Diagnostic // normalised path → baseline diagnostics
	logger   *slog.Logger
}

// NewDiagnosticTracker creates a tracker with a default buffer size.
func NewDiagnosticTracker(logger *slog.Logger) *DiagnosticTracker {
	return &DiagnosticTracker{
		buffer:   NewDiagnosticBuffer(10000),
		baseline: make(map[string][]Diagnostic),
		logger:   logger,
	}
}

// Record adds a diagnostic event.
func (t *DiagnosticTracker) Record(category Category, severity Severity, message string, metadata map[string]any) {
	ev := DiagnosticEvent{
		Timestamp: time.Now(),
		Category:  category,
		Severity:  severity,
		Message:   message,
		Metadata:  metadata,
	}
	t.buffer.Add(ev)
	if t.logger != nil {
		t.logger.Debug("diagnostic event",
			"category", string(category),
			"severity", string(severity),
			"message", message,
		)
	}
}

// SetBaseline records the current diagnostics for a file before editing.
func (t *DiagnosticTracker) SetBaseline(filePath string, diagnostics []Diagnostic) {
	t.mu.Lock()
	defer t.mu.Unlock()
	normalised := normalisePath(filePath)
	cp := make([]Diagnostic, len(diagnostics))
	copy(cp, diagnostics)
	t.baseline[normalised] = cp
}

// GetNewDiagnostics returns diagnostics that are not in the baseline for the given file.
func (t *DiagnosticTracker) GetNewDiagnostics(filePath string, current []Diagnostic) []Diagnostic {
	t.mu.Lock()
	defer t.mu.Unlock()
	normalised := normalisePath(filePath)
	base, ok := t.baseline[normalised]
	if !ok {
		return current
	}
	var newDiags []Diagnostic
	for _, d := range current {
		if !containsDiagnostic(base, d) {
			newDiags = append(newDiags, d)
		}
	}
	return newDiags
}

// Reset clears all baselines and buffered events.
func (t *DiagnosticTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.baseline = make(map[string][]Diagnostic)
	t.buffer.Clear()
}

// ExportDiagnostics returns all buffered events as JSON suitable for the /doctor command.
func (t *DiagnosticTracker) ExportDiagnostics() ([]byte, error) {
	events := t.buffer.Events()
	return json.MarshalIndent(events, "", "  ")
}

// FormatDiagnosticsSummary produces a human-readable summary of diagnostic files,
// truncated to maxDiagnosticsSummaryChars.
func FormatDiagnosticsSummary(files []DiagnosticFile) string {
	var sb strings.Builder
	for i, file := range files {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		name := filepath.Base(file.URI)
		sb.WriteString(name)
		sb.WriteString(":\n")
		for _, d := range file.Diagnostics {
			symbol := SeveritySymbol(d.Severity)
			fmt.Fprintf(&sb, "  %s [Line %d:%d] %s",
				symbol, d.Range.StartLine+1, d.Range.StartChar+1, d.Message)
			if d.Code != "" {
				fmt.Fprintf(&sb, " [%s]", d.Code)
			}
			if d.Source != "" {
				fmt.Fprintf(&sb, " (%s)", d.Source)
			}
			sb.WriteString("\n")
		}
	}
	result := sb.String()
	if len(result) > maxDiagnosticsSummaryChars {
		return result[:maxDiagnosticsSummaryChars-len("…[truncated]")] + "…[truncated]"
	}
	return result
}

// SeveritySymbol returns a text symbol for a diagnostic severity.
func SeveritySymbol(s Severity) string {
	switch s {
	case SeverityError:
		return "✗"
	case SeverityWarning:
		return "⚠"
	case SeverityInfo:
		return "ℹ"
	case SeverityHint:
		return "★"
	default:
		return "•"
	}
}

// --- Diagnostic file log (diagLogs) ---

// DiagLogLevel mirrors the TS DiagnosticLogLevel.
type DiagLogLevel string

const (
	DiagLogDebug DiagLogLevel = "debug"
	DiagLogInfo  DiagLogLevel = "info"
	DiagLogWarn  DiagLogLevel = "warn"
	DiagLogError DiagLogLevel = "error"
)

// diagLogEntry is a single line in the diagnostics log file.
type diagLogEntry struct {
	Timestamp string         `json:"timestamp"`
	Level     DiagLogLevel   `json:"level"`
	Event     string         `json:"event"`
	Data      map[string]any `json:"data"`
}

// LogForDiagnosticsNoPII logs a diagnostic event to the file specified by
// POLYPOD_DIAGNOSTICS_FILE. The caller MUST NOT include PII.
func LogForDiagnosticsNoPII(level DiagLogLevel, event string, data map[string]any) {
	logFile := os.Getenv("POLYPOD_DIAGNOSTICS_FILE")
	if logFile == "" {
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	entry := diagLogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Level:     level,
		Event:     event,
		Data:      data,
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	line = append(line, '\n')
	if err := appendToFile(logFile, line); err != nil {
		// Try creating directory first
		_ = os.MkdirAll(filepath.Dir(logFile), 0755)
		_ = appendToFile(logFile, line)
	}
}

func appendToFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// WithDiagnosticsTiming wraps fn and logs start/complete/fail events with duration.
func WithDiagnosticsTiming[T any](event string, fn func() (T, error)) (T, error) {
	start := time.Now()
	LogForDiagnosticsNoPII(DiagLogInfo, event+"_started", nil)
	result, err := fn()
	durationMS := time.Since(start).Milliseconds()
	if err != nil {
		LogForDiagnosticsNoPII(DiagLogError, event+"_failed", map[string]any{"duration_ms": durationMS})
		return result, err
	}
	LogForDiagnosticsNoPII(DiagLogInfo, event+"_completed", map[string]any{"duration_ms": durationMS})
	return result, nil
}

// --- internal helpers ---

func normalisePath(p string) string {
	// Strip common URI prefixes
	for _, prefix := range []string{"file://", "_claude_fs_right:", "_claude_fs_left:"} {
		if strings.HasPrefix(p, prefix) {
			p = p[len(prefix):]
			break
		}
	}
	return strings.ToLower(filepath.Clean(p))
}

func containsDiagnostic(list []Diagnostic, d Diagnostic) bool {
	for _, b := range list {
		if diagnosticsEqual(b, d) {
			return true
		}
	}
	return false
}

func diagnosticsEqual(a, b Diagnostic) bool {
	return a.Message == b.Message &&
		a.Severity == b.Severity &&
		a.Source == b.Source &&
		a.Code == b.Code &&
		a.Range == b.Range
}
