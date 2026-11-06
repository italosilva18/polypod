package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/skill"
	"github.com/costa/polypod/internal/types"
)

// RegisterSkillCatalogTools exposes only the checked-in local PolyClaw
// catalog. Downloads from arbitrary registries are deliberately absent.
func RegisterSkillCatalogTools(reg *Registry, catalog *skill.Catalog) {
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "find_skills" },
		DescriptionFn: func() string {
			return "Busca skills no catálogo local do PolyClaw e nas skills ativas do Polypod. Mostra quando uma adaptação já está ativa. Use install_skill com slug apenas para uma skill ainda não instalada."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Termos de busca; use * para listar todas"},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
			}, "required": []string{"query"}}
		},
		ValidateFn: func(args map[string]any) types.ValidationResult {
			query, ok := args["query"].(string)
			if !ok || strings.TrimSpace(query) == "" {
				return types.ValidationResult{Message: "query é obrigatória"}
			}
			if _, err := integerArg(args, "limit", 5, 20); err != nil {
				return types.ValidationResult{Message: err.Error()}
			}
			return types.ValidationResult{Valid: true}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if catalog == nil {
				return types.ToolResult{Content: "Catálogo de skills indisponível.", IsError: true}, nil
			}
			query, ok := args["query"].(string)
			if !ok || strings.TrimSpace(query) == "" {
				return types.ToolResult{Content: "query é obrigatória", IsError: true}, nil
			}
			limit, err := integerArg(args, "limit", 5, 20)
			if err != nil {
				return types.ToolResult{Content: err.Error(), IsError: true}, nil
			}
			ctx := tc.Context
			if ctx == nil {
				ctx = context.Background()
			}
			entries, findErr := catalog.Find(ctx, query, limit)
			if findErr != nil && len(entries) == 0 {
				return types.ToolResult{Content: "Busca de skills: " + findErr.Error(), IsError: true}, nil
			}
			result := map[string]any{"results": entries}
			if findErr != nil {
				result["warning"] = findErr.Error()
			}
			raw, err := json.Marshal(result)
			if err != nil {
				return types.ToolResult{Content: "Não foi possível formatar skills.", IsError: true}, nil
			}
			return types.ToolResult{Content: string(raw)}, nil
		},
	})
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "install_skill" },
		DescriptionFn: func() string {
			return "Instala uma skill do catálogo PolyClaw local sob .polypod/skills por cópia validada e ativação atômica. Skills já adaptadas retornam a adaptação ativa; não baixa nem sobrescreve arquivos."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"slug": map[string]any{"type": "string", "description": "Identificador retornado por find_skills"},
			}, "required": []string{"slug"}}
		},
		ValidateFn: func(args map[string]any) types.ValidationResult {
			slug, ok := args["slug"].(string)
			if !ok || strings.TrimSpace(slug) == "" {
				return types.ValidationResult{Message: "slug é obrigatório"}
			}
			return types.ValidationResult{Valid: true}
		},
		IsConcurrencyFn: func(map[string]any) bool { return false },
		IsDestructiveFn: func(map[string]any) bool { return true },
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if catalog == nil {
				return types.ToolResult{Content: "Catálogo de skills indisponível.", IsError: true}, nil
			}
			slug, ok := args["slug"].(string)
			if !ok || strings.TrimSpace(slug) == "" {
				return types.ToolResult{Content: "slug é obrigatório", IsError: true}, nil
			}
			ctx := tc.Context
			if ctx == nil {
				ctx = context.Background()
			}
			result, err := catalog.Install(ctx, slug)
			if err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Instalação de skill: %v", err), IsError: true}, nil
			}
			raw, err := json.Marshal(result)
			if err != nil {
				return types.ToolResult{Content: "Não foi possível formatar instalação.", IsError: true}, nil
			}
			return types.ToolResult{Content: string(raw)}, nil
		},
	})
}
