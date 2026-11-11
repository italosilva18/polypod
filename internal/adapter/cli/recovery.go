package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/session"
	"github.com/costa/polypod/internal/turncheckpoint"
)

const recoveryDraft = "Continue a solicitação anterior a partir do estado atual. Confira o que já foi concluído no histórico e no ambiente antes de qualquer nova ação. Se uma ação externa puder ter sido executada e não for possível confirmar o resultado, descreva a dúvida e pergunte antes de repeti-la."

func lastRecoveryUser(messages []session.Message) (session.Message, int) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i], i
		}
	}
	return session.Message{}, -1
}

func recoveryResolution(messages []session.Message, userIndex int) (answered, discardNoted bool) {
	for _, message := range messages[userIndex+1:] {
		if message.Role == "assistant" {
			answered = true
			if strings.HasPrefix(message.Content, turncheckpoint.RecoveryDiscardNotePrefix) {
				discardNoted = true
			}
		}
	}
	return
}

// pendingCheckpointFor is used by branch transitions before a branch's
// unanswered request can be copied or hidden behind another history.
func (a *Adapter) pendingCheckpointFor(id string, messages []session.Message) error {
	if a.turnCheckpoint == nil || id == "" {
		return nil
	}
	lastUser, index := lastRecoveryUser(messages)
	if index < 0 || lastUser.ID == "" {
		return nil
	}
	cp, err := a.turnCheckpoint.LoadAI(id, lastUser.ID)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checkpoint da sessão indisponível: %w", err)
	}
	answered, discardNoted := recoveryResolution(messages, index)
	return cp.NextTurnError(answered, discardNoted)
}

func (a *Adapter) pendingCheckpointGuard() error {
	return a.withCLIProfileLease(context.Background(), a.pendingCheckpointGuardUnlocked)
}

func (a *Adapter) acquireCLIProfileLease(ctx context.Context) (func() error, error) {
	if a.turnCheckpoint == nil {
		return func() error { return nil }, nil
	}
	return a.turnCheckpoint.AcquireBranchLease(ctx, "cli:local")
}

func (a *Adapter) withCLIProfileLease(ctx context.Context, action func() error) error {
	release, err := a.acquireCLIProfileLease(ctx)
	if err != nil {
		return err
	}
	defer a.releaseCLIProfileLease(release)
	return action()
}

func (a *Adapter) releaseCLIProfileLease(release func() error) {
	if release == nil {
		return
	}
	if err := release(); err != nil {
		if a.logger != nil {
			a.logger.Warn("CLI profile lease release failed", "error", err)
		} else {
			slog.Warn("CLI profile lease release failed", "error", err)
		}
	}
}

// checkCurrentCLISnapshot is called only after the profile lease is held.
// A second process may have advanced both the external manifest and the
// conversation DB while this adapter retained an older in-memory pointer.
func (a *Adapter) checkCurrentCLISnapshot(ctx context.Context) (*conversation.Session, error) {
	if a.convMgr == nil {
		return nil, nil
	}
	if storage, ok := a.sessStorage.(interface {
		LoadHeader(string) (*session.SessionHeader, error)
	}); ok && a.activeSessionID != "" {
		header, err := storage.LoadHeader(a.activeSessionID)
		if err != nil {
			return nil, err
		}
		revision := ""
		if header != nil {
			revision = header.Revision
		}
		if revision != a.activeSessionRevision {
			return nil, fmt.Errorf("%w: recarregue a branch antes de alterar a conversa", session.ErrSessionConflict)
		}
	}
	sess, err := a.convMgr.GetSession(ctx, "cli", "local")
	if err != nil {
		return nil, err
	}
	if err := a.convMgr.CheckCurrentSnapshot(ctx, sess); err != nil {
		return nil, err
	}
	return sess, nil
}

func (a *Adapter) pendingCheckpointGuardUnlocked() error {
	sess, err := a.checkCurrentCLISnapshot(context.Background())
	if err != nil || sess == nil {
		return err
	}
	return a.pendingCheckpointFor(a.activeSessionID, recoveryMessages(sess.Messages))
}

