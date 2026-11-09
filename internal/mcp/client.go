package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"sync"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sashabaranov/go-openai/jsonschema"

	"github.com/costa/polypod/internal/skill"
	"github.com/costa/polypod/internal/types"
)

// ServerConfig describes how to connect to an MCP server.
type ServerConfig struct {
	Name      string            `yaml:"name"`
	Transport string            `yaml:"transport"` // "stdio", "sse", or "http" (Streamable HTTP)
	Command   string            `yaml:"command"`
	Args      []string          `yaml:"args"`
	URL       string            `yaml:"url"`
	Env       map[string]string `yaml:"env"`
	Headers   map[string]string `yaml:"headers"`
}

// Manager manages multiple MCP server connections using the official SDK.
type Manager struct {
	mu        sync.RWMutex
	sessions  map[string]*mcpsdk.ClientSession
	tools     map[string][]*mcpsdk.Tool
	resources map[string][]*mcpsdk.Resource
	prompts   map[string][]*mcpsdk.Prompt
	skillReg  *skill.Registry
	closed    bool
	client    *mcpsdk.Client
	logger    *slog.Logger
	reconnect *ReconnectManager
}

// NewManager creates an MCP manager.
func NewManager(logger *slog.Logger) *Manager {
	m := &Manager{
		sessions:  make(map[string]*mcpsdk.ClientSession),
		tools:     make(map[string][]*mcpsdk.Tool),
		resources: make(map[string][]*mcpsdk.Resource),
		prompts:   make(map[string][]*mcpsdk.Prompt),
		logger:    logger,
		reconnect: NewReconnectManager(DefaultReconnectConfig()),
	}
	m.client = mcpsdk.NewClient(&mcpsdk.Implementation{
		Name:    "polypod",
		Version: "0.6.0",
	}, &mcpsdk.ClientOptions{
		ToolListChangedHandler: func(_ context.Context, req *mcpsdk.ToolListChangedRequest) {
			go m.refreshNotification(req.Session, "tools")
		},
		ResourceListChangedHandler: func(_ context.Context, req *mcpsdk.ResourceListChangedRequest) {
			go m.refreshNotification(req.Session, "resources")
		},
		PromptListChangedHandler: func(_ context.Context, req *mcpsdk.PromptListChangedRequest) {
			go m.refreshNotification(req.Session, "prompts")
		},
	})
	return m
}

// Connect connects to an MCP server using the given config.
func (m *Manager) Connect(ctx context.Context, cfg ServerConfig) error {
	m.mu.RLock()
	_, alreadyConnected := m.sessions[cfg.Name]
	m.mu.RUnlock()
	if alreadyConnected {
		if err := m.Disconnect(cfg.Name); err != nil {
			m.logger.Warn("mcp previous session close failed", "server", cfg.Name, "error", err)
		}
	}
	newTransport := func() (mcpsdk.Transport, error) {
		var transport mcpsdk.Transport

		switch cfg.Transport {
		case "", "stdio":
			args := cfg.Args
			cmd := exec.Command(cfg.Command, args...)
			if len(cfg.Env) > 0 {
				cmd.Env = os.Environ()
				for k, v := range cfg.Env {
					cmd.Env = append(cmd.Env, k+"="+v)
				}
			}
			transport = &mcpsdk.CommandTransport{Command: cmd}
		case "sse":
			transport = &mcpsdk.SSEClientTransport{Endpoint: cfg.URL, HTTPClient: mcpHTTPClient(cfg.Headers)}
		case "http", "streamable-http":
			transport = &mcpsdk.StreamableClientTransport{Endpoint: cfg.URL, HTTPClient: mcpHTTPClient(cfg.Headers)}
		default:
			return nil, fmt.Errorf("mcp: transport desconhecido %q (use 'stdio', 'sse' ou 'http')", cfg.Transport)
		}

		return transport, nil
	}
	if _, err := newTransport(); err != nil {
		return err
	}

	var session *mcpsdk.ClientSession
	err := m.reconnect.Connect(ctx, cfg.Name, func(innerCtx context.Context) error {
		transport, err := newTransport()
		if err != nil {
			return err
		}
		var dialErr error
		session, dialErr = m.client.Connect(innerCtx, transport, nil)
		return dialErr
	})
	if err != nil {
		return fmt.Errorf("mcp connect %s: %w", cfg.Name, err)
	}

	// Servers may expose only resources or prompts. Query tools only when the
	// server advertises that capability.
	var items []*mcpsdk.Tool
	if initialization := session.InitializeResult(); initialization != nil && initialization.Capabilities.Tools != nil {
		items, err = listAllTools(ctx, session)
		if err != nil {
			session.Close()
			return fmt.Errorf("mcp list tools %s: %w", cfg.Name, err)
		}
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		session.Close()
		return fmt.Errorf("mcp: manager fechado")
	}
	if old := m.sessions[cfg.Name]; old != nil {
		m.mu.Unlock()
		session.Close()
		return fmt.Errorf("mcp: servidor %q já conectado", cfg.Name)
	}
	m.sessions[cfg.Name] = session
	m.tools[cfg.Name] = items
	if m.skillReg != nil {
		for _, item := range items {
			m.registerMCPToolLocked(cfg.Name, item, session)
		}
	}
	m.mu.Unlock()

	// Resource and prompt discovery is optional. A server without either
	// capability must still connect successfully.
	m.refreshOptionalLists(ctx, session)

	m.logger.Info("mcp: servidor conectado",
		"name", cfg.Name,
		"tools", len(items),
	)

	return nil
}

