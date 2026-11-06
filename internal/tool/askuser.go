package tool

import (
	"fmt"
	"time"

	"github.com/costa/polypod/internal/types"
)

// askUserTimeout is how long the tool blocks waiting for the user's answer.
const askUserTimeout = 300 * time.Second

// UserQuestion carries a question from the AI tool to the TUI.
type UserQuestion struct {
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
}

// NewUserPromptHandler creates a ready-to-use handler.
func NewUserPromptHandler() *UserPromptHandler {
	return &UserPromptHandler{
		QuestionCh: make(chan UserQuestion, 1),
		AnswerCh:   make(chan string, 1),
	}
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

			// Send the question to the TUI
			select {
			case handler.QuestionCh <- uq:
			case <-ctx.Context.Done():
				return types.ToolResult{
					Content: "Tool cancelled: context expired before question could be delivered.",
					IsError: true,
				}, nil
			}

			// Block waiting for the user's answer
			select {
			case answer := <-handler.AnswerCh:
				return types.ToolResult{
					Content: fmt.Sprintf("User responded: %s", answer),
				}, nil
			case <-time.After(askUserTimeout):
				return types.ToolResult{
					Content: "User did not respond within the timeout period.",
					IsError: true,
				}, nil
			case <-ctx.Context.Done():
				return types.ToolResult{
					Content: "Tool cancelled: context expired while waiting for user response.",
					IsError: true,
				}, nil
			}
		},
	}
}
