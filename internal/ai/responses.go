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
		req.Messages = append(req.Messages, providerapi.Message{Role: msg.Role, Content: msg.Content})
	}
	if c.toolsOn && c.skills != nil {
		for _, tool := range c.skills.ToolDefinitions(c.skillNames) {
			if tool.Function != nil {
				req.Tools = append(req.Tools, providerapi.Tool{
					Name: tool.Function.Name, Description: tool.Function.Description, Parameters: responseToolParameters(tool.Function.Parameters),
				})
			}
		}
	}

	var totalPrompt, totalCompletion int
	for iter := 0; iter < maxToolIter; iter++ {
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
		if c.costEngine != nil {
			c.costEngine.RecordCompletion(c.model, resp.PromptTokens, resp.CompletionTokens, 0, 0)
		}
		if len(resp.ToolCalls) == 0 {
			return &ChatResponse{Content: resp.Content, PromptTokens: totalPrompt, CompletionTokens: totalCompletion, TotalTokens: totalPrompt + totalCompletion}, nil
		}

		assistant := providerapi.Message{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls}
		req.Messages = append(req.Messages, assistant)

		if c.streamingToolMgr != nil {
			calls := make([]ToolCallInfo, 0, len(resp.ToolCalls))
			for _, tc := range resp.ToolCalls {
				calls = append(calls, ToolCallInfo{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
				c.fireResponseToolStart(tc)
				if c.costEngine != nil {
					c.costEngine.RecordToolUse(tc.Name, resp.PromptTokens, resp.CompletionTokens)
				}
			}
			started := time.Now()
			results := c.streamingToolMgr.ExecuteToolCalls(ctx, calls)
			elapsed := time.Since(started)
			for _, result := range results {
				content := result.Content
				if result.IsError {
					content = "Erro executando tool: " + content
				}
				c.fireResponseToolDone(result, elapsed)
				req.Messages = append(req.Messages, providerapi.Message{Role: "tool", Content: content, ToolCallID: result.ID, Name: result.Name})
			}
		} else {
			for _, tc := range resp.ToolCalls {
				logger.Debug("executing Responses API tool", "tool", tc.Name, "iter", iter)
				c.fireResponseToolStart(tc)
				started := time.Now()
				result, execErr := c.skills.Execute(tc.Name, tc.Arguments)
				if execErr != nil {
					result = fmt.Sprintf("Erro executando tool: %v", execErr)
				}
				c.fireResponseToolDone(ToolCallResult{ID: tc.ID, Name: tc.Name, Content: result, IsError: execErr != nil}, time.Since(started))
				req.Messages = append(req.Messages, providerapi.Message{Role: "tool", Content: result, ToolCallID: tc.ID, Name: tc.Name})
			}
		}
		if callback != nil {
			callback("\n")
		}
	}
	return nil, fmt.Errorf("tool calling excedeu %d iteracoes", maxToolIter)
}

func (c *Client) fireResponseToolStart(tc providerapi.ToolCall) {
	if c.toolEventCB == nil {
		return
	}
	var args map[string]any
	if json.Unmarshal([]byte(tc.Arguments), &args) != nil {
		args = map[string]any{"raw": tc.Arguments}
	}
	c.toolEventCB(ToolEvent{Type: ToolEventStart, ToolName: tc.Name, ToolID: tc.ID, Args: args})
}

func (c *Client) fireResponseToolDone(result ToolCallResult, elapsed time.Duration) {
	if c.toolEventCB == nil {
		return
	}
	event := ToolEvent{ToolName: result.Name, ToolID: result.ID, Elapsed: elapsed}
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
	c.toolEventCB(event)
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
