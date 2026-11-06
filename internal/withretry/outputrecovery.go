package withretry

import (
	"context"
	"fmt"
	"strings"
)

// OutputTruncationError is a typed error that signals the model stopped
// because it hit its output token cap before completing the response.
// Callers can check for this to trigger a continuation recovery loop.
type OutputTruncationError struct {
	StopReason      string
	PartialResponse any
}

// Error implements the error interface.
func (e *OutputTruncationError) Error() string {
	return fmt.Sprintf("output truncated: stop_reason=%q", e.StopReason)
}

// IsOutputTruncated reports whether the given stop reason indicates that the
// model hit its output token cap. Matches the set of strings various providers
// return ("max_tokens" — Anthropic, "length" — OpenAI, "max_output_tokens",
// "output_limit").
func IsOutputTruncated(stopReason string) bool {
	switch strings.ToLower(strings.TrimSpace(stopReason)) {
	case "max_tokens", "length", "max_output_tokens", "output_limit":
		return true
	default:
		return false
	}
}

// ContinuationPolicy controls how the recovery loop asks the model to keep
// going after hitting an output cap.
type ContinuationPolicy struct {
	// MaxAttempts is the maximum number of continuation calls to make before
	// giving up (default 3, matching the TS implementation).
	MaxAttempts int
	// AppendPrompt is the message appended to the conversation to ask the
	// model to continue from where it stopped.
	AppendPrompt string
}

// DefaultContinuationPolicy returns the default policy: 3 attempts with a
// neutral continuation prompt.
func DefaultContinuationPolicy() ContinuationPolicy {
	return ContinuationPolicy{
		MaxAttempts:  3,
		AppendPrompt: "Please continue from where you stopped.",
	}
}

// ContinuationFn is the callback invoked by RecoverTruncated for each
// continuation attempt. Implementations should append the given prompt to the
// conversation, make a fresh model call, and return the resulting stop reason
// and response payload.
type ContinuationFn func(ctx context.Context, continuationPrompt string) (stopReason string, response any, err error)

// Concatenatable is an optional interface a response type can implement to
// customize how RecoverTruncated merges a continuation chunk into the running
// accumulator. If not implemented, responses are merged into a []any slice.
type Concatenatable interface {
	Concat(other any) any
}

// RecoverTruncated drives the continuation recovery loop.
//
// If initialStopReason does not indicate truncation, the initial response is
// returned unchanged. Otherwise fn is called up to policy.MaxAttempts times,
// each time with policy.AppendPrompt, concatenating the responses, until a
// non-truncated stop reason is returned or the budget is exhausted.
//
// When the budget is exhausted the accumulated response is returned alongside
// an *OutputTruncationError so callers can decide whether to surface the
// partial result.
func RecoverTruncated(
	ctx context.Context,
	initialStopReason string,
	initialResponse any,
	fn ContinuationFn,
	policy ContinuationPolicy,
) (any, error) {
	policy = mergeContinuationDefaults(policy)

	if !IsOutputTruncated(initialStopReason) {
		return initialResponse, nil
	}

	accumulated := initialResponse
	lastStopReason := initialStopReason

	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return accumulated, err
		}

		stopReason, chunk, err := fn(ctx, policy.AppendPrompt)
		if err != nil {
			return accumulated, fmt.Errorf("continuation attempt %d failed: %w", attempt, err)
		}

		accumulated = mergeResponses(accumulated, chunk)
		lastStopReason = stopReason

		if !IsOutputTruncated(stopReason) {
			return accumulated, nil
		}
	}

	return accumulated, &OutputTruncationError{
		StopReason:      lastStopReason,
		PartialResponse: accumulated,
	}
}

// mergeResponses concatenates a continuation chunk onto the accumulated
// response. When the accumulator implements Concatenatable, its Concat method
// is used; otherwise the pieces are collected into a []any slice (flattening
// an existing slice accumulator).
func mergeResponses(acc any, next any) any {
	if acc == nil {
		return next
	}
	if c, ok := acc.(Concatenatable); ok {
		return c.Concat(next)
	}
	if existing, ok := acc.([]any); ok {
		return append(existing, next)
	}
	return []any{acc, next}
}

func mergeContinuationDefaults(p ContinuationPolicy) ContinuationPolicy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 3
	}
	if strings.TrimSpace(p.AppendPrompt) == "" {
		p.AppendPrompt = "Please continue from where you stopped."
	}
	return p
}
