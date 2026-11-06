// Package lrucache provides a generic LRU cache with optional TTL support.
package lrucache

import (
	"container/list"
	"sync"
	"time"
)

// Cache is a thread-safe LRU cache.
type Cache[K comparable, V any] struct {
	mu       sync.Mutex
	capacity int
	ttl      time.Duration
	items    map[K]*list.Element
	order    *list.List
	onEvict  func(K, V)

	// Stats
	hits    int64
	misses  int64
	evicted int64
}

type entry[K comparable, V any] struct {
	key       K
	value     V
	expiresAt time.Time // zero means no expiry
}

// New creates a new cache with the given capacity.
// If ttl > 0, entries expire after that duration.
func New[K comparable, V any](capacity int, ttl time.Duration) *Cache[K, V] {
	if capacity <= 0 {
		capacity = 1000
	}
	return &Cache[K, V]{
		capacity: capacity,
		ttl:      ttl,
		items:    make(map[K]*list.Element, capacity),
		order:    list.New(),
	}
}

// Get retrieves a value from the cache.
// Returns the value and whether it was found and still valid.
func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		c.misses++
		var zero V
		return zero, false
	}

	ent := elem.Value.(*entry[K, V])

	// Check expiry
	if !ent.expiresAt.IsZero() && time.Now().After(ent.expiresAt) {
		c.removeElement(elem)
		c.misses++
		var zero V
		return zero, false
	}

	// Move to front (most recently used)
	c.order.MoveToFront(elem)
	c.hits++
	return ent.value, true
}

// Set adds or updates a value in the cache.
func (c *Cache[K, V]) Set(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Update existing
	if elem, ok := c.items[key]; ok {
		ent := elem.Value.(*entry[K, V])
		ent.value = value
		ent.expiresAt = c.computeExpiry()
		c.order.MoveToFront(elem)
		return
	}

	// Add new
	ent := &entry[K, V]{
		key:       key,
		value:     value,
		expiresAt: c.computeExpiry(),
	}
	elem := c.order.PushFront(ent)
	c.items[key] = elem

	// Evict if over capacity
	for c.order.Len() > c.capacity {
		oldest := c.order.Back()
		if oldest != nil {
			c.removeElement(oldest)
			c.evicted++
		}
	}
}

// SetWithTTL adds a value with a custom TTL that overrides the cache-wide TTL.
func (c *Cache[K, V]) SetWithTTL(key K, value V, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	expiresAt := time.Time{}
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}

	if elem, ok := c.items[key]; ok {
		ent := elem.Value.(*entry[K, V])
		ent.value = value
		ent.expiresAt = expiresAt
		c.order.MoveToFront(elem)
		return
	}

	ent := &entry[K, V]{
		key:       key,
		value:     value,
		expiresAt: expiresAt,
	}
	elem := c.order.PushFront(ent)
	c.items[key] = elem

	for c.order.Len() > c.capacity {
		oldest := c.order.Back()
		if oldest != nil {
			c.removeElement(oldest)
			c.evicted++
		}
	}
}

// Delete removes a key from the cache.
func (c *Cache[K, V]) Delete(key K) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		return false
	}
	c.removeElement(elem)
	return true
}

// Has checks if a key is present (without updating LRU order).
func (c *Cache[K, V]) Has(key K) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		return false
	}
	ent := elem.Value.(*entry[K, V])
	if !ent.expiresAt.IsZero() && time.Now().After(ent.expiresAt) {
		return false
	}
	return true
}

// Len returns the number of items currently in the cache.
func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

// Capacity returns the max capacity.
func (c *Cache[K, V]) Capacity() int {
	return c.capacity
}

// Clear removes all items from the cache.
func (c *Cache[K, V]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.onEvict != nil {
		for _, elem := range c.items {
			ent := elem.Value.(*entry[K, V])
			c.onEvict(ent.key, ent.value)
		}
	}
	c.items = make(map[K]*list.Element, c.capacity)
	c.order.Init()
}

// OnEvict sets the eviction callback.
func (c *Cache[K, V]) OnEvict(fn func(K, V)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onEvict = fn
}

// Keys returns all current keys in LRU order (most recent first).
func (c *Cache[K, V]) Keys() []K {
	c.mu.Lock()
	defer c.mu.Unlock()

	keys := make([]K, 0, c.order.Len())
	for elem := c.order.Front(); elem != nil; elem = elem.Next() {
		ent := elem.Value.(*entry[K, V])
		keys = append(keys, ent.key)
	}
	return keys
}

// Stats returns cache statistics.
func (c *Cache[K, V]) Stats() (hits, misses, evicted int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.misses, c.evicted
}

// HitRate returns the cache hit rate (0-1).
func (c *Cache[K, V]) HitRate() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := c.hits + c.misses
	if total == 0 {
		return 0
	}
	return float64(c.hits) / float64(total)
}

// Prune removes expired entries.
// Returns the number of entries removed.
func (c *Cache[K, V]) Prune() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ttl <= 0 {
		return 0
	}

	now := time.Now()
	removed := 0

	// Walk from back (oldest) forward
	for elem := c.order.Back(); elem != nil; {
		prev := elem.Prev()
		ent := elem.Value.(*entry[K, V])
		if !ent.expiresAt.IsZero() && now.After(ent.expiresAt) {
			c.removeElement(elem)
			removed++
		}
		elem = prev
	}
	return removed
}

// removeElement removes an element from both the map and list.
// Caller must hold the lock.
func (c *Cache[K, V]) removeElement(elem *list.Element) {
	ent := elem.Value.(*entry[K, V])
	c.order.Remove(elem)
	delete(c.items, ent.key)
	if c.onEvict != nil {
		c.onEvict(ent.key, ent.value)
	}
}

func (c *Cache[K, V]) computeExpiry() time.Time {
	if c.ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(c.ttl)
}

// Resize changes the capacity, evicting if necessary.
func (c *Cache[K, V]) Resize(newCapacity int) {
	if newCapacity <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.capacity = newCapacity
	for c.order.Len() > c.capacity {
		oldest := c.order.Back()
		if oldest != nil {
			c.removeElement(oldest)
			c.evicted++
		}
	}
}
