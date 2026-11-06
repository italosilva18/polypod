package withretry

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestIsOutputTruncated(t *testing.T) {
	cases := map[string]bool{
		"max_tokens":        true,
		"MAX_TOKENS":        true,
		"length":            true,
		"max_output_tokens": true,
		"output_limit":      true,
		" length ":          true,
		"stop":              false,
		"end_turn":          false,
		"tool_use":          false,
		"":                  false,
	}
	for reason, want := range cases {
		if got := IsOutputTruncated(reason); got != want {
			t.Errorf("IsOutputTruncated(%q) = %v, want %v", reason, got, want)
		}
	}
}

func TestDefaultContinuationPolicy(t *testing.T) {
	p := DefaultContinuationPolicy()
	if p.MaxAttempts != 3 {
		t.Errorf("MaxAttempts = %d, want 3", p.MaxAttempts)
	}
	if p.AppendPrompt == "" {
		t.Error("AppendPrompt should be non-empty")
	}
}

func TestRecoverTruncated_NonTruncatedPassThrough(t *testing.T) {
	called := 0
	fn := func(ctx context.Context, prompt string) (string, any, error) {
		called++
		return "end_turn", "should-not-be-used", nil
	}

	got, err := RecoverTruncated(context.Background(), "end_turn", "original", fn, DefaultContinuationPolicy())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "original" {
		t.Errorf("got %v, want %q", got, "original")
	}
	if called != 0 {
		t.Errorf("fn called %d times, want 0", called)
	}
}

func TestRecoverTruncated_RecoversAfterOneContinuation(t *testing.T) {
	called := 0
	fn := func(ctx context.Context, prompt string) (string, any, error) {
		called++
		if prompt == "" {
			t.Errorf("expected continuation prompt, got empty string")
		}
		return "end_turn", "part2", nil
	}

	got, err := RecoverTruncated(context.Background(), "max_tokens", "part1", fn, DefaultContinuationPolicy())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []any{"part1", "part2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if called != 1 {
		t.Errorf("fn called %d times, want 1", called)
	}
}

func TestRecoverTruncated_GivesUpAfterMaxAttempts(t *testing.T) {
	called := 0
	fn := func(ctx context.Context, prompt string) (string, any, error) {
		called++
		return "max_tokens", "chunk", nil
	}

	policy := ContinuationPolicy{MaxAttempts: 3, AppendPrompt: "continue"}
	got, err := RecoverTruncated(context.Background(), "max_tokens", "initial", fn, policy)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var truncErr *OutputTruncationError
	if !errors.As(err, &truncErr) {
		t.Fatalf("error %v is not *OutputTruncationError", err)
	}
	if truncErr.StopReason != "max_tokens" {
		t.Errorf("StopReason = %q, want %q", truncErr.StopReason, "max_tokens")
	}
	if called != 3 {
		t.Errorf("fn called %d times, want 3", called)
	}
	// Accumulated = initial + 3 chunks = 4 entries.
	acc, ok := got.([]any)
	if !ok {
		t.Fatalf("got %T, want []any", got)
	}
	if len(acc) != 4 {
		t.Errorf("accumulated len = %d, want 4", len(acc))
	}
}

func TestRecoverTruncated_FnErrorReturnsWrapped(t *testing.T) {
	sentinel := errors.New("boom")
	fn := func(ctx context.Context, prompt string) (string, any, error) {
		return "", nil, sentinel
	}
	_, err := RecoverTruncated(context.Background(), "length", "initial", fn, DefaultContinuationPolicy())
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want to wrap %v", err, sentinel)
	}
}

func TestRecoverTruncated_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fn := func(ctx context.Context, prompt string) (string, any, error) {
		t.Fatal("fn should not be called when ctx is already cancelled")
		return "", nil, nil
	}
	_, err := RecoverTruncated(ctx, "max_tokens", "initial", fn, DefaultContinuationPolicy())
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func TestRecoverTruncated_ZeroPolicyUsesDefaults(t *testing.T) {
	called := 0
	fn := func(ctx context.Context, prompt string) (string, any, error) {
		called++
		if prompt == "" {
			t.Error("expected default prompt to be injected")
		}
		return "end_turn", "done", nil
	}
	_, err := RecoverTruncated(context.Background(), "max_tokens", "start", fn, ContinuationPolicy{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called != 1 {
		t.Errorf("fn called %d times, want 1", called)
	}
}

// concatStr implements Concatenatable for string-like accumulation.
type concatStr string

func (c concatStr) Concat(other any) any {
	if s, ok := other.(concatStr); ok {
		return c + s
	}
	if s, ok := other.(string); ok {
		return c + concatStr(s)
	}
	return c
}

func TestRecoverTruncated_ConcatenatableAccumulator(t *testing.T) {
	fn := func(ctx context.Context, prompt string) (string, any, error) {
		return "stop", concatStr("-world"), nil
	}
	got, err := RecoverTruncated(context.Background(), "max_tokens", concatStr("hello"), fn, DefaultContinuationPolicy())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != concatStr("hello-world") {
		t.Errorf("got %v, want %q", got, "hello-world")
	}
}
