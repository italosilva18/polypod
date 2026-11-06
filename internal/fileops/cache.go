package fileops

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultMaxCacheEntries is the default maximum number of entries in the read cache.
const DefaultMaxCacheEntries = 1000

// DefaultMaxCacheSizeBytes is the default maximum total size of cached content (25 MB).
const DefaultMaxCacheSizeBytes = 25 * 1024 * 1024

// DefaultCacheTTL is the default time-to-live for cache entries.
const DefaultCacheTTL = 5 * time.Minute

// CacheStats tracks cache hit/miss statistics.
type CacheStats struct {
	Hits   atomic.Int64
	Misses atomic.Int64
}

// HitRate returns the cache hit rate as a ratio [0.0, 1.0].
func (s *CacheStats) HitRate() float64 {
	total := s.Hits.Load() + s.Misses.Load()
	if total == 0 {
		return 0
	}
	return float64(s.Hits.Load()) / float64(total)
}

// --- FileReadCache ---

// CachedFileData holds cached file content with modification time for invalidation.
type CachedFileData struct {
	Content string
	MtimeNs int64 // Modification time in nanoseconds for invalidation.
}

// FileReadCache is an LRU cache for file reads with mtime-based invalidation.
type FileReadCache struct {
	mu      sync.Mutex
	cache   map[string]*readCacheEntry
	order   []string // LRU order, most recently used at the end.
	maxSize int
	stats   CacheStats
}

type readCacheEntry struct {
	data      CachedFileData
	accessIdx int // Position in LRU order.
}

// NewFileReadCache creates a new FileReadCache with the given maximum number of entries.
func NewFileReadCache(maxSize int) *FileReadCache {
	if maxSize <= 0 {
		maxSize = DefaultMaxCacheEntries
	}
	return &FileReadCache{
		cache:   make(map[string]*readCacheEntry, maxSize),
		order:   make([]string, 0, maxSize),
		maxSize: maxSize,
	}
}

// ReadFile reads a file with caching. Returns content or error.
// Cache is invalidated automatically when the file modification time changes.
func (c *FileReadCache) ReadFile(filePath string) (string, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		c.mu.Lock()
		delete(c.cache, filePath)
		c.mu.Unlock()
		return "", err
	}

	mtimeNs := info.ModTime().UnixNano()

	c.mu.Lock()
	if entry, ok := c.cache[filePath]; ok && entry.data.MtimeNs == mtimeNs {
		c.touchLocked(filePath)
		c.stats.Hits.Add(1)
		content := entry.data.Content
		c.mu.Unlock()
		return content, nil
	}
	c.mu.Unlock()

	c.stats.Misses.Add(1)

	// Cache miss — read from disk.
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}

	content := string(data)

	c.mu.Lock()
	c.setLocked(filePath, CachedFileData{
		Content: content,
		MtimeNs: mtimeNs,
	})
	c.mu.Unlock()

	return content, nil
}

// Invalidate removes a specific file from the cache.
func (c *FileReadCache) Invalidate(filePath string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeLocked(filePath)
}

// InvalidateAll clears the entire cache.
func (c *FileReadCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = make(map[string]*readCacheEntry, c.maxSize)
	c.order = c.order[:0]
}

// Stats returns the current cache statistics.
func (c *FileReadCache) Stats() *CacheStats {
	return &c.stats
}

// Size returns the current number of entries in the cache.
func (c *FileReadCache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.cache)
}

func (c *FileReadCache) setLocked(key string, data CachedFileData) {
	if _, exists := c.cache[key]; exists {
		c.cache[key].data = data
		c.touchLocked(key)
		return
	}

	// Evict if at capacity.
	for len(c.cache) >= c.maxSize && len(c.order) > 0 {
		oldest := c.order[0]
		c.removeLocked(oldest)
	}

	c.cache[key] = &readCacheEntry{data: data}
	c.order = append(c.order, key)
}

func (c *FileReadCache) touchLocked(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			c.order = append(c.order, key)
			return
		}
	}
}

func (c *FileReadCache) removeLocked(key string) {
	delete(c.cache, key)
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}

// --- FileStateCache ---

// FileState records the known state of a file, including content and metadata.
type FileState struct {
	Content       string
	Timestamp     int64 // Unix milliseconds.
	Offset        int   // Line offset, -1 if not applicable.
	Limit         int   // Line limit, -1 if not applicable.
	IsPartialView bool  // True if content was injected/truncated and doesn't match disk.
}

// FileStateCache is a size-limited LRU cache for file states with path normalization.
type FileStateCache struct {
	mu             sync.Mutex
	entries        map[string]*fsCacheEntry
	order          []string
	maxEntries     int
	maxSizeBytes   int64
	calculatedSize int64
	stats          CacheStats
}

