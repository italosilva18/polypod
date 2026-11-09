package tool

import (
	"context"
	"encoding/json"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/costa/polypod/internal/types"
)

// Advanced MCP capabilities are optional so older in-memory resource stores
// and test backends keep their existing behavior.
type mcpTemplateBackend interface {
	ListResourceTemplates(server string) map[string][]*mcpsdk.ResourceTemplate
}

func registerMCPAdvancedResourceTools(reg *Registry) {
	reg.RegisterDef(newListMCPResourceTemplatesTool())
	reg.RegisterDef(newMCPResourceSubscriptionTool("mcp_resource_subscribe", true))
	reg.RegisterDef(newMCPResourceSubscriptionTool("mcp_resource_unsubscribe", false))
	reg.RegisterDef(newListMCPResourceUpdatesTool())
}

func newListMCPResourceTemplatesTool() types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return "list_mcp_resource_templates" },
		DescriptionFn: func() string {
			return "Listar modelos de URI de recursos dos servidores MCP conectados; informe server para filtrar."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"server": map[string]any{"type": "string", "description": "Servidor MCP opcional"},
			}}
		},
		IsReadOnlyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(_ types.ToolContext, args map[string]any) (types.ToolResult, error) {
			backend, ok := GetMCPResourceStore().getBackend().(mcpTemplateBackend)
			if !ok {
				return types.ToolResult{IsError: true, Content: "catalogo MCP de templates indisponivel"}, nil
			}
			server, _ := args["server"].(string)
			catalog := backend.ListResourceTemplates(server)
			data, err := json.Marshal(catalog)
			if err != nil {
				return types.ToolResult{IsError: true, Content: fmt.Sprintf("codificando templates MCP: %v", err)}, nil
			}
			return types.ToolResult{Content: string(data)}, nil
		},
	}
}

func newMCPResourceSubscriptionTool(name string, subscribe bool) types.ToolDef {
	verb := "assinar"
	if !subscribe {
		verb = "cancelar assinatura de"
	}
	return types.ToolDef{
		NameFn: func() string { return name },
		DescriptionFn: func() string {
			return "Solicitar ao servidor MCP para " + verb + " atualizacoes de um recurso por URI."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"server": map[string]any{"type": "string", "description": "Nome do servidor MCP"},
				"uri":    map[string]any{"type": "string", "description": "URI concreta do recurso"},
			}, "required": []string{"server", "uri"}}
		},
		IsReadOnlyFn: func(map[string]any) bool { return false },
		ExecuteFn: func(toolCtx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			backend, ok := GetMCPResourceStore().getBackend().(interface {
				SubscribeResource(context.Context, string, string) error
				UnsubscribeResource(context.Context, string, string) error
			})
			if !ok {
				return types.ToolResult{IsError: true, Content: "assinaturas MCP indisponiveis"}, nil
			}
			server, _ := args["server"].(string)
			uri, _ := args["uri"].(string)
			if server == "" || uri == "" {
				return types.ToolResult{IsError: true, Content: "server e uri sao obrigatorios"}, nil
			}
			ctx := toolCtx.Context
			if ctx == nil {
				ctx = context.Background()
			}
			var err error
			if subscribe {
				err = backend.SubscribeResource(ctx, server, uri)
			} else {
				err = backend.UnsubscribeResource(ctx, server, uri)
			}
			if err != nil {
				return types.ToolResult{IsError: true, Content: err.Error()}, nil
			}
			return types.ToolResult{Content: fmt.Sprintf("assinatura MCP atualizada: %s %s", server, uri)}, nil
		},
	}
}

func newListMCPResourceUpdatesTool() types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return "list_mcp_resource_updates" },
		DescriptionFn: func() string {
			return "Listar notificacoes recentes de recursos MCP e assinaturas ativas; releia o recurso para obter seu conteudo atual."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"server": map[string]any{"type": "string", "description": "Servidor MCP opcional"},
			}}
		},
		IsReadOnlyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(_ types.ToolContext, args map[string]any) (types.ToolResult, error) {
			backend, ok := GetMCPResourceStore().getBackend().(interface {
				ResourceActivity(server string) any
			})
			if !ok {
				return types.ToolResult{IsError: true, Content: "historico de recursos MCP indisponivel"}, nil
			}
			server, _ := args["server"].(string)
			data, err := json.Marshal(backend.ResourceActivity(server))
			if err != nil {
				return types.ToolResult{IsError: true, Content: fmt.Sprintf("codificando eventos MCP: %v", err)}, nil
			}
			return types.ToolResult{Content: string(data)}, nil
		},
	}
}
