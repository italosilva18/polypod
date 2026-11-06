package command

import (
	"sort"
	"strings"
	"sync"

	"github.com/costa/polypod/internal/trie"
)

// CompletionIndex is a trie-backed index for fast command name completion.
// Thread-safe. Falls back to substring/fuzzy match when the prefix is empty.
type CompletionIndex struct {
	mu   sync.RWMutex
	tree *trie.Trie[CompletionEntry]
}

// CompletionEntry is the data stored for each command name.
type CompletionEntry struct {
	Name        string
	Description string
	Category    string
	Aliases     []string
}

// NewCompletionIndex creates an empty completion index.
func NewCompletionIndex() *CompletionIndex {
	return &CompletionIndex{
		tree: trie.New[CompletionEntry](),
	}
}

// Add inserts a command (and its aliases) into the index.
func (ci *CompletionIndex) Add(entry CompletionEntry) {
	ci.mu.Lock()
	defer ci.mu.Unlock()

	key := normalizeKey(entry.Name)
	ci.tree.Insert(key, entry)

	for _, alias := range entry.Aliases {
		aliasKey := normalizeKey(alias)
		if aliasKey != key {
			ci.tree.Insert(aliasKey, entry)
		}
	}
}

// Remove deletes a command by name.
func (ci *CompletionIndex) Remove(name string) bool {
	ci.mu.Lock()
	defer ci.mu.Unlock()
	return ci.tree.Delete(normalizeKey(name))
}

// Complete returns all commands matching the given prefix.
// If prefix starts with "/", the slash is stripped for matching.
// Returns up to limit results sorted by name.
func (ci *CompletionIndex) Complete(prefix string, limit int) []CompletionEntry {
	ci.mu.RLock()
	defer ci.mu.RUnlock()

	prefix = strings.TrimPrefix(prefix, "/")
	prefix = normalizeKey(prefix)

	matches := ci.tree.PrefixSearch(prefix)

	// Dedupe by name (aliases produce duplicates)
	seen := make(map[string]bool, len(matches))
	results := make([]CompletionEntry, 0, len(matches))
	for _, kv := range matches {
		if seen[kv.Value.Name] {
			continue
		}
		seen[kv.Value.Name] = true
		results = append(results, kv.Value)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results
}

// FuzzyMatch falls back to subsequence matching when no prefix match exists.
// Useful for "did you mean?" suggestions on typos.
func (ci *CompletionIndex) FuzzyMatch(input string, limit int) []CompletionEntry {
	ci.mu.RLock()
	defer ci.mu.RUnlock()

	input = strings.TrimPrefix(input, "/")
	input = normalizeKey(input)

	if limit <= 0 {
		limit = 10
	}
	keys := ci.tree.FuzzyMatch(input, limit*2) // oversample

	seen := make(map[string]bool, len(keys))
	results := make([]CompletionEntry, 0, len(keys))

	for _, k := range keys {
		if entry, ok := ci.tree.Get(k); ok {
			if seen[entry.Name] {
				continue
			}
			seen[entry.Name] = true
			results = append(results, entry)
			if len(results) >= limit {
				break
			}
		}
	}
	return results
}

// All returns every command in the index, sorted by name.
func (ci *CompletionIndex) All() []CompletionEntry {
	ci.mu.RLock()
	defer ci.mu.RUnlock()

	pairs := ci.tree.All()
	seen := make(map[string]bool, len(pairs))
	results := make([]CompletionEntry, 0, len(pairs))
	for _, kv := range pairs {
		if seen[kv.Value.Name] {
			continue
		}
		seen[kv.Value.Name] = true
		results = append(results, kv.Value)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})
	return results
}

// Len returns the number of unique command names (not counting aliases).
func (ci *CompletionIndex) Len() int {
	return len(ci.All())
}

// LongestCommonPrefix returns the longest prefix that is shared by all
// commands matching the input. Useful for single-tab completion.
func (ci *CompletionIndex) LongestCommonPrefix(input string) string {
	matches := ci.Complete(input, 0)
	if len(matches) == 0 {
		return ""
	}
	if len(matches) == 1 {
		return matches[0].Name
	}

	first := matches[0].Name
	commonLen := len(first)
	for _, m := range matches[1:] {
		for commonLen > 0 && !strings.HasPrefix(m.Name, first[:commonLen]) {
			commonLen--
		}
		if commonLen == 0 {
			return ""
		}
	}
	return first[:commonLen]
}

// normalizeKey canonicalizes command names for indexing.
func normalizeKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
