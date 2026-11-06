// Package ratelimit provides token bucket and sliding window rate limiters.
//
// TokenBucket implements a classic token bucket with continuous refill,
// suitable for controlling burst traffic. SlidingWindow implements a
// sliding window counter for sustained rate limiting over a time window.
// RateLimiter combines both: the token bucket gates bursts while the
// sliding window enforces a sustained request rate.
package ratelimit

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"time"
)

// LimiterConfig configures a RateLimiter.
type LimiterConfig struct {
	RequestsPerMinute int          // sliding window max requests per minute
	TokensPerMinute   int          // token bucket refill rate (tokens/min)
	BurstSize         int          // token bucket capacity (max burst)
	Logger            *slog.Logger // optional; nil disables logging
}

// ---------------------------------------------------------------------------
// TokenBucket
// ---------------------------------------------------------------------------

// TokenBucket implements a classic token bucket algorithm.
// Tokens are added at a constant rate up to a maximum capacity.
// Each Allow / AllowN call consumes tokens; if insufficient tokens
// are available the call returns false (or Wait blocks).
type TokenBucket struct {
	mu       sync.Mutex
	tokens   float64   // current token count (fractional for smooth refill)
	capacity int       // max tokens the bucket can hold
	rate     float64   // tokens added per second
	lastTime time.Time // last refill timestamp
}

// NewTokenBucket creates a TokenBucket that refills at rate tokens/second
// with the given maximum capacity. The bucket starts full.
func NewTokenBucket(rate float64, capacity int) *TokenBucket {
	if capacity < 1 {
		capacity = 1
	}
	if rate <= 0 {
		rate = 1
	}
	return &TokenBucket{
		tokens:   float64(capacity),
		capacity: capacity,
		rate:     rate,
		lastTime: time.Now(),
	}
}

// refill adds tokens accumulated since the last call. Must be called
// with mu held.
func (tb *TokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastTime).Seconds()
	if elapsed <= 0 {
		return
	}
	tb.tokens += elapsed * tb.rate
	if tb.tokens > float64(tb.capacity) {
		tb.tokens = float64(tb.capacity)
	}
	tb.lastTime = now
}

// Allow tries to consume 1 token. Returns true on success.
func (tb *TokenBucket) Allow() bool {
	return tb.AllowN(1)
}

// AllowN tries to consume n tokens. Returns true if enough tokens
// were available and consumed.
func (tb *TokenBucket) AllowN(n int) bool {
	if n <= 0 {
		return true
	}
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.refill()
	if tb.tokens >= float64(n) {
		tb.tokens -= float64(n)
		return true
	}
	return false
}

// Wait blocks until at least one token is available or ctx is cancelled.
func (tb *TokenBucket) Wait(ctx context.Context) error {
	for {
		tb.mu.Lock()
		tb.refill()
		if tb.tokens >= 1 {
			tb.tokens--
			tb.mu.Unlock()
			return nil
		}
		// Calculate how long until the next token arrives.
		deficit := 1.0 - tb.tokens
		waitSec := deficit / tb.rate
		tb.mu.Unlock()

		// Clamp minimum wait to avoid busy-spinning on float rounding.
		wait := time.Duration(math.Ceil(waitSec*1000)) * time.Millisecond
		if wait < time.Millisecond {
			wait = time.Millisecond
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
			// Loop back to try consuming.
		}
	}
}

// Remaining returns the number of whole tokens currently available.
func (tb *TokenBucket) Remaining() int {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.refill()
	return int(tb.tokens)
}

// ---------------------------------------------------------------------------
// SlidingWindow
// ---------------------------------------------------------------------------

// SlidingWindow implements a sliding window counter. It tracks timestamps
// of events within a configurable window and rejects events once the count
// reaches maxRequests.
type SlidingWindow struct {
	mu          sync.Mutex
	timestamps  []time.Time
	windowSize  time.Duration
	maxRequests int
}

// NewSlidingWindow creates a SlidingWindow that allows at most maxRequests
// events within windowSize.
func NewSlidingWindow(windowSize time.Duration, maxRequests int) *SlidingWindow {
	if maxRequests < 1 {
		maxRequests = 1
	}
	if windowSize <= 0 {
		windowSize = time.Minute
	}
	return &SlidingWindow{
		timestamps:  make([]time.Time, 0, maxRequests),
		windowSize:  windowSize,
		maxRequests: maxRequests,
	}
}

