package ai

import (
	"context"

	"github.com/costa/polypod/internal/document"
	"github.com/costa/polypod/internal/memory"
	"github.com/costa/polypod/internal/workdir"
)

// relevantMarkdownMemory reads the current workspace's editable notes only
// for a locally authenticated owner. Other remote participants cannot inherit
// notes from the daemon's process working directory.
func (s *Service) relevantMarkdownMemory(ctx context.Context, req AnswerRequest) string {
	documents, ok := s.knowledge.(*DocumentKnowledgeSearcher)
	if !ok || documents == nil {
		return ""
	}
	local := false
	for _, scope := range documents.scopes(ctx, req.Channel, req.UserID) {
		if scope == document.LocalScope {
			local = true
			break
		}
	}
	if !local {
		return ""
	}
	section, err := memory.RelevantMarkdownContext(ctx, workdir.Dir(ctx, "."), req.Query, s.logger)
	if err != nil {
		s.logger.Warn("markdown memory unavailable", "error", err)
		return ""
	}
	return section
}
