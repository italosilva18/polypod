package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/skill"
	"github.com/costa/polypod/internal/types"
)

// RegisterPluginTools exposes local plugin lifecycle operations to the agent.
// The manager owns registry updates; the registry's on-change callback keeps
// skill adapters and agent tool lists synchronized during the same process.
func RegisterPluginTools(reg *Registry, manager *skill.PluginManager) {
	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return "list_local_plugins" },
		DescriptionFn: func() string { return "Lista plugins locais, seu estado e as skills carregadas no processo atual." },
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{}}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(tc types.ToolContext, _ map[string]any) (types.ToolResult, error) {
			if manager == nil {
				return pluginToolError("Gerenciador de plugins indisponível"), nil
			}
			plugins, err := manager.List(pluginToolContext(tc))
			result := map[string]any{"plugins": plugins}
			if err != nil {
				result["warning"] = err.Error()
			}
			return pluginToolJSON(result), nil
		},
	})
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "set_local_plugin_enabled" },
		DescriptionFn: func() string {
			return "Ativa ou desativa um plugin local pelo ID retornado por list_local_plugins e sincroniza suas skills imediatamente."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"id":      map[string]any{"type": "string", "description": "ID do plugin retornado por list_local_plugins"},
				"enabled": map[string]any{"type": "boolean", "description": "true para ativar, false para desativar"},
			}, "required": []string{"id", "enabled"}}
		},
		ValidateFn: func(args map[string]any) types.ValidationResult {
			id, ok := args["id"].(string)
			if !ok || strings.TrimSpace(id) == "" {
				return types.ValidationResult{Message: "id do plugin é obrigatório"}
			}
			if _, ok := args["enabled"].(bool); !ok {
				return types.ValidationResult{Message: "enabled deve ser booleano"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if manager == nil {
				return pluginToolError("Gerenciador de plugins indisponível"), nil
			}
			id, _ := args["id"].(string)
			enabled, _ := args["enabled"].(bool)
			info, err := manager.SetEnabled(pluginToolContext(tc), id, enabled)
			if err != nil {
				output := pluginToolJSON(map[string]any{"plugin": info, "error": fmt.Sprintf("Plugin %s: %v", id, err)})
				output.IsError = true
				return output, nil
			}
			return pluginToolJSON(info), nil
		},
	})
	reg.RegisterDef(types.ToolDef{
		NameFn:          func() string { return "install_local_plugin" },
		IsDestructiveFn: func(map[string]any) bool { return true },
		DescriptionFn: func() string {
			return "Instala um plugin de um diretorio local ou URL Git HTTPS/SSH explicita, valida o manifesto e as skills e ativa no processo atual."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"source": map[string]any{"type": "string", "description": "Diretorio local ou URL Git HTTPS/SSH sem credenciais embutidas"},
			}, "required": []string{"source"}}
		},
		ValidateFn: func(args map[string]any) types.ValidationResult {
			if source, ok := args["source"].(string); !ok || strings.TrimSpace(source) == "" {
				return types.ValidationResult{Message: "source deve ser um diretorio ou URL Git"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if manager == nil {
				return pluginToolError("Gerenciador de plugins indisponível"), nil
			}
			source, _ := args["source"].(string)
			info, err := manager.Install(pluginToolContext(tc), source)
			if err != nil {
				return pluginToolError(fmt.Sprintf("Instalação do plugin falhou: %v", err)), nil
			}
			return pluginToolJSON(info), nil
		},
	})
	reg.RegisterDef(types.ToolDef{
		NameFn:          func() string { return "update_local_plugin" },
		IsDestructiveFn: func(map[string]any) bool { return true },
		DescriptionFn: func() string {
			return "Atualiza um plugin pelo ID de list_local_plugins usando diretorio local ou URL Git HTTPS/SSH explicita."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"id":     map[string]any{"type": "string", "description": "ID retornado por list_local_plugins"},
				"source": map[string]any{"type": "string", "description": "Diretorio local ou URL Git HTTPS/SSH sem credenciais embutidas"},
			}, "required": []string{"id", "source"}}
		},
		ValidateFn: func(args map[string]any) types.ValidationResult {
			if id, ok := args["id"].(string); !ok || strings.TrimSpace(id) == "" {
				return types.ValidationResult{Message: "id do plugin é obrigatório"}
			}
			if source, ok := args["source"].(string); !ok || strings.TrimSpace(source) == "" {
				return types.ValidationResult{Message: "source deve ser um diretorio ou URL Git"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if manager == nil {
				return pluginToolError("Gerenciador de plugins indisponível"), nil
			}
			id, _ := args["id"].(string)
			source, _ := args["source"].(string)
			info, err := manager.Update(pluginToolContext(tc), id, source)
			if err != nil {
				return pluginToolError(fmt.Sprintf("Atualização do plugin falhou: %v", err)), nil
			}
			return pluginToolJSON(info), nil
		},
	})
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "reload_local_plugins" },
		DescriptionFn: func() string {
			return "Recarrega um plugin local pelo ID ou reconcilia todos, incluindo plugins adicionados e removidos."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"id": map[string]any{"type": "string", "description": "ID opcional; omita para reconciliar todos"},
			}}
		},
		ValidateFn: func(args map[string]any) types.ValidationResult {
			if id, exists := args["id"]; exists && id != nil {
				if _, ok := id.(string); !ok {
					return types.ValidationResult{Message: "id deve ser texto"}
				}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if manager == nil {
				return pluginToolError("Gerenciador de plugins indisponível"), nil
			}
			id, _ := args["id"].(string)
			count, err := manager.Reload(pluginToolContext(tc), strings.TrimSpace(id))
			result := map[string]any{"loaded_skills": count}
			if err != nil {
				result["error"] = err.Error()
				output := pluginToolJSON(result)
				output.IsError = true
				return output, nil
			}
			return pluginToolJSON(result), nil
		},
	})
}

func pluginToolContext(tc types.ToolContext) context.Context {
	if tc.Context != nil {
		return tc.Context
	}
	return context.Background()
}

func pluginToolError(message string) types.ToolResult {
	return types.ToolResult{Content: message, IsError: true}
}

func pluginToolJSON(value any) types.ToolResult {
	raw, err := json.Marshal(value)
	if err != nil {
		return pluginToolError("Não foi possível formatar o resultado de plugins")
	}
	return types.ToolResult{Content: string(raw)}
}
