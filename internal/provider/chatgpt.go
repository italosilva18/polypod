package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

// TokenSource supplies a fresh ChatGPT OAuth access token. The concrete
// implementation refreshes and persists rotating tokens when necessary.
type TokenSource func(context.Context) (string, error)

// ChatGPT uses the official Responses API with a Sign in with ChatGPT token.
// It is only a model backend: all tool execution remains inside Polypod.
type ChatGPT struct {
	tokenSource TokenSource
}

func NewChatGPT(tokenSource TokenSource) *ChatGPT { return &ChatGPT{tokenSource: tokenSource} }

func (p *ChatGPT) Name() string                   { return "chatgpt" }
func (p *ChatGPT) SupportsVision() bool           { return true }
func (p *ChatGPT) SupportsTools() bool            { return true }
func (p *ChatGPT) SupportsStructuredOutput() bool { return false }

func (p *ChatGPT) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// ChatGPT plan sharing requires every Responses request to stream through
	// its terminal response.completed event, even for synchronous Poly callers.
	return p.ChatStream(ctx, req, nil)
}

func (p *ChatGPT) ChatStream(ctx context.Context, req ChatRequest, callback StreamCallback) (*ChatResponse, error) {
	return p.chatStream(ctx, req, callback, nil)
}

// ChatStreamWithCompletedCalls exposes finalized function calls to the AI
// runtime before the response ends. It is deliberately opt-in: a Failover
// provider continues to use ChatStream, so an incomplete provider response
// cannot cause tools to run before its fallback decision.
func (p *ChatGPT) ChatStreamWithCompletedCalls(
	ctx context.Context,
	req ChatRequest,
	callback StreamCallback,
	itemReady func(index int64, call *ToolCall),
) (*ChatResponse, error) {
	return p.chatStream(ctx, req, callback, itemReady)
}

func (p *ChatGPT) chatStream(
	ctx context.Context,
	req ChatRequest,
	callback StreamCallback,
	itemReady func(index int64, call *ToolCall),
) (*ChatResponse, error) {
	emitted := false
	wrappedCallback := callback
	if callback != nil {
		wrappedCallback = func(d StreamDelta) {
			if d.Content != "" || len(d.ToolCalls) > 0 || d.Done {
				emitted = true
			}
			callback(d)
		}
	}
	resp, err := p.chatStreamOnce(ctx, req, wrappedCallback, func(idx int64, call *ToolCall) {
		emitted = true
		if itemReady != nil {
			itemReady(idx, call)
		}
	})
	if err != nil && !emitted && p.tokenSource != nil && IsRateLimitError(err) {
		if _, rotated := tryRotateAccount("chatgpt"); rotated {
			return p.chatStreamOnce(ctx, req, callback, itemReady)
		}
	}
	return resp, err
}

