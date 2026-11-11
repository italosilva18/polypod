package turncheckpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const AIVersion = 1

const (
	AIPhaseModel     = "model"
	AIPhaseTools     = "tools"
	AIPhaseDone      = "done"
	AIPhaseAbandoned = "abandoned"
)

// RecoveryDiscardNotePrefix identifies the persisted resolution entry that
// must exist before an abandoned checkpoint can release a new CLI turn.
const RecoveryDiscardNotePrefix = "[Polypod: turno abandonado]"

// AICheckpoint is the complete provider transcript at a model/tool boundary.
// A tool is marked pending on disk before it starts. A crash while pending is
// ambiguous even if no result was observed, so it cannot be replayed.
type AICheckpoint struct {
	// Revision is the on-disk content hash loaded or saved by this process.
	// It is intentionally excluded from the JSON document it versions.
	Revision              string          `json:"-"`
	Version               int             `json:"version"`
	SessionID             string          `json:"session_id"`
	TurnID                string          `json:"turn_id"`
	Format                string          `json:"format"`
	Model                 string          `json:"model"`
	AgentName             string          `json:"agent_name,omitempty"`
	WorkingDir            string          `json:"working_dir,omitempty"`
	SkillNames            []string        `json:"skill_names,omitempty"`
	ToolDefinitionsSHA256 string          `json:"tool_definitions_sha256,omitempty"`
	Messages              json.RawMessage `json:"messages"`
	DeferredImages        json.RawMessage `json:"deferred_images,omitempty"`
	Phase                 string          `json:"phase"`
	Iteration             int             `json:"iteration"`
	NextToolIndex         int             `json:"next_tool_index,omitempty"`
	PendingToolCallID     string          `json:"pending_tool_call_id,omitempty"`
	PromptTokens          int             `json:"prompt_tokens,omitempty"`
	CompletionTokens      int             `json:"completion_tokens,omitempty"`
	FinalContent          string          `json:"final_content,omitempty"`
	DiscardedAt           time.Time       `json:"discarded_at,omitempty"`
	DiscardReason         string          `json:"discard_reason,omitempty"`
	UpdatedAt             time.Time       `json:"updated_at"`
}

func (c *AICheckpoint) Validate() error {
	if c == nil || c.Version != AIVersion || c.SessionID == "" || c.TurnID == "" || c.Model == "" {
		return errors.New("checkpoint de IA ausente ou incompleto")
	}
	if c.Format != "openai" && c.Format != "provider" {
		return fmt.Errorf("formato de checkpoint de IA %q invalido", c.Format)
	}
	if c.Phase != AIPhaseModel && c.Phase != AIPhaseTools && c.Phase != AIPhaseDone && c.Phase != AIPhaseAbandoned {
		return fmt.Errorf("fase de checkpoint de IA %q invalida", c.Phase)
	}
	if c.Iteration < 0 || c.NextToolIndex < 0 || c.PromptTokens < 0 || c.CompletionTokens < 0 || len(c.Messages) == 0 || !json.Valid(c.Messages) {
		return errors.New("transcript ou contadores do checkpoint de IA invalidos")
	}
	if len(c.DeferredImages) > 0 && !json.Valid(c.DeferredImages) {
		return errors.New("imagens adiadas do checkpoint de IA invalidas")
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(c.Messages, &messages); err != nil || len(messages) == 0 {
		return errors.New("transcript do checkpoint de IA vazio")
	}
	return nil
}

// ValidateResume only accepts a completed answer or a boundary with no tool
// of uncertain outcome. Pending calls require manual inspection before a new
// turn can be submitted; they are never retried from this checkpoint.
func (c *AICheckpoint) ValidateResume() error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.PendingToolCallID != "" {
		return fmt.Errorf("ferramenta %s estava em andamento; confirme o efeito antes de continuar manualmente", c.PendingToolCallID)
	}
	if c.Phase == AIPhaseAbandoned {
		return errors.New("turno abandonado explicitamente; nao pode ser retomado")
	}
	return nil
}

