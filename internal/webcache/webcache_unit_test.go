package webcache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestNewDefaultsTTL verifies that passing 0 TTL falls back to 1 hour
// and that the cache directory is created underneath the supplied root.
func TestNewDefaultsTTL(t *testing.T) {
	root := t.TempDir()
	c := New(root, 0)
	if c == nil {
		t.Fatal("New returned nil")
	}
	if c.ttl != 1*time.Hour {
		t.Errorf("default ttl = %s, want 1h", c.ttl)
	}

	// The webcache subdirectory must exist on disk.
	want := filepath.Join(root, "webcache")
	info, err := os.Stat(want)
	if err != nil {
		t.Fatalf("expected cache dir at %s: %v", want, err)
	}
	if !info.IsDir() {
		t.Errorf("%s is not a directory", want)
	}
}

// TestNewCustomTTL verifies that a non-zero TTL is honored verbatim.
func TestNewCustomTTL(t *testing.T) {
	c := New(t.TempDir(), 15*time.Minute)
	if c.ttl != 15*time.Minute {
		t.Errorf("ttl = %s, want 15m", c.ttl)
	}
}

// TestSetGetWithURLField checks that the URL field is preserved through a
// Set/Get round trip, including the Provider field.
func TestSetGetWithURLField(t *testing.T) {
	c := New(t.TempDir(), 1*time.Hour)

	c.Set("https://example.com", CachedResult{
		Query:    "https://example.com",
		URL:      "https://example.com",
		Result:   "<html>hi</html>",
		Provider: "fetch",
	})

	got := c.Get("https://example.com")
	if got == nil {
		t.Fatal("Get returned nil for freshly-set entry")
	}
	if got.URL != "https://example.com" {
		t.Errorf("URL = %q, want %q", got.URL, "https://example.com")
	}
	if got.Result != "<html>hi</html>" {
		t.Errorf("Result = %q, want %q", got.Result, "<html>hi</html>")
	}
	if got.Provider != "fetch" {
		t.Errorf("Provider = %q, want %q", got.Provider, "fetch")
	}
	if got.CachedAt.IsZero() {
		t.Error("CachedAt should have been stamped by Set")
	}
	if got.ExpiresAt.IsZero() {
		t.Error("ExpiresAt should have been stamped by Set")
	}
}

// TestSetRespectsExplicitExpiresAt verifies that when a caller provides a
// non-zero ExpiresAt it is retained instead of being overridden by ttl.
func TestSetRespectsExplicitExpiresAt(t *testing.T) {
	c := New(t.TempDir(), 1*time.Hour)

	future := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	c.Set("k", CachedResult{
		Result:    "v",
		ExpiresAt: future,
	})

	got := c.Get("k")
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if !got.ExpiresAt.Equal(future) {
		t.Errorf("ExpiresAt = %s, want %s", got.ExpiresAt, future)
	}
}

// TestStatsFormat exercises the Stats() string: empty cache, then populated,
// then with an expired entry.
func TestStatsFormat(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, 1*time.Hour)

	s := c.Stats()
	if !strings.Contains(s, "0 entradas") {
		t.Errorf("empty stats should mention 0 entradas, got: %s", s)
	}
	if !strings.Contains(s, "1h0m0s") {
		t.Errorf("stats should mention the TTL, got: %s", s)
	}

	c.Set("a", CachedResult{Result: "1"})
	c.Set("b", CachedResult{Result: "2"})

	s = c.Stats()
	if !strings.Contains(s, "2 entradas") {
		t.Errorf("stats should show 2 entradas, got: %s", s)
	}
	if !strings.Contains(s, "(0 expiradas)") {
		t.Errorf("stats should show 0 expired, got: %s", s)
	}

	// Inject an already-expired entry.
	c.Set("stale", CachedResult{
		Result:    "old",
		ExpiresAt: time.Now().Add(-1 * time.Minute),
	})
	s = c.Stats()
	if !strings.Contains(s, "3 entradas") {
		t.Errorf("stats should show 3 entradas, got: %s", s)
	}
	if !strings.Contains(s, "(1 expiradas)") {
		t.Errorf("stats should show 1 expired, got: %s", s)
	}
}

