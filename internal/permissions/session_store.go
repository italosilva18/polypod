package permissions

// session_store.go implements session-scoped approval persistence.
//
// The TypeScript original tracks "always allow this tool for this session"
// decisions in an in-memory store so the user is not re-prompted for the
// same (tool, argsHash) pair during a single session. This Go port mirrors
// that behaviour via SessionStore plus a deterministic HashArgs helper.
//
// The store is process-wide (exposed through DefaultSessionStore) but also
// instantiable per-enforcer if a caller wants isolation (e.g. tests).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"sync"
	"time"
)

// SessionRule is a session-scoped allow/deny/ask decision that lives only
// for the lifetime of the current process (i.e. a single user "session").
//
// ArgsHash is a stable fingerprint of the tool-call arguments produced via
// HashArgs so that callers can look up a previous decision by computing the
// same hash before prompting the user.
type SessionRule struct {
	ToolName      string    `json:"tool_name"`
	ArgsHash      string    `json:"args_hash"`
	Decision      string    `json:"decision"` // "allow" | "deny" | "ask"
	AddedAt       time.Time `json:"added_at"`
	Reason        string    `json:"reason,omitempty"`
	SourceMessage string    `json:"source_message,omitempty"` // turn id that added this rule
}

// SessionStore is a thread-safe in-memory store of SessionRules.
//
// Rules are kept in insertion order; Lookup walks the slice in reverse so
// the most-recent matching rule wins. Add is idempotent on the tuple
// (ToolName, ArgsHash, Decision).
type SessionStore struct {
	mu    sync.RWMutex
	rules []SessionRule
}

// NewSessionStore returns a fresh, empty SessionStore.
func NewSessionStore() *SessionStore {
	return &SessionStore{}
}

// Add inserts a rule into the store. If an identical rule (same ToolName,
// ArgsHash and Decision) already exists, Add is a no-op so repeated
// approvals from the same session don't duplicate entries.
func (s *SessionStore) Add(rule SessionRule) {
	if rule.AddedAt.IsZero() {
		rule.AddedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.rules {
		if existing.ToolName == rule.ToolName &&
			existing.ArgsHash == rule.ArgsHash &&
			existing.Decision == rule.Decision {
			return
		}
	}
	s.rules = append(s.rules, rule)
}

// Lookup returns the most-recent matching rule for the given tool name and
// args hash. The second return is false when no rule matches.
//
// An empty argsHash acts as a wildcard on the store side: a stored rule
// with an empty ArgsHash matches every call to the same tool (this is how
// "always allow this tool regardless of arguments" is represented).
func (s *SessionStore) Lookup(toolName string, argsHash string) (SessionRule, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Walk in reverse so the most recently added rule wins.
	for i := len(s.rules) - 1; i >= 0; i-- {
		r := s.rules[i]
		if r.ToolName != toolName {
			continue
		}
		if r.ArgsHash == "" || r.ArgsHash == argsHash {
			return r, true
		}
	}
	return SessionRule{}, false
}

// List returns a snapshot copy of all rules currently in the store.
// Mutating the returned slice does not affect the store.
func (s *SessionStore) List() []SessionRule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SessionRule, len(s.rules))
	copy(out, s.rules)
	return out
}

// Clear removes every rule from the store.
func (s *SessionStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules = nil
}

// CountByDecision returns a map[decision]count, useful for the
// /permissions command to summarise session-scoped state. The keys are
// "allow", "deny", "ask" (or whatever Decision strings are present).
func (s *SessionStore) CountByDecision() map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]int, 3)
	for _, r := range s.rules {
		out[r.Decision]++
	}
	return out
}

// ToJSON serialises the entire store to JSON. Intended for transient
// persistence (e.g. dumping to a temp file for debugging, or surfacing in
// the /permissions UI). Not a long-term format — the store is session-
// scoped by design.
func (s *SessionStore) ToJSON() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return json.Marshal(s.rules)
}

// FromJSON replaces the store contents with the rules decoded from data.
// Any existing rules are discarded. An empty or null input results in an
// empty store.
func (s *SessionStore) FromJSON(data []byte) error {
	if len(data) == 0 {
		s.mu.Lock()
		s.rules = nil
		s.mu.Unlock()
		return nil
	}
	var rules []SessionRule
	if err := json.Unmarshal(data, &rules); err != nil {
		return err
	}
	s.mu.Lock()
	s.rules = rules
	s.mu.Unlock()
	return nil
}

// HashArgs produces a stable SHA-256 hex digest of a tool-call argument
// map. Stability is guaranteed by marshalling the map with the keys
// visited in sorted order, so two calls with the same semantic content
// always produce the same hash regardless of the original map's iteration
// order.
//
// Exported so that enforcer code (or callers wanting to pre-compute a
// lookup key) produces the exact same hash as whoever stored the rule.
func HashArgs(args map[string]any) string {
	encoded, err := stableMarshal(args)
	if err != nil {
		// Fall back to an empty-map hash — HashArgs never returns an error
		// because callers use it as a pure key-derivation helper. In
		// practice stableMarshal only fails on unsupported types, which we
		// don't expect from JSON-compatible tool args.
		encoded = []byte("{}")
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// stableMarshal JSON-encodes a value, emitting object keys in sorted
// order. For nested maps, slices and primitives it recurses so the entire
// structure is canonical.
func stableMarshal(v any) ([]byte, error) {
	switch val := v.(type) {
	case nil:
		return []byte("null"), nil
	case map[string]any:
		if val == nil {
			return []byte("null"), nil
		}
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := []byte{'{'}
		for i, k := range keys {
			if i > 0 {
				out = append(out, ',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return nil, err
			}
			out = append(out, kb...)
			out = append(out, ':')
			vb, err := stableMarshal(val[k])
			if err != nil {
				return nil, err
			}
			out = append(out, vb...)
		}
		out = append(out, '}')
		return out, nil
	case []any:
		out := []byte{'['}
		for i, item := range val {
			if i > 0 {
				out = append(out, ',')
			}
			ib, err := stableMarshal(item)
			if err != nil {
				return nil, err
			}
			out = append(out, ib...)
		}
		out = append(out, ']')
		return out, nil
	default:
		// Strings, numbers, bools, and any other JSON-compatible type.
		return json.Marshal(val)
	}
}

// --- process-wide singleton ---

var (
	defaultSessionStore     *SessionStore
	defaultSessionStoreOnce sync.Once
	defaultSessionStoreMu   sync.RWMutex
)

// DefaultSessionStore returns the process-wide singleton SessionStore,
// lazily initialised on first call. Useful for code paths that don't have
// an Enforcer handy but still want to consult session-scoped approvals.
func DefaultSessionStore() *SessionStore {
	defaultSessionStoreOnce.Do(func() {
		defaultSessionStoreMu.Lock()
		if defaultSessionStore == nil {
			defaultSessionStore = NewSessionStore()
		}
		defaultSessionStoreMu.Unlock()
	})
	defaultSessionStoreMu.RLock()
	s := defaultSessionStore
	defaultSessionStoreMu.RUnlock()
	return s
}

// SetSessionStore replaces the process-wide singleton store. Calling this
// also marks the once-guard as fired so subsequent DefaultSessionStore
// calls observe the new value. Passing nil resets to a fresh empty store.
func SetSessionStore(s *SessionStore) {
	if s == nil {
		s = NewSessionStore()
	}
	defaultSessionStoreMu.Lock()
	defaultSessionStore = s
	defaultSessionStoreMu.Unlock()
	// Ensure DefaultSessionStore() short-circuits past the Once initialiser.
	defaultSessionStoreOnce.Do(func() {})
}
