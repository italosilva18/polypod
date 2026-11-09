package tool

import (
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/tooloutput"
	"github.com/costa/polypod/internal/types"
)

// RegisterToolOutputTool exposes retained results through the ordinary
// registry, orchestrator and permission flow.
func RegisterToolOutputTool(reg *Registry) {
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "tool_output" },
		DescriptionFn: func() string {
			return "Ler uma pagina de um resultado de ferramenta grande preservado em cache privado. " +
				"Use o ID informado no resultado e continue pelo proximo offset em bytes."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":     map[string]any{"type": "string", "description": "ID informado no resultado original"},
					"offset": map[string]any{"type": "integer", "minimum": 0, "description": "Posicao inicial em bytes"},
					"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": tooloutput.ReadLimit, "description": "Bytes por pagina"},
				},
				"required": []string{"id"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			id, ok := args["id"].(string)
			if !ok || id == "" {
				return types.ValidationResult{Valid: false, Message: "id e obrigatorio"}
			}
			if offset := argInt(args, "offset", 0); offset < 0 {
				return types.ValidationResult{Valid: false, Message: "offset deve ser positivo"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			id, _ := args["id"].(string)
			offset := int64(argInt(args, "offset", 0))
			limit := int64(argInt(args, "limit", 8192))
			page, start, total, err := tooloutput.Read(id, offset, limit)
			if err != nil {
				return types.ToolResult{IsError: true, Content: fmt.Sprintf("Erro ao ler resultado: %v", err)}, nil
			}
			next := start + int64(len(page))
			content := fmt.Sprintf("Resultado %s, bytes %d-%d de %d; proximo offset: %d\n%s",
				id, start, next, total, next, strings.ToValidUTF8(string(page), "�"))
			return types.ToolResult{Content: content}, nil
		},
	})
}