// prune removes timestamps older than the window. Must be called with mu held.
func (sw *SlidingWindow) prune() {
	cutoff := time.Now().Add(-sw.windowSize)
	i := 0
	for i < len(sw.timestamps) && sw.timestamps[i].Before(cutoff) {
		i++
	}
	if i > 0 {
		sw.timestamps = sw.timestamps[i:]
	}
}

// Allow records an event if the window has capacity, returning true.
// If the window is full, it returns false without recording.
func (sw *SlidingWindow) Allow() bool {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	sw.prune()
	if len(sw.timestamps) >= sw.maxRequests {
		return false
	}
	sw.timestamps = append(sw.timestamps, time.Now())
	return true
}

// Count returns the number of events currently in the window.
func (sw *SlidingWindow) Count() int {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	sw.prune()
	return len(sw.timestamps)
}

// Reset clears all recorded timestamps.
func (sw *SlidingWindow) Reset() {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	sw.timestamps = sw.timestamps[:0]
}

// ---------------------------------------------------------------------------
// RateLimiter — combines TokenBucket (burst) + SlidingWindow (sustained)
// ---------------------------------------------------------------------------

// RateLimiter combines a TokenBucket for burst control with a SlidingWindow
// for sustained rate enforcement. A request must be allowed by both to proceed.
type RateLimiter struct {
	bucket *TokenBucket
	window *SlidingWindow
	logger *slog.Logger
}

// NewRateLimiter creates a RateLimiter from cfg.
//
//   - TokenBucket rate = TokensPerMinute / 60 tokens/sec, capacity = BurstSize.
//   - SlidingWindow: 1-minute window, max = RequestsPerMinute.
//
// If BurstSize is zero it defaults to RequestsPerMinute.
// If TokensPerMinute is zero the token bucket is effectively unlimited.
func NewRateLimiter(cfg LimiterConfig) *RateLimiter {
	burst := cfg.BurstSize
	if burst <= 0 {
		burst = cfg.RequestsPerMinute
	}
	if burst <= 0 {
		burst = 1
	}

	// Token bucket refill rate (tokens per second).
	var tbRate float64
	if cfg.TokensPerMinute > 0 {
		tbRate = float64(cfg.TokensPerMinute) / 60.0
	} else {
		// If not specified, derive from requests-per-minute so the bucket
		// rate-limits requests rather than tokens.
		tbRate = float64(cfg.RequestsPerMinute) / 60.0
		if tbRate <= 0 {
			tbRate = 1
		}
	}

	rpm := cfg.RequestsPerMinute
	if rpm <= 0 {
		rpm = 1
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &RateLimiter{
		bucket: NewTokenBucket(tbRate, burst),
		window: NewSlidingWindow(time.Minute, rpm),
		logger: logger,
	}
}

// Allow returns true if the request is permitted by both the token bucket
// and the sliding window. On rejection it logs a debug message.
func (rl *RateLimiter) Allow() bool {
	if !rl.bucket.Allow() {
		rl.logger.Debug("rate limit: token bucket exhausted")
		return false
	}
	if !rl.window.Allow() {
		rl.logger.Debug("rate limit: sliding window full")
		return false
	}
	return true
}

// Wait blocks until a token is available in the bucket, then checks the
// sliding window. If the window rejects the request, Wait retries after
// a short back-off until ctx is cancelled.
func (rl *RateLimiter) Wait(ctx context.Context) error {
	for {
		if err := rl.bucket.Wait(ctx); err != nil {
			return err
		}
		if rl.window.Allow() {
			return nil
		}
		// Sliding window full — wait a bit and retry.
		rl.logger.Debug("rate limit: sliding window full, backing off")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Stats returns the number of remaining tokens in the bucket and the
// estimated time at which the sliding window resets (i.e. the oldest
// entry leaves the window).
func (rl *RateLimiter) Stats() (remaining int, resetAt time.Time) {
	remaining = rl.bucket.Remaining()

	rl.window.mu.Lock()
	defer rl.window.mu.Unlock()
	rl.window.prune()
	if len(rl.window.timestamps) > 0 {
		// The oldest entry exits the window at this time.
		resetAt = rl.window.timestamps[0].Add(rl.window.windowSize)
	} else {
		resetAt = time.Now()
	}
	return
}