// NextTurnError prevents an unresolved user request from being copied into a
// new model prompt. Even a safe model boundary must be completed or explicitly
// discarded before another prompt can be accepted.
func (c *AICheckpoint) NextTurnError(answered, discardNoted bool) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.Phase == AIPhaseAbandoned {
		if discardNoted {
			return nil
		}
		return errors.New("descarte do turno nao consta no historico; use /recover discard novamente")
	}
	if c.Phase == AIPhaseDone {
		if answered {
			return nil
		}
		return errors.New("resposta anterior pronta mas ausente do historico; use /recover antes de enviar outro pedido")
	}
	if c.PendingToolCallID != "" {
		return fmt.Errorf("ferramenta %s tem efeito incerto; confira o estado e use /recover discard para abandonar o turno antes de outro pedido", c.PendingToolCallID)
	}
	return errors.New("turno anterior ainda incompleto; use /recover ou /recover discard antes de outro pedido")
}

func (m *Manager) aiPath(sessionID, turnID string) string {
	return filepath.Join(m.turnDir(identity{sessionID: sessionID, turnID: turnID}), "ai.json")
}

// SaveAI durably records a boundary. Callers must stop before starting a tool
// if this write fails. The private file contains full prompts and tool results.
func (m *Manager) SaveAI(c *AICheckpoint) error {
	if err := c.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	path := m.aiPath(c.SessionID, c.TurnID)
	release, err := acquireCheckpointFileLock(context.Background(), path+".lock", 10*time.Second)
	if err != nil {
		return fmt.Errorf("adquirindo lock do checkpoint de IA: %w", err)
	}
	defer release()
	previous, err := os.ReadFile(path)
	if err == nil {
		if c.Revision != digest(previous) {
			return ErrAICheckpointConflict
		}
	} else if os.IsNotExist(err) {
		if c.Revision != "" {
			return ErrAICheckpointConflict
		}
	} else {
		return fmt.Errorf("lendo revisão do checkpoint de IA: %w", err)
	}
	c.UpdatedAt = time.Now()
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if len(data) > 128<<20 {
		return errors.New("checkpoint de IA excede 128 MiB")
	}
	if err := writePrivate(path, data); err != nil {
		return fmt.Errorf("salvando checkpoint de IA: %w", err)
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("abrindo diretorio do checkpoint de IA: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sincronizando diretorio do checkpoint de IA: %w", err)
	}
	c.Revision = digest(data)
	return nil
}

// LoadAI reads a checkpoint for exactly one conversation branch and turn.
func (m *Manager) LoadAI(sessionID, turnID string) (*AICheckpoint, error) {
	if sessionID == "" || turnID == "" {
		return nil, errors.New("identidade de checkpoint de IA vazia")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	path := m.aiPath(sessionID, turnID)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 128<<20 {
		return nil, errors.New("arquivo de checkpoint de IA inseguro ou excessivo")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c AICheckpoint
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.SessionID != sessionID || c.TurnID != turnID {
		return nil, errors.New("identidade do checkpoint de IA nao corresponde a sessao")
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	c.Revision = digest(data)
	return &c, nil
}

// MarkAIAbandoned records an explicit decision to stop an interrupted turn.
// The pending tool ID remains in the private checkpoint as audit evidence.
// Callers must persist a conversation resolution note before this transition.
func (m *Manager) MarkAIAbandoned(sessionID, turnID, reason string) error {
	cp, err := m.LoadAI(sessionID, turnID)
	if err != nil {
		return err
	}
	if cp.Phase == AIPhaseDone {
		return errors.New("turno ja concluido")
	}
	if cp.Phase == AIPhaseAbandoned {
		return nil
	}
	cp.Phase = AIPhaseAbandoned
	cp.DiscardedAt = time.Now()
	cp.DiscardReason = reason
	return m.SaveAI(cp)
}
