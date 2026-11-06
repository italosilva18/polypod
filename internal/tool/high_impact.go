package tool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/costa/polypod/internal/constants"
	"github.com/costa/polypod/internal/types"
)

// RegisterHighImpactTools registers SendMessage, Skill, and Config tools
func RegisterHighImpactTools(reg *Registry, deps HighImpactToolDeps) {
	registerSendMessageTool(reg, deps)
	registerSkillTool(reg, deps)
	registerConfigTool(reg, deps)
}

// HighImpactToolDeps provides dependencies for high-impact tools.
type HighImpactToolDeps struct {
	MessageSender func(fromAgent types.AgentID, toAgent types.AgentID, message string) error
	SkillExecutor func(name string, args map[string]any) (string, error)
	ConfigGetter  func(key string) string
	ConfigSetter  func(key, value string) error
	ModeGetter    func() types.PermissionMode
	ModeSetter    func(mode types.PermissionMode)
	AgentRegistry interface {
		Get(name string) (any, bool)
		List() []string
	}
}

// SendMessageTool enables inter-agent communication
func registerSendMessageTool(reg *Registry, deps HighImpactToolDeps) {
	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return constants.ToolSendMessage },
		DescriptionFn: func() string { return "Enviar mensagem para outro agente no mesmo sessão" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"to_agent":  map[string]any{"type": "string", "description": "ID do agente destino"},
					"message":   map[string]any{"type": "string", "description": "Conteúdo da mensagem"},
					"broadcast": map[string]any{"type": "boolean", "description": "Se verdadeiro, envia para todos os agentes"},
				},
				"required": []string{"to_agent", "message"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if deps.MessageSender == nil {
				return types.ToolResult{Content: "MessageSender dependency not configured", IsError: true}, nil
			}

			toAgentStr, _ := args["to_agent"].(string)
			message, _ := args["message"].(string)
			broadcast, _ := args["broadcast"].(bool)

			if toAgentStr == "" {
				return types.ToolResult{Content: "to_agent is required", IsError: true}, nil
			}
			if message == "" {
				return types.ToolResult{Content: "message is required", IsError: true}, nil
			}

			toAgent := types.AgentID(toAgentStr)

			if broadcast {
				// Broadcast to all agents
				if deps.AgentRegistry != nil {
					agentNames := deps.AgentRegistry.List()
					{
						var sent []string
						var failed []string
						for _, agentName := range agentNames {
							if _, ok := deps.AgentRegistry.Get(agentName); ok {
								err := deps.MessageSender(ctx.AgentID, types.AgentID(agentName), message)
								if err != nil {
									failed = append(failed, fmt.Sprintf("%s: %v", agentName, err))
								} else {
									sent = append(sent, agentName)
								}
							}
						}
						result := fmt.Sprintf("Broadcast sent to %d agents: %v", len(sent), sent)
						if len(failed) > 0 {
							result += fmt.Sprintf("\nFailed to send to %d agents: %v", len(failed), failed)
						}
						return types.ToolResult{Content: result}, nil
					}
				}
				return types.ToolResult{Content: "AgentRegistry not available for broadcast", IsError: true}, nil
			} else {
				// Direct message
				if err := deps.MessageSender(ctx.AgentID, toAgent, message); err != nil {
					return types.ToolResult{Content: fmt.Sprintf("Failed to send message: %v", err), IsError: true}, nil
				}
				return types.ToolResult{Content: fmt.Sprintf("Message sent to %s", toAgentStr)}, nil
			}
		},
	})
}

