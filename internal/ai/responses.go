package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	providerapi "github.com/costa/polypod/internal/provider"
)

func (c *Client) completeWithResponseTools(ctx context.Context, messages []ChatMessage, callback StreamCallback, logger *slog.Logger) (*ChatResponse, error) {
	req := providerapi.ChatRequest{Model: c.model, MaxTokens: c.maxTok, Temperature: c.temp}
	for _, msg := range messages {
		req.Messages = append(req.Messages, providerapi.Message{Role: msg.Role, Content: msg.Content, Parts: msg.Parts})
	}
	var catalog *toolCatalog
	if c.toolsOn && c.skills != nil {
		catalog = c.catalogFor(ctx)
		req.Tools = catalog.providerDefinitions()
	}

	var totalPrompt, totalCompletion int
	maxIterations := c.toolIterationLimit()
	for iter := 0; maxIterations < 0 || iter < maxIterations; iter++ {
		var resp *providerapi.ChatResponse
		var err error
		if callback == nil {
			resp, err = c.responseProvider.Chat(ctx, req)
		} else {
			resp, err = c.responseProvider.ChatStream(ctx, req, func(delta providerapi.StreamDelta) {
				if delta.Content != "" {
					callback(delta.Content)
				}
			})
		}
		if err != nil {
			return nil, err
		}
		totalPrompt += resp.PromptTokens
		totalCompletion += resp.CompletionTokens
		c.recordProviderUsage(resp)
		if len(resp.ToolCalls) == 0 {
			return &ChatResponse{Content: resp.Content, PromptTokens: totalPrompt, CompletionTokens: totalCompletion, TotalTokens: totalPrompt + totalCompletion}, nil
		}

		assistant := providerapi.Message{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls}
		req.Messages = append(req.Messages, assistant)

		var observations []ToolCallResult
		if c.streamingToolMgr != nil {
			calls := make([]ToolCallInfo, 0, len(resp.ToolCalls))
			for _, tc := range resp.ToolCalls {
				calls = append(calls, ToolCallInfo{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
				c.fireResponseToolStart(ctx, tc)
				if c.costEngine != nil {
					c.costEngine.RecordToolUse(tc.Name, resp.PromptTokens, resp.CompletionTokens)
				}
			}
			started := time.Now()
			results := c.streamingToolMgr.ExecuteToolCalls(ctx, calls)
			observations = append(observations, results...)
			elapsed := time.Since(started)
			for _, result := range results {
				content := result.Content
				if result.IsError {
					content = "Erro executando tool: " + content
				}
				c.fireResponseToolDone(ctx, result, elapsed)
				req.Messages = append(req.Messages, providerapi.Message{Role: "tool", Content: content, ToolCallID: result.ID, Name: result.Name})
			}
		} else {
			for _, tc := range resp.ToolCalls {
				logger.Debug("executing Responses API tool", "tool", tc.Name, "iter", iter)
				c.fireResponseToolStart(ctx, tc)
				started := time.Now()
				richResult, execErr := executeSkillResult(ctx, c.skills, tc.Name, tc.Arguments)
				result := richResult.Content
				observations = append(observations, ToolCallResult{ID: tc.ID, Name: tc.Name, Content: result, Images: richResult.Images, IsError: richResult.IsError || execErr != nil})
				if execErr != nil {
					result = fmt.Sprintf("Erro executando tool: %v", execErr)
				}
				c.fireResponseToolDone(ctx, ToolCallResult{ID: tc.ID, Name: tc.Name, Content: result, IsError: richResult.IsError || execErr != nil}, time.Since(started))
				req.Messages = append(req.Messages, providerapi.Message{Role: "tool", Content: result, ToolCallID: tc.ID, Name: tc.Name})
			}
		}
		if c.responseProvider.SupportsVision() {
			req.Messages = append(req.Messages, imageObservations(observations)...)
		}
		if catalog != nil {
			catalog.observe(observations)
			req.Tools = catalog.providerDefinitions()
		}
		if callback != nil {
			callback("\n")
		}
	}
	return nil, fmt.Errorf("chamadas de ferramentas atingiram ai.max_tool_iterations (%d)", maxIterations)
}

func (c *Client) fireResponseToolStart(ctx context.Context, tc providerapi.ToolCall) {
	var args map[string]any
	if json.Unmarshal([]byte(tc.Arguments), &args) != nil {
		args = map[string]any{"raw": tc.Arguments}
	}
	c.emitToolEvent(ctx, ToolEvent{Type: ToolEventStart, ToolName: tc.Name, ToolID: tc.ID, Args: args})
}

func (c *Client) fireResponseToolDone(ctx context.Context, result ToolCallResult, elapsed time.Duration) {
	resultBytes, resultHash := toolResultMetadata(result.Content)
	event := ToolEvent{ToolName: result.Name, ToolID: result.ID, Elapsed: elapsed,
		ResultBytes: resultBytes, ResultSHA256: resultHash}
	if result.IsError {
		event.Type = ToolEventError
		event.Error = result.Content
	} else {
		event.Type = ToolEventComplete
		event.Result = result.Content
		if len(event.Result) > 200 {
			event.Result = event.Result[:197] + "..."
		}
	}
	c.emitToolEvent(ctx, event)
}

func responseToolParameters(value any) map[string]interface{} {
	if params, ok := value.(map[string]interface{}); ok {
		return params
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return map[string]interface{}{"type": "object"}
	}
	var params map[string]interface{}
	if json.Unmarshal(raw, &params) != nil {
		return map[string]interface{}{"type": "object"}
	}
	return params
}