// Disconnect closes a server connection.
func (m *Manager) Disconnect(name string) error {
	m.reconnect.Stop(name)
	m.mu.Lock()

	session, ok := m.sessions[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("mcp: servidor %q nao conectado", name)
	}

	m.removeServerSkillsLocked(name)
	delete(m.sessions, name)
	delete(m.tools, name)
	delete(m.resources, name)
	delete(m.prompts, name)
	m.mu.Unlock()
	return session.Close()
}

// RegisterTools registers all MCP tools from connected servers into a skill registry.
func (m *Manager) RegisterTools(reg *skill.Registry) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.skillReg = reg

	count := 0
	for serverName, tools := range m.tools {
		session := m.sessions[serverName]
		for _, tool := range tools {
			m.registerMCPToolLocked(serverName, tool, session)
			count++
		}
	}
	return count
}

func (m *Manager) toolToSkill(serverName string, tool *mcpsdk.Tool, session *mcpsdk.ClientSession) *skill.Skill {
	skillName := fmt.Sprintf("mcp_%s_%s", serverName, tool.Name)

	schema := schemaMap(tool.InputSchema)
	params := jsonschema.Definition{Type: jsonschema.Object}
	// Keep a best-effort typed definition for callers inspecting Parameters;
	// InputSchema is authoritative and retains JSON Schema extensions.
	if data, err := json.Marshal(schema); err == nil {
		if err := json.Unmarshal(data, &params); err != nil {
			params = jsonschema.Definition{Type: jsonschema.Object}
		}
	}
	execute := func(ctx context.Context, args map[string]any) (types.ToolResult, error) {
		result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: tool.Name, Arguments: args})
		if err != nil {
			return types.ToolResult{}, fmt.Errorf("mcp call %s/%s: %w", serverName, tool.Name, err)
		}
		return mcpToolResult(result), nil
	}
	return &skill.Skill{
		Name:          skillName,
		Origin:        "mcp:" + serverName,
		Description:   fmt.Sprintf("[MCP:%s] %s", serverName, tool.Description),
		Parameters:    params,
		InputSchema:   schema,
		ExecuteResult: execute,
		Execute: func(args map[string]string) (string, error) {
			typed := make(map[string]any, len(args))
			for k, v := range args {
				typed[k] = v
			}
			result, err := execute(context.Background(), typed)
			if err == nil && result.IsError {
				err = fmt.Errorf("mcp tool error: %s", result.Content)
			}
			return result.Content, err
		},
	}
}

func schemaMap(schema any) map[string]any {
	if value, ok := schema.(map[string]any); ok {
		return value
	}
	var result map[string]any
	if data, err := json.Marshal(schema); err == nil {
		if err := json.Unmarshal(data, &result); err != nil {
			result = nil
		}
	}
	if result == nil {
		result = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return result
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

	return result, result.IsError, nil
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
				schema := schemaMap(tool.InputSchema)
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
	m.closed = true
	sessions := m.sessions
	for name := range sessions {
		m.removeServerSkillsLocked(name)
	}
	m.sessions = make(map[string]*mcpsdk.ClientSession)
	m.tools = make(map[string][]*mcpsdk.Tool)
	m.resources = make(map[string][]*mcpsdk.Resource)
	m.prompts = make(map[string][]*mcpsdk.Prompt)
	m.mu.Unlock()

	var lastErr error
	for name, session := range sessions {
		if err := session.Close(); err != nil {
			lastErr = err
			m.logger.Warn("mcp close error", "server", name, "error", err)
		}
	}
	return lastErr
}

// mcpHTTPClient attaches configured credentials without forwarding them through
// redirects. A fresh immutable header copy is shared by concurrent SDK requests.
func mcpHTTPClient(headers map[string]string) *http.Client {
	values := make(http.Header, len(headers))
	for key, value := range headers {
		values.Set(key, value)
	}
	return &http.Client{
		Transport:     &mcpHeaderTransport{headers: values, base: http.DefaultTransport},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

type mcpHeaderTransport struct {
	headers http.Header
	base    http.RoundTripper
}

func (t *mcpHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	for key, values := range t.headers {
		cloned.Header[key] = append([]string(nil), values...)
	}
	return t.base.RoundTrip(cloned)
}
