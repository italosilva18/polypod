package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
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
	mu                 sync.RWMutex
	sessions           map[string]*mcpsdk.ClientSession
	configs            map[string]ServerConfig
	tools              map[string][]*mcpsdk.Tool
	resources          map[string][]*mcpsdk.Resource
	resourceTemplates  map[string][]*mcpsdk.ResourceTemplate
	subscriptions      map[string]map[string]struct{}
	subscriptionStore  string
	subscriptionSaveMu sync.Mutex
	resourceUpdates    map[string][]ResourceUpdate
	resourceWake       *resourceWakeDispatcher
	// disconnecting keeps session identity available to notification callbacks
	// while ClientSession.Close drains them. mu protects the map and channels.
	disconnecting      map[string]chan struct{}
	prompts            map[string][]*mcpsdk.Prompt
	elicitationHandler ElicitationHandler
	elicitationWaiters map[*mcpsdk.ClientSession]map[string]chan struct{}
	skillReg           *skill.Registry
	closed             bool
	client             *mcpsdk.Client
	logger             *slog.Logger
	reconnect          *ReconnectManager
}

// NewManager creates an MCP manager.
func NewManager(logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	m := &Manager{
		sessions:           make(map[string]*mcpsdk.ClientSession),
		configs:            make(map[string]ServerConfig),
		tools:              make(map[string][]*mcpsdk.Tool),
		resources:          make(map[string][]*mcpsdk.Resource),
		resourceTemplates:  make(map[string][]*mcpsdk.ResourceTemplate),
		subscriptions:      make(map[string]map[string]struct{}),
		resourceUpdates:    make(map[string][]ResourceUpdate),
		resourceWake:       newResourceWakeDispatcher(logger),
		disconnecting:      make(map[string]chan struct{}),
		elicitationWaiters: make(map[*mcpsdk.ClientSession]map[string]chan struct{}),
		prompts:            make(map[string][]*mcpsdk.Prompt),
		logger:             logger,
		reconnect:          NewReconnectManager(DefaultReconnectConfig()),
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
		ResourceUpdatedHandler: func(_ context.Context, req *mcpsdk.ResourceUpdatedNotificationRequest) {
			m.recordResourceUpdate(req)
		},
		ElicitationHandler:         m.handleSDKElicitation,
		ElicitationCompleteHandler: m.handleElicitationComplete,
		PromptListChangedHandler: func(_ context.Context, req *mcpsdk.PromptListChangedRequest) {
			go m.refreshNotification(req.Session, "prompts")
		},
	})
	return m
}

// Connect connects to an MCP server using the given config.
func (m *Manager) Connect(ctx context.Context, cfg ServerConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("mcp: nome do servidor obrigatorio")
	}
	m.mu.RLock()
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		return fmt.Errorf("mcp: manager fechado")
	}
	if _, err := transportForConfig(cfg); err != nil {
		return err
	}
	m.mu.Lock()
	if m.closed || m.disconnecting[cfg.Name] != nil {
		m.mu.Unlock()
		return fmt.Errorf("mcp: servidor %q em encerramento", cfg.Name)
	}
	m.configs[cfg.Name] = cloneServerConfig(cfg)
	_, alreadyConnected := m.sessions[cfg.Name]
	m.mu.Unlock()
	if alreadyConnected {
		return m.Reconnect(ctx, cfg.Name)
	}

	var session *mcpsdk.ClientSession
	err := m.reconnect.Connect(ctx, cfg.Name, func(innerCtx context.Context) error {
		transport, err := transportForConfig(cfg)
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
	if m.closed || m.disconnecting[cfg.Name] != nil {
		m.mu.Unlock()
		session.Close()
		return fmt.Errorf("mcp: servidor %q em encerramento", cfg.Name)
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
	m.restoreSubscriptions(ctx, cfg.Name, session)

	m.logger.Info("mcp: servidor conectado",
		"name", cfg.Name,
		"tools", len(items),
	)

	return nil
}

// Disconnect closes a server connection.
func (m *Manager) Disconnect(name string) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return fmt.Errorf("mcp: manager fechado")
	}
	session, ok := m.sessions[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("mcp: servidor %q nao conectado", name)
	}
	if m.disconnecting[name] != nil {
		m.mu.Unlock()
		return fmt.Errorf("mcp: servidor %q ja esta desconectando", name)
	}
	done := make(chan struct{})
	m.disconnecting[name] = done
	m.mu.Unlock()

	m.reconnect.Stop(name)
	err := session.Close()
	m.mu.Lock()
	if m.sessions[name] == session {
		m.removeServerSkillsLocked(name)
		delete(m.sessions, name)
		delete(m.tools, name)
		delete(m.resources, name)
		delete(m.resourceTemplates, name)
		delete(m.resourceUpdates, name)
		delete(m.prompts, name)
	}
	delete(m.disconnecting, name)
	close(done)
	m.mu.Unlock()
	// A notification already accepted by the local journal survives an
	// explicit disconnect. The queued task has a bounded retry budget; dropping
	// it here would erase acknowledged work before that budget is used.
	return err
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
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	// Reject new connections while retaining session identity and desired
	// subscriptions for resource callbacks already being drained by Close.
	m.closed = true
	var disconnects []chan struct{}
	for _, done := range m.disconnecting {
		disconnects = append(disconnects, done)
	}
	m.mu.Unlock()
	for _, done := range disconnects {
		<-done
	}
	m.mu.Lock()
	sessions := make(map[string]*mcpsdk.ClientSession, len(m.sessions))
	for name, session := range m.sessions {
		sessions[name] = session
	}
	m.mu.Unlock()

	m.reconnect.StopAll()
	var lastErr error
	for name, session := range sessions {
		if err := session.Close(); err != nil {
			lastErr = err
			m.logger.Warn("mcp close error", "server", name, "error", err)
		}
	}
	// The SDK has finished closing its sessions, including callbacks they
	// invoked. Keep the wake dispatcher open until those callbacks can persist.
	m.resourceWake.Close()

	m.mu.Lock()
	for name := range sessions {
		m.removeServerSkillsLocked(name)
	}
	m.sessions = make(map[string]*mcpsdk.ClientSession)
	m.configs = make(map[string]ServerConfig)
	m.tools = make(map[string][]*mcpsdk.Tool)
	m.resources = make(map[string][]*mcpsdk.Resource)
	m.resourceTemplates = make(map[string][]*mcpsdk.ResourceTemplate)
	m.subscriptions = make(map[string]map[string]struct{})
	m.resourceUpdates = make(map[string][]ResourceUpdate)
	m.elicitationWaiters = make(map[*mcpsdk.ClientSession]map[string]chan struct{})
	m.prompts = make(map[string][]*mcpsdk.Prompt)
	m.mu.Unlock()
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
