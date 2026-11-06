package tool

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/costa/polypod/internal/types"
)

// askUserTimeout is how long the tool blocks waiting for the user's answer.
const askUserTimeout = 300 * time.Second

// UserQuestion carries a question from the AI tool to the TUI.
type UserQuestion struct {
	ID       string
	Question string
	Options  []UserQuestionOption
	Header   string
}

// UserQuestionOption represents a selectable option for ask_user.
type UserQuestionOption struct {
	Label       string
	Description string
}

// UserPromptHandler manages the bidirectional ask-user flow between
// the AI tool loop (which blocks) and the TUI (which displays the
// question and collects the user's answer).
type UserPromptHandler struct {
	QuestionCh chan UserQuestion // tool sends question to TUI
	AnswerCh   chan string       // TUI sends answer back to tool

	mu        sync.RWMutex
	remoteAsk RemoteAskFunc
	askMu     sync.Mutex
}

// RemoteAskFunc forwards a correlated question to a remote human channel.
// Implementations must honor ctx and must not log credentials or answers.
type RemoteAskFunc func(ctx context.Context, requestID string, question UserQuestion) (string, error)

// NewUserPromptHandler creates a ready-to-use handler.
func NewUserPromptHandler() *UserPromptHandler {
	return &UserPromptHandler{
		QuestionCh: make(chan UserQuestion, 1),
		AnswerCh:   make(chan string, 1),
	}
}

// SetRemoteAsk installs an optional remote human-question relay. The local
// TUI remains active and the first successful response wins.
func (h *UserPromptHandler) SetRemoteAsk(fn RemoteAskFunc) {
	h.mu.Lock()
	h.remoteAsk = fn
	h.mu.Unlock()
}

func (h *UserPromptHandler) getRemoteAsk() RemoteAskFunc {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.remoteAsk
}

type askResult struct {
	answer string
	err    error
}

// Ask delivers a question locally and, when configured, remotely. Only one
// ask is active at a time because the legacy TUI answer channel is untagged.
func (h *UserPromptHandler) Ask(ctx context.Context, question UserQuestion) (string, error) {
	h.askMu.Lock()
	defer h.askMu.Unlock()

	if question.ID == "" {
		question.ID = newQuestionID()
	}
	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	remote := h.getRemoteAsk()
	var remoteCh chan askResult
	if remote != nil {
		remoteCh = make(chan askResult, 1)
		go func() {
			answer, err := remote(raceCtx, question.ID, question)
			remoteCh <- askResult{answer: answer, err: err}
		}()
	}

	// Deliver to the TUI. If its queue is temporarily full, a successful
	// remote answer can still complete the request.
	for {
		select {
		case h.QuestionCh <- question:
			goto delivered
		case result := <-remoteCh:
			if result.err == nil && result.answer != "" {
				h.discardQueuedQuestion(question.ID)
				return result.answer, nil
			}
			remoteCh = nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

delivered:
	timer := time.NewTimer(askUserTimeout)
	defer timer.Stop()
	for {
		select {
		case answer := <-h.AnswerCh:
			if answer == "" {
				continue
			}
			return answer, nil
		case result := <-remoteCh:
			if result.err == nil && result.answer != "" {
				h.discardQueuedQuestion(question.ID)
				return result.answer, nil
			}
			remoteCh = nil
		case <-timer.C:
			return "", fmt.Errorf("user did not respond within the timeout period")
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

func (h *UserPromptHandler) discardQueuedQuestion(id string) {
	select {
	case queued := <-h.QuestionCh:
		if queued.ID != id {
			// Preserve an unrelated queued question. The channel is buffered and
			// asks are serialized, so this is defensive rather than expected.
			h.QuestionCh <- queued
		}
	default:
	}
}

func newQuestionID() string {
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("q_%d", time.Now().UnixNano())
	}
	return "q_" + hex.EncodeToString(raw[:])
}

// RegisterAskUserTool registers the real ask_user tool that blocks until
// the TUI delivers the user's answer. It replaces both the ask_user stub
// in advanced_tools.go and the ask_user_question stub in remaining.go.
func RegisterAskUserTool(reg *Registry, handler *UserPromptHandler) {
	// Register "ask_user" — the primary tool name used by advanced tools
	reg.RegisterDef(newAskUserDef("ask_user", handler))

	// Register "ask_user_question" — the secondary name from remaining tools
	reg.RegisterDef(newAskUserDef("ask_user_question", handler))
}

func newAskUserDef(name string, handler *UserPromptHandler) types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return name },
		DescriptionFn: func() string {
			return "Ask the user a question and wait for their response. " +
				"Use this when you need clarification or a decision from the user."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"question": map[string]any{
						"type":        "string",
						"description": "The question to ask the user",
					},
					"options": map[string]any{
						"type":        "array",
						"description": "Optional list of choices for the user",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"label":       map[string]any{"type": "string"},
								"description": map[string]any{"type": "string"},
							},
						},
					},
					"header": map[string]any{
						"type":        "string",
						"description": "Optional header shown above the question",
					},
				},
				"required": []string{"question"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return false },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			q, _ := args["question"].(string)
			if q == "" {
				return types.ValidationResult{Valid: false, Message: "question is required"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			question, _ := args["question"].(string)

			// Build the UserQuestion
			uq := UserQuestion{
				Question: question,
			}
			if h, ok := args["header"].(string); ok {
				uq.Header = h
			}
			if opts, ok := args["options"].([]any); ok {
				for _, o := range opts {
					if m, ok := o.(map[string]any); ok {
						opt := UserQuestionOption{}
						if l, ok := m["label"].(string); ok {
							opt.Label = l
						}
						if d, ok := m["description"].(string); ok {
							opt.Description = d
						}
						uq.Options = append(uq.Options, opt)
					}
				}
			}

			answer, err := handler.Ask(ctx.Context, uq)
			if err == nil {
				return types.ToolResult{
					Content: fmt.Sprintf("User responded: %s", answer),
				}, nil
			}
			if ctx.Context.Err() != nil {
				return types.ToolResult{
					Content: "Tool cancelled: context expired while waiting for user response.",
					IsError: true,
				}, nil
			}
			return types.ToolResult{
				Content: "User did not respond within the timeout period.",
				IsError: true,
			}, nil
		},
	}
}