// TestDiskPersistenceAcrossInstances verifies that a second Cache pointed
// at the same directory reloads entries written by the first instance.
func TestDiskPersistenceAcrossInstances(t *testing.T) {
	root := t.TempDir()

	first := New(root, 1*time.Hour)
	first.Set("persisted", CachedResult{
		Query:    "persisted",
		Result:   "durable",
		Provider: "test",
	})

	// Confirm the JSON file landed on disk.
	files, err := os.ReadDir(filepath.Join(root, "webcache"))
	if err != nil {
		t.Fatalf("reading webcache dir: %v", err)
	}
	jsonCount := 0
	for _, f := range files {
		if filepath.Ext(f.Name()) == ".json" && f.Name() != "index.json" {
			jsonCount++
		}
	}
	if jsonCount == 0 {
		t.Fatal("expected at least one .json entry file on disk")
	}

	// New instance should find the same entry.
	second := New(root, 1*time.Hour)
	got := second.Get("persisted")
	if got == nil {
		t.Fatal("second instance did not reload persisted entry")
	}
	if got.Result != "durable" {
		t.Errorf("reloaded Result = %q, want %q", got.Result, "durable")
	}
	if got.Provider != "test" {
		t.Errorf("reloaded Provider = %q, want %q", got.Provider, "test")
	}
}

// TestClearRemovesDiskFiles ensures Clear() wipes entries both in memory
// and from the backing directory.
func TestClearRemovesDiskFiles(t *testing.T) {
	root := t.TempDir()
	c := New(root, 1*time.Hour)

	c.Set("x", CachedResult{Result: "1"})
	c.Set("y", CachedResult{Result: "2"})

	c.Clear()

	if c.Get("x") != nil || c.Get("y") != nil {
		t.Error("entries still reachable after Clear")
	}

	files, err := os.ReadDir(filepath.Join(root, "webcache"))
	if err != nil {
		t.Fatalf("reading webcache dir after Clear: %v", err)
	}
	for _, f := range files {
		if filepath.Ext(f.Name()) == ".json" && f.Name() != "index.json" {
			t.Errorf("leftover entry file after Clear: %s", f.Name())
		}
	}

	// Directory itself must still exist so the cache remains usable.
	if _, err := os.Stat(filepath.Join(root, "webcache")); err != nil {
		t.Errorf("cache dir should be recreated after Clear: %v", err)
	}

	// And the cache must remain functional for subsequent writes.
	c.Set("z", CachedResult{Result: "3"})
	if got := c.Get("z"); got == nil || got.Result != "3" {
		t.Error("cache not usable after Clear")
	}
}

// TestOverwriteEntry verifies that Set on an existing key replaces the
// previous value and does not create a duplicate entry.
func TestOverwriteEntry(t *testing.T) {
	c := New(t.TempDir(), 1*time.Hour)

	c.Set("dup", CachedResult{Result: "first"})
	c.Set("dup", CachedResult{Result: "second"})

	got := c.Get("dup")
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Result != "second" {
		t.Errorf("Result = %q, want %q", got.Result, "second")
	}

	if !strings.Contains(c.Stats(), "1 entradas") {
		t.Errorf("overwrite should keep entry count at 1, stats: %s", c.Stats())
	}
}

// TestMultipleKeysIsolated verifies that distinct keys produce independent
// entries and that removal of one does not affect the others.
func TestMultipleKeysIsolated(t *testing.T) {
	c := New(t.TempDir(), 1*time.Hour)

	items := map[string]string{
		"alpha": "A",
		"beta":  "B",
		"gamma": "G",
	}
	for k, v := range items {
		c.Set(k, CachedResult{Query: k, Result: v})
	}

	for k, want := range items {
		got := c.Get(k)
		if got == nil {
			t.Errorf("Get(%q) = nil", k)
			continue
		}
		if got.Result != want {
			t.Errorf("Get(%q).Result = %q, want %q", k, got.Result, want)
		}
	}

	if !strings.Contains(c.Stats(), "3 entradas") {
		t.Errorf("expected 3 entradas, stats: %s", c.Stats())
	}
}

// TestExpiredEntryTreatedAsMiss is a focused check that a manually-expired
// entry is not returned by Get even though it still occupies a slot
// (Stats should count it as expired).
func TestExpiredEntryTreatedAsMiss(t *testing.T) {
	c := New(t.TempDir(), 1*time.Hour)

	c.Set("old", CachedResult{
		Result:    "stale",
		ExpiresAt: time.Now().Add(-1 * time.Second),
	})

	if got := c.Get("old"); got != nil {
		t.Errorf("expired entry should miss, got %+v", got)
	}
	if !strings.Contains(c.Stats(), "(1 expiradas)") {
		t.Errorf("stats should report 1 expired, got: %s", c.Stats())
	}
}
