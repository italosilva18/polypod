package tool

import (
	"fmt"
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
	RegisterToolSearchTool(reg)
	RegisterToolOutputTool(reg)
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
