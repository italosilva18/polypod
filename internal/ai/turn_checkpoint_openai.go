package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/costa/polypod/internal/turncheckpoint"
	openai "github.com/sashabaranov/go-openai"
)

// The checkpointed path executes tools serially. A concurrent batch cannot
// offer a durable boundary between each external effect and its result.
func (c *Client) completeWithCheckpointedOpenAI(ctx context.Context, initial []ChatMessage, callback StreamCallback, logger *slog.Logger) (*ChatResponse, error) {
	msgs := make([]openai.ChatCompletionMessage, len(initial))
	for i, msg := range initial {
		msgs[i] = openAIChatMessage(msg)
	}
	cp, scope, err := c.beginAICheckpoint(ctx, "openai", msgs)
	if err != nil {
		return nil, err
	}
	if scope.resume {
		if err := json.Unmarshal(cp.Messages, &msgs); err != nil {
			return nil, err
		}
	}
	var deferredImages []openai.ChatCompletionMessage
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
	replayOpenAICatalog(catalog, msgs)
	limit := c.toolIterationLimit()
	guard := newCognitiveGuard()
	for limit < 0 || cp.Iteration < limit {
		if cp.Phase == turncheckpoint.AIPhaseModel {
			for _, guidance := range takeSteering(ctx) {
				msgs = append(msgs, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: guidance})
			}
			if err := saveAICheckpoint(scope, cp, msgs); err != nil {
				return nil, err
			}
			req := openai.ChatCompletionRequest{Model: cp.Model, Messages: msgs, MaxTokens: c.maxTok, Temperature: c.temp, Stream: true}
			c.applyReasoningToOpenAIReq(ctx, &req, guard.boostedReasoning)
			if c.toolsOn {
				req.Tools = catalog.definitions()
			}
			stream, err := retry(ctx, func() (*openai.ChatCompletionStream, error) {
				return c.client.CreateChatCompletionStream(ctx, req)
			})
			if err != nil {
				return nil, fmt.Errorf("stream creation (iter %d): %w", cp.Iteration, err)
			}
			var content string
			prepared := newStreamToolPreparation()
			for {
				chunk, recvErr := stream.Recv()
				if errors.Is(recvErr, io.EOF) {
					break
				}
				if recvErr != nil {
					stream.Close()
					return nil, fmt.Errorf("stream recv (iter %d): %w", cp.Iteration, recvErr)
				}
				if len(chunk.Choices) == 0 {
					continue
				}
				delta := chunk.Choices[0].Delta
				if delta.Content != "" {
					content += delta.Content
					if callback != nil {
						callback(delta.Content)
					}
				}
				for _, tc := range delta.ToolCalls {
					prepared.add(tc)
				}
			}
			stream.Close()
			if len(prepared.byIndex) == 0 {
				var guidance []string
				if limit < 0 || cp.Iteration+1 < limit {
					guidance = takeSteering(ctx)
					if len(guidance) == 0 {
						if gate := guard.checkCompletionGate(ctx); gate != "" {
							guidance = append(guidance, gate)
						}
					}
				}
				if len(guidance) > 0 {
					msgs = append(msgs, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: content})
					for _, message := range guidance {
						msgs = append(msgs, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: message})
					}
					cp.Iteration++
					if err := saveAICheckpoint(scope, cp, msgs); err != nil {
						return nil, err
					}
					if callback != nil {
						callback("\n")
					}
					continue
				}
				cp.Phase = turncheckpoint.AIPhaseDone
				cp.FinalContent = content
				if err := saveAICheckpoint(scope, cp, msgs); err != nil {
					return nil, err
				}
				c.recordTurnPlaybook(ctx, guard, content)
				return responseFromCheckpoint(cp), nil
			}
			toolCalls, _, err := prepared.finalize()
			if err != nil {
				return nil, err
			}
			msgs = append(msgs, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: content, ToolCalls: toolCalls})
			cp.Phase = turncheckpoint.AIPhaseTools
			cp.NextToolIndex = 0
			if err := saveAICheckpoint(scope, cp, msgs); err != nil {
				return nil, err
			}
		}
		if cp.Phase != turncheckpoint.AIPhaseTools {
			return nil, fmt.Errorf("fase de checkpoint %q nao esperada", cp.Phase)
		}
		calls, err := pendingOpenAITools(msgs, cp.NextToolIndex)
		if err != nil {
			return nil, err
		}
		for i := cp.NextToolIndex; i < len(calls); i++ {
			tc := calls[i]
			if err := c.checkCheckpointToolDefinitions(ctx, cp); err != nil {
				return nil, err
			}
			cp.PendingToolCallID = checkpointToolID(tc.ID, tc.Function.Name, i)
			if err := saveAICheckpoint(scope, cp, msgs); err != nil {
				return nil, err
			}
			var args map[string]any
			if json.Unmarshal([]byte(tc.Function.Arguments), &args) != nil {
				args = map[string]any{"raw": tc.Function.Arguments}
			}
			c.emitToolEvent(ctx, ToolEvent{Type: ToolEventStart, ToolName: tc.Function.Name, ToolID: tc.ID, Args: args})
			started := time.Now()
			if c.costEngine != nil {
				c.costEngine.RecordToolUse(tc.Function.Name, 0, 0)
			}
			rich, execErr := executeSkillResult(ctx, c.skills, tc.Function.Name, tc.Function.Arguments)
			result := rich.Content
			if execErr != nil {
				result = fmt.Sprintf("Erro executando tool: %v", execErr)
			}
			observation := ToolCallResult{ID: tc.ID, Name: tc.Function.Name, Content: result, Images: rich.Images, IsError: rich.IsError || execErr != nil}
			bytes, hash := toolResultMetadata(result)
			event := ToolEvent{ToolName: tc.Function.Name, ToolID: tc.ID, Args: args, ResultBytes: bytes, ResultSHA256: hash, Elapsed: time.Since(started)}
			if observation.IsError {
				event.Type, event.Error = ToolEventError, result
			} else {
				event.Type, event.Result = ToolEventComplete, result
			}
			c.emitToolEvent(ctx, event)
			msgs = append(msgs, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleTool, Content: result, Name: tc.Function.Name, ToolCallID: tc.ID})
			if !c.isTextOnly() {
				deferredImages = append(deferredImages, openAIImageObservations([]ToolCallResult{observation})...)
				cp.DeferredImages, err = json.Marshal(deferredImages)
				if err != nil {
					return nil, err
				}
			}
			catalog.observe([]ToolCallResult{observation})
			if replan, _ := guard.observe(ctx, []guardedCall{{Name: tc.Function.Name, Arguments: tc.Function.Arguments}}, []ToolCallResult{observation}); replan != "" && (limit < 0 || cp.Iteration+1 < limit) {
				deferredImages = append(deferredImages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: replan})
				cp.DeferredImages, err = json.Marshal(deferredImages)
				if err != nil {
					return nil, err
				}
			}
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
	logger.Debug("checkpointed tool loop reached limit", "iterations", cp.Iteration)
	return nil, fmt.Errorf("tool calling reached ai.max_tool_iterations (%d)", limit)
}

func pendingOpenAITools(messages []openai.ChatCompletionMessage, next int) ([]openai.ToolCall, error) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != openai.ChatMessageRoleAssistant || len(messages[i].ToolCalls) == 0 {
			continue
		}
		calls := messages[i].ToolCalls
		completed := 0
		for _, msg := range messages[i+1:] {
			if msg.Role == openai.ChatMessageRoleTool {
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

func replayOpenAICatalog(catalog *toolCatalog, messages []openai.ChatCompletionMessage) {
	for _, msg := range messages {
		if msg.Role == openai.ChatMessageRoleTool {
			catalog.observe([]ToolCallResult{{Name: msg.Name, Content: msg.Content}})
		}
	}
}

func checkpointToolID(id, name string, index int) string {
	if id != "" {
		return id
	}
	return fmt.Sprintf("%s#%d", name, index)
}

func responseFromCheckpoint(cp *turncheckpoint.AICheckpoint) *ChatResponse {
	return &ChatResponse{Content: cp.FinalContent, PromptTokens: cp.PromptTokens,
		CompletionTokens: cp.CompletionTokens, TotalTokens: cp.PromptTokens + cp.CompletionTokens}
}
