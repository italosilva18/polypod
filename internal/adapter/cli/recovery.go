package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/session"
)

const recoveryDraft = "Continue a solicitação anterior a partir do estado atual. Confira o que já foi concluído no histórico e no ambiente antes de qualquer nova ação. Se uma ação externa puder ter sido executada e não for possível confirmar o resultado, descreva a dúvida e pergunte antes de repeti-la."

// activeRecovery prefers the conversation actually used by the model, but
// falls back to the restored display if persistence failed during startup.
func (a *Adapter) activeRecovery(ctx context.Context) (string, string) {
	if a.activeSessionID == "" {
		return "", ""
	}
	visible := make([]session.Message, len(a.restoredMessages))
	for i, msg := range a.restoredMessages {
		visible[i] = session.Message{Role: msg.Role, Content: msg.Content}
	}
	if a.convMgr != nil {
		sess, err := a.convMgr.GetSession(ctx, "cli", "local")
		if err == nil {
			current := recoveryMessages(sess.Messages)
			if sameRecoveryDisplay(current, visible) {
				return a.inspectRecovery(a.activeSessionID, current)
			}
		} else {
			a.logger.Warn("recovery conversation inspection unavailable", "error", err)
		}
	}
	if a.sessStorage != nil {
		saved, err := a.sessStorage.Load(a.activeSessionID)
		if err == nil && saved != nil && sameRecoveryDisplay(saved.Messages, visible) {
			return a.inspectRecovery(a.activeSessionID, saved.Messages)
		}
		if err != nil {
			a.logger.Warn("recovery session inspection unavailable", "session_id", a.activeSessionID, "error", err)
		}
	}
	return a.inspectRecovery(a.activeSessionID, visible)
}

func sameRecoveryDisplay(a, b []session.Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Role != b[i].Role || a[i].Content != b[i].Content {
			return false
		}
	}
	return true
}

func recoveryMessages(messages []conversation.Message) []session.Message {
	result := make([]session.Message, len(messages))
	for i, msg := range messages {
		result[i] = session.Message{
			ID: msg.ID, Role: string(msg.Role), Content: msg.Content, Timestamp: msg.Timestamp,
		}
	}
	return result
}

func (a *Adapter) inspectRecovery(id string, messages []session.Message) (string, string) {
	if id == "" || len(messages) == 0 {
		return "", ""
	}
	var records []session.ToolTraceRecord
	var journalErr error
	if a.toolTranscript != nil {
		records, journalErr = a.toolTranscript.Load(id, 0)
		if journalErr != nil {
			a.logger.Warn("tool journal unavailable for recovery", "session_id", id, "error", journalErr)
		}
	}
	status := session.InspectInterruptedTurn(messages, records)
	if !status.NeedsReview() {
		return "", ""
	}

	var b strings.Builder
	b.WriteString("Retomada: ")
	if status.Unanswered {
		b.WriteString("a última entrada permanece sem resposta concluída. Ela está no histórico acima. ")
	}
	if len(status.AmbiguousTools) > 0 {
		b.WriteString(fmt.Sprintf("O registro de ferramentas tem %d chamada(s) iniciada(s) sem conclusão registrada. A ação pode ter produzido efeitos; confira o estado antes de repeti-la. ", len(status.AmbiguousTools)))
	}
	if journalErr != nil || a.toolTranscript == nil {
		b.WriteString("O registro de ferramentas não pôde ser consultado; confira o estado manualmente. ")
	}
	b.WriteString("Nada foi retomado automaticamente. Digite /recover para criar um rascunho editável de continuidade; revise e envie quando quiser.")
	return b.String(), recoveryDraft
}
