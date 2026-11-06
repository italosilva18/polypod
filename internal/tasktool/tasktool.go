package tasktool

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/constants"
	"github.com/costa/polypod/internal/types"
)

// Task represents a session task (todo list item).
// Migrated from Claude Code's TodoWrite tool.
type Task struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Priority  string    `json:"priority"` // "high", "medium", "low"
	Status    string    `json:"status"`   // "pending", "in_progress", "completed", "cancelled"
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TaskList manages session-level task lists.
type TaskList struct {
	mu    sync.Mutex
	tasks []Task
}

// NewTaskList creates an empty task list.
func NewTaskList() *TaskList {
	return &TaskList{}
}

// Add adds a new task.
func (tl *TaskList) Add(content, priority string) *Task {
	tl.mu.Lock()
	defer tl.mu.Unlock()

	t := Task{
		ID:        fmt.Sprintf("task_%d", len(tl.tasks)+1),
		Content:   content,
		Priority:  priority,
		Status:    "pending",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	tl.tasks = append(tl.tasks, t)
	return &t
}

// Update changes a task's status.
func (tl *TaskList) Update(id, status string) error {
	tl.mu.Lock()
	defer tl.mu.Unlock()

	for i := range tl.tasks {
		if tl.tasks[i].ID == id {
			tl.tasks[i].Status = status
			tl.tasks[i].UpdatedAt = time.Now()
			return nil
		}
	}
	return fmt.Errorf("tarefa %s nao encontrada", id)
}

// Get returns a task by ID.
func (tl *TaskList) Get(id string) (*Task, error) {
	tl.mu.Lock()
	defer tl.mu.Unlock()

	for i := range tl.tasks {
		if tl.tasks[i].ID == id {
			return &tl.tasks[i], nil
		}
	}
	return nil, fmt.Errorf("tarefa %s nao encontrada", id)
}

// List returns all tasks.
func (tl *TaskList) List() []Task {
	tl.mu.Lock()
	defer tl.mu.Unlock()

	result := make([]Task, len(tl.tasks))
	copy(result, tl.tasks)
	return result
}

// ListByStatus returns tasks filtered by status.
func (tl *TaskList) ListByStatus(status string) []Task {
	tl.mu.Lock()
	defer tl.mu.Unlock()

	var result []Task
	for _, t := range tl.tasks {
		if t.Status == status {
			result = append(result, t)
		}
	}
	return result
}

// Remove removes a task by ID.
func (tl *TaskList) Remove(id string) error {
	tl.mu.Lock()
	defer tl.mu.Unlock()

	for i := range tl.tasks {
		if tl.tasks[i].ID == id {
			tl.tasks = append(tl.tasks[:i], tl.tasks[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("tarefa %s nao encontrada", id)
}

// Clear removes all tasks.
func (tl *TaskList) Clear() {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	tl.tasks = nil
}

// Format displays the task list in a readable format.
func (tl *TaskList) Format() string {
	tasks := tl.List()
	if len(tasks) == 0 {
		return "Nenhuma tarefa."
	}

	var sb strings.Builder
	sb.WriteString("=== Lista de Tarefas ===\n\n")

	for _, t := range tasks {
		icon := ""
		switch t.Status {
		case "completed":
			icon = "[x]"
		case "in_progress":
			icon = "[>]"
		case "cancelled":
			icon = "[-]"
		default:
			icon = "[ ]"
		}

		priority := ""
		switch t.Priority {
		case "high":
			priority = " !!!"
		case "low":
			priority = " -"
		}

		sb.WriteString(fmt.Sprintf("%s %s%s\n", icon, t.Content, priority))
	}

	return sb.String()
}

// Stats returns summary statistics.
func (tl *TaskList) Stats() map[string]int {
	tl.mu.Lock()
	defer tl.mu.Unlock()

	stats := map[string]int{
		"total":       len(tl.tasks),
		"pending":     0,
		"in_progress": 0,
		"completed":   0,
		"cancelled":   0,
	}
	for _, t := range tl.tasks {
		stats[t.Status]++
	}
	return stats
}

// --- Tool Registration ---

// RegisterTaskTools registers all task management tools.
func RegisterTaskTools(reg interface{ Register(types.Tool) }, tl *TaskList) {
	// TodoWrite
	reg.Register(types.BuildTool(types.ToolDef{
		NameFn:        func() string { return constants.ToolTodoWrite },
		DescriptionFn: func() string { return "Gerenciar lista de tarefas da sessao atual (criar, atualizar, remover)" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action":   map[string]any{"type": "string", "description": "Acao: add, update, remove, list, clear"},
					"content":  map[string]any{"type": "string", "description": "Conteudo da tarefa (para add)"},
					"task_id":  map[string]any{"type": "string", "description": "ID da tarefa (para update/remove)"},
					"status":   map[string]any{"type": "string", "description": "Novo status (para update): pending, in_progress, completed, cancelled"},
					"priority": map[string]any{"type": "string", "description": "Prioridade: high, medium, low"},
				},
				"required": []string{"action"},
			}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			action, _ := args["action"].(string)
			switch action {
			case "add":
				content, _ := args["content"].(string)
				if content == "" {
					return types.ToolResult{Content: "content e obrigatorio para add", IsError: true}, nil
				}
				priority, _ := args["priority"].(string)
				if priority == "" {
					priority = "medium"
				}
				task := tl.Add(content, priority)
				return types.ToolResult{Content: fmt.Sprintf("Tarefa criada: %s (ID: %s)", task.Content, task.ID)}, nil

			case "update":
				taskID, _ := args["task_id"].(string)
				status, _ := args["status"].(string)
				if taskID == "" || status == "" {
					return types.ToolResult{Content: "task_id e status obrigatorios para update", IsError: true}, nil
				}
				if err := tl.Update(taskID, status); err != nil {
					return types.ToolResult{Content: err.Error(), IsError: true}, nil
				}
				return types.ToolResult{Content: fmt.Sprintf("Tarefa %s atualizada para %s", taskID, status)}, nil

			case "remove":
				taskID, _ := args["task_id"].(string)
				if taskID == "" {
					return types.ToolResult{Content: "task_id obrigatorio para remove", IsError: true}, nil
				}
				if err := tl.Remove(taskID); err != nil {
					return types.ToolResult{Content: err.Error(), IsError: true}, nil
				}
				return types.ToolResult{Content: fmt.Sprintf("Tarefa %s removida", taskID)}, nil

			case "list":
				return types.ToolResult{Content: tl.Format()}, nil

			case "clear":
				tl.Clear()
				return types.ToolResult{Content: "Lista de tarefas limpa."}, nil

			default:
				return types.ToolResult{Content: fmt.Sprintf("Acao desconhecida: %s. Use: add, update, remove, list, clear", action), IsError: true}, nil
			}
		},
	}))

	// TaskCreate
	reg.Register(types.BuildTool(types.ToolDef{
		NameFn:        func() string { return constants.ToolTaskCreate },
		DescriptionFn: func() string { return "Criar uma nova tarefa de fundo" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt":     map[string]any{"type": "string", "description": "Descricao da tarefa"},
					"agent_type": map[string]any{"type": "string", "description": "Tipo de agente (default: general-purpose)"},
				},
				"required": []string{"prompt"},
			}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			prompt, _ := args["prompt"].(string)
			task := tl.Add(prompt, "medium")
			return types.ToolResult{Content: fmt.Sprintf("Tarefa de fundo criada: %s (ID: %s)", task.Content, task.ID)}, nil
		},
	}))

	// TaskGet
	reg.Register(types.BuildTool(types.ToolDef{
		NameFn:        func() string { return constants.ToolTaskGet },
		DescriptionFn: func() string { return "Obter detalhes de uma tarefa por ID" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "string", "description": "ID da tarefa"},
				},
				"required": []string{"task_id"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			taskID, _ := args["task_id"].(string)
			task, err := tl.Get(taskID)
			if err != nil {
				return types.ToolResult{Content: err.Error(), IsError: true}, nil
			}
			return types.ToolResult{Content: fmt.Sprintf("[%s] %s (%s, %s)", task.Status, task.Content, task.Priority, task.ID)}, nil
		},
	}))

	// TaskList
	reg.Register(types.BuildTool(types.ToolDef{
		NameFn:        func() string { return constants.ToolTaskList },
		DescriptionFn: func() string { return "Listar todas as tarefas" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			return types.ToolResult{Content: tl.Format()}, nil
		},
	}))

	// TaskStop
	reg.Register(types.BuildTool(types.ToolDef{
		NameFn:        func() string { return constants.ToolTaskStop },
		DescriptionFn: func() string { return "Cancelar uma tarefa de fundo" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "string", "description": "ID da tarefa"},
				},
				"required": []string{"task_id"},
			}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			taskID, _ := args["task_id"].(string)
			if err := tl.Update(taskID, "cancelled"); err != nil {
				return types.ToolResult{Content: err.Error(), IsError: true}, nil
			}
			return types.ToolResult{Content: fmt.Sprintf("Tarefa %s cancelada", taskID)}, nil
		},
	}))
}
