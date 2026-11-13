package humanloop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ChildQuestionStatus represents the lifecycle state of a child/subagent question.
type ChildQuestionStatus string

const (
	ChildQuestionPending  ChildQuestionStatus = "pending"
	ChildQuestionAnswered ChildQuestionStatus = "answered"
	ChildQuestionCanceled ChildQuestionStatus = "canceled"
)

// ChildQuestion captures an isolated question asked by a concurrent child task or subagent.
type ChildQuestion struct {
	ID         string              `json:"id"`
	ChildID    string              `json:"child_id"`
	ChildLabel string              `json:"child_label"`
	Question   string              `json:"question"`
	Draft      string              `json:"draft,omitempty"`
	Answer     string              `json:"answer,omitempty"`
	ErrorText  string              `json:"error_text,omitempty"`
	Status     ChildQuestionStatus `json:"status"`
	CreatedAt  time.Time           `json:"created_at"`
	AnsweredAt time.Time           `json:"answered_at,omitempty"`

	replyCh chan childReply
}

type childReply struct {
	answer string
	err    error
}

// MultiChildQuestionQueue manages concurrent questions from multiple child
// agents/tasks, allowing the user to inspect drafts, keep errors isolated per
// child, and answer questions in ANY order via `/questions` and `/answer`.
type MultiChildQuestionQueue struct {
	mu        sync.RWMutex
	questions map[string]*ChildQuestion
	seq       int
}

// NewMultiChildQuestionQueue creates an isolated concurrent child Q&A queue.
func NewMultiChildQuestionQueue() *MultiChildQuestionQueue {
	return &MultiChildQuestionQueue{
		questions: make(map[string]*ChildQuestion),
	}
}

// Submit registers a pending child question without blocking, returning its short ID.
func (q *MultiChildQuestionQueue) Submit(childID, childLabel, question string) (*ChildQuestion, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return nil, fmt.Errorf("a pergunta do subagente não pode ser vazia")
	}
	childID = strings.TrimSpace(childID)
	if childID == "" {
		var rnd [4]byte
		_, _ = rand.Read(rnd[:])
		childID = "child_" + hex.EncodeToString(rnd[:])
	}
	if strings.TrimSpace(childLabel) == "" {
		childLabel = childID
	}

	q.mu.Lock()
	q.seq++
	id := fmt.Sprintf("q%d", q.seq)
	entry := &ChildQuestion{
		ID:         id,
		ChildID:    childID,
		ChildLabel: strings.TrimSpace(childLabel),
		Question:   question,
		Status:     ChildQuestionPending,
		CreatedAt:  time.Now(),
		replyCh:    make(chan childReply, 1),
	}
	q.questions[id] = entry
	cp := *entry
	cp.replyCh = nil
	q.mu.Unlock()
	return &cp, nil
}

// Ask submits a question from a child agent and waits until the user answers it
// (in any order relative to sibling tasks) or ctx is canceled.
func (q *MultiChildQuestionQueue) Ask(ctx context.Context, childID, childLabel, question string) (string, error) {
	submitted, err := q.Submit(childID, childLabel, question)
	if err != nil {
		return "", err
	}
	q.mu.RLock()
	live := q.questions[submitted.ID]
	q.mu.RUnlock()
	if live == nil {
		return "", fmt.Errorf("pergunta %s não encontrada", submitted.ID)
	}

	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case rep := <-live.replyCh:
		if rep.err != nil {
			return "", rep.err
		}
		return rep.answer, nil
	case <-ctx.Done():
		q.mu.Lock()
		if live.Status == ChildQuestionPending {
			live.Status = ChildQuestionCanceled
			live.ErrorText = ctx.Err().Error()
		}
		q.mu.Unlock()
		return "", ctx.Err()
	}
}

// SaveDraft stores an in-progress draft answer for one specific question without
// affecting any sibling child tasks.
func (q *MultiChildQuestionQueue) SaveDraft(questionID, draft string) error {
	id := strings.ToLower(strings.TrimSpace(questionID))
	q.mu.Lock()
	defer q.mu.Unlock()
	item, ok := q.questions[id]
	if !ok {
		return fmt.Errorf("pergunta %q não encontrada", questionID)
	}
	if item.Status != ChildQuestionPending {
		return fmt.Errorf("pergunta %s já está com status %s", id, item.Status)
	}
	item.Draft = draft
	return nil
}

// Answer resolves questionID (out of order if desired) and wakes the waiting child task.
func (q *MultiChildQuestionQueue) Answer(questionID, answer string) error {
	id := strings.ToLower(strings.TrimSpace(questionID))
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return fmt.Errorf("a resposta para %s não pode ser vazia", questionID)
	}

	q.mu.Lock()
	item, ok := q.questions[id]
	if !ok {
		q.mu.Unlock()
		return fmt.Errorf("pergunta %q não encontrada", questionID)
	}
	if item.Status != ChildQuestionPending {
		st := item.Status
		q.mu.Unlock()
		return fmt.Errorf("pergunta %s já foi encerrada (%s)", id, st)
	}
	item.Answer = answer
	item.Draft = ""
	item.ErrorText = ""
	item.Status = ChildQuestionAnswered
	item.AnsweredAt = time.Now()
	ch := item.replyCh
	q.mu.Unlock()

	if ch != nil {
		select {
		case ch <- childReply{answer: answer}:
		default:
		}
	}
	return nil
}

// FailChild marks a question as failed with an isolated error without tainting other child questions.
func (q *MultiChildQuestionQueue) FailChild(questionID, errMsg string) error {
	id := strings.ToLower(strings.TrimSpace(questionID))
	q.mu.Lock()
	item, ok := q.questions[id]
	if !ok {
		q.mu.Unlock()
		return fmt.Errorf("pergunta %q não encontrada", questionID)
	}
	if item.Status != ChildQuestionPending {
		q.mu.Unlock()
		return nil
	}
	item.Status = ChildQuestionCanceled
	item.ErrorText = strings.TrimSpace(errMsg)
	ch := item.replyCh
	q.mu.Unlock()

	if ch != nil {
		select {
		case ch <- childReply{err: fmt.Errorf("%s", errMsg)}:
		default:
		}
	}
	return nil
}

// ListPending returns all pending child questions ordered by creation time.
func (q *MultiChildQuestionQueue) ListPending() []ChildQuestion {
	if q == nil {
		return nil
	}
	q.mu.RLock()
	out := make([]ChildQuestion, 0, len(q.questions))
	for _, item := range q.questions {
		if item.Status == ChildQuestionPending {
			cp := *item
			cp.replyCh = nil
			out = append(out, cp)
		}
	}
	q.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// FormatPending renders a human-readable list of concurrent child questions for `/questions`.
func (q *MultiChildQuestionQueue) FormatPending() string {
	pending := q.ListPending()
	if len(pending) == 0 {
		return "Nenhuma pergunta pendente de subagentes/tarefas filhas."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Perguntas Pendentes de Subagentes (%d)\n\n", len(pending))
	for _, item := range pending {
		fmt.Fprintf(&b, "- **[%s]** (%s): %s\n", item.ID, item.ChildLabel, item.Question)
		if item.Draft != "" {
			fmt.Fprintf(&b, "  _Rascunho salvo_: %s\n", item.Draft)
		}
	}
	b.WriteString("\nResponda em qualquer ordem com: `/answer <id> <sua resposta>` (ex.: `/answer q1 usar PostgreSQL`).")
	return b.String()
}
