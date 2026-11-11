package ai

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/costa/polypod/internal/turncheckpoint"
	"github.com/costa/polypod/internal/workdir"
)

// ResumeStream continues an interrupted CLI turn from a saved model/tool
// boundary. It reuses the provider transcript rather than rebuilding the
// original prompt from a newer conversation or repeating completed tools.
func (s *Service) ResumeStream(ctx context.Context, req AnswerRequest, checkpoint *turncheckpoint.AICheckpoint, callback StreamCallback) (*AnswerResponse, error) {
	if checkpoint == nil || s.client == nil {
		return nil, errors.New("checkpoint de retomada indisponivel")
	}
	if err := checkpoint.ValidateResume(); err != nil {
		return nil, err
	}
	if checkpoint.Phase == turncheckpoint.AIPhaseDone {
		if callback != nil && checkpoint.FinalContent != "" {
			callback(checkpoint.FinalContent)
		}
		return &AnswerResponse{Content: checkpoint.FinalContent, PromptTokens: checkpoint.PromptTokens,
			CompletionTokens: checkpoint.CompletionTokens, TotalTokens: checkpoint.PromptTokens + checkpoint.CompletionTokens}, nil
	}
	if s.onActivity != nil {
		s.onActivity()
	}
	ctx = s.ownerMemoryContext(ctx, req)
	ctx = workdir.WithScope(ctx, s.workdirs, req.Channel, req.UserID, req.SessionMemoryScope)
	if checkpoint.AgentName != "" && checkpoint.AgentName != s.agentName {
		return nil, fmt.Errorf("agente ativo %s difere do agente %s do checkpoint", s.agentName, checkpoint.AgentName)
	}
	if checkpoint.WorkingDir != "" {
		currentDir, err := filepath.Abs(workdir.Dir(ctx, "."))
		if err != nil || currentDir != checkpoint.WorkingDir {
			return nil, fmt.Errorf("diretorio de trabalho mudou desde o checkpoint; restaure %s antes de retomar", checkpoint.WorkingDir)
		}
	}
	ctx = s.withProjectInstructions(ctx)
	ctx = withRequestToolSession(ctx, req.Channel, req.UserID, s.agentName, req.SessionMemoryScope)
	ctx = withRequestSkillNames(ctx, checkpoint.SkillNames)
	ctx = withRequestModel(ctx, checkpoint.Model)
	resp, err := s.client.CompleteWithToolsStream(ctx, nil, callback, s.logger)
	if err != nil {
		return nil, err
	}
	return &AnswerResponse{Content: resp.Content, PromptTokens: resp.PromptTokens,
		CompletionTokens: resp.CompletionTokens, TotalTokens: resp.TotalTokens}, nil
}
