package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	providerapi "github.com/costa/polypod/internal/provider"
)

const toolLimitSummaryPrompt = "O limite de chamadas de ferramentas foi atingido. Resuma o resultado atual sem solicitar mais ferramentas; informe se algum teste ou edição falhou."

func (c *Client) finalProviderSummary(ctx context.Context, req providerapi.ChatRequest, callback StreamCallback) (*providerapi.ChatResponse, error) {
	req.Tools = nil
	if len(req.Messages) == 0 || req.Messages[len(req.Messages)-1].Role != "user" || req.Messages[len(req.Messages)-1].Content != toolLimitSummaryPrompt {
		req.Messages = append(req.Messages, providerapi.Message{Role: "user", Content: toolLimitSummaryPrompt})
	}
	prov := c.activeResponseProviderFor(ctx)
	if callback == nil {
		return prov.Chat(ctx, req)
	}
	return prov.ChatStream(ctx, req, func(delta providerapi.StreamDelta) {
		if delta.Content != "" {
			callback(delta.Content)
		}
	})
}

func (c *Client) completeWithResponseTools(ctx context.Context, messages []ChatMessage, callback StreamCallback, logger *slog.Logger) (*ChatResponse, error) {
	model := c.modelFor(ctx)
	req := providerapi.ChatRequest{Model: model, MaxTokens: c.maxTok, Temperature: c.temp}
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
	guard := newCognitiveGuard()
	for iter := 0; maxIterations < 0 || iter < maxIterations; iter++ {
		for _, guidance := range takeSteering(ctx) {
			req.Messages = append(req.Messages, providerapi.Message{Role: "user", Content: guidance})
		}
		req.Model = model
		c.applyReasoningToProviderReq(ctx, &req, guard.boostedReasoning)
		prov := c.activeResponseProviderFor(ctx)
		var resp *providerapi.ChatResponse
		var err error
		var early *earlyResponseTools
		started := time.Now()
		if callback == nil {
			resp, err = prov.Chat(ctx, req)
		} else {
			streamCallback := func(delta providerapi.StreamDelta) {
				if delta.Content != "" {
					callback(delta.Content)
				}
			}
			if streamer, ok := prov.(completedCallStreamer); ok && c.streamingToolMgr != nil {
				early = newEarlyResponseTools(ctx, c)
				resp, err = streamer.ChatStreamWithCompletedCalls(ctx, req, streamCallback, early.itemReady)
				early.finish(err != nil)
			} else {
				resp, err = prov.ChatStream(ctx, req, streamCallback)
			}
		}
		if err != nil {
			c.Waterfall().RecordModelSpan(model, started, time.Since(started), 0, 0, true)
			return nil, err
		}
		c.Waterfall().RecordModelSpan(model, started, time.Since(started), resp.PromptTokens, resp.CompletionTokens, false)
		if early != nil {
			if err := early.validateFinal(resp.ToolCalls); err != nil {
				return nil, err
			}
		}
		totalPrompt += resp.PromptTokens
		totalCompletion += resp.CompletionTokens
		c.recordProviderUsage(resp, model)
		if len(resp.ToolCalls) == 0 {
			var guidance []string
			if maxIterations < 0 || iter+1 < maxIterations {
				guidance = takeSteering(ctx)
				if len(guidance) == 0 {
					if gate := guard.checkCompletionGate(ctx); gate != "" {
						guidance = append(guidance, gate)
					}
				}
			}
			if len(guidance) > 0 {
				req.Messages = append(req.Messages, providerapi.Message{Role: "assistant", Content: resp.Content})
				for _, message := range guidance {
					req.Messages = append(req.Messages, providerapi.Message{Role: "user", Content: message})
				}
				if callback != nil {
					callback("\n")
				}
				continue
			}
			c.recordTurnPlaybook(ctx, guard, resp.Content)
			return &ChatResponse{Content: resp.Content, PromptTokens: totalPrompt, CompletionTokens: totalCompletion, TotalTokens: totalPrompt + totalCompletion}, nil
		}

		assistant := providerapi.Message{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls}
		req.Messages = append(req.Messages, assistant)

		var observations []ToolCallResult
		if c.streamingToolMgr != nil {
			calls := make([]ToolCallInfo, 0, len(resp.ToolCalls))
			results := make([]ToolCallResult, 0, len(resp.ToolCalls))
			seenIDs := make(map[string]bool, len(resp.ToolCalls))
			pendingStarted := false
			for _, tc := range resp.ToolCalls {
				if tc.ID != "" {
					if seenIDs[tc.ID] {
						continue
					}
					seenIDs[tc.ID] = true
				}
				if c.costEngine != nil {
					c.costEngine.RecordToolUse(tc.Name, resp.PromptTokens, resp.CompletionTokens)
				}
				if early != nil {
					if job, ok := early.jobs[tc.ID]; ok {
						if pendingStarted {
							return nil, fmt.Errorf("resposta final do provedor alterou a ordem das ferramentas")
						}
						results = append(results, job.result)
						continue
					}
				}
				pendingStarted = true
				calls = append(calls, ToolCallInfo{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
				c.fireResponseToolStart(ctx, tc)
			}
			if len(calls) > 0 {
				started := time.Now()
				pending := c.streamingToolMgr.ExecuteToolCalls(ctx, calls)
				elapsed := time.Since(started)
				for _, result := range pending {
					c.fireResponseToolDone(ctx, result, elapsed)
				}
				results = append(results, pending...)
			}
			observations = append(observations, results...)
			for _, result := range results {
				content := result.Content
				if result.IsError {
					content = "Erro executando tool: " + content
				}
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
		if observationsContainImages(observations) && !c.modelSupportsVision(ctx, model) {
			if visProv, visModel := c.resolveAutoVisionFallback(model); visModel != "" {
				model = visModel
				ctx = withRequestModel(ctx, visModel)
				if visProv != nil {
					ctx = withRequestProvider(ctx, visProv)
				}
			}
		}
		if c.activeResponseProviderFor(ctx).SupportsVision() {
			req.Messages = append(req.Messages, imageObservations(observations)...)
		}
		if catalog != nil {
			catalog.observe(observations)
			req.Tools = catalog.providerDefinitions()
		}
		gCalls := make([]guardedCall, 0, len(resp.ToolCalls))
		for _, tc := range resp.ToolCalls {
			gCalls = append(gCalls, guardedCall{Name: tc.Name, Arguments: tc.Arguments})
		}
		if replan, _ := guard.observe(ctx, gCalls, observations); replan != "" && (maxIterations < 0 || iter+1 < maxIterations) {
			req.Messages = append(req.Messages, providerapi.Message{Role: "user", Content: replan})
		}
		if callback != nil {
			callback("\n")
		}
	}
	logger.Debug("provider tool loop reached limit", "iterations", maxIterations)
	resp, err := c.finalProviderSummary(ctx, req, callback)
	if err != nil {
		return nil, err
	}
	totalPrompt += resp.PromptTokens
	totalCompletion += resp.CompletionTokens
	c.recordProviderUsage(resp, model)
	return &ChatResponse{Content: resp.Content, PromptTokens: totalPrompt, CompletionTokens: totalCompletion, TotalTokens: totalPrompt + totalCompletion}, nil
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
