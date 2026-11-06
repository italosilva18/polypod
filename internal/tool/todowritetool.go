package tool

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/costa/polypod/internal/constants"
	"github.com/costa/polypod/internal/types"
)

// Todo is a single entry in the session todo list managed by the
// todo_write tool. It intentionally mirrors the shape used by Claude
// Code's TodoWriteTool: a human-readable content string, a lifecycle
// status ("pending" | "in_progress" | "completed"), and an "active
// form" description used when the item is actively being worked on.
type Todo struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	ActiveForm string `json:"activeForm"`
}

// Todo status constants.
const (
	TodoStatusPending    = "pending"
	TodoStatusInProgress = "in_progress"
	TodoStatusCompleted  = "completed"
)

// TodoStore is an in-memory, mutex-guarded holder for the current
// session todo list. It is scoped to a single conversation session —
// callers that need multi-session isolation should instantiate one
// store per session.
//
// TodoStore is intentionally minimal: the todo_write tool fully
// replaces the list on every call, and the adapter layer reads the
// current list to render it to the user.
type TodoStore struct {
	mu    sync.RWMutex
	todos []Todo
}

// NewTodoStore returns an empty in-memory todo store.
func NewTodoStore() *TodoStore {
	return &TodoStore{todos: nil}
}

// Set replaces the entire list with a copy of the supplied slice.
// Passing nil or an empty slice clears the store.
func (s *TodoStore) Set(todos []Todo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(todos) == 0 {
		s.todos = nil
		return
	}
	cp := make([]Todo, len(todos))
	copy(cp, todos)
	s.todos = cp
}

// Get returns a snapshot copy of the current todo list. Mutating the
// returned slice does not affect the store.
func (s *TodoStore) Get() []Todo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.todos) == 0 {
		return nil
	}
	cp := make([]Todo, len(s.todos))
	copy(cp, s.todos)
	return cp
}

// Clear wipes the todo list.
func (s *TodoStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.todos = nil
}

// Len returns the current number of todos. Useful for tests and
// lightweight UI indicators without paying for a full copy.
func (s *TodoStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.todos)
}

// ---------------------------------------------------------------------------
// todo_write tool
// ---------------------------------------------------------------------------

// NewTodoWriteTool builds the todo_write tool backed by the given
// TodoStore. The tool replaces the store's current list entirely on
// every call and returns a short Portuguese summary of the new state.
//
// Note: this is DIFFERENT from the background agent task tools
// (TaskCreate/TaskUpdate/TaskList) — todo_write is purely in-memory
// session state that lets the model maintain a visible checklist
// during a conversation. It does not spawn background work.
func NewTodoWriteTool(store *TodoStore) types.Tool {
	return types.BuildTool(NewTodoWriteToolDef(store))
}

// NewTodoWriteToolDef returns the ToolDef for the todo_write tool so
// callers can wrap/compose it before registration (matching the
// bashtool.go pattern of exposing a ToolDef via RegisterDef).
func NewTodoWriteToolDef(store *TodoStore) types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return constants.ToolTodoWrite },
		DescriptionFn: func() string {
			return "Mantem uma lista de tarefas da sessao atual. Substitui " +
				"toda a lista pelos itens fornecidos. Use para planejar e " +
				"acompanhar o progresso de tarefas complexas de multiplos passos. " +
				"Cada item tem 'content' (forma imperativa), 'status' " +
				"('pending', 'in_progress' ou 'completed') e 'activeForm' " +
				"(forma presente continua exibida quando o item esta em andamento). " +
				"No maximo um item pode estar 'in_progress' por vez."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"todos": map[string]any{
						"type":        "array",
						"description": "Lista completa de tarefas. Substitui a lista atual.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"content": map[string]any{
									"type":        "string",
									"description": "Descricao imperativa da tarefa (ex: 'Adicionar validacao de email').",
								},
								"status": map[string]any{
									"type":        "string",
									"enum":        []string{TodoStatusPending, TodoStatusInProgress, TodoStatusCompleted},
									"description": "Status atual da tarefa.",
								},
								"activeForm": map[string]any{
									"type":        "string",
									"description": "Forma presente continua exibida quando a tarefa esta em andamento (ex: 'Adicionando validacao de email').",
								},
							},
							"required": []string{"content", "status", "activeForm"},
						},
					},
				},
				"required": []string{"todos"},
			}
		},

		// ---- Classification -------------------------------------------------
		//
		// todo_write mutates session state so it is NOT read-only, but it
		// never touches the filesystem or external systems so it is not
		// destructive. It should be serialized with other writes to session
		// state, so concurrency-safe is false.
		IsReadOnlyFn:    func(map[string]any) bool { return false },
		IsDestructiveFn: func(map[string]any) bool { return false },
		IsConcurrencyFn: func(map[string]any) bool { return false },
		IsEnabledFn:     func() bool { return store != nil },

		// ---- Validation -----------------------------------------------------
		ValidateFn: func(args map[string]any) types.ValidationResult {
			todos, err := parseTodosArg(args)
			if err != nil {
				return types.ValidationResult{Valid: false, Message: err.Error()}
			}
			if err := validateTodoList(todos); err != nil {
				return types.ValidationResult{Valid: false, Message: err.Error()}
			}
			return types.ValidationResult{Valid: true}
		},

		// ---- Permissions ----------------------------------------------------
		CheckPermFn: func(map[string]any, types.ToolContext) types.PermissionResult {
			return types.PermissionResult{Behavior: types.PermissionAllow}
		},

		// ---- Execution ------------------------------------------------------
		ExecuteFn: func(_ types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if store == nil {
				return types.ToolResult{
					Content: "todo_write: nenhum TodoStore configurado",
					IsError: true,
				}, nil
			}

			todos, err := parseTodosArg(args)
			if err != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("entrada invalida: %v", err),
					IsError: true,
				}, nil
			}
			if err := validateTodoList(todos); err != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("entrada invalida: %v", err),
					IsError: true,
				}, nil
			}

			store.Set(todos)
			return types.ToolResult{Content: summarizeTodos(todos)}, nil
		},
	}
}

