package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/ai"
	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/types"
	openai "github.com/sashabaranov/go-openai"
)

// Default limits for sub-agent execution.
const (
	DefaultMaxTurns   = 10
	DefaultTokenBudge = 50_000
	DefaultTimeout    = 120 * time.Second
)

// SubAgentConfig holds the dependencies for creating a SubAgentExecutor.
type SubAgentConfig struct {
	AIClient    *ai.Client
	AgentReg    *agent.Registry
	ToolReg     *tool.Registry
	Logger      *slog.Logger
	MaxTurns    int
	TokenBudget int
}

// SubAgentExecutor runs an AI query loop for sub-agent tasks.
// It looks up the agent definition, restricts tools to the agent's skill
// set, and drives a multi-turn tool-calling conversation until the sub-agent
// produces a final text response or a limit is reached.
type SubAgentExecutor struct {
	aiClient    *ai.Client
	agentReg    *agent.Registry
	toolReg     *tool.Registry
	logger      *slog.Logger
	maxTurns    int
	tokenBudget int
}

// NewSubAgentExecutor creates a SubAgentExecutor from the given config.
func NewSubAgentExecutor(cfg SubAgentConfig) *SubAgentExecutor {
	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = DefaultMaxTurns
	}
	tokenBudget := cfg.TokenBudget
	if tokenBudget <= 0 {
		tokenBudget = DefaultTokenBudge
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &SubAgentExecutor{
		aiClient:    cfg.AIClient,
		agentReg:    cfg.AgentReg,
		toolReg:     cfg.ToolReg,
		logger:      logger,
		maxTurns:    maxTurns,
		tokenBudget: tokenBudget,
	}
}

