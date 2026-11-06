package fallback

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"
)

// scriptedCaller is a fake ModelCaller returning a scripted error or response per model.
type scriptedCaller struct {
	// responses[model] yields either (resp, nil) or (nil, err).
	responses map[string]callerResult
	// calls records the models visited in order.
	calls []string
}

type callerResult struct {
	resp any
	err  error
}

func (s *scriptedCaller) Call(_ context.Context, model string, _ any) (any, error) {
	s.calls = append(s.calls, model)
	r, ok := s.responses[model]
	if !ok {
		return nil, fmt.Errorf("unexpected model call: %s", model)
	}
	return r.resp, r.err
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestClassifyError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want ErrorClass
	}{
		{"nil", nil, ErrClassOther},
		{"context deadline", context.DeadlineExceeded, ErrClassTimeout},
		{"rate limit 429", errors.New("HTTP 429 Too Many Requests"), ErrClassRateLimit},
		{"rate_limit keyword", errors.New("rate_limit_error: quota exceeded"), ErrClassRateLimit},
		{"overloaded", errors.New("anthropic overloaded_error: try again"), ErrClassOverloaded},
		{"529 high demand", errors.New("status 529 - high demand"), ErrClassOverloaded},
		{"timeout text", errors.New("request timed out after 30s"), ErrClassTimeout},
		{"500 server", errors.New("unexpected 500 Internal Server Error"), ErrClassServer},
		{"503 unavailable", errors.New("service unavailable"), ErrClassServer},
		{"random", errors.New("invalid api key"), ErrClassOther},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyError(tc.err)
			if got != tc.want {
				t.Fatalf("ClassifyError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestErrorClassString(t *testing.T) {
	want := map[ErrorClass]string{
		ErrClassRateLimit:  "rate_limit",
		ErrClassOverloaded: "overloaded",
		ErrClassTimeout:    "timeout",
		ErrClassServer:     "server_error",
		ErrClassOther:      "other",
	}
	for class, name := range want {
		if got := class.String(); got != name {
			t.Errorf("class %d: got %q, want %q", class, got, name)
		}
	}
}

func TestCallWithFallback_PrimarySucceeds(t *testing.T) {
	caller := &scriptedCaller{
		responses: map[string]callerResult{
			"primary": {resp: "ok", err: nil},
		},
	}
	policy := ModelFallbackPolicy{
		PrimaryModel:   "primary",
		FallbackModels: []string{"fb1", "fb2"},
		Logger:         discardLogger(),
	}

	resp, used, err := CallWithFallback(context.Background(), caller, nil, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "ok" {
		t.Errorf("resp = %v, want ok", resp)
	}
	if used != "primary" {
		t.Errorf("used = %q, want primary", used)
	}
	if len(caller.calls) != 1 {
		t.Errorf("calls = %v, want 1", caller.calls)
	}
}

func TestCallWithFallback_FallsBackOnRateLimit(t *testing.T) {
	caller := &scriptedCaller{
		responses: map[string]callerResult{
			"primary": {err: errors.New("429 rate limit exceeded")},
			"fb1":     {err: errors.New("overloaded_error")},
			"fb2":     {resp: "from-fb2"},
		},
	}
	policy := ModelFallbackPolicy{
		PrimaryModel:   "primary",
		FallbackModels: []string{"fb1", "fb2"},
		Logger:         discardLogger(),
	}

	resp, used, err := CallWithFallback(context.Background(), caller, nil, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "from-fb2" {
		t.Errorf("resp = %v, want from-fb2", resp)
	}
	if used != "fb2" {
		t.Errorf("used = %q, want fb2", used)
	}
	wantOrder := []string{"primary", "fb1", "fb2"}
	if !equalSlices(caller.calls, wantOrder) {
		t.Errorf("order = %v, want %v", caller.calls, wantOrder)
	}
}

func TestCallWithFallback_NonRetryableStops(t *testing.T) {
	caller := &scriptedCaller{
		responses: map[string]callerResult{
			"primary": {err: errors.New("invalid api key")},
			"fb1":     {resp: "should-not-reach"},
		},
	}
	policy := ModelFallbackPolicy{
		PrimaryModel:   "primary",
		FallbackModels: []string{"fb1"},
		Logger:         discardLogger(),
	}

	_, _, err := CallWithFallback(context.Background(), caller, nil, policy)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(caller.calls) != 1 {
		t.Errorf("expected 1 call (no fallback), got %v", caller.calls)
	}
}

func TestCallWithFallback_MaxFallbacksCaps(t *testing.T) {
	caller := &scriptedCaller{
		responses: map[string]callerResult{
			"primary": {err: errors.New("503 service unavailable")},
			"fb1":     {err: errors.New("503 service unavailable")},
			"fb2":     {err: errors.New("503 service unavailable")},
			"fb3":     {resp: "unreachable"},
		},
	}
	policy := ModelFallbackPolicy{
		PrimaryModel:   "primary",
		FallbackModels: []string{"fb1", "fb2", "fb3"},
		MaxFallbacks:   2, // only fb1 + fb2 are eligible, fb3 is skipped
		Logger:         discardLogger(),
	}

	_, _, err := CallWithFallback(context.Background(), caller, nil, policy)
	if err == nil {
		t.Fatal("expected error after exhausting fallbacks")
	}
	wantOrder := []string{"primary", "fb1", "fb2"}
	if !equalSlices(caller.calls, wantOrder) {
		t.Errorf("order = %v, want %v", caller.calls, wantOrder)
	}
}

func TestCallWithFallback_RetryableErrorsFilter(t *testing.T) {
	// Policy only treats Timeout as retryable; a rate-limit is non-retryable here.
	caller := &scriptedCaller{
		responses: map[string]callerResult{
			"primary": {err: errors.New("429 rate limit")},
			"fb1":     {resp: "should-not-reach"},
		},
	}
	policy := ModelFallbackPolicy{
		PrimaryModel:    "primary",
		FallbackModels:  []string{"fb1"},
		RetryableErrors: []ErrorClass{ErrClassTimeout},
		Logger:          discardLogger(),
	}

	_, _, err := CallWithFallback(context.Background(), caller, nil, policy)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(caller.calls) != 1 {
		t.Errorf("expected caller invoked once, got %v", caller.calls)
	}
}

func TestCallWithFallback_ContextCancelled(t *testing.T) {
	caller := ModelCallerFunc(func(ctx context.Context, _ string, _ any) (any, error) {
		return nil, errors.New("503 service unavailable")
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancelled

	policy := ModelFallbackPolicy{
		PrimaryModel:   "primary",
		FallbackModels: []string{"fb1"},
		Logger:         discardLogger(),
	}
	_, _, err := CallWithFallback(ctx, caller, nil, policy)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestCallWithFallback_NoFallbacks(t *testing.T) {
	caller := &scriptedCaller{
		responses: map[string]callerResult{
			"primary": {err: errors.New("503 service unavailable")},
		},
	}
	policy := ModelFallbackPolicy{
		PrimaryModel: "primary",
		Logger:       discardLogger(),
	}
	_, _, err := CallWithFallback(context.Background(), caller, nil, policy)
	if err == nil {
		t.Fatal("expected error when only primary fails")
	}
	if len(caller.calls) != 1 {
		t.Errorf("expected 1 call, got %v", caller.calls)
	}
}

func TestCallWithFallback_ValidatesInputs(t *testing.T) {
	_, _, err := CallWithFallback(context.Background(), nil, nil, ModelFallbackPolicy{PrimaryModel: "m"})
	if err == nil {
		t.Error("expected error for nil caller")
	}

	caller := &scriptedCaller{responses: map[string]callerResult{}}
	_, _, err = CallWithFallback(context.Background(), caller, nil, ModelFallbackPolicy{})
	if err == nil {
		t.Error("expected error for empty PrimaryModel")
	}
}

// timeoutNetError is a net.Error whose Timeout() returns true.
type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "i/o something" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return true }

func TestClassifyError_NetTimeout(t *testing.T) {
	if got := ClassifyError(timeoutNetError{}); got != ErrClassTimeout {
		t.Fatalf("got %v, want timeout", got)
	}
}

// Sanity check the scripted caller time budget so tests don't hang forever.
func TestCallWithFallback_HonoursContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	caller := ModelCallerFunc(func(ctx context.Context, _ string, _ any) (any, error) {
		return "ok", nil
	})
	resp, used, err := CallWithFallback(ctx, caller, nil, ModelFallbackPolicy{PrimaryModel: "primary"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp != "ok" || used != "primary" {
		t.Fatalf("resp=%v used=%q", resp, used)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
