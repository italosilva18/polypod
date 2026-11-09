package tool

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/tasks"
	"github.com/costa/polypod/internal/types"
)

// NewTaskUpdateTool updates a field that really belongs to a background task.
// Runtime status is owned by its process; planned work uses plan_update.
func NewTaskUpdateTool(taskRegistry *tasks.TaskRegistry) types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return "task_update" },
		DescriptionFn: func() string {
			return "Atualizar a descrição de uma tarefa background existente. Para estado, dono e dependências de planejamento, use plan_update; esta ferramenta não altera a execução."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id":     map[string]any{"type": "string", "description": "ID de uma tarefa background existente"},
					"description": map[string]any{"type": "string", "description": "Nova descrição mostrada em task_get e task_list"},
					"title":       map[string]any{"type": "string", "description": "Sinônimo legado de description"},
				},
				"required": []string{"task_id"},
			}
		},
		ValidateFn: func(args map[string]any) types.ValidationResult {
			if err := validateTaskUpdate(args); err != nil {
				return types.ValidationResult{Message: err.Error()}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(_ types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if err := validateTaskUpdate(args); err != nil {
				return types.ToolResult{Content: err.Error(), IsError: true}, nil
			}
			if taskRegistry == nil {
				return types.ToolResult{Content: "Registro de tarefas background indisponível", IsError: true}, nil
			}
			id := args["task_id"].(string)
			description, ok := args["description"].(string)
			if !ok {
				description = args["title"].(string)
			}
			if !taskRegistry.Update(id, func(task *tasks.TaskState) { task.Description = description }) {
				return types.ToolResult{Content: fmt.Sprintf("Tarefa background %s não encontrada", id), IsError: true}, nil
			}
			state, ok := taskRegistry.Get(id)
			if !ok {
				return types.ToolResult{Content: fmt.Sprintf("Tarefa background %s não encontrada", id), IsError: true}, nil
			}
			raw, err := json.Marshal(state)
			if err != nil {
				return types.ToolResult{Content: err.Error(), IsError: true}, nil
			}
			return types.ToolResult{Content: string(raw)}, nil
		},
	}
}

func validateTaskUpdate(args map[string]any) error {
	id, ok := args["task_id"].(string)
	if !ok || strings.TrimSpace(id) == "" {
		return fmt.Errorf("task_id é obrigatório")
	}
	for key := range args {
		if key != "task_id" && key != "description" && key != "title" {
			return fmt.Errorf("%s não altera tarefas background; use plan_update para planejamento", key)
		}
	}
	if _, both := args["description"]; both {
		if _, alias := args["title"]; alias {
			return fmt.Errorf("informe apenas description ou title")
		}
	}
	value, exists := args["description"]
	if !exists {
		value, exists = args["title"]
	}
	text, ok := value.(string)
	if !exists || !ok || strings.TrimSpace(text) == "" {
		return fmt.Errorf("description é obrigatório e precisa ser texto não vazio")
	}
	return nil
}

func RegisterTaskUpdateTool(reg *Registry, taskRegistry *tasks.TaskRegistry) {
	reg.RegisterDef(NewTaskUpdateTool(taskRegistry))
}
