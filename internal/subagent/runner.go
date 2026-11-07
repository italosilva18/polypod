package subagent

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/costa/polypod/internal/constants"
	"github.com/costa/polypod/internal/types"
)

// AgentRunner executes subagent tasks in isolated contexts.
type AgentRunner struct {
	mu     sync.RWMutex
	tasks  map[types.TaskID]*AgentTask
	logger *slog.Logger
}

// AgentTask represents a running subagent.
type AgentTask struct {
	State     types.TaskStateBase
	AgentName string
	Query     string
	Result    string
	Error     error
	Cancel    context.CancelFunc
	Done      chan struct{}
}

// NewAgentRunner creates a new subagent runner.
func NewAgentRunner(logger *slog.Logger) *AgentRunner {
	return &AgentRunner{
		tasks:  make(map[types.TaskID]*AgentTask),
		logger: logger,
	}
}

// SpawnConfig configures a subagent spawn.
type SpawnConfig struct {
	AgentName string
	Query     string
	Timeout   time.Duration
	MaxTokens int
	ToolUseID string
}

// Spawn starts a new subagent task.
func (r *AgentRunner) Spawn(ctx context.Context, cfg SpawnConfig, executor func(ctx context.Context, query string) (string, error)) (*AgentTask, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * time.Second
	}

	taskCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	state := types.NewTaskState(types.TaskTypeLocalAgent, cfg.Query, "")
	state.ToolUseID = cfg.ToolUseID

	task := &AgentTask{
		State:     state,
		AgentName: cfg.AgentName,
		Query:     cfg.Query,
		Cancel:    cancel,
		Done:      make(chan struct{}),
	}

	r.mu.Lock()
	r.tasks[state.ID] = task
	initial := snapshotTask(task)
	r.mu.Unlock()

	// Run in background
	go func() {
		defer close(task.Done)
		defer cancel()

		r.mu.Lock()
		if !task.State.Status.IsTerminal() {
			task.State.Status = types.TaskStatusRunning
		}
		r.mu.Unlock()

		var result string
		var err error
		if taskCtx.Err() != nil {
			err = taskCtx.Err()
		} else {
			result, err = executor(taskCtx, cfg.Query)
		}

		r.mu.Lock()
		now := time.Now()
		if task.State.Status != types.TaskStatusKilled {
			if err != nil {
				task.State.Status = types.TaskStatusFailed
				task.Error = err
			} else {
				task.State.Status = types.TaskStatusCompleted
				task.Result = result
			}
			task.State.EndTime = &now
		}
		r.mu.Unlock()

		if err != nil {
			r.logger.Error("subagent failed", "task_id", state.ID, "error", err)
		} else {
			r.logger.Debug("subagent completed", "task_id", state.ID)
		}
	}()

	r.logger.Info("subagent spawned", "task_id", state.ID, "agent", cfg.AgentName)
	return initial, nil
}

// snapshotTask must be called while r.mu protects the source task. Done and
// Cancel are shared handles; task state and EndTime are independent copies.
func snapshotTask(task *AgentTask) *AgentTask {
	copy := *task
	if task.State.EndTime != nil {
		end := *task.State.EndTime
		copy.State.EndTime = &end
	}
	return &copy
}

// Get returns a snapshot of a task by ID.
func (r *AgentRunner) Get(id types.TaskID) (*AgentTask, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tasks[id]
	if !ok {
		return nil, false
	}
	return snapshotTask(t), true
}

// List returns task snapshots.
func (r *AgentRunner) List() []*AgentTask {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*AgentTask, 0, len(r.tasks))
	for _, t := range r.tasks {
		result = append(result, snapshotTask(t))
	}
	return result
}

// ListByStatus returns tasks filtered by status.
func (r *AgentRunner) ListByStatus(status types.TaskStatus) []*AgentTask {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []*AgentTask
	for _, t := range r.tasks {
		if t.State.Status == status {
			result = append(result, snapshotTask(t))
		}
	}
	return result
}

// Running returns all currently running tasks.
func (r *AgentRunner) Running() []*AgentTask {
	return r.ListByStatus(types.TaskStatusRunning)
}

