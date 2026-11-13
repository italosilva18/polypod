package tool

import (
	"strings"

	"github.com/costa/polypod/internal/codesearch"
	"github.com/costa/polypod/internal/types"
)

// RegisterCodeSearchTool exposes opt-in semantic search to the model.
func RegisterCodeSearchTool(reg *Registry, engine *codesearch.Engine, root string) {
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "code_context_search" },
		DescriptionFn: func() string {
			return "Seleciona trechos relevantes do código por caminho, símbolos, conteúdo, alterações e embeddings (híbrido por padrão quando configurado), com orçamento estimado de tokens."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"query": map[string]any{"type": "string"}, "token_budget": map[string]any{"type": "integer", "minimum": 64, "maximum": 16000},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 20}, "semantic": map[string]any{"type": "boolean"},
				"ranking": map[string]any{"type": "string", "enum": []string{"hybrid", "lexical", "map", "semantic"}},
			}, "required": []string{"query"}}
		},
		ValidateFn: func(args map[string]any) types.ValidationResult {
			query, ok := args["query"].(string)
			if !ok || strings.TrimSpace(query) == "" {
				return types.ValidationResult{Message: "query obrigatória"}
			}
			return types.ValidationResult{Valid: true}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		IsEnabledFn:     func() bool { cfg, err := codesearch.LoadConfig(root); return err == nil && cfg.Enabled },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			limit, budget := 5, 2000
			if n, ok := args["limit"].(float64); ok {
				limit = int(n)
			}
			if n, ok := args["limit"].(int); ok {
				limit = n
			}
			if n, ok := args["token_budget"].(float64); ok {
				budget = int(n)
			}
			if n, ok := args["token_budget"].(int); ok {
				budget = n
			}
			semantic := engine != nil
			if explicit, ok := args["semantic"].(bool); ok {
				semantic = explicit
			}
			ranking, _ := args["ranking"].(string)
			matches, err := engine.SearchContext(ctx.Context, effectiveWorkingDir(ctx, root), args["query"].(string),
				codesearch.ContextOptions{Limit: limit, TokenBudget: budget, Semantic: semantic, Ranking: ranking})
			if err != nil {
				return types.ToolResult{Content: err.Error(), IsError: true}, nil
			}
			return types.ToolResult{Content: codesearch.FormatContext(matches)}, nil
		},
	})
	if engine == nil {
		return
	}
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "code_semantic_search" },
		DescriptionFn: func() string {
			return "Busca trechos de código por significado no índice opcional do projeto. Retorna caminhos e linhas."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Descrição do código a localizar"},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
			}, "required": []string{"query"}}
		},
		ValidateFn: func(args map[string]any) types.ValidationResult {
			query, ok := args["query"].(string)
			if !ok || strings.TrimSpace(query) == "" {
				return types.ValidationResult{Valid: false, Message: "query obrigatória"}
			}
			return types.ValidationResult{Valid: true}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		IsEnabledFn: func() bool {
			cfg, err := codesearch.LoadConfig(root)
			return err == nil && cfg.Enabled
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			limit := 8
			switch value := args["limit"].(type) {
			case float64:
				limit = int(value)
			case int:
				limit = value
			}
			matches, err := engine.Search(ctx.Context, effectiveWorkingDir(ctx, root), args["query"].(string), limit)
			if err != nil {
				return types.ToolResult{Content: err.Error(), IsError: true}, nil
			}
			return types.ToolResult{Content: codesearch.Format(matches)}, nil
		},
	})
}
