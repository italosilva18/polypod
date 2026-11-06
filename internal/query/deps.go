package query

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/costa/polypod/internal/types"
	"github.com/google/uuid"
)

// QueryDeps contains all I/O dependencies for the query loop.
// Passing a QueryDeps override into AdvancedQueryParams lets tests inject
// fakes directly instead of per-module mocking.
//
// Using function types keeps signatures in sync with real implementations.
// Scope is intentionally narrow — follow-up expansions can add more deps
// without breaking existing callers.
// Migrated from Claude Code query/deps.ts.
type QueryDeps struct {
	// CallModel invokes the AI model with streaming.
	// Returns a channel that yields StreamEvent values (content deltas,
	// tool-use blocks, usage, stop reasons).
	CallModel ModelStreamFunc

	// Microcompact applies lightweight compaction (tool-result trimming,
	// duplicate removal) before each API call.
	Microcompact MicrocompactFunc

	// Autocompact performs full context summarization when the token count
	// exceeds the compaction threshold.
	Autocompact AutocompactFunc

	// UUID generates unique identifiers.
	UUID UUIDFunc

	// PersistSession writes the current message history to transcript storage.
	PersistSession PersistFunc

	// Logger for structured logging.
	Logger *slog.Logger
}

// --- Function type definitions ---

// StreamEvent represents an event yielded during model streaming.
type StreamEvent struct {
	// Type discriminates the event kind.
	Type StreamEventType

	// Delta is the incremental text content (for ContentDelta events).
	Delta string

	// ToolCall is populated for ToolUseStart events.
	ToolCall *StreamToolCall

	// Usage is populated for MessageStart and MessageDelta events.
	Usage *StreamUsage

	// StopReason is populated on MessageStop events.
	StopReason string

	// Error is set when the stream encounters a recoverable error.
	Error error

	// AssistantMessage is the full message when a complete assistant block is available.
	AssistantMessage *AssistantMessageContent
}

// StreamEventType enumerates the kinds of stream events.
type StreamEventType int

const (
	// EventContentDelta is an incremental text token.
	EventContentDelta StreamEventType = iota
	// EventToolUseStart signals the beginning of a tool-use block.
	EventToolUseStart
	// EventToolUseEnd signals the end of a tool-use block with accumulated input.
	EventToolUseEnd
	// EventMessageStart signals the start of a new model response.
	EventMessageStart
	// EventMessageDelta carries intermediate usage/stop updates.
	EventMessageDelta
	// EventMessageStop signals the response is complete.
	EventMessageStop
	// EventError carries a recoverable error.
	EventError
	// EventThinking carries a thinking block.
	EventThinking
)

// StreamToolCall represents a tool call from the model.
type StreamToolCall struct {
	ID        string
	Name      string
	Arguments map[string]any
}

// StreamUsage carries token usage information.
type StreamUsage struct {
	InputTokens              int
	OutputTokens             int
	CacheReadInputTokens     int
	CacheCreationInputTokens int
}

// AssistantMessageContent represents a complete assistant message block.
type AssistantMessageContent struct {
	Content    []ContentBlock
	StopReason string
}

// ContentBlock is a single block within an assistant message.
type ContentBlock struct {
	Type  string         // "text", "tool_use", "thinking"
	Text  string         // for text blocks
	ID    string         // for tool_use blocks
	Name  string         // for tool_use blocks
	Input map[string]any // for tool_use blocks
}

// ModelStreamFunc is the signature for streaming model calls.
// The returned channel yields events until closed. The caller must drain it.
// ctx cancellation should terminate the stream.
type ModelStreamFunc func(ctx context.Context, req ModelStreamRequest) (<-chan StreamEvent, error)

// ModelStreamRequest contains all parameters for a streaming model call.
type ModelStreamRequest struct {
	Messages        []Message
	SystemPrompt    string
	Tools           []types.Tool
	Model           string
	MaxOutputTokens int
	Temperature     float32
	ThinkingEnabled bool
	ThinkingBudget  int
	FallbackModel   string
	Signal          <-chan struct{} // abort signal
	TaskBudget      *TaskBudget
}

// MicrocompactFunc applies lightweight compaction to messages.
// Returns the (possibly modified) messages and any compaction metadata.
type MicrocompactFunc func(ctx context.Context, messages []Message, opts MicrocompactOpts) (MicrocompactResult, error)