// RegisterTodoWriteTool builds and registers the todo_write tool
// against the given Registry. If a tool with the same name already
// exists it will be overwritten.
func RegisterTodoWriteTool(reg *Registry, store *TodoStore) {
	if reg == nil {
		return
	}
	reg.RegisterDef(NewTodoWriteToolDef(store))
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// parseTodosArg extracts the todos slice from the raw tool arguments,
// accepting both the native Go shape ([]any of map[string]any — the
// form the JSON tool-call decoder produces) and a pre-decoded []Todo.
// This keeps the tool easy to call from tests without going through
// JSON marshalling.
func parseTodosArg(args map[string]any) ([]Todo, error) {
	if args == nil {
		return nil, fmt.Errorf("argumento 'todos' é obrigatório")
	}
	raw, ok := args["todos"]
	if !ok {
		return nil, fmt.Errorf("argumento 'todos' é obrigatório")
	}

	switch v := raw.(type) {
	case []Todo:
		return v, nil
	case []any:
		out := make([]Todo, 0, len(v))
		for i, item := range v {
			obj, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("todos[%d] deve ser um objeto", i)
			}
			t, err := todoFromMap(obj)
			if err != nil {
				return nil, fmt.Errorf("todos[%d]: %w", i, err)
			}
			out = append(out, t)
		}
		return out, nil
	default:
		// Last-resort: try to round-trip through JSON in case a caller
		// handed us a generic decoded payload (e.g. []map[string]any).
		data, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("argumento 'todos' deve ser um array")
		}
		var out []Todo
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("argumento 'todos' deve ser um array de objetos")
		}
		return out, nil
	}
}

// todoFromMap converts a single decoded JSON object into a Todo,
// normalising the status value and requiring the three fields to be
// present as non-empty strings.
func todoFromMap(obj map[string]any) (Todo, error) {
	content, _ := obj["content"].(string)
	status, _ := obj["status"].(string)
	activeForm, _ := obj["activeForm"].(string)

	content = strings.TrimSpace(content)
	status = strings.TrimSpace(status)
	activeForm = strings.TrimSpace(activeForm)

	if content == "" {
		return Todo{}, fmt.Errorf("campo 'content' é obrigatório")
	}
	if status == "" {
		return Todo{}, fmt.Errorf("campo 'status' é obrigatório")
	}
	if activeForm == "" {
		return Todo{}, fmt.Errorf("campo 'activeForm' é obrigatório")
	}
	return Todo{
		Content:    content,
		Status:     status,
		ActiveForm: activeForm,
	}, nil
}

// validateTodoList enforces the invariants expected by the tool: each
// item has a valid status and at most one item is marked in_progress.
func validateTodoList(todos []Todo) error {
	inProgress := 0
	for i, t := range todos {
		switch t.Status {
		case TodoStatusPending, TodoStatusInProgress, TodoStatusCompleted:
			// ok
		default:
			return fmt.Errorf("todos[%d]: status invalido %q (esperado pending|in_progress|completed)", i, t.Status)
		}
		if t.Status == TodoStatusInProgress {
			inProgress++
		}
	}
	if inProgress > 1 {
		return fmt.Errorf("apenas um todo pode estar 'in_progress' por vez (encontrados %d)", inProgress)
	}
	return nil
}

// summarizeTodos returns a short Portuguese summary of the list, of
// the form "3 todos atualizados (1 in_progress, 2 pending)". The
// ordering of the status breakdown is stable (in_progress, pending,
// completed) so the summary is deterministic across calls.
func summarizeTodos(todos []Todo) string {
	n := len(todos)
	if n == 0 {
		return "0 todos atualizados (lista vazia)"
	}

	var pending, inProgress, completed int
	for _, t := range todos {
		switch t.Status {
		case TodoStatusInProgress:
			inProgress++
		case TodoStatusCompleted:
			completed++
		default:
			pending++
		}
	}

	var parts []string
	if inProgress > 0 {
		parts = append(parts, fmt.Sprintf("%d in_progress", inProgress))
	}
	if pending > 0 {
		parts = append(parts, fmt.Sprintf("%d pending", pending))
	}
	if completed > 0 {
		parts = append(parts, fmt.Sprintf("%d completed", completed))
	}

	noun := "todos atualizados"
	if n == 1 {
		noun = "todo atualizado"
	}
	return fmt.Sprintf("%d %s (%s)", n, noun, strings.Join(parts, ", "))
}
