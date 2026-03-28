package retryux

import (
	"fmt"
	"testing"
	"time"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		err        error
		statusCode int
		wantCat    string
		wantRetry  bool
	}{
		{nil, 401, "auth", false},
		{nil, 403, "auth", false},
		{nil, 429, "rate_limit", true},
		{nil, 500, "server_error", true},
		{nil, 503, "server_error", true},
		{fmt.Errorf("connection refused"), 0, "connection", true},
		{fmt.Errorf("timeout exceeded"), 0, "timeout", true},
		{nil, 400, "client_error", false},
	}

	for _, tt := range tests {
		cat, retryable, _ := ClassifyError(tt.err, tt.statusCode)
		if cat != tt.wantCat {
			t.Errorf("ClassifyError(%v, %d) category = %q, want %q", tt.err, tt.statusCode, cat, tt.wantCat)
		}
		if retryable != tt.wantRetry {
			t.Errorf("ClassifyError(%v, %d) retryable = %v, want %v", tt.err, tt.statusCode, retryable, tt.wantRetry)
		}
	}
}

func TestCalculateBackoff(t *testing.T) {
	d0 := CalculateBackoff(0, time.Second)
	d1 := CalculateBackoff(1, time.Second)
	d2 := CalculateBackoff(2, time.Second)

	if d1 <= d0 {
		t.Errorf("backoff should increase: d0=%s, d1=%s", d0, d1)
	}
	if d2 <= d1 {
		t.Errorf("backoff should increase: d1=%s, d2=%s", d1, d2)
	}

	// Test cap at 2 minutes
	dMax := CalculateBackoff(20, time.Second)
	if dMax > 3*time.Minute { // with jitter
		t.Errorf("backoff should cap near 2m, got %s", dMax)
	}
}

func TestFormatCountdown(t *testing.T) {
	info := RetryInfo{
		Attempt:    2,
		MaxRetries: 3,
		WaitUntil:  time.Now().Add(90 * time.Second),
		Reason:     "rate_limit",
		Provider:   "deepseek",
	}

	s := FormatCountdown(info)
	if s == "" {
		t.Fatal("countdown should not be empty")
	}
}
