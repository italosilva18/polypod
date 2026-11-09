package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/costa/polypod/internal/chatgptauth"
	"github.com/costa/polypod/internal/config"
	providerapi "github.com/costa/polypod/internal/provider"
	"github.com/costa/polypod/internal/tracking"
	openai "github.com/sashabaranov/go-openai"
)

// Client wraps the OpenAI-compatible API client for DeepSeek.
type Client struct {
	client           *openai.Client
	responseProvider providerapi.Provider
	textOnly         bool
	model            string
	maxTok           int
	maxToolIter      int
	temp             float32
	toolsOn          bool
	skills           SkillRegistry
	skillNamesMu     sync.RWMutex
	skillNames       []string
	costEngine       *tracking.CostEngine
	toolEventCB      ToolEventCallback
	streamingToolMgr *StreamingToolManager
}

// SetCostEngine attaches a CostEngine for automatic cost tracking on every
// API call made through this client.
func (c *Client) SetCostEngine(ce *tracking.CostEngine) {
	c.costEngine = ce
}

func (c *Client) recordProviderUsage(resp *providerapi.ChatResponse) {
	if c.costEngine == nil || resp == nil {
		return
	}
	model := resp.Model
	if model == "" {
		model = c.model
	}
	c.costEngine.RecordCompletion(model, resp.PromptTokens, resp.CompletionTokens, resp.CacheReadTokens, resp.CacheWriteTokens)
}

// SetToolEventCallback sets a callback that is invoked for each tool
// lifecycle event (start, complete, error) during streaming tool execution.
func (c *Client) SetToolEventCallback(cb ToolEventCallback) {
	c.toolEventCB = cb
}

// SetStreamingToolManager attaches a StreamingToolManager that enables
// intelligent parallel execution of tool calls during streaming. When set,
// read-only tools are executed concurrently while write tools run serially.
func (c *Client) SetStreamingToolManager(stm *StreamingToolManager) {
	c.streamingToolMgr = stm
}

// NewClient creates a DeepSeek client using OpenAI-compatible API.
func NewClient(cfg config.AIConfig, skills SkillRegistry) *Client {
	ocfg := openai.DefaultConfig(cfg.APIKey)
	ocfg.BaseURL = cfg.BaseURL
	c := &Client{
		client:      openai.NewClientWithConfig(ocfg),
		model:       cfg.Model,
		maxTok:      cfg.MaxToks,
		maxToolIter: cfg.ToolIterationLimit(),
		temp:        cfg.Temp,
		toolsOn:     cfg.Tools,
		skills:      skills,
		textOnly:    cfg.Provider == "deepseek",
	}
	if cfg.Provider == "chatgpt" {
		manager := chatgptauth.NewManager(chatgptauth.DefaultCredentialPath())
		c.responseProvider = providerapi.NewChatGPT(manager.AccessToken)
	} else if cfg.Provider == "anthropic" || cfg.Provider == "google" || cfg.Provider == "ollama" {
		c.responseProvider = providerapi.ProviderFactory(cfg.Provider, cfg.APIKey, cfg.BaseURL)
	}
	if cfg.FallbackProvider != "" && cfg.FallbackProvider != cfg.Provider && cfg.FallbackModel != "" {
		primary := c.responseProvider
		if primary == nil {
			primary = providerapi.ProviderFactory(cfg.Provider, cfg.APIKey, cfg.BaseURL)
		}
		var secondary providerapi.Provider
		if cfg.FallbackProvider == "chatgpt" {
			manager := chatgptauth.NewManager(chatgptauth.DefaultCredentialPath())
			secondary = providerapi.NewChatGPT(manager.AccessToken)
		} else {
			secondary = providerapi.ProviderFactory(cfg.FallbackProvider, cfg.FallbackAPIKey, cfg.FallbackBaseURL)
		}
		c.responseProvider = providerapi.NewFailover(primary, secondary, cfg.FallbackModel, nil)
	}
	return c
}

func (c *Client) toolIterationLimit() int {
	if c.maxToolIter == -1 {
		return -1
	}
	if c.maxToolIter <= 0 {
		return config.DefaultMaxToolIterations
	}
	return c.maxToolIter
}

