package subagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/ai"
	"github.com/costa/polypod/internal/project"
	"github.com/costa/polypod/internal/swarm"
	"github.com/costa/polypod/internal/tasks"
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
	Executor    ToolExecutor
	Logger      *slog.Logger
	MaxTurns    int
	TokenBudget int
}

// ToolExecutor routes sub-agent actions through the same execution pipeline
// used by the parent agent, including audit records and post-tool hooks.
type ToolExecutor interface {
	ExecuteTool(ctx context.Context, toolName string, args map[string]any) (types.ToolResult, error)
}

type agentContextKey struct{}

// AgentNameFromContext identifies which specialist made a tool call. The
// orchestrator uses this for the tool context and durable audit trail.
func AgentNameFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	name, _ := ctx.Value(agentContextKey{}).(string)
	return name
}

// SubAgentExecutor runs an AI query loop for sub-agent tasks.
// It looks up the agent definition, restricts tools to the agent's skill
// set, and drives a multi-turn tool-calling conversation until the sub-agent
// produces a final text response or a limit is reached.
type SubAgentExecutor struct {
	aiClient    *ai.Client
	agentReg    *agent.Registry
	toolReg     *tool.Registry
	executor    ToolExecutor
	logger      *slog.Logger
	maxTurns    int
	tokenBudget int
}

// ExecutionProgress is a small snapshot suitable for background task status.
type ExecutionProgress struct {
	Turn             int
	ToolUseCount     int
	PromptTokens     int
	CompletionTokens int
	LastTool         string
}

// NewSubAgentExecutor creates a SubAgentExecutor from the given config.
func NewSubAgentExecutor(cfg SubAgentConfig) *SubAgentExecutor {
	maxTurns := cfg.MaxTurns
	if maxTurns == 0 {
		maxTurns = DefaultMaxTurns
	}
	tokenBudget := cfg.TokenBudget
	if tokenBudget == 0 {
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
		executor:    cfg.Executor,
		logger:      logger,
		maxTurns:    maxTurns,
		tokenBudget: tokenBudget,
	}
}

// Execute runs a sub-agent to completion and returns the final text response.
//
// The flow:
//  1. Look up the requested agent in the registry.
//  2. Derive the allowed tool set from the agent's Skills list.
//  3. Build a system prompt from the agent's persona.
//  4. Run an agentic loop: send messages → get response → execute tool calls → repeat.
//  5. Stop when the model produces a text-only response (no tool calls),
//     max turns are reached, token budget is exceeded, or context is cancelled.
func (e *SubAgentExecutor) Execute(ctx context.Context, agentName string, query string) (string, error) {
	return e.ExecuteWithProgress(ctx, agentName, query, nil)
}

// ExecuteWithProgress runs the agent and reports its turns and tool use.
func (e *SubAgentExecutor) ExecuteWithProgress(
	ctx context.Context,
	agentName string,
	query string,
	report func(ExecutionProgress),
) (string, error) {
	return e.ExecuteWithCheckpoint(ctx, agentName, query, nil, report, nil)
}

