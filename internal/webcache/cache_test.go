package webcache

import (
	"testing"
	"time"
)

func TestCacheSetGet(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, 1*time.Hour)

	c.Set("test query", CachedResult{
		Query:    "test query",
		Result:   "test result",
		Provider: "duckduckgo",
	})

	got := c.Get("test query")
	if got == nil {
		t.Fatal("expected cached result")
	}
	if got.Result != "test result" {
		t.Errorf("result = %q, want %q", got.Result, "test result")
	}
}

func TestCacheExpiry(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, 50*time.Millisecond)

	c.Set("expiring", CachedResult{Result: "data"})

	// Should exist immediately
	if c.Get("expiring") == nil {
		t.Fatal("should exist before expiry")
	}

	// Wait for expiry
	time.Sleep(60 * time.Millisecond)

	if c.Get("expiring") != nil {
		t.Fatal("should be expired")
	}
}

func TestCacheClear(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, 1*time.Hour)

	c.Set("key1", CachedResult{Result: "a"})
	c.Set("key2", CachedResult{Result: "b"})
	c.Clear()

	if c.Get("key1") != nil || c.Get("key2") != nil {
		t.Fatal("cache should be empty after clear")
	}
}

func TestCacheMiss(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, 1*time.Hour)

	if c.Get("nonexistent") != nil {
		t.Fatal("should return nil for cache miss")
	}
}
