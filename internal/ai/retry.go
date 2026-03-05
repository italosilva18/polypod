package ai

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"
)

const (
	maxRetries    = 3
	baseBackoff   = 1 * time.Second
	backoffFactor = 2.0
)

// retry executes fn up to maxRetries times with exponential backoff for retryable errors.
func retry[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	var lastErr error
	var zero T

	for attempt := 0; attempt <= maxRetries; attempt++ {
		result, err := fn()
		if err == nil {
			return result, nil
		}

		lastErr = err

		if !isRetryable(err) {
			return zero, err
		}

		if attempt == maxRetries {
			break
		}

		delay := time.Duration(float64(baseBackoff) * math.Pow(backoffFactor, float64(attempt)))
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(delay):
		}
	}

	return zero, lastErr
}

// isRetryable checks if an error warrants a retry (429, 500-504, timeout).
func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	errMsg := err.Error()

	// Check for timeout
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "Timeout") {
		return true
	}

	// Check for HTTP status codes in error message
	retryStatuses := []int{
		http.StatusTooManyRequests,       // 429
		http.StatusInternalServerError,   // 500
		http.StatusBadGateway,            // 502
		http.StatusServiceUnavailable,    // 503
		http.StatusGatewayTimeout,        // 504
	}

	for _, status := range retryStatuses {
		if strings.Contains(errMsg, http.StatusText(status)) {
			return true
		}
		if strings.Contains(errMsg, strings.ToLower(http.StatusText(status))) {
			return true
		}
	}

	// Check for common transient error patterns
	if strings.Contains(errMsg, "connection refused") ||
		strings.Contains(errMsg, "connection reset") ||
		strings.Contains(errMsg, "EOF") ||
		strings.Contains(errMsg, "429") ||
		strings.Contains(errMsg, "500") ||
		strings.Contains(errMsg, "502") ||
		strings.Contains(errMsg, "503") ||
		strings.Contains(errMsg, "504") {
		return true
	}

	return false
}
