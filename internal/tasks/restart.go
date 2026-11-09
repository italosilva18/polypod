package tasks

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// Restart starts a new task with the same input after a prior run has ended.
// A new ID preserves the original task's status, output and audit trail.
func (r *TaskRegistry) Restart(ctx context.Context, id string, logger *slog.Logger) (*TaskState, error) {
	r.mu.RLock()
	state, ok := r.tasks[id]
	if !ok {
		r.mu.RUnlock()
		return nil, fmt.Errorf("tarefa %s nao encontrada", id)
	}
	status := state.Status
	taskType := state.Type
	description := state.Description
	input := state.input
	agentType := state.AgentName
	attempt := state.Attempt
	checkpoint := cloneAgentCheckpoint(state.Checkpoint)
	toolUseCount := -1
	if state.Progress != nil {
		toolUseCount = state.Progress.ToolUseCount
	}
	failure := state.Error
	r.mu.RUnlock()
	if !status.IsTerminal() {
		return nil, fmt.Errorf("tarefa %s ainda esta em execucao; encerre-a antes de reiniciar", id)
	}
	if input == "" {
		return nil, fmt.Errorf("tarefa %s nao possui dados de reinicio", id)
	}
	if logger == nil {
		logger = slog.Default()
	}
	switch taskType {
	case TaskTypeShell:
		handle, err := SpawnShellTask(r, input, description, "", logger)
		if err != nil {
			return nil, err
		}
		r.Update(handle.TaskID, func(started *TaskState) {
			started.RestartOf = id
			started.Attempt = attempt + 1
		})
		started, ok := r.Get(handle.TaskID)
		if !ok {
			return nil, fmt.Errorf("tarefa reiniciada %s nao encontrada no registro", handle.TaskID)
		}
		return started, nil
	case TaskTypeAgent:
		if status == StatusCompleted {
			return nil, fmt.Errorf("tarefa %s ja foi concluida; crie uma nova tarefa para repetir o trabalho", id)
		}
		if checkpoint == nil && !legacyAgentReplaySafe(status, toolUseCount, failure) {
			return nil, fmt.Errorf("tarefa %s nao tem checkpoint retomavel; repetir o prompt pode refazer acoes externas. Revise o resultado e crie uma nova tarefa se desejar recomecar", id)
		}
		if checkpoint != nil {
			if err := checkpoint.ValidateResume(); err != nil {
				return nil, fmt.Errorf("tarefa %s nao pode ser retomada: %w", id, err)
			}
		}
		started, err := spawnAgentTask(ctx, r, description, input, agentType, checkpoint, logger)
		if err != nil {
			return nil, err
		}
		r.Update(started.ID, func(state *TaskState) {
			state.RestartOf = id
			state.Attempt = attempt + 1
		})
		resumed, ok := r.Get(started.ID)
		if !ok {
			return nil, fmt.Errorf("tarefa reiniciada %s nao encontrada no registro", started.ID)
		}
		return resumed, nil
	default:
		return nil, fmt.Errorf("reinicio indisponivel para o tipo %s", taskType)
	}
}

func legacyAgentReplaySafe(status TaskStatus, toolUseCount int, failure string) bool {
	if status != StatusFailed || toolUseCount != 0 {
		return false
	}
	return strings.HasPrefix(failure, "subagent AI call failed (turn 0)") ||
		strings.HasPrefix(failure, "subagent received empty response (turn 0)")
}
