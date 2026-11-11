package ai

import (
	"context"
	"strings"
	"time"

	"github.com/costa/polypod/internal/whatsappgateway"
)

// ExperienceSearcher supplies summaries of internally verified outcomes and
// reviewed skills. The caller decides which authenticated turns may read the
// global operational history; outcome records have no per-chat ownership.
type ExperienceSearcher interface {
	SearchVerified(ctx context.Context, query string, limit int) ([]string, error)
	SearchApplied(ctx context.Context, query string, limit int) ([]string, error)
}

// SetExperienceSearcher is safe to call while turns are running. Only direct
// local sessions and the validated owner WhatsApp chat receive global history.
func (s *Service) SetExperienceSearcher(searcher ExperienceSearcher, ownerChat string) {
	s.experienceMu.Lock()
	s.experienceSearcher = searcher
	s.experienceOwnerChat = ownerChat
	s.experienceMu.Unlock()
}

func (s *Service) relevantExperiences(ctx context.Context, req AnswerRequest) string {
	s.experienceMu.RLock()
	searcher := s.experienceSearcher
	ownerChat := s.experienceOwnerChat
	s.experienceMu.RUnlock()
	if searcher == nil || !ownerExperienceTurn(ctx, req, ownerChat) {
		return ""
	}
	searchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	verified, err := searcher.SearchVerified(searchCtx, req.Query, 2)
	if err != nil {
		s.logger.Warn("verified experience search failed", "error", err)
	}
	applied, err := searcher.SearchApplied(searchCtx, req.Query, 1)
	if err != nil {
		s.logger.Warn("applied skill search failed", "error", err)
	}
	if len(verified) == 0 && len(applied) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("EXPERIÊNCIAS RELEVANTES (histórico resumido, use apenas se combinar com a tarefa atual; confira o efeito atual antes de concluir):\n")
	for _, item := range verified {
		b.WriteString("- ")
		b.WriteString(item)
		b.WriteByte('\n')
	}
	for _, item := range applied {
		b.WriteString("\n")
		b.WriteString(item)
		b.WriteByte('\n')
	}
	return b.String()
}

func ownerExperienceTurn(ctx context.Context, req AnswerRequest, ownerChat string) bool {
	if req.Channel == "cli" && req.UserID == "local" {
		return true
	}
	if req.Channel == "system" && req.UserID == "owner" {
		return true
	}
	if req.Channel != whatsappgateway.Channel || ownerChat == "" {
		return false
	}
	inbound, ok := whatsappgateway.InboundFromContext(ctx)
	return ok && inbound.ChatID == ownerChat && inbound.SenderID == ownerChat && req.UserID == inbound.ChatID
}