func (a *Adapter) discardRecovery(ctx context.Context) (string, error) {
	if a.convMgr == nil || a.turnCheckpoint == nil || a.activeSessionID == "" {
		return "", fmt.Errorf("descarte de recuperação indisponível")
	}
	release, err := a.acquireCLIProfileLease(ctx)
	if err != nil {
		return "", err
	}
	defer a.releaseCLIProfileLease(release)
	agentName := "default"
	if a.aiSvc != nil {
		agentName = a.aiSvc.ActiveAgent()
	}
	ctx = conversation.WithJournalScope(ctx, "cli", "local", a.activeSessionID, agentName)
	sess, err := a.checkCurrentCLISnapshot(ctx)
	if err != nil {
		return "", err
	}
	messages := recoveryMessages(sess.Messages)
	lastUser, index := lastRecoveryUser(messages)
	if index < 0 || lastUser.ID == "" {
		return "", fmt.Errorf("nenhum turno do usuário para abandonar")
	}
	cp, err := a.turnCheckpoint.LoadAI(a.activeSessionID, lastUser.ID)
	if err != nil {
		return "", err
	}
	if cp.Phase == turncheckpoint.AIPhaseDone {
		return "", fmt.Errorf("resposta anterior já pronta; use /recover para gravá-la")
	}
	_, noted := recoveryResolution(messages, index)
	if !noted {
		detail := "nenhuma ferramenta ficou marcada como pendente"
		if cp.PendingToolCallID != "" {
			detail = fmt.Sprintf("o efeito da ferramenta %s é incerto", cp.PendingToolCallID)
		}
		note := turncheckpoint.RecoveryDiscardNotePrefix + " Solicitação interrompida e abandonada por decisão explícita do usuário; " + detail + ". Não repetir as ações anteriores sem um novo pedido."
		if _, err := a.convMgr.AddAssistantMessageDurable(ctx, sess, note); err != nil {
			return "", fmt.Errorf("salvando resolução do turno: %w", err)
		}
	} else if err := a.convMgr.EnsureCurrentDurable(ctx, sess); err != nil {
		return "", fmt.Errorf("confirmando resolução anterior do turno: %w", err)
	}
	if err := a.saveActiveSession(sess); err != nil {
		return "", fmt.Errorf("salvando sessão com resolução do turno: %w", err)
	}
	if err := a.turnCheckpoint.MarkAIAbandoned(a.activeSessionID, lastUser.ID, "user_discard"); err != nil {
		return "", fmt.Errorf("registrando abandono do turno: %w", err)
	}
	return "Turno abandonado. O efeito de qualquer ferramenta pendente continua incerto; novas ações exigem um pedido explícito.", nil
}

// activeRecovery prefers the conversation actually used by the model, but
// falls back to the restored display if persistence failed during startup.
func (a *Adapter) activeRecovery(ctx context.Context) (string, string, string) {
	if a.activeSessionID == "" {
		return "", "", ""
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

func (a *Adapter) inspectRecovery(id string, messages []session.Message) (string, string, string) {
	if id == "" || len(messages) == 0 {
		return "", "", ""
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
	if lastUser, index := lastRecoveryUser(messages); index >= 0 && lastUser.ID != "" && a.turnCheckpoint != nil {
		if cp, err := a.turnCheckpoint.LoadAI(id, lastUser.ID); err == nil {
			_, discardNoted := recoveryResolution(messages, index)
			if cp.Phase == turncheckpoint.AIPhaseAbandoned {
				if discardNoted {
					return "", "", ""
				}
				return "Retomada bloqueada: abandono registrado sem nota de resolução no histórico. Use /recover discard novamente.", "", ""
			}
			if cp.PendingToolCallID != "" {
				return fmt.Sprintf("Retomada bloqueada: ferramenta %s pode ter produzido efeito. Confira o estado e use /recover discard para abandonar o turno sem repetir a ação.", cp.PendingToolCallID), "", ""
			}
		}
	}
	if !status.NeedsReview() {
		return "", "", ""
	}
	if status.Unanswered && status.LastUser.ID != "" && a.turnCheckpoint != nil {
		cp, err := a.turnCheckpoint.LoadAI(id, status.LastUser.ID)
		if err == nil {
			resumeErr := cp.ValidateResume()
			if resumeErr == nil {
				if a.aiSvc != nil && cp.AgentName != "" && cp.AgentName != a.aiSvc.ActiveAgent() {
					return fmt.Sprintf("Retomada bloqueada: o agente ativo difere de %s usado no turno interrompido.", cp.AgentName), "", ""
				}
				if a.aiSvc != nil && cp.WorkingDir != "" {
					currentDir, dirErr := filepath.Abs(a.aiSvc.WorkingDir("cli", "local", id, "."))
					if dirErr != nil || currentDir != cp.WorkingDir {
						return fmt.Sprintf("Retomada bloqueada: o diretório de trabalho mudou. Restaure %s antes de continuar.", cp.WorkingDir), "", ""
					}
				}
				phase := "último ponto seguro"
				if cp.Phase == "done" {
					phase = "resposta concluída, aguardando gravação no histórico"
				}
				return "Retomada disponível: " + phase + ". Digite /recover para continuar o mesmo turno sem repetir ferramentas concluídas.", "", status.LastUser.ID
			}
			return fmt.Sprintf("Retomada bloqueada: %v. A ação pode ter produzido efeitos. Confira o estado antes de iniciar um novo pedido.", resumeErr), "", ""
		}
		if !os.IsNotExist(err) {
			return fmt.Sprintf("Retomada bloqueada: checkpoint indisponível: %v", err), "", ""
		}
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
	return b.String(), recoveryDraft, ""
}