func (p *ChatGPT) chatStreamOnce(
	ctx context.Context,
	req ChatRequest,
	callback StreamCallback,
	itemReady func(index int64, call *ToolCall),
) (*ChatResponse, error) {
	client, params, err := p.request(ctx, req)
	if err != nil {
		return nil, err
	}
	stream := client.Responses.NewStreaming(ctx, params)
	defer stream.Close()

	var final *responses.Response
	var text strings.Builder
	var items []responses.ResponseOutputItemUnion
	type streamedItem struct {
		item  responses.ResponseOutputItemUnion
		index int64
	}
	itemByID := make(map[string]streamedItem)
	for stream.Next() {
		event := stream.Current()
		switch event.Type {
		case "response.output_item.added":
			added := event.AsResponseOutputItemAdded()
			if added.Item.Type == "function_call" {
				itemByID[added.Item.ID] = streamedItem{item: added.Item, index: added.OutputIndex}
			} else if itemReady != nil {
				// Non-function items have no local tool barrier.
				itemReady(added.OutputIndex, nil)
			}
		case "response.function_call_arguments.done":
			if itemReady != nil {
				done := event.AsResponseFunctionCallArgumentsDone()
				tracked, ok := itemByID[done.ItemID]
				item := tracked.item
				name := done.Name
				if name == "" {
					name = item.Name
				}
				if ok && tracked.index == done.OutputIndex && item.CallID != "" && name != "" && (item.Name == "" || item.Name == name) {
					itemReady(done.OutputIndex, &ToolCall{ID: item.CallID, Name: name, Arguments: done.Arguments})
				}
			}
		case "response.output_text.delta":
			text.WriteString(event.Delta)
			if callback != nil {
				callback(StreamDelta{Content: event.Delta})
			}
		case "response.output_item.done":
			completed := event.AsResponseOutputItemDone()
			item := completed.Item
			if item.Type != "" {
				items = append(items, item)
			}
			if itemReady != nil {
				if item.Type == "function_call" && item.CallID != "" && item.Name != "" {
					itemReady(completed.OutputIndex, &ToolCall{ID: item.CallID, Name: item.Name, Arguments: item.Arguments})
				} else if item.Type != "function_call" {
					itemReady(completed.OutputIndex, nil)
				}
			}
		case "response.completed", "response.failed", "response.incomplete":
			if event.Response.ID != "" {
				copy := event.Response
				final = &copy
			}
		}
	}
	if err := stream.Err(); err != nil {
		return nil, fmt.Errorf("ChatGPT Responses stream: %w", err)
	}
	if final == nil {
		return nil, fmt.Errorf("ChatGPT Responses stream terminou sem resposta final: %w", io.ErrUnexpectedEOF)
	}
	if final.Status != responses.ResponseStatusCompleted {
		if final.Error.Code == responses.ResponseErrorCodeServerError || final.Error.Code == responses.ResponseErrorCodeRateLimitExceeded {
			return nil, fmt.Errorf("ChatGPT Responses %s: %w", final.Error.Code, ErrProviderUnavailable)
		}
		if final.Error.Message != "" {
			return nil, fmt.Errorf("ChatGPT Responses %s: %s", final.Status, final.Error.Message)
		}
		return nil, fmt.Errorf("ChatGPT Responses terminou com status %s", final.Status)
	}
	if len(final.Output) == 0 && len(items) > 0 {
		final.Output = items
	}
	parsed := parseChatGPTResponse(final)
	if parsed.Content == "" {
		parsed.Content = text.String()
	}
	if callback != nil {
		callback(StreamDelta{Done: true})
	}
	return parsed, nil
}

func (p *ChatGPT) request(ctx context.Context, req ChatRequest) (*openai.Client, responses.ResponseNewParams, error) {
	token, err := p.tokenSource(ctx)
	if err != nil {
		return nil, responses.ResponseNewParams{}, errors.Join(ErrProviderUnavailable, err)
	}
	client := openai.NewClient(
		option.WithAPIKey(token),
		option.WithBaseURL("https://api.openai.com/v1"),
	)
	input, instructions := translateChatGPTMessages(req.Messages)
	params := responses.ResponseNewParams{
		Model: req.Model,
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: input},
		Store: openai.Opt(false),
	}
	if instructions != "" {
		params.Instructions = openai.Opt(instructions)
	}
	if len(req.Tools) > 0 {
		params.Tools = translateChatGPTTools(req.Tools)
	}
	return &client, params, nil
}

func translateChatGPTMessages(messages []Message) (responses.ResponseInputParam, string) {
	input := make(responses.ResponseInputParam, 0, len(messages))
	var instructions strings.Builder
	for _, msg := range messages {
		switch msg.Role {
		case "system", "developer":
			if instructions.Len() > 0 {
				instructions.WriteString("\n\n")
			}
			instructions.WriteString(msg.Content)
		case "assistant":
			if msg.Content != "" {
				input = append(input, responses.ResponseInputItemUnionParam{OfMessage: &responses.EasyInputMessageParam{
					Role:    responses.EasyInputMessageRoleAssistant,
					Content: responses.EasyInputMessageContentUnionParam{OfString: openai.Opt(msg.Content)},
				}})
			}
			for _, tc := range msg.ToolCalls {
				input = append(input, responses.ResponseInputItemUnionParam{OfFunctionCall: &responses.ResponseFunctionToolCallParam{
					CallID: tc.ID, Name: tc.Name, Arguments: tc.Arguments,
				}})
			}
		case "tool":
			input = append(input, responses.ResponseInputItemUnionParam{OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
				CallID: msg.ToolCallID,
				Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{OfString: openai.Opt(msg.Content)},
			}})
		default:
			input = append(input, responses.ResponseInputItemUnionParam{OfMessage: &responses.EasyInputMessageParam{
				Role:    responses.EasyInputMessageRoleUser,
				Content: chatGPTMessageContent(msg),
			}})
		}
	}
	return input, instructions.String()
}

