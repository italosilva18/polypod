package toolcache

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// -----------------------------------------------------------------------------
// ToolSchemaCache: constructor, set/get round trip, size, clear
// -----------------------------------------------------------------------------

func TestNewToolSchemaCache_ReturnsEmptyCache(t *testing.T) {
	c := NewToolSchemaCache()
	if c == nil {
		t.Fatal("NewToolSchemaCache returned nil")
	}
	if got := c.Size(); got != 0 {
		t.Errorf("new cache Size = %d, want 0", got)
	}
	if _, ok := c.Get("anything"); ok {
		t.Error("new cache Get returned ok=true; want false")
	}
}

func TestToolSchemaCache_SetGetRoundTrip(t *testing.T) {
	c := NewToolSchemaCache()
	want := CachedSchema{
		Name:        "Read",
		Description: "reads a file",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
		},
		Strict:       true,
		CacheControl: &CacheControl{Type: "ephemeral"},
	}
	c.Set("Read", want)

	got, ok := c.Get("Read")
	if !ok {
		t.Fatal("Get returned ok=false after Set")
	}
	if got.Name != want.Name || got.Description != want.Description {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, want)
	}
	if got.Strict != want.Strict {
		t.Errorf("Strict mismatch: got %v, want %v", got.Strict, want.Strict)
	}
	if got.CacheControl == nil || got.CacheControl.Type != "ephemeral" {
		t.Errorf("CacheControl mismatch: got %+v", got.CacheControl)
	}
	if got.InputSchema["type"] != "object" {
		t.Errorf("InputSchema mismatch: got %+v", got.InputSchema)
	}
}

func TestToolSchemaCache_SetOverwrites(t *testing.T) {
	c := NewToolSchemaCache()
	c.Set("tool", CachedSchema{Name: "tool", Description: "v1"})
	c.Set("tool", CachedSchema{Name: "tool", Description: "v2"})

	got, ok := c.Get("tool")
	if !ok {
		t.Fatal("Get missing after overwrite")
	}
	if got.Description != "v2" {
		t.Errorf("expected overwritten description v2, got %q", got.Description)
	}
	if got := c.Size(); got != 1 {
		t.Errorf("Size after overwrite = %d, want 1", got)
	}
}

func TestToolSchemaCache_ClearEvictsAll(t *testing.T) {
	c := NewToolSchemaCache()
	c.Set("a", CachedSchema{Name: "a"})
	c.Set("b", CachedSchema{Name: "b"})
	c.Set("c", CachedSchema{Name: "c"})
	if c.Size() != 3 {
		t.Fatalf("pre-clear Size = %d, want 3", c.Size())
	}

	c.Clear()

	if got := c.Size(); got != 0 {
		t.Errorf("post-clear Size = %d, want 0", got)
	}
	if _, ok := c.Get("a"); ok {
		t.Error("Get(a) still present after Clear")
	}
}

func TestToolSchemaCache_ConcurrentSetGet(t *testing.T) {
	c := NewToolSchemaCache()
	var wg sync.WaitGroup
	const n = 50

	for i := 0; i < n; i++ {
		wg.Add(2)
		name := "tool" + string(rune('A'+(i%26)))
		go func(name string) {
			defer wg.Done()
			c.Set(name, CachedSchema{Name: name})
		}(name)
		go func(name string) {
			defer wg.Done()
			_, _ = c.Get(name)
		}(name)
	}
	wg.Wait()

	if c.Size() == 0 {
		t.Error("expected non-zero Size after concurrent writes")
	}
}

// -----------------------------------------------------------------------------
// CacheKey: derivation
// -----------------------------------------------------------------------------

func TestCacheKey_Deterministic(t *testing.T) {
	k1 := CacheKey("Read", map[string]any{"path": "/tmp/x", "limit": 10})
	k2 := CacheKey("Read", map[string]any{"path": "/tmp/x", "limit": 10})
	if k1 != k2 {
		t.Errorf("CacheKey not deterministic: %q vs %q", k1, k2)
	}
	if !strings.HasPrefix(k1, "Read:") {
		t.Errorf("CacheKey missing tool name prefix: %q", k1)
	}
}