// SkillTool enables execution of slash-commands/skills
func registerSkillTool(reg *Registry, deps HighImpactToolDeps) {
	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return constants.ToolSkillSearch },
		DescriptionFn: func() string { return "Executar um comando slash ou skill disponível" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":  map[string]any{"type": "string", "description": "Nome da skill/comando"},
					"args":  map[string]any{"type": "object", "description": "Argumentos para a skill"},
					"agent": map[string]any{"type": "string", "description": "Agente para executar a skill (opcional)"},
				},
				"required": []string{"name"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return false }, // Skills can modify state
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			name, _ := args["name"].(string)
			if name == "" {
				return types.ValidationResult{Valid: false, Message: "skill name is required"}
			}
			if deps.SkillExecutor != nil {
				if _, err := deps.SkillExecutor(name, nil); err != nil {
					return types.ValidationResult{Valid: false, Message: fmt.Sprintf("skill '%s' not found or not executable", name)}
				}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if deps.SkillExecutor == nil {
				return types.ToolResult{Content: "SkillExecutor dependency not configured", IsError: true}, nil
			}

			name, _ := args["name"].(string)
			argsAny, _ := args["args"].(map[string]any)
			agentName, _ := args["agent"].(string)

			// Determine which agent to use for execution
			if agentName != "" {
				if _, ok := deps.AgentRegistry.Get(agentName); !ok {
					return types.ToolResult{Content: fmt.Sprintf("agent '%s' not found", agentName), IsError: true}, nil
				}
			}

			// Execute the skill
			result, err := deps.SkillExecutor(name, argsAny)
			if err != nil {
				return types.ToolResult{Content: fmt.Sprintf("skill execution failed: %v", err), IsError: true}, nil
			}

			return types.ToolResult{Content: result}, nil
		},
	})
}

// ConfigTool enables getting/setting configuration
func registerConfigTool(reg *Registry, deps HighImpactToolDeps) {
	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return constants.ToolConfig },
		DescriptionFn: func() string { return "Obter ou definir configurações do sistema" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{"type": "string", "description": "Ação: get, set, list, reset"},
					"key":    map[string]any{"type": "string", "description": "Chave de configuração"},
					"value":  map[string]any{"type": "string", "description": "Valor para definir (apenas para set)"},
				},
				"required": []string{"action"},
			}
		},
		IsReadOnlyFn: func(args map[string]any) bool {
			action, _ := args["action"].(string)
			return action == "get" || action == "list"
		},
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if deps.ConfigGetter == nil || deps.ConfigSetter == nil {
				return types.ToolResult{Content: "Config dependencies not configured", IsError: true}, nil
			}

			action, _ := args["action"].(string)
			switch action {
			case "get":
				key, _ := args["key"].(string)
				if key == "" {
					return types.ToolResult{Content: "key is required for get action", IsError: true}, nil
				}
				value := deps.ConfigGetter(key)
				return types.ToolResult{Content: fmt.Sprintf("%s=%s", key, value)}, nil

			case "set":
				key, _ := args["key"].(string)
				value, _ := args["value"].(string)
				if key == "" {
					return types.ToolResult{Content: "key is required for set action", IsError: true}, nil
				}
				if value == "" {
					return types.ToolResult{Content: "value is required for set action", IsError: true}, nil
				}
				if err := deps.ConfigSetter(key, value); err != nil {
					return types.ToolResult{Content: fmt.Sprintf("failed to set config: %v", err), IsError: true}, nil
				}
				return types.ToolResult{Content: fmt.Sprintf("%s=%s (set)", key, value)}, nil

			case "list":
				// For simplicity, return a few common config items
				// In a full implementation, this would iterate over all known keys
				commonKeys := []string{
					"model", "temperature", "max_tokens", "theme", "permission_mode",
				}
				var result strings.Builder
				result.WriteString("Current configuration:\n")
				for _, key := range commonKeys {
					value := deps.ConfigGetter(key)
					result.WriteString(fmt.Sprintf("  %s=%s\n", key, value))
				}
				return types.ToolResult{Content: result.String()}, nil

			case "reset":
				// Reset to defaults - implementation would depend on specific config system
				return types.ToolResult{Content: "Configuration reset to defaults (implementation pending)"}, nil

			default:
				return types.ToolResult{Content: fmt.Sprintf("unknown action: %s. Valid actions: get, set, list, reset", action), IsError: true}, nil
			}
		},
	})
}

// Ensure we're using the imports
var _ context.Context
var _ time.Duration
