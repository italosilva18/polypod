package types

import "context"

// PermissionBehavior defines the result of a permission check.
type PermissionBehavior string

const (
	PermissionAllow PermissionBehavior = "allow"
	PermissionDeny  PermissionBehavior = "deny"
	PermissionAsk   PermissionBehavior = "ask"
)

// PermissionResult is the outcome of checking permissions for a tool call.
type PermissionResult struct {
	Behavior     PermissionBehavior
	Message      string
	UpdatedInput map[string]any
}

// ValidationResult is the outcome of validating tool input.
type ValidationResult struct {
	Valid   bool
	Message string
}

// ToolProgress is reported during long-running tool execution.
type ToolProgress struct {
	ToolUseID string
	Message   string
	Percent   int // 0-100, -1 for indeterminate
}

// ProgressFunc is called to report progress during tool execution.
type ProgressFunc func(ToolProgress)

// ToolContext provides execution context to tools.
type ToolContext struct {
	Context    context.Context
	SessionID  SessionID
	AgentID    AgentID
	WorkingDir string
	Debug      bool
	Verbose    bool
	AbortChan  <-chan struct{}
	OnProgress ProgressFunc
}

// Tool is the core interface for all callable capabilities.
// Inspired by Claude Code's Tool<Input, Output, Progress> generic type.
type Tool interface {
	// Name returns the unique identifier for this tool.
	Name() string

	// Description returns a human-readable description of what the tool does.
	Description() string

	// InputSchema returns the JSON schema for the tool's input parameters.
	InputSchema() map[string]any

	// Execute runs the tool with the given arguments.
	Execute(ctx ToolContext, args map[string]any) (ToolResult, error)

	// ValidateInput checks if the provided input is valid.
	// Return ValidationResult{Valid: true} to proceed.
	ValidateInput(args map[string]any) ValidationResult

	// CheckPermissions determines if the tool call is allowed in the current context.
	// Called after ValidateInput passes.
	CheckPermissions(args map[string]any, ctx ToolContext) PermissionResult

	// IsConcurrencySafe returns true if multiple instances of this tool
	// can run simultaneously without conflicts.
	IsConcurrencySafe(args map[string]any) bool

	// IsReadOnly returns true if the tool only reads data and makes no changes.
	IsReadOnly(args map[string]any) bool

	// IsDestructive returns true if the tool performs irreversible operations
	// (delete, overwrite, send).
	IsDestructive(args map[string]any) bool

	// IsEnabled returns true if this tool is currently available.
	IsEnabled() bool
}

// ToolResult is the output of a tool execution.
type ToolResult struct {
	// Content is the text result returned to the model.
	Content string

	// IsError indicates the tool execution failed.
	IsError bool

	// NewMessages can inject additional messages into the conversation.
	NewMessages []ChatMessage
}

// ChatMessage represents a message in the conversation.
type ChatMessage struct {
	Role    string
	Content string
}

// ToolDef is a partial tool definition that ToolBuilder fills with defaults.
// Mirrors Claude Code's ToolDef pattern where common methods have safe defaults.
type ToolDef struct {
	NameFn          func() string
	DescriptionFn   func() string
	InputSchemaFn   func() map[string]any
	ExecuteFn       func(ctx ToolContext, args map[string]any) (ToolResult, error)
	ValidateFn      func(args map[string]any) ValidationResult
	CheckPermFn     func(args map[string]any, ctx ToolContext) PermissionResult
	IsConcurrencyFn func(args map[string]any) bool
	IsReadOnlyFn    func(args map[string]any) bool
	IsDestructiveFn func(args map[string]any) bool
	IsEnabledFn     func() bool
}

// builtTool wraps a ToolDef with defaults, implementing Tool.
type builtTool struct {
	def ToolDef
}

// BuildTool creates a complete Tool from a partial ToolDef,
// filling in safe defaults for missing methods.
func BuildTool(def ToolDef) Tool {
	if def.IsEnabledFn == nil {
		def.IsEnabledFn = func() bool { return true }
	}
	if def.IsConcurrencyFn == nil {
		def.IsConcurrencyFn = func(map[string]any) bool { return false }
	}
	if def.IsReadOnlyFn == nil {
		def.IsReadOnlyFn = func(map[string]any) bool { return false }
	}
	if def.IsDestructiveFn == nil {
		def.IsDestructiveFn = func(map[string]any) bool { return false }
	}
	if def.ValidateFn == nil {
		def.ValidateFn = func(map[string]any) ValidationResult {
			return ValidationResult{Valid: true}
		}
	}
	if def.CheckPermFn == nil {
		def.CheckPermFn = func(map[string]any, ToolContext) PermissionResult {
			return PermissionResult{Behavior: PermissionAllow}
		}
	}
	return &builtTool{def: def}
}

func (t *builtTool) Name() string                { return t.def.NameFn() }
func (t *builtTool) Description() string         { return t.def.DescriptionFn() }
func (t *builtTool) InputSchema() map[string]any { return t.def.InputSchemaFn() }
func (t *builtTool) Execute(ctx ToolContext, args map[string]any) (ToolResult, error) {
	return t.def.ExecuteFn(ctx, args)
}
func (t *builtTool) ValidateInput(args map[string]any) ValidationResult {
	return t.def.ValidateFn(args)
}
func (t *builtTool) CheckPermissions(args map[string]any, ctx ToolContext) PermissionResult {
	return t.def.CheckPermFn(args, ctx)
}
func (t *builtTool) IsConcurrencySafe(args map[string]any) bool { return t.def.IsConcurrencyFn(args) }
func (t *builtTool) IsReadOnly(args map[string]any) bool        { return t.def.IsReadOnlyFn(args) }
func (t *builtTool) IsDestructive(args map[string]any) bool     { return t.def.IsDestructiveFn(args) }
func (t *builtTool) IsEnabled() bool                            { return t.def.IsEnabledFn() }
