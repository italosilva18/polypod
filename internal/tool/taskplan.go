package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/taskplan"
	"github.com/costa/polypod/internal/taskqueue"
	"github.com/costa/polypod/internal/types"
)

// RegisterPlanTaskTools exposes durable planned work. The optional queue
// connects plan_run to the full background agent runtime when wired.
func RegisterPlanTaskTools(reg *Registry, store *taskplan.Store, queues ...*taskqueue.Queue) {
	var queue *taskqueue.Queue
	if len(queues) != 0 {
		queue = queues[0]
	}
	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return "plan_create" },
		DescriptionFn: func() string { return "Criar tarefa planejada persistente. Não inicia execução." },
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"subject":     map[string]any{"type": "string", "description": "Título curto da tarefa"},
				"description": map[string]any{"type": "string", "description": "Objetivo e critérios de conclusão"},
				"owner":       map[string]any{"type": "string", "description": "Identificador do responsável pelo plano, opcional"},
				"blocked_by":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "IDs de tarefas que precisam terminar antes desta"},
			}, "required": []string{"subject"}}
		},
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			subject, err := planString(args, "subject", true)
			if err != nil {
				return planError(err), nil
			}
			description, err := planString(args, "description", false)
			if err != nil {
				return planError(err), nil
			}
			owner, err := planString(args, "owner", false)
			if err != nil {
				return planError(err), nil
			}
			blockedBy, err := planIDs(args, "blocked_by")
			if err != nil {
				return planError(err), nil
			}
			task, err := store.Create(planContext(tc), taskplan.CreateInput{
				Subject: subject, Description: description, Owner: owner, BlockedBy: blockedBy,
			})
			return planResult(task, err), nil
		},
	})

	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "plan_update" },
		DescriptionFn: func() string {
			return "Atualizar título, descrição, dono, estado ou dependências antes de iniciar a execução de uma tarefa planejada."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"id":          map[string]any{"type": "string", "description": "ID da tarefa planejada"},
				"subject":     map[string]any{"type": "string", "description": "Novo título"},
				"description": map[string]any{"type": "string", "description": "Nova descrição"},
				"status":      map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed"}},
				"owner":       map[string]any{"type": "string", "description": "Novo agente responsável; string vazia remove a atribuição"},
				"blocked_by":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Lista completa de dependências; lista vazia remove todas"},
			}, "required": []string{"id"}}
		},
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			id, err := planString(args, "id", true)
			if err != nil {
				return planError(err), nil
			}
			var in taskplan.UpdateInput
			if in.Subject, err = optionalPlanString(args, "subject"); err != nil {
				return planError(err), nil
			}
			if in.Description, err = optionalPlanString(args, "description"); err != nil {
				return planError(err), nil
			}
			if in.Owner, err = optionalPlanString(args, "owner"); err != nil {
				return planError(err), nil
			}
			if raw, exists := args["status"]; exists {
				status, ok := raw.(string)
				if !ok {
					return planError(fmt.Errorf("status precisa ser texto")), nil
				}
				value := taskplan.Status(status)
				in.Status = &value
			}
			if _, exists := args["blocked_by"]; exists {
				ids, idsErr := planIDs(args, "blocked_by")
				if idsErr != nil {
					return planError(idsErr), nil
				}
				in.BlockedBy = &ids
			}
			task, err := store.Update(planContext(tc), id, in)
			return planResult(task, err), nil
		},
	})

	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return "plan_get" },
		DescriptionFn: func() string { return "Consultar uma tarefa planejada e suas dependências." },
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"id": map[string]any{"type": "string", "description": "ID da tarefa planejada"},
			}, "required": []string{"id"}}
		},
		// With a queue, a read may persist a newly observed execution status.
		IsReadOnlyFn: func(map[string]any) bool { return queue == nil },
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			id, err := planString(args, "id", true)
			if err != nil {
				return planError(err), nil
			}
			task, err := store.Get(planContext(tc), id)
			if err == nil {
				task, err = syncPlanTask(planContext(tc), store, queue, task)
			}
			return planResult(task, err), nil
		},
	})

	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return "plan_list" },
		DescriptionFn: func() string { return "Listar tarefas planejadas persistentes, com filtro opcional por estado." },
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"status": map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed"}},
				"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
				"offset": map[string]any{"type": "integer", "minimum": 0, "description": "Número de tarefas anteriores a pular"},
			}}
		},
		IsReadOnlyFn: func(map[string]any) bool { return queue == nil },
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			status, err := planString(args, "status", false)
			if err != nil {
				return planError(err), nil
			}
			limit := 50
			if raw, exists := args["limit"]; exists {
				var valid bool
				limit, valid = planInt(raw)
				if !valid || limit < 1 || limit > 200 {
					return planError(fmt.Errorf("limit precisa estar entre 1 e 200")), nil
				}
			}
			offset := 0
			if raw, exists := args["offset"]; exists {
				var valid bool
				offset, valid = planInt(raw)
				if !valid || offset < 0 {
					return planError(fmt.Errorf("offset precisa ser um inteiro positivo")), nil
				}
			}
			tasks, err := store.List(planContext(tc), taskplan.Status(status), limit, offset)
			if err == nil {
				for i := range tasks {
					tasks[i], err = syncPlanTask(planContext(tc), store, queue, tasks[i])
					if err != nil {
						break
					}
				}
			}
			return planResult(tasks, err), nil
		},
	})

	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "plan_claim" },
		DescriptionFn: func() string {
			return "Assumir uma tarefa planejada disponível; verifica dependências e atribuição atual. Não inicia execução."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"id":    map[string]any{"type": "string", "description": "ID da tarefa planejada"},
				"owner": map[string]any{"type": "string", "description": "Responsável que assume a tarefa; padrão: agente atual"},
			}, "required": []string{"id"}}
		},
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			id, err := planString(args, "id", true)
			if err != nil {
				return planError(err), nil
			}
			owner, err := planString(args, "owner", false)
			if err != nil {
				return planError(err), nil
			}
			if owner == "" {
				owner = string(tc.AgentID)
			}
			task, err := store.Claim(planContext(tc), id, owner)
			return planResult(task, err), nil
		},
	})

	if queue != nil {
		registerPlanRunTool(reg, store, queue)
	}
}

