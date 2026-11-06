package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/costa/polypod/internal/types"
)

// Question represents a structured question from the AI to the user.
type Question struct {
	ID            string           `json:"id"`
	Prompt        string           `json:"prompt"`
	Options       []QuestionOption `json:"options,omitempty"`
	AllowFreeForm bool             `json:"allow_free_form"`
	Header        string           `json:"header,omitempty"`
}

// QuestionOption is a single multiple-choice option for a Question.
type QuestionOption struct {
	Label       string `json:"label"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
}

// Answer is the user's response to a Question.
type Answer struct {
	QuestionID string `json:"question_id"`
	Value      string `json:"value"`
	Cancelled  bool   `json:"cancelled"`
}

// AskQuestionChannel wires the ask_user_structured tool to a UI.
// The tool publishes Questions on Questions and waits for matching
// Answers to be delivered via DeliverAnswer (typically called by the
// TUI after the user picks an option or types a response).
type AskQuestionChannel struct {
	Questions chan Question
	Answers   chan Answer

	mu      sync.Mutex
	pending map[string]chan Answer
	timeout time.Duration
}

// NewAskQuestionChannel creates a new channel with sane defaults.
func NewAskQuestionChannel() *AskQuestionChannel {
	return &AskQuestionChannel{
		Questions: make(chan Question, 8),
		Answers:   make(chan Answer, 8),
		pending:   make(map[string]chan Answer),
		timeout:   300 * time.Second,
	}
}

// SetTimeout overrides the default per-question wait timeout.
func (c *AskQuestionChannel) SetTimeout(d time.Duration) {
	if d > 0 {
		c.timeout = d
	}
}

// Ask submits a question and blocks until the user answers, the context
// is cancelled, or the timeout fires.
func (c *AskQuestionChannel) Ask(ctx context.Context, q Question) (Answer, error) {
	if q.ID == "" {
		q.ID = fmt.Sprintf("q-%d", time.Now().UnixNano())
	}

	respCh := make(chan Answer, 1)
	c.mu.Lock()
	c.pending[q.ID] = respCh
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, q.ID)
		c.mu.Unlock()
	}()

	// Publish the question to the UI side.
	select {
	case c.Questions <- q:
	case <-ctx.Done():
		return Answer{QuestionID: q.ID, Cancelled: true}, ctx.Err()
	}

	// Block until an answer arrives, or we time out / get cancelled.
	select {
	case ans := <-respCh:
		return ans, nil
	case <-time.After(c.timeout):
		return Answer{QuestionID: q.ID, Cancelled: true},
			fmt.Errorf("timeout waiting for user answer to question %s", q.ID)
	case <-ctx.Done():
		return Answer{QuestionID: q.ID, Cancelled: true}, ctx.Err()
	}
}

// DeliverAnswer routes an answer back to the goroutine waiting inside Ask.
// Returns true if a waiter was found and the answer was delivered.
func (c *AskQuestionChannel) DeliverAnswer(ans Answer) bool {
	c.mu.Lock()
	ch, ok := c.pending[ans.QuestionID]
	c.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- ans:
		return true
	default:
		return false
	}
}

// NewAskUserQuestionTool builds the structured ask-user tool definition.
//
// NOTE: the tool name is "ask_user_structured" to avoid colliding with
// the existing "ask_user" / "ask_user_question" tools registered from
// askuser.go. This tool offers a richer schema (typed options with a
// distinct value/label, free-form toggle, optional header) and returns
// the answer as a JSON payload.
func NewAskUserQuestionTool(channel *AskQuestionChannel) types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return "ask_user_structured" },
		DescriptionFn: func() string {
			return "Ask the user a structured question with optional " +
				"multiple-choice options and an optional free-form fallback. " +
				"Blocks until the user responds, cancels, or the request times out."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{
						"type":        "string",
						"description": "The question text to show the user",
					},
					"header": map[string]any{
						"type":        "string",
						"description": "Optional short header/title for the question",
					},
					"options": map[string]any{
						"type":        "array",
						"description": "Optional list of choices the user can pick from",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"label":       map[string]any{"type": "string"},
								"value":       map[string]any{"type": "string"},
								"description": map[string]any{"type": "string"},
							},
							"required": []string{"label"},
						},
					},
					"allow_free_form": map[string]any{
						"type":        "boolean",
						"description": "If true, the user may type a free-form answer in addition to picking an option",
					},
				},
				"required": []string{"prompt"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return false },
		IsDestructiveFn: func(map[string]any) bool { return false },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			p, _ := args["prompt"].(string)
			if p == "" {
				return types.ValidationResult{Valid: false, Message: "prompt is required"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(tctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if channel == nil {
				return types.ToolResult{
					IsError: true,
					Content: "ask_user_structured: no AskQuestionChannel wired up",
				}, nil
			}

			prompt, _ := args["prompt"].(string)
			if prompt == "" {
				return types.ToolResult{IsError: true, Content: "prompt is required"}, nil
			}

			q := Question{
				Prompt:        prompt,
				AllowFreeForm: true,
			}
			if h, ok := args["header"].(string); ok {
				q.Header = h
			}
			if af, ok := args["allow_free_form"].(bool); ok {
				q.AllowFreeForm = af
			}
			if opts, ok := args["options"].([]any); ok {
				for _, o := range opts {
					m, ok := o.(map[string]any)
					if !ok {
						continue
					}
					qo := QuestionOption{}
					if s, ok := m["label"].(string); ok {
						qo.Label = s
					}
					if s, ok := m["value"].(string); ok && s != "" {
						qo.Value = s
					} else {
						qo.Value = qo.Label
					}
					if s, ok := m["description"].(string); ok {
						qo.Description = s
					}
					if qo.Label != "" {
						q.Options = append(q.Options, qo)
					}
				}
			}

			// Resolve a context for the Ask call. types.ToolContext.Context
			// is a context.Context (interface) — fall back to Background if
			// the caller didn't supply one.
			bgCtx := tctx.Context
			if bgCtx == nil {
				bgCtx = context.Background()
			}

			ans, err := channel.Ask(bgCtx, q)
			if err != nil {
				return types.ToolResult{
					IsError: true,
					Content: fmt.Sprintf("question failed: %v", err),
				}, nil
			}
			if ans.Cancelled {
				return types.ToolResult{Content: "User cancelled the question."}, nil
			}

			result := map[string]any{
				"answer":      ans.Value,
				"question_id": ans.QuestionID,
			}
			data, mErr := json.Marshal(result)
			if mErr != nil {
				return types.ToolResult{
					IsError: true,
					Content: fmt.Sprintf("failed to marshal answer: %v", mErr),
				}, nil
			}
			return types.ToolResult{Content: string(data)}, nil
		},
	}
}

// RegisterAskUserQuestionTool builds and registers the structured ask-user
// tool against the given Registry.
func RegisterAskUserQuestionTool(reg *Registry, channel *AskQuestionChannel) {
	reg.RegisterDef(NewAskUserQuestionTool(channel))
}
