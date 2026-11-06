// Package sidequery provides a lightweight API-call wrapper for "side queries"
// that run outside the main conversation loop.
//
// Migrated from Claude Code TypeScript:
//
//	src/utils/sideQuery.ts
//
// SideQuery is a CRITICAL component used by:
//   - Permission classifiers (permission_explainer)
//   - Compaction decision logic
//   - The /btw (side question) feature
//   - Session search
//   - Model validation
//
// It wraps the Anthropic SDK in a way that:
//   - Defaults to the small/haiku model for cost efficiency
//   - Retries automatically on transient errors and rate limits
//   - Parses JSON responses into typed structs via SideQueryJSON
//   - Exposes tool calls via SideQueryWithTools
package sidequery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// DefaultModel is the cost-efficient small model used when SideQueryParams.Model
// is empty.  Mirrors the TS default: haiku/small for classifier calls.
const DefaultModel = "claude-haiku-4-5-20251001"

// DefaultMaxTokens is the default output token budget for side queries.
// Mirrors max_tokens=1024 from sideQuery.ts.
const DefaultMaxTokens = 1024

// DefaultMaxRetries is the number of automatic retries on transient errors.
const DefaultMaxRetries = 2

// retryableHTTPCodes contains HTTP status codes that warrant a retry.
var retryableHTTPCodes = map[int]bool{
	http.StatusTooManyRequests:     true, // 429 rate limit
	http.StatusServiceUnavailable:  true, // 503
	http.StatusBadGateway:          true, // 502
	http.StatusGatewayTimeout:      true, // 504
	http.StatusInternalServerError: true, // 500
}

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

// Message is a simple role/content pair for constructing SideQuery conversations.
// For multi-part content use the Content field with a JSON array of blocks;
// for plain text use Text (mutually exclusive, Text takes precedence).
type Message struct {
	Role    string `json:"role"`    // "user" or "assistant"
	Content string `json:"content"` // plain-text or raw JSON array of content blocks
}

// ToolDef describes a callable tool that may be passed to SideQueryWithTools.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// ToolUse represents a single tool-use block returned by the model.
type ToolUse struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// SideQueryParams groups all options for a SideQuery call.
type SideQueryParams struct {
	// SystemPrompt is the system message text.  Optional.
	SystemPrompt string

	// Messages is the conversation history to send.
	Messages []Message

	// Tools is an optional list of tool definitions (enables tool use).
	Tools []ToolDef

	// MaxTokens caps the response.  Defaults to DefaultMaxTokens.
	MaxTokens int

	// ThinkingBudget, when > 0, enables extended thinking with the given token
	// budget.  When < 0 it explicitly disables thinking.
	ThinkingBudget int

	// OutputFormat selects the response shape: "" (default text), "json".
	OutputFormat string

	// StopSequences causes the model to stop when any sequence is emitted.
	StopSequences []string

	// Temperature is the sampling temperature.  0 means use the API default.
	Temperature float64

	// Model overrides the default small model.  Empty → DefaultModel.
	Model string

	// APIKey is the Anthropic API key.  Falls back to ANTHROPIC_API_KEY env var
	// when empty (handled transparently by the SDK).
	APIKey string

	// MaxRetries overrides DefaultMaxRetries.
	MaxRetries int

	// SkipSystemPrefix disables the CLI system-prompt prefix injection.
	// Set to true for internal classifiers that supply their own full prompt.
	SkipSystemPrefix bool
}

// ---------------------------------------------------------------------------
// Core entry points
// ---------------------------------------------------------------------------

// SideQuery makes a lightweight single-turn API call and returns the full
// text content of the model's response.
//
// On rate-limit (429) or transient server errors the call is retried up to
// params.MaxRetries times with exponential back-off.
func SideQuery(ctx context.Context, params SideQueryParams) (string, error) {
	resp, err := doSideQuery(ctx, params)
	if err != nil {
		return "", err
	}
	return extractText(resp), nil
}

// SideQueryJSON makes a SideQuery call and unmarshals the text response into
// result.  The model is expected to return valid JSON; wrap SystemPrompt with
// "respond with JSON only" instructions when using this function.
func SideQueryJSON[T any](ctx context.Context, params SideQueryParams, result *T) error {
	text, err := SideQuery(ctx, params)
	if err != nil {
		return err
	}

	// Strip markdown code fences that the model sometimes wraps JSON in.
	text = stripJSONFence(text)

	if err := json.Unmarshal([]byte(text), result); err != nil {
		return fmt.Errorf("sidequery: failed to unmarshal JSON response: %w (raw: %q)", err, truncate(text, 200))
	}
	return nil
}

// SideQueryWithTools makes a SideQuery call that may include tool-use blocks.
// It returns the text content and a slice of ToolUse results so callers can
// act on tool requests without entering a full tool-calling loop.
func SideQueryWithTools(ctx context.Context, params SideQueryParams) (string, []ToolUse, error) {
	resp, err := doSideQuery(ctx, params)
	if err != nil {
		return "", nil, err
	}

	text := extractText(resp)
	tools := extractToolUses(resp)
	return text, tools, nil
}

// ---------------------------------------------------------------------------
// Internal implementation
// ---------------------------------------------------------------------------

