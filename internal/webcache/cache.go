package webcache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CachedResult holds a cached web search or fetch result.
type CachedResult struct {
	Query     string    `json:"query"`
	URL       string    `json:"url,omitempty"`
	Result    string    `json:"result"`
	CachedAt  time.Time `json:"cached_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Provider  string    `json:"provider"`
}

// Cache stores web results on disk with TTL.
type Cache struct {
	mu       sync.RWMutex
	dir      string
	ttl      time.Duration
	entries  map[string]*CachedResult
	maxSize  int
}

// New creates a web cache.
func New(cacheDir string, ttl time.Duration) *Cache {
	if ttl == 0 {
		ttl = 1 * time.Hour
	}
	dir := filepath.Join(cacheDir, "webcache")
	os.MkdirAll(dir, 0755)
	c := &Cache{
		dir:     dir,
		ttl:     ttl,
		entries: make(map[string]*CachedResult),
		maxSize: 500,
	}
	c.loadIndex()
	return c
}

// Get retrieves a cached result. Returns nil if not found or expired.
func (c *Cache) Get(key string) *CachedResult {
	c.mu.RLock()
	defer c.mu.RUnlock()

	hash := hashKey(key)
	entry, ok := c.entries[hash]
	if !ok {
		return nil
	}
	if time.Now().After(entry.ExpiresAt) {
		return nil // expired
	}
	return entry
}

// Set stores a result in the cache.
func (c *Cache) Set(key string, result CachedResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	hash := hashKey(key)
	result.CachedAt = time.Now()
	if result.ExpiresAt.IsZero() {
		result.ExpiresAt = time.Now().Add(c.ttl)
	}

	c.entries[hash] = &result

	// Evict oldest if over limit
	if len(c.entries) > c.maxSize {
		c.evictOldest()
	}

	// Persist to disk
	data, _ := json.Marshal(result)
	os.WriteFile(filepath.Join(c.dir, hash+".json"), data, 0644)
	c.saveIndex()
}

// Clear removes all cached results.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*CachedResult)
	os.RemoveAll(c.dir)
	os.MkdirAll(c.dir, 0755)
}

// Stats returns cache statistics.
func (c *Cache) Stats() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	total := len(c.entries)
	expired := 0
	now := time.Now()
	for _, e := range c.entries {
		if now.After(e.ExpiresAt) {
			expired++
		}
	}

	return fmt.Sprintf("Cache: %d entradas (%d expiradas), TTL: %s, dir: %s",
		total, expired, c.ttl, c.dir)
}

func (c *Cache) loadIndex() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" || e.Name() == "index.json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(c.dir, e.Name()))
		if err != nil {
			continue
		}
		var result CachedResult
		if err := json.Unmarshal(data, &result); err != nil {
			continue
		}
		hash := e.Name()[:len(e.Name())-5] // remove .json
		c.entries[hash] = &result
	}
}

func (c *Cache) saveIndex() {
	type indexEntry struct {
		Hash    string    `json:"hash"`
		Query   string    `json:"query"`
		Expires time.Time `json:"expires"`
	}
	var index []indexEntry
	for hash, entry := range c.entries {
		index = append(index, indexEntry{hash, entry.Query, entry.ExpiresAt})
	}
	data, _ := json.Marshal(index)
	os.WriteFile(filepath.Join(c.dir, "index.json"), data, 0644)
}

func (c *Cache) evictOldest() {
	var oldestHash string
	var oldestTime time.Time
	first := true

	for hash, entry := range c.entries {
		if first || entry.CachedAt.Before(oldestTime) {
			oldestHash = hash
			oldestTime = entry.CachedAt
			first = false
		}
	}

	if oldestHash != "" {
		delete(c.entries, oldestHash)
		os.Remove(filepath.Join(c.dir, oldestHash+".json"))
	}
}

func hashKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:12])
}
