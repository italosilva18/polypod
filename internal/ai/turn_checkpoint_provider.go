package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	providerapi "github.com/costa/polypod/internal/provider"
	"github.com/costa/polypod/internal/turncheckpoint"
)

func (c *Client) completeWithCheckpointedProvider(ctx context.Context, initial []ChatMessage, callback StreamCallback, logger *slog.Logger) (*ChatResponse, error) {
	msgs := make([]providerapi.Message, len(initial))
	for i, msg := range initial {
		msgs[i] = providerapi.Message{Role: msg.Role, Content: msg.Content, Parts: msg.Parts}
	}
	cp, scope, err := c.beginAICheckpoint(ctx, "provider", msgs)
	if err != nil {
		return nil, err
	}
	if scope.resume {
		if err := json.Unmarshal(cp.Messages, &msgs); err != nil {
			return nil, err
		}
	}
	var deferredImages []providerapi.Message
	if len(cp.DeferredImages) > 0 {
		if err := json.Unmarshal(cp.DeferredImages, &deferredImages); err != nil {
			return nil, err
		}
	}
	if cp.Phase == turncheckpoint.AIPhaseDone {
		return responseFromCheckpoint(cp), nil
	}
	ctx = withRequestSkillNames(ctx, cp.SkillNames)
	ctx = withRequestModel(ctx, cp.Model)
	catalog := c.catalogFor(ctx)
	replayProviderCatalog(catalog, msgs)
	limit := c.toolIterationLimit()
	for limit < 0 || cp.Iteration < limit {
		if cp.Phase == turncheckpoint.AIPhaseModel {
			req := providerapi.ChatRequest{Model: cp.Model, MaxTokens: c.maxTok, Temperature: c.temp, Messages: msgs}
			if c.toolsOn {
				req.Tools = catalog.providerDefinitions()
			}
			var resp *providerapi.ChatResponse
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
			cp.PromptTokens += resp.PromptTokens
			cp.CompletionTokens += resp.CompletionTokens
			c.recordProviderUsage(resp, cp.Model)
			if len(resp.ToolCalls) == 0 {
				cp.Phase = turncheckpoint.AIPhaseDone
				cp.FinalContent = resp.Content
				if err := saveAICheckpoint(scope, cp, msgs); err != nil {
					return nil, err
				}
				return responseFromCheckpoint(cp), nil
			}
			msgs = append(msgs, providerapi.Message{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls})
			cp.Phase = turncheckpoint.AIPhaseTools
			cp.NextToolIndex = 0
			if err := saveAICheckpoint(scope, cp, msgs); err != nil {
				return nil, err
			}
		}
		if cp.Phase != turncheckpoint.AIPhaseTools {
			return nil, fmt.Errorf("fase de checkpoint %q nao esperada", cp.Phase)
		}
		calls, err := pendingProviderTools(msgs, cp.NextToolIndex)
		if err != nil {
			return nil, err
		}
		for i := cp.NextToolIndex; i < len(calls); i++ {
			tc := calls[i]
			if err := c.checkCheckpointToolDefinitions(ctx, cp); err != nil {
				return nil, err
			}
			cp.PendingToolCallID = checkpointToolID(tc.ID, tc.Name, i)
			if err := saveAICheckpoint(scope, cp, msgs); err != nil {
				return nil, err
			}
			c.fireResponseToolStart(ctx, tc)
			started := time.Now()
			if c.costEngine != nil {
				c.costEngine.RecordToolUse(tc.Name, 0, 0)
			}
			rich, execErr := executeSkillResult(ctx, c.skills, tc.Name, tc.Arguments)
			result := rich.Content
			if execErr != nil {
				result = fmt.Sprintf("Erro executando tool: %v", execErr)
			}
			observation := ToolCallResult{ID: tc.ID, Name: tc.Name, Content: result, Images: rich.Images, IsError: rich.IsError || execErr != nil}
			c.fireResponseToolDone(ctx, observation, time.Since(started))
			msgs = append(msgs, providerapi.Message{Role: "tool", Content: result, ToolCallID: tc.ID, Name: tc.Name})
			if c.responseProvider.SupportsVision() {
				deferredImages = append(deferredImages, imageObservations([]ToolCallResult{observation})...)
				cp.DeferredImages, err = json.Marshal(deferredImages)
				if err != nil {
					return nil, err
				}
			}
			catalog.observe([]ToolCallResult{observation})
			cp.PendingToolCallID = ""
			cp.NextToolIndex = i + 1
			if err := saveAICheckpoint(scope, cp, msgs); err != nil {
				return nil, err
			}
		}
		msgs = append(msgs, deferredImages...)
		deferredImages = nil
		cp.DeferredImages = nil
		cp.Phase = turncheckpoint.AIPhaseModel
		cp.NextToolIndex = 0
		cp.Iteration++
		if err := saveAICheckpoint(scope, cp, msgs); err != nil {
			return nil, err
		}
		if callback != nil {
			callback("\n")
		}
	}
	logger.Debug("checkpointed provider tool loop reached limit", "iterations", cp.Iteration)
	return nil, fmt.Errorf("chamadas de ferramentas atingiram ai.max_tool_iterations (%d)", limit)
}

func pendingProviderTools(messages []providerapi.Message, next int) ([]providerapi.ToolCall, error) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "assistant" || len(messages[i].ToolCalls) == 0 {
			continue
		}
		calls := messages[i].ToolCalls
		completed := 0
		for _, msg := range messages[i+1:] {
			if msg.Role == "tool" {
				if completed >= len(calls) || msg.ToolCallID != calls[completed].ID {
					return nil, errors.New("ordem de resultados de ferramentas no checkpoint invalida")
				}
				completed++
			}
		}
		if next != completed || next > len(calls) {
			return nil, errors.New("indice de ferramenta do checkpoint nao corresponde ao transcript")
		}
		return calls, nil
	}
	return nil, errors.New("checkpoint sem chamadas de ferramenta pendentes")
}

func replayProviderCatalog(catalog *toolCatalog, messages []providerapi.Message) {
	for _, msg := range messages {
		if msg.Role == "tool" {
			catalog.observe([]ToolCallResult{{Name: msg.Name, Content: msg.Content}})
		}
	}
}