// Kill stops a running task.
func (r *AgentRunner) Kill(id types.TaskID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	task, ok := r.tasks[id]
	if !ok {
		return fmt.Errorf("tarefa %s nao encontrada", id)
	}
	if task.State.Status.IsTerminal() {
		return fmt.Errorf("tarefa %s ja finalizada: %s", id, task.State.Status)
	}
	task.Cancel()
	task.State.Status = types.TaskStatusKilled
	now := time.Now()
	task.State.EndTime = &now
	return nil
}

// Wait blocks until a task completes or context is cancelled.
func (r *AgentRunner) Wait(ctx context.Context, id types.TaskID) (*AgentTask, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.RLock()
	task, ok := r.tasks[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("tarefa %s nao encontrada", id)
	}
	select {
	case <-task.Done:
		r.mu.RLock()
		result := snapshotTask(task)
		r.mu.RUnlock()
		return result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Cleanup removes completed/failed/killed tasks older than maxAge.
func (r *AgentRunner) Cleanup(maxAge time.Duration) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	removed := 0
	for id, t := range r.tasks {
		if t.State.Status.IsTerminal() && t.State.EndTime != nil {
			if now.Sub(*t.State.EndTime) > maxAge {
				delete(r.tasks, id)
				removed++
			}
		}
	}
	return removed
}

// Stats returns summary statistics.
func (r *AgentRunner) Stats() map[string]int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	stats := map[string]int{
		"total":     len(r.tasks),
		"running":   0,
		"completed": 0,
		"failed":    0,
		"killed":    0,
	}
	for _, t := range r.tasks {
		switch t.State.Status {
		case types.TaskStatusRunning:
			stats["running"]++
		case types.TaskStatusCompleted:
			stats["completed"]++
		case types.TaskStatusFailed:
			stats["failed"]++
		case types.TaskStatusKilled:
			stats["killed"]++
		}
	}
	return stats
}

// --- Tool integration: AgentTool ---

// NewAgentTool creates a Tool that spawns sub-agents using a SubAgentExecutor.
//
// The tool accepts two parameters:
//   - prompt (required): the task for the sub-agent to execute.
//   - agent_name (optional, default "task"): the agent persona to use.
//   - timeout (optional, default 120): timeout in seconds.
//
// Read-only specialist roles may run concurrently. Roles with write tools
// are serialized with other write operations.
func NewAgentTool(runner *AgentRunner, executor *SubAgentExecutor) types.Tool {
	return types.BuildTool(types.ToolDef{
		NameFn: func() string { return constants.ToolAgentRun },
		DescriptionFn: func() string {
			return "Executar uma tarefa em um subagente independente. " +
				"Use para delegar subtarefas de pesquisa, analise ou implementacao a um agente especializado. " +
				"O agente 'task' e somente-leitura (pesquisa); 'coder' tem acesso total."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{
						"type":        "string",
						"description": "A tarefa para o subagente executar. Seja especifico e inclua todo o contexto necessario.",
					},
					"agent_name": map[string]any{
						"type":        "string",
						"description": "Nome do agente a usar. Opcoes: 'task' (pesquisa somente-leitura), 'coder' (acesso total), 'reviewer' (revisao de codigo), 'architect' (design). Default: task",
						"enum":        []string{"task", "coder", "reviewer", "architect", "devops", "default"},
					},
					"timeout": map[string]any{
						"type":        "integer",
						"description": "Timeout em segundos (default: 120)",
					},
				},
				"required": []string{"prompt"},
			}
		},
		IsConcurrencyFn: executor.agentRunReadOnly,
		IsReadOnlyFn:    executor.agentRunReadOnly,
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			prompt, _ := args["prompt"].(string)
			if prompt == "" {
				return types.ToolResult{Content: "Erro: 'prompt' e obrigatorio", IsError: true}, nil
			}

			agentName, _ := args["agent_name"].(string)
			if agentName == "" {
				agentName = "task"
			}

			timeout := DefaultTimeout
			if t, ok := args["timeout"].(float64); ok && t > 0 {
				timeout = time.Duration(t) * time.Second
			}

			runCtx := ctx.Context
			if runCtx == nil {
				runCtx = context.Background()
			}
			task, err := runner.Spawn(runCtx, SpawnConfig{
				AgentName: agentName,
				Query:     prompt,
				Timeout:   timeout,
			}, func(runCtx context.Context, query string) (string, error) {
				return executor.Execute(runCtx, agentName, query)
			})
			if err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Erro ao iniciar subagente: %v", err), IsError: true}, nil
			}

			// Wait for a final snapshot; Spawn returns only the initial state.
			completed, err := runner.Wait(runCtx, task.State.ID)
			if err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Subagente '%s' interrompido: %v", agentName, err), IsError: true}, nil
			}
			if completed.State.Status == types.TaskStatusKilled {
				return types.ToolResult{Content: fmt.Sprintf("Subagente '%s' interrompido", agentName), IsError: true}, nil
			}
			if completed.Error != nil {
				return types.ToolResult{Content: fmt.Sprintf("Subagente '%s' falhou: %v", agentName, completed.Error), IsError: true}, nil
			}
			return types.ToolResult{Content: completed.Result}, nil
		},
	})
}

