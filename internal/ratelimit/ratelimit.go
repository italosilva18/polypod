package ratelimit

import (
	"sync"
	"time"
)

// Limiter implements a per-user token bucket rate limiter.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64 // tokens per second
	burst   int
}

type bucket struct {
	tokens    float64
	lastCheck time.Time
}

// New creates a new rate limiter.
// requestsPerMinute is the sustained rate, burst is the max burst size.
func New(requestsPerMinute, burst int) *Limiter {
	return &Limiter{
		buckets: make(map[string]*bucket),
		rate:    float64(requestsPerMinute) / 60.0,
		burst:   burst,
	}
}

// Allow checks if a request from the given key is allowed.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{
			tokens:    float64(l.burst),
			lastCheck: time.Now(),
		}
		l.buckets[key] = b
	}

	now := time.Now()
	elapsed := now.Sub(b.lastCheck).Seconds()
	b.lastCheck = now

	// Refill tokens
	b.tokens += elapsed * l.rate
	if b.tokens > float64(l.burst) {
		b.tokens = float64(l.burst)
	}

	if b.tokens < 1 {
		return false
	}

	b.tokens--
	return true
}

// Cleanup removes stale buckets older than maxAge.
func (l *Limiter) Cleanup(maxAge time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	for key, b := range l.buckets {
		if b.lastCheck.Before(cutoff) {
			delete(l.buckets, key)
		}
	}
}
