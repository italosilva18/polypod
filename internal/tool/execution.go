package tool

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/costa/polypod/internal/constants"
	"github.com/costa/polypod/internal/hooks"
	"github.com/costa/polypod/internal/types"
)

// ExecutionConfig configures tool execution behavior.
type ExecutionConfig struct {
	// DefaultTimeout is applied when a tool call has no explicit timeout.
	DefaultTimeout time.Duration

	// MaxResultBytes truncates tool output exceeding this size.
	MaxResultBytes int

	// MaxConcurrent is the maximum number of tools running simultaneously.
	MaxConcurrent int

	// Logger for execution events.
	Logger *slog.Logger

	// Hooks manager (optional).
	Hooks *hooks.Manager
}

// DefaultExecutionConfig returns production defaults.
func DefaultExecutionConfig() ExecutionConfig {
	return ExecutionConfig{
		DefaultTimeout: 120 * time.Second,
		MaxResultBytes: constants.MaxOutputBytes,
		MaxConcurrent:  constants.MaxConcurrentTools,
		Logger:         slog.Default(),
	}
}

// ExecutionResult is the full outcome of a tool execution pipeline run.
type ExecutionResult struct {
	// ToolUseID is the unique identifier for this tool invocation.
	ToolUseID string

	// ToolName is the name of the tool that was executed.
	ToolName string

	// Result is the tool's output.
	Result types.ToolResult

	// Duration is how long the tool execution took.
	Duration time.Duration

	// ContextModifier optionally transforms the context after execution.
	// This allows tools to inject state (e.g., working directory changes).
	ContextModifier func(types.ToolContext) types.ToolContext

	// Phase records where in the pipeline execution ended.
	Phase ExecutionPhase

	// Error is set if execution failed with an unrecoverable error.
	Error error
}

// ExecutionPhase marks where in the pipeline execution completed or failed.
type ExecutionPhase string

const (
	PhaseValidation  ExecutionPhase = "validation"
	PhasePermission  ExecutionPhase = "permission"
	PhasePreHook     ExecutionPhase = "pre_hook"
	PhaseExecution   ExecutionPhase = "execution"
	PhasePostHook    ExecutionPhase = "post_hook"
	PhaseComplete    ExecutionPhase = "complete"
	PhaseCancelled   ExecutionPhase = "cancelled"
	PhaseTimeout     ExecutionPhase = "timeout"
	PhaseToolUnknown ExecutionPhase = "tool_unknown"
)

// ToolUseRequest describes a single tool invocation to execute.
type ToolUseRequest struct {
	ID        string
	ToolName  string
	Input     map[string]any
	MessageID string
	RequestID string
}

// ToolMetrics collects telemetry for tool executions.
type ToolMetrics struct {
	mu               sync.Mutex
	totalExecutions  int64
	totalErrors      int64
	totalDuration    time.Duration
	executionsByTool map[string]int64
	errorsByTool     map[string]int64
	durationByTool   map[string]time.Duration
}

// NewToolMetrics creates a metrics collector.
func NewToolMetrics() *ToolMetrics {
	return &ToolMetrics{
		executionsByTool: make(map[string]int64),
		errorsByTool:     make(map[string]int64),
		durationByTool:   make(map[string]time.Duration),
	}
}

// Record records a tool execution result.
func (m *ToolMetrics) Record(toolName string, duration time.Duration, isError bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.totalExecutions++
	m.totalDuration += duration
	m.executionsByTool[toolName]++
	m.durationByTool[toolName] += duration
	if isError {
		m.totalErrors++
		m.errorsByTool[toolName]++
	}
}

// Snapshot returns a point-in-time copy of the metrics.
func (m *ToolMetrics) Snapshot() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	byTool := make(map[string]map[string]any, len(m.executionsByTool))
	for name, count := range m.executionsByTool {
		byTool[name] = map[string]any{
			"executions": count,
			"errors":     m.errorsByTool[name],
			"duration":   m.durationByTool[name].String(),
		}
	}
	return map[string]any{
		"total_executions": m.totalExecutions,
		"total_errors":     m.totalErrors,
		"total_duration":   m.totalDuration.String(),
		"by_tool":          byTool,
	}
}

// ToolExecutor runs tools through the full lifecycle pipeline.
type ToolExecutor struct {
	registry *Registry
	config   ExecutionConfig
	metrics  *ToolMetrics
	inflight atomic.Int64
}

