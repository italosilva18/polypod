package lrucache

import (
	"sync"
	"testing"
	"time"
)

func TestCache_BasicGetSet(t *testing.T) {
	c := New[string, int](10, 0)

	c.Set("foo", 1)
	c.Set("bar", 2)

	if v, ok := c.Get("foo"); !ok || v != 1 {
		t.Errorf("Get(foo) = %v, %v; want 1, true", v, ok)
	}
	if v, ok := c.Get("bar"); !ok || v != 2 {
		t.Errorf("Get(bar) = %v, %v; want 2, true", v, ok)
	}
	if _, ok := c.Get("missing"); ok {
		t.Errorf("Get(missing) should return false")
	}
}

func TestCache_Update(t *testing.T) {
	c := New[string, int](10, 0)
	c.Set("foo", 1)
	c.Set("foo", 2)

	if v, _ := c.Get("foo"); v != 2 {
		t.Errorf("Get after update = %v; want 2", v)
	}
	if c.Len() != 1 {
		t.Errorf("Len = %d; want 1", c.Len())
	}
}

func TestCache_LRUEviction(t *testing.T) {
	c := New[string, int](3, 0)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)

	// Access 'a' to make it most recent
	c.Get("a")

	// Adding 'd' should evict 'b' (oldest)
	c.Set("d", 4)

	if _, ok := c.Get("b"); ok {
		t.Errorf("'b' should have been evicted")
	}
	if _, ok := c.Get("a"); !ok {
		t.Errorf("'a' should still be present")
	}
	if c.Len() != 3 {
		t.Errorf("Len = %d; want 3", c.Len())
	}
}

func TestCache_TTL(t *testing.T) {
	c := New[string, int](10, 50*time.Millisecond)
	c.Set("foo", 1)

	if _, ok := c.Get("foo"); !ok {
		t.Fatal("Get immediately after Set should return true")
	}

	time.Sleep(100 * time.Millisecond)

	if _, ok := c.Get("foo"); ok {
		t.Errorf("Get after TTL should return false")
	}
}

func TestCache_Delete(t *testing.T) {
	c := New[string, int](10, 0)
	c.Set("foo", 1)

	if !c.Delete("foo") {
		t.Error("Delete should return true for existing key")
	}
	if _, ok := c.Get("foo"); ok {
		t.Error("Get after Delete should return false")
	}
	if c.Delete("missing") {
		t.Error("Delete should return false for missing key")
	}
}

func TestCache_Clear(t *testing.T) {
	c := New[string, int](10, 0)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)

	c.Clear()

	if c.Len() != 0 {
		t.Errorf("Len after Clear = %d; want 0", c.Len())
	}
}

func TestCache_Stats(t *testing.T) {
	c := New[string, int](10, 0)
	c.Set("foo", 1)

	c.Get("foo")     // hit
	c.Get("foo")     // hit
	c.Get("missing") // miss

	hits, misses, _ := c.Stats()
	if hits != 2 {
		t.Errorf("hits = %d; want 2", hits)
	}
	if misses != 1 {
		t.Errorf("misses = %d; want 1", misses)
	}

	rate := c.HitRate()
	want := float64(2) / float64(3)
	if rate < want-0.001 || rate > want+0.001 {
		t.Errorf("HitRate = %v; want ~%v", rate, want)
	}
}

func TestCache_Keys(t *testing.T) {
	c := New[string, int](10, 0)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)
	c.Get("a") // bump 'a' to front

	keys := c.Keys()
	if len(keys) != 3 {
		t.Fatalf("Keys len = %d; want 3", len(keys))
	}
	if keys[0] != "a" {
		t.Errorf("Keys[0] = %s; want 'a' (most recent)", keys[0])
	}
}

func TestCache_OnEvict(t *testing.T) {
	c := New[string, int](2, 0)

	var mu sync.Mutex
	evicted := make([]string, 0)
	c.OnEvict(func(k string, v int) {
		mu.Lock()
		defer mu.Unlock()
		evicted = append(evicted, k)
	})

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3) // evicts 'a'

	mu.Lock()
	defer mu.Unlock()
	if len(evicted) != 1 || evicted[0] != "a" {
		t.Errorf("evicted = %v; want [a]", evicted)
	}
}

func TestCache_Concurrent(t *testing.T) {
	c := New[int, int](100, 0)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := id*100 + j
				c.Set(key, j)
				c.Get(key)
			}
		}(i)
	}
	wg.Wait()

	if c.Len() != 100 {
		t.Errorf("Len = %d; want 100", c.Len())
	}
}

func TestCache_Resize(t *testing.T) {
	c := New[string, int](10, 0)
	for i := 0; i < 10; i++ {
		c.Set(string(rune('a'+i)), i)
	}

	c.Resize(5)

	if c.Len() != 5 {
		t.Errorf("Len after Resize = %d; want 5", c.Len())
	}
	if c.Capacity() != 5 {
		t.Errorf("Capacity after Resize = %d; want 5", c.Capacity())
	}
}

func TestCache_Has(t *testing.T) {
	c := New[string, int](10, 100*time.Millisecond)
	c.Set("foo", 1)

	if !c.Has("foo") {
		t.Error("Has(foo) should return true")
	}
	if c.Has("missing") {
		t.Error("Has(missing) should return false")
	}

	// Expired entries should return false
	time.Sleep(150 * time.Millisecond)
	if c.Has("foo") {
		t.Error("Has(foo) after TTL should return false")
	}
}

func TestCache_SetWithTTL(t *testing.T) {
	c := New[string, int](10, 1*time.Second) // default 1s TTL
	c.SetWithTTL("short", 1, 20*time.Millisecond)
	c.Set("default", 2)

	time.Sleep(50 * time.Millisecond)

	if _, ok := c.Get("short"); ok {
		t.Error("short should be expired")
	}
	if _, ok := c.Get("default"); !ok {
		t.Error("default should still be valid")
	}
}

func TestCache_ZeroCapacity(t *testing.T) {
	// Passing 0 should use a sensible default
	c := New[string, int](0, 0)
	c.Set("foo", 1)
	if v, ok := c.Get("foo"); !ok || v != 1 {
		t.Errorf("Get(foo) = %v, %v; want 1, true", v, ok)
	}
}