// Execute runs a sub-agent to completion and returns the final text response.
//
// The flow:
//  1. Look up the agent in the registry (fall back to "task" if not found).
//  2. Derive the allowed tool set from the agent's Skills list.
//  3. Build a system prompt from the agent's persona.
//  4. Run an agentic loop: send messages → get response → execute tool calls → repeat.
//  5. Stop when the model produces a text-only response (no tool calls),
//     max turns are reached, token budget is exceeded, or context is cancelled.
func (e *SubAgentExecutor) Execute(ctx context.Context, agentName string, query string) (string, error) {
	// 1. Look up agent definition.
	ag := e.agentReg.Get(agentName)
	if ag == nil {
		// Should not happen since Registry.Get falls back to default,
		// but guard against nil registries.
		ag = agent.TaskAgent()
	}
	e.logger.Info("subagent executing",
		"agent", ag.Name,
		"query_len", len(query),
		"max_turns", e.maxTurns,
	)

	// 2. Resolve the allowed tools for this agent.
	allowedTools := e.resolveTools(ag)
	if len(allowedTools) == 0 {
		e.logger.Warn("subagent has no tools available", "agent", ag.Name)
	}

	// 3. Build system prompt.
	systemPrompt := ag.Persona
	if systemPrompt == "" {
		systemPrompt = fmt.Sprintf("Voce e o subagente '%s'. Execute a tarefa solicitada de forma completa e concisa.", ag.Name)
	}

	// Wire the system prompt through the package-level PromptCache so that
	// repeated calls with the same persona text are deduplicated and reused.
	// Get() is called first to detect a hit before Put() stores/returns the entry.
	cache := GetPromptCache()
	if _, hit := cache.Get(systemPrompt); hit {
		e.logger.Debug("subagent system prompt cache hit",
			"agent", ag.Name,
			"prompt_len", len(systemPrompt),
		)
	} else {
		e.logger.Debug("subagent system prompt cache miss",
			"agent", ag.Name,
			"prompt_len", len(systemPrompt),
		)
	}
	entry := cache.Put(systemPrompt)
	// Use entry.Content (guaranteed deduplicated/normalised by the cache).
	systemPrompt = entry.Content

	// 4. Build initial messages.
	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
		{Role: openai.ChatMessageRoleUser, Content: query},
	}

	// 5. Agentic loop.
	var (
		totalPromptTokens     int
		totalCompletionTokens int
		lastContent           string
	)

	for turn := 0; turn < e.maxTurns; turn++ {
		// Check context cancellation.
		if err := ctx.Err(); err != nil {
			e.logger.Warn("subagent context cancelled", "agent", ag.Name, "turn", turn, "error", err)
			if lastContent != "" {
				return lastContent + "\n\n[subagente interrompido: contexto cancelado]", nil
			}
			return "", fmt.Errorf("subagente cancelado: %w", err)
		}

		// Check token budget.
		totalTokens := totalPromptTokens + totalCompletionTokens
		if totalTokens > e.tokenBudget {
			e.logger.Warn("subagent token budget exceeded",
				"agent", ag.Name,
				"turn", turn,
				"total_tokens", totalTokens,
				"budget", e.tokenBudget,
			)
			if lastContent != "" {
				return lastContent + "\n\n[subagente parou: orcamento de tokens excedido]", nil
			}
			return "[subagente parou: orcamento de tokens excedido]", nil
		}

		e.logger.Debug("subagent turn",
			"agent", ag.Name,
			"turn", turn,
			"messages", len(msgs),
			"total_tokens", totalTokens,
		)

		// Make the API request.
		resp, err := e.aiClient.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
			Model:       e.resolveModel(ag),
			Messages:    msgs,
			MaxTokens:   e.resolveMaxTokens(ag),
			Temperature: e.resolveTemperature(ag),
			Tools:       allowedTools,
		})
		if err != nil {
			return "", fmt.Errorf("subagent AI call failed (turn %d): %w", turn, err)
		}

		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("subagent received empty response (turn %d)", turn)
		}

		// Track token usage.
		totalPromptTokens += resp.Usage.PromptTokens
		totalCompletionTokens += resp.Usage.CompletionTokens

		choice := resp.Choices[0]

		// If the model produced text content, capture it.
		if choice.Message.Content != "" {
			lastContent = choice.Message.Content
		}

		// Check if the model is done (no tool calls).
		if choice.FinishReason != openai.FinishReasonToolCalls || len(choice.Message.ToolCalls) == 0 {
			e.logger.Info("subagent completed",
				"agent", ag.Name,
				"turns", turn+1,
				"prompt_tokens", totalPromptTokens,
				"completion_tokens", totalCompletionTokens,
				"finish_reason", choice.FinishReason,
			)
			return lastContent, nil
		}

		// Append the assistant message with tool calls.
		msgs = append(msgs, choice.Message)

		// Execute each tool call.
		for _, tc := range choice.Message.ToolCalls {
			toolResult := e.executeTool(ctx, ag, tc)
			msgs = append(msgs, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    toolResult,
				Name:       tc.Function.Name,
				ToolCallID: tc.ID,
			})
		}
	}

	// Max turns reached.
	e.logger.Warn("subagent reached max turns",
		"agent", ag.Name,
		"max_turns", e.maxTurns,
		"prompt_tokens", totalPromptTokens,
		"completion_tokens", totalCompletionTokens,
	)
	if lastContent != "" {
		return lastContent + "\n\n[subagente parou: limite de turnos atingido]", nil
	}
	return "[subagente parou: limite de turnos atingido]", nil
}

