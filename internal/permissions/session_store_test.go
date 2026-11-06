package permissions

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestSessionStore_AddLookupRoundtrip(t *testing.T) {
	s := NewSessionStore()
	hash := HashArgs(map[string]any{"path": "/tmp/foo"})

	rule := SessionRule{
		ToolName:      "Read",
		ArgsHash:      hash,
		Decision:      "allow",
		Reason:        "user approved",
		SourceMessage: "turn-42",
	}
	s.Add(rule)

	got, ok := s.Lookup("Read", hash)
	if !ok {
		t.Fatalf("Lookup: expected rule to be found")
	}
	if got.Decision != "allow" {
		t.Errorf("Decision = %q, want %q", got.Decision, "allow")
	}
	if got.SourceMessage != "turn-42" {
		t.Errorf("SourceMessage = %q, want %q", got.SourceMessage, "turn-42")
	}
	if got.AddedAt.IsZero() {
		t.Errorf("AddedAt should have been auto-populated")
	}
}

func TestSessionStore_LookupUnknownReturnsFalse(t *testing.T) {
	s := NewSessionStore()
	s.Add(SessionRule{ToolName: "Read", ArgsHash: "abc", Decision: "allow"})

	if _, ok := s.Lookup("Write", "abc"); ok {
		t.Errorf("Lookup for unknown tool should return false")
	}
	if _, ok := s.Lookup("Read", "different"); ok {
		t.Errorf("Lookup for unknown hash should return false")
	}
}

func TestSessionStore_LookupWildcardArgsHash(t *testing.T) {
	s := NewSessionStore()
	// Empty ArgsHash means "match any call to this tool".
	s.Add(SessionRule{ToolName: "Bash", ArgsHash: "", Decision: "allow"})

	if _, ok := s.Lookup("Bash", "any-hash-at-all"); !ok {
		t.Errorf("empty ArgsHash rule should match any hash")
	}
}

func TestSessionStore_AddIdempotent(t *testing.T) {
	s := NewSessionStore()
	rule := SessionRule{
		ToolName: "Edit",
		ArgsHash: "deadbeef",
		Decision: "allow",
		AddedAt:  time.Unix(1700000000, 0),
	}
	s.Add(rule)
	s.Add(rule)
	s.Add(rule)

	list := s.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 rule after idempotent Adds, got %d", len(list))
	}

	// Adding a different Decision for the same (tool, hash) is NOT a duplicate.
	s.Add(SessionRule{ToolName: "Edit", ArgsHash: "deadbeef", Decision: "deny"})
	if n := len(s.List()); n != 2 {
		t.Errorf("expected 2 rules after adding distinct decision, got %d", n)
	}
}

func TestSessionStore_CountByDecision(t *testing.T) {
	s := NewSessionStore()
	s.Add(SessionRule{ToolName: "Read", ArgsHash: "h1", Decision: "allow"})
	s.Add(SessionRule{ToolName: "Read", ArgsHash: "h2", Decision: "allow"})
	s.Add(SessionRule{ToolName: "Write", ArgsHash: "h3", Decision: "deny"})
	s.Add(SessionRule{ToolName: "Bash", ArgsHash: "h4", Decision: "ask"})

	counts := s.CountByDecision()
	if counts["allow"] != 2 {
		t.Errorf("allow count = %d, want 2", counts["allow"])
	}
	if counts["deny"] != 1 {
		t.Errorf("deny count = %d, want 1", counts["deny"])
	}
	if counts["ask"] != 1 {
		t.Errorf("ask count = %d, want 1", counts["ask"])
	}
}

func TestSessionStore_Clear(t *testing.T) {
	s := NewSessionStore()
	s.Add(SessionRule{ToolName: "Read", ArgsHash: "h1", Decision: "allow"})
	s.Add(SessionRule{ToolName: "Write", ArgsHash: "h2", Decision: "deny"})

	s.Clear()
	if n := len(s.List()); n != 0 {
		t.Errorf("after Clear, List should be empty, got %d", n)
	}
	if _, ok := s.Lookup("Read", "h1"); ok {
		t.Errorf("after Clear, Lookup should return false")
	}
}

