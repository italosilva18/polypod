package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// OpenAICompat works with any OpenAI-compatible API (DeepSeek, Groq, Together, etc.).
type OpenAICompat struct {
	name   string
	client *openai.Client
}

// NewOpenAICompat creates a provider for an OpenAI-compatible endpoint.
func NewOpenAICompat(name, apiKey, baseURL string) *OpenAICompat {
	cfg := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}
	return &OpenAICompat{name: name, client: openai.NewClientWithConfig(cfg)}
}

func (o *OpenAICompat) Name() string                  { return o.name }
func (o *OpenAICompat) SupportsVision() bool           { return true }
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

	var resp openai.ChatCompletionResponse
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err = o.client.CreateChatCompletion(ctx, oReq)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		time.Sleep(time.Duration(math.Pow(2, float64(attempt))) * time.Second)
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

	stream, err := o.client.CreateChatCompletionStream(ctx, oReq)
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
