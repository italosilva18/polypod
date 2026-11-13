package acp

import (
	"errors"

	"github.com/costa/polypod/internal/session"
)

type turnReceipt = session.TurnReceipt

// claimTurn publishes a durable marker in the shared session event journal
// before the model or any tool runs.
func (s *Server) claimTurn(sessionID, key, text string) (turnReceipt, bool, error) {
	j := s.journal()
	if j == nil {
		return turnReceipt{}, false, errors.New("idempotência ACP requer diário de eventos persistente")
	}
	return j.ClaimTurn(sessionID, key, text)
}

func (s *Server) completeTurn(sessionID, key, text, reason, message string) error {
	j := s.journal()
	if j == nil {
		return errors.New("idempotência ACP requer diário de eventos persistente")
	}
	return j.CompleteTurn(sessionID, key, text, reason, "", message)
}