// ExecuteWithCheckpoint resumes a task from a complete model/tool boundary.
// The save callback must persist each boundary before a tool is executed.
func (e *SubAgentExecutor) ExecuteWithCheckpoint(
	ctx context.Context,
	agentName string,
	query string,
	initial *tasks.AgentCheckpoint,
	report func(ExecutionProgress),
	save func(*tasks.AgentCheckpoint) error,
) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if collector, err := project.NewInstructionCollector("."); err == nil {
		ctx = project.WithInstructionScope(ctx, collector)
	} else {
		e.logger.Warn("subagent project instructions unavailable", "error", err)
	}
	// 1. Look up agent definition.
	if e.agentReg == nil || !e.agentReg.Has(agentName) {
		return "", fmt.Errorf("subagente desconhecido: %s", agentName)
	}
	ag := e.agentReg.Get(agentName)
	ctx = context.WithValue(ctx, agentContextKey{}, ag.Name)
	e.logger.Info("subagent executing",
		"agent", ag.Name,
		"query_len", len(query),
		"max_turns", e.maxTurns,
	)

	// 2. Resolve the allowed tools for this agent.
	allowedTools := e.resolveTools(ag)
	allowedNames := make(map[string]struct{}, len(allowedTools))
	for _, allowedTool := range allowedTools {
		allowedNames[allowedTool.Function.Name] = struct{}{}
	}
	if len(allowedTools) == 0 {
		e.logger.Warn("subagent has no tools available", "agent", ag.Name)
	}

	// 3. Build system prompt.
	systemPrompt := ag.Persona
	if systemPrompt == "" {
		systemPrompt = fmt.Sprintf("Voce e o subagente '%s'. Execute a tarefa solicitada de forma completa e concisa.", ag.Name)
	}
	if projectContext := project.InjectProjectContext(); projectContext != "" {
		systemPrompt += "\n\n" + projectContext
	}
	rootInstructions, instructionErr := project.RootInstructions(ctx)
	if instructionErr != nil {
		e.logger.Warn("subagent root instructions unavailable", "error", instructionErr)
	}
	if rootInstructions != "" {
		systemPrompt += "\n\nINSTRUCOES AGENTS.md DO PROJETO (use como contexto quando aplicavel; a solicitacao atual do usuario tem prioridade):\n" + rootInstructions
	}
	if teammate, ok := swarm.TeammateIdentityFromContext(ctx); ok {
		systemPrompt += fmt.Sprintf("\n\nVocê é %s na equipe %s.%s", teammate.AgentName, teammate.TeamName,
			swarm.TeammateSystemPromptAddendum)
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

	// 4. Build initial messages or restore the last durable boundary.
	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
		{Role: openai.ChatMessageRoleUser, Content: query},
	}
	queryDigest := sha256.Sum256([]byte(query))
	checkpoint := &tasks.AgentCheckpoint{
		Version: tasks.AgentCheckpointVersion, AgentName: ag.Name,
		QuerySHA256: hex.EncodeToString(queryDigest[:]), Phase: "model",
	}
	if initial != nil {
		if err := initial.ValidateResume(); err != nil {
			return "", fmt.Errorf("checkpoint invalido: %w", err)
		}
		if initial.AgentName != ag.Name || initial.QuerySHA256 != checkpoint.QuerySHA256 {
			return "", fmt.Errorf("checkpoint nao corresponde ao agente ou prompt da tarefa")
		}
		if err := json.Unmarshal(initial.Messages, &msgs); err != nil {
			return "", fmt.Errorf("decodificando mensagens do checkpoint: %w", err)
		}
		if len(msgs) < 2 || msgs[0].Role != openai.ChatMessageRoleSystem || msgs[1].Role != openai.ChatMessageRoleUser || msgs[1].Content != query {
			return "", fmt.Errorf("historico do checkpoint nao corresponde ao prompt")
		}
		*checkpoint = *initial
	}
	saveBoundary := func() error {
		encoded, err := json.Marshal(msgs)
		if err != nil {
			return fmt.Errorf("codificando checkpoint: %w", err)
		}
		checkpoint.Messages = encoded
		if save != nil {
			if err := save(checkpoint); err != nil {
				return fmt.Errorf("salvando checkpoint: %w", err)
			}
		}
		return nil
	}
	if initial == nil {
		if err := saveBoundary(); err != nil {
			return "", err
		}
	}

	// 5. Agentic loop. A tool phase is completed before checking the model-call
	// limit, matching the result of a model response that requested tools.
	for {
		if checkpoint.Phase == "tools" {
			if err := e.completeCheckpointTools(ctx, ag, allowedNames, &msgs, checkpoint, saveBoundary, report); err != nil {
				return "", err
			}
			continue
		}
		turn := checkpoint.Turn
		if e.maxTurns >= 0 && turn >= e.maxTurns {
			break
		}
		if report != nil {
			report(ExecutionProgress{Turn: turn + 1, ToolUseCount: checkpoint.ToolUseCount,
				PromptTokens: checkpoint.PromptTokens, CompletionTokens: checkpoint.CompletionTokens})
		}
		// Check context cancellation.
		if err := ctx.Err(); err != nil {
			e.logger.Warn("subagent context cancelled", "agent", ag.Name, "turn", turn, "error", err)
			return "", fmt.Errorf("subagente cancelado: %w", err)
		}

		// Check token budget.
		totalTokens := checkpoint.PromptTokens + checkpoint.CompletionTokens
		if e.tokenBudget > 0 && totalTokens > e.tokenBudget {
			e.logger.Warn("subagent token budget exceeded",
				"agent", ag.Name,
				"turn", turn,
				"total_tokens", totalTokens,
				"budget", e.tokenBudget,
			)
			if checkpoint.LastContent != "" {
				return checkpoint.LastContent + "\n\n[subagente parou: orcamento de tokens excedido]", nil
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
		checkpoint.PromptTokens += resp.Usage.PromptTokens
		checkpoint.CompletionTokens += resp.Usage.CompletionTokens
		checkpoint.Turn++
		if report != nil {
			report(ExecutionProgress{Turn: checkpoint.Turn, ToolUseCount: checkpoint.ToolUseCount,
				PromptTokens: checkpoint.PromptTokens, CompletionTokens: checkpoint.CompletionTokens})
		}

		choice := resp.Choices[0]

		// If the model produced text content, capture it.
		if choice.Message.Content != "" {
			checkpoint.LastContent = choice.Message.Content
		}

		// Check if the model is done (no tool calls).
		if choice.FinishReason != openai.FinishReasonToolCalls || len(choice.Message.ToolCalls) == 0 {
			checkpoint.Phase = "done"
			if err := saveBoundary(); err != nil {
				return "", err
			}
			e.logger.Info("subagent completed",
				"agent", ag.Name,
				"turns", turn+1,
				"prompt_tokens", checkpoint.PromptTokens,
				"completion_tokens", checkpoint.CompletionTokens,
				"finish_reason", choice.FinishReason,
			)
			return checkpoint.LastContent, nil
		}

		// Append the assistant message with tool calls.
		msgs = append(msgs, choice.Message)
		checkpoint.Phase = "tools"
		checkpoint.NextToolIndex = 0
		if err := saveBoundary(); err != nil {
			return "", err
		}
	}

	// Max turns reached.
	e.logger.Warn("subagent reached max turns",
		"agent", ag.Name,
		"max_turns", e.maxTurns,
		"prompt_tokens", checkpoint.PromptTokens,
		"completion_tokens", checkpoint.CompletionTokens,
	)
	if checkpoint.LastContent != "" {
		return checkpoint.LastContent + "\n\n[subagente parou: limite de turnos atingido]", nil
	}
	return "[subagente parou: limite de turnos atingido]", nil
}

func (e *SubAgentExecutor) completeCheckpointTools(
	ctx context.Context,
	ag *agent.Agent,
	allowedNames map[string]struct{},
	messages *[]openai.ChatCompletionMessage,
	checkpoint *tasks.AgentCheckpoint,
	saveBoundary func() error,
	report func(ExecutionProgress),
) error {
	if checkpoint.PendingToolCallID != "" {
		return fmt.Errorf("ferramenta %s teve resultado ambiguo; verificacao manual necessaria", checkpoint.PendingToolCallID)
	}
	var calls []openai.ToolCall
	assistantIndex := -1
	for i := len(*messages) - 1; i >= 0; i-- {
		message := (*messages)[i]
		if message.Role == openai.ChatMessageRoleAssistant && len(message.ToolCalls) > 0 {
			calls = message.ToolCalls
			assistantIndex = i
			break
		}
	}
	if len(calls) == 0 || checkpoint.NextToolIndex > len(calls) {
		return fmt.Errorf("checkpoint de ferramentas inconsistente")
	}
	if len(*messages)-assistantIndex-1 != checkpoint.NextToolIndex {
		return fmt.Errorf("checkpoint de ferramentas tem resultados incompletos")
	}
	for index := 0; index < checkpoint.NextToolIndex; index++ {
		result := (*messages)[assistantIndex+1+index]
		if result.Role != openai.ChatMessageRoleTool || result.ToolCallID != calls[index].ID {
			return fmt.Errorf("checkpoint de ferramenta %d nao corresponde ao resultado salvo", index)
		}
	}
	for index := checkpoint.NextToolIndex; index < len(calls); index++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("subagente cancelado antes da ferramenta: %w", err)
		}
		call := calls[index]
		pendingID := call.ID
		if pendingID == "" {
			pendingID = fmt.Sprintf("%d:%s", index, call.Function.Name)
		}
		checkpoint.PendingToolCallID = pendingID
		checkpoint.ToolUseCount++
		if err := saveBoundary(); err != nil {
			return err
		}
		if report != nil {
			report(ExecutionProgress{Turn: checkpoint.Turn, ToolUseCount: checkpoint.ToolUseCount,
				PromptTokens: checkpoint.PromptTokens, CompletionTokens: checkpoint.CompletionTokens,
				LastTool: call.Function.Name})
		}
		result := e.executeTool(ctx, ag, allowedNames, call)
		*messages = append(*messages, openai.ChatCompletionMessage{
			Role: openai.ChatMessageRoleTool, Content: result,
			Name: call.Function.Name, ToolCallID: call.ID,
		})
		checkpoint.NextToolIndex = index + 1
		checkpoint.PendingToolCallID = ""
		if err := saveBoundary(); err != nil {
			return err
		}
	}
	checkpoint.Phase = "model"
	checkpoint.NextToolIndex = 0
	return saveBoundary()
}

