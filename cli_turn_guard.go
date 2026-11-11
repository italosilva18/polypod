package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/turncheckpoint"
)

// ensureCLINextTurnResolved runs before a queued or typed message is added to
// the conversation. An unresolved request must never be included in a new
// model prompt, where it could cause a completed or ambiguous tool to repeat.
func ensureCLINextTurnResolved(manager *turncheckpoint.Manager, branchID string, sess *conversation.Session) error {
	if manager == nil || branchID == "" || sess == nil {
		return nil
	}
	lastUser := -1
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		if sess.Messages[i].Role == conversation.RoleUser {
			lastUser = i
			break
		}
	}
	if lastUser < 0 || sess.Messages[lastUser].ID == "" {
		return nil
	}
	cp, err := manager.LoadAI(branchID, sess.Messages[lastUser].ID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("novo turno bloqueado: checkpoint anterior indisponível: %w", err)
	}
	answered, discardNoted := false, false
	for _, message := range sess.Messages[lastUser+1:] {
		if message.Role == conversation.RoleAssistant {
			answered = true
			if strings.HasPrefix(message.Content, turncheckpoint.RecoveryDiscardNotePrefix) {
				discardNoted = true
			}
		}
	}
	if err := cp.NextTurnError(answered, discardNoted); err != nil {
		return fmt.Errorf("novo turno bloqueado: %w", err)
	}
	return nil
}
