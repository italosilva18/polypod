package errors

import (
	stderrors "errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// ErrorCode.String
// ---------------------------------------------------------------------------

func TestErrorCodeString(t *testing.T) {
	cases := []struct {
		name string
		code ErrorCode
		want string
	}{
		{"unknown", CodeUnknown, "unknown"},
		{"abort", CodeAbort, "abort"},
		{"tool", CodeTool, "tool"},
		{"api", CodeAPI, "api"},
		{"permission", CodePermission, "permission"},
		{"validation", CodeValidation, "validation"},
		{"compaction", CodeCompaction, "compaction"},
		{"session", CodeSession, "session"},
		{"config", CodeConfig, "config"},
		{"shell", CodeShell, "shell"},
		{"rate_limit", CodeRateLimit, "rate_limit"},
		{"auth", CodeAuth, "auth"},
		{"network", CodeNetwork, "network"},
		{"timeout", CodeTimeout, "timeout"},
		{"teleport", CodeTeleport, "teleport"},
		{"out_of_range", ErrorCode(9999), "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.code.String(); got != tc.want {
				t.Fatalf("ErrorCode(%d).String() = %q, want %q", tc.code, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// PolypodError.Error and Unwrap
// ---------------------------------------------------------------------------

func TestPolypodErrorFormatting(t *testing.T) {
	cause := stderrors.New("root cause")
	cases := []struct {
		name      string
		err       *PolypodError
		wantSub   string
		wantUnwrp error
	}{
		{
			name:      "without cause",
			err:       &PolypodError{Code: CodeValidation, Message: "bad input"},
			wantSub:   "[validation] bad input",
			wantUnwrp: nil,
		},
		{
			name:      "with cause",
			err:       &PolypodError{Code: CodeAPI, Message: "boom", Cause: cause},
			wantSub:   "[api] boom: root cause",
			wantUnwrp: cause,
		},
		{
			name:      "unknown code",
			err:       &PolypodError{Code: CodeUnknown, Message: "no category"},
			wantSub:   "[unknown] no category",
			wantUnwrp: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.wantSub {
				t.Errorf("Error() = %q, want %q", got, tc.wantSub)
			}
			if got := tc.err.Unwrap(); got != tc.wantUnwrp {
				t.Errorf("Unwrap() = %v, want %v", got, tc.wantUnwrp)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Concrete constructors
// ---------------------------------------------------------------------------

// basePolypod extracts the embedded PolypodError from any of the concrete
// error types in this package. Go's errors.As does NOT traverse struct
// embedding, so we need direct access via the concrete type.
func basePolypod(err error) *PolypodError {
	switch e := err.(type) {
	case *AbortError:
		return &e.PolypodError
	case *APIError:
		return &e.PolypodError
	case *PermissionError:
		return &e.PolypodError
	case *ValidationError:
		return &e.PolypodError
	case *CompactionError:
		return &e.PolypodError
	case *SessionError:
		return &e.PolypodError
	case *ConfigParseError:
		return &e.PolypodError
	case *ShellError:
		return &e.PolypodError
	case *TeleportError:
		return &e.PolypodError
	case *TelemetrySafeError:
		return &e.PolypodError
	case *ToolExecutionError:
		return &e.PolypodError
	case *ToolTimeoutError:
		return &e.PolypodError
	case *ToolPermissionDenied:
		return &e.PolypodError
	case *ToolValidationError:
		return &e.PolypodError
	case *PolypodError:
		return e
	}
	return nil
}

func TestConcreteErrorConstructors(t *testing.T) {
	cause := stderrors.New("underlying")

	cases := []struct {
		name     string
		err      error
		wantCode ErrorCode
		wantMsg  string
		wantWrap bool
	}{
		{
			name:     "AbortError default message",
			err:      NewAbortError(""),
			wantCode: CodeAbort,
			wantMsg:  "operation aborted",
		},
		{
			name:     "AbortError custom message",
			err:      NewAbortError("user cancelled"),
			wantCode: CodeAbort,
			wantMsg:  "user cancelled",
		},
		{
			name:     "APIError wraps cause",
			err:      WrapAPIError("api failed", http.StatusInternalServerError, cause),
			wantCode: CodeAPI,
			wantMsg:  "api failed",
			wantWrap: true,
		},
		{
			name:     "PermissionError",
			err:      NewPermissionError("denied"),
			wantCode: CodePermission,
			wantMsg:  "denied",
		},
		{
			name:     "ValidationError",
			err:      NewValidationError("name", "required"),
			wantCode: CodeValidation,
			wantMsg:  "required",
		},
		{
			name:     "CompactionError",
			err:      NewCompactionError("compact failed", cause),
			wantCode: CodeCompaction,
			wantMsg:  "compact failed",
			wantWrap: true,
		},
		{
			name:     "SessionError",
			err:      NewSessionError("session lost", cause),
			wantCode: CodeSession,
			wantMsg:  "session lost",
			wantWrap: true,
		},
		{
			name:     "ConfigParseError",
			err:      NewConfigParseError("/etc/cfg.json", "parse", cause),
			wantCode: CodeConfig,
			wantMsg:  "parse",
			wantWrap: true,
		},
		{
			name:     "ShellError",
			err:      NewShellError(1, "out", "err", false),
			wantCode: CodeShell,
			wantMsg:  "shell command failed",
		},
		{
			name:     "TeleportError",
			err:      NewTeleportError("teleport boom", "Oops"),
			wantCode: CodeTeleport,
			wantMsg:  "teleport boom",
		},
		{
			name:     "TelemetrySafeError fallback telemetry",
			err:      NewTelemetrySafeError("raw", ""),
			wantCode: CodeUnknown,
			wantMsg:  "raw",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pe := basePolypod(tc.err)
			if pe == nil {
				t.Fatalf("error %v has no embedded *PolypodError", tc.err)
			}
			if pe.Code != tc.wantCode {
				t.Errorf("code = %v, want %v", pe.Code, tc.wantCode)
			}
			if pe.Message != tc.wantMsg {
				t.Errorf("message = %q, want %q", pe.Message, tc.wantMsg)
			}
			if tc.wantWrap && pe.Cause == nil {
				t.Errorf("expected wrapped cause, got nil")
			}
			if !tc.wantWrap && pe.Cause != nil {
				t.Errorf("expected no cause, got %v", pe.Cause)
			}
			// Every concrete error must also produce a non-empty Error() string.
			if tc.err.Error() == "" {
				t.Errorf("Error() returned empty string")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Structured field constructors
// ---------------------------------------------------------------------------

func TestStructuredFieldConstructors(t *testing.T) {
	cause := stderrors.New("cause")

	t.Run("APIError status code", func(t *testing.T) {
		e := WrapAPIError("oops", 503, cause)
		if e.StatusCode != 503 {
			t.Errorf("StatusCode = %d, want 503", e.StatusCode)
		}
		if !stderrors.Is(e, cause) {
			t.Errorf("errors.Is did not find wrapped cause")
		}
	})

	t.Run("ValidationError field", func(t *testing.T) {
		e := NewValidationError("email", "invalid format")
		if e.Field != "email" {
			t.Errorf("Field = %q, want %q", e.Field, "email")
		}
	})

	t.Run("ConfigParseError file path", func(t *testing.T) {
		e := NewConfigParseError("/tmp/x.json", "bad", cause)
		if e.FilePath != "/tmp/x.json" {
			t.Errorf("FilePath = %q", e.FilePath)
		}
	})

	t.Run("ShellError fields", func(t *testing.T) {
		e := NewShellError(42, "stdout-data", "stderr-data", true)
		if e.ExitCode != 42 || e.Stdout != "stdout-data" || e.Stderr != "stderr-data" || !e.Interrupted {
			t.Errorf("ShellError fields not set correctly: %+v", e)
		}
	})

	t.Run("TeleportError formatted", func(t *testing.T) {
		e := NewTeleportError("raw", "Formatted user message")
		if e.FormattedMessage != "Formatted user message" {
			t.Errorf("FormattedMessage = %q", e.FormattedMessage)
		}
	})

	t.Run("TelemetrySafeError separate telemetry", func(t *testing.T) {
		e := NewTelemetrySafeError("raw with /path/info", "raw with PATH")
		if e.TelemetryMessage != "raw with PATH" {
			t.Errorf("TelemetryMessage = %q", e.TelemetryMessage)
		}
	})
}

// ---------------------------------------------------------------------------
// errors.Is / errors.As wrapping behaviour
// ---------------------------------------------------------------------------

func TestErrorWrappingIsAs(t *testing.T) {
	root := stderrors.New("root")
	api := WrapAPIError("api call failed", 500, root)

	// errors.Is should find the root cause through Unwrap
	if !stderrors.Is(api, root) {
		t.Errorf("errors.Is(api, root) = false, want true")
	}

	// errors.As should find *APIError
	var got *APIError
	if !stderrors.As(api, &got) {
		t.Fatalf("errors.As(*APIError) failed")
	}
	if got.StatusCode != 500 {
		t.Errorf("StatusCode = %d, want 500", got.StatusCode)
	}

	// Chained wrapping through fmt.Errorf
	wrapped := fmt.Errorf("higher context: %w", api)
	if !stderrors.Is(wrapped, root) {
		t.Errorf("errors.Is through fmt.Errorf chain failed")
	}
	var got2 *APIError
	if !stderrors.As(wrapped, &got2) {
		t.Errorf("errors.As through fmt.Errorf chain failed")
	}
	if got2.StatusCode != 500 {
		t.Errorf("wrapped StatusCode = %d, want 500", got2.StatusCode)
	}

	// errors.Is against the original API error works through the chain
	if !stderrors.Is(wrapped, api) {
		t.Errorf("errors.Is(wrapped, api) failed")
	}

	// A *PolypodError sentinel can be matched when it is the concrete type.
	pe := &PolypodError{Code: CodeNetwork, Message: "net"}
	peWrapped := fmt.Errorf("outer: %w", pe)
	var peTarget *PolypodError
	if !stderrors.As(peWrapped, &peTarget) {
		t.Errorf("errors.As(*PolypodError) through wrapping failed")
	}
	if peTarget.Code != CodeNetwork {
		t.Errorf("Code = %v, want CodeNetwork", peTarget.Code)
	}
}

// ---------------------------------------------------------------------------
// Classification helpers
// ---------------------------------------------------------------------------

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"429 rate limit", WrapAPIError("rl", http.StatusTooManyRequests, nil), true},
		{"500 server", WrapAPIError("boom", http.StatusInternalServerError, nil), true},
		{"503 unavailable", WrapAPIError("u", 503, nil), true},
		{"400 bad req", WrapAPIError("bad", http.StatusBadRequest, nil), false},
		{"401 unauth", WrapAPIError("u", http.StatusUnauthorized, nil), false},
		{"timeout polypod", &PolypodError{Code: CodeTimeout, Message: "t"}, true},
		{"network polypod", &PolypodError{Code: CodeNetwork, Message: "n"}, true},
		{"validation polypod", &PolypodError{Code: CodeValidation, Message: "v"}, false},
		{"plain error", stderrors.New("plain"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRetryable(tc.err); got != tc.want {
				t.Errorf("IsRetryable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsRateLimited(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"429", WrapAPIError("rl", http.StatusTooManyRequests, nil), true},
		{"500", WrapAPIError("boom", 500, nil), false},
		{"plain", stderrors.New("plain"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRateLimited(tc.err); got != tc.want {
				t.Errorf("IsRateLimited = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsAuthError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"401", WrapAPIError("u", http.StatusUnauthorized, nil), true},
		{"403", WrapAPIError("f", http.StatusForbidden, nil), true},
		{"500", WrapAPIError("s", 500, nil), false},
		{"permission error", NewPermissionError("no"), true},
		{"plain", stderrors.New("plain"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsAuthError(tc.err); got != tc.want {
				t.Errorf("IsAuthError = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsAbort(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"abort error", NewAbortError("stop"), true},
		{"wrapped abort", fmt.Errorf("ctx: %w", NewAbortError("x")), true},
		{"plain", stderrors.New("plain"), false},
		{"api error", WrapAPIError("a", 500, nil), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsAbort(tc.err); got != tc.want {
				t.Errorf("IsAbort = %v, want %v", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Utility helpers: ErrorMessage / ToError / ShortErrorStack
// ---------------------------------------------------------------------------

func TestErrorMessage(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"plain", stderrors.New("oops"), "oops"},
		{"polypod", NewPermissionError("denied"), "[permission] denied"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ErrorMessage(tc.err); got != tc.want {
				t.Errorf("ErrorMessage = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestToError(t *testing.T) {
	original := stderrors.New("orig")
	cases := []struct {
		name    string
		in      any
		wantNil bool
		wantSub string
	}{
		{"nil", nil, true, ""},
		{"error passthrough", original, false, "orig"},
		{"string", "failure", false, "failure"},
		{"int", 42, false, "42"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ToError(tc.in)
			if tc.wantNil {
				if got != nil {
					t.Errorf("want nil, got %v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("want non-nil error")
			}
			if !strings.Contains(got.Error(), tc.wantSub) {
				t.Errorf("error %q does not contain %q", got.Error(), tc.wantSub)
			}
			if tc.name == "error passthrough" && got != original {
				t.Errorf("error passthrough should return same instance")
			}
		})
	}
}

func TestShortErrorStack(t *testing.T) {
	// Build a chain where each frame has a DIFFERENT Error() string, so that
	// the frame-count limit is observable. PolypodError without a Cause
	// produces a self-contained message that does not embed the child's text.
	inner := &PolypodError{Code: CodeNetwork, Message: "innermost"}
	mid := &PolypodError{Code: CodeAPI, Message: "middle", Cause: inner}
	// Use a non-wrapping outer via fmt.Errorf + %w so Unwrap returns mid.
	outer := fmt.Errorf("outer context: %w", mid)

	cases := []struct {
		name      string
		err       error
		maxFrames int
		wantEmpty bool
		wantSubs  []string
	}{
		{"nil", nil, 5, true, nil},
		{"zero frames uses default", inner, 0, false, []string{"innermost"}},
		{"full chain", outer, 5, false, []string{"outer context", "middle", "caused by"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ShortErrorStack(tc.err, tc.maxFrames)
			if tc.wantEmpty {
				if got != "" {
					t.Errorf("want empty, got %q", got)
				}
				return
			}
			for _, sub := range tc.wantSubs {
				if !strings.Contains(got, sub) {
					t.Errorf("result %q missing substring %q", got, sub)
				}
			}
		})
	}

	// Frame limit observable behaviour: a two-level chain at maxFrames=1
	// should NOT contain a "caused by" separator.
	t.Run("frame limit stops after first level", func(t *testing.T) {
		got := ShortErrorStack(mid, 1)
		if strings.Contains(got, "caused by") {
			t.Errorf("expected no caused-by separator at maxFrames=1, got %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// ClassifyHTTPError
// ---------------------------------------------------------------------------

func TestClassifyHTTPError(t *testing.T) {
	base := stderrors.New("boom")

	cases := []struct {
		name       string
		err        error
		status     int
		errCode    string
		wantKind   HTTPErrorKind
		wantStatus int
	}{
		{"auth 401", base, http.StatusUnauthorized, "", HTTPErrorKindAuth, 401},
		{"auth 403", base, http.StatusForbidden, "", HTTPErrorKindAuth, 403},
		{"timeout", base, 0, "ECONNABORTED", HTTPErrorKindTimeout, 0},
		{"network conn refused", base, 0, "ECONNREFUSED", HTTPErrorKindNetwork, 0},
		{"network not found", base, 0, "ENOTFOUND", HTTPErrorKindNetwork, 0},
		{"http 500", base, 500, "", HTTPErrorKindHTTP, 500},
		{"http 404", base, 404, "", HTTPErrorKindHTTP, 404},
		{"other", base, 0, "", HTTPErrorKindOther, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyHTTPError(tc.err, tc.status, tc.errCode)
			if got.Kind != tc.wantKind {
				t.Errorf("Kind = %v, want %v", got.Kind, tc.wantKind)
			}
			if got.StatusCode != tc.wantStatus {
				t.Errorf("StatusCode = %d, want %d", got.StatusCode, tc.wantStatus)
			}
			if got.Message != "boom" {
				t.Errorf("Message = %q, want %q", got.Message, "boom")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// FormatUserError / FormatLogError
// ---------------------------------------------------------------------------

func TestFormatUserError(t *testing.T) {
	// FormatUserError uses errors.As(&*PolypodError{}). Because Go's errors.As
	// does not traverse struct embedding, it only matches when the chain
	// contains a direct *PolypodError. Concrete subtypes fall through to
	// their full Error() string (which already includes a "[code]" prefix).
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "direct PolypodError returns bare message",
			err:  &PolypodError{Code: CodePermission, Message: "access denied"},
			want: "access denied",
		},
		{
			name: "wrapped direct PolypodError returns bare message",
			err:  fmt.Errorf("ctx: %w", &PolypodError{Code: CodeValidation, Message: "invalid value"}),
			want: "invalid value",
		},
		{
			name: "concrete subtype falls through to full Error()",
			err:  NewPermissionError("access denied"),
			want: "[permission] access denied",
		},
		{
			name: "plain error full message",
			err:  stderrors.New("raw error"),
			want: "raw error",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatUserError(tc.err); got != tc.want {
				t.Errorf("FormatUserError = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatLogError(t *testing.T) {
	inner := stderrors.New("root")
	wrapped := fmt.Errorf("layer: %w", inner)
	got := FormatLogError(wrapped)
	if !strings.Contains(got, "root") || !strings.Contains(got, "layer") {
		t.Errorf("FormatLogError missing parts: %q", got)
	}
	if !strings.Contains(got, "caused by") {
		t.Errorf("FormatLogError should include 'caused by': %q", got)
	}
}

// ---------------------------------------------------------------------------
// Tool error constructors and helpers
// ---------------------------------------------------------------------------

func TestToolErrorConstructors(t *testing.T) {
	cause := stderrors.New("child failed")

	t.Run("WrapToolError", func(t *testing.T) {
		e := WrapToolError("bash", `{"cmd":"ls"}`, "partial", cause)
		if e.ToolName != "bash" {
			t.Errorf("ToolName = %q", e.ToolName)
		}
		if e.Input != `{"cmd":"ls"}` {
			t.Errorf("Input = %q", e.Input)
		}
		if e.Output != "partial" {
			t.Errorf("Output = %q", e.Output)
		}
		if e.Code != CodeTool {
			t.Errorf("Code = %v, want CodeTool", e.Code)
		}
		if !stderrors.Is(e, cause) {
			t.Errorf("errors.Is cause failed")
		}
		if !strings.Contains(e.Message, `"bash"`) {
			t.Errorf("Message should contain tool name: %q", e.Message)
		}
	})

	t.Run("NewToolTimeoutError", func(t *testing.T) {
		e := NewToolTimeoutError("read", 3000)
		if e.Code != CodeTimeout {
			t.Errorf("Code = %v, want CodeTimeout", e.Code)
		}
		if e.TimeoutMS != 3000 {
			t.Errorf("TimeoutMS = %d", e.TimeoutMS)
		}
		if !strings.Contains(e.Message, "3000ms") {
			t.Errorf("Message = %q", e.Message)
		}
	})

	t.Run("NewToolPermissionDenied", func(t *testing.T) {
		e := NewToolPermissionDenied("write", "path outside sandbox")
		if e.Code != CodePermission {
			t.Errorf("Code = %v, want CodePermission", e.Code)
		}
		if e.Reason != "path outside sandbox" {
			t.Errorf("Reason = %q", e.Reason)
		}
	})
}

func TestNewToolValidationErrorMessage(t *testing.T) {
	cases := []struct {
		name     string
		tool     string
		issues   []ValidationIssue
		wantSubs []string
	}{
		{
			name:     "no issues",
			tool:     "foo",
			issues:   nil,
			wantSubs: []string{"foo", "validation failed"},
		},
		{
			name: "single missing",
			tool: "foo",
			issues: []ValidationIssue{
				{Type: "missing", Param: "name"},
			},
			wantSubs: []string{"required parameter `name` is missing", "issue"},
		},
		{
			name: "multiple mixed",
			tool: "foo",
			issues: []ValidationIssue{
				{Type: "missing", Param: "a"},
				{Type: "unexpected", Param: "b"},
				{Type: "type_mismatch", Param: "c", Details: "expected string, got number"},
				{Type: "other", Param: "d", Details: "weird"},
			},
			wantSubs: []string{
				"required parameter `a` is missing",
				"unexpected parameter `b` was provided",
				"parameter `c` expected string, got number",
				"Parameter `d`: weird",
				"issues", // plural
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := NewToolValidationError(tc.tool, tc.issues)
			if e.Code != CodeValidation {
				t.Errorf("Code = %v, want CodeValidation", e.Code)
			}
			if e.ToolName != tc.tool {
				t.Errorf("ToolName = %q", e.ToolName)
			}
			for _, sub := range tc.wantSubs {
				if !strings.Contains(e.Message, sub) {
					t.Errorf("message %q missing %q", e.Message, sub)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ClassifyToolError
// ---------------------------------------------------------------------------

func TestClassifyToolError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want ToolErrorCategory
	}{
		{"nil", nil, ToolCategoryUnknown},
		{"abort", NewAbortError("x"), ToolCategoryAbort},
		{"timeout", NewToolTimeoutError("t", 100), ToolCategoryTimeout},
		{"permission", NewToolPermissionDenied("p", "r"), ToolCategoryPermission},
		{"validation", NewToolValidationError("v", nil), ToolCategoryValidation},
		{"execution", WrapToolError("e", "", "", stderrors.New("x")), ToolCategoryExecution},
		{"plain unknown", stderrors.New("what"), ToolCategoryUnknown},
		{"polypod unknown", NewPermissionError("no"), ToolCategoryUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyToolError(tc.err); got != tc.want {
				t.Errorf("ClassifyToolError = %v, want %v", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// FormatToolError
// ---------------------------------------------------------------------------

func TestFormatToolError(t *testing.T) {
	longMsg := strings.Repeat("a", 12000)

	cases := []struct {
		name      string
		err       error
		wantExact string // if set, require exact match
		wantSubs  []string
		wantNoSub []string
	}{
		{
			name:      "nil",
			err:       nil,
			wantExact: "",
		},
		{
			name:     "abort",
			err:      NewAbortError("cancel"),
			wantSubs: []string{"Operation was interrupted"},
		},
		{
			name: "shell error with stdout/stderr",
			err:  NewShellError(2, "out-data", "err-data", false),
			wantSubs: []string{
				"Exit code 2",
				"err-data",
				"out-data",
			},
			wantNoSub: []string{"Operation was interrupted"},
		},
		{
			name: "shell error interrupted",
			err:  NewShellError(130, "", "", true),
			wantSubs: []string{
				"Exit code 130",
				"Operation was interrupted",
			},
		},
		{
			name:      "shell error empty",
			err:       &ShellError{PolypodError: PolypodError{Code: CodeShell, Message: "x"}, ExitCode: 0},
			wantExact: "Exit code 0",
		},
		{
			name: "tool execution with output",
			err:  WrapToolError("bash", "{}", "partial-output", stderrors.New("inner")),
			wantSubs: []string{
				`"bash"`,
				"partial-output",
			},
		},
		{
			name:     "plain error",
			err:      stderrors.New("plain text"),
			wantSubs: []string{"plain text"},
		},
		{
			name:     "long message truncated",
			err:      stderrors.New(longMsg),
			wantSubs: []string{"characters truncated"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatToolError(tc.err)
			if tc.wantExact != "" {
				if got != tc.wantExact {
					t.Errorf("FormatToolError = %q, want %q", got, tc.wantExact)
				}
				return
			}
			for _, sub := range tc.wantSubs {
				if !strings.Contains(got, sub) {
					t.Errorf("result missing %q: %q", sub, got)
				}
			}
			for _, sub := range tc.wantNoSub {
				if strings.Contains(got, sub) {
					t.Errorf("result should not contain %q: %q", sub, got)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Package-level As helper
// ---------------------------------------------------------------------------

func TestAsHelper(t *testing.T) {
	cause := stderrors.New("root")
	api := WrapAPIError("wrapper", 502, cause)

	var target *APIError
	if !As(api, &target) {
		t.Fatalf("As failed to match *APIError")
	}
	if target.StatusCode != 502 {
		t.Errorf("StatusCode = %d, want 502", target.StatusCode)
	}

	// Non-matching target
	var perm *PermissionError
	if As(api, &perm) {
		t.Errorf("As should not match *PermissionError for *APIError")
	}
}
