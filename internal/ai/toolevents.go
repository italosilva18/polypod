package ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

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
	Type         ToolEventType
	ToolName     string
	ToolID       string
	Args         map[string]any
	Result       string
	Error        string
	ResultBytes  int
	ResultSHA256 string
	Elapsed      time.Duration
	IsReadOnly   bool
}

// ToolEventCallback is called for each tool lifecycle event during streaming.
type ToolEventCallback func(event ToolEvent)

func toolResultMetadata(content string) (int, string) {
	if content == "" {
		return 0, ""
	}
	digest := sha256.Sum256([]byte(content))
	return len(content), hex.EncodeToString(digest[:])
}

type toolEventCallbackKey struct{}

// WithToolEventCallback attaches a callback to one model request. Concurrent
// channels can observe their own tool events without changing shared client
// state. The older SetToolEventCallback remains a fallback for other callers.
func WithToolEventCallback(ctx context.Context, callback ToolEventCallback) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, toolEventCallbackKey{}, callback)
}
