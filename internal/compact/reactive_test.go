package compact

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// quietLogger returns an slog.Logger that discards all output so that test
// runs stay clean. reactive.go emits Info/Warn log lines on the recovery
// paths and we do not want them polluting `go test -v` output.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// fakeBackend is a test double for ReactiveCompactorBackend. It records how
// many times CompactNow was called and lets individual tests dictate the
// outcome.
type fakeBackend struct {
	mu     sync.Mutex
	calls  int
	result *CompactResult
	err    error
	onCall func(ctx context.Context) // optional side-effect hook
}

func (f *fakeBackend) CompactNow(ctx context.Context) (*CompactResult, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.onCall != nil {
		f.onCall(ctx)
	}
	return f.result, f.err
}

func (f *fakeBackend) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeBudget captures NotifyReactiveCompact calls so tests can assert that
// the budget hook is invoked exactly once on successful recovery.
type fakeBudget struct {
	calls atomic.Int32
	pre   atomic.Int32
	post  atomic.Int32
}

func (b *fakeBudget) NotifyReactiveCompact(pre, post int) {
	b.calls.Add(1)
	b.pre.Store(int32(pre))
	b.post.Store(int32(post))
}

// fakeModel captures OnCompacted calls.
type fakeModel struct {
	calls   atomic.Int32
	hookErr error
}

func (m *fakeModel) OnCompacted(ctx context.Context, result *CompactResult) error {
	m.calls.Add(1)
	return m.hookErr
}

// Test 1: overflow detection — verifies that IsContextOverflowError and
// ShouldReactivelyCompact recognise the known provider wordings without
// false positives on unrelated errors.
func TestIsContextOverflowError(t *testing.T) {
	t.Parallel()

	overflowCases := []struct {
		name string
		err  error
	}{
		{"anthropic direct", errors.New("prompt is too long: 137500 tokens > 135000 maximum")},
		{"anthropic 413", errors.New("request too large: HTTP 413")},
		{"openai canonical", errors.New("This model's maximum context length is 128000 tokens")},
		{"openai code", errors.New("error: context_length_exceeded")},
		{"generic context length", errors.New("context length reached")},
		{"generic token limit", errors.New("token limit exceeded for this request")},
		{"google style", errors.New("the input is too long for the model")},
		{"too many tokens", errors.New("too many tokens in request")},
		{"content too long", errors.New("content too long, shorten your prompt")},
		{"bare 413", errors.New("status 413 from upstream")},
		{"uppercase noise", errors.New("ERROR: Prompt Is Too Long, please compact")},
	}
	for _, tc := range overflowCases {
		tc := tc
		t.Run("overflow/"+tc.name, func(t *testing.T) {
			t.Parallel()
			if !IsContextOverflowError(tc.err) {
				t.Fatalf("expected overflow detection for %q", tc.err)
			}
		})
	}

	nonOverflowCases := []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"auth", errors.New("401 unauthorized: invalid api key")},
		{"rate limit", errors.New("429 too many requests, please retry")},
		{"network", errors.New("dial tcp: connection refused")},
		{"random", errors.New("something went wrong")},
	}
	for _, tc := range nonOverflowCases {
		tc := tc
		t.Run("non_overflow/"+tc.name, func(t *testing.T) {
			t.Parallel()
			if IsContextOverflowError(tc.err) {
				t.Fatalf("unexpected overflow detection for %q", tc.err)
			}
		})
	}

	// ShouldReactivelyCompact adds the turn-count gate — turn 0 must never
	// trigger reactive compaction even on a clearly-overflow error, because
	// the user's very first message is the only content in the history and
	// compacting it would either no-op or infinite-loop.
	overflowErr := errors.New("prompt is too long")
	if ShouldReactivelyCompact(overflowErr, 0) {
		t.Fatal("ShouldReactivelyCompact must return false on turn 0")
	}
	if !ShouldReactivelyCompact(overflowErr, 1) {
		t.Fatal("ShouldReactivelyCompact must return true on turn 1 with overflow error")
	}
	if ShouldReactivelyCompact(errors.New("unrelated"), 5) {
		t.Fatal("ShouldReactivelyCompact must gate on error type, not just turn count")
	}
	if ShouldReactivelyCompact(nil, 5) {
		t.Fatal("ShouldReactivelyCompact must return false for nil error")
	}
}

