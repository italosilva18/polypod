package withretry

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestDo_Success(t *testing.T) {
	ctx := context.Background()

	result, err := Do(ctx, DefaultOptions(), func(ctx context.Context) (string, error) {
		return "success", nil
	})

	if err != nil {
		t.Errorf("Do returned error: %v", err)
	}
	if result != "success" {
		t.Errorf("Do = %q; want success", result)
	}
}

func TestDo_RetryThenSuccess(t *testing.T) {
	ctx := context.Background()
	var attempts int32

	opts := DefaultOptions()
	opts.BaseDelay = 1 * time.Millisecond

	result, err := Do(ctx, opts, func(ctx context.Context) (int, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			return 0, errors.New("temporary failure")
		}
		return 42, nil
	})

	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	if result != 42 {
		t.Errorf("Do = %d; want 42", result)
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Errorf("attempts = %d; want 3", atomic.LoadInt32(&attempts))
	}
}

func TestDo_AllFailures(t *testing.T) {
	ctx := context.Background()
	var attempts int32

	opts := Options{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
	}

	_, err := Do(ctx, opts, func(ctx context.Context) (string, error) {
		atomic.AddInt32(&attempts, 1)
		return "", errors.New("persistent failure")
	})

	if err == nil {
		t.Error("Do should return error after all retries fail")
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Errorf("attempts = %d; want 3", atomic.LoadInt32(&attempts))
	}
}

func TestDo_ShouldRetryFalse(t *testing.T) {
	ctx := context.Background()
	var attempts int32

	opts := Options{
		MaxAttempts: 5,
		BaseDelay:   1 * time.Millisecond,
		ShouldRetry: func(err error, attempt int) bool {
			return false // never retry
		},
	}

	_, err := Do(ctx, opts, func(ctx context.Context) (string, error) {
		atomic.AddInt32(&attempts, 1)
		return "", errors.New("error")
	})

	if err == nil {
		t.Error("Do should return error")
	}
	if atomic.LoadInt32(&attempts) != 1 {
		t.Errorf("attempts = %d; want 1 (no retries)", atomic.LoadInt32(&attempts))
	}
}

func TestDo_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	opts := Options{
		MaxAttempts: 10,
		BaseDelay:   100 * time.Millisecond,
	}

	var attempts int32

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := Do(ctx, opts, func(ctx context.Context) (string, error) {
		atomic.AddInt32(&attempts, 1)
		return "", errors.New("error")
	})

	if err == nil {
		t.Error("Do should return error after context cancel")
	}

	final := atomic.LoadInt32(&attempts)
	if final >= 10 {
		t.Errorf("attempts = %d; want < 10 (cancelled early)", final)
	}
}

func TestDo_OnRetryCallback(t *testing.T) {
	ctx := context.Background()
	var retries int32

	opts := Options{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		OnRetry: func(err error, attempt int, delay time.Duration) {
			atomic.AddInt32(&retries, 1)
		},
	}

	_, _ = Do(ctx, opts, func(ctx context.Context) (string, error) {
		return "", errors.New("error")
	})

	// 3 attempts = 2 retries (after attempt 1 and after attempt 2)
	if atomic.LoadInt32(&retries) != 2 {
		t.Errorf("retries = %d; want 2", atomic.LoadInt32(&retries))
	}
}

func TestDoVoid(t *testing.T) {
	ctx := context.Background()

	err := DoVoid(ctx, DefaultOptions(), func(ctx context.Context) error {
		return nil
	})

	if err != nil {
		t.Errorf("DoVoid = %v; want nil", err)
	}
}

func TestDefaultOptions(t *testing.T) {
	opts := DefaultOptions()

	if opts.MaxAttempts <= 0 {
		t.Errorf("MaxAttempts = %d; want > 0", opts.MaxAttempts)
	}
	if opts.BaseDelay <= 0 {
		t.Errorf("BaseDelay = %v; want > 0", opts.BaseDelay)
	}
	if opts.MaxDelay <= 0 {
		t.Errorf("MaxDelay = %v; want > 0", opts.MaxDelay)
	}
}

var errFoo = errors.New("foo")
var errBar = errors.New("bar")

func TestIsRetryable(t *testing.T) {
	fn := IsRetryable(errFoo, errBar)

	if !fn(errFoo, 1) {
		t.Error("errFoo should be retryable")
	}
	if !fn(errBar, 1) {
		t.Error("errBar should be retryable")
	}
	if fn(errors.New("other"), 1) {
		t.Error("other error should not be retryable")
	}
}

func TestNotRetryable(t *testing.T) {
	fn := NotRetryable(errFoo)

	if fn(errFoo, 1) {
		t.Error("errFoo should not be retryable")
	}
	if !fn(errBar, 1) {
		t.Error("errBar should be retryable (not in exclusion list)")
	}
}

func TestDoWithStats(t *testing.T) {
	ctx := context.Background()
	var attempts int32

	opts := Options{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
	}

	_, stats, _ := DoWithStats(ctx, opts, func(ctx context.Context) (int, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 2 {
			return 0, errors.New("retry me")
		}
		return 42, nil
	})

	if !stats.Succeeded {
		t.Error("stats.Succeeded should be true")
	}
	if stats.Attempts != 2 {
		t.Errorf("stats.Attempts = %d; want 2", stats.Attempts)
	}
}
