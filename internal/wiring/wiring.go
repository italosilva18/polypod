// Package wiring is the central module that connects all polypod subsystems.
// Call WireAll from main.go to replace scattered initialization with a single
// coherent setup that returns all subsystems ready to use.
package wiring

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/ai"
	"github.com/costa/polypod/internal/command"
	"github.com/costa/polypod/internal/commands"
	"github.com/costa/polypod/internal/contextwindow"
	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/hooks"
	"github.com/costa/polypod/internal/memory"
	"github.com/costa/polypod/internal/modes"
	"github.com/costa/polypod/internal/orchestrator"
	"github.com/costa/polypod/internal/permissions"
	"github.com/costa/polypod/internal/skill"
	"github.com/costa/polypod/internal/subagent"
	"github.com/costa/polypod/internal/swarm"
	"github.com/costa/polypod/internal/tasks"
	"github.com/costa/polypod/internal/tasktool"
	"github.com/costa/polypod/internal/tokenbudget"
	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/tracking"
	"github.com/costa/polypod/internal/types"
)

// Config holds the minimal inputs required by WireAll.
type Config struct {
	Logger     *slog.Logger
	WorkingDir string // base directory for file tools
	Model      string // model name for context window limits
	DataDir    string // directory for persistent data (memory, tracking)

	// Optional overrides — if nil, defaults are created.
	MemoryStore memory.Store
	// ConversationStore supplies the immutable journal for scoped history tools.
	ConversationStore *conversation.Store
}

// Systems holds all wired subsystems so main.go can use them.
type Systems struct {
	ToolRegistry       *tool.Registry
	SkillRegistry      *skill.Registry
	Orchestrator       *orchestrator.Orchestrator
	ToolAdapter        *ai.ToolRegistryAdapter
	Permissions        *permissions.Manager
	Hooks              *hooks.Manager
	Modes              *modes.State
	Commands           *command.Registry
	SlashCommands      *commands.Registry
	Agents             *agent.Registry
	AgentRunner        *subagent.AgentRunner
	Memory             memory.Store
	TaskList           *tasktool.TaskList
	TaskRegistry       *tasks.TaskRegistry
	Tracker            *tracking.Tracker
	BudgetTracker      *tokenbudget.BudgetTracker
	Swarm              *swarm.SwarmManager
	ContextWindow      *contextwindow.Manager
	UserPromptHandler  *tool.UserPromptHandler
	AskQuestionChannel *tool.AskQuestionChannel
	PromptCommands     *command.PromptRegistry
	teamRecovery       *teamRecovery
}

