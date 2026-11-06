package query

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/costa/polypod/internal/types"
)

// Message represents a conversation message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`

	// Extended fields used by AdvancedQueryEngine.
	// UUID uniquely identifies this message within the session.
	UUID string `json:"uuid,omitempty"`
	// StopReason is set on assistant messages (e.g. "end_turn", "max_tokens", "tool_use").
	StopReason string `json:"stop_reason,omitempty"`
	// IsAPIError marks synthetic assistant messages that represent API errors.
	IsAPIError bool `json:"is_api_error,omitempty"`
	// Timestamp is when the message was created.
	Timestamp time.Time `json:"timestamp,omitempty"`
	// IsMeta marks synthetic messages (e.g. recovery nudges) not authored by the user.
	IsMeta bool `json:"is_meta,omitempty"`
}

// QueryParams configures a query execution.
type QueryParams struct {
	Messages     []Message
	MaxTurns     int
	Timeout      time.Duration
	SystemPrompt string
	Tools        []string // tool names to enable
}

// QueryResult is the outcome of a query execution.
type QueryResult struct {
	Messages         []Message
	TotalTokens      int
	PromptTokens     int
	CompletionTokens int
	Turns            int
	ToolCalls        []ToolCallRecord
	Error            error
}

// ToolCallRecord records a tool call during query execution.
type ToolCallRecord struct {
	Name     string
	Args     map[string]any
	Result   string
	Duration time.Duration
	IsError  bool
}

// ToolExecutor is the interface for executing tools.
type ToolExecutor interface {
	Execute(name string, args map[string]any, ctx types.ToolContext) (types.ToolResult, error)
}

// ModelCaller is the interface for calling the AI model.
type ModelCaller interface {
	Call(ctx context.Context, messages []Message, tools []string) (*ModelResponse, error)
}

// ModelResponse is the response from the AI model.
type ModelResponse struct {
	Content    string
	ToolCalls  []ModelToolCall
	Usage      TokenUsage
	StopReason string // "end_turn", "tool_use", "max_tokens"
}

// ModelToolCall represents a tool call from the model.
type ModelToolCall struct {
	ID        string
	Name      string
	Arguments map[string]any
}

// TokenUsage tracks token consumption.
type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// QueryEngine manages the query lifecycle and conversation state.
// Migrated from Claude Code's QueryEngine.ts
type QueryEngine struct {
	messages    []Message
	sessionID   types.SessionID
	model       ModelCaller
	tools       ToolExecutor
	toolNames   []string
	logger      *slog.Logger
	totalTokens int
	turns       int
	maxTurns    int
}

// NewQueryEngine creates a new query engine.
func NewQueryEngine(
	sessionID types.SessionID,
	model ModelCaller,
	tools ToolExecutor,
	toolNames []string,
	logger *slog.Logger,
) *QueryEngine {
	return &QueryEngine{
		messages:  make([]Message, 0),
		sessionID: sessionID,
		model:     model,
		tools:     tools,
		toolNames: toolNames,
		logger:    logger,
		maxTurns:  20,
	}
}

// Submit processes a user message through the query loop.
func (qe *QueryEngine) Submit(ctx context.Context, userMessage string, params QueryParams) (*QueryResult, error) {
	// Add system prompt if provided
	if params.SystemPrompt != "" && len(qe.messages) == 0 {
		qe.messages = append(qe.messages, Message{Role: "system", Content: params.SystemPrompt})
	}

	// Add user message
	qe.messages = append(qe.messages, Message{Role: "user", Content: userMessage})

	// Override settings from params
	maxTurns := qe.maxTurns
	if params.MaxTurns > 0 {
		maxTurns = params.MaxTurns
	}

	var toolCalls []ToolCallRecord
	promptTokens := 0
	completionTokens := 0

	// Main query loop
	for iteration := 0; iteration < maxTurns; iteration++ {
		qe.logger.Debug("query iteration", "turn", iteration, "messages", len(qe.messages))

		// Call the model
		tools := qe.toolNames
		if len(params.Tools) > 0 {
			tools = params.Tools
		}

		resp, err := qe.model.Call(ctx, qe.messages, tools)
		if err != nil {
			return &QueryResult{
				Messages:         qe.messages,
				TotalTokens:      qe.totalTokens,
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				Turns:            iteration,
				ToolCalls:        toolCalls,
				Error:            err,
			}, fmt.Errorf("model call failed: %w", err)
		}

		// Track tokens
		promptTokens += resp.Usage.PromptTokens
		completionTokens += resp.Usage.CompletionTokens
		qe.totalTokens += resp.Usage.TotalTokens

		// Add assistant response
		qe.messages = append(qe.messages, Message{Role: "assistant", Content: resp.Content})

		// If no tool calls, we're done
		if resp.StopReason == "end_turn" || len(resp.ToolCalls) == 0 {
			qe.turns = iteration + 1
			return &QueryResult{
				Messages:         qe.messages,
				TotalTokens:      qe.totalTokens,
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				Turns:            iteration + 1,
				ToolCalls:        toolCalls,
			}, nil
		}

		// Execute tool calls
		tCtx := types.ToolContext{
			Context:    ctx,
			SessionID:  qe.sessionID,
			WorkingDir: ".",
		}

		for _, tc := range resp.ToolCalls {
			start := time.Now()
			result, execErr := qe.tools.Execute(tc.Name, tc.Arguments, tCtx)
			duration := time.Since(start)

			record := ToolCallRecord{
				Name:     tc.Name,
				Args:     tc.Arguments,
				Result:   result.Content,
				Duration: duration,
				IsError:  result.IsError || execErr != nil,
			}
			toolCalls = append(toolCalls, record)

			// Add tool result to messages
			role := "tool"
			content := result.Content
			if execErr != nil {
				content = fmt.Sprintf("Erro: %v", execErr)
			}
			qe.messages = append(qe.messages, Message{
				Role:    role,
				Content: content,
			})
		}

		qe.turns = iteration + 1
	}

	return &QueryResult{
		Messages:         qe.messages,
		TotalTokens:      qe.totalTokens,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		Turns:            qe.turns,
		ToolCalls:        toolCalls,
		Error:            fmt.Errorf("maximo de iteracoes atingido (%d)", maxTurns),
	}, nil
}

// GetMessages returns the current conversation messages.
func (qe *QueryEngine) GetMessages() []Message {
	result := make([]Message, len(qe.messages))
	copy(result, qe.messages)
	return result
}

// Clear resets the conversation.
func (qe *QueryEngine) Clear() {
	qe.messages = qe.messages[:0]
	qe.totalTokens = 0
	qe.turns = 0
}

// SetModel changes the model caller.
func (qe *QueryEngine) SetModel(model ModelCaller) {
	qe.model = model
}

// SetTools changes the available tools.
func (qe *QueryEngine) SetTools(tools ToolExecutor, names []string) {
	qe.tools = tools
	qe.toolNames = names
}

// Stats returns query statistics.
func (qe *QueryEngine) Stats() map[string]any {
	return map[string]any{
		"session_id":   qe.sessionID.String(),
		"messages":     len(qe.messages),
		"total_tokens": qe.totalTokens,
		"turns":        qe.turns,
	}
}