// NewToolExecutor creates a tool executor with the given registry and config.
func NewToolExecutor(registry *Registry, config ExecutionConfig) *ToolExecutor {
	return &ToolExecutor{
		registry: registry,
		config:   config,
		metrics:  NewToolMetrics(),
	}
}

// Metrics returns the executor's metrics collector.
func (e *ToolExecutor) Metrics() *ToolMetrics {
	return e.metrics
}

// Inflight returns the number of currently executing tools.
func (e *ToolExecutor) Inflight() int64 {
	return e.inflight.Load()
}

// ExecuteTool runs a single tool through the full pipeline:
//
//	validate → permission → pre-hooks → execute → post-hooks
//
// It respects context cancellation at every stage and enforces per-tool timeouts.
// The onProgress callback (if non-nil) receives progress updates during execution.
func (e *ToolExecutor) ExecuteTool(
	ctx context.Context,
	req ToolUseRequest,
	toolCtx types.ToolContext,
	onProgress func(types.ToolProgress),
) ExecutionResult {
	start := time.Now()
	logger := e.config.Logger.With(
		"tool", req.ToolName,
		"tool_use_id", req.ID,
	)

	result := ExecutionResult{
		ToolUseID: req.ID,
		ToolName:  req.ToolName,
	}

	// Check cancellation before starting.
	if ctx.Err() != nil {
		result.Phase = PhaseCancelled
		result.Result = types.ToolResult{
			Content: "tool execution cancelled",
			IsError: true,
		}
		result.Duration = time.Since(start)
		return result
	}

	// 1. Resolve tool.
	t, ok := e.registry.Get(req.ToolName)
	if !ok {
		logger.Warn("unknown tool requested")
		result.Phase = PhaseToolUnknown
		result.Result = types.ToolResult{
			Content: fmt.Sprintf("no such tool available: %s", req.ToolName),
			IsError: true,
		}
		result.Duration = time.Since(start)
		return result
	}

	// 2. Validate input.
	vr := t.ValidateInput(req.Input)
	if !vr.Valid {
		logger.Debug("input validation failed", "message", vr.Message)
		result.Phase = PhaseValidation
		result.Result = types.ToolResult{
			Content: fmt.Sprintf("InputValidationError: %s", vr.Message),
			IsError: true,
		}
		result.Duration = time.Since(start)
		return result
	}

	// 3. Check tool-level permissions.
	pr := t.CheckPermissions(req.Input, toolCtx)
	if pr.Behavior == types.PermissionDeny {
		logger.Debug("permission denied", "message", pr.Message)
		result.Phase = PhasePermission
		result.Result = types.ToolResult{
			Content: fmt.Sprintf("permission denied: %s", pr.Message),
			IsError: true,
		}
		result.Duration = time.Since(start)
		return result
	}

	// Apply updated input from permission check.
	input := req.Input
	if pr.UpdatedInput != nil {
		input = pr.UpdatedInput
	}

	// 4. Fire PreToolUse hooks.
	if e.config.Hooks != nil {
		if ctx.Err() != nil {
			result.Phase = PhaseCancelled
			result.Result = types.ToolResult{Content: "cancelled before hooks", IsError: true}
			result.Duration = time.Since(start)
			return result
		}

		hookResult, err := e.config.Hooks.FireWithProgress(ctx, hooks.HookPayload{
			Event:     hooks.EventPreToolUse,
			Timestamp: time.Now().Format(time.RFC3339),
			ToolName:  req.ToolName,
			ToolArgs:  input,
			SessionID: toolCtx.SessionID.String(),
		}, onProgress)

		if err != nil {
			logger.Warn("pre-hook error", "error", err)
		}

		if hookResult != nil {
			if hookResult.Decision == hooks.DecisionDeny {
				result.Phase = PhasePreHook
				result.Result = types.ToolResult{
					Content: fmt.Sprintf("hook denied: %s", hookResult.Message),
					IsError: true,
				}
				result.Duration = time.Since(start)
				return result
			}
			// Apply hook-modified input.
			if hookResult.Modified != nil {
				for k, v := range hookResult.Modified {
					input[k] = v
				}
			}
		}
	}

	// 5. Execute the tool with timeout enforcement.
	e.inflight.Add(1)
	defer e.inflight.Add(-1)

	timeout := e.config.DefaultTimeout
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Wire the timeout context into the tool context.
	execToolCtx := toolCtx
	execToolCtx.Context = execCtx
	execToolCtx.OnProgress = onProgress

	logger.Debug("executing tool")
	execStart := time.Now()

	toolResult, err := t.Execute(execToolCtx, input)

	execDuration := time.Since(execStart)
	totalDuration := time.Since(start)

	// Record metrics.
	e.metrics.Record(req.ToolName, execDuration, err != nil || toolResult.IsError)

	if err != nil {
		// Check if the error is a timeout.
		if execCtx.Err() == context.DeadlineExceeded {
			logger.Warn("tool execution timed out", "timeout", timeout)
			result.Phase = PhaseTimeout
			result.Result = types.ToolResult{
				Content: fmt.Sprintf("tool execution timed out after %s", timeout),
				IsError: true,
			}
			result.Duration = totalDuration
			result.Error = err
			return result
		}

		// Check if cancelled.
		if ctx.Err() != nil {
			result.Phase = PhaseCancelled
			result.Result = types.ToolResult{
				Content: "tool execution cancelled",
				IsError: true,
			}
			result.Duration = totalDuration
			result.Error = err
			return result
		}

		logger.Error("tool execution error", "error", err, "duration", execDuration)
		result.Phase = PhaseExecution
		result.Result = types.ToolResult{
			Content: FormatToolError(req.ToolName, err),
			IsError: true,
		}
		result.Duration = totalDuration
		result.Error = err
		return result
	}

	// 6. Truncate output if needed.
	toolResult.Content = TruncateResult(toolResult.Content, e.config.MaxResultBytes)

	// 7. Fire PostToolUse hooks.
	if e.config.Hooks != nil && !toolResult.IsError {
		_, hookErr := e.config.Hooks.Fire(ctx, hooks.HookPayload{
			Event:      hooks.EventPostToolUse,
			Timestamp:  time.Now().Format(time.RFC3339),
			ToolName:   req.ToolName,
			ToolArgs:   input,
			ToolResult: toolResult.Content,
			SessionID:  toolCtx.SessionID.String(),
		})
		if hookErr != nil {
			logger.Warn("post-hook error", "error", hookErr)
		}
	}

	logger.Debug("tool execution complete",
		"duration", totalDuration,
		"is_error", toolResult.IsError,
		"result_bytes", len(toolResult.Content),
	)

	result.Phase = PhaseComplete
	result.Result = toolResult
	result.Duration = totalDuration
	return result
}

