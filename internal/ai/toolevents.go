package ai

import "time"

// ToolEventType represents the lifecycle stage of a tool execution.
type ToolEventType int

const (
	// ToolEventStart indicates a tool execution has started.
	ToolEventStart ToolEventType = iota
	// ToolEventProgress indicates a tool execution is in progress.
	ToolEventProgress
	// ToolEventComplete indicates a tool execution finished successfully.
	ToolEventComplete
	// ToolEventError indicates a tool execution errored.
	ToolEventError
)

// ToolEvent carries information about a tool call lifecycle event.
type ToolEvent struct {
	Type       ToolEventType
	ToolName   string
	ToolID     string
	Args       map[string]any
	Result     string
	Error      string
	Elapsed    time.Duration
	IsReadOnly bool
}

// ToolEventCallback is called for each tool lifecycle event during streaming.
type ToolEventCallback func(event ToolEvent)
