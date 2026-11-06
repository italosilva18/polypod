// Package withretry provides a generic retry helper with exponential backoff.
package withretry

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"
)

// Options configures retry behavior.
type Options struct {
	// MaxAttempts is the total number of attempts (default 3).
	MaxAttempts int
	// BaseDelay is the initial delay between attempts (default 100ms).
	BaseDelay time.Duration
	// MaxDelay caps the delay between attempts (default 30s).
	MaxDelay time.Duration
	// Multiplier for exponential backoff (default 2.0).
	Multiplier float64
	// Jitter adds randomness (0-1, default 0.1 = ±10%).
	Jitter float64
	// ShouldRetry decides whether to retry on a given error.
	// If nil, any non-nil error is retried.
	ShouldRetry func(err error, attempt int) bool
	// OnRetry is called before each retry (not before the first attempt).
	OnRetry func(err error, attempt int, delay time.Duration)
	// OnGiveUp is called once when all attempts fail.
	OnGiveUp func(err error, totalAttempts int)
}

// DefaultOptions returns sensible defaults.
func DefaultOptions() Options {
	return Options{
		MaxAttempts: 3,
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    30 * time.Second,
		Multiplier:  2.0,
		Jitter:      0.1,
	}
}

// Do runs fn with retry logic.
func Do[T any](ctx context.Context, opts Options, fn func(ctx context.Context) (T, error)) (T, error) {
	opts = mergeDefaults(opts)

	var zero T
	var lastErr error

	for attempt := 1; attempt <= opts.MaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}

		result, err := fn(ctx)
		if err == nil {
			return result, nil
		}
		lastErr = err

		// Last attempt - don't retry
		if attempt >= opts.MaxAttempts {
			break
		}

		// Check if we should retry
		if opts.ShouldRetry != nil && !opts.ShouldRetry(err, attempt) {
			break
		}

		// Compute delay
		delay := computeDelay(opts, attempt)

		// Fire OnRetry callback
		if opts.OnRetry != nil {
			opts.OnRetry(err, attempt, delay)
		}

		// Sleep with context cancellation
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(delay):
		}
	}

	// Give up
	if opts.OnGiveUp != nil {
		opts.OnGiveUp(lastErr, opts.MaxAttempts)
	}
	return zero, fmt.Errorf("failed after %d attempts: %w", opts.MaxAttempts, lastErr)
}

// DoVoid is a convenience wrapper for functions that don't return a value.
func DoVoid(ctx context.Context, opts Options, fn func(ctx context.Context) error) error {
	_, err := Do(ctx, opts, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, fn(ctx)
	})
	return err
}

// Forever retries indefinitely (until context is cancelled or fn succeeds).
func Forever[T any](ctx context.Context, opts Options, fn func(ctx context.Context) (T, error)) (T, error) {
	opts = mergeDefaults(opts)
	opts.MaxAttempts = 0 // disable limit in Do loop below

	var zero T
	attempt := 0
	for {
		attempt++
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}

		result, err := fn(ctx)
		if err == nil {
			return result, nil
		}

		if opts.ShouldRetry != nil && !opts.ShouldRetry(err, attempt) {
			return zero, err
		}

		delay := computeDelay(opts, attempt)
		if opts.OnRetry != nil {
			opts.OnRetry(err, attempt, delay)
		}

		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(delay):
		}
	}
}

// IsRetryable returns a ShouldRetry function for specific error types.
func IsRetryable(retryableErrors ...error) func(err error, attempt int) bool {
	return func(err error, attempt int) bool {
		for _, re := range retryableErrors {
			if errors.Is(err, re) {
				return true
			}
		}
		return false
	}
}

// NotRetryable returns a ShouldRetry function that excludes certain errors.
func NotRetryable(nonRetryable ...error) func(err error, attempt int) bool {
	return func(err error, attempt int) bool {
		for _, nr := range nonRetryable {
			if errors.Is(err, nr) {
				return false
			}
		}
		return true
	}
}

// computeDelay calculates the backoff delay for a given attempt (1-indexed).
func computeDelay(opts Options, attempt int) time.Duration {
	// attempt 1 → base delay
	// attempt 2 → base * multiplier
	// attempt 3 → base * multiplier^2
	// ...
	delay := float64(opts.BaseDelay)
	for i := 1; i < attempt; i++ {
		delay *= opts.Multiplier
	}

	// Cap
	if delay > float64(opts.MaxDelay) {
		delay = float64(opts.MaxDelay)
	}

	// Jitter
	if opts.Jitter > 0 {
		jitterRange := delay * opts.Jitter
		jitter := (rand.Float64()*2 - 1) * jitterRange
		delay += jitter
	}

	if delay < 0 {
		delay = 0
	}
	return time.Duration(delay)
}

func mergeDefaults(opts Options) Options {
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 3
	}
	if opts.BaseDelay <= 0 {
		opts.BaseDelay = 100 * time.Millisecond
	}
	if opts.MaxDelay <= 0 {
		opts.MaxDelay = 30 * time.Second
	}
	if opts.Multiplier <= 0 {
		opts.Multiplier = 2.0
	}
	if opts.Jitter < 0 {
		opts.Jitter = 0
	}
	return opts
}

// Stats holds retry execution statistics.
type Stats struct {
	Attempts   int
	TotalDelay time.Duration
	LastError  error
	Succeeded  bool
}

// DoWithStats runs fn and returns stats about the retry execution.
func DoWithStats[T any](ctx context.Context, opts Options, fn func(ctx context.Context) (T, error)) (T, Stats, error) {
	opts = mergeDefaults(opts)

	var zero T
	var stats Stats
	var lastErr error

	start := time.Now()

	for attempt := 1; attempt <= opts.MaxAttempts; attempt++ {
		stats.Attempts = attempt

		if ctx.Err() != nil {
			stats.LastError = ctx.Err()
			return zero, stats, ctx.Err()
		}

		result, err := fn(ctx)
		if err == nil {
			stats.Succeeded = true
			stats.TotalDelay = time.Since(start)
			return result, stats, nil
		}
		lastErr = err
		stats.LastError = err

		if attempt >= opts.MaxAttempts {
			break
		}

		if opts.ShouldRetry != nil && !opts.ShouldRetry(err, attempt) {
			break
		}

		delay := computeDelay(opts, attempt)
		if opts.OnRetry != nil {
			opts.OnRetry(err, attempt, delay)
		}

		select {
		case <-ctx.Done():
			stats.TotalDelay = time.Since(start)
			stats.LastError = ctx.Err()
			return zero, stats, ctx.Err()
		case <-time.After(delay):
		}
	}

	stats.TotalDelay = time.Since(start)
	return zero, stats, fmt.Errorf("failed after %d attempts: %w", stats.Attempts, lastErr)
}
