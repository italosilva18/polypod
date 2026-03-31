package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// RegisterSkills registers MCP management skills.
func RegisterSkills(reg *skill.Registry, mgr *Manager) {
	reg.Register(&skill.Skill{
		Name:        "mcp_list_servers",
		Description: "Listar servidores MCP conectados e suas ferramentas",
		Parameters: jsonschema.Definition{
			Type:       jsonschema.Object,
			Properties: map[string]jsonschema.Definition{},
		},
		Execute: func(args map[string]string) (string, error) {
			servers := mgr.ListServers()
			if len(servers) == 0 {
				return "Nenhum servidor MCP conectado.", nil
			}
			var sb strings.Builder
			for name, tools := range servers {
				fmt.Fprintf(&sb, "## %s (%d ferramentas)\n", name, len(tools))
				for _, t := range tools {
					fmt.Fprintf(&sb, "  - %s\n", t)
				}
				sb.WriteByte('\n')
			}
			return sb.String(), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "mcp_connect",
		Description: "Conectar a um servidor MCP via stdio (comando + args) ou SSE (URL)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"name":      {Type: jsonschema.String, Description: "Nome para identificar o servidor"},
				"transport": {Type: jsonschema.String, Description: "Tipo de transporte: 'stdio' ou 'sse'"},
				"command":   {Type: jsonschema.String, Description: "Comando para stdio (ex: npx -y @modelcontextprotocol/server-filesystem)"},
				"args":      {Type: jsonschema.String, Description: "Argumentos do comando separados por espaco"},
				"url":       {Type: jsonschema.String, Description: "URL para SSE/HTTP"},
			},
			Required: []string{"name", "transport"},
		},
		Execute: func(args map[string]string) (string, error) {
			cfg := ServerConfig{
				Name:      args["name"],
				Transport: args["transport"],
				Command:   args["command"],
				URL:       args["url"],
			}
			if a := args["args"]; a != "" {
				cfg.Args = strings.Fields(a)
			}
			if err := mgr.Connect(context.Background(), cfg); err != nil {
				return "", err
			}
			servers := mgr.ListServers()
			tools := servers[cfg.Name]
			return fmt.Sprintf("Conectado ao servidor MCP '%s' com %d ferramentas: %s",
				cfg.Name, len(tools), strings.Join(tools, ", ")), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "mcp_disconnect",
		Description: "Desconectar de um servidor MCP",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"name": {Type: jsonschema.String, Description: "Nome do servidor a desconectar"},
			},
			Required: []string{"name"},
		},
		Execute: func(args map[string]string) (string, error) {
			if err := mgr.Disconnect(args["name"]); err != nil {
				return "", err
			}
			return fmt.Sprintf("Servidor MCP '%s' desconectado.", args["name"]), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "mcp_call",
		Description: "Chamar diretamente uma ferramenta MCP por servidor e nome",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"server":    {Type: jsonschema.String, Description: "Nome do servidor MCP"},
				"tool":      {Type: jsonschema.String, Description: "Nome da ferramenta"},
				"arguments": {Type: jsonschema.String, Description: "Argumentos em JSON (ex: {\"path\":\"/tmp\"})"},
			},
			Required: []string{"server", "tool"},
		},
		Execute: func(args map[string]string) (string, error) {
			mgr.mu.RLock()
			session, ok := mgr.sessions[args["server"]]
			mgr.mu.RUnlock()
			if !ok {
				return "", fmt.Errorf("servidor MCP '%s' nao conectado", args["server"])
			}

			var mcpArgs map[string]any
			if a := args["arguments"]; a != "" {
				if err := json.Unmarshal([]byte(a), &mcpArgs); err != nil {
					return "", fmt.Errorf("argumentos JSON invalidos: %w", err)
				}
			}

			result, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{
				Name:      args["tool"],
				Arguments: mcpArgs,
			})
			if err != nil {
				return "", err
			}

			var texts []string
			for _, c := range result.Content {
				if tc, ok := c.(*mcpsdk.TextContent); ok {
					texts = append(texts, tc.Text)
				}
			}
			return strings.Join(texts, "\n"), nil
		},
	})
}
