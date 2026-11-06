package observability

import (
	"log/slog"
	"sync"
	"time"

	"github.com/costa/polypod/internal/ringbuffer"
)

// LogEntry is a single log record kept in memory.
type LogEntry struct {
	Time    time.Time
	Level   slog.Level
	Message string
	Attrs   map[string]any
}

// LogRing is a bounded in-memory log tail.
type LogRing struct {
	mu     sync.RWMutex
	buffer *ringbuffer.Buffer[LogEntry]
}

// NewLogRing creates a log ring with the given capacity.
func NewLogRing(capacity int) *LogRing {
	if capacity <= 0 {
		capacity = 500
	}
	return &LogRing{
		buffer: ringbuffer.New[LogEntry](capacity),
	}
}

// Append adds a log entry to the ring.
func (r *LogRing) Append(entry LogEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buffer.Push(entry)
}

// AppendSimple adds a basic log entry.
func (r *LogRing) AppendSimple(level slog.Level, msg string) {
	r.Append(LogEntry{
		Time:    time.Now(),
		Level:   level,
		Message: msg,
	})
}

// Tail returns the most recent n entries (newest last).
func (r *LogRing) Tail(n int) []LogEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	all := r.buffer.Snapshot()
	if n <= 0 || n >= len(all) {
		return all
	}
	return all[len(all)-n:]
}

// Snapshot returns all current entries in chronological order.
func (r *LogRing) Snapshot() []LogEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.buffer.Snapshot()
}

// Len returns the current number of entries.
func (r *LogRing) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.buffer.Len()
}

// FilterByLevel returns entries at or above the given level.
func (r *LogRing) FilterByLevel(min slog.Level) []LogEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	all := r.buffer.Snapshot()
	filtered := make([]LogEntry, 0, len(all))
	for _, e := range all {
		if e.Level >= min {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// FilterByMessage returns entries whose message contains the substring.
func (r *LogRing) FilterByMessage(needle string) []LogEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	all := r.buffer.Snapshot()
	filtered := make([]LogEntry, 0, len(all))
	for _, e := range all {
		if contains(e.Message, needle) {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// Reset clears all entries.
func (r *LogRing) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buffer.Reset()
}

// Capacity returns the ring's max capacity.
func (r *LogRing) Capacity() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.buffer.Cap()
}

// contains is a dependency-free substring check.
func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
