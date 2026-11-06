package subagent

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"sync"
	"time"
)

// PromptCacheEntry holds one cached system-prompt blob together with its
// access statistics.
type PromptCacheEntry struct {
	// Hash is the hex-encoded SHA-256 of Content. It is used as the map key.
	Hash string

	// Content is the raw system-prompt text that was cached.
	Content string

	// HitCount is incremented on every Get that finds this entry.
	HitCount int

	// CreatedAt is set once, when the entry is first stored by Put.
	CreatedAt time.Time
}

// PromptCache is a thread-safe, in-process store of serialized system prompts
// keyed by their SHA-256 hash. Repeated fork calls that share the same persona
// text will reuse the cached entry instead of re-hashing and re-transmitting
// the bytes.
type PromptCache struct {
	mu      sync.Mutex
	entries map[string]*PromptCacheEntry
}

// NewPromptCache allocates a ready-to-use PromptCache.
func NewPromptCache() *PromptCache {
	return &PromptCache{
		entries: make(map[string]*PromptCacheEntry),
	}
}

// hashContent returns the hex-encoded SHA-256 of content.
func hashContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// Get looks up content in the cache.  If found, HitCount is incremented and
// the updated entry is returned with hit == true.  If not found, the zero
// PromptCacheEntry is returned with hit == false.
func (c *PromptCache) Get(content string) (entry PromptCacheEntry, hit bool) {
	h := hashContent(content)

	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[h]
	if !ok {
		return PromptCacheEntry{}, false
	}
	e.HitCount++
	return *e, true
}

// Put stores content in the cache if it is not already present, then returns
// the (possibly pre-existing) entry.  Put is idempotent: calling it twice with
// the same content returns the same entry both times (HitCount is not
// incremented by Put itself — only Get increments it).
func (c *PromptCache) Put(content string) PromptCacheEntry {
	h := hashContent(content)

	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.entries[h]; ok {
		return *e
	}
	e := &PromptCacheEntry{
		Hash:      h,
		Content:   content,
		HitCount:  0,
		CreatedAt: time.Now(),
	}
	c.entries[h] = e
	return *e
}

// Stats returns the number of distinct cached entries and the sum of all
// HitCount values across those entries.
func (c *PromptCache) Stats() (entries int, totalHits int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, e := range c.entries {
		totalHits += e.HitCount
	}
	return len(c.entries), totalHits
}

// Evict removes the least-recently-used entries so that at most maxEntries
// remain.  "Least-recently-used" is approximated by sorting ascending on
// HitCount (fewer hits → evicted first); ties are broken by CreatedAt
// ascending (older entries evicted first).
//
// If the cache already holds maxEntries or fewer entries, Evict is a no-op.
// A maxEntries value ≤ 0 clears the entire cache.
func (c *PromptCache) Evict(maxEntries int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if maxEntries <= 0 {
		c.entries = make(map[string]*PromptCacheEntry)
		return
	}

	if len(c.entries) <= maxEntries {
		return
	}

	// Collect pointers, sort by (HitCount asc, CreatedAt asc).
	all := make([]*PromptCacheEntry, 0, len(c.entries))
	for _, e := range c.entries {
		all = append(all, e)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].HitCount != all[j].HitCount {
			return all[i].HitCount < all[j].HitCount
		}
		return all[i].CreatedAt.Before(all[j].CreatedAt)
	})

	// Drop entries from the front of the sorted slice until we are at budget.
	toRemove := len(all) - maxEntries
	for i := 0; i < toRemove; i++ {
		delete(c.entries, all[i].Hash)
	}
}

// globalPromptCache is the package-level singleton used by default callers.
var globalPromptCache = NewPromptCache()

// GetPromptCache returns the package-level PromptCache singleton.  Callers
// that need an isolated cache (e.g. tests) should create their own via
// NewPromptCache instead.
func GetPromptCache() *PromptCache {
	return globalPromptCache
}