// MicrocompactOpts configures microcompact behavior.
type MicrocompactOpts struct {
	QuerySource string
	Tools       []types.Tool
}

// MicrocompactResult is the outcome of microcompact processing.
type MicrocompactResult struct {
	Messages      []Message
	TokensRemoved int
	Modified      bool
}

// AutocompactFunc performs full context compaction.
type AutocompactFunc func(ctx context.Context, messages []Message, opts AutocompactOpts) (*AutocompactResult, error)

// AutocompactOpts configures autocompact behavior.
type AutocompactOpts struct {
	SystemPrompt  string
	UserContext   map[string]string
	SystemContext map[string]string
	QuerySource   string
	Tracking      *AutoCompactTracking
}

// AutoCompactTracking carries state across compaction cycles.
type AutoCompactTracking struct {
	Compacted           bool
	TurnID              string
	TurnCounter         int
	ConsecutiveFailures int
}

// AutocompactResult is the outcome of full compaction.
type AutocompactResult struct {
	SummaryMessages       []Message
	PreCompactTokenCount  int
	PostCompactTokenCount int
	CompactionUsage       *StreamUsage
	BoundaryMessage       *Message // the compact_boundary system message
}

// UUIDFunc generates unique identifiers.
type UUIDFunc func() string

// PersistFunc persists the current message history.
type PersistFunc func(ctx context.Context, messages []Message) error

// --- Default (production) implementations ---

// DefaultDeps returns production dependencies.
// In production, the caller provides a real ModelStreamFunc wired to the AI
// client; the other deps have sensible defaults.
func DefaultDeps(logger *slog.Logger) QueryDeps {
	return QueryDeps{
		CallModel:      nil, // Must be provided by caller
		Microcompact:   defaultMicrocompact,
		Autocompact:    defaultAutocompact,
		UUID:           defaultUUID,
		PersistSession: defaultPersist,
		Logger:         logger,
	}
}

// Validate checks that all required deps are set.
func (d *QueryDeps) Validate() error {
	if d.CallModel == nil {
		return fmt.Errorf("QueryDeps: CallModel is required")
	}
	if d.Microcompact == nil {
		d.Microcompact = defaultMicrocompact
	}
	if d.Autocompact == nil {
		d.Autocompact = defaultAutocompact
	}
	if d.UUID == nil {
		d.UUID = defaultUUID
	}
	if d.PersistSession == nil {
		d.PersistSession = defaultPersist
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return nil
}

// --- Default implementations ---

func defaultUUID() string {
	return uuid.New().String()
}

func defaultMicrocompact(_ context.Context, messages []Message, _ MicrocompactOpts) (MicrocompactResult, error) {
	return MicrocompactResult{
		Messages:      messages,
		TokensRemoved: 0,
		Modified:      false,
	}, nil
}

func defaultAutocompact(_ context.Context, _ []Message, _ AutocompactOpts) (*AutocompactResult, error) {
	// No-op: autocompact not triggered
	return nil, nil
}

func defaultPersist(_ context.Context, _ []Message) error {
	return nil
}

// --- Retry helper for deps ---

// RetryConfig controls retry behavior for model calls.
type RetryConfig struct {
	MaxRetries    int
	BaseBackoff   time.Duration
	BackoffFactor float64
	IsRetryable   func(error) bool
}

// DefaultRetryConfig returns sensible retry defaults.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:    3,
		BaseBackoff:   time.Second,
		BackoffFactor: 2.0,
		IsRetryable:   defaultIsRetryable,
	}
}

// RetryWithBackoff executes fn with exponential backoff.
func RetryWithBackoff(ctx context.Context, cfg RetryConfig, fn func() error) error {
	var lastErr error
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err

		if !cfg.IsRetryable(err) {
			return err
		}
		if attempt == cfg.MaxRetries {
			break
		}

		delay := time.Duration(float64(cfg.BaseBackoff) * math.Pow(cfg.BackoffFactor, float64(attempt)))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return fmt.Errorf("exhausted %d retries: %w", cfg.MaxRetries, lastErr)
}

func defaultIsRetryable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// Check for common transient patterns
	retryPatterns := []string{
		"429", "500", "502", "503", "504",
		"timeout", "Timeout",
		"connection refused", "connection reset", "EOF",
		"overloaded", "rate_limit",
	}
	for _, p := range retryPatterns {
		if contains(msg, p) {
			return true
		}
	}
	return false
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