// isReadOnlySubAgentSkill identifies tools safe for parallel research and
// review. Newly added skills keep the role serial until classified here.
func isReadOnlySubAgentSkill(name string) bool {
	switch name {
	case "read_file", "read_file_dir", "glob", "grep", "web_search", "web_fetch",
		"tor_fetch", "document_extract", "image_analyze", "polypod_evidence_research",
		"polypod_document_analysis", "polypod_onion_research":
		return true
	default:
		return false
	}
}

// agentRunReadOnly checks the current agent definition so a self-modified
// research role cannot be treated as read-only after gaining write tools.
func (e *SubAgentExecutor) agentRunReadOnly(args map[string]any) bool {
	name, _ := args["agent_name"].(string)
	switch name {
	case "", "task", "architect", "reviewer":
	default:
		return false
	}
	if name == "" {
		name = "task"
	}
	if e == nil || e.agentReg == nil || !e.agentReg.Has(name) {
		return false
	}
	for _, skillName := range e.agentReg.Get(name).Skills {
		if !isReadOnlySubAgentSkill(skillName) {
			return false
		}
	}
	return true
}

// NewAgentToolWithFunc creates a Tool that spawns sub-agents using a raw
// executor function. This is the legacy constructor for backward compatibility
// when a full SubAgentExecutor is not available.
func NewAgentToolWithFunc(runner *AgentRunner, executor func(ctx context.Context, agentName string, query string) (string, error)) types.Tool {
	return types.BuildTool(types.ToolDef{
		NameFn:        func() string { return constants.ToolAgentRun },
		DescriptionFn: func() string { return "Executar uma tarefa em um subagente independente" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt":     map[string]any{"type": "string", "description": "Tarefa para o subagente executar"},
					"agent_name": map[string]any{"type": "string", "description": "Nome do agente (default: task)"},
					"timeout":    map[string]any{"type": "integer", "description": "Timeout em segundos (default: 120)"},
				},
				"required": []string{"prompt"},
			}
		},
		IsConcurrencyFn: func(map[string]any) bool { return false },
		IsReadOnlyFn:    func(map[string]any) bool { return false },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			prompt, _ := args["prompt"].(string)
			agentName, _ := args["agent_name"].(string)
			if agentName == "" {
				agentName = "task"
			}
			timeout := DefaultTimeout
			if t, ok := args["timeout"].(float64); ok && t > 0 {
				timeout = time.Duration(t) * time.Second
			}

			runCtx := ctx.Context
			if runCtx == nil {
				runCtx = context.Background()
			}
			task, err := runner.Spawn(runCtx, SpawnConfig{
				AgentName: agentName,
				Query:     prompt,
				Timeout:   timeout,
			}, func(runCtx context.Context, query string) (string, error) {
				return executor(runCtx, agentName, query)
			})
			if err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Erro ao iniciar subagente: %v", err), IsError: true}, nil
			}

			// Wait for a final snapshot; Spawn returns only the initial state.
			completed, err := runner.Wait(runCtx, task.State.ID)
			if err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Subagente interrompido: %v", err), IsError: true}, nil
			}
			if completed.State.Status == types.TaskStatusKilled {
				return types.ToolResult{Content: "Subagente interrompido", IsError: true}, nil
			}
			if completed.Error != nil {
				return types.ToolResult{Content: fmt.Sprintf("Subagente falhou: %v", completed.Error), IsError: true}, nil
			}
			return types.ToolResult{Content: completed.Result}, nil
		},
	})
}
