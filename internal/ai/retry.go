package ai

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/costa/polypod/internal/withretry"
)

// RetryConfig controls how AI API calls are retried.
type RetryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	Logger      *slog.Logger
}

// DefaultRetryConfig returns sane defaults for AI provider retries.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   500 * time.Millisecond,
		MaxDelay:    10 * time.Second,
	}
}

// IsRetryableError returns true for errors that are worth retrying:
// network timeouts, 429 rate limits, 5xx server errors, temporary failures.
// Returns false for client errors (400, 401, 403, 404) and auth errors.
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Context cancellations are never retryable
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Network errors (temporary)
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return true
		}
	}

	// String-based checks for HTTP errors (providers often wrap them)
	msg := strings.ToLower(err.Error())

	// Retryable HTTP status codes
	retryableHTTP := []string{
		"429", // rate limit
		"500", // server error
		"502", // bad gateway
		"503", // service unavailable
		"504", // gateway timeout
		"rate limit",
		"too many requests",
		"server error",
		"service unavailable",
		"timeout",
		"temporarily unavailable",
		"overloaded",
		"try again",
	}
	for _, s := range retryableHTTP {
		if strings.Contains(msg, s) {
			return true
		}
	}

	// Non-retryable errors
	nonRetryable := []string{
		"400", // bad request
		"401", // unauthorized
		"403", // forbidden
		"404", // not found
		"unauthorized",
		"forbidden",
		"not found",
		"invalid api key",
		"authentication",
		"invalid request",
		"content policy",
		"safety",
	}
	for _, s := range nonRetryable {
		if strings.Contains(msg, s) {
			return false
		}
	}

	// Default: assume retryable (network blips, unknown errors)
	return true
}

// WithRetry runs an AI call with exponential backoff.
// Only errors passing IsRetryableError are retried.
func WithRetry[T any](ctx context.Context, cfg RetryConfig, fn func(ctx context.Context) (T, error)) (T, error) {
	opts := withretry.Options{
		MaxAttempts: cfg.MaxAttempts,
		BaseDelay:   cfg.BaseDelay,
		MaxDelay:    cfg.MaxDelay,
		Multiplier:  2.0,
		Jitter:      0.2,
		ShouldRetry: func(err error, attempt int) bool {
			return IsRetryableError(err)
		},
		OnRetry: func(err error, attempt int, delay time.Duration) {
			if cfg.Logger != nil {
				cfg.Logger.Warn("ai retry",
					"attempt", attempt,
					"delay", delay,
					"error", err.Error(),
				)
			}
		},
	}
	return withretry.Do(ctx, opts, fn)
}

// WithRetryStats runs an AI call with retry and returns execution stats.
func WithRetryStats[T any](ctx context.Context, cfg RetryConfig, fn func(ctx context.Context) (T, error)) (T, withretry.Stats, error) {
	opts := withretry.Options{
		MaxAttempts: cfg.MaxAttempts,
		BaseDelay:   cfg.BaseDelay,
		MaxDelay:    cfg.MaxDelay,
		Multiplier:  2.0,
		Jitter:      0.2,
		ShouldRetry: func(err error, attempt int) bool {
			return IsRetryableError(err)
		},
		OnRetry: func(err error, attempt int, delay time.Duration) {
			if cfg.Logger != nil {
				cfg.Logger.Warn("ai retry",
					"attempt", attempt,
					"delay", delay,
					"error", err.Error(),
				)
			}
		},
	}
	return withretry.DoWithStats(ctx, opts, fn)
}

// retry is a backwards-compatible shim for the old callers in client.go.
// It wraps WithRetry using DefaultRetryConfig and a context-discarding
// callback signature. New code should use WithRetry directly.
func retry[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	return WithRetry(ctx, DefaultRetryConfig(), func(ctx context.Context) (T, error) {
		return fn()
	})
}
