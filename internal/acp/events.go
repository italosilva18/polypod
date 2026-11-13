package acp

import (
	"fmt"

	"github.com/costa/polypod/internal/session"
)

type sessionEvent = session.SessionEvent

// EventDir is optional in tests. Production wires Journal directly to the
// shared session.EventJournal so ACP, TUI, headless, and channels share the
// same durable event contract.
func (s *Server) appendEvent(sessionID string, update map[string]any) error {
	j := s.journal()
	if j == nil {
		return nil
	}
	if _, err := j.AppendTurnEvent(sessionID, "acp", "", update); err != nil {
		return fmt.Errorf("publicar evento ACP: %w", err)
	}
	return nil
}

func (s *Server) readEvents(sessionID string, after uint64, limit int) ([]sessionEvent, error) {
	j := s.journal()
	if j == nil {
		return nil, nil
	}
	return j.ReadAfter(sessionID, after, limit)
}