// Test 2: successful recovery — the first turnFn invocation returns an
// overflow error, the backend compacts successfully, and the second turnFn
// invocation succeeds. RecoverAndRetry must return nil and the budget/model
// hooks must fire exactly once each.
func TestReactiveCompactor_SuccessfulRecovery(t *testing.T) {
	t.Parallel()

	backend := &fakeBackend{
		result: &CompactResult{
			Summary:           "summarized conversation",
			PreCompactTokens:  150_000,
			PostCompactTokens: 20_000,
			MessagesRemoved:   42,
		},
	}
	budget := &fakeBudget{}
	model := &fakeModel{}

	rc := NewReactiveCompactor(backend, model, budget, ReactiveConfig{Logger: quietLogger()})

	var turnCalls int
	turnFn := func() error {
		turnCalls++
		if turnCalls == 1 {
			return errors.New("prompt is too long: 200000 tokens > 150000 maximum")
		}
		return nil
	}

	if err := rc.RecoverAndRetry(context.Background(), turnFn); err != nil {
		t.Fatalf("expected successful recovery, got %v", err)
	}
	if turnCalls != 2 {
		t.Fatalf("expected turnFn called twice, got %d", turnCalls)
	}
	if got := backend.CallCount(); got != 1 {
		t.Fatalf("expected backend.CompactNow called once, got %d", got)
	}
	if got := budget.calls.Load(); got != 1 {
		t.Fatalf("expected budget hook called once, got %d", got)
	}
	if got := budget.pre.Load(); got != 150_000 {
		t.Fatalf("expected budget pre=150000, got %d", got)
	}
	if got := budget.post.Load(); got != 20_000 {
		t.Fatalf("expected budget post=20000, got %d", got)
	}
	if got := model.calls.Load(); got != 1 {
		t.Fatalf("expected model hook called once, got %d", got)
	}
	if rc.Stats() != 1 {
		t.Fatalf("expected lifetime retry count of 1, got %d", rc.Stats())
	}

	// After a successful recovery the per-turn guard should be cleared so
	// the NEXT turn (which is a separate invocation) can react again if it
	// needs to. Simulate a second turn that also overflows and recovers.
	turnCalls = 0
	turnFn2 := func() error {
		turnCalls++
		if turnCalls == 1 {
			return errors.New("context_length_exceeded")
		}
		return nil
	}
	if err := rc.RecoverAndRetry(context.Background(), turnFn2); err != nil {
		t.Fatalf("expected second-turn recovery, got %v", err)
	}
	if rc.Stats() != 2 {
		t.Fatalf("expected lifetime retry count of 2 after second turn, got %d", rc.Stats())
	}
}