// FormatToolError creates a user-facing error message for a tool execution failure.
func FormatToolError(toolName string, err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("Error executing %s: %v", toolName, err)
}

// TruncateResult truncates a tool result string if it exceeds maxBytes.
func TruncateResult(content string, maxBytes int) string {
	if maxBytes <= 0 || len(content) <= maxBytes {
		return content
	}
	return content[:maxBytes] + "\n... [output truncated]"
}

// ClassifyToolError categorizes an error into a telemetry-safe string.
// This avoids logging sensitive information while preserving diagnostic value.
func ClassifyToolError(err error) string {
	if err == nil {
		return ""
	}

	// Check for context-related errors.
	if err == context.DeadlineExceeded {
		return "TimeoutError"
	}
	if err == context.Canceled {
		return "CancelledError"
	}

	msg := err.Error()

	// Common OS error patterns.
	if strings.Contains(msg, "permission denied") {
		return "Error:EACCES"
	}
	if strings.Contains(msg, "no such file or directory") {
		return "Error:ENOENT"
	}
	if strings.Contains(msg, "file exists") {
		return "Error:EEXIST"
	}
	if strings.Contains(msg, "not a directory") {
		return "Error:ENOTDIR"
	}
	if strings.Contains(msg, "is a directory") {
		return "Error:EISDIR"
	}

	// Truncate to safe length.
	if len(msg) > 60 {
		msg = msg[:60]
	}
	return fmt.Sprintf("Error:%s", msg)
}

// ToolExecutionError wraps an error with tool-specific context.
type ToolExecutionError struct {
	ToolName  string
	ToolUseID string
	Phase     ExecutionPhase
	Err       error
}

func (e *ToolExecutionError) Error() string {
	return fmt.Sprintf("tool %s (id=%s, phase=%s): %v", e.ToolName, e.ToolUseID, e.Phase, e.Err)
}

func (e *ToolExecutionError) Unwrap() error {
	return e.Err
}

// WrapToolError wraps an error with tool execution context.
func WrapToolError(toolName, toolUseID string, phase ExecutionPhase, err error) error {
	if err == nil {
		return nil
	}
	return &ToolExecutionError{
		ToolName:  toolName,
		ToolUseID: toolUseID,
		Phase:     phase,
		Err:       err,
	}
}
