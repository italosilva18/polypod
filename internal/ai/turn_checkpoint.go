package ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/costa/polypod/internal/turncheckpoint"
	"github.com/costa/polypod/internal/workdir"
)

type turnCheckpointKey struct{}

type turnCheckpointScope struct {
	manager   *turncheckpoint.Manager
	sessionID string
	turnID    string
	resume    bool
}

// WithTurnCheckpoint enables durable model/tool boundaries for one CLI turn.
func WithTurnCheckpoint(ctx context.Context, manager *turncheckpoint.Manager, sessionID, turnID string) context.Context {
	if manager == nil || sessionID == "" || turnID == "" {
		return ctx
	}
	return context.WithValue(ctx, turnCheckpointKey{}, turnCheckpointScope{manager: manager, sessionID: sessionID, turnID: turnID})
}

// WithResumeTurnCheckpoint selects an existing boundary. No new user message
// should be inserted by the caller when resuming this turn.
func WithResumeTurnCheckpoint(ctx context.Context, manager *turncheckpoint.Manager, sessionID, turnID string) context.Context {
	if manager == nil || sessionID == "" || turnID == "" {
		return ctx
	}
	return context.WithValue(ctx, turnCheckpointKey{}, turnCheckpointScope{manager: manager, sessionID: sessionID, turnID: turnID, resume: true})
}

func checkpointScopeFor(ctx context.Context) (turnCheckpointScope, bool) {
	scope, ok := ctx.Value(turnCheckpointKey{}).(turnCheckpointScope)
	return scope, ok && scope.manager != nil
}

func (c *Client) beginAICheckpoint(ctx context.Context, format string, initial any) (*turncheckpoint.AICheckpoint, turnCheckpointScope, error) {
	scope, ok := checkpointScopeFor(ctx)
	if !ok {
		return nil, turnCheckpointScope{}, errors.New("checkpoint de IA nao configurado")
	}
	if scope.resume {
		cp, err := scope.manager.LoadAI(scope.sessionID, scope.turnID)
		if err != nil {
			return nil, scope, err
		}
		if err := cp.ValidateResume(); err != nil {
			return nil, scope, err
		}
		if cp.Format != format {
			return nil, scope, fmt.Errorf("provedor atual %s difere do checkpoint %s", format, cp.Format)
		}
		if err := c.checkCheckpointToolDefinitions(ctx, cp); err != nil {
			return nil, scope, err
		}
		return cp, scope, nil
	}
	if _, err := scope.manager.LoadAI(scope.sessionID, scope.turnID); err == nil {
		return nil, scope, errors.New("checkpoint do turno ja existe; use retomada em vez de reiniciar")
	} else if !os.IsNotExist(err) {
		return nil, scope, err
	}
	data, err := json.Marshal(initial)
	if err != nil {
		return nil, scope, err
	}
	cp := &turncheckpoint.AICheckpoint{
		Version: turncheckpoint.AIVersion, SessionID: scope.sessionID, TurnID: scope.turnID,
		Format: format, Model: c.modelFor(ctx), SkillNames: c.skillNamesFor(ctx),
		Messages: data, Phase: turncheckpoint.AIPhaseModel,
	}
	cp.ToolDefinitionsSHA256, err = c.toolDefinitionsDigest(ctx)
	if err != nil {
		return nil, scope, err
	}
	if session, ok := ctx.Value(requestToolSessionKey{}).(toolSessionID); ok {
		cp.AgentName = session.agent
	}
	if dir, err := filepath.Abs(workdir.Dir(ctx, ".")); err == nil {
		cp.WorkingDir = dir
	}
	if err := scope.manager.SaveAI(cp); err != nil {
		return nil, scope, err
	}
	return cp, scope, nil
}

func (c *Client) toolDefinitionsDigest(ctx context.Context) (string, error) {
	type definition struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Parameters  any    `json:"parameters"`
	}
	definitions := make([]definition, 0)
	if c.skills != nil {
		for _, tool := range c.skills.ToolDefinitions(c.skillNamesFor(ctx)) {
			if tool.Function != nil {
				definitions = append(definitions, definition{tool.Function.Name, tool.Function.Description, tool.Function.Parameters})
			}
		}
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })
	raw, err := json.Marshal(definitions)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (c *Client) checkCheckpointToolDefinitions(ctx context.Context, cp *turncheckpoint.AICheckpoint) error {
	if cp.Phase != turncheckpoint.AIPhaseTools {
		return nil
	}
	if cp.ToolDefinitionsSHA256 == "" {
		return errors.New("checkpoint sem assinatura de ferramentas; retomada de chamada antiga bloqueada")
	}
	ctx = withRequestSkillNames(ctx, cp.SkillNames)
	actual, err := c.toolDefinitionsDigest(ctx)
	if err != nil {
		return err
	}
	if actual != cp.ToolDefinitionsSHA256 {
		return errors.New("definições de ferramentas mudaram desde o checkpoint; chamada antiga bloqueada")
	}
	return nil
}

func saveAICheckpoint(scope turnCheckpointScope, cp *turncheckpoint.AICheckpoint, messages any) error {
	data, err := json.Marshal(messages)
	if err != nil {
		return err
	}
	cp.Messages = data
	return scope.manager.SaveAI(cp)
}