// doSideQuery executes the API call with retry logic.
func doSideQuery(ctx context.Context, params SideQueryParams) (*anthropic.Message, error) {
	model := params.Model
	if model == "" {
		model = DefaultModel
	}

	maxTokens := params.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}

	maxRetries := params.MaxRetries
	if maxRetries <= 0 {
		maxRetries = DefaultMaxRetries
	}

	client := buildClient(params.APIKey)

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential back-off: 1s, 2s, 4s …
			wait := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}

		msgParams := buildMessageParams(model, int64(maxTokens), params)
		resp, err := client.Messages.New(ctx, msgParams)
		if err == nil {
			return resp, nil
		}

		lastErr = err

		// Only retry on specific error types.
		if !isRetryable(err) {
			break
		}
	}

	return nil, fmt.Errorf("sidequery: API call failed after %d attempt(s): %w", maxRetries+1, lastErr)
}

// buildClient constructs an Anthropic SDK client.
// When apiKey is empty the SDK will use the ANTHROPIC_API_KEY environment variable.
func buildClient(apiKey string) *anthropic.Client {
	opts := []option.RequestOption{}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	c := anthropic.NewClient(opts...)
	return &c
}

// buildMessageParams assembles the MessageNewParams from SideQueryParams.
func buildMessageParams(model string, maxTokens int64, p SideQueryParams) anthropic.MessageNewParams {
	params := anthropic.MessageNewParams{
		Model:     model,
		MaxTokens: maxTokens,
	}

	// System blocks
	var sysBlocks []anthropic.TextBlockParam
	if !p.SkipSystemPrefix {
		sysBlocks = append(sysBlocks, anthropic.TextBlockParam{
			Text: "You are a helpful AI assistant.",
		})
	}
	if p.SystemPrompt != "" {
		sysBlocks = append(sysBlocks, anthropic.TextBlockParam{
			Text: p.SystemPrompt,
		})
	}
	params.System = sysBlocks

	// Messages
	for _, m := range p.Messages {
		switch m.Role {
		case "user":
			params.Messages = append(params.Messages,
				anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)),
			)
		case "assistant":
			params.Messages = append(params.Messages,
				anthropic.NewAssistantMessage(anthropic.NewTextBlock(m.Content)),
			)
		}
	}

	// Tools
	if len(p.Tools) > 0 {
		var tools []anthropic.ToolUnionParam
		for _, t := range p.Tools {
			schemaBytes, _ := json.Marshal(t.InputSchema)
			var schema anthropic.ToolInputSchemaParam
			json.Unmarshal(schemaBytes, &schema) //nolint:errcheck
			tools = append(tools, anthropic.ToolUnionParam{
				OfTool: &anthropic.ToolParam{
					Name:        t.Name,
					Description: anthropic.String(t.Description),
					InputSchema: schema,
				},
			})
		}
		params.Tools = tools
	}

	// Temperature
	if p.Temperature > 0 {
		params.Temperature = anthropic.Float(p.Temperature)
	}

	// Stop sequences
	if len(p.StopSequences) > 0 {
		params.StopSequences = p.StopSequences
	}

	// Thinking
	if p.ThinkingBudget < 0 {
		params.Thinking = anthropic.ThinkingConfigParamUnion{
			OfDisabled: &anthropic.ThinkingConfigDisabledParam{Type: "disabled"},
		}
	} else if p.ThinkingBudget > 0 {
		budget := int64(p.ThinkingBudget)
		if budget >= maxTokens {
			budget = maxTokens - 1
		}
		params.Thinking = anthropic.ThinkingConfigParamUnion{
			OfEnabled: &anthropic.ThinkingConfigEnabledParam{
				BudgetTokens: budget,
			},
		}
	}

	return params
}

// extractText collects all text blocks from an API response and joins them.
func extractText(msg *anthropic.Message) string {
	if msg == nil {
		return ""
	}
	var parts []string
	for _, block := range msg.Content {
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok && tb.Text != "" {
			parts = append(parts, tb.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

// extractToolUses collects all tool_use blocks from an API response.
func extractToolUses(msg *anthropic.Message) []ToolUse {
	if msg == nil {
		return nil
	}
	var uses []ToolUse
	for _, block := range msg.Content {
		if tu, ok := block.AsAny().(anthropic.ToolUseBlock); ok {
			var inputMap map[string]any
			// tu.Input is json.RawMessage ([]byte), unmarshal directly.
			if len(tu.Input) > 0 {
				json.Unmarshal(tu.Input, &inputMap) //nolint:errcheck
			}
			uses = append(uses, ToolUse{
				ID:    tu.ID,
				Name:  tu.Name,
				Input: inputMap,
			})
		}
	}
	return uses
}

// isRetryable returns true when an error is a transient condition that warrants
// a retry (rate limit, server error, connection reset).
func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Check for Anthropic SDK API errors with retryable status codes.
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		return retryableHTTPCodes[apiErr.StatusCode]
	}

	// Retry on context-unrelated network errors.
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "connection reset") ||
		strings.Contains(errMsg, "connection refused") ||
		strings.Contains(errMsg, "eof") ||
		strings.Contains(errMsg, "timeout")
}

// stripJSONFence removes ```json ... ``` fences from model output.
func stripJSONFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// Remove first line (```json or ```)
		if idx := strings.Index(s, "\n"); idx != -1 {
			s = s[idx+1:]
		}
		// Remove trailing ```
		if idx := strings.LastIndex(s, "```"); idx != -1 {
			s = s[:idx]
		}
		s = strings.TrimSpace(s)
	}
	return s
}

// truncate shortens s to at most n runes for error messages.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
