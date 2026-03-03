package ai

import (
	"context"
	"fmt"
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

// Complete sends a chat completion request.
func (c *Client) Complete(ctx context.Context, messages []ChatMessage) (*ChatResponse, error) {
	msgs := make([]openai.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		msgs[i] = openai.ChatCompletionMessage{
			Role:    m.Role,
			Content: m.Content,
		}
	}

	resp, err := c.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:       c.model,
		Messages:    msgs,
		MaxTokens:   c.maxTok,
		Temperature: c.temp,
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

// CompleteWithTools sends a chat completion with tool calling loop.
func (c *Client) CompleteWithTools(ctx context.Context, messages []ChatMessage, logger *slog.Logger) (*ChatResponse, error) {
	// Compute tools dynamically each call so runtime-added skills appear
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
		resp, err := c.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
			Model:       c.model,
			Messages:    msgs,
			MaxTokens:   c.maxTok,
			Temperature: c.temp,
			Tools:       tools,
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

		// Append the assistant message with tool calls
		msgs = append(msgs, choice.Message)

		// Execute each tool call and append results
		for _, tc := range choice.Message.ToolCalls {
			logger.Debug("executing tool",
				"tool", tc.Function.Name,
				"args", tc.Function.Arguments,
				"iter", iter,
			)

			result, err := c.skills.Execute(tc.Function.Name, tc.Function.Arguments)
			if err != nil {
				result = fmt.Sprintf("Erro executando tool: %v", err)
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