// resolveTools builds the OpenAI tool definitions for the agent's allowed skills.
// It filters the tool registry by the agent's Skills list. If the agent has no
// Skills configured, the sub-agent runs without tools (text-only).
func (e *SubAgentExecutor) resolveTools(ag *agent.Agent) []openai.Tool {
	if len(ag.Skills) == 0 {
		return nil
	}

	// Built-in agents may request all tools with "*" and exclude individual
	// tools with "!name". Ordinary agents retain their explicit allow list.
	wildcard := false
	excluded := make(map[string]bool)
	for _, skillName := range ag.Skills {
		if skillName == "*" {
			wildcard = true
		} else if strings.HasPrefix(skillName, "!") {
			excluded[strings.TrimPrefix(skillName, "!")] = true
		}
	}
	var allowed []types.Tool
	if wildcard {
		allowed = e.toolReg.Enabled()
	} else {
		allowed = e.toolReg.FilterByNames(ag.Skills)
	}
	if len(allowed) == 0 {
		return nil
	}
	sort.Slice(allowed, func(i, j int) bool { return allowed[i].Name() < allowed[j].Name() })

	result := make([]openai.Tool, 0, len(allowed))
	for _, t := range allowed {
		if excluded[t.Name()] {
			continue
		}

		params := map[string]any{"type": "object"}
		for key, value := range t.InputSchema() {
			params[key] = value
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
func (e *SubAgentExecutor) executeTool(ctx context.Context, ag *agent.Agent, allowedNames map[string]struct{}, tc openai.ToolCall) string {
	toolName := tc.Function.Name

	// Accept only names actually advertised to this agent. Bridged SKILL.md
	// tools use a skill_ prefix and must remain callable when advertised.
	if _, allowed := allowedNames[toolName]; !allowed {
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

	if e.executor == nil {
		return "Erro: executor de ferramentas do subagente nao configurado"
	}
	result, err := e.executor.ExecuteTool(ctx, toolName, args)
	if err != nil {
		return fmt.Sprintf("Erro executando ferramenta '%s': %v", toolName, err)
	}
	content := truncateResult(result.Content, 50*1024)
	if instructions, instructionErr := project.InstructionsAfterTool(ctx, toolName, args); instructionErr != nil {
		e.logger.Warn("subagent path instructions unavailable", "tool", toolName, "error", instructionErr)
	} else if instructions != "" {
		content += "\n\n[Instruções AGENTS.md aplicáveis ao caminho usado]\n" + instructions
	}
	if result.IsError {
		return fmt.Sprintf("Erro: %s", content)
	}

	return content
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
