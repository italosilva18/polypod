package trie

import (
	"sort"
	"testing"
)

func TestTrie_InsertGet(t *testing.T) {
	tr := New[int]()
	tr.Insert("foo", 1)
	tr.Insert("bar", 2)
	tr.Insert("foobar", 3)

	if v, ok := tr.Get("foo"); !ok || v != 1 {
		t.Errorf("Get(foo) = %v, %v; want 1, true", v, ok)
	}
	if v, ok := tr.Get("bar"); !ok || v != 2 {
		t.Errorf("Get(bar) = %v, %v; want 2, true", v, ok)
	}
	if v, ok := tr.Get("foobar"); !ok || v != 3 {
		t.Errorf("Get(foobar) = %v, %v; want 3, true", v, ok)
	}
	if _, ok := tr.Get("fo"); ok {
		t.Error("Get(fo) should not match partial prefix")
	}
}

func TestTrie_Has(t *testing.T) {
	tr := New[string]()
	tr.Insert("hello", "world")

	if !tr.Has("hello") {
		t.Error("Has(hello) should return true")
	}
	if tr.Has("hell") {
		t.Error("Has(hell) should return false for partial prefix")
	}
	if tr.Has("missing") {
		t.Error("Has(missing) should return false")
	}
}

func TestTrie_Delete(t *testing.T) {
	tr := New[int]()
	tr.Insert("foo", 1)
	tr.Insert("bar", 2)

	if !tr.Delete("foo") {
		t.Error("Delete(foo) should return true")
	}
	if tr.Has("foo") {
		t.Error("Has(foo) after Delete should return false")
	}
	if tr.Delete("foo") {
		t.Error("Second Delete(foo) should return false")
	}
	if !tr.Has("bar") {
		t.Error("Delete(foo) should not affect bar")
	}
}

func TestTrie_Len(t *testing.T) {
	tr := New[int]()
	if tr.Len() != 0 {
		t.Errorf("Empty Len = %d; want 0", tr.Len())
	}

	tr.Insert("a", 1)
	tr.Insert("b", 2)
	tr.Insert("c", 3)

	if tr.Len() != 3 {
		t.Errorf("Len = %d; want 3", tr.Len())
	}

	tr.Delete("a")
	if tr.Len() != 2 {
		t.Errorf("Len after delete = %d; want 2", tr.Len())
	}

	// Re-inserting shouldn't double-count
	tr.Insert("b", 20)
	if tr.Len() != 2 {
		t.Errorf("Len after overwrite = %d; want 2", tr.Len())
	}
}

func TestTrie_PrefixKeys(t *testing.T) {
	tr := New[int]()
	words := []string{"apple", "app", "apricot", "banana", "band", "bandana"}
	for i, w := range words {
		tr.Insert(w, i)
	}

	got := tr.PrefixKeys("ap")
	sort.Strings(got)
	want := []string{"app", "apple", "apricot"}
	if !equalStrSlice(got, want) {
		t.Errorf("PrefixKeys(ap) = %v; want %v", got, want)
	}

	got = tr.PrefixKeys("ban")
	sort.Strings(got)
	want = []string{"banana", "band", "bandana"}
	if !equalStrSlice(got, want) {
		t.Errorf("PrefixKeys(ban) = %v; want %v", got, want)
	}

	// Empty prefix returns all
	got = tr.PrefixKeys("")
	if len(got) != len(words) {
		t.Errorf("PrefixKeys() len = %d; want %d", len(got), len(words))
	}

	// Non-existent prefix
	got = tr.PrefixKeys("xyz")
	if len(got) != 0 {
		t.Errorf("PrefixKeys(xyz) = %v; want []", got)
	}
}

