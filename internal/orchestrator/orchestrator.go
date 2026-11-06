package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/command"
	"github.com/costa/polypod/internal/hooks"
	"github.com/costa/polypod/internal/memory"
	"github.com/costa/polypod/internal/modes"
	"github.com/costa/polypod/internal/permissions"
	"github.com/costa/polypod/internal/subagent"
	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/types"
)

// Orchestrator is the central coordinator for all subsystems.
// It manages the full lifecycle: validate → permission → hooks → execute.
type Orchestrator struct {
	mu       sync.RWMutex
	tools    *tool.Registry
	perms    *permissions.Manager
	enforcer *permissions.Enforcer
	hooks    *hooks.Manager
	modes    *modes.State
	commands *command.Registry
	agents   *agent.Registry
	runner   *subagent.AgentRunner
	memory   memory.Store
	logger   *slog.Logger

	// Current session
	sessionID types.SessionID
	agentName string

	// Plan simulator for plan mode
	planSimulator *modes.PlanSimulator
}

// Config holds orchestrator configuration.
type Config struct {
	Logger    *slog.Logger
	Tools     *tool.Registry
	Perms     *permissions.Manager
	Hooks     *hooks.Manager
	Modes     *modes.State
	Commands  *command.Registry
	Agents    *agent.Registry
	Runner    *subagent.AgentRunner
	Memory    memory.Store
	SessionID types.SessionID
	AgentName string
}

// New creates a new orchestrator.
func New(cfg Config) *Orchestrator {
	if cfg.SessionID.IsZero() {
		cfg.SessionID = types.NewSessionID()
	}
	if cfg.AgentName == "" {
		cfg.AgentName = "default"
	}
	if cfg.Commands == nil {
		cfg.Commands = command.NewRegistry()
		command.RegisterBuiltinCommands(cfg.Commands)
	}
	if cfg.Modes == nil {
		cfg.Modes = modes.NewState()
	}
	if cfg.Agents == nil {
		cfg.Agents = agent.NewRegistry()
	}
	if cfg.Runner == nil {
		cfg.Runner = subagent.NewAgentRunner(cfg.Logger)
	}

	return &Orchestrator{
		tools:         cfg.Tools,
		perms:         cfg.Perms,
		hooks:         cfg.Hooks,
		modes:         cfg.Modes,
		commands:      cfg.Commands,
		agents:        cfg.Agents,
		runner:        cfg.Runner,
		memory:        cfg.Memory,
		logger:        cfg.Logger,
		sessionID:     cfg.SessionID,
		agentName:     cfg.AgentName,
		planSimulator: modes.NewPlanSimulator(cfg.Logger),
	}
}

// ExecuteTool runs a tool through the full pipeline:
// 1. Check if in plan mode (simulate destructive tools)
// 2. Validate input
// 3. Check permissions (rules + mode)
// 4. Fire PreToolUse hooks
// 5. Execute
// 6. Fire PostToolUse hooks
// 7. Auto-extract memories
func (o *Orchestrator) ExecuteTool(ctx context.Context, toolName string, args map[string]any) (types.ToolResult, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	tCtx := o.toolContext(ctx)

	// 1. Plan mode: simulate destructive tools
	if o.modes.Current == modes.ModePlan {
		if o.planSimulator != nil {
			if result, intercepted := o.planSimulator.SimulateToolCall(toolName, args); intercepted {
				return result, nil
			}
		}
	}

	// 2. Get tool
	t, ok := o.tools.Get(toolName)
	if !ok {
		return types.ToolResult{
			Content: fmt.Sprintf("ferramenta desconhecida: %s", toolName),
			IsError: true,
		}, nil
	}

	// 3. Validate input
	vr := t.ValidateInput(args)
	if !vr.Valid {
		return types.ToolResult{
			Content: fmt.Sprintf("validacao falhou: %s", vr.Message),
			IsError: true,
		}, nil
	}

	// 4. Check permissions (enforcer takes priority over legacy manager)
	if o.enforcer != nil {
		decision, err := o.enforcer.CheckToolCall(ctx, toolName, args)
		if err != nil {
			o.logger.Error("enforcer error", "tool", toolName, "error", err)
			return types.ToolResult{
				Content: fmt.Sprintf("erro de permissao: %v", err),
				IsError: true,
			}, nil
		}
		if decision.Behavior == types.PermissionDeny {
			return types.ToolResult{
				Content: fmt.Sprintf("permissao negada para %s: %s", toolName, decision.Message),
				IsError: true,
			}, nil
		}
		if decision.Behavior == types.PermissionAsk {
			// "ask" without resolution means no handler was available — deny
			return types.ToolResult{
				Content: fmt.Sprintf("permissao requerida para %s: %s", toolName, decision.Message),
				IsError: true,
			}, nil
		}
	} else if o.perms != nil {
		// Legacy fallback: simple name-based check
		decision := o.perms.Check(toolName)
		if decision == "deny" {
			return types.ToolResult{
				Content: fmt.Sprintf("permissao negada para: %s", toolName),
				IsError: true,
			}, nil
		}
	}

	// 5. Fire PreToolUse hooks
	if o.hooks != nil {
		permDecision := o.hooks.CheckToolPermission(ctx, toolName, args)
		if permDecision.Behavior == types.PermissionDeny {
			return types.ToolResult{
				Content: fmt.Sprintf("hook negou: %s", permDecision.Message),
				IsError: true,
			}, nil
		}
	}

	// 6. Execute
	result, err := t.Execute(tCtx, args)
	if err != nil {
		o.logger.Error("tool execution error", "tool", toolName, "error", err)
		return types.ToolResult{
			Content: fmt.Sprintf("erro na execucao: %v", err),
			IsError: true,
		}, nil
	}

	// 7. Fire PostToolUse hooks
	if o.hooks != nil {
		o.hooks.Fire(ctx, hooks.HookPayload{
			Event:      hooks.EventPostToolUse,
			ToolName:   toolName,
			ToolArgs:   args,
			ToolResult: result.Content,
			SessionID:  o.sessionID.String(),
		})
	}

	// 8. Auto-extract memories from tool results
	if o.memory != nil && !result.IsError {
		o.autoExtractMemory(toolName, args, result)
	}

	return result, nil
}

