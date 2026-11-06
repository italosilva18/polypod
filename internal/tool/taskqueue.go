package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/costa/polypod/internal/taskqueue"
	"github.com/costa/polypod/internal/types"
)

// RegisterTaskQueueTool gives the agent durable, prioritized one-off work.
// The queue's handler must be wired to the complete agent loop at startup.
func RegisterTaskQueueTool(reg *Registry, queue *taskqueue.Queue) {
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "task_queue" },
		DescriptionFn: func() string {
			return "Criar e acompanhar tarefas pontuais persistentes com prioridade, tentativas e recuperacao apos reinicio."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action":              map[string]any{"type": "string", "enum": []string{"create", "list", "get", "history", "cancel", "retry", "status"}},
					"id":                  map[string]any{"type": "string", "description": "ID da tarefa para get, history, cancel ou retry"},
					"title":               map[string]any{"type": "string", "description": "Nome curto da tarefa"},
					"prompt":              map[string]any{"type": "string", "description": "Objetivo completo para o agente, incluindo verificacao"},
					"priority":            map[string]any{"type": "integer", "minimum": -100, "maximum": 100},
					"max_attempts":        map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
					"next_run_at":         map[string]any{"type": "string", "description": "Horario RFC3339 opcional"},
					"status":              map[string]any{"type": "string", "enum": []string{"pending", "running", "succeeded", "failed", "canceled"}},
					"limit":               map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
					"additional_attempts": map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
				},
				"required": []string{"action"},
			}
		},
		ValidateFn: func(args map[string]any) types.ValidationResult {
			if err := validateTaskQueueArgs(args); err != nil {
				return types.ValidationResult{Message: err.Error()}
			}
			return types.ValidationResult{Valid: true}
		},
		IsReadOnlyFn: func(args map[string]any) bool {
			return args["action"] == "list" || args["action"] == "get" || args["action"] == "history" || args["action"] == "status"
		},
		IsConcurrencyFn: func(map[string]any) bool { return false },
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if queue == nil {
				return types.ToolResult{Content: "Fila de tarefas indisponível.", IsError: true}, nil
			}
			if err := validateTaskQueueArgs(args); err != nil {
				return types.ToolResult{Content: err.Error(), IsError: true}, nil
			}
			ctx := tc.Context
			if ctx == nil {
				ctx = context.Background()
			}
			var value any
			var err error
			switch args["action"] {
			case "status":
				value, err = queue.Overview(ctx)
			case "create":
				priority, _ := signedIntArg(args, "priority", 0, -100, 100)
				maxAttempts, _ := integerArg(args, "max_attempts", 3, 20)
				var next time.Time
				if raw, ok := args["next_run_at"].(string); ok && raw != "" {
					next, _ = time.Parse(time.RFC3339, raw)
				}
				value, err = queue.Create(ctx, taskqueue.CreateInput{Title: stringArg(args, "title"),
					Prompt: args["prompt"].(string), Priority: priority, MaxAttempts: maxAttempts,
					Channel: "agent", Owner: string(tc.AgentID), NextRunAt: next})
			case "list":
				limit, _ := integerArg(args, "limit", 50, 200)
				value, err = queue.List(ctx, taskqueue.Status(stringArg(args, "status")), limit)
			case "get":
				value, err = queue.Get(ctx, args["id"].(string))
			case "history":
				value, err = queue.History(ctx, args["id"].(string))
			case "cancel":
				err = queue.Cancel(ctx, args["id"].(string))
				value = map[string]any{"status": "canceled"}
			case "retry":
				additional, _ := integerArg(args, "additional_attempts", 3, 20)
				err = queue.Retry(ctx, args["id"].(string), additional)
				value = map[string]any{"status": "pending"}
			}
			if err != nil {
				return types.ToolResult{Content: "Fila de tarefas: " + err.Error(), IsError: true}, nil
			}
			raw, err := json.Marshal(value)
			if err != nil {
				return types.ToolResult{Content: err.Error(), IsError: true}, nil
			}
			return types.ToolResult{Content: string(raw)}, nil
		},
	})
}

func validateTaskQueueArgs(args map[string]any) error {
	action, ok := args["action"].(string)
	if !ok || action == "" {
		return errors.New("action é obrigatório")
	}
	switch action {
	case "create":
		prompt, ok := args["prompt"].(string)
		if !ok || strings.TrimSpace(prompt) == "" {
			return errors.New("prompt é obrigatório")
		}
		if _, err := signedIntArg(args, "priority", 0, -100, 100); err != nil {
			return err
		}
		if _, err := integerArg(args, "max_attempts", 3, 20); err != nil {
			return err
		}
		if raw, exists := args["next_run_at"]; exists {
			text, ok := raw.(string)
			if !ok {
				return errors.New("next_run_at deve ser RFC3339")
			}
			if _, err := time.Parse(time.RFC3339, text); err != nil {
				return fmt.Errorf("next_run_at invalido: %w", err)
			}
		}
	case "status":
	case "list":
		if _, err := integerArg(args, "limit", 50, 200); err != nil {
			return err
		}
		if rawValue, exists := args["status"]; exists {
			raw, ok := rawValue.(string)
			if !ok {
				return errors.New("status deve ser texto")
			}
			if raw == "" {
				return nil
			}
			switch taskqueue.Status(raw) {
			case taskqueue.Pending, taskqueue.Running, taskqueue.Succeeded, taskqueue.Failed, taskqueue.Canceled:
			default:
				return errors.New("status invalido")
			}
		}
	case "get", "history", "cancel", "retry":
		id, ok := args["id"].(string)
		if !ok || strings.TrimSpace(id) == "" {
			return errors.New("id é obrigatório")
		}
		if action == "retry" {
			if _, err := integerArg(args, "additional_attempts", 3, 20); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("action desconhecida: %s", action)
	}
	return nil
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return value
}

func signedIntArg(args map[string]any, key string, fallback, minimum, maximum int) (int, error) {
	raw, exists := args[key]
	if !exists {
		return fallback, nil
	}
	var n float64
	switch v := raw.(type) {
	case float64:
		n = v
	case int:
		n = float64(v)
	default:
		return 0, fmt.Errorf("%s deve ser inteiro", key)
	}
	if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) || n < float64(minimum) || n > float64(maximum) {
		return 0, fmt.Errorf("%s deve estar entre %d e %d", key, minimum, maximum)
	}
	return int(n), nil
}