func translateChatGPTTools(tools []Tool) []responses.ToolUnionParam {
	result := make([]responses.ToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		fn := responses.FunctionToolParam{
			Name: tool.Name, Parameters: normalizeChatGPTToolSchema(tool.Parameters), Strict: openai.Opt(false),
		}
		if tool.Description != "" {
			fn.Description = openai.Opt(tool.Description)
		}
		result = append(result, responses.ToolUnionParam{OfFunction: &fn})
	}
	return result
}

// The Responses API validates `required` as an array and `properties` as an
// object when either field is present. Some legacy tools provide typed nils,
// which otherwise become JSON null. Normalize only JSON Schema object nodes
// and leave the caller's schema untouched for other providers.
func normalizeChatGPTToolSchema(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []interface{}{}}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []interface{}{}}
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(raw, &schema); err != nil || schema == nil {
		return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []interface{}{}}
	}
	if schema["type"] == nil {
		schema["type"] = "object"
	}
	normalizeChatGPTSchemaNode(schema)
	return schema
}

func normalizeChatGPTSchemaNode(value any) {
	switch node := value.(type) {
	case map[string]interface{}:
		if node["type"] == "object" {
			if node["properties"] == nil {
				node["properties"] = map[string]interface{}{}
			}
			if node["required"] == nil {
				node["required"] = []interface{}{}
			}
		}
		for _, child := range node {
			normalizeChatGPTSchemaNode(child)
		}
	case []interface{}:
		for _, child := range node {
			normalizeChatGPTSchemaNode(child)
		}
	}
}

func parseChatGPTResponse(resp *responses.Response) *ChatResponse {
	result := &ChatResponse{FinishReason: "stop"}
	var content strings.Builder
	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" {
					content.WriteString(part.Text)
				} else if part.Type == "refusal" {
					content.WriteString(part.Refusal)
				}
			}
		case "function_call":
			args := item.Arguments
			if !json.Valid([]byte(args)) {
				args = "{}"
			}
			result.ToolCalls = append(result.ToolCalls, ToolCall{ID: item.CallID, Name: item.Name, Arguments: args})
		}
	}
	result.Content = content.String()
	if len(result.ToolCalls) > 0 {
		result.FinishReason = "tool_calls"
	}
	result.PromptTokens = int(resp.Usage.InputTokens)
	result.CompletionTokens = int(resp.Usage.OutputTokens)
	result.TotalTokens = int(resp.Usage.TotalTokens)
	return result
}

func chatGPTMessageContent(msg Message) responses.EasyInputMessageContentUnionParam {
	if len(msg.Parts) == 0 {
		return responses.EasyInputMessageContentUnionParam{OfString: openai.Opt(msg.Content)}
	}
	var parts responses.ResponseInputMessageContentListParam
	if msg.Content != "" {
		parts = append(parts, responses.ResponseInputContentUnionParam{OfInputText: &responses.ResponseInputTextParam{Text: msg.Content}})
	}
	for _, p := range msg.Parts {
		switch p.Type {
		case "text":
			parts = append(parts, responses.ResponseInputContentUnionParam{OfInputText: &responses.ResponseInputTextParam{Text: p.Text}})
		case "image_base64", "image_url":
			url := p.ImageURL
			if p.Type == "image_base64" {
				url = "data:" + p.MimeType + ";base64," + p.ImageB64
			}
			parts = append(parts, responses.ResponseInputContentUnionParam{OfInputImage: &responses.ResponseInputImageParam{ImageURL: openai.Opt(url), Detail: "auto"}})
		}
	}
	return responses.EasyInputMessageContentUnionParam{OfInputItemContentList: parts}
}
