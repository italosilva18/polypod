package whatsappgateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/humanloop"
)

const questionTimeout = 5 * time.Minute

var answerCommand = regexp.MustCompile(`(?is)^\s*(?:resposta|r)\s+(q_[a-z0-9]+)\s+(.+?)\s*$`)

// QuestionRelay correlates ask_user with the WhatsApp turn that asked it. The
// question and answer are checkpointed before the transport is acknowledged.
// A waiting agent turn and the native receive loop run on separate goroutines.
type QuestionRelay struct {
	mu         sync.Mutex
	store      *humanloop.Store
	gateway    *Gateway
	statePath  string
	ownerChat  string
	timeout    time.Duration
	retryDelay time.Duration
	questions  map[string]questionRecord
	aliases    map[string]string
	waiters    map[string]chan string
}

type questionRecord struct {
	ID         string    `json:"id"`
	Key        string    `json:"key"`
	ChatID     string    `json:"chat_id"`
	SenderID   string    `json:"sender_id"`
	OutboundID string    `json:"outbound_id,omitempty"`
	Answer     string    `json:"answer,omitempty"`
	Closed     bool      `json:"closed,omitempty"`
	ExpiresAt  time.Time `json:"expires_at"`
	AnsweredAt time.Time `json:"answered_at,omitempty"`
}

type questionState struct {
	Questions map[string]questionRecord `json:"questions"`
}

