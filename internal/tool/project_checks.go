package tool

import (
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/projectchecks"
	"github.com/costa/polypod/internal/types"
)

// RegisterProjectChecksTool lets coding agents run the same verifiers as /verify.
func RegisterProjectChecksTool(reg *Registry, root string) {
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "run_project_checks" },
		DescriptionFn: func() string {
			return "Executa os verificadores configurados em .polypod/verifiers.yaml e devolve o resultado de cada um."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
		},
		ValidateFn:      func(map[string]any) types.ValidationResult { return types.ValidationResult{Valid: true} },
		IsReadOnlyFn:    func(map[string]any) bool { return false },
		IsConcurrencyFn: func(map[string]any) bool { return false },
		ExecuteFn: func(ctx types.ToolContext, _ map[string]any) (types.ToolResult, error) {
			results, err := projectchecks.Run(ctx.Context, effectiveWorkingDir(ctx, root), "manual")
			if err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Falha ao carregar verificadores: %v", err), IsError: true}, nil
			}
			if len(results) == 0 {
				return types.ToolResult{Content: "Nenhum verificador configurado em .polypod/verifiers.yaml."}, nil
			}
			var out strings.Builder
			failed := false
			for _, result := range results {
				status := "PASSOU"
				if result.Err != nil {
					status = "FALHOU"
					failed = true
				}
				fmt.Fprintf(&out, "%s: %s\n", result.Name, status)
				if result.Err != nil {
					fmt.Fprintf(&out, "erro: %v\n", result.Err)
				}
				if strings.TrimSpace(result.Output) != "" {
					out.WriteString(result.Output)
					out.WriteByte('\n')
				}
			}
			return types.ToolResult{Content: strings.TrimSpace(out.String()), IsError: failed}, nil
		},
	})
}