// SetSkillNames sets the active skill names for tool definitions.
func (c *Client) SetSkillNames(names []string) {
	c.skillNamesMu.Lock()
	c.skillNames = append([]string(nil), names...)
	c.skillNamesMu.Unlock()
}

type requestSkillNamesKey struct{}

func withRequestSkillNames(ctx context.Context, names []string) context.Context {
	return context.WithValue(ctx, requestSkillNamesKey{}, append([]string(nil), names...))
}

func (c *Client) skillNamesFor(ctx context.Context) []string {
	if names, ok := ctx.Value(requestSkillNamesKey{}).([]string); ok {
		return names
	}
	c.skillNamesMu.RLock()
	defer c.skillNamesMu.RUnlock()
	return append([]string(nil), c.skillNames...)
}

// Model returns the configured model name.
func (c *Client) Model() string {
	return c.model
}

// SetModel changes the model used for subsequent API calls.
func (c *Client) SetModel(model string) {
	if model == "" {
		return
	}
	c.model = model
}

// MaxTokens returns the configured max tokens limit.
func (c *Client) MaxTokens() int {
	return c.maxTok
}

// Temperature returns the configured temperature.
func (c *Client) Temperature() float32 {
	return c.temp
}

// CreateChatCompletion sends a raw chat completion request to the underlying API
// with retry logic. This is used by sub-agent executors that manage their own
// tool-calling loop.
func (c *Client) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	if c.responseProvider != nil {
		genericReq := providerapi.ChatRequest{Model: req.Model, MaxTokens: req.MaxTokens, Temperature: req.Temperature}
		genericReq.Stop = req.Stop
		if req.ResponseFormat != nil {
			genericReq.ResponseFormat = &providerapi.ResponseFormat{Type: string(req.ResponseFormat.Type)}
			if req.ResponseFormat.JSONSchema != nil {
				genericReq.ResponseFormat.Schema = responseToolParameters(req.ResponseFormat.JSONSchema.Schema)
			}
		}
		for _, msg := range req.Messages {
			m := providerapi.Message{Role: msg.Role, Content: msg.Content, ToolCallID: msg.ToolCallID, Name: msg.Name}
			for _, part := range msg.MultiContent {
				switch part.Type {
				case openai.ChatMessagePartTypeText:
					m.Parts = append(m.Parts, providerapi.ContentPart{Type: "text", Text: part.Text})
				case openai.ChatMessagePartTypeImageURL:
					if part.ImageURL != nil {
						m.Parts = append(m.Parts, providerapi.ContentPart{Type: "image_url", ImageURL: part.ImageURL.URL})
					}
				}
			}
			for _, tc := range msg.ToolCalls {
				m.ToolCalls = append(m.ToolCalls, providerapi.ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
			}
			genericReq.Messages = append(genericReq.Messages, m)
		}
		for _, tool := range req.Tools {
			if tool.Function != nil {
				genericReq.Tools = append(genericReq.Tools, providerapi.Tool{Name: tool.Function.Name, Description: tool.Function.Description, Parameters: responseToolParameters(tool.Function.Parameters)})
			}
		}
		resp, err := c.responseProvider.Chat(ctx, genericReq)
		if err != nil {
			return openai.ChatCompletionResponse{}, err
		}
		c.recordProviderUsage(resp)
		message := openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: resp.Content}
		for _, tc := range resp.ToolCalls {
			message.ToolCalls = append(message.ToolCalls, openai.ToolCall{ID: tc.ID, Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: tc.Name, Arguments: tc.Arguments}})
		}
		finish := openai.FinishReasonStop
		if len(message.ToolCalls) > 0 {
			finish = openai.FinishReasonToolCalls
		}
		return openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{{Message: message, FinishReason: finish}},
			Usage:   openai.Usage{PromptTokens: resp.PromptTokens, CompletionTokens: resp.CompletionTokens, TotalTokens: resp.TotalTokens},
		}, nil
	}
	return retry(ctx, func() (openai.ChatCompletionResponse, error) {
		return c.client.CreateChatCompletion(ctx, req)
	})
}

