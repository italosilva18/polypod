package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/costa/polypod/internal/evolution"
	"github.com/costa/polypod/internal/taskqueue"
	"github.com/costa/polypod/internal/types"
)

// RegisterTaskQueueTool gives the agent durable, prioritized one-off work.
// The queue's handler must be wired to the complete agent loop at startup.
func RegisterTaskQueueTool(reg *Registry, queue *taskqueue.Queue, evolutionStores ...*evolution.Store) {
	var evolutionStore *evolution.Store
	if len(evolutionStores) != 0 {
		evolutionStore = evolutionStores[0]
	}
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "task_queue" },
		DescriptionFn: func() string {
			return "Criar e acompanhar tarefas pontuais persistentes. Opcionalmente, registre antes do despacho uma verificação de arquivo, HTTP ou SQLite para aprender com o resultado real."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action":                       map[string]any{"type": "string", "enum": []string{"create", "list", "get", "history", "cancel", "retry", "status"}},
					"id":                           map[string]any{"type": "string", "description": "ID da tarefa para get, history, cancel ou retry"},
					"title":                        map[string]any{"type": "string", "description": "Nome curto da tarefa"},
					"prompt":                       map[string]any{"type": "string", "description": "Objetivo completo para o agente, incluindo verificacao"},
					"priority":                     map[string]any{"type": "integer", "minimum": -100, "maximum": 100},
					"max_attempts":                 map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
					"next_run_at":                  map[string]any{"type": "string", "description": "Horario RFC3339 opcional"},
					"verification_kind":            map[string]any{"type": "string", "enum": []string{"file_sha256", "http_status", "http_json", "sqlite_scalar"}},
					"verification_path":            map[string]any{"type": "string", "description": "Arquivo/SQLite absoluto ou URL HTTP(S) a verificar"},
					"verification_selector":        map[string]any{"type": "string", "description": "Ponteiro JSON RFC 6901 ou SELECT escalar"},
					"verification_sha256":          map[string]any{"type": "string", "description": "SHA-256 exato esperado após a tarefa"},
					"verification_expected_json":   map[string]any{"type": "string", "description": "Valor JSON esperado; somente o hash é persistido"},
					"verification_expected_status": map[string]any{"type": "integer", "minimum": 100, "maximum": 599},
					"verification_pattern":         map[string]any{"type": "string", "description": "Categoria estável para agrupar resultados verificados"},
					"status":                       map[string]any{"type": "string", "enum": []string{"pending", "running", "succeeded", "failed", "canceled"}},
					"limit":                        map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
					"additional_attempts":          map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
				},
				"required": []string{"action"},
			}
		},
		ValidateFn: func(args map[string]any) types.ValidationResult {
			if err := validateTaskQueueArgs(args); err != nil {
				return types.ValidationResult{Message: err.Error()}
			}
			if args["action"] == "create" && verificationRequested(args) && evolutionStore == nil {
				return types.ValidationResult{Message: "verificação de tarefa indisponível"}
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
				input := taskqueue.CreateInput{Title: stringArg(args, "title"),
					Prompt: args["prompt"].(string), Priority: priority, MaxAttempts: maxAttempts,
					Channel: "agent", Owner: string(tc.AgentID), NextRunAt: next}
				if verificationRequested(args) {
					if evolutionStore == nil {
						err = errors.New("verificação de tarefa indisponível")
						break
					}
					input.ID = taskqueue.NewTaskID()
					status, _ := integerArg(args, "verification_expected_status", 0, 599)
					plan, planErr := evolutionStore.RegisterVerificationPlan(ctx, evolution.VerificationPlanInput{
						SourceKind: "task", SubjectID: input.ID,
						PatternKey: args["verification_pattern"].(string), Kind: stringArg(args, "verification_kind"),
						Path: args["verification_path"].(string), Selector: stringArg(args, "verification_selector"),
						ExpectedSHA256: stringArg(args, "verification_sha256"),
						ExpectedJSON:   stringArg(args, "verification_expected_json"), ExpectedStatus: status,
					})
					err = planErr
					if err != nil {
						break
					}
					// The attempt history uses millisecond timestamps. Ensure its
					// start is strictly later than the plan's millisecond.
					minimum := time.UnixMilli(plan.CreatedAt).Add(2 * time.Millisecond)
					if input.NextRunAt.Before(minimum) {
						input.NextRunAt = minimum
					}
				}
				value, err = queue.Create(ctx, input)
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
		if verificationRequested(args) {
			for _, key := range []string{"verification_path", "verification_pattern"} {
				if err := requiredStringArg(args, key); err != nil {
					return err
				}
			}
			kind := stringArg(args, "verification_kind")
			if kind == "" {
				kind = evolution.FileSHA256Check
			}
			switch kind {
			case evolution.FileSHA256Check:
				if err := requiredStringArg(args, "verification_sha256"); err != nil {
					return err
				}
				path := strings.TrimSpace(args["verification_path"].(string))
				if !filepath.IsAbs(path) || strings.ContainsRune(path, '\x00') {
					return errors.New("verification_path deve ser caminho absoluto de arquivo")
				}
				digest, err := hex.DecodeString(strings.TrimSpace(args["verification_sha256"].(string)))
				if err != nil || len(digest) != sha256.Size {
					return errors.New("verification_sha256 deve ser SHA-256 hexadecimal completo")
				}
			case evolution.HTTPStatusCheck, evolution.HTTPJSONCheck:
				status, err := integerArg(args, "verification_expected_status", 0, 599)
				if err != nil || status < 100 {
					return errors.New("verification_expected_status deve estar entre 100 e 599")
				}
				if kind == evolution.HTTPJSONCheck {
					if err := requiredStringArg(args, "verification_expected_json"); err != nil {
						return err
					}
				}
			case evolution.SQLiteScalarCheck:
				for _, key := range []string{"verification_selector", "verification_expected_json"} {
					if err := requiredStringArg(args, key); err != nil {
						return err
					}
				}
			default:
				return errors.New("verification_kind desconhecido")
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

func verificationRequested(args map[string]any) bool {
	for _, key := range []string{"verification_kind", "verification_path", "verification_selector", "verification_sha256", "verification_expected_json", "verification_expected_status", "verification_pattern"} {
		if _, exists := args[key]; exists {
			return true
		}
	}
	return false
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
