package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

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

// Manager manages multiple MCP server connections using the official SDK.
type Manager struct {
	mu        sync.RWMutex
	sessions  map[string]*mcpsdk.ClientSession
	tools     map[string][]*mcpsdk.Tool
	client    *mcpsdk.Client
	logger    *slog.Logger
	reconnect *ReconnectManager
}

// NewManager creates an MCP manager.
func NewManager(logger *slog.Logger) *Manager {
	client := mcpsdk.NewClient(&mcpsdk.Implementation{
		Name:    "polypod",
		Version: "0.6.0",
	}, nil)

	return &Manager{
		sessions:  make(map[string]*mcpsdk.ClientSession),
		tools:     make(map[string][]*mcpsdk.Tool),
		client:    client,
		logger:    logger,
		reconnect: NewReconnectManager(DefaultReconnectConfig()),
	}
}

// Connect connects to an MCP server using the given config.
func (m *Manager) Connect(ctx context.Context, cfg ServerConfig) error {
	var transport mcpsdk.Transport

	switch cfg.Transport {
	case "stdio":
		args := cfg.Args
		cmd := exec.Command(cfg.Command, args...)
		if len(cfg.Env) > 0 {
			for k, v := range cfg.Env {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
		}
		transport = &mcpsdk.CommandTransport{Command: cmd}
	case "sse":
		transport = &mcpsdk.SSEClientTransport{Endpoint: cfg.URL}
	default:
		return fmt.Errorf("mcp: transport desconhecido %q (use 'stdio' ou 'sse')", cfg.Transport)
	}

	var session *mcpsdk.ClientSession
	err := m.reconnect.Connect(ctx, cfg.Name, func(innerCtx context.Context) error {
		var dialErr error
		session, dialErr = m.client.Connect(innerCtx, transport, nil)
		return dialErr
	})
	if err != nil {
		return fmt.Errorf("mcp connect %s: %w", cfg.Name, err)
	}

	// List tools
	result, err := session.ListTools(ctx, nil)
	if err != nil {
		session.Close()
		return fmt.Errorf("mcp list tools %s: %w", cfg.Name, err)
	}

	m.mu.Lock()
	m.sessions[cfg.Name] = session
	m.tools[cfg.Name] = result.Tools
	m.mu.Unlock()

	m.logger.Info("mcp: servidor conectado",
		"name", cfg.Name,
		"tools", len(result.Tools),
	)

	return nil
}

// Disconnect closes a server connection.
func (m *Manager) Disconnect(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[name]
	if !ok {
		return fmt.Errorf("mcp: servidor %q nao conectado", name)
	}

	err := session.Close()
	delete(m.sessions, name)
	delete(m.tools, name)
	return err
}

// RegisterTools registers all MCP tools from connected servers into a skill registry.
func (m *Manager) RegisterTools(reg *skill.Registry) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for serverName, tools := range m.tools {
		session := m.sessions[serverName]
		for _, tool := range tools {
			s := m.toolToSkill(serverName, tool, session)
			reg.Register(s)
			count++
		}
	}
	return count
}

func (m *Manager) toolToSkill(serverName string, tool *mcpsdk.Tool, session *mcpsdk.ClientSession) *skill.Skill {
	skillName := fmt.Sprintf("mcp_%s_%s", serverName, tool.Name)

	params := jsonschema.Definition{
		Type:       jsonschema.Object,
		Properties: make(map[string]jsonschema.Definition),
	}

	// Convert MCP inputSchema (any) to jsonschema.Definition
	if tool.InputSchema != nil {
		if schemaMap, ok := tool.InputSchema.(map[string]any); ok {
			if props, ok := schemaMap["properties"].(map[string]any); ok {
				for name := range props {
					params.Properties[name] = jsonschema.Definition{
						Type: jsonschema.String,
					}
				}
			}
		}
	}

	return &skill.Skill{
		Name:        skillName,
		Description: fmt.Sprintf("[MCP:%s] %s", serverName, tool.Description),
		Parameters:  params,
		Execute: func(args map[string]string) (string, error) {
			mcpArgs := make(map[string]any, len(args))
			for k, v := range args {
				mcpArgs[k] = v
			}

			result, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{
				Name:      tool.Name,
				Arguments: mcpArgs,
			})
			if err != nil {
				return "", fmt.Errorf("mcp call %s/%s: %w", serverName, tool.Name, err)
			}

			if result.IsError {
				var errTexts []string
				for _, c := range result.Content {
					if tc, ok := c.(*mcpsdk.TextContent); ok {
						errTexts = append(errTexts, tc.Text)
					}
				}
				return "", fmt.Errorf("mcp tool error: %s", strings.Join(errTexts, "; "))
			}

			var texts []string
			for _, c := range result.Content {
				if tc, ok := c.(*mcpsdk.TextContent); ok {
					texts = append(texts, tc.Text)
				}
			}
			return strings.Join(texts, "\n"), nil
		},
	}
}

// CallTool implements MCPCaller so that Manager can be passed to WrapMCPTool.
func (m *Manager) CallTool(ctx context.Context, server, name string, args map[string]any) (any, bool, error) {
	m.mu.RLock()
	session, ok := m.sessions[server]
	m.mu.RUnlock()
	if !ok {
		return nil, false, fmt.Errorf("mcp: servidor %q nao conectado", server)
	}

	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return nil, false, fmt.Errorf("mcp call %s/%s: %w", server, name, err)
	}

	if result.IsError {
		var errTexts []string
		for _, c := range result.Content {
			if tc, ok := c.(*mcpsdk.TextContent); ok {
				errTexts = append(errTexts, tc.Text)
			}
		}
		return nil, true, fmt.Errorf("mcp tool error: %s", strings.Join(errTexts, "; "))
	}

	var texts []string
	for _, c := range result.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			texts = append(texts, tc.Text)
		}
	}
	return strings.Join(texts, "\n"), false, nil
}

// RegisterWrappedTools registers all MCP tools from connected servers into both
// the skill registry (legacy) and a types.ToolRegistry (new-style tools).
// Pass nil for typesReg to skip types.Tool registration.
func (m *Manager) RegisterWrappedTools(reg *skill.Registry, typesReg ToolRegistry) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for serverName, tools := range m.tools {
		session := m.sessions[serverName]
		for _, tool := range tools {
			// Legacy skill registration.
			s := m.toolToSkill(serverName, tool, session)
			reg.Register(s)

			// New-style types.Tool registration via WrapMCPTool.
			if typesReg != nil {
				schema, _ := tool.InputSchema.(map[string]any)
				wrapped := WrapMCPTool(m, serverName, tool.Name, tool.Description, schema)
				typesReg.Register(wrapped)
			}

			count++
		}
	}
	return count
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
	m.reconnect.StopAll()

	m.mu.Lock()
	defer m.mu.Unlock()

	var lastErr error
	for name, session := range m.sessions {
		if err := session.Close(); err != nil {
			lastErr = err
			m.logger.Warn("mcp close error", "server", name, "error", err)
		}
	}
	m.sessions = make(map[string]*mcpsdk.ClientSession)
	m.tools = make(map[string][]*mcpsdk.Tool)
	return lastErr
}
