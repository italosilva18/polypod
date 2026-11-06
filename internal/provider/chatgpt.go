package provider

import (
	"context"
	"encoding/json"
	"fmt"
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
	client, params, err := p.request(ctx, req)
	if err != nil {
		return nil, err
	}
	stream := client.Responses.NewStreaming(ctx, params)
	defer stream.Close()

	var final *responses.Response
	var text strings.Builder
	var items []responses.ResponseOutputItemUnion
	for stream.Next() {
		event := stream.Current()
		switch event.Type {
		case "response.output_text.delta":
			text.WriteString(event.Delta)
			if callback != nil {
				callback(StreamDelta{Content: event.Delta})
			}
		case "response.output_item.done":
			item := event.AsResponseOutputItemDone().Item
			if item.Type != "" {
				items = append(items, item)
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
		return nil, fmt.Errorf("ChatGPT Responses stream terminou sem resposta final")
	}
	if final.Status != responses.ResponseStatusCompleted {
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
		return nil, responses.ResponseNewParams{}, err
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
				Content: responses.EasyInputMessageContentUnionParam{OfString: openai.Opt(msg.Content)},
			}})
		}
	}
	return input, instructions.String()
}

func translateChatGPTTools(tools []Tool) []responses.ToolUnionParam {
	result := make([]responses.ToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		fn := responses.FunctionToolParam{
			Name: tool.Name, Parameters: tool.Parameters, Strict: openai.Opt(false),
		}
		if tool.Description != "" {
			fn.Description = openai.Opt(tool.Description)
		}
		result = append(result, responses.ToolUnionParam{OfFunction: &fn})
	}
	return result
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
