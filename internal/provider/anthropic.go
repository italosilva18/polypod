package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Anthropic provides native access to Claude models via the official SDK.
type Anthropic struct {
	client        anthropic.Client
	tokenSource   TokenSource
	baseURL       string
	enableCaching bool
	thinkingMode  string // "", "enabled", "adaptive"
	thinkBudget   int
}

// NewAnthropic creates an Anthropic provider using the official Go SDK.
func NewAnthropic(apiKey, baseURL string) *Anthropic {
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}

	return &Anthropic{
		client:        anthropic.NewClient(opts...),
		baseURL:       baseURL,
		enableCaching: true,
	}
}

// NewAnthropicWithTokenSource creates an Anthropic provider authenticated with OAuth Bearer tokens.
func NewAnthropicWithTokenSource(tokenSource TokenSource, baseURL string) *Anthropic {
	return &Anthropic{
		tokenSource:   tokenSource,
		baseURL:       baseURL,
		enableCaching: true,
	}
}

func (a *Anthropic) activeClient(ctx context.Context) (anthropic.Client, error) {
	if a.tokenSource == nil {
		return a.client, nil
	}
	token, err := a.tokenSource(ctx)
	if err != nil {
		return anthropic.Client{}, fmt.Errorf("anthropic oauth: %w", err)
	}
	opts := []option.RequestOption{option.WithAuthToken(token)}
	if a.baseURL != "" {
		opts = append(opts, option.WithBaseURL(a.baseURL))
	}
	return anthropic.NewClient(opts...), nil
}

func (a *Anthropic) Name() string                   { return "anthropic" }
func (a *Anthropic) SupportsVision() bool           { return true }
func (a *Anthropic) SupportsTools() bool            { return true }
func (a *Anthropic) SupportsStructuredOutput() bool { return true }

// SetThinking configures extended thinking mode.
func (a *Anthropic) SetThinking(mode string, budget int) {
	a.thinkingMode = mode
	a.thinkBudget = budget
}

func (a *Anthropic) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	params := a.buildParams(req)
	for attempt := 0; attempt < 2; attempt++ {
		client, err := a.activeClient(ctx)
		if err != nil {
			return nil, err
		}
		msg, err := client.Messages.New(ctx, params)
		if err == nil {
			return a.parseResponse(msg), nil
		}
		if attempt == 0 && a.tokenSource != nil && IsRateLimitError(err) {
			if _, rotated := tryRotateAccount("anthropic"); rotated {
				continue
			}
		}
		return nil, fmt.Errorf("anthropic: %w", err)
	}
	return nil, fmt.Errorf("anthropic: falha inesperada")
}

func (a *Anthropic) ChatStream(ctx context.Context, req ChatRequest, callback StreamCallback) (*ChatResponse, error) {
	client, err := a.activeClient(ctx)
	if err != nil {
		return nil, err
	}
	params := a.buildParams(req)

	stream := client.Messages.NewStreaming(ctx, params)
	msg := anthropic.Message{}

	for stream.Next() {
		event := stream.Current()
		msg.Accumulate(event)

		// Extract text deltas for streaming
		switch evt := event.AsAny().(type) {
		case anthropic.ContentBlockDeltaEvent:
			if delta, ok := evt.Delta.AsAny().(anthropic.TextDelta); ok {
				if callback != nil {
					callback(StreamDelta{Content: delta.Text})
				}
			}
		}
	}

	if err := stream.Err(); err != nil {
		return nil, fmt.Errorf("anthropic stream: %w", err)
	}

	if callback != nil {
		callback(StreamDelta{Done: true})
	}

	return a.parseResponse(&msg), nil
}

