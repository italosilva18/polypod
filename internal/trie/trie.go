// Package trie provides a generic prefix trie data structure.
package trie

import (
	"sort"
	"sync"
)

// Trie is a thread-safe prefix tree.
type Trie[V any] struct {
	mu   sync.RWMutex
	root *node[V]
	size int
}

type node[V any] struct {
	children map[rune]*node[V]
	value    V
	isEnd    bool
}

// New creates an empty trie.
func New[V any]() *Trie[V] {
	return &Trie[V]{
		root: &node[V]{children: make(map[rune]*node[V])},
	}
}

// Insert adds a key-value pair to the trie.
// If the key already exists, its value is replaced.
func (t *Trie[V]) Insert(key string, value V) {
	t.mu.Lock()
	defer t.mu.Unlock()

	cur := t.root
	for _, r := range key {
		next, ok := cur.children[r]
		if !ok {
			next = &node[V]{children: make(map[rune]*node[V])}
			cur.children[r] = next
		}
		cur = next
	}
	if !cur.isEnd {
		t.size++
	}
	cur.isEnd = true
	cur.value = value
}

// Get returns the value for an exact key match.
func (t *Trie[V]) Get(key string) (V, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	n := t.findNode(key)
	if n == nil || !n.isEnd {
		var zero V
		return zero, false
	}
	return n.value, true
}

// Has returns true if the trie contains the exact key.
func (t *Trie[V]) Has(key string) bool {
	_, ok := t.Get(key)
	return ok
}

// Delete removes a key from the trie.
// Returns true if the key was present.
func (t *Trie[V]) Delete(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	n := t.findNode(key)
	if n == nil || !n.isEnd {
		return false
	}
	n.isEnd = false
	var zero V
	n.value = zero
	t.size--

	// Note: we don't prune empty branches for simplicity
	// (would require parent pointers or recursive cleanup)
	return true
}

// Len returns the number of keys in the trie.
func (t *Trie[V]) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.size
}

// PrefixKeys returns all keys with the given prefix.
func (t *Trie[V]) PrefixKeys(prefix string) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	n := t.findNode(prefix)
	if n == nil {
		return nil
	}

	var results []string
	t.collectKeys(n, prefix, &results)
	sort.Strings(results)
	return results
}

// PrefixSearch returns all key-value pairs with the given prefix.
func (t *Trie[V]) PrefixSearch(prefix string) []KeyValue[V] {
	t.mu.RLock()
	defer t.mu.RUnlock()

	n := t.findNode(prefix)
	if n == nil {
		return nil
	}

	var results []KeyValue[V]
	t.collectKV(n, prefix, &results)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Key < results[j].Key
	})
	return results
}

// KeyValue is a key-value pair.
type KeyValue[V any] struct {
	Key   string
	Value V
}

// LongestPrefix returns the longest prefix of s that is a key in the trie.
// Useful for routing or longest-match lookup.
func (t *Trie[V]) LongestPrefix(s string) (string, V, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var zero V
	cur := t.root
	var lastMatch struct {
		end   int
		value V
		found bool
	}

	for i, r := range s {
		next, ok := cur.children[r]
		if !ok {
			break
		}
		cur = next
		if cur.isEnd {
			lastMatch.end = i + len(string(r))
			lastMatch.value = cur.value
			lastMatch.found = true
		}
	}

	if !lastMatch.found {
		return "", zero, false
	}
	return s[:lastMatch.end], lastMatch.value, true
}

// AllKeys returns every key in the trie, sorted.
func (t *Trie[V]) AllKeys() []string {
	return t.PrefixKeys("")
}

// All returns every key-value pair in the trie, sorted by key.
func (t *Trie[V]) All() []KeyValue[V] {
	return t.PrefixSearch("")
}

// Clear removes all entries from the trie.
func (t *Trie[V]) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.root = &node[V]{children: make(map[rune]*node[V])}
	t.size = 0
}

// findNode walks the trie to the node for key (or nil if path doesn't exist).
// Caller must hold appropriate lock.
func (t *Trie[V]) findNode(key string) *node[V] {
	cur := t.root
	for _, r := range key {
		next, ok := cur.children[r]
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

// collectKeys does a DFS from n, gathering all key endings.
func (t *Trie[V]) collectKeys(n *node[V], prefix string, out *[]string) {
	if n.isEnd {
		*out = append(*out, prefix)
	}
	for r, child := range n.children {
		t.collectKeys(child, prefix+string(r), out)
	}
}

// collectKV does a DFS from n, gathering all key-value pairs.
func (t *Trie[V]) collectKV(n *node[V], prefix string, out *[]KeyValue[V]) {
	if n.isEnd {
		*out = append(*out, KeyValue[V]{Key: prefix, Value: n.value})
	}
	for r, child := range n.children {
		t.collectKV(child, prefix+string(r), out)
	}
}

// FuzzyMatch returns up to limit keys that approximately match the input.
// Uses simple substring matching after exact prefix matching.
func (t *Trie[V]) FuzzyMatch(input string, limit int) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if limit <= 0 {
		limit = 10
	}

	// First try exact prefix match
	prefixMatches := t.PrefixKeys(input)
	if len(prefixMatches) >= limit {
		return prefixMatches[:limit]
	}

	// Then add substring matches from all keys
	allKeys := t.AllKeys()
	seen := make(map[string]bool, len(prefixMatches))
	for _, k := range prefixMatches {
		seen[k] = true
	}

	results := make([]string, len(prefixMatches))
	copy(results, prefixMatches)

	for _, k := range allKeys {
		if seen[k] {
			continue
		}
		if containsSubsequence(k, input) {
			results = append(results, k)
			seen[k] = true
			if len(results) >= limit {
				break
			}
		}
	}
	return results
}

// containsSubsequence returns true if all chars of needle appear in haystack in order.
func containsSubsequence(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	needleIdx := 0
	needleRunes := []rune(needle)
	for _, r := range haystack {
		if needleIdx < len(needleRunes) && r == needleRunes[needleIdx] {
			needleIdx++
			if needleIdx == len(needleRunes) {
				return true
			}
		}
	}
	return false
}

// CountPrefix returns the number of keys with the given prefix.
func (t *Trie[V]) CountPrefix(prefix string) int {
	t.mu.RLock()
	defer t.mu.RUnlock()

	n := t.findNode(prefix)
	if n == nil {
		return 0
	}
	return countKeys(n)
}

func countKeys[V any](n *node[V]) int {
	count := 0
	if n.isEnd {
		count++
	}
	for _, c := range n.children {
		count += countKeys(c)
	}
	return count
}

// Walk calls fn for each key-value pair in the trie.
func (t *Trie[V]) Walk(fn func(key string, value V) bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	walkNode(t.root, "", fn)
}

func walkNode[V any](n *node[V], prefix string, fn func(string, V) bool) bool {
	if n.isEnd {
		if !fn(prefix, n.value) {
			return false
		}
	}
	for r, child := range n.children {
		if !walkNode(child, prefix+string(r), fn) {
			return false
		}
	}
	return true
}