func TestCacheKey_DifferentInputsDiffer(t *testing.T) {
	k1 := CacheKey("Read", map[string]any{"path": "/tmp/a"})
	k2 := CacheKey("Read", map[string]any{"path": "/tmp/b"})
	if k1 == k2 {
		t.Errorf("CacheKey collided for different inputs: %q", k1)
	}
}

func TestCacheKey_DifferentToolsDiffer(t *testing.T) {
	input := map[string]any{"x": 1}
	k1 := CacheKey("ToolA", input)
	k2 := CacheKey("ToolB", input)
	if k1 == k2 {
		t.Errorf("CacheKey collided for different tools: %q", k1)
	}
	if !strings.HasPrefix(k1, "ToolA:") || !strings.HasPrefix(k2, "ToolB:") {
		t.Errorf("CacheKey prefixes wrong: %q, %q", k1, k2)
	}
}

func TestCacheKey_UnmarshalableInputFallsBack(t *testing.T) {
	// chan values cannot be marshaled by encoding/json -> fallback branch.
	key := CacheKey("Bash", make(chan int))
	if key != "Bash" {
		t.Errorf("fallback key = %q, want %q", key, "Bash")
	}
}

// -----------------------------------------------------------------------------
// CacheEntry: TTL / expiry
// -----------------------------------------------------------------------------

