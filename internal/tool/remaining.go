package tool

import (
	"fmt"
	"strings"
	"time"

	"github.com/costa/polypod/internal/types"
)

// RegisterRemainingTools registers remaining tools with simplified implementations.
// Note: task/todo tools are registered separately via RegisterTaskTools in tasktools.go.
// Note: plan mode tools are registered separately via RegisterPlanModeTools in plantools.go.
func RegisterRemainingTools(reg *Registry) {
	RegisterGrokTool(reg)
	RegisterDocumentTool(reg)
	registerSleepTool(reg)
	// ask_user_question is now registered by RegisterAskUserTool (askuser.go)
	registerBriefTool(reg)
	registerToolSearchTool(reg)
}

// --- Sleep ---
func registerSleepTool(reg *Registry) {
	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return "sleep" },
		DescriptionFn: func() string { return "Aguardar por um periodo de tempo" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"duration": map[string]any{"type": "number", "description": "Duracao em segundos"},
				},
				"required": []string{"duration"},
			}
		},
		IsReadOnlyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			d, _ := args["duration"].(float64)
			time.Sleep(time.Duration(d) * time.Second)
			return types.ToolResult{Content: fmt.Sprintf("Aguardou %.1f segundos", d)}, nil
		},
	})
}

// --- Brief (send message to user) ---
func registerBriefTool(reg *Registry) {
	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return "send_user_message" },
		DescriptionFn: func() string { return "Enviar mensagem visivel ao usuario" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message": map[string]any{"type": "string", "description": "Mensagem para o usuario"},
				},
				"required": []string{"message"},
			}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			msg, _ := args["message"].(string)
			return types.ToolResult{Content: msg}, nil
		},
	})
}

// --- ToolSearch ---
func registerToolSearchTool(reg *Registry) {
	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return "tool_search" },
		DescriptionFn: func() string { return "Buscar ferramentas por palavra-chave" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Termo de busca"},
				},
				"required": []string{"query"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			query := strings.ToLower(args["query"].(string))
			var matches []string
			for _, name := range reg.List() {
				if strings.Contains(strings.ToLower(name), query) {
					matches = append(matches, name)
				}
			}
			if len(matches) == 0 {
				return types.ToolResult{Content: "Nenhuma ferramenta encontrada."}, nil
			}
			return types.ToolResult{Content: fmt.Sprintf("Ferramentas encontradas: %s", strings.Join(matches, ", "))}, nil
		},
	})
}