type fsCacheEntry struct {
	state FileState
	size  int64 // Cached byte length of content.
}

// NewFileStateCache creates a new FileStateCache.
func NewFileStateCache(maxEntries int, maxSizeBytes int64) *FileStateCache {
	if maxEntries <= 0 {
		maxEntries = 100
	}
	if maxSizeBytes <= 0 {
		maxSizeBytes = int64(DefaultMaxCacheSizeBytes)
	}
	return &FileStateCache{
		entries:      make(map[string]*fsCacheEntry, maxEntries),
		order:        make([]string, 0, maxEntries),
		maxEntries:   maxEntries,
		maxSizeBytes: maxSizeBytes,
	}
}

func normalizeKey(key string) string {
	return filepath.Clean(key)
}

// Get retrieves a file state from the cache.
func (c *FileStateCache) Get(key string) (FileState, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	nk := normalizeKey(key)
	entry, ok := c.entries[nk]
	if !ok {
		c.stats.Misses.Add(1)
		return FileState{}, false
	}
	c.touchLocked(nk)
	c.stats.Hits.Add(1)
	return entry.state, true
}

// Set adds or updates a file state in the cache.
func (c *FileStateCache) Set(key string, state FileState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	nk := normalizeKey(key)
	entrySize := int64(len(state.Content))
	if entrySize < 1 {
		entrySize = 1
	}

	// Remove old entry if exists.
	if old, exists := c.entries[nk]; exists {
		c.calculatedSize -= old.size
		c.removeOrderLocked(nk)
	}

	// Evict until we have room.
	for (len(c.entries) >= c.maxEntries || c.calculatedSize+entrySize > c.maxSizeBytes) && len(c.order) > 0 {
		oldest := c.order[0]
		if e, ok := c.entries[oldest]; ok {
			c.calculatedSize -= e.size
		}
		delete(c.entries, oldest)
		c.order = c.order[1:]
	}

	c.entries[nk] = &fsCacheEntry{
		state: state,
		size:  entrySize,
	}
	c.order = append(c.order, nk)
	c.calculatedSize += entrySize
}

// Has returns whether the cache contains an entry for the given key.
func (c *FileStateCache) Has(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.entries[normalizeKey(key)]
	return ok
}

// Delete removes a file state from the cache.
func (c *FileStateCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	nk := normalizeKey(key)
	if e, ok := c.entries[nk]; ok {
		c.calculatedSize -= e.size
		delete(c.entries, nk)
		c.removeOrderLocked(nk)
	}
}

// Clear removes all entries from the cache.
func (c *FileStateCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*fsCacheEntry, c.maxEntries)
	c.order = c.order[:0]
	c.calculatedSize = 0
}

// Size returns the number of entries in the cache.
func (c *FileStateCache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// CalculatedSize returns the total byte size of cached content.
func (c *FileStateCache) CalculatedSize() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calculatedSize
}

// Keys returns all cached file paths.
func (c *FileStateCache) Keys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]string, len(c.order))
	copy(keys, c.order)
	return keys
}

// Entries returns all entries as a map.
func (c *FileStateCache) Entries() map[string]FileState {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make(map[string]FileState, len(c.entries))
	for k, v := range c.entries {
		result[k] = v.state
	}
	return result
}

// Clone creates a deep copy of the cache preserving size limits.
func (c *FileStateCache) Clone() *FileStateCache {
	c.mu.Lock()
	defer c.mu.Unlock()

	cloned := NewFileStateCache(c.maxEntries, c.maxSizeBytes)
	for _, k := range c.order {
		if e, ok := c.entries[k]; ok {
			cloned.entries[k] = &fsCacheEntry{
				state: e.state,
				size:  e.size,
			}
			cloned.order = append(cloned.order, k)
			cloned.calculatedSize += e.size
		}
	}
	return cloned
}

// Merge merges another cache into this one, with newer entries (by timestamp) winning.
func (c *FileStateCache) Merge(other *FileStateCache) {
	other.mu.Lock()
	otherEntries := make(map[string]FileState, len(other.entries))
	for k, v := range other.entries {
		otherEntries[k] = v.state
	}
	other.mu.Unlock()

	for k, state := range otherEntries {
		existing, ok := c.Get(k)
		if !ok || state.Timestamp > existing.Timestamp {
			c.Set(k, state)
		}
	}
}

func (c *FileStateCache) touchLocked(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			c.order = append(c.order, key)
			return
		}
	}
}

func (c *FileStateCache) removeOrderLocked(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}
