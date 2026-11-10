package ai

import (
	"context"

	"github.com/costa/polypod/internal/memory"
)

// ownerMemoryContext marks only trusted local or owner turns as able to read
// explicit global facts. Remote channel and user IDs alone are insufficient;
// the WhatsApp owner also needs a validated inbound gateway context.
func (s *Service) ownerMemoryContext(ctx context.Context, req AnswerRequest) context.Context {
	s.experienceMu.RLock()
	ownerChat := s.experienceOwnerChat
	s.experienceMu.RUnlock()
	if ownerExperienceTurn(ctx, req, ownerChat) {
		return memory.WithGlobalMemoryAccess(ctx)
	}
	return ctx
}
