package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

func supportsOpenAIReasoningEffort(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	for _, prefix := range []string{"o1", "o3", "o4", "gpt-5", "gpt-6", "openai/o1", "openai/o3", "openai/o4", "openai/gpt-5"} {
		if strings.HasPrefix(m, prefix) {
			return true
		}
	}
	return strings.Contains(m, "reasoner") || strings.Contains(m, "qwq") || strings.Contains(m, "deepseek-r1")
}

func normalizeOpenAIEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high", "max":
		return "high"
	default:
		return ""
	}
}

// OpenAICompat works with any OpenAI-compatible API (DeepSeek, Groq, Together, OpenRouter, Qwen, GitHub, etc.).
type OpenAICompat struct {
	name        string
	baseURL     string
	tokenSource TokenSource
	client      *openai.Client
}

// NewOpenAICompat creates a provider for an OpenAI-compatible endpoint.
func NewOpenAICompat(name, apiKey, baseURL string) *OpenAICompat {
	cfg := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}
	return &OpenAICompat{name: name, baseURL: baseURL, client: openai.NewClientWithConfig(cfg)}
}

// NewOpenAICompatWithTokenSource creates an OpenAI-compatible provider backed by a rotating OAuth TokenSource.
func NewOpenAICompatWithTokenSource(name string, tokenSource TokenSource, baseURL string) *OpenAICompat {
	return &OpenAICompat{name: name, baseURL: baseURL, tokenSource: tokenSource}
}

func (o *OpenAICompat) resolveClient(ctx context.Context) (*openai.Client, error) {
	if o.tokenSource == nil {
		return o.client, nil
	}
	token, err := o.tokenSource(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s oauth: %w", o.name, err)
	}
	cfg := openai.DefaultConfig(token)
	if o.baseURL != "" {
		cfg.BaseURL = o.baseURL
	}
	return openai.NewClientWithConfig(cfg), nil
}

func (o *OpenAICompat) Name() string                   { return o.name }
func (o *OpenAICompat) SupportsVision() bool           { return o.name != "deepseek" }
func (o *OpenAICompat) SupportsTools() bool            { return true }
func (o *OpenAICompat) SupportsStructuredOutput() bool { return true }

