package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
)

const AgentCheckpointVersion = 1

// AgentCheckpoint is a durable boundary between subagent model calls and tool
// calls. Messages contains the serialized chat and tool transcript used by
// the subagent executor. A pending tool call is ambiguous
// after a crash and must never be run again automatically.
type AgentCheckpoint struct {
	Version           int             `json:"version"`
	AgentName         string          `json:"agent_name"`
	QuerySHA256       string          `json:"query_sha256"`
	Messages          json.RawMessage `json:"messages"`
	Phase             string          `json:"phase"`
	Turn              int             `json:"turn"`
	NextToolIndex     int             `json:"next_tool_index,omitempty"`
	PendingToolCallID string          `json:"pending_tool_call_id,omitempty"`
	ToolUseCount      int             `json:"tool_use_count"`
	PromptTokens      int             `json:"prompt_tokens"`
	CompletionTokens  int             `json:"completion_tokens"`
	LastContent       string          `json:"last_content,omitempty"`
}

func cloneAgentCheckpoint(checkpoint *AgentCheckpoint) *AgentCheckpoint {
	if checkpoint == nil {
		return nil
	}
	clone := *checkpoint
	clone.Messages = append(json.RawMessage(nil), checkpoint.Messages...)
	return &clone
}

// ValidateResume rejects unsupported or ambiguous checkpoints before starting
// a new attempt. A completed tool call has both its call and result persisted.
func (checkpoint *AgentCheckpoint) ValidateResume() error {
	if checkpoint == nil {
		return errors.New("tarefa sem checkpoint; reiniciar repetiria a tarefa desde o inicio")
	}
	if checkpoint.Version != AgentCheckpointVersion {
		return fmt.Errorf("versao de checkpoint %d nao suportada", checkpoint.Version)
	}
	if checkpoint.AgentName == "" || checkpoint.QuerySHA256 == "" || len(checkpoint.Messages) == 0 {
		return errors.New("checkpoint incompleto")
	}
	if checkpoint.PendingToolCallID != "" {
		return fmt.Errorf("chamada de ferramenta %s estava em andamento; verifique o efeito no sistema antes de criar uma nova tarefa", checkpoint.PendingToolCallID)
	}
	if checkpoint.Phase != "model" && checkpoint.Phase != "tools" {
		return fmt.Errorf("fase de checkpoint %q nao retomavel", checkpoint.Phase)
	}
	if checkpoint.Turn < 0 || checkpoint.NextToolIndex < 0 {
		return errors.New("contadores de checkpoint invalidos")
	}
	return nil
}

// SaveAgentCheckpoint persists the boundary before any tool side effect.
// Unlike ordinary progress updates, it returns a persistence failure to the
// executor, which must stop instead of running an unjournaled tool.
func (r *TaskRegistry) SaveAgentCheckpoint(id string, checkpoint *AgentCheckpoint) error {
	if checkpoint == nil {
		return errors.New("checkpoint ausente")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.tasks[id]
	if !ok {
		return fmt.Errorf("tarefa %s nao encontrada", id)
	}
	if state.Status != StatusRunning {
		return fmt.Errorf("tarefa %s nao esta em execucao", id)
	}
	previous := state.Checkpoint
	state.Checkpoint = cloneAgentCheckpoint(checkpoint)
	if r.persistPath == "" {
		return nil
	}
	if err := r.saveLocked(); err != nil {
		state.Checkpoint = previous
		r.persistErr = err
		return fmt.Errorf("persistindo checkpoint da tarefa %s: %w", id, err)
	}
	r.persistErr = nil
	return nil
}