// Test 3: giving up after one retry — the first turnFn call overflows, the
// backend compacts successfully, but the retry still overflows. Reactive
// compaction must not loop; it must return ErrReactiveRetryExhausted with
// the retry error in the chain, and the backend must be called exactly
// once (no double compaction).
func TestReactiveCompactor_GivesUpAfterOneRetry(t *testing.T) {
	t.Parallel()

	backend := &fakeBackend{
		result: &CompactResult{
			Summary:           "too-large history summary",
			PreCompactTokens:  500_000,
			PostCompactTokens: 200_000, // still over, post is large
		},
	}
	rc := NewReactiveCompactor(backend, nil, nil, ReactiveConfig{Logger: quietLogger()})

	retryErr := errors.New("prompt is too long: still 200000 tokens > 150000")
	initialErr := errors.New("prompt is too long: 500000 tokens > 150000")
	var turnCalls int
	turnFn := func() error {
		turnCalls++
		if turnCalls == 1 {
			return initialErr
		}
		return retryErr
	}

	err := rc.RecoverAndRetry(context.Background(), turnFn)
	if err == nil {
		t.Fatal("expected error after failed retry, got nil")
	}
	if !errors.Is(err, ErrReactiveRetryExhausted) {
		t.Fatalf("expected ErrReactiveRetryExhausted, got %v", err)
	}
	if !strings.Contains(err.Error(), "prompt is too long") {
		t.Fatalf("expected retry error wrapped in result, got %v", err)
	}
	if turnCalls != 2 {
		t.Fatalf("expected turnFn called twice (initial + retry), got %d", turnCalls)
	}
	if got := backend.CallCount(); got != 1 {
		t.Fatalf("expected backend.CompactNow called exactly once, got %d", got)
	}

	// Because the per-turn guard (lastAttempt) stays set after exhaustion,
	// a subsequent call with the same compactor and the same overflow must
	// NOT trigger another backend call — it must short-circuit to
	// ErrReactiveRetryExhausted. This is the infinite-loop guard.
	err2 := rc.RecoverAndRetry(context.Background(), func() error {
		return errors.New("prompt is too long again")
	})
	if !errors.Is(err2, ErrReactiveRetryExhausted) {
		t.Fatalf("expected second call to short-circuit to ErrReactiveRetryExhausted, got %v", err2)
	}
	if got := backend.CallCount(); got != 1 {
		t.Fatalf("expected backend still called only once after second attempt, got %d", got)
	}

	// ResetAttempt should clear the guard so a fresh user turn can try again.
	rc.ResetAttempt()
	recoveredTurn := 0
	recoverFn := func() error {
		recoveredTurn++
		if recoveredTurn == 1 {
			return errors.New("prompt is too long")
		}
		return nil
	}
	if err := rc.RecoverAndRetry(context.Background(), recoverFn); err != nil {
		t.Fatalf("expected recovery after ResetAttempt, got %v", err)
	}
	if got := backend.CallCount(); got != 2 {
		t.Fatalf("expected backend called twice after reset, got %d", got)
	}
}

// TestReactiveCompactor_NonOverflowErrorPassesThrough verifies that a
// non-overflow error from turnFn is surfaced immediately without invoking
// the backend. This matters because the reactive path is the only recovery
// strategy for overflow, but for auth/rate-limit/network errors there is
// nothing to compact and running the backend would just burn tokens.
func TestReactiveCompactor_NonOverflowErrorPassesThrough(t *testing.T) {
	t.Parallel()

	backend := &fakeBackend{result: &CompactResult{}}
	rc := NewReactiveCompactor(backend, nil, nil, ReactiveConfig{Logger: quietLogger()})

	authErr := errors.New("401 unauthorized: invalid api key")
	err := rc.RecoverAndRetry(context.Background(), func() error { return authErr })
	if !errors.Is(err, authErr) {
		t.Fatalf("expected pass-through of auth error, got %v", err)
	}
	if got := backend.CallCount(); got != 0 {
		t.Fatalf("expected backend NOT to be called on non-overflow error, got %d calls", got)
	}
}

// TestReactiveCompactor_BackendFailureSurfacesBothErrors verifies that a
// compact backend failure returns an error chain containing BOTH the
// compact failure and the original overflow error, so operators can tell
// which half broke.
func TestReactiveCompactor_BackendFailureSurfacesBothErrors(t *testing.T) {
	t.Parallel()

	compactErr := fmt.Errorf("summarize API down: %w", io.ErrUnexpectedEOF)
	backend := &fakeBackend{err: compactErr}
	rc := NewReactiveCompactor(backend, nil, nil, ReactiveConfig{Logger: quietLogger()})

	overflowErr := errors.New("prompt is too long")
	err := rc.RecoverAndRetry(context.Background(), func() error { return overflowErr })
	if err == nil {
		t.Fatal("expected error when backend fails")
	}
	if !errors.Is(err, compactErr) {
		t.Fatalf("expected wrapped compact error, got %v", err)
	}
	if !errors.Is(err, overflowErr) {
		t.Fatalf("expected wrapped original overflow error, got %v", err)
	}
}

// TestReactiveCompactor_NilTurnFn verifies the defensive nil check.
func TestReactiveCompactor_NilTurnFn(t *testing.T) {
	t.Parallel()
	rc := NewReactiveCompactor(&fakeBackend{}, nil, nil, ReactiveConfig{Logger: quietLogger()})
	if err := rc.RecoverAndRetry(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil turnFn")
	}
}
