// Package sessionmem keeps small, scoped facts from a conversation across
// context compaction and process restarts.
package sessionmem

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// Entry is a single session memory record.
type Entry struct {
	Key        string    `json:"key"`
	Value      string    `json:"value"`
	Category   string    `json:"category"` // "context", "decision", or "learning"
	CreatedAt  time.Time `json:"created_at"`
	TurnNumber int       `json:"turn_number"`
	SourceID   string    `json:"source_id,omitempty"`
}

// SessionMemory is an ordered, thread-safe collection of session memory entries.
// A FileStore can persist its entries across restarts of the same session.
type SessionMemory struct {
	mu      sync.RWMutex
	entries []Entry
}

// NewSessionMemory returns an empty SessionMemory.
func NewSessionMemory() *SessionMemory {
	return &SessionMemory{}
}

// Add inserts a new entry. If an entry with the same key already exists it is
// replaced (preserving insertion order of the replacement).
func (sm *SessionMemory) Add(key, value, category string, turn int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Remove any previous entry with the same key so the new one takes its place.
	sm.removeLocked(key)

	sm.entries = append(sm.entries, Entry{
		Key:        key,
		Value:      value,
		Category:   category,
		CreatedAt:  time.Now(),
		TurnNumber: turn,
	})
}

// Get returns the value for the given key and true, or ("", false) if not found.
func (sm *SessionMemory) Get(key string) (string, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	for i := range sm.entries {
		if sm.entries[i].Key == key {
			return sm.entries[i].Value, true
		}
	}
	return "", false
}

// GetByCategory returns all entries that match the given category, in
// insertion order.
func (sm *SessionMemory) GetByCategory(category string) []Entry {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var out []Entry
	for _, e := range sm.entries {
		if e.Category == category {
			out = append(out, e)
		}
	}
	return out
}

// All returns a copy of every entry in insertion order.
func (sm *SessionMemory) All() []Entry {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	out := make([]Entry, len(sm.entries))
	copy(out, sm.entries)
	return out
}

// Remove deletes the entry with the given key, if it exists.
func (sm *SessionMemory) Remove(key string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.removeLocked(key)
}

// removeLocked removes an entry by key without locking (caller must hold mu).
func (sm *SessionMemory) removeLocked(key string) {
	for i, e := range sm.entries {
		if e.Key == key {
			sm.entries = append(sm.entries[:i], sm.entries[i+1:]...)
			return
		}
	}
}

// ToSystemPrompt formats all entries as a system-prompt section that can be
// injected into the conversation context.
func (sm *SessionMemory) ToSystemPrompt() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if len(sm.entries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("# Session Memory\n\n")

	// Group entries by category, preserving insertion order within each group.
	categories := []string{"context", "decision", "learning"}
	for _, cat := range categories {
		var items []Entry
		for _, e := range sm.entries {
			if e.Category == cat {
				items = append(items, e)
			}
		}
		if len(items) == 0 {
			continue
		}

		title := cases.Title(language.English).String(cat)
		sb.WriteString(fmt.Sprintf("## %s\n", title))
		for _, e := range items {
			sb.WriteString(fmt.Sprintf("- **%s**: %s\n", e.Key, e.Value))
		}
		sb.WriteByte('\n')
	}

	// Entries whose category doesn't match the well-known list.
	var other []Entry
	known := map[string]bool{"context": true, "decision": true, "learning": true}
	for _, e := range sm.entries {
		if !known[e.Category] {
			other = append(other, e)
		}
	}
	if len(other) > 0 {
		sb.WriteString("## Other\n")
		for _, e := range other {
			sb.WriteString(fmt.Sprintf("- **%s** [%s]: %s\n", e.Key, e.Category, e.Value))
		}
		sb.WriteByte('\n')
	}

	return sb.String()
}

// Serialize marshals the session memory to JSON.
func (sm *SessionMemory) Serialize() ([]byte, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return json.Marshal(sm.entries)
}

// Deserialize creates a SessionMemory from JSON produced by Serialize.
func Deserialize(data []byte) (*SessionMemory, error) {
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("sessionmem: deserialize: %w", err)
	}
	return &SessionMemory{entries: entries}, nil
}

// Prune keeps only the most recent maxEntries entries, discarding the oldest
// ones first (lowest index = oldest).
func (sm *SessionMemory) Prune(maxEntries int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if maxEntries < 0 {
		maxEntries = 0
	}
	if len(sm.entries) <= maxEntries {
		return
	}
	sm.entries = sm.entries[len(sm.entries)-maxEntries:]
}
