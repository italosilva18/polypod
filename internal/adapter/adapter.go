package adapter

import (
	"context"
	"time"
)

// MessageHandler processes an incoming message and returns a response.
type MessageHandler func(ctx context.Context, msg InMessage) (OutMessage, error)

// ToolEventType represents the lifecycle stage of a tool execution in the
// adapter layer. It mirrors ai.ToolEventType so adapters don't import ai.
type ToolEventType int

const (
	ToolEventStart    ToolEventType = iota // Tool execution started
	ToolEventProgress                      // Tool execution in progress
	ToolEventComplete                      // Tool execution finished
	ToolEventError                         // Tool execution errored
)

// ToolEventInfo carries information about a tool call event through the stream.
type ToolEventInfo struct {
	Type     ToolEventType
	ToolName string
	ToolID   string
	Args     map[string]any
	Result   string
	Error    string
	Elapsed  time.Duration
}

// StreamChunk represents a single piece of a streamed response.
type StreamChunk struct {
	Delta string
	Done  bool
	Error error
	// SessionRevision is the manifest revision durably saved with a CLI turn.
	SessionRevision string
	ToolEvent       *ToolEventInfo // non-nil when this chunk carries a tool event
}

// StreamHandler processes an incoming message and streams the response via chunks channel.
type StreamHandler func(ctx context.Context, msg InMessage, chunks chan<- StreamChunk)

// Channel is the interface every adapter (Telegram, WhatsApp, REST) must implement.
type Channel interface {
	// Name returns the channel identifier (e.g. "telegram", "whatsapp", "rest").
	Name() string

	// Start begins listening for messages. It calls handler for each incoming message.
	// It blocks until ctx is cancelled.
	Start(ctx context.Context, handler MessageHandler) error

	// Send delivers a message through this channel.
	Send(ctx context.Context, msg OutMessage) error
}