func TestCacheEntry_NotExpired(t *testing.T) {
	e := CacheEntry{
		Value:     "hello",
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	if e.IsExpired() {
		t.Error("fresh entry reported as expired")
	}
}

func TestCacheEntry_Expired(t *testing.T) {
	e := CacheEntry{
		Value:     "stale",
		ExpiresAt: time.Now().Add(-1 * time.Second),
	}
	if !e.IsExpired() {
		t.Error("past-deadline entry not reported as expired")
	}
}

func TestCacheEntry_ExpiryTransition(t *testing.T) {
	e := CacheEntry{
		Value:     42,
		ExpiresAt: time.Now().Add(20 * time.Millisecond),
	}
	if e.IsExpired() {
		t.Fatal("entry expired before TTL elapsed")
	}
	time.Sleep(40 * time.Millisecond)
	if !e.IsExpired() {
		t.Error("entry should be expired after TTL elapsed")
	}
}

// -----------------------------------------------------------------------------
// ToolResultStorage: set/get round trip, eviction (Clear), stats (IsSeen)
// -----------------------------------------------------------------------------

func TestToolResultStorage_PersistAndPreview(t *testing.T) {
	dir := t.TempDir()
	s := NewToolResultStorage(dir, "session-123")

	content := strings.Repeat("x", DefaultMaxResultSizeChars+100)
	res, err := s.Persist(content, "toolu_01", false)
	if err != nil {
		t.Fatalf("Persist error: %v", err)
	}
	if res == nil {
		t.Fatal("Persist returned nil result")
	}
	if res.OriginalSize != len(content) {
		t.Errorf("OriginalSize = %d, want %d", res.OriginalSize, len(content))
	}
	if !res.HasMore {
		t.Error("expected HasMore=true for oversized content")
	}

	// File should exist on disk.
	if _, err := os.Stat(res.Filepath); err != nil {
		t.Errorf("persisted file missing: %v", err)
	}
	// Path should be under the session directory.
	wantPrefix := filepath.Join(dir, "session-123", ToolResultsSubdir)
	if !strings.HasPrefix(res.Filepath, wantPrefix) {
		t.Errorf("Filepath %q not under %q", res.Filepath, wantPrefix)
	}
}

func TestToolResultStorage_SeenAndReplacementRoundTrip(t *testing.T) {
	s := NewToolResultStorage(t.TempDir(), "s1")

	if s.IsSeen("id1") {
		t.Error("new storage reports id1 seen")
	}
	s.MarkSeen("id1")
	if !s.IsSeen("id1") {
		t.Error("MarkSeen did not take effect")
	}

	s.SetReplacement("id2", "[replaced]")
	got, ok := s.GetReplacement("id2")
	if !ok || got != "[replaced]" {
		t.Errorf("GetReplacement = (%q,%v), want ([replaced], true)", got, ok)
	}
	if !s.IsSeen("id2") {
		t.Error("SetReplacement should also mark seen")
	}

	if _, ok := s.GetReplacement("missing"); ok {
		t.Error("GetReplacement should return false for missing id")
	}
}

func TestToolResultStorage_CloneIsIndependent(t *testing.T) {
	s := NewToolResultStorage(t.TempDir(), "s1")
	s.MarkSeen("A")
	s.SetReplacement("B", "replB")

	clone := s.Clone()

	// Clone observes existing state.
	if !clone.IsSeen("A") || !clone.IsSeen("B") {
		t.Error("clone missing original seen ids")
	}
	if r, ok := clone.GetReplacement("B"); !ok || r != "replB" {
		t.Errorf("clone replacement = %q,%v", r, ok)
	}

	// Mutating clone must not affect original (eviction/isolation).
	clone.MarkSeen("C")
	if s.IsSeen("C") {
		t.Error("mutation on clone leaked to original")
	}
}

func TestGeneratePreview_ShortContent(t *testing.T) {
	content := "short output"
	preview, hasMore := GeneratePreview(content, 100)
	if hasMore {
		t.Error("hasMore should be false for content under limit")
	}
	if preview != content {
		t.Errorf("preview = %q, want %q", preview, content)
	}
}

func TestGeneratePreview_TruncatesAtNewline(t *testing.T) {
	// Build content where a newline sits in the second half of the window,
	// so GeneratePreview should truncate exactly at that newline.
	var b strings.Builder
	for i := 0; i < 60; i++ {
		b.WriteByte('a')
	}
	b.WriteByte('\n')
	for i := 0; i < 200; i++ {
		b.WriteByte('b')
	}
	content := b.String()

	preview, hasMore := GeneratePreview(content, 100)
	if !hasMore {
		t.Error("hasMore should be true when content exceeds limit")
	}
	if !strings.HasSuffix(preview, "a") {
		t.Errorf("preview should end at newline boundary; got tail %q",
			preview[max(0, len(preview)-5):])
	}
	if strings.Contains(preview, "b") {
		t.Error("preview leaked content past truncation point")
	}
}

// -----------------------------------------------------------------------------
// ToolPool: checkout/return/stats/eviction
// -----------------------------------------------------------------------------

func TestToolPool_PutGetSizeClear(t *testing.T) {
	p := NewToolPool()
	if p.Size("Read") != 0 {
		t.Errorf("new pool Size = %d, want 0", p.Size("Read"))
	}
	if inst := p.Get("Read"); inst != nil {
		t.Errorf("empty Get = %v, want nil", inst)
	}

	p.Put("Read", "instance-1")
	p.Put("Read", "instance-2")
	if p.Size("Read") != 2 {
		t.Errorf("Size after 2 Puts = %d, want 2", p.Size("Read"))
	}

	// Pool is LIFO: last-in, first-out.
	got := p.Get("Read")
	if got != "instance-2" {
		t.Errorf("Get returned %v, want instance-2 (LIFO)", got)
	}
	if p.Size("Read") != 1 {
		t.Errorf("Size after Get = %d, want 1", p.Size("Read"))
	}

	// Clear evicts all pooled instances across tool names.
	p.Put("Write", "w1")
	p.Clear()
	if p.Size("Read") != 0 || p.Size("Write") != 0 {
		t.Error("Clear did not evict all pooled instances")
	}
}

// small helper so older Go versions without builtin max still compile
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
