package fallback

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
)

// ErrorClass categorizes an error for fallback decisions.
type ErrorClass int

const (
	// ErrClassOther is any error not recognized as retryable.
	ErrClassOther ErrorClass = iota
	// ErrClassRateLimit is a 429 / rate-limit error.
	ErrClassRateLimit
	// ErrClassOverloaded is an "overloaded" / high-demand error (Anthropic 529, etc.).
	ErrClassOverloaded
	// ErrClassTimeout is a request/context timeout or deadline exceeded.
	ErrClassTimeout
	// ErrClassServer is a 5xx server-side error.
	ErrClassServer
)

// String returns a human-readable name for the error class.
func (c ErrorClass) String() string {
	switch c {
	case ErrClassRateLimit:
		return "rate_limit"
	case ErrClassOverloaded:
		return "overloaded"
	case ErrClassTimeout:
		return "timeout"
	case ErrClassServer:
		return "server_error"
	default:
		return "other"
	}
}

// ModelFallbackPolicy describes how to fall back between models of a single caller.
type ModelFallbackPolicy struct {
	// PrimaryModel is attempted first.
	PrimaryModel string
	// FallbackModels are attempted in order on retryable errors.
	FallbackModels []string
	// RetryableErrors lists which ErrorClass values trigger fallback.
	// If empty, all known retryable classes (RateLimit, Overloaded, Timeout, Server) are used.
	RetryableErrors []ErrorClass
	// MaxFallbacks caps how many fallback models are tried. Zero means unlimited.
	MaxFallbacks int
	// Logger receives debug events. If nil, slog.Default() is used.
	Logger *slog.Logger
}

// ModelCaller abstracts a model client that can execute a request against a named model.
type ModelCaller interface {
	Call(ctx context.Context, model string, req any) (any, error)
}

// ModelCallerFunc adapts a function to the ModelCaller interface.
type ModelCallerFunc func(ctx context.Context, model string, req any) (any, error)

// Call implements ModelCaller.
func (f ModelCallerFunc) Call(ctx context.Context, model string, req any) (any, error) {
	return f(ctx, model, req)
}

// ClassifyError maps an error to an ErrorClass by inspecting wrapped errors and message text.
func ClassifyError(err error) ErrorClass {
	if err == nil {
		return ErrClassOther
	}

	// Context-level timeouts first.
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return ErrClassTimeout
	}

	// net.Error with Timeout() == true.
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ErrClassTimeout
	}

	msg := strings.ToLower(err.Error())

	// Overloaded beats rate-limit when both keywords are present since the provider
	// distinction matters ("overloaded" is an Anthropic-style high-demand signal).
	if containsAny(msg, []string{
		"overloaded",
		"overloaded_error",
		"high demand",
		"capacity",
		"529",
	}) {
		return ErrClassOverloaded
	}

	if containsAny(msg, []string{
		"429",
		"rate limit",
		"rate_limit",
		"too many requests",
		"quota",
	}) {
		return ErrClassRateLimit
	}

	if containsAny(msg, []string{
		"timeout",
		"timed out",
		"deadline exceeded",
		"context deadline",
	}) {
		return ErrClassTimeout
	}

	if containsAny(msg, []string{
		"500",
		"502",
		"503",
		"504",
		"internal server error",
		"bad gateway",
		"service unavailable",
		"gateway timeout",
		"server_error",
	}) {
		return ErrClassServer
	}

	return ErrClassOther
}

func containsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

// isRetryable reports whether the policy considers the given class retryable.
func (p ModelFallbackPolicy) isRetryable(class ErrorClass) bool {
	if class == ErrClassOther {
		return false
	}
	if len(p.RetryableErrors) == 0 {
		// Default: all well-known transient classes trigger fallback.
		switch class {
		case ErrClassRateLimit, ErrClassOverloaded, ErrClassTimeout, ErrClassServer:
			return true
		}
		return false
	}
	for _, c := range p.RetryableErrors {
		if c == class {
			return true
		}
	}
	return false
}

// CallWithFallback invokes caller against the primary model and, on retryable errors,
// iterates through the fallback models in order. It returns the successful response,
// the name of the model that served it, or the last error encountered.
func CallWithFallback(
	ctx context.Context,
	caller ModelCaller,
	req any,
	policy ModelFallbackPolicy,
) (any, string, error) {
	if caller == nil {
		return nil, "", errors.New("fallback: nil ModelCaller")
	}
	if policy.PrimaryModel == "" {
		return nil, "", errors.New("fallback: empty PrimaryModel in policy")
	}

	logger := policy.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Build the ordered attempt list: primary first, then fallbacks (capped by MaxFallbacks).
	attempts := make([]string, 0, 1+len(policy.FallbackModels))
	attempts = append(attempts, policy.PrimaryModel)
	fallbacks := policy.FallbackModels
	if policy.MaxFallbacks > 0 && len(fallbacks) > policy.MaxFallbacks {
		fallbacks = fallbacks[:policy.MaxFallbacks]
	}
	attempts = append(attempts, fallbacks...)

	var lastErr error
	var lastClass ErrorClass
	for i, model := range attempts {
		// Abort early if the context was cancelled between attempts.
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return nil, "", fmt.Errorf("fallback: context cancelled after model %q: %w", attempts[i-1], err)
			}
			return nil, "", fmt.Errorf("fallback: context cancelled before any attempt: %w", err)
		}

		resp, err := caller.Call(ctx, model, req)
		if err == nil {
			if i > 0 {
				logger.Debug("model fallback succeeded",
					"primary_model", policy.PrimaryModel,
					"fallback_model", model,
					"attempt", i,
					"previous_error_class", lastClass.String(),
				)
			}
			return resp, model, nil
		}

		lastErr = err
		lastClass = ClassifyError(err)

		if !policy.isRetryable(lastClass) {
			// Non-retryable: give up immediately.
			return nil, "", fmt.Errorf("fallback: non-retryable error on model %q (%s): %w", model, lastClass, err)
		}

		// If there is another attempt available, log the fallback trigger.
		if i < len(attempts)-1 {
			logger.Debug("model fallback triggered",
				"failed_model", model,
				"next_model", attempts[i+1],
				"error_class", lastClass.String(),
				"error", err.Error(),
			)
		}
	}

	return nil, "", fmt.Errorf("fallback: exhausted %d model(s), last error (%s): %w",
		len(attempts), lastClass, lastErr)
}