// ExecuteParallel runs multiple tools concurrently.
func (o *Orchestrator) ExecuteParallel(ctx context.Context, calls []tool.ToolCall) []tool.ToolCallResult {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.tools.ExecuteParallel(calls, o.toolContext(ctx))
}

// HandleCommand processes a slash command.
func (o *Orchestrator) HandleCommand(name string, args []string) (*command.CommandResult, error) {
	cmd, ok := o.commands.Get(name)
	if !ok {
		return nil, fmt.Errorf("comando desconhecido: /%s", name)
	}

	ctx := command.CommandContext{
		SessionID:  o.sessionID.String(),
		WorkingDir: ".",
	}

	if cmd.Execute != nil {
		return cmd.Execute(args, ctx)
	}
	return nil, fmt.Errorf("comando /%s sem handler Execute", name)
}

// SwitchAgent changes the active agent.
func (o *Orchestrator) SwitchAgent(name string) error {
	if !o.agents.Has(name) {
		return fmt.Errorf("agente '%s' nao encontrado", name)
	}
	o.mu.Lock()
	o.agentName = name
	o.mu.Unlock()
	o.logger.Info("agente trocado", "agent", name)
	return nil
}

// SetMode changes the operating mode.
func (o *Orchestrator) SetMode(mode modes.Mode) {
	o.modes.SetMode(string(mode))
}

// GetSessionID returns the current session ID.
func (o *Orchestrator) GetSessionID() types.SessionID {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.sessionID
}

// toolContext builds a ToolContext for tool execution.
func (o *Orchestrator) toolContext(ctx context.Context) types.ToolContext {
	return types.ToolContext{
		Context:    ctx,
		SessionID:  o.sessionID,
		AgentID:    o.agents.AgentID(o.agentName),
		WorkingDir: ".",
	}
}

// autoExtractMemory extracts memories from tool interactions.
func (o *Orchestrator) autoExtractMemory(toolName string, args map[string]any, result types.ToolResult) {
	// Extract decisions from write/edit operations
	if toolName == "write_file" || toolName == "edit_file" {
		if content, ok := args["content"].(string); ok {
			if mem := memory.ExtractDecision(content); mem != nil {
				o.memory.SaveWithMeta(context.Background(), *mem)
			}
		}
	}
}

// SetEnforcer sets the permission enforcer for the orchestrator.
// When set, the enforcer replaces the legacy permissions.Manager for tool call checks.
func (o *Orchestrator) SetEnforcer(e *permissions.Enforcer) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.enforcer = e
}

// Enforcer returns the current permission enforcer, or nil if not set.
func (o *Orchestrator) Enforcer() *permissions.Enforcer {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.enforcer
}

// Stats returns orchestrator statistics.
func (o *Orchestrator) Stats() map[string]any {
	o.mu.RLock()
	defer o.mu.RUnlock()
	stats := map[string]any{
		"session_id": o.sessionID.String(),
		"agent":      o.agentName,
		"mode":       string(o.modes.Current),
		"tools":      o.tools.Count(),
		"commands":   o.commands.Count(),
		"agents":     o.agents.Count(),
		"subagents":  o.runner.Stats(),
	}
	if o.enforcer != nil {
		stats["enforcer"] = o.enforcer.Stats()
	}
	return stats
}

// PlanSimulator returns the plan simulator used for plan mode.
func (o *Orchestrator) PlanSimulator() *modes.PlanSimulator {
	return o.planSimulator
}

// Cleanup performs maintenance tasks.
func (o *Orchestrator) Cleanup() {
	// Clean up finished subagents older than 5 minutes
	removed := o.runner.Cleanup(5 * time.Minute)
	if removed > 0 {
		o.logger.Debug("subagent cleanup", "removed", removed)
	}
}
