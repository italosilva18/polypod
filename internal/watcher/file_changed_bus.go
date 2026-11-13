package watcher

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// FileChangeOp describes the kind of external workspace change observed.
type FileChangeOp string

const (
	FileOpAdded    FileChangeOp = "added"
	FileOpModified FileChangeOp = "modified"
	FileOpRemoved  FileChangeOp = "removed"
)

// FileChangeEvent records a single workspace file addition, modification, or unlink.
type FileChangeEvent struct {
	Path      string       `json:"path"`
	Op        FileChangeOp `json:"op"`
	Timestamp time.Time    `json:"timestamp"`
}

// ExternalChangeBuffer accumulates workspace file changes detected between AI
// turns so hooks (`FileChanged`) and the next prompt receive fresh awareness of
// external IDE edits without race conditions.
type ExternalChangeBuffer struct {
	mu      sync.Mutex
	pending map[string]FileChangeEvent
	maxSize int
}

// NewExternalChangeBuffer creates a thread-safe bounded file change buffer.
func NewExternalChangeBuffer(maxSize int) *ExternalChangeBuffer {
	if maxSize <= 0 {
		maxSize = 128
	}
	return &ExternalChangeBuffer{
		pending: make(map[string]FileChangeEvent),
		maxSize: maxSize,
	}
}

// Record stores or updates the latest change op for path.
func (b *ExternalChangeBuffer) Record(path string, op FileChangeOp) {
	if b == nil || strings.TrimSpace(path) == "" {
		return
	}
	clean := filepath.Clean(path)
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pending) >= b.maxSize {
		// Evict oldest entry when full.
		var oldestKey string
		var oldestAt time.Time
		for k, ev := range b.pending {
			if oldestKey == "" || ev.Timestamp.Before(oldestAt) {
				oldestKey, oldestAt = k, ev.Timestamp
			}
		}
		if oldestKey != "" {
			delete(b.pending, oldestKey)
		}
	}
	b.pending[clean] = FileChangeEvent{
		Path:      clean,
		Op:        op,
		Timestamp: time.Now(),
	}
}

// Drain atomically returns and clears all pending external file changes.
func (b *ExternalChangeBuffer) Drain() []FileChangeEvent {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	if len(b.pending) == 0 {
		b.mu.Unlock()
		return nil
	}
	out := make([]FileChangeEvent, 0, len(b.pending))
	for _, ev := range b.pending {
		out = append(out, ev)
	}
	clear(b.pending)
	b.mu.Unlock()

	sort.Slice(out, func(i, j int) bool {
		return out[i].Path < out[j].Path
	})
	return out
}

// DrainFormatted returns a concise prompt block summarizing external edits since the last turn.
func (b *ExternalChangeBuffer) DrainFormatted() string {
	events := b.Drain()
	if len(events) == 0 {
		return ""
	}
	limit := len(events)
	if limit > 15 {
		limit = 15
	}
	var sb strings.Builder
	sb.WriteString("[ARQUIVOS ALTERADOS EXTERNAMENTE NO WORKSPACE DESDE O ÚLTIMO TURNO — releia antes de sobrescrever]:\n")
	for i := 0; i < limit; i++ {
		ev := events[i]
		label := "modificado"
		switch ev.Op {
		case FileOpAdded:
			label = "adicionado"
		case FileOpRemoved:
			label = "removido"
		}
		fmt.Fprintf(&sb, "- %s (%s)\n", ev.Path, label)
	}
	if len(events) > limit {
		fmt.Fprintf(&sb, "- ... (+%d arquivos alterados)\n", len(events)-limit)
	}
	return strings.TrimSpace(sb.String())
}
