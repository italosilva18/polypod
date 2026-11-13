package tool

import (
	"strings"

	"github.com/costa/polypod/internal/projectchecks"
	"github.com/costa/polypod/internal/types"
)

// RegisterImpactTool registers `code_impact`, a read-only dependency and test
// radius analyzer for symbols before or during refactors.
func RegisterImpactTool(reg *Registry, root string) {
	if reg == nil {
		return
	}
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "code_impact" },
		DescriptionFn: func() string {
			return "Analisa o raio de impacto de um símbolo (função, método, struct, interface ou classe) no repositório: localiza chamadores externos em produção e arquivos de teste correlatos antes ou depois de refatorações."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{
						"type":        "string",
						"description": "Nome exato do símbolo exportado/função/tipo a inspecionar (ex.: CompleteWithTools, ProviderFactory)",
					},
					"exclude_file": map[string]any{
						"type":        "string",
						"description": "Opcional: arquivo de definição a excluir da lista de chamadores externos",
					},
				},
				"required": []string{"symbol"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			sym, _ := args["symbol"].(string)
			if strings.TrimSpace(sym) == "" {
				return types.ValidationResult{Valid: false, Message: "symbol é obrigatório"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			sym, _ := args["symbol"].(string)
			exclude, _ := args["exclude_file"].(string)
			report := projectchecks.QuerySymbolImpact(ctx.Context, effectiveWorkingDir(ctx, root), sym, exclude)
			return types.ToolResult{Content: report}, nil
		},
	})
}
