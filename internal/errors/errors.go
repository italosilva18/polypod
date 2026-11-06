// Package errors provides structured error types for Polypod,
// migrated from the TypeScript utils/errors.ts module.
package errors

import (
	stderrors "errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrorCode identifies the category of an error for programmatic handling.
type ErrorCode int

const (
	CodeUnknown ErrorCode = iota
	CodeAbort
	CodeTool
	CodeAPI
	CodePermission
	CodeValidation
	CodeCompaction
	CodeSession
	CodeConfig
	CodeShell
	CodeRateLimit
	CodeAuth
	CodeNetwork
	CodeTimeout
	CodeTeleport
)

// String returns a human-readable name for the error code.
func (c ErrorCode) String() string {
	switch c {
	case CodeAbort:
		return "abort"
	case CodeTool:
		return "tool"
	case CodeAPI:
		return "api"
	case CodePermission:
		return "permission"
	case CodeValidation:
		return "validation"
	case CodeCompaction:
		return "compaction"
	case CodeSession:
		return "session"
	case CodeConfig:
		return "config"
	case CodeShell:
		return "shell"
	case CodeRateLimit:
		return "rate_limit"
	case CodeAuth:
		return "auth"
	case CodeNetwork:
		return "network"
	case CodeTimeout:
		return "timeout"
	case CodeTeleport:
		return "teleport"
	default:
		return "unknown"
	}
}

// PolypodError is the base error type carrying a code and optional wrapped cause.
type PolypodError struct {
	Code    ErrorCode
	Message string
	Cause   error
}

func (e *PolypodError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *PolypodError) Unwrap() error { return e.Cause }

// --- Concrete error types ---

// AbortError signals an intentional cancellation (user abort, context cancelled).
type AbortError struct{ PolypodError }

// NewAbortError creates an AbortError.
func NewAbortError(msg string) *AbortError {
	if msg == "" {
		msg = "operation aborted"
	}
	return &AbortError{PolypodError{Code: CodeAbort, Message: msg}}
}

// APIError represents a failure from an upstream API call.
type APIError struct {
	PolypodError
	StatusCode int
	RetryAfter string // from Retry-After header, if present
}

// WrapAPIError creates an APIError wrapping a cause.
func WrapAPIError(msg string, statusCode int, cause error) *APIError {
	return &APIError{
		PolypodError: PolypodError{Code: CodeAPI, Message: msg, Cause: cause},
		StatusCode:   statusCode,
	}
}

// PermissionError represents a denied operation.
type PermissionError struct{ PolypodError }

// NewPermissionError creates a PermissionError.
func NewPermissionError(msg string) *PermissionError {
	return &PermissionError{PolypodError{Code: CodePermission, Message: msg}}
}

// ValidationError represents invalid input or parameters.
type ValidationError struct {
	PolypodError
	Field string
}

// NewValidationError creates a ValidationError.
func NewValidationError(field, msg string) *ValidationError {
	return &ValidationError{
		PolypodError: PolypodError{Code: CodeValidation, Message: msg},
		Field:        field,
	}
}

// CompactionError represents a failure during conversation compaction.
type CompactionError struct{ PolypodError }

// NewCompactionError creates a CompactionError.
func NewCompactionError(msg string, cause error) *CompactionError {
	return &CompactionError{PolypodError{Code: CodeCompaction, Message: msg, Cause: cause}}
}

// SessionError represents a session-level failure.
type SessionError struct{ PolypodError }

// NewSessionError creates a SessionError.
func NewSessionError(msg string, cause error) *SessionError {
	return &SessionError{PolypodError{Code: CodeSession, Message: msg, Cause: cause}}
}

// ConfigParseError represents a failure to parse a config file.
type ConfigParseError struct {
	PolypodError
	FilePath string
}

// NewConfigParseError creates a ConfigParseError.
func NewConfigParseError(filePath, msg string, cause error) *ConfigParseError {
	return &ConfigParseError{
		PolypodError: PolypodError{Code: CodeConfig, Message: msg, Cause: cause},
		FilePath:     filePath,
	}
}

// ShellError represents a failed shell command.
type ShellError struct {
	PolypodError
	Stdout      string
	Stderr      string
	ExitCode    int
	Interrupted bool
}

// NewShellError creates a ShellError.
func NewShellError(exitCode int, stdout, stderr string, interrupted bool) *ShellError {
	return &ShellError{
		PolypodError: PolypodError{Code: CodeShell, Message: "shell command failed"},
		Stdout:       stdout,
		Stderr:       stderr,
		ExitCode:     exitCode,
		Interrupted:  interrupted,
	}
}

// TeleportError represents a teleport operation failure with a user-facing formatted message.
type TeleportError struct {
	PolypodError
	FormattedMessage string
}

// NewTeleportError creates a TeleportError.
func NewTeleportError(msg, formatted string) *TeleportError {
	return &TeleportError{
		PolypodError:     PolypodError{Code: CodeTeleport, Message: msg},
		FormattedMessage: formatted,
	}
}

// --- TelemetrySafeError ---

// TelemetrySafeError is an error whose message is verified safe for telemetry
// (no PII, file paths, code snippets).
type TelemetrySafeError struct {
	PolypodError
	TelemetryMessage string // sanitised message for telemetry; may differ from Error()
}

// NewTelemetrySafeError creates a TelemetrySafeError.
// If telemetryMsg is empty, the main message is used for telemetry.
func NewTelemetrySafeError(msg, telemetryMsg string) *TelemetrySafeError {
	if telemetryMsg == "" {
		telemetryMsg = msg
	}
	return &TelemetrySafeError{
		PolypodError:     PolypodError{Code: CodeUnknown, Message: msg},
		TelemetryMessage: telemetryMsg,
	}
}

// --- Classification helpers ---

// IsRetryable returns true if the error is likely transient and the operation
// can be retried (rate-limit 429, server errors 5xx, network timeouts).
func IsRetryable(err error) bool {
	var apiErr *APIError
	if stderrors.As(err, &apiErr) {
		if apiErr.StatusCode == http.StatusTooManyRequests {
			return true
		}
		if apiErr.StatusCode >= 500 && apiErr.StatusCode < 600 {
			return true
		}
	}
	if IsRateLimited(err) {
		return true
	}
	var polypodErr *PolypodError
	if stderrors.As(err, &polypodErr) {
		return polypodErr.Code == CodeTimeout || polypodErr.Code == CodeNetwork
	}
	return false
}

// IsRateLimited returns true if the error indicates a 429 rate-limit response.
func IsRateLimited(err error) bool {
	var apiErr *APIError
	if stderrors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusTooManyRequests
	}
	return false
}