// resolveTools builds the OpenAI tool definitions for the agent's allowed skills.
// It filters the tool registry by the agent's Skills list. If the agent has no
// Skills configured, the sub-agent runs without tools (text-only).
func (e *SubAgentExecutor) resolveTools(ag *agent.Agent) []openai.Tool {
	if len(ag.Skills) == 0 {
		return nil
	}

	// Filter the tool registry to only include tools in the agent's skill set.
	allowed := e.toolReg.FilterByNames(ag.Skills)
	if len(allowed) == 0 {
		return nil
	}

	result := make([]openai.Tool, 0, len(allowed))
	for _, t := range allowed {
		// Skip the agent_run tool to prevent recursive sub-agent spawning.
		if t.Name() == "agent_run" {
			continue
		}

		schema := t.InputSchema()
		properties := make(map[string]any)
		var required []string

		if props, ok := schema["properties"]; ok {
			if pm, ok := props.(map[string]any); ok {
				properties = pm
			}
		}
		if req, ok := schema["required"]; ok {
			switch r := req.(type) {
			case []string:
				required = r
			case []any:
				for _, v := range r {
					if s, ok := v.(string); ok {
						required = append(required, s)
					}
				}
			}
		}

		params := map[string]any{
			"type":       "object",
			"properties": properties,
		}
		if len(required) > 0 {
			params["required"] = required
		}

		result = append(result, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  params,
			},
		})
	}
	return result
}

// executeTool runs a single tool call and returns the result as a string.
// It enforces the agent's tool restrictions by checking that the tool is
// in the agent's allowed set.
func (e *SubAgentExecutor) executeTool(ctx context.Context, ag *agent.Agent, tc openai.ToolCall) string {
	toolName := tc.Function.Name

	// Enforce tool restrictions: verify the tool is in the agent's skill set.
	if !e.isToolAllowed(ag, toolName) {
		e.logger.Warn("subagent attempted disallowed tool",
			"agent", ag.Name,
			"tool", toolName,
		)
		return fmt.Sprintf("Erro: ferramenta '%s' nao permitida para o agente '%s'", toolName, ag.Name)
	}

	// Parse arguments.
	var args map[string]any
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		// Try as string map fallback.
		var strArgs map[string]string
		if err2 := json.Unmarshal([]byte(tc.Function.Arguments), &strArgs); err2 != nil {
			return fmt.Sprintf("Erro parsing argumentos da ferramenta '%s': %v", toolName, err)
		}
		args = make(map[string]any, len(strArgs))
		for k, v := range strArgs {
			args[k] = v
		}
	}

	e.logger.Debug("subagent executing tool",
		"agent", ag.Name,
		"tool", toolName,
		"args_keys", mapKeys(args),
	)

	// Execute through the tool registry's pipeline (validate → permissions → execute).
	toolCtx := types.ToolContext{
		Context:    ctx,
		WorkingDir: ".",
	}
	result, err := e.toolReg.Execute(toolName, args, toolCtx)
	if err != nil {
		return fmt.Sprintf("Erro executando ferramenta '%s': %v", toolName, err)
	}
	if result.IsError {
		return fmt.Sprintf("Erro: %s", result.Content)
	}

	// Truncate large results.
	return truncateResult(result.Content, 50*1024)
}

// isToolAllowed checks if a tool is in the agent's allowed skill set.
func (e *SubAgentExecutor) isToolAllowed(ag *agent.Agent, toolName string) bool {
	// Prevent recursive agent spawning.
	if toolName == "agent_run" {
		return false
	}
	for _, skill := range ag.Skills {
		if skill == toolName {
			return true
		}
	}
	return false
}

// resolveModel returns the model to use for this agent, falling back to the
// AI client's configured model.
func (e *SubAgentExecutor) resolveModel(ag *agent.Agent) string {
	if ag.Model != "" {
		return ag.Model
	}
	return e.aiClient.Model()
}

// resolveMaxTokens returns the max tokens for this agent.
func (e *SubAgentExecutor) resolveMaxTokens(ag *agent.Agent) int {
	if ag.MaxTokens > 0 {
		return ag.MaxTokens
	}
	return e.aiClient.MaxTokens()
}

// resolveTemperature returns the temperature for this agent.
func (e *SubAgentExecutor) resolveTemperature(ag *agent.Agent) float32 {
	if ag.Temperature > 0 {
		return ag.Temperature
	}
	return e.aiClient.Temperature()
}

// truncateResult truncates a string to maxLen bytes.
func truncateResult(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n... [saida truncada]"
}

// mapKeys returns the keys of a map as a comma-separated string (for logging).
func mapKeys(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return strings.Join(keys, ",")
}