func (a *Anthropic) buildParams(req ChatRequest) anthropic.MessageNewParams {
	params := anthropic.MessageNewParams{
		Model:     req.Model,
		MaxTokens: int64(req.MaxTokens),
	}
	if params.MaxTokens == 0 {
		params.MaxTokens = 4096
	}

	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			block := anthropic.TextBlockParam{Text: m.Content}
			if a.enableCaching {
				block.CacheControl = anthropic.CacheControlEphemeralParam{Type: "ephemeral"}
			}
			params.System = append(params.System, block)

		case "user":
			var blocks []anthropic.ContentBlockParamUnion
			if len(m.Parts) > 0 {
				for _, p := range m.Parts {
					switch p.Type {
					case "text":
						blocks = append(blocks, anthropic.NewTextBlock(p.Text))
					case "image_base64":
						blocks = append(blocks, anthropic.NewImageBlockBase64(p.MimeType, p.ImageB64))
					case "image_url":
						// URL images: use base64 source with URL
						blocks = append(blocks, anthropic.ContentBlockParamUnion{
							OfImage: &anthropic.ImageBlockParam{
								Source: anthropic.ImageBlockParamSourceUnion{
									OfURL: &anthropic.URLImageSourceParam{URL: p.ImageURL},
								},
							},
						})
					}
				}
			} else {
				blocks = append(blocks, anthropic.NewTextBlock(m.Content))
			}
			params.Messages = append(params.Messages, anthropic.NewUserMessage(blocks...))

		case "assistant":
			if len(m.ToolCalls) > 0 {
				var blocks []anthropic.ContentBlockParamUnion
				if m.Content != "" {
					blocks = append(blocks, anthropic.NewTextBlock(m.Content))
				}
				for _, tc := range m.ToolCalls {
					var input any
					json.Unmarshal([]byte(tc.Arguments), &input)
					blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, input, tc.Name))
				}
				params.Messages = append(params.Messages, anthropic.NewAssistantMessage(blocks...))
			} else {
				params.Messages = append(params.Messages, anthropic.NewAssistantMessage(
					anthropic.NewTextBlock(m.Content),
				))
			}

		case "tool":
			params.Messages = append(params.Messages, anthropic.NewUserMessage(
				anthropic.NewToolResultBlock(m.ToolCallID, m.Content, false),
			))
		}
	}

	// Tools
	for _, t := range req.Tools {
		schemaBytes, _ := json.Marshal(t.Parameters)
		var schema anthropic.ToolInputSchemaParam
		json.Unmarshal(schemaBytes, &schema)

		tool := anthropic.ToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: schema,
		}
		if a.enableCaching {
			tool.CacheControl = anthropic.CacheControlEphemeralParam{Type: "ephemeral"}
		}
		params.Tools = append(params.Tools, anthropic.ToolUnionParam{OfTool: &tool})
	}

	// Extended thinking (per-request budget overrides static provider default)
	mode := a.thinkingMode
	budget := int64(a.thinkBudget)
	if req.ThinkingBudget >= 1024 && mode == "" {
		mLower := strings.ToLower(req.Model)
		if strings.Contains(mLower, "claude-3-7") || strings.Contains(mLower, "claude-4") || strings.Contains(mLower, "sonnet-4") || strings.Contains(mLower, "opus-4") {
			mode = "enabled"
			budget = int64(req.ThinkingBudget)
		}
	}
	switch mode {
	case "enabled":
		if budget == 0 {
			budget = 4096
		}
		if params.MaxTokens <= budget {
			params.MaxTokens = budget + 4096
		}
		params.Thinking = anthropic.ThinkingConfigParamUnion{
			OfEnabled: &anthropic.ThinkingConfigEnabledParam{BudgetTokens: budget},
		}
	case "adaptive":
		params.Thinking = anthropic.ThinkingConfigParamUnion{
			OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{},
		}
	}

	return params
}

func (a *Anthropic) parseResponse(msg *anthropic.Message) *ChatResponse {
	cacheRead := int(msg.Usage.CacheReadInputTokens)
	cacheWrite := int(msg.Usage.CacheCreationInputTokens)
	inputTokens := int(msg.Usage.InputTokens) + cacheRead + cacheWrite
	cr := &ChatResponse{
		FinishReason:     string(msg.StopReason),
		PromptTokens:     inputTokens,
		CompletionTokens: int(msg.Usage.OutputTokens),
		TotalTokens:      inputTokens + int(msg.Usage.OutputTokens),
		CacheReadTokens:  cacheRead,
		CacheWriteTokens: cacheWrite,
	}

	var textParts []string
	for _, block := range msg.Content {
		switch v := block.AsAny().(type) {
		case anthropic.TextBlock:
			textParts = append(textParts, v.Text)
		case anthropic.ToolUseBlock:
			argsJSON, _ := json.Marshal(v.Input)
			cr.ToolCalls = append(cr.ToolCalls, ToolCall{
				ID:        v.ID,
				Name:      v.Name,
				Arguments: string(argsJSON),
			})
		case anthropic.ThinkingBlock:
			textParts = append(textParts, fmt.Sprintf("<thinking>%s</thinking>", v.Thinking))
		}
	}
	cr.Content = strings.Join(textParts, "")

	return cr
}