// IsAuthError returns true if the error indicates an authentication failure (401/403).
func IsAuthError(err error) bool {
	var apiErr *APIError
	if stderrors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden
	}
	var permErr *PermissionError
	return stderrors.As(err, &permErr)
}

// IsAbort returns true if the error represents an intentional abort.
func IsAbort(err error) bool {
	var abortErr *AbortError
	return stderrors.As(err, &abortErr)
}

// --- Utility helpers ---

// ErrorMessage extracts a string message from an error (or any value via fmt).
func ErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ToError converts an arbitrary value to an error.
func ToError(v any) error {
	if v == nil {
		return nil
	}
	if err, ok := v.(error); ok {
		return err
	}
	return fmt.Errorf("%v", v)
}

// ShortErrorStack returns the error message plus up to maxFrames lines of context.
// In Go there are no JS-style stack traces attached to errors, so this returns
// the unwrap chain up to maxFrames levels.
func ShortErrorStack(err error, maxFrames int) string {
	if err == nil {
		return ""
	}
	if maxFrames <= 0 {
		maxFrames = 5
	}
	var parts []string
	current := err
	for i := 0; i < maxFrames && current != nil; i++ {
		parts = append(parts, current.Error())
		current = stderrors.Unwrap(current)
		if current != nil && current.Error() == parts[len(parts)-1] {
			break // avoid infinite loop when Unwrap returns same message
		}
	}
	return strings.Join(parts, "\n  caused by: ")
}

// HTTPErrorKind classifies an HTTP response status into a category string.
type HTTPErrorKind string

const (
	HTTPErrorKindAuth    HTTPErrorKind = "auth"
	HTTPErrorKindTimeout HTTPErrorKind = "timeout"
	HTTPErrorKindNetwork HTTPErrorKind = "network"
	HTTPErrorKindHTTP    HTTPErrorKind = "http"
	HTTPErrorKindOther   HTTPErrorKind = "other"
)

// HTTPClassification holds the result of ClassifyHTTPError.
type HTTPClassification struct {
	Kind       HTTPErrorKind
	StatusCode int
	Message    string
}

// ClassifyHTTPError classifies an error from an HTTP request. If statusCode
// is 0 and errCode is provided, network/timeout classification is attempted.
func ClassifyHTTPError(err error, statusCode int, errCode string) HTTPClassification {
	msg := ErrorMessage(err)
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		return HTTPClassification{Kind: HTTPErrorKindAuth, StatusCode: statusCode, Message: msg}
	}
	switch errCode {
	case "ECONNABORTED":
		return HTTPClassification{Kind: HTTPErrorKindTimeout, StatusCode: statusCode, Message: msg}
	case "ECONNREFUSED", "ENOTFOUND":
		return HTTPClassification{Kind: HTTPErrorKindNetwork, StatusCode: statusCode, Message: msg}
	}
	if statusCode > 0 {
		return HTTPClassification{Kind: HTTPErrorKindHTTP, StatusCode: statusCode, Message: msg}
	}
	return HTTPClassification{Kind: HTTPErrorKindOther, StatusCode: 0, Message: msg}
}

// FormatUserError returns a user-facing error message without internal details.
func FormatUserError(err error) string {
	var pe *PolypodError
	if stderrors.As(err, &pe) {
		return pe.Message
	}
	return err.Error()
}

// FormatLogError returns a detailed error string suitable for logging.
func FormatLogError(err error) string {
	return ShortErrorStack(err, 10)
}
