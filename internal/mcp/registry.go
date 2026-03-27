package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// ServerConfig describes how to connect to an MCP server.
type ServerConfig struct {
	Name      string            `yaml:"name"`
	Transport string            `yaml:"transport"` // "stdio" or "sse"
	Command   string            `yaml:"command"`
	Args      []string          `yaml:"args"`
	URL       string            `yaml:"url"`
	Env       map[string]string `yaml:"env"`
}

// Manager manages multiple MCP server connections.
type Manager struct {
	mu      sync.RWMutex
	clients map[string]*Client
	tools   map[string][]MCPTool // server name -> tools
	logger  *slog.Logger
}

// NewManager creates an MCP manager.
func NewManager(logger *slog.Logger) *Manager {
	return &Manager{
		clients: make(map[string]*Client),
		tools:   make(map[string][]MCPTool),
		logger:  logger,
	}
}

// Connect connects to an MCP server using the given config.
func (m *Manager) Connect(ctx context.Context, cfg ServerConfig) error {
	var transport Transport
	var err error

	switch cfg.Transport {
	case "stdio":
		var envSlice []string
		for k, v := range cfg.Env {
			envSlice = append(envSlice, k+"="+v)
		}
		transport, err = NewStdioTransport(cfg.Command, cfg.Args, envSlice)
		if err != nil {
			return fmt.Errorf("mcp connect %s: %w", cfg.Name, err)
		}
	case "sse", "http":
		transport = NewSSETransport(cfg.URL)
	default:
		return fmt.Errorf("mcp: transport desconhecido %q (use 'stdio' ou 'sse')", cfg.Transport)
	}

	client := NewClient(transport, m.logger)

	if _, err := client.Initialize(ctx); err != nil {
		transport.Close()
		return fmt.Errorf("mcp initialize %s: %w", cfg.Name, err)
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		client.Close()
		return fmt.Errorf("mcp list tools %s: %w", cfg.Name, err)
	}

	m.mu.Lock()
	m.clients[cfg.Name] = client
	m.tools[cfg.Name] = tools
	m.mu.Unlock()

	m.logger.Info("mcp: servidor conectado",
		"name", cfg.Name,
		"tools", len(tools),
	)

	return nil
}

// Disconnect closes a server connection.
func (m *Manager) Disconnect(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	client, ok := m.clients[name]
	if !ok {
		return fmt.Errorf("mcp: servidor %q nao conectado", name)
	}

	err := client.Close()
	delete(m.clients, name)
	delete(m.tools, name)
	return err
}

// RegisterTools registers all MCP tools from connected servers into a skill registry.
func (m *Manager) RegisterTools(reg *skill.Registry) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for serverName, tools := range m.tools {
		client := m.clients[serverName]
		for _, tool := range tools {
			s := m.toolToSkill(serverName, tool, client)
			reg.Register(s)
			count++
		}
	}
	return count
}

func (m *Manager) toolToSkill(serverName string, tool MCPTool, client *Client) *skill.Skill {
	skillName := fmt.Sprintf("mcp_%s_%s", serverName, tool.Name)

	params := jsonschema.Definition{
		Type:       jsonschema.Object,
		Properties: make(map[string]jsonschema.Definition),
	}

	// Convert MCP inputSchema to jsonschema.Definition
	if props, ok := tool.InputSchema["properties"].(map[string]any); ok {
		for name, prop := range props {
			propMap, ok := prop.(map[string]any)
			if !ok {
				continue
			}
			def := jsonschema.Definition{Type: jsonschema.String}
			if desc, ok := propMap["description"].(string); ok {
				def.Description = desc
			}
			params.Properties[name] = def
		}
	}
	if required, ok := tool.InputSchema["required"].([]any); ok {
		for _, r := range required {
			if s, ok := r.(string); ok {
				params.Required = append(params.Required, s)
			}
		}
	}

	return &skill.Skill{
		Name:        skillName,
		Description: fmt.Sprintf("[MCP:%s] %s", serverName, tool.Description),
		Parameters:  params,
		Execute: func(args map[string]string) (string, error) {
			// Convert string args to any for MCP
			mcpArgs := make(map[string]any, len(args))
			for k, v := range args {
				mcpArgs[k] = v
			}

			result, err := client.CallTool(context.Background(), tool.Name, mcpArgs)
			if err != nil {
				return "", fmt.Errorf("mcp call %s/%s: %w", serverName, tool.Name, err)
			}

			if result.IsError {
				var errTexts []string
				for _, c := range result.Content {
					errTexts = append(errTexts, c.Text)
				}
				return "", fmt.Errorf("mcp tool error: %s", strings.Join(errTexts, "; "))
			}

			var texts []string
			for _, c := range result.Content {
				if c.Text != "" {
					texts = append(texts, c.Text)
				}
			}
			return strings.Join(texts, "\n"), nil
		},
	}
}

// ListServers returns info about connected servers and their tools.
func (m *Manager) ListServers() map[string][]string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string][]string)
	for name, tools := range m.tools {
		var toolNames []string
		for _, t := range tools {
			toolNames = append(toolNames, t.Name)
		}
		result[name] = toolNames
	}
	return result
}

// Close disconnects all servers.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var lastErr error
	for name, client := range m.clients {
		if err := client.Close(); err != nil {
			lastErr = err
			m.logger.Warn("mcp close error", "server", name, "error", err)
		}
	}
	m.clients = make(map[string]*Client)
	m.tools = make(map[string][]MCPTool)
	return lastErr
}

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
				sb.WriteString(fmt.Sprintf("## %s (%d ferramentas)\n", name, len(tools)))
				for _, t := range tools {
					sb.WriteString(fmt.Sprintf("  - %s\n", t))
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
			client, ok := mgr.clients[args["server"]]
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

			result, err := client.CallTool(context.Background(), args["tool"], mcpArgs)
			if err != nil {
				return "", err
			}

			var texts []string
			for _, c := range result.Content {
				if c.Text != "" {
					texts = append(texts, c.Text)
				}
			}
			return strings.Join(texts, "\n"), nil
		},
	})
}