func NewQuestionRelay(store *humanloop.Store, gateway *Gateway, statePath, ownerChat string) (*QuestionRelay, error) {
	if store == nil || gateway == nil || statePath == "" {
		return nil, errors.New("whatsapp: caixa, gateway e arquivo de perguntas obrigatorios")
	}
	r := &QuestionRelay{store: store, gateway: gateway, statePath: statePath,
		ownerChat: ownerChat, timeout: questionTimeout, retryDelay: 2 * time.Second, questions: make(map[string]questionRecord),
		aliases: make(map[string]string), waiters: make(map[string]chan string)}
	if raw, err := os.ReadFile(statePath); err == nil {
		var saved questionState
		if err := json.Unmarshal(raw, &saved); err != nil {
			return nil, fmt.Errorf("whatsapp: estado de perguntas invalido: %w", err)
		}
		for id, q := range saved.Questions {
			if id == "" || q.ID != id || q.ChatID == "" || q.SenderID == "" || q.Key == "" {
				return nil, errors.New("whatsapp: estado de perguntas incompleto")
			}
			r.questions[id] = q
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("whatsapp: lendo perguntas: %w", err)
	}
	// The process can stop after the outbox commit but before its ID reaches
	// this state file. Recover that narrow window before the generic worker
	// starts, or an expired question could be sent much later.
	_, messages := store.Snapshot()
	reconciled := false
	for id, q := range r.questions {
		if q.OutboundID != "" {
			continue
		}
		for _, msg := range messages {
			if msg.Direction == humanloop.DirectionOutbound && msg.Channel == Channel &&
				msg.UserID == q.ChatID && msg.ExternalID == "ask:"+id {
				q.OutboundID = msg.ID
				r.questions[id] = q
				reconciled = true
				break
			}
		}
	}
	if reconciled {
		if err := r.saveLocked(); err != nil {
			return nil, fmt.Errorf("whatsapp: reconciliando perguntas: %w", err)
		}
	}
	return r, nil
}

// Ask sends a durable question and waits for a response from the exact chat
// and sender of the originating turn. Background turns use the configured
// direct owner chat, if one is present and allowlisted.
func (r *QuestionRelay) Ask(ctx context.Context, requestID, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	requestID, prompt = strings.TrimSpace(requestID), strings.TrimSpace(prompt)
	if requestID == "" || prompt == "" {
		return "", errors.New("whatsapp: ID e pergunta obrigatorios")
	}
	source, ok := InboundFromContext(ctx)
	if !ok {
		if r.ownerChat == "" {
			return "", errors.New("whatsapp: conversa de perguntas nao configurada")
		}
		source = InboundContext{MessageID: "background:" + requestID, ChatID: r.ownerChat, SenderID: r.ownerChat}
	}
	if !r.gateway.Allowed(source.ChatID, source.SenderID) {
		return "", errors.New("whatsapp: origem da pergunta nao autorizada")
	}
	key := questionKey(source.MessageID, prompt)
	now := time.Now()
	r.mu.Lock()
	var q questionRecord
	for _, saved := range r.questions {
		if saved.Key == key && saved.ChatID == source.ChatID && saved.SenderID == source.SenderID &&
			(now.Before(saved.ExpiresAt) || saved.Answer != "") {
			q = saved
			break
		}
	}
	if q.ID == "" {
		q = questionRecord{ID: requestID, Key: key, ChatID: source.ChatID,
			SenderID: source.SenderID, ExpiresAt: now.Add(r.timeout)}
		if _, used := r.questions[q.ID]; used {
			r.mu.Unlock()
			return "", errors.New("whatsapp: ID de pergunta duplicado")
		}
		r.questions[q.ID] = q
		if err := r.saveLocked(); err != nil {
			delete(r.questions, q.ID)
			r.mu.Unlock()
			return "", err
		}
	}
	if requestID != q.ID {
		r.aliases[requestID] = q.ID
	}
	if q.Answer != "" {
		r.mu.Unlock()
		return q.Answer, nil
	}
	if _, waiting := r.waiters[q.ID]; waiting {
		r.mu.Unlock()
		return "", errors.New("whatsapp: pergunta ja aguarda resposta")
	}
	ch := make(chan string, 1)
	r.waiters[q.ID] = ch
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.waiters, q.ID)
		r.mu.Unlock()
	}()

	text := fmt.Sprintf("*Pergunta do Polypod*\n\n%s\n\nResponda a esta mensagem ou envie: resposta %s sua resposta", prompt, q.ID)
	out, err := r.store.EnqueueQuestionOutboundOnce(Channel, q.ChatID, text, "ask:"+q.ID, q.ExpiresAt.Add(time.Hour))
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	current := r.questions[q.ID]
	if current.OutboundID == "" {
		previous := current
		current.OutboundID = out.ID
		r.questions[q.ID] = current
		if err := r.saveLocked(); err != nil {
			r.questions[q.ID] = previous
			r.mu.Unlock()
			return "", err
		}
	}
	r.mu.Unlock()
	askCtx, cancel := context.WithDeadline(ctx, q.ExpiresAt)
	defer cancel()
	for !r.store.DeliveredTo(out.ID, Channel) {
		r.mu.Lock()
		answered := r.questions[q.ID].Answer
		r.mu.Unlock()
		if answered != "" {
			return answered, nil
		}
		if err := r.gateway.Deliver(askCtx, out); err != nil {
			if !errors.Is(err, humanloop.ErrRemoteUnavailable) {
				if askCtx.Err() != nil && ctx.Err() == nil {
					if _, unavailable := source.AskStatus.unavailableQuestion(); unavailable {
						source.AskStatus.cancelUnavailable()
						return "", humanloop.ErrRemoteUnavailable
					}
				}
				return "", err
			}
			source.AskStatus.observeUnavailable(q.ID)
			// A temporary WhatsApp disconnect must not consume an inbound
			// attempt. Keep the agent turn waiting for reconnect or timeout.
			timer := time.NewTimer(r.retryDelay)
			select {
			case <-askCtx.Done():
				timer.Stop()
				if ctx.Err() == nil {
					source.AskStatus.cancelUnavailable()
					return "", humanloop.ErrRemoteUnavailable
				}
				return "", ctx.Err()
			case <-timer.C:
			}
			continue
		}
		if err := r.store.MarkDelivered(out.ID, Channel); err != nil {
			return "", err
		}
		source.AskStatus.markDelivered()
	}
	if err := r.store.Complete(out.ID, "", time.Now()); err != nil {
		return "", err
	}
	r.mu.Lock()
	current = r.questions[q.ID]
	if current.Answer != "" {
		r.mu.Unlock()
		return current.Answer, nil
	}
	r.mu.Unlock()
	select {
	case answer := <-ch:
		return answer, nil
	case <-askCtx.Done():
		return "", askCtx.Err()
	}
}

// ShouldDeferUnavailable reports whether the inbound turn still has an
// undelivered question after its tool returns. A local TUI answer wins over
// a temporary remote outage, including a timeout race between both relays.
func (r *QuestionRelay) ShouldDeferUnavailable(status *AskDeliveryStatus) bool {
	id, unavailable := status.unavailableQuestion()
	if !unavailable || id == "" {
		return false
	}
	r.mu.Lock()
	q, found := r.questions[id]
	r.mu.Unlock()
	if !found || q.Answer != "" {
		return false
	}
	return q.OutboundID == "" || !r.store.DeliveredTo(q.OutboundID, Channel)
}