func registerPlanRunTool(reg *Registry, store *taskplan.Store, queue *taskqueue.Queue) {
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "plan_run" },
		DescriptionFn: func() string {
			return "Iniciar uma vez a execução durável de um plano pelo ciclo completo do agente padrão, respeitando responsável e dependências. Retorna o estado existente se repetido; para falha use plan_retry."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"id":    map[string]any{"type": "string", "description": "ID da tarefa planejada"},
				"owner": map[string]any{"type": "string", "description": "Responsável pelo plano, não seleciona o agente de execução; padrão: agente atual"},
			}, "required": []string{"id"}}
		},
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			id, err := planString(args, "id", true)
			if err != nil {
				return planError(err), nil
			}
			owner, err := planString(args, "owner", false)
			if err != nil {
				return planError(err), nil
			}
			if owner == "" {
				owner = string(tc.AgentID)
			}
			ctx := planContext(tc)
			planned, err := store.Get(ctx, id)
			if err != nil {
				return planError(err), nil
			}
			// Reconcile terminal prerequisites before the transactional blocker
			// check. The queue may have finished while this process was offline.
			for _, dependencyID := range planned.BlockedBy {
				dependency, getErr := store.Get(ctx, dependencyID)
				if getErr != nil {
					return planError(getErr), nil
				}
				if _, getErr = syncPlanTask(ctx, store, queue, dependency); getErr != nil {
					return planError(getErr), nil
				}
			}
			planned, err = store.ReserveExecution(ctx, id, owner)
			if err != nil {
				return planError(err), nil
			}
			if planned.Execution == nil {
				return planError(taskplan.ErrExecutionConflict), nil
			}
			planned, err = store.PublishExecution(ctx, queue, planned)
			return planResult(planned, err), nil
		},
	})

	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "plan_retry" },
		DescriptionFn: func() string {
			return "Retomar explicitamente a execução de um plano com falha ou cancelado, preservando o histórico de tentativas."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"id":                  map[string]any{"type": "string", "description": "ID da tarefa planejada"},
				"additional_attempts": map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
			}, "required": []string{"id"}}
		},
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			id, err := planString(args, "id", true)
			if err != nil {
				return planError(err), nil
			}
			attempts := 3
			if raw, present := args["additional_attempts"]; present {
				var valid bool
				attempts, valid = planInt(raw)
				if !valid || attempts < 1 || attempts > 20 {
					return planError(fmt.Errorf("additional_attempts precisa estar entre 1 e 20")), nil
				}
			}
			ctx := planContext(tc)
			planned, err := store.Get(ctx, id)
			if err != nil {
				return planError(err), nil
			}
			if planned.Execution == nil {
				return planError(taskplan.ErrExecutionNotFound), nil
			}
			queued, err := queue.Get(ctx, planned.Execution.QueueID)
			if err != nil {
				return planError(err), nil
			}
			if _, err = store.SyncExecution(ctx, queued); err != nil {
				return planError(err), nil
			}
			if err = queue.Retry(ctx, queued.ID, attempts); err != nil {
				return planError(err), nil
			}
			queued, err = queue.Get(ctx, queued.ID)
			if err != nil {
				return planError(err), nil
			}
			planned, err = store.SyncExecution(ctx, queued)
			return planResult(planned, err), nil
		},
	})
}

