package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/costa/polypod/internal/audit"
	"github.com/costa/polypod/internal/types"
)

// RegisterAuditTool exposes the durable metadata trail and file checkpoints.
// The central orchestrator still applies its normal permission policy.
func RegisterAuditTool(reg *Registry, store *audit.Store) {
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "audit" },
		DescriptionFn: func() string {
			return "Consultar auditoria de ferramentas e criar, inspecionar ou restaurar checkpoints de arquivos. " +
				"Para restaurar, inspecione antes e informe confirm=true e current_sha256 atual."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action":         map[string]any{"type": "string", "enum": []string{"recent", "list", "checkpoint", "inspect", "restore"}},
					"path":           map[string]any{"type": "string", "description": "Caminho do arquivo para checkpoint"},
					"checkpoint_id":  map[string]any{"type": "string", "description": "ID retornado por checkpoint/list"},
					"limit":          map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
					"confirm":        map[string]any{"type": "boolean", "description": "Confirma explicitamente a restauração"},
					"current_sha256": map[string]any{"type": "string", "description": "Valor de inspect para verificar mudanças antes de restaurar"},
				},
				"required": []string{"action"},
			}
		},
		ValidateFn: func(args map[string]any) types.ValidationResult {
			if err := validateAuditArgs(args); err != nil {
				return types.ValidationResult{Message: err.Error()}
			}
			return types.ValidationResult{Valid: true}
		},
		IsConcurrencyFn: func(map[string]any) bool { return false },
		IsReadOnlyFn: func(args map[string]any) bool {
			return args["action"] == "recent" || args["action"] == "list" || args["action"] == "inspect"
		},
		IsDestructiveFn: func(args map[string]any) bool { return args["action"] == "restore" },
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if store == nil {
				return types.ToolResult{Content: "Auditoria indisponível.", IsError: true}, nil
			}
			if err := validateAuditArgs(args); err != nil {
				return types.ToolResult{Content: err.Error(), IsError: true}, nil
			}
			ctx := tc.Context
			if ctx == nil {
				ctx = context.Background()
			}
			var value any
			var err error
			switch args["action"] {
			case "recent":
				limit, _ := integerArg(args, "limit", 50, 200)
				value, err = store.Recent(ctx, limit)
			case "list":
				limit, _ := integerArg(args, "limit", 50, 200)
				value, err = store.ListCheckpoints(ctx, limit)
			case "checkpoint":
				path := strings.TrimSpace(args["path"].(string))
				if !filepath.IsAbs(path) {
					path = filepath.Join(tc.WorkingDir, path)
				}
				value, err = store.CreateCheckpoint(ctx, path)
			case "inspect":
				value, err = store.InspectCheckpoint(ctx, args["checkpoint_id"].(string))
			case "restore":
				value, err = store.RestoreCheckpoint(ctx, args["checkpoint_id"].(string), args["current_sha256"].(string))
			}
			if err != nil {
				return types.ToolResult{Content: "Auditoria: " + err.Error(), IsError: true}, nil
			}
			raw, err := json.Marshal(value)
			if err != nil {
				return types.ToolResult{Content: "Não foi possível apresentar o resultado: " + err.Error(), IsError: true}, nil
			}
			return types.ToolResult{Content: string(raw)}, nil
		},
	})
}

func validateAuditArgs(args map[string]any) error {
	action, ok := args["action"].(string)
	if !ok || action == "" {
		return errors.New("action é obrigatório")
	}
	switch action {
	case "recent", "list":
		if _, err := integerArg(args, "limit", 50, 200); err != nil {
			return err
		}
	case "checkpoint":
		path, ok := args["path"].(string)
		if !ok || strings.TrimSpace(path) == "" {
			return errors.New("path é obrigatório para checkpoint")
		}
	case "inspect", "restore":
		id, ok := args["checkpoint_id"].(string)
		if !ok || strings.TrimSpace(id) == "" {
			return errors.New("checkpoint_id é obrigatório")
		}
		if action == "restore" {
			if confirm, ok := args["confirm"].(bool); !ok || !confirm {
				return errors.New("restore exige confirm=true")
			}
			if sha, ok := args["current_sha256"].(string); !ok || strings.TrimSpace(sha) == "" {
				return errors.New("restore exige current_sha256 de inspect")
			}
		}
	default:
		return fmt.Errorf("action desconhecida: %s", action)
	}
	return nil
}
