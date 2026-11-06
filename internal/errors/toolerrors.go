package errors

import (
	stderrors "errors"
	"fmt"
	"strings"
)

// --- Tool-specific error types ---

// ToolExecutionError captures a failure during tool execution, including the
// tool name, input, and partial output.
type ToolExecutionError struct {
	PolypodError
	ToolName string
	Input    string // JSON-serialised input provided to the tool
	Output   string // partial output captured before failure
}

// WrapToolError creates a ToolExecutionError wrapping a cause.
func WrapToolError(toolName, input, output string, cause error) *ToolExecutionError {
	return &ToolExecutionError{
		PolypodError: PolypodError{
			Code:    CodeTool,
			Message: fmt.Sprintf("tool %q execution failed", toolName),
			Cause:   cause,
		},
		ToolName: toolName,
		Input:    input,
		Output:   output,
	}
}

// ToolTimeoutError represents a tool that exceeded its time budget.
type ToolTimeoutError struct {
	PolypodError
	ToolName  string
	TimeoutMS int64
}

// NewToolTimeoutError creates a ToolTimeoutError.
func NewToolTimeoutError(toolName string, timeoutMS int64) *ToolTimeoutError {
	return &ToolTimeoutError{
		PolypodError: PolypodError{
			Code:    CodeTimeout,
			Message: fmt.Sprintf("tool %q timed out after %dms", toolName, timeoutMS),
		},
		ToolName:  toolName,
		TimeoutMS: timeoutMS,
	}
}

// ToolPermissionDenied represents a tool invocation denied by permissions.
type ToolPermissionDenied struct {
	PolypodError
	ToolName string
	Reason   string
}

// NewToolPermissionDenied creates a ToolPermissionDenied error.
func NewToolPermissionDenied(toolName, reason string) *ToolPermissionDenied {
	return &ToolPermissionDenied{
		PolypodError: PolypodError{
			Code:    CodePermission,
			Message: fmt.Sprintf("tool %q permission denied: %s", toolName, reason),
		},
		ToolName: toolName,
		Reason:   reason,
	}
}

// ToolValidationError represents invalid parameters passed to a tool.
type ToolValidationError struct {
	PolypodError
	ToolName string
	Issues   []ValidationIssue
}

// ValidationIssue describes a single parameter validation problem.
type ValidationIssue struct {
	Type    string // "missing", "unexpected", "type_mismatch"
	Param   string
	Details string // e.g., "expected string, got number"
}

// NewToolValidationError creates a ToolValidationError from a list of issues.
func NewToolValidationError(toolName string, issues []ValidationIssue) *ToolValidationError {
	msg := formatValidationIssues(toolName, issues)
	return &ToolValidationError{
		PolypodError: PolypodError{
			Code:    CodeValidation,
			Message: msg,
		},
		ToolName: toolName,
		Issues:   issues,
	}
}

func formatValidationIssues(toolName string, issues []ValidationIssue) string {
	if len(issues) == 0 {
		return fmt.Sprintf("%s: validation failed", toolName)
	}
	var parts []string
	for _, iss := range issues {
		switch iss.Type {
		case "missing":
			parts = append(parts, fmt.Sprintf("The required parameter `%s` is missing", iss.Param))
		case "unexpected":
			parts = append(parts, fmt.Sprintf("An unexpected parameter `%s` was provided", iss.Param))
		case "type_mismatch":
			parts = append(parts, fmt.Sprintf("The parameter `%s` %s", iss.Param, iss.Details))
		default:
			parts = append(parts, fmt.Sprintf("Parameter `%s`: %s", iss.Param, iss.Details))
		}
	}
	noun := "issue"
	if len(parts) > 1 {
		noun = "issues"
	}
	return fmt.Sprintf("%s failed due to the following %s:\n%s", toolName, noun, strings.Join(parts, "\n"))
}

// --- ToolErrorClassifier ---

// ToolErrorCategory is used for telemetry classification.
type ToolErrorCategory string

const (
	ToolCategoryTimeout    ToolErrorCategory = "timeout"
	ToolCategoryPermission ToolErrorCategory = "permission"
	ToolCategoryValidation ToolErrorCategory = "validation"
	ToolCategoryExecution  ToolErrorCategory = "execution"
	ToolCategoryAbort      ToolErrorCategory = "abort"
	ToolCategoryUnknown    ToolErrorCategory = "unknown"
)

// ClassifyToolError returns a telemetry category for a tool error.
func ClassifyToolError(err error) ToolErrorCategory {
	if err == nil {
		return ToolCategoryUnknown
	}
	switch {
	case IsAbort(err):
		return ToolCategoryAbort
	}
	// Check concrete tool error types
	switch err.(type) {
	case *ToolTimeoutError:
		return ToolCategoryTimeout
	case *ToolPermissionDenied:
		return ToolCategoryPermission
	case *ToolValidationError:
		return ToolCategoryValidation
	case *ToolExecutionError:
		return ToolCategoryExecution
	}
	return ToolCategoryUnknown
}

// FormatToolError produces a user-facing message for a tool error,
// mirroring the TS formatError function. Long messages are truncated
// with the middle elided.
func FormatToolError(err error) string {
	if err == nil {
		return ""
	}

	// Abort → short message
	var abortErr *AbortError
	if IsAbort(err) {
		if abortErr != nil {
			return abortErr.Message
		}
		return "Operation was interrupted"
	}

	// ShellError → structured parts
	var shellErr *ShellError
	if ok := As(err, &shellErr); ok {
		return formatShellErrorParts(shellErr)
	}

	// ToolExecutionError → include partial output
	var toolExec *ToolExecutionError
	if ok := As(err, &toolExec); ok {
		parts := []string{toolExec.Message}
		if toolExec.Output != "" {
			parts = append(parts, toolExec.Output)
		}
		return truncateMessage(strings.Join(parts, "\n"), 10000)
	}

	return truncateMessage(err.Error(), 10000)
}

func formatShellErrorParts(e *ShellError) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("Exit code %d", e.ExitCode))
	if e.Interrupted {
		parts = append(parts, "Operation was interrupted")
	}
	if e.Stderr != "" {
		parts = append(parts, e.Stderr)
	}
	if e.Stdout != "" {
		parts = append(parts, e.Stdout)
	}
	full := strings.Join(parts, "\n")
	if full == "" {
		return "Command failed with no output"
	}
	return truncateMessage(full, 10000)
}

// truncateMessage keeps up to maxLen characters, eliding the middle.
func truncateMessage(msg string, maxLen int) string {
	if len(msg) <= maxLen {
		return msg
	}
	half := maxLen / 2
	elided := len(msg) - maxLen
	return fmt.Sprintf("%s\n\n... [%d characters truncated] ...\n\n%s",
		msg[:half], elided, msg[len(msg)-half:])
}

// As is a convenience re-export of errors.As for callers in this package.
func As(err error, target any) bool { return stderrors.As(err, target) }
