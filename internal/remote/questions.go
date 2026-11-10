package remote

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const remoteQuestionTimeout = 5 * time.Minute

var remoteAnswerCommand = regexp.MustCompile("(?is)^\\s*(?:resposta|r)\\s+(q_[a-z0-9]+)\\s+(.+?)\\s*$")

type storedQuestion struct {
	ID            string
	SourceKey     string
	SourceMessage string
	SessionID     string
	Participant   string
	Prompt        string
	Answer        string
	ExpiresAt     int64
}

func remoteQuestionKey(source InboundContext, prompt string) string {
	sum := sha256.Sum256([]byte(source.SessionID + "\x00" + source.ParticipantID + "\x00" + source.MessageID + "\x00" + prompt))
	return hex.EncodeToString(sum[:])
}

func validQuestionID(id string) bool {
	if !strings.HasPrefix(id, "q_") || len(id) < 4 || len(id) > 128 {
		return false
	}
	for _, ch := range id[2:] {
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')) {
			return false
		}
	}
	return true
}

// reserveQuestion reuses the same question for a retried inbound agent turn.
// An expired unanswered question is retained for late-reply recognition; a
// later retry receives a new ID and a new five-minute response window.
func (s *sessionStore) reserveQuestion(ctx context.Context, source InboundContext, requestID, prompt string) (storedQuestion, error) {
	if !validQuestionID(requestID) {
		return storedQuestion{}, fmt.Errorf("ID de pergunta remota inválido")
	}
	key := remoteQuestionKey(source, prompt)
	now := time.Now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storedQuestion{}, err
	}
	defer tx.Rollback()
	var q storedQuestion
	err = tx.QueryRowContext(ctx,
		"SELECT id,source_key,source_message_id,session_id,participant,prompt,answer,expires_at FROM remote_questions WHERE source_key=? ORDER BY created_at DESC,id DESC LIMIT 1",
		key).Scan(&q.ID, &q.SourceKey, &q.SourceMessage, &q.SessionID, &q.Participant, &q.Prompt, &q.Answer, &q.ExpiresAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return storedQuestion{}, err
	}
	if errors.Is(err, sql.ErrNoRows) || (q.Answer == "" && q.ExpiresAt <= now) {
		q = storedQuestion{ID: requestID, SourceKey: key, SourceMessage: source.MessageID,
			SessionID: source.SessionID, Participant: source.ParticipantID, Prompt: prompt,
			ExpiresAt: now + remoteQuestionTimeout.Milliseconds()}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO remote_questions(id,source_key,source_message_id,session_id,participant,prompt,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?)",
			q.ID, q.SourceKey, q.SourceMessage, q.SessionID, q.Participant, q.Prompt, q.ExpiresAt, time.Now().UnixNano()); err != nil {
			return storedQuestion{}, err
		}
	}
	if q.SessionID != source.SessionID || q.Participant != source.ParticipantID || q.SourceMessage != source.MessageID || q.Prompt != prompt {
		return storedQuestion{}, fmt.Errorf("pergunta remota diverge da origem")
	}
	if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO remote_question_aliases(alias,question_id) VALUES(?,?)", requestID, q.ID); err != nil {
		return storedQuestion{}, err
	}
	var aliasTarget string
	if err := tx.QueryRowContext(ctx, "SELECT question_id FROM remote_question_aliases WHERE alias=?", requestID).Scan(&aliasTarget); err != nil {
		return storedQuestion{}, err
	}
	if aliasTarget != q.ID {
		return storedQuestion{}, fmt.Errorf("ID de pergunta remota já pertence a outra origem")
	}
	if err := tx.Commit(); err != nil {
		return storedQuestion{}, err
	}
	return q, nil
}

func (s *sessionStore) questionAnswer(ctx context.Context, id string) (string, error) {
	var answer string
	err := s.db.QueryRowContext(ctx, "SELECT answer FROM remote_questions WHERE id=?", id).Scan(&answer)
	return answer, err
}