func syncPlanTask(ctx context.Context, store *taskplan.Store, queue *taskqueue.Queue, planned taskplan.Task) (taskplan.Task, error) {
	if queue == nil || planned.Execution == nil {
		return planned, nil
	}
	queued, err := queue.Get(ctx, planned.Execution.QueueID)
	if errors.Is(err, taskqueue.ErrNotFound) {
		return planned, nil // Reserved before publication; plan_run can retry.
	}
	if err != nil {
		return taskplan.Task{}, err
	}
	return store.SyncExecution(ctx, queued)
}

func planContext(tc types.ToolContext) context.Context {
	if tc.Context != nil {
		return tc.Context
	}
	return context.Background()
}

func planString(args map[string]any, key string, required bool) (string, error) {
	value, exists := args[key]
	if !exists {
		if required {
			return "", fmt.Errorf("%s é obrigatório", key)
		}
		return "", nil
	}
	text, ok := value.(string)
	if !ok || (required && strings.TrimSpace(text) == "") {
		return "", fmt.Errorf("%s precisa ser texto não vazio", key)
	}
	return text, nil
}

func optionalPlanString(args map[string]any, key string) (*string, error) {
	if _, exists := args[key]; !exists {
		return nil, nil
	}
	value, err := planString(args, key, false)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func planIDs(args map[string]any, key string) ([]string, error) {
	raw, exists := args[key]
	if !exists {
		return nil, nil
	}
	switch values := raw.(type) {
	case []string:
		return values, nil
	case []any:
		ids := make([]string, 0, len(values))
		for _, item := range values {
			id, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s precisa conter apenas IDs em texto", key)
			}
			ids = append(ids, id)
		}
		return ids, nil
	default:
		return nil, fmt.Errorf("%s precisa ser uma lista de IDs", key)
	}
}

func planInt(raw any) (int, bool) {
	switch value := raw.(type) {
	case int:
		return value, true
	case float64:
		integer := int(value)
		return integer, float64(integer) == value
	default:
		return 0, false
	}
}

func planError(err error) types.ToolResult {
	return types.ToolResult{Content: "Plano: " + err.Error(), IsError: true}
}

func planResult(value any, err error) types.ToolResult {
	if err != nil {
		return planError(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return planError(err)
	}
	return types.ToolResult{Content: string(raw)}
}
