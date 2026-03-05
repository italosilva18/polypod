package ai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/costa/polypod/internal/config"
	openai "github.com/sashabaranov/go-openai"
)

// Client wraps the OpenAI-compatible API client for DeepSeek.
type Client struct {
	client     *openai.Client
	model      string
	maxTok     int
	temp       float32
	toolsOn    bool
	skills     SkillRegistry
	skillNames []string
}

// NewClient creates a DeepSeek client using OpenAI-compatible API.
func NewClient(cfg config.AIConfig, skills SkillRegistry) *Client {
	ocfg := openai.DefaultConfig(cfg.APIKey)
	ocfg.BaseURL = cfg.BaseURL
	return &Client{
		client:  openai.NewClientWithConfig(ocfg),
		model:   cfg.Model,
		maxTok:  cfg.MaxToks,
		temp:    cfg.Temp,
		toolsOn: cfg.Tools,
		skills:  skills,
	}
}

// SetSkillNames sets the active skill names for tool definitions.
func (c *Client) SetSkillNames(names []string) {
	c.skillNames = names
}

// ChatMessage mirrors openai.ChatCompletionMessage.
type ChatMessage struct {
	Role    string
	Content string
}

// ChatResponse is the result of a chat completion.
type ChatResponse struct {
	Content          string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// StreamCallback is called for each token delta during streaming.
type StreamCallback func(delta string)

// Complete sends a chat completion request with retry.
func (c *Client) Complete(ctx context.Context, messages []ChatMessage) (*ChatResponse, error) {
	msgs := make([]openai.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		msgs[i] = openai.ChatCompletionMessage{
			Role:    m.Role,
			Content: m.Content,
		}
	}

	resp, err := retry(ctx, func() (openai.ChatCompletionResponse, error) {
		return c.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
			Model:       c.model,
			Messages:    msgs,
			MaxTokens:   c.maxTok,
			Temperature: c.temp,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("chat completion: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	return &ChatResponse{
		Content:          resp.Choices[0].Message.Content,
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	}, nil
}

// CompleteWithTools sends a chat completion with tool calling loop and retry.
func (c *Client) CompleteWithTools(ctx context.Context, messages []ChatMessage, logger *slog.Logger) (*ChatResponse, error) {
	var tools []openai.Tool
	if c.toolsOn && c.skills != nil {
		tools = c.skills.ToolDefinitions(c.skillNames)
	}
	if !c.toolsOn || len(tools) == 0 {
		return c.Complete(ctx, messages)
	}

	msgs := make([]openai.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		msgs[i] = openai.ChatCompletionMessage{
			Role:    m.Role,
			Content: m.Content,
		}
	}

	var totalPrompt, totalCompletion int

	for iter := 0; iter < maxToolIter; iter++ {
		resp, err := retry(ctx, func() (openai.ChatCompletionResponse, error) {
			return c.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
				Model:       c.model,
				Messages:    msgs,
				MaxTokens:   c.maxTok,
				Temperature: c.temp,
				Tools:       tools,
			})
		})
		if err != nil {
			return nil, fmt.Errorf("chat completion (iter %d): %w", iter, err)
		}
		if len(resp.Choices) == 0 {
			return nil, fmt.Errorf("no choices in response (iter %d)", iter)
		}

		totalPrompt += resp.Usage.PromptTokens
		totalCompletion += resp.Usage.CompletionTokens

		choice := resp.Choices[0]

		if choice.FinishReason != openai.FinishReasonToolCalls || len(choice.Message.ToolCalls) == 0 {
			return &ChatResponse{
				Content:          choice.Message.Content,
				PromptTokens:     totalPrompt,
				CompletionTokens: totalCompletion,
				TotalTokens:      totalPrompt + totalCompletion,
			}, nil
		}

		msgs = append(msgs, choice.Message)

		for _, tc := range choice.Message.ToolCalls {
			logger.Debug("executing tool",
				"tool", tc.Function.Name,
				"args", tc.Function.Arguments,
				"iter", iter,
			)

			result, execErr := c.skills.Execute(tc.Function.Name, tc.Function.Arguments)
			if execErr != nil {
				result = fmt.Sprintf("Erro executando tool: %v", execErr)
			}

			msgs = append(msgs, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    result,
				Name:       tc.Function.Name,
				ToolCallID: tc.ID,
			})
		}
	}

	return nil, fmt.Errorf("tool calling exceeded max iterations (%d)", maxToolIter)
}