// WireAll creates and connects all subsystems. It returns a fully-wired
// Systems struct ready for use by the main CLI loop.
func WireAll(cfg Config) *Systems {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// ---------------------------------------------------------------
	// 1. Tool Registry — register all tool sources
	// ---------------------------------------------------------------
	toolReg := tool.NewRegistry()
	skillReg := skill.NewRegistry()

	// 1a. Register skills as tools (bridges skill.Skill → types.Tool)
	tool.RegisterAllSkills(toolReg, skillReg)

	// 1b. Built-in file/command tools
	workingDir := cfg.WorkingDir
	if workingDir == "" {
		workingDir = "."
	}
	tool.RegisterBuiltinTools(toolReg, workingDir)
	tool.RegisterHistoryRecallTools(toolReg, cfg.ConversationStore)

	// 1c. Subagent runner + AgentTool
	// The AgentTool is initially registered with a placeholder executor.
	// Call WireAIClient after the ai.Client is created to replace it with
	// a real SubAgentExecutor that drives a full agentic loop.
	agentRunner := subagent.NewAgentRunner(logger)
	agentTool := subagent.NewAgentToolWithFunc(agentRunner, func(ctx context.Context, agentName string, query string) (string, error) {
		return "", fmt.Errorf("subagent executor not initialized — call WireAIClient first")
	})
	toolReg.Register(agentTool)

	// 1d. Task tools (TodoWrite, TodoRead, TaskCreate, TaskGet, TaskList, TaskStop, TaskOutput)
	taskList := tasktool.NewTaskList()
	taskRegistry := tasks.NewTaskRegistry()
	if cfg.DataDir != "" {
		if err := taskRegistry.EnablePersistence(filepath.Join(cfg.DataDir, "task-registry.json")); err != nil {
			logger.Warn("background task recovery unavailable", "error", err)
		}
	}
	// Register killers for shell and agent tasks
	taskRegistry.RegisterKiller(tasks.NewLocalShellTaskKiller(logger))
	taskRegistry.RegisterKiller(tasks.NewLocalAgentTaskKiller(logger))
	tool.RegisterTaskTools(toolReg, taskList, taskRegistry, logger)

	// 1e. Remaining utility tools (sleep, send_user_message, tool_search)
	tool.RegisterRemainingTools(toolReg)

	// 1f. Ask-user bidirectional tool (replaces old stubs)
	userPromptHandler := tool.NewUserPromptHandler()
	tool.RegisterAskUserTool(toolReg, userPromptHandler)

	// 1g. Additional bundled tools (structured ask, alt plan mode, worktrees,
	//     multiedit, cron, REPL, MCP auth/resources, task update, synthetic).
	askQuestionChannel := tool.NewAskQuestionChannel()
	tool.RegisterAskUserQuestionTool(toolReg, askQuestionChannel)
	tool.RegisterPlanModeAltTools(toolReg)
	tool.RegisterWorktreeTools(toolReg)
	tool.RegisterMultiEditTool(toolReg)
	tool.RegisterScheduleCronTool(toolReg)
	tool.RegisterREPLTool(toolReg)
	tool.RegisterMCPAuthTool(toolReg)
	tool.RegisterMCPResourceTools(toolReg)
	tool.RegisterTaskUpdateTool(toolReg, taskRegistry)
	tool.RegisterSyntheticOutputTool(toolReg)

	// ---------------------------------------------------------------
	// 2. Core subsystems
	// ---------------------------------------------------------------
	permsMgr := permissions.NewManager(logger)
	hooksMgr := hooks.NewManager(logger)
	modesState := modes.NewState()
	cmdReg := command.NewRegistry()
	command.RegisterBuiltinCommands(cmdReg)
	agentReg := agent.NewRegistry()
	agentReg.Register(agent.DefaultAgent())

	// Memory store
	memStore := cfg.MemoryStore
	if memStore == nil && cfg.DataDir != "" {
		memStore = memory.NewFileStore(cfg.DataDir)
	}

	// ---------------------------------------------------------------
	// 3. Orchestrator — central coordinator for tool execution
	// ---------------------------------------------------------------
	orch := orchestrator.New(orchestrator.Config{
		Logger:    logger,
		Tools:     toolReg,
		Perms:     permsMgr,
		Hooks:     hooksMgr,
		Modes:     modesState,
		Commands:  cmdReg,
		Agents:    agentReg,
		Runner:    agentRunner,
		Memory:    memStore,
		AgentName: "default",
	})

	// 3b. Plan mode tools (enter_plan_mode, exit_plan_mode) — needs
	//     the orchestrator's PlanSimulator and modes.State.
	tool.RegisterPlanModeTools(toolReg, &tool.PlanModeState{
		ModeState:     modesState,
		PlanSimulator: orch.PlanSimulator(),
	})

	// ---------------------------------------------------------------
	// 4. ToolRegistryAdapter — bridges tool.Registry → ai.SkillRegistry
	//    Routes every tool call through the Orchestrator pipeline.
	// ---------------------------------------------------------------
	toolAdapter := ai.NewToolRegistryAdapterWithContext(toolReg, func(ctx context.Context, name string, args map[string]any) (types.ToolResult, error) {
		return orch.ExecuteTool(ctx, name, args)
	})

	// ---------------------------------------------------------------
	// 5. Tracking, Swarm
	// ---------------------------------------------------------------
	dataFile := "usage.json"
	if cfg.DataDir != "" {
		dataFile = cfg.DataDir + "/usage.json"
	}
	tracker := tracking.NewTracker(dataFile, logger)
	tracking.RegisterSkills(skillReg, tracker)

	swarmMgr := swarm.NewSwarmManager(logger)
	if cfg.DataDir != "" {
		// A root service and an unprivileged CLI can share DataDir. Each UID
		// owns private journal files while same-UID processes remain visible.
		journalDir := filepath.Join(cfg.DataDir, fmt.Sprintf("team-journals-uid-%d", os.Geteuid()))
		if err := swarmMgr.EnableJournal(journalDir); err != nil {
			logger.Warn("team journal unavailable", "error", err)
		}
	}

	// ---------------------------------------------------------------
	// 6. Slash commands (project-level, from .polypod/commands/*.md)
	// ---------------------------------------------------------------
	slashCmdReg := commands.NewRegistry()

	// ---------------------------------------------------------------
	// 7. Context Window Manager
	// ---------------------------------------------------------------
	model := cfg.Model
	if model == "" {
		model = "deepseek-chat"
	}
	ctxWinMgr := contextwindow.NewManager(model, logger)
	ctxWinMgr.SetAutoCompactCallback(func() {
		logger.Info("context window auto-compact threshold reached")
	})

	// ---------------------------------------------------------------
	// 8. Session-level BudgetTracker for /cost and /usage commands
	// ---------------------------------------------------------------
	modelLimits := contextwindow.WellKnownLimits(model)
	budgetTr := tokenbudget.NewTracker(tokenbudget.TrackerConfig{
		Limit:            modelLimits.MaxContextTokens,
		CompactThreshold: 0.70,
	})

	return &Systems{
		ToolRegistry:       toolReg,
		SkillRegistry:      skillReg,
		Orchestrator:       orch,
		ToolAdapter:        toolAdapter,
		Permissions:        permsMgr,
		Hooks:              hooksMgr,
		Modes:              modesState,
		Commands:           cmdReg,
		SlashCommands:      slashCmdReg,
		Agents:             agentReg,
		AgentRunner:        agentRunner,
		Memory:             memStore,
		TaskList:           taskList,
		TaskRegistry:       taskRegistry,
		Tracker:            tracker,
		BudgetTracker:      budgetTr,
		Swarm:              swarmMgr,
		ContextWindow:      ctxWinMgr,
		UserPromptHandler:  userPromptHandler,
		AskQuestionChannel: askQuestionChannel,
		PromptCommands:     command.NewPromptRegistry(),
	}
}