func (o *OpenAICompat) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	msgs := convertMessages(req.Messages)
	tools := convertTools(req.Tools)

	oReq := openai.ChatCompletionRequest{
		Model:       req.Model,
		Messages:    msgs,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}
	if len(tools) > 0 {
		oReq.Tools = tools
	}
	if req.ResponseFormat != nil {
		oReq.ResponseFormat = convertResponseFormat(req.ResponseFormat)
	}
	if len(req.Stop) > 0 {
		oReq.Stop = req.Stop
	}
	if eff := normalizeOpenAIEffort(req.ReasoningEffort); eff != "" && supportsOpenAIReasoningEffort(req.Model) {
		oReq.ReasoningEffort = eff
	}

	client, err := o.resolveClient(ctx)
	if err != nil {
		return nil, err
	}

	var resp openai.ChatCompletionResponse
	for attempt := 0; attempt < 3; attempt++ {
		resp, err = client.CreateChatCompletion(ctx, oReq)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if o.tokenSource != nil && IsRateLimitError(err) {
			if _, rotated := tryRotateAccount(o.name); rotated {
				if nextClient, rotErr := o.resolveClient(ctx); rotErr == nil {
					client = nextClient
					continue
				}
			}
		}
		if !IsFailoverError(err) || attempt == 2 {
			break
		}
		timer := time.NewTimer(time.Duration(math.Pow(2, float64(attempt))) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if err != nil {
		return nil, fmt.Errorf("openai chat: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai: nenhuma escolha na resposta")
	}

	choice := resp.Choices[0]
	return &ChatResponse{
		Content:          choice.Message.Content,
		ToolCalls:        extractToolCalls(choice.Message.ToolCalls),
		FinishReason:     string(choice.FinishReason),
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	}, nil
}

func (o *OpenAICompat) ChatStream(ctx context.Context, req ChatRequest, callback StreamCallback) (*ChatResponse, error) {
	msgs := convertMessages(req.Messages)
	tools := convertTools(req.Tools)

	oReq := openai.ChatCompletionRequest{
		Model:       req.Model,
		Messages:    msgs,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Stream:      true,
	}
	if len(tools) > 0 {
		oReq.Tools = tools
	}
	if len(req.Stop) > 0 {
		oReq.Stop = req.Stop
	}
	if req.ResponseFormat != nil {
		oReq.ResponseFormat = convertResponseFormat(req.ResponseFormat)
	}
	if eff := normalizeOpenAIEffort(req.ReasoningEffort); eff != "" && supportsOpenAIReasoningEffort(req.Model) {
		oReq.ReasoningEffort = eff
	}

	client, err := o.resolveClient(ctx)
	if err != nil {
		return nil, err
	}

	stream, err := client.CreateChatCompletionStream(ctx, oReq)
	if err != nil && o.tokenSource != nil && IsRateLimitError(err) {
		if _, rotated := tryRotateAccount(o.name); rotated {
			if nextClient, rotErr := o.resolveClient(ctx); rotErr == nil {
				stream, err = nextClient.CreateChatCompletionStream(ctx, oReq)
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("openai stream: %w", err)
	}
	defer stream.Close()

	var content string
	toolMap := make(map[int]*ToolCall)

	for {
		chunk, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			return nil, fmt.Errorf("openai stream recv: %w", recvErr)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		if delta.Content != "" {
			content += delta.Content
			if callback != nil {
				callback(StreamDelta{Content: delta.Content})
			}
		}
		for _, tc := range delta.ToolCalls {
			idx := 0
			if tc.Index != nil {
				idx = *tc.Index
			}
			if existing, ok := toolMap[idx]; !ok {
				toolMap[idx] = &ToolCall{
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				}
			} else {
				if tc.ID != "" {
					existing.ID = tc.ID
				}
				existing.Name += tc.Function.Name
				existing.Arguments += tc.Function.Arguments
			}
		}
	}

	var tcs []ToolCall
	for i := 0; i < len(toolMap); i++ {
		if tc, ok := toolMap[i]; ok {
			tcs = append(tcs, *tc)
		}
	}

	if callback != nil {
		callback(StreamDelta{Done: true})
	}

	return &ChatResponse{
		Content:   content,
		ToolCalls: tcs,
	}, nil
}

// --- helpers ---

func convertMessages(msgs []Message) []openai.ChatCompletionMessage {
	out := make([]openai.ChatCompletionMessage, 0, len(msgs))
	for _, m := range msgs {
		om := openai.ChatCompletionMessage{
			Role:       m.Role,
			Content:    m.Content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		// Multimodal content parts
		if len(m.Parts) > 0 {
			var parts []openai.ChatMessagePart
			for _, p := range m.Parts {
				switch p.Type {
				case "text":
					parts = append(parts, openai.ChatMessagePart{
						Type: openai.ChatMessagePartTypeText,
						Text: p.Text,
					})
				case "image_url":
					parts = append(parts, openai.ChatMessagePart{
						Type:     openai.ChatMessagePartTypeImageURL,
						ImageURL: &openai.ChatMessageImageURL{URL: p.ImageURL},
					})
				case "image_base64":
					dataURL := fmt.Sprintf("data:%s;base64,%s", p.MimeType, p.ImageB64)
					parts = append(parts, openai.ChatMessagePart{
						Type:     openai.ChatMessagePartTypeImageURL,
						ImageURL: &openai.ChatMessageImageURL{URL: dataURL},
					})
				}
			}
			om.MultiContent = parts
			om.Content = ""
		}
		// Tool calls
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				om.ToolCalls = append(om.ToolCalls, openai.ToolCall{
					ID:   tc.ID,
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      tc.Name,
						Arguments: tc.Arguments,
					},
				})
			}
		}
		out = append(out, om)
	}
	return out
}

func convertTools(tools []Tool) []openai.Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]openai.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	return out
}

func convertResponseFormat(rf *ResponseFormat) *openai.ChatCompletionResponseFormat {
	if rf == nil {
		return nil
	}
	switch rf.Type {
	case "json_schema":
		schema, err := json.Marshal(rf.Schema)
		if err != nil {
			return nil
		}
		return &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONSchema, JSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{Name: "response", Schema: json.RawMessage(schema), Strict: true}}
	case "json_object":
		return &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject}
	default:
		return nil
	}
}

func extractToolCalls(tcs []openai.ToolCall) []ToolCall {
	if len(tcs) == 0 {
		return nil
	}
	out := make([]ToolCall, 0, len(tcs))
	for _, tc := range tcs {
		out = append(out, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return out
}
