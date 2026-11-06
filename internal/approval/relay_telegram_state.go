package approval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type telegramQuestion struct {
	Text   string `json:"text"`
	Answer string `json:"answer,omitempty"`
}

type telegramState struct {
	Offset    int64                       `json:"offset"`
	Questions map[string]telegramQuestion `json:"questions,omitempty"`
}

func (r *TelegramApprovalRelay) loadState() error {
	r.questions = make(map[string]telegramQuestion)
	if r.cfg.StatePath == "" {
		return nil
	}
	raw, err := os.ReadFile(r.cfg.StatePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("telegram: lendo estado: %w", err)
	}
	var state telegramState
	if err := json.Unmarshal(raw, &state); err != nil {
		return fmt.Errorf("telegram: estado invalido: %w", err)
	}
	r.offset = state.Offset
	if state.Questions != nil {
		r.questions = state.Questions
	}
	return nil
}

// saveStateLocked checkpoints only events already accepted by the durable inbox.
func (r *TelegramApprovalRelay) saveStateLocked() error {
	if r.cfg.StatePath == "" {
		return nil
	}
	raw, err := json.Marshal(telegramState{Offset: r.offset, Questions: r.questions})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.cfg.StatePath), 0700); err != nil {
		return err
	}
	tmp := r.cfg.StatePath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, r.cfg.StatePath)
}

// Run continuously receives messages even when no question is pending. Only
// one instance may own a bot token. Cancellation stops its long poll.
func (r *TelegramApprovalRelay) Run(ctx context.Context) error {
	if err := r.validateConfig(); err != nil {
		return err
	}
	for ctx.Err() == nil {
		if err := r.pollAndDispatch(ctx, nil, nil); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			r.log.WarnContext(ctx, "telegram: inbound polling failed; retrying")
			if err := waitContext(ctx, 2*time.Second); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

// ResolveQuestion records a local answer too, so a later remote answer cannot
// accidentally create a second agent task for an already resolved question.
func (r *TelegramApprovalRelay) ResolveQuestion(id, answer string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	previous, ok := r.questions[id]
	if ok && previous.Answer != "" {
		return nil
	}
	updated := previous
	updated.Answer = answer
	r.questions[id] = updated
	if err := r.saveStateLocked(); err != nil {
		if ok {
			r.questions[id] = previous
		} else {
			delete(r.questions, id)
		}
		return err
	}
	return nil
}