func TestHashArgs_StableAcrossCalls(t *testing.T) {
	// Build two logically-identical maps with different insertion orders.
	a := map[string]any{
		"command": "ls -la",
		"timeout": 30,
		"env": map[string]any{
			"PATH": "/usr/bin",
			"HOME": "/root",
		},
	}
	b := map[string]any{
		"timeout": 30,
		"env": map[string]any{
			"HOME": "/root",
			"PATH": "/usr/bin",
		},
		"command": "ls -la",
	}

	h1 := HashArgs(a)
	h2 := HashArgs(b)
	if h1 != h2 {
		t.Errorf("HashArgs not stable: %q != %q", h1, h2)
	}

	// Sanity: second call on the same input yields the same thing.
	if HashArgs(a) != h1 {
		t.Errorf("HashArgs not deterministic on repeated call")
	}

	// A different input should produce a different hash.
	diff := HashArgs(map[string]any{"command": "rm -rf /"})
	if diff == h1 {
		t.Errorf("HashArgs collision: different inputs produced same hash")
	}

	// Nil / empty maps should still produce a stable hash.
	if HashArgs(nil) == "" {
		t.Errorf("HashArgs(nil) returned empty string")
	}
}

func TestHashArgs_HandlesNestedSlices(t *testing.T) {
	args := map[string]any{
		"files": []any{"a.go", "b.go", "c.go"},
		"flags": []any{map[string]any{"name": "verbose", "value": true}},
	}
	h1 := HashArgs(args)
	h2 := HashArgs(args)
	if h1 != h2 {
		t.Errorf("nested slice hash not stable: %q != %q", h1, h2)
	}
}

func TestSessionStore_ToJSONFromJSONRoundtrip(t *testing.T) {
	s := NewSessionStore()
	when := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	s.Add(SessionRule{
		ToolName:      "Read",
		ArgsHash:      "hash-1",
		Decision:      "allow",
		AddedAt:       when,
		Reason:        "explicit user approval",
		SourceMessage: "turn-1",
	})
	s.Add(SessionRule{
		ToolName: "Bash",
		ArgsHash: "hash-2",
		Decision: "deny",
		AddedAt:  when,
	})

	data, err := s.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON failed: %v", err)
	}

	// Independently validate the JSON is well-formed.
	var raw []map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("ToJSON produced invalid JSON: %v", err)
	}
	if len(raw) != 2 {
		t.Errorf("serialised rule count = %d, want 2", len(raw))
	}

	restored := NewSessionStore()
	if err := restored.FromJSON(data); err != nil {
		t.Fatalf("FromJSON failed: %v", err)
	}

	list := restored.List()
	if len(list) != 2 {
		t.Fatalf("restored rules = %d, want 2", len(list))
	}

	got, ok := restored.Lookup("Read", "hash-1")
	if !ok {
		t.Fatalf("restored Lookup(Read, hash-1) not found")
	}
	if got.Decision != "allow" || got.Reason != "explicit user approval" || got.SourceMessage != "turn-1" {
		t.Errorf("restored rule fields lost: %+v", got)
	}
	if !got.AddedAt.Equal(when) {
		t.Errorf("restored AddedAt = %v, want %v", got.AddedAt, when)
	}

	// Round-tripping empty payloads should be tolerated.
	empty := NewSessionStore()
	if err := empty.FromJSON(nil); err != nil {
		t.Errorf("FromJSON(nil) returned error: %v", err)
	}
	if err := empty.FromJSON([]byte{}); err != nil {
		t.Errorf("FromJSON(empty) returned error: %v", err)
	}
}

func TestDefaultSessionStore_Singleton(t *testing.T) {
	// Snapshot and restore the global so this test doesn't leak state.
	original := DefaultSessionStore()
	t.Cleanup(func() { SetSessionStore(original) })

	fresh := NewSessionStore()
	SetSessionStore(fresh)

	if got := DefaultSessionStore(); got != fresh {
		t.Errorf("DefaultSessionStore did not reflect SetSessionStore")
	}

	// Passing nil should reset to a fresh empty store (not nil).
	SetSessionStore(nil)
	if got := DefaultSessionStore(); got == nil {
		t.Errorf("DefaultSessionStore returned nil after SetSessionStore(nil)")
	}
	if n := len(DefaultSessionStore().List()); n != 0 {
		t.Errorf("reset store should be empty, got %d rules", n)
	}
}

func TestSessionStore_ConcurrentAddLookup(t *testing.T) {
	s := NewSessionStore()
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			hash := HashArgs(map[string]any{"i": i})
			s.Add(SessionRule{ToolName: "Tool", ArgsHash: hash, Decision: "allow"})
			if _, ok := s.Lookup("Tool", hash); !ok {
				t.Errorf("concurrent Lookup failed for i=%d", i)
			}
		}(i)
	}
	wg.Wait()

	if n := len(s.List()); n != 50 {
		t.Errorf("concurrent Add produced %d rules, want 50", n)
	}
}
