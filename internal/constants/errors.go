package constants

// Error constants migrated from Claude Code TypeScript source.
// Source: constants/errorIds.ts, constants/messages.ts

// ---------------------------------------------------------------------------
// Error IDs — obfuscated identifiers for tracking error sources.
// ---------------------------------------------------------------------------

const (
	// EToolUseSummaryGenerationFailed is the error ID for tool use summary generation failures.
	EToolUseSummaryGenerationFailed = 344
)

// NextErrorID is the next available error ID. Increment when adding new errors.
const NextErrorID = 346

// ---------------------------------------------------------------------------
// Error categories
// ---------------------------------------------------------------------------

// ErrorCategory groups error types for reporting and handling.
type ErrorCategory string

const (
	ErrorCategoryAPI        ErrorCategory = "api"
	ErrorCategoryTool       ErrorCategory = "tool"
	ErrorCategoryPermission ErrorCategory = "permission"
	ErrorCategoryConfig     ErrorCategory = "config"
	ErrorCategoryNetwork    ErrorCategory = "network"
	ErrorCategoryFileSystem ErrorCategory = "filesystem"
	ErrorCategoryAuth       ErrorCategory = "auth"
	ErrorCategoryTask       ErrorCategory = "task"
	ErrorCategoryMCP        ErrorCategory = "mcp"
	ErrorCategoryInternal   ErrorCategory = "internal"
)

// ---------------------------------------------------------------------------
// Error message templates
// ---------------------------------------------------------------------------

const (
	// ErrMsgNoContent is the placeholder for empty content.
	ErrMsgNoContent = "(no content)"

	// ErrMsgTaskNotFound is the template for task not found errors.
	ErrMsgTaskNotFound = "no task found with ID: %s"

	// ErrMsgTaskNotRunning is the template for task not running errors.
	ErrMsgTaskNotRunning = "task %s is not running (status: %s)"

	// ErrMsgUnsupportedTaskType is the template for unsupported task type errors.
	ErrMsgUnsupportedTaskType = "unsupported task type: %s"

	// ErrMsgSkillNotFound is the template for skill not found errors.
	ErrMsgSkillNotFound = "skill not found: %s"

	// ErrMsgAPIRateLimited is the template for API rate limit errors.
	ErrMsgAPIRateLimited = "API rate limited — retry after %s"

	// ErrMsgAPIOverloaded is the message for API overload errors.
	ErrMsgAPIOverloaded = "API is temporarily overloaded — please try again"

	// ErrMsgAuthExpired is the message for expired authentication.
	ErrMsgAuthExpired = "authentication expired — please log in again"

	// ErrMsgMaxMediaExceeded is the template for exceeding media limits.
	ErrMsgMaxMediaExceeded = "too many media items in request (%d), maximum is %d"

	// ErrMsgFileTooLarge is the template for files exceeding size limits.
	ErrMsgFileTooLarge = "file too large (%d bytes), maximum is %d bytes"

	// ErrMsgBinaryFile is the message for attempted text operations on binary files.
	ErrMsgBinaryFile = "file appears to be binary — cannot perform text operations"

	// ErrMsgPermissionDenied is the message for permission-denied tool calls.
	ErrMsgPermissionDenied = "permission denied by user"

	// ErrMsgToolTimeout is the template for tool execution timeout.
	ErrMsgToolTimeout = "tool execution timed out after %s"

	// ErrMsgContextOverflow is the message for context window overflow.
	ErrMsgContextOverflow = "conversation exceeds context window — automatic summarization will be applied"
)

// ---------------------------------------------------------------------------
// Stop task error codes
// ---------------------------------------------------------------------------

// StopTaskErrorCode categorizes stop-task failures.
type StopTaskErrorCode string

const (
	StopTaskNotFound        StopTaskErrorCode = "not_found"
	StopTaskNotRunning      StopTaskErrorCode = "not_running"
	StopTaskUnsupportedType StopTaskErrorCode = "unsupported_type"
)
