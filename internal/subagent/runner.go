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
	r.mu.Unlock()

	// Run in background
	go func() {
		defer close(task.Done)
		defer cancel()

		r.mu.Lock()
		r.tasks[state.ID].State.Status = types.TaskStatusRunning
		r.mu.Unlock()

		result, err := executor(taskCtx, cfg.Query)

		r.mu.Lock()
		now := time.Now()
		if err != nil {
			r.tasks[state.ID].State.Status = types.TaskStatusFailed
			r.tasks[state.ID].Error = err
		} else {
			r.tasks[state.ID].State.Status = types.TaskStatusCompleted
			r.tasks[state.ID].Result = result
		}
		r.tasks[state.ID].State.EndTime = &now
		r.mu.Unlock()

		if err != nil {
			r.logger.Error("subagent failed", "task_id", state.ID, "error", err)
		} else {
			r.logger.Debug("subagent completed", "task_id", state.ID)
		}
	}()

	r.logger.Info("subagent spawned", "task_id", state.ID, "agent", cfg.AgentName)
	return task, nil
}

// Get returns a task by ID.
func (r *AgentRunner) Get(id types.TaskID) (*AgentTask, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tasks[id]
	return t, ok
}

// List returns all tasks.
func (r *AgentRunner) List() []*AgentTask {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*AgentTask, 0, len(r.tasks))
	for _, t := range r.tasks {
		result = append(result, t)
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
			result = append(result, t)
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
	task, ok := r.Get(id)
	if !ok {
		return nil, fmt.Errorf("tarefa %s nao encontrada", id)
	}
	select {
	case <-task.Done:
		return task, nil
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
// The tool is marked as read-only because it delegates file-modifying
// operations to the sub-agent's own tool calls rather than performing them
// directly.
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
		IsConcurrencyFn: func(map[string]any) bool { return true },
		IsReadOnlyFn:    func(map[string]any) bool { return true },
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

			task, err := runner.Spawn(ctx.Context, SpawnConfig{
				AgentName: agentName,
				Query:     prompt,
				Timeout:   timeout,
			}, func(runCtx context.Context, query string) (string, error) {
				return executor.Execute(runCtx, agentName, query)
			})
			if err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Erro ao iniciar subagente: %v", err), IsError: true}, nil
			}

			// Wait for completion.
			<-task.Done
			if task.Error != nil {
				return types.ToolResult{Content: fmt.Sprintf("Subagente '%s' falhou: %v", agentName, task.Error), IsError: true}, nil
			}
			return types.ToolResult{Content: task.Result}, nil
		},
	})
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
		IsConcurrencyFn: func(map[string]any) bool { return true },
		IsReadOnlyFn:    func(map[string]any) bool { return true },
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

			task, err := runner.Spawn(ctx.Context, SpawnConfig{
				AgentName: agentName,
				Query:     prompt,
				Timeout:   timeout,
			}, func(runCtx context.Context, query string) (string, error) {
				return executor(runCtx, agentName, query)
			})
			if err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Erro ao iniciar subagente: %v", err), IsError: true}, nil
			}

			// Wait for completion.
			<-task.Done
			if task.Error != nil {
				return types.ToolResult{Content: fmt.Sprintf("Subagente falhou: %v", task.Error), IsError: true}, nil
			}
			return types.ToolResult{Content: task.Result}, nil
		},
	})
}
