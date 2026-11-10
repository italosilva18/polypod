package tasks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

const AgentCheckpointVersion = 1

const (
	// AgentCheckpointPhaseModel is a boundary before a model call. No tool
	// from this boundary has started.
	AgentCheckpointPhaseModel = "model"
	// AgentCheckpointPhaseTools is a boundary between completed tool results.
	// The next tool has not started unless PendingToolCallID is set.
	AgentCheckpointPhaseTools = "tools"
	// AgentCheckpointPhaseDone means the executor finished this turn. It is
	// not a reason to call the model or tools again.
	AgentCheckpointPhaseDone = "done"
)

// AgentCheckpoint is a durable boundary between subagent model calls and tool
// calls. Messages contains the serialized chat and tool transcript used by
// the subagent executor. A pending tool call is ambiguous
// after a crash and must never be run again automatically. A prompt kept
// outside Messages is not a resume token.
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
	if checkpoint.Phase != AgentCheckpointPhaseModel && checkpoint.Phase != AgentCheckpointPhaseTools {
		return fmt.Errorf("fase de checkpoint %q nao retomavel", checkpoint.Phase)
	}
	if checkpoint.Turn < 0 || checkpoint.NextToolIndex < 0 {
		return errors.New("contadores de checkpoint invalidos")
	}
	return nil
}

// QueryDigest is the canonical hash of the user prompt stored in a checkpoint.
func QueryDigest(query string) string {
	sum := sha256.Sum256([]byte(query))
	return hex.EncodeToString(sum[:])
}

// ResumeQuery returns the user prompt embedded in Messages. Callers must not
// replace it with a journal or task prompt. ValidateResume is separate: a
// done or pending checkpoint can still be decoded without becoming executable.
func (checkpoint *AgentCheckpoint) ResumeQuery() (string, error) {
	if checkpoint == nil {
		return "", errors.New("checkpoint ausente")
	}
	var messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(checkpoint.Messages, &messages); err != nil {
		return "", fmt.Errorf("decodificando mensagens do checkpoint: %w", err)
	}
	if len(messages) < 2 || messages[0].Role != "system" || messages[1].Role != "user" {
		return "", errors.New("checkpoint sem mensagem de usuario")
	}
	if checkpoint.QuerySHA256 == "" || QueryDigest(messages[1].Content) != checkpoint.QuerySHA256 {
		return "", errors.New("hash do prompt nao corresponde ao checkpoint")
	}
	return messages[1].Content, nil
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