func TestTrie_PrefixSearch(t *testing.T) {
	tr := New[int]()
	tr.Insert("apple", 1)
	tr.Insert("app", 2)
	tr.Insert("apricot", 3)

	results := tr.PrefixSearch("ap")
	if len(results) != 3 {
		t.Fatalf("PrefixSearch(ap) len = %d; want 3", len(results))
	}

	// Results should be sorted by key
	for i := 1; i < len(results); i++ {
		if results[i-1].Key > results[i].Key {
			t.Errorf("PrefixSearch results not sorted: %v", results)
			break
		}
	}
}

func TestTrie_LongestPrefix(t *testing.T) {
	tr := New[string]()
	tr.Insert("/api", "root")
	tr.Insert("/api/v1", "v1")
	tr.Insert("/api/v1/users", "users")

	prefix, value, ok := tr.LongestPrefix("/api/v1/users/123")
	if !ok {
		t.Fatal("LongestPrefix should return ok")
	}
	if prefix != "/api/v1/users" {
		t.Errorf("prefix = %s; want /api/v1/users", prefix)
	}
	if value != "users" {
		t.Errorf("value = %s; want users", value)
	}

	// Partial match stops at shallower node
	prefix, value, ok = tr.LongestPrefix("/api/v2")
	if !ok {
		t.Fatal("LongestPrefix /api/v2 should return ok (/api)")
	}
	if prefix != "/api" {
		t.Errorf("prefix = %s; want /api", prefix)
	}

	// No match
	_, _, ok = tr.LongestPrefix("/other")
	if ok {
		t.Error("LongestPrefix /other should return !ok")
	}
}

func TestTrie_AllKeys(t *testing.T) {
	tr := New[int]()
	words := []string{"cat", "car", "dog", "doggy", "zebra"}
	for i, w := range words {
		tr.Insert(w, i)
	}

	keys := tr.AllKeys()
	if len(keys) != len(words) {
		t.Errorf("AllKeys len = %d; want %d", len(keys), len(words))
	}

	// Should be sorted
	for i := 1; i < len(keys); i++ {
		if keys[i-1] > keys[i] {
			t.Errorf("AllKeys not sorted: %v", keys)
			break
		}
	}
}

func TestTrie_Clear(t *testing.T) {
	tr := New[int]()
	tr.Insert("a", 1)
	tr.Insert("b", 2)

	tr.Clear()

	if tr.Len() != 0 {
		t.Errorf("Len after Clear = %d; want 0", tr.Len())
	}
	if tr.Has("a") {
		t.Error("Has(a) after Clear should be false")
	}
}

func TestTrie_FuzzyMatch(t *testing.T) {
	tr := New[int]()
	words := []string{"apple", "application", "apricot", "banana"}
	for i, w := range words {
		tr.Insert(w, i)
	}

	// Prefix match should work
	results := tr.FuzzyMatch("app", 10)
	if len(results) < 2 {
		t.Errorf("FuzzyMatch(app) len = %d; want at least 2", len(results))
	}
}

func TestTrie_CountPrefix(t *testing.T) {
	tr := New[int]()
	tr.Insert("apple", 1)
	tr.Insert("app", 2)
	tr.Insert("banana", 3)

	if n := tr.CountPrefix("ap"); n != 2 {
		t.Errorf("CountPrefix(ap) = %d; want 2", n)
	}
	if n := tr.CountPrefix(""); n != 3 {
		t.Errorf("CountPrefix() = %d; want 3", n)
	}
	if n := tr.CountPrefix("xyz"); n != 0 {
		t.Errorf("CountPrefix(xyz) = %d; want 0", n)
	}
}

func TestTrie_Walk(t *testing.T) {
	tr := New[int]()
	tr.Insert("a", 1)
	tr.Insert("b", 2)
	tr.Insert("c", 3)

	count := 0
	sum := 0
	tr.Walk(func(key string, value int) bool {
		count++
		sum += value
		return true
	})

	if count != 3 {
		t.Errorf("walk count = %d; want 3", count)
	}
	if sum != 6 {
		t.Errorf("walk sum = %d; want 6", sum)
	}
}

func equalStrSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