// Consume records a reply before allowing WhatsApp's acknowledgement path to
// proceed. Replies from another chat or sender remain ordinary inbound text.
func (r *QuestionRelay) Consume(ctx context.Context, e Event) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !r.gateway.Allowed(e.ChatID, e.SenderID) || strings.TrimSpace(e.Text) == "" {
		return false, nil
	}
	match := answerCommand.FindStringSubmatch(e.Text)
	r.mu.Lock()
	defer r.mu.Unlock()
	var q questionRecord
	answer := ""
	if len(match) == 3 {
		q = r.questions[strings.ToLower(match[1])]
		answer = strings.TrimSpace(match[2])
	} else if e.ReplyTo != "" {
		for _, saved := range r.questions {
			if saved.OutboundID != "" && strings.EqualFold(outboundID(saved.OutboundID), e.ReplyTo) &&
				saved.ChatID == e.ChatID && saved.SenderID == e.SenderID {
				q = saved
				answer = strings.TrimSpace(e.Text)
				break
			}
		}
	}
	if q.ID == "" || q.ChatID != e.ChatID || q.SenderID != e.SenderID || answer == "" {
		return false, nil
	}
	if q.Answer != "" {
		return true, nil // repeated transport event or repeated reply
	}
	if q.Closed || !time.Now().Before(q.ExpiresAt) {
		return true, nil // known late answer is not a new agent instruction
	}
	q.Answer, q.AnsweredAt = answer, time.Now()
	r.questions[q.ID] = q
	if err := r.saveLocked(); err != nil {
		q.Answer = ""
		q.AnsweredAt = time.Time{}
		r.questions[q.ID] = q
		return false, err
	}
	if ch := r.waiters[q.ID]; ch != nil {
		select {
		case ch <- answer:
		default:
		}
	}
	return true, nil
}

// Owns reports whether this relay created the question. Used to keep Telegram
// and WhatsApp resolution records separate when both channels are configured.
func (r *QuestionRelay) Owns(requestID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := requestID
	if alias := r.aliases[requestID]; alias != "" {
		id = alias
	}
	_, ok := r.questions[id]
	return ok
}

// ResolveQuestion records a TUI answer so a later WhatsApp reply does not
// generate a second agent answer.
func (r *QuestionRelay) ResolveQuestion(requestID, answer string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := requestID
	if alias := r.aliases[requestID]; alias != "" {
		id = alias
	}
	defer delete(r.aliases, requestID)
	q, ok := r.questions[id]
	if !ok || q.Answer != "" {
		return nil
	}
	q.Answer, q.AnsweredAt = answer, time.Now()
	r.questions[id] = q
	if err := r.saveLocked(); err != nil {
		q.Answer, q.AnsweredAt = "", time.Time{}
		r.questions[id] = q
		return err
	}
	return nil
}

// Run expires orphaned questions after a restart. The deferred outbox is never
// made due by the generic worker; only Ask sends it synchronously.
func (r *QuestionRelay) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		r.expire(time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *QuestionRelay) expire(now time.Time) {
	r.mu.Lock()
	var outboxes []string
	changed := false
	previous := make(map[string]questionRecord, len(r.questions))
	for id, q := range r.questions {
		previous[id] = q
	}
	for id, q := range r.questions {
		if now.Before(q.ExpiresAt) {
			continue
		}
		if !q.Closed {
			q.Closed = true
			r.questions[id] = q
			changed = true
			if q.OutboundID != "" {
				outboxes = append(outboxes, q.OutboundID)
			}
		}
		if now.Sub(q.ExpiresAt) > 7*24*time.Hour {
			delete(r.questions, id)
			changed = true
		}
	}
	if changed {
		if err := r.saveLocked(); err != nil {
			r.questions = previous
			r.mu.Unlock()
			return
		}
	}
	for alias, id := range r.aliases {
		if _, exists := r.questions[id]; !exists {
			delete(r.aliases, alias)
		}
	}
	r.mu.Unlock()
	for _, id := range outboxes {
		var err error
		if r.store.DeliveredTo(id, Channel) {
			err = r.store.Complete(id, "", now)
		} else {
			err = r.store.Retry(id, context.DeadlineExceeded, now, 1)
		}
		if err != nil {
			r.mu.Lock()
			for questionID, q := range r.questions {
				if q.OutboundID == id {
					q.Closed = false
					r.questions[questionID] = q
					_ = r.saveLocked()
					break
				}
			}
			r.mu.Unlock()
		}
	}
}

func (r *QuestionRelay) saveLocked() error {
	raw, err := json.Marshal(questionState{Questions: r.questions})
	if err != nil {
		return err
	}
	dir := filepath.Dir(r.statePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".whatsapp-questions-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), r.statePath); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func questionKey(turnID, prompt string) string {
	sum := sha256.Sum256([]byte(turnID + "\x00" + prompt))
	return hex.EncodeToString(sum[:])
}