// ChatMessage mirrors openai.ChatCompletionMessage.
type ChatMessage struct {
	Parts   []providerapi.ContentPart
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
	if c.responseProvider != nil {
		req := providerapi.ChatRequest{Model: c.model, MaxTokens: c.maxTok, Temperature: c.temp}
		for _, msg := range messages {
			req.Messages = append(req.Messages, providerapi.Message{Role: msg.Role, Content: msg.Content, Parts: msg.Parts})
		}
		resp, err := c.responseProvider.Chat(ctx, req)
		if err != nil {
			return nil, err
		}
		c.recordProviderUsage(resp)
		return &ChatResponse{Content: resp.Content, PromptTokens: resp.PromptTokens, CompletionTokens: resp.CompletionTokens, TotalTokens: resp.TotalTokens}, nil
	}
	msgs := make([]openai.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		msgs[i] = openAIChatMessage(m)
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

	if c.costEngine != nil {
		c.costEngine.RecordCompletion(c.model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, 0, 0)
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
	if c.responseProvider != nil {
		resp, err := c.completeWithResponseTools(ctx, messages, nil, logger)
		return resp, err
	}
	var tools []openai.Tool
	if c.toolsOn && c.skills != nil {
		tools = c.skills.ToolDefinitions(c.skillNamesFor(ctx))
	}
	if !c.toolsOn || len(tools) == 0 {
		return c.Complete(ctx, messages)
	}

	msgs := make([]openai.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		msgs[i] = openAIChatMessage(m)
	}

	var totalPrompt, totalCompletion int

	maxIterations := c.toolIterationLimit()
	for iter := 0; maxIterations < 0 || iter < maxIterations; iter++ {
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

		if c.costEngine != nil {
			c.costEngine.RecordCompletion(c.model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, 0, 0)
		}

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

		var observations []ToolCallResult
		for _, tc := range choice.Message.ToolCalls {
			logger.Debug("executing tool",
				"tool", tc.Function.Name,
				"args", tc.Function.Arguments,
				"iter", iter,
			)

			if c.costEngine != nil {
				c.costEngine.RecordToolUse(tc.Function.Name, resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
			}

			richResult, execErr := executeSkillResult(ctx, c.skills, tc.Function.Name, tc.Function.Arguments)
			result := richResult.Content
			observations = append(observations, ToolCallResult{ID: tc.ID, Name: tc.Function.Name, Images: richResult.Images, IsError: richResult.IsError || execErr != nil})
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
		if !c.textOnly {
			msgs = append(msgs, openAIImageObservations(observations)...)
		}
	}

	return nil, fmt.Errorf("tool calling reached ai.max_tool_iterations (%d)", maxIterations)
}

// CompleteWithToolsStream sends a streaming chat completion with tool calling loop.
// It calls callback for each content delta token, and handles tool calls between stream cycles.
func (c *Client) CompleteWithToolsStream(ctx context.Context, messages []ChatMessage, callback StreamCallback, logger *slog.Logger) (*ChatResponse, error) {
	if c.responseProvider != nil {
		resp, err := c.completeWithResponseTools(ctx, messages, callback, logger)
		return resp, err
	}
	var tools []openai.Tool
	if c.toolsOn && c.skills != nil {
		tools = c.skills.ToolDefinitions(c.skillNamesFor(ctx))
	}

	msgs := make([]openai.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		msgs[i] = openAIChatMessage(m)
	}

	var totalPrompt, totalCompletion int

	maxIterations := c.toolIterationLimit()
	for iter := 0; maxIterations < 0 || iter < maxIterations; iter++ {
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

		// Execute tool calls — use StreamingToolManager for intelligent
		// parallelism when available, fall back to sequential execution.
		var observations []ToolCallResult
		if c.streamingToolMgr != nil {
			// Convert accumulated tool calls to ToolCallInfo
			calls := make([]ToolCallInfo, 0, len(toolCalls))
			for _, tc := range toolCalls {
				calls = append(calls, ToolCallInfo{
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})

				// Fire ToolEventStart for each tool
				if c.toolEventCB != nil {
					var parsedArgs map[string]any
					if err := json.Unmarshal([]byte(tc.Function.Arguments), &parsedArgs); err != nil {
						parsedArgs = map[string]any{"raw": tc.Function.Arguments}
					}
					c.toolEventCB(ToolEvent{
						Type:     ToolEventStart,
						ToolName: tc.Function.Name,
						ToolID:   tc.ID,
						Args:     parsedArgs,
					})
				}

				if c.costEngine != nil {
					c.costEngine.RecordToolUse(tc.Function.Name, 0, 0)
				}
			}

			// Execute with parallel partitioning
			batchStart := time.Now()
			results := c.streamingToolMgr.ExecuteToolCalls(ctx, calls)
			observations = append(observations, results...)
			batchElapsed := time.Since(batchStart)

			logger.Debug("streaming tool batch complete",
				"total", len(results),
				"elapsed", batchElapsed,
				"iter", iter,
			)

			// Convert results back to messages
			for _, r := range results {
				content := r.Content
				if r.IsError {
					content = fmt.Sprintf("Erro executando tool: %s", r.Content)
				}

				// Fire ToolEventComplete/Error
				if c.toolEventCB != nil {
					if r.IsError {
						c.toolEventCB(ToolEvent{
							Type:     ToolEventError,
							ToolName: r.Name,
							ToolID:   r.ID,
							Error:    r.Content,
							Elapsed:  batchElapsed,
						})
					} else {
						displayResult := r.Content
						if len(displayResult) > 200 {
							displayResult = displayResult[:197] + "..."
						}
						c.toolEventCB(ToolEvent{
							Type:     ToolEventComplete,
							ToolName: r.Name,
							ToolID:   r.ID,
							Result:   displayResult,
							Elapsed:  batchElapsed,
						})
					}
				}

				msgs = append(msgs, openai.ChatCompletionMessage{
					Role:       openai.ChatMessageRoleTool,
					Content:    content,
					Name:       r.Name,
					ToolCallID: r.ID,
				})
			}
		} else {
			// Fallback: sequential execution
			for _, tc := range toolCalls {
				logger.Debug("executing tool (stream)",
					"tool", tc.Function.Name,
					"args", tc.Function.Arguments,
					"iter", iter,
				)

				// Parse args for the tool event
				var parsedArgs map[string]any
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &parsedArgs); err != nil {
					parsedArgs = map[string]any{"raw": tc.Function.Arguments}
				}

				// Fire ToolEventStart
				if c.toolEventCB != nil {
					c.toolEventCB(ToolEvent{
						Type:     ToolEventStart,
						ToolName: tc.Function.Name,
						ToolID:   tc.ID,
						Args:     parsedArgs,
					})
				}

				toolStart := time.Now()

				if c.costEngine != nil {
					c.costEngine.RecordToolUse(tc.Function.Name, 0, 0)
				}

				richResult, execErr := executeSkillResult(ctx, c.skills, tc.Function.Name, tc.Function.Arguments)
				result := richResult.Content
				observations = append(observations, ToolCallResult{ID: tc.ID, Name: tc.Function.Name, Images: richResult.Images, IsError: richResult.IsError || execErr != nil})

				toolElapsed := time.Since(toolStart)

				if execErr != nil {
					result = fmt.Sprintf("Erro executando tool: %v", execErr)
					// Fire ToolEventError
					if c.toolEventCB != nil {
						c.toolEventCB(ToolEvent{
							Type:     ToolEventError,
							ToolName: tc.Function.Name,
							ToolID:   tc.ID,
							Args:     parsedArgs,
							Error:    execErr.Error(),
							Elapsed:  toolElapsed,
						})
					}
				} else {
					// Fire ToolEventComplete
					if c.toolEventCB != nil {
						displayResult := result
						if len(displayResult) > 200 {
							displayResult = displayResult[:197] + "..."
						}
						c.toolEventCB(ToolEvent{
							Type:     ToolEventComplete,
							ToolName: tc.Function.Name,
							ToolID:   tc.ID,
							Args:     parsedArgs,
							Result:   displayResult,
							Elapsed:  toolElapsed,
						})
					}
				}

				msgs = append(msgs, openai.ChatCompletionMessage{
					Role:       openai.ChatMessageRoleTool,
					Content:    result,
					Name:       tc.Function.Name,
					ToolCallID: tc.ID,
				})
			}
		}

		if !c.textOnly {
			msgs = append(msgs, openAIImageObservations(observations)...)
		}

		// Notify callback that tool execution happened (optional separator)
		if callback != nil {
			callback("\n")
		}
	}

	return nil, fmt.Errorf("tool calling reached ai.max_tool_iterations (%d)", maxIterations)
}
