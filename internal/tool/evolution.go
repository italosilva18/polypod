package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/evolution"
	"github.com/costa/polypod/internal/types"
)

// RegisterEvolutionTool exposes the reviewed learning cycle. onApply can add
// the skill to the active agent immediately; the filesystem watcher separately
// loads the new SKILL.md into the skill/tool registries within its poll window.
// A repeated apply calls onApply again, making runtime activation retryable.
func RegisterEvolutionTool(reg *Registry, store *evolution.Store, onApply func(name string) error) {
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "skill_evolution" },
		DescriptionFn: func() string {
			return "Planejar verificação SHA-256 independente para tarefa/rotina, consultar resultados " +
				"verificados, agrupar padrões repetidos, revisar rascunhos " +
				"SKILL.md e aplicar ou descartar explicitamente. O agente não certifica o próprio sucesso."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action":          map[string]any{"type": "string", "enum": []string{"plan", "patterns", "propose", "get", "list", "review", "apply"}},
					"source_kind":     map[string]any{"type": "string", "enum": []string{"task", "routine"}},
					"subject_id":      map[string]any{"type": "string", "description": "ID estável da tarefa ou rotina a verificar"},
					"kind":            map[string]any{"type": "string", "enum": []string{"file_sha256"}},
					"path":            map[string]any{"type": "string", "description": "Caminho absoluto do arquivo a verificar"},
					"expected_sha256": map[string]any{"type": "string", "description": "SHA-256 exato do estado esperado após execução"},
					"pattern_key":     map[string]any{"type": "string"},
					"draft_id":        map[string]any{"type": "string"},
					"decision":        map[string]any{"type": "string", "enum": []string{"approve", "discard"}},
					"reviewer":        map[string]any{"type": "string"},
					"revised_body":    map[string]any{"type": "string", "description": "SKILL.md inteiro revisado; opcional em approve"},
					"confirm":         map[string]any{"type": "boolean", "description": "Confirma a aplicação no diretório de skills"},
					"limit":           map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
				},
				"required": []string{"action"},
			}
		},
		ValidateFn: func(args map[string]any) types.ValidationResult {
			if err := validateEvolutionArgs(args); err != nil {
				return types.ValidationResult{Message: err.Error()}
			}
			return types.ValidationResult{Valid: true}
		},
		IsConcurrencyFn: func(map[string]any) bool { return false },
		IsReadOnlyFn: func(args map[string]any) bool {
			switch args["action"] {
			case "patterns", "get", "list":
				return true
			}
			return false
		},
		IsDestructiveFn: func(args map[string]any) bool {
			return args["action"] == "review" || args["action"] == "apply"
		},
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if store == nil {
				return types.ToolResult{Content: "Evolução de skills indisponível.", IsError: true}, nil
			}
			if err := validateEvolutionArgs(args); err != nil {
				return types.ToolResult{Content: err.Error(), IsError: true}, nil
			}
			ctx := tc.Context
			if ctx == nil {
				ctx = context.Background()
			}
			var value any
			var err error
			switch args["action"] {
			case "plan":
				value, err = store.RegisterVerificationPlan(ctx, evolution.VerificationPlanInput{
					SourceKind: args["source_kind"].(string), SubjectID: args["subject_id"].(string),
					PatternKey: args["pattern_key"].(string), Kind: stringArg(args, "kind"),
					Path: args["path"].(string), ExpectedSHA256: args["expected_sha256"].(string),
				})
			case "patterns":
				value, err = store.Patterns(ctx)
			case "propose":
				value, err = store.Propose(ctx, args["pattern_key"].(string))
			case "get":
				value, err = store.GetDraft(ctx, args["draft_id"].(string))
			case "list":
				limit, _ := integerArg(args, "limit", 50, 200)
				value, err = store.ListDrafts(ctx, limit)
			case "review":
				body, _ := args["revised_body"].(string)
				value, err = store.Review(ctx, args["draft_id"].(string), args["decision"].(string),
					args["reviewer"].(string), body)
			case "apply":
				var draft evolution.Draft
				draft, err = store.Apply(ctx, args["draft_id"].(string))
				if err == nil && onApply != nil {
					err = onApply(draft.TargetSkill)
				}
				value = draft
			}
			if err != nil {
				return types.ToolResult{Content: "Evolução: " + err.Error(), IsError: true}, nil
			}
			raw, err := json.Marshal(value)
			if err != nil {
				return types.ToolResult{Content: "Não foi possível apresentar o resultado: " + err.Error(), IsError: true}, nil
			}
			return types.ToolResult{Content: string(raw)}, nil
		},
	})
}

func validateEvolutionArgs(args map[string]any) error {
	action, ok := args["action"].(string)
	if !ok || action == "" {
		return errors.New("action é obrigatório")
	}
	switch action {
	case "plan":
		for _, key := range []string{"source_kind", "subject_id", "pattern_key", "path", "expected_sha256"} {
			if err := requiredStringArg(args, key); err != nil {
				return err
			}
		}
		if kind, exists := args["kind"]; exists {
			if kind != evolution.FileSHA256Check {
				return errors.New("kind deve ser file_sha256")
			}
		}
	case "propose":
		return requiredStringArg(args, "pattern_key")
	case "get":
		return requiredStringArg(args, "draft_id")
	case "list":
		_, err := integerArg(args, "limit", 50, 200)
		return err
	case "review":
		for _, key := range []string{"draft_id", "decision", "reviewer"} {
			if err := requiredStringArg(args, key); err != nil {
				return err
			}
		}
		if _, ok := args["revised_body"]; ok {
			if _, ok := args["revised_body"].(string); !ok {
				return errors.New("revised_body deve ser texto")
			}
		}
	case "apply":
		if err := requiredStringArg(args, "draft_id"); err != nil {
			return err
		}
		if confirm, ok := args["confirm"].(bool); !ok || !confirm {
			return errors.New("apply exige confirm=true após revisão")
		}
	case "patterns":
	default:
		return fmt.Errorf("action desconhecida: %s", action)
	}
	return nil
}

func requiredStringArg(args map[string]any, key string) error {
	value, ok := args[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s é obrigatório", key)
	}
	return nil
}