// WireAIClient replaces the placeholder agent_run tool with a real
// SubAgentExecutor backed by the given ai.Client. This should be called
// after ai.NewClient, before the system starts handling requests.
func WireAIClient(sys *Systems, aiClient *ai.Client, logger *slog.Logger) {
	maxTurns := subagent.DefaultMaxTurns
	tokenBudget := subagent.DefaultTokenBudge
	if sys.Permissions != nil && sys.Permissions.Mode() == types.ModeBypass {
		maxTurns, tokenBudget = -1, -1
	}
	executor := subagent.NewSubAgentExecutor(subagent.SubAgentConfig{
		AIClient:    aiClient,
		AgentReg:    sys.Agents,
		ToolReg:     sys.ToolRegistry,
		Executor:    sys.Orchestrator,
		Hooks:       sys.Hooks,
		Logger:      logger,
		MaxTurns:    maxTurns,
		TokenBudget: tokenBudget,
	})
	sys.TaskRegistry.SetAgentRunner(func(ctx context.Context, agentName string, query string,
		checkpoint *tasks.AgentCheckpoint, report func(tasks.AgentProgress),
		save func(*tasks.AgentCheckpoint) error) (string, error) {
		var recent []tasks.ToolActivity
		return executor.ExecuteWithCheckpoint(ctx, agentName, query, checkpoint, func(update subagent.ExecutionProgress) {
			progress := tasks.AgentProgress{
				ToolUseCount: update.ToolUseCount,
				TokenCount:   update.PromptTokens + update.CompletionTokens,
				Summary:      fmt.Sprintf("Turno %d", update.Turn),
			}
			modelName := update.Model
			if modelName == "" {
				modelName = aiClient.Model()
			}
			if cost, ok := tracking.MeasuredCost(aiClient.Provider(), modelName, update.PromptTokens, update.CompletionTokens); ok {
				progress.CostUSD = &cost
			}
			if update.LastTool != "" {
				activity := tasks.ToolActivity{ToolName: update.LastTool}
				recent = append(recent, activity)
				if len(recent) > 5 {
					recent = recent[len(recent)-5:]
				}
				progress.LastActivity = &activity
			}
			progress.RecentActivities = append([]tasks.ToolActivity(nil), recent...)
			if report != nil {
				report(progress)
			}
		}, save)
	})

	// Replace the placeholder AgentTool with the real one.
	sys.ToolRegistry.Remove("agent_run")
	agentTool := subagent.NewAgentTool(sys.AgentRunner, executor)
	sys.ToolRegistry.Register(agentTool)
	registerSwarmTools(sys.ToolRegistry, sys.Swarm, sys.Agents, executor)
	sys.teamRecovery = &teamRecovery{manager: sys.Swarm, agents: sys.Agents, executor: executor, logger: logger}

	// Re-bridge the skill registry now that all skills (plugins, MCP, git,
	// vision, voice, dbquery, etc.) have been loaded by main.go. The first
	// call from WireAll only saw the skill builtins; this second pass exposes
	// the rest as "skill_*" tools without colliding with safer builtin
	// equivalents (read_file, list_directory, run_command, search_files).
	beforeCount := sys.ToolRegistry.Count()
	tool.RegisterAllSkills(sys.ToolRegistry, sys.SkillRegistry)
	afterCount := sys.ToolRegistry.Count()
	logger.Info("skills bridged into tool registry",
		"skills_total", len(sys.SkillRegistry.List()),
		"tools_added", afterCount-beforeCount,
		"tools_total", afterCount,
	)

	// Wire the StreamingToolManager for intelligent parallel tool execution.
	// Read-only tools will run concurrently; write tools run serially.
	//
	// CRITICAL: pass sys.Orchestrator so every streaming tool call is routed
	// through the full pipeline (plan simulation, permission enforcement,
	// PreToolUse/PostToolUse hooks, and auto-memory extraction). Without
	// this, the streaming code path bypasses all permission checks.
	// *orchestrator.Orchestrator satisfies ai.OrchestratorExecutor by duck typing.
	stm := ai.NewStreamingToolManager(sys.ToolRegistry, sys.Orchestrator, logger)
	aiClient.SetStreamingToolManager(stm)

	logger.Info("subagent executor wired", "max_turns", subagent.DefaultMaxTurns, "token_budget", subagent.DefaultTokenBudge)
	logger.Info("streaming tool manager wired")
}