// AskQuestion publishes a correlated prompt to the originating remote session
// and waits for the exact participant to answer with "resposta q_ID texto".
// The question row and outbound message ID survive an agent-turn retry.
func (s *Server) AskQuestion(ctx context.Context, requestID, prompt string) (string, error) {
	source, ok := InboundFromContext(ctx)
	if !ok {
		return "", fmt.Errorf("pergunta remota sem origem de sessão")
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("pergunta remota vazia")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	q, err := s.store.reserveQuestion(ctx, source, requestID, prompt)
	if err != nil {
		return "", fmt.Errorf("registrando pergunta remota: %w", err)
	}
	if q.Answer != "" {
		return q.Answer, nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	text := fmt.Sprintf("Pergunta do Polypod:\n\n%s\n\nResponda nesta sessão: resposta %s sua resposta", prompt, q.ID)
	if len(text) > 256<<10 {
		return "", fmt.Errorf("pergunta remota excede o limite de mensagem")
	}
	if _, err := s.Publish(ctx, source.SessionID, Message{
		ID: "ask_" + q.ID, Type: "assistant", Content: text,
	}); err != nil {
		return "", fmt.Errorf("publicando pergunta remota: %w", err)
	}
	deadline := time.UnixMilli(q.ExpiresAt)
	waitCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		answer, err := s.store.questionAnswer(waitCtx, q.ID)
		if err == nil && answer != "" {
			return answer, nil
		}
		if err != nil {
			if waitCtx.Err() != nil {
				return "", waitCtx.Err()
			}
			return "", fmt.Errorf("consultando resposta remota: %w", err)
		}
		select {
		case <-waitCtx.Done():
			return "", waitCtx.Err()
		case <-ticker.C:
		case <-s.currentNotification():
		}
	}
}

// ConsumeQuestionAnswer stores a response before the forwarder acknowledges
// the user message. A repeated event or a late answer is consumed again.
func (s *Server) ConsumeQuestionAnswer(ctx context.Context, session RemoteSession, msg Message) (bool, error) {
	match := remoteAnswerCommand.FindStringSubmatch(msg.Content)
	if len(match) != 3 || !validQuestionID(strings.ToLower(match[1])) {
		return false, nil
	}
	id, answer := strings.ToLower(match[1]), strings.TrimSpace(match[2])
	if answer == "" {
		return false, nil
	}
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var sessionID, participant, existing, sourceKey string
	err = tx.QueryRowContext(ctx,
		"SELECT session_id,participant,answer,source_key FROM remote_questions WHERE id=?",
		id).Scan(&sessionID, &participant, &existing, &sourceKey)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if sessionID != session.ID || participant != msg.Sender {
		return false, nil
	}
	if existing == "" {
		// A response to an expired prompt still belongs to its original
		// agent turn. If a retry already issued a replacement prompt, wake it
		// too instead of silently losing the participant's late answer.
		if _, err := tx.ExecContext(ctx,
			"UPDATE remote_questions SET answer=?,answer_message_id=? WHERE source_key=? AND session_id=? AND participant=? AND answer=''",
			answer, msg.ID, sourceKey, session.ID, msg.Sender); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	s.notifyReaders()
	return true, nil
}

// OwnsQuestion identifies a request ID created by this remote relay, including
// a fresh alias assigned when an inbound agent turn was retried.
func (s *Server) OwnsQuestion(ctx context.Context, requestID string) (bool, error) {
	if !validQuestionID(requestID) {
		return false, nil
	}
	var found int
	err := s.store.db.QueryRowContext(ctx, "SELECT 1 FROM remote_question_aliases WHERE alias=?", requestID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// ResolveQuestion records the winning local TUI answer so an eventual remote
// reply cannot change the value used by the current agent turn.
func (s *Server) ResolveQuestion(ctx context.Context, requestID, answer string) error {
	if !validQuestionID(requestID) || strings.TrimSpace(answer) == "" {
		return nil
	}
	result, err := s.store.db.ExecContext(ctx,
		"UPDATE remote_questions SET answer=?,answer_message_id='local' WHERE id=(SELECT question_id FROM remote_question_aliases WHERE alias=?)",
		answer, requestID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows > 0 {
		s.notifyReaders()
	}
	return nil
}
