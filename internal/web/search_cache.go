package web

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/lrucache"
)

// searchCache caches recent search results by query.
// Each entry expires after 10 minutes to avoid stale data.
var (
	searchCacheOnce sync.Once
	searchCache     *lrucache.Cache[string, []SearchResult]
)

// getSearchCache lazily initializes the shared cache.
func getSearchCache() *lrucache.Cache[string, []SearchResult] {
	searchCacheOnce.Do(func() {
		searchCache = lrucache.New[string, []SearchResult](256, 10*time.Minute)
	})
	return searchCache
}

// SearchCached runs Search but checks the LRU cache first.
// Cache key is a normalized form of the query + maxResults.
func SearchCached(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	cache := getSearchCache()
	key := normalizeQuery(query, maxResults)

	if cached, ok := cache.Get(key); ok {
		// Return a copy so callers can't mutate the cached slice.
		out := make([]SearchResult, len(cached))
		copy(out, cached)
		return out, nil
	}

	results, err := Search(ctx, query, maxResults)
	if err != nil {
		return nil, err
	}

	// Store a copy
	stored := make([]SearchResult, len(results))
	copy(stored, results)
	cache.Set(key, stored)

	return results, nil
}

// ClearSearchCache empties the cache.
func ClearSearchCache() {
	getSearchCache().Clear()
}

// SearchCacheStats returns cache hit/miss statistics.
func SearchCacheStats() (hits, misses, evicted int64, hitRate float64) {
	c := getSearchCache()
	hits, misses, evicted = c.Stats()
	hitRate = c.HitRate()
	return
}

// SearchCacheSize returns the current number of cached entries.
func SearchCacheSize() int {
	return getSearchCache().Len()
}

// PruneExpiredSearches removes expired entries.
func PruneExpiredSearches() int {
	return getSearchCache().Prune()
}

// normalizeQuery produces a stable cache key.
func normalizeQuery(query string, maxResults int) string {
	normalized := strings.ToLower(strings.TrimSpace(query))
	return buildCacheKey(normalized, maxResults)
}

func buildCacheKey(query string, n int) string {
	var sb strings.Builder
	sb.WriteString(query)
	sb.WriteByte(0)
	sb.WriteString(strconv.Itoa(n))
	return sb.String()
}