// CompleteWithToolsStream sends a streaming chat completion with tool calling loop.
// It calls callback for each content delta token, and handles tool calls between stream cycles.
func (c *Client) CompleteWithToolsStream(ctx context.Context, messages []ChatMessage, callback StreamCallback, logger *slog.Logger) (*ChatResponse, error) {
	var tools []openai.Tool
	if c.toolsOn && c.skills != nil {
		tools = c.skills.ToolDefinitions(c.skillNames)
	}

	msgs := make([]openai.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		msgs[i] = openai.ChatCompletionMessage{
			Role:    m.Role,
			Content: m.Content,
		}
	}

	var totalPrompt, totalCompletion int

	for iter := 0; iter < maxToolIter; iter++ {
		req := openai.ChatCompletionRequest{
			Model:       c.model,
			Messages:    msgs,
			MaxTokens:   c.maxTok,
			Temperature: c.temp,
			Stream:      true,
		}
		if len(tools) > 0 {
			req.Tools = tools
		}

		stream, err := retry(ctx, func() (*openai.ChatCompletionStream, error) {
			return c.client.CreateChatCompletionStream(ctx, req)
		})
		if err != nil {
			return nil, fmt.Errorf("stream creation (iter %d): %w", iter, err)
		}

		var contentBuf string
		// Accumulate tool calls by index during streaming
		toolCallMap := make(map[int]*openai.ToolCall)

		for {
			chunk, recvErr := stream.Recv()
			if errors.Is(recvErr, io.EOF) {
				break
			}
			if recvErr != nil {
				stream.Close()
				return nil, fmt.Errorf("stream recv (iter %d): %w", iter, recvErr)
			}

			if len(chunk.Choices) == 0 {
				continue
			}

			delta := chunk.Choices[0].Delta

			// Accumulate content and call callback
			if delta.Content != "" {
				contentBuf += delta.Content
				if callback != nil {
					callback(delta.Content)
				}
			}

			// Accumulate tool calls by index
			for _, tc := range delta.ToolCalls {
				idx := 0
				if tc.Index != nil {
					idx = *tc.Index
				}
				existing, ok := toolCallMap[idx]
				if !ok {
					toolCallMap[idx] = &openai.ToolCall{
						ID:   tc.ID,
						Type: tc.Type,
						Function: openai.FunctionCall{
							Name:      tc.Function.Name,
							Arguments: tc.Function.Arguments,
						},
					}
				} else {
					if tc.ID != "" {
						existing.ID = tc.ID
					}
					if tc.Function.Name != "" {
						existing.Function.Name += tc.Function.Name
					}
					existing.Function.Arguments += tc.Function.Arguments
				}
			}
		}
		stream.Close()

		// If no tool calls, we're done
		if len(toolCallMap) == 0 {
			return &ChatResponse{
				Content:          contentBuf,
				PromptTokens:     totalPrompt,
				CompletionTokens: totalCompletion,
				TotalTokens:      totalPrompt + totalCompletion,
			}, nil
		}

		// Convert tool call map to sorted slice and build assistant message
		var toolCalls []openai.ToolCall
		for i := 0; i < len(toolCallMap); i++ {
			if tc, ok := toolCallMap[i]; ok {
				toolCalls = append(toolCalls, *tc)
			}
		}

		assistantMsg := openai.ChatCompletionMessage{
			Role:      openai.ChatMessageRoleAssistant,
			Content:   contentBuf,
			ToolCalls: toolCalls,
		}
		msgs = append(msgs, assistantMsg)

		// Execute tool calls
		for _, tc := range toolCalls {
			logger.Debug("executing tool (stream)",
				"tool", tc.Function.Name,
				"args", tc.Function.Arguments,
				"iter", iter,
			)

			result, execErr := c.skills.Execute(tc.Function.Name, tc.Function.Arguments)
			if execErr != nil {
				result = fmt.Sprintf("Erro executando tool: %v", execErr)
			}

			msgs = append(msgs, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    result,
				Name:       tc.Function.Name,
				ToolCallID: tc.ID,
			})
		}

		// Notify callback that tool execution happened (optional separator)
		if callback != nil {
			callback("\n")
		}
	}

	return nil, fmt.Errorf("tool calling exceeded max iterations (%d)", maxToolIter)
}
