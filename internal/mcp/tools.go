package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
)

// ─── Name Normalization ─────────────────────────────────────────────────────

var invalidNameChars = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// NormalizeName normalizes a server/tool name to be compatible with
// ^[a-zA-Z0-9_-]{1,64}$. Replaces invalid characters with underscores.
func NormalizeName(name string) string {
	return invalidNameChars.ReplaceAllString(name, "_")
}

// BuildMCPToolName builds a fully qualified MCP tool name: mcp__<server>__<tool>
func BuildMCPToolName(serverName, toolName string) string {
	return fmt.Sprintf("mcp__%s__%s", NormalizeName(serverName), NormalizeName(toolName))
}

// ParseMCPToolName extracts the server and tool names from an MCP tool name.
// Returns ("", "", false) if the name is not a valid MCP tool name.
func ParseMCPToolName(fullName string) (serverName, toolName string, ok bool) {
	parts := strings.SplitN(fullName, "__", 3)
	if len(parts) < 3 || parts[0] != "mcp" || parts[1] == "" {
		return "", "", false
	}
	return parts[1], strings.Join(parts[2:], "__"), true
}

// ─── MCP Tool Adapter ───────────────────────────────────────────────────────

// MCPToolAdapter wraps an MCP server's tools to be used as local tools.
type MCPToolAdapter struct {
	pool   *ConnectionPool
	perms  *MCPToolPermissionChecker
	logger *slog.Logger

	mu         sync.RWMutex
	toolCache  map[string][]ToolInfo // server name -> tools
	toolServer map[string]string     // full tool name -> server name
}

// NewMCPToolAdapter creates a new tool adapter.
func NewMCPToolAdapter(pool *ConnectionPool, perms *MCPToolPermissionChecker, logger *slog.Logger) *MCPToolAdapter {
	return &MCPToolAdapter{
		pool:       pool,
		perms:      perms,
		logger:     logger,
		toolCache:  make(map[string][]ToolInfo),
		toolServer: make(map[string]string),
	}
}

// ─── List MCP Tools ─────────────────────────────────────────────────────────

// ListMCPTools discovers all tools from all connected MCP servers.
func (a *MCPToolAdapter) ListMCPTools(ctx context.Context) ([]MCPAdaptedTool, error) {
	conns := a.pool.Connected()
	var allTools []MCPAdaptedTool

	for _, conn := range conns {
		if conn.Capabilities.Tools == nil {
			continue
		}

		tools, err := a.listToolsForConnection(ctx, conn)
		if err != nil {
			a.logger.Warn("failed to list tools",
				"server", conn.Name,
				"error", err,
			)
			continue
		}

		for _, tool := range tools {
			fullName := BuildMCPToolName(conn.Name, tool.Name)

			adapted := MCPAdaptedTool{
				Name:         fullName,
				ServerName:   conn.Name,
				OriginalName: tool.Name,
				Description:  truncateDescription(tool.Description),
				InputSchema:  tool.InputSchema,
				IsMCP:        true,
			}

			allTools = append(allTools, adapted)

			a.mu.Lock()
			a.toolServer[fullName] = conn.Name
			a.mu.Unlock()
		}
	}

	return allTools, nil
}

func (a *MCPToolAdapter) listToolsForConnection(ctx context.Context, conn *MCPConnection) ([]ToolInfo, error) {
	resp, err := conn.Request(ctx, MethodToolsList, nil)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}

	result, err := ParseResult[ToolsListResult](resp)
	if err != nil {
		return nil, fmt.Errorf("parse tools list: %w", err)
	}

	// Cache the tools
	a.mu.Lock()
	a.toolCache[conn.Name] = result.Tools
	a.mu.Unlock()

	return result.Tools, nil
}

// ─── Call MCP Tool ──────────────────────────────────────────────────────────

// CallMCPTool executes a tool on a remote MCP server.
func (a *MCPToolAdapter) CallMCPTool(ctx context.Context, fullToolName string, arguments map[string]any) (*ToolCallResult, error) {
	serverName, toolName, ok := ParseMCPToolName(fullToolName)
	if !ok {
		return nil, fmt.Errorf("invalid MCP tool name: %s", fullToolName)
	}

	// Permission check
	if a.perms != nil {
		check := a.perms.CheckPermission(serverName, toolName)
		switch check.Decision {
		case PermissionDeny:
			return nil, fmt.Errorf("permission denied: %s", check.Reason)
		case PermissionAsk:
			approved, err := a.perms.RequestApproval(serverName, toolName, arguments)
			if err != nil {
				return nil, fmt.Errorf("permission approval failed: %w", err)
			}
			if !approved {
				return nil, fmt.Errorf("user denied permission for %s on %s", toolName, serverName)
			}
		case PermissionAllow:
			// proceed
		}
	}

	// Get connection
	conn, ok := a.pool.Get(serverName)
	if !ok {
		return nil, fmt.Errorf("MCP server %q not connected", serverName)
	}

	if conn.Status != StatusConnected {
		return nil, fmt.Errorf("MCP server %q is not connected (status: %s)", serverName, conn.Status)
	}

	// Call the tool
	params := ToolCallParams{
		Name:      toolName,
		Arguments: arguments,
	}

	resp, err := conn.Request(ctx, MethodToolsCall, params)
	if err != nil {
		return nil, fmt.Errorf("call tool %s on %s: %w", toolName, serverName, err)
	}

	result, err := ParseResult[ToolCallResult](resp)
	if err != nil {
		return nil, fmt.Errorf("parse tool result: %w", err)
	}

	if result.IsError {
		var errTexts []string
		for _, c := range result.Content {
			if c.Type == "text" && c.Text != "" {
				errTexts = append(errTexts, c.Text)
			}
		}
		return result, fmt.Errorf("MCP tool error: %s", strings.Join(errTexts, "; "))
	}

	return result, nil
}

// CallMCPToolByServer calls a tool by server name and original tool name (not the full qualified name).
func (a *MCPToolAdapter) CallMCPToolByServer(ctx context.Context, serverName, toolName string, arguments map[string]any) (*ToolCallResult, error) {
	fullName := BuildMCPToolName(serverName, toolName)
	return a.CallMCPTool(ctx, fullName, arguments)
}

// ─── List MCP Resources ─────────────────────────────────────────────────────

// ServerResource is a resource with its owning server name attached.
type ServerResource struct {
	ResourceInfo
	Server string `json:"server"`
}

// ListMCPResources lists resources from all connected MCP servers.
func (a *MCPToolAdapter) ListMCPResources(ctx context.Context) ([]ServerResource, error) {
	conns := a.pool.Connected()
	var allResources []ServerResource

	for _, conn := range conns {
		if conn.Capabilities.Resources == nil {
			continue
		}

		resources, err := a.listResourcesForConnection(ctx, conn)
		if err != nil {
			a.logger.Warn("failed to list resources",
				"server", conn.Name,
				"error", err,
			)
			continue
		}

		for _, r := range resources {
			allResources = append(allResources, ServerResource{
				ResourceInfo: r,
				Server:       conn.Name,
			})
		}
	}

	return allResources, nil
}

// ListMCPResourcesForServer lists resources from a specific server.
func (a *MCPToolAdapter) ListMCPResourcesForServer(ctx context.Context, serverName string) ([]ServerResource, error) {
	conn, ok := a.pool.Get(serverName)
	if !ok {
		return nil, fmt.Errorf("MCP server %q not connected", serverName)
	}

	if conn.Capabilities.Resources == nil {
		return nil, nil
	}

	resources, err := a.listResourcesForConnection(ctx, conn)
	if err != nil {
		return nil, err
	}

	var result []ServerResource
	for _, r := range resources {
		result = append(result, ServerResource{
			ResourceInfo: r,
			Server:       conn.Name,
		})
	}
	return result, nil
}

func (a *MCPToolAdapter) listResourcesForConnection(ctx context.Context, conn *MCPConnection) ([]ResourceInfo, error) {
	resp, err := conn.Request(ctx, MethodResourcesList, nil)
	if err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}

	result, err := ParseResult[ResourcesListResult](resp)
	if err != nil {
		return nil, fmt.Errorf("parse resources list: %w", err)
	}

	return result.Resources, nil
}

// ─── Read MCP Resource ──────────────────────────────────────────────────────

// ReadMCPResource reads a specific resource from an MCP server.
func (a *MCPToolAdapter) ReadMCPResource(ctx context.Context, serverName, uri string) (*ResourceReadResult, error) {
	conn, ok := a.pool.Get(serverName)
	if !ok {
		return nil, fmt.Errorf("MCP server %q not connected", serverName)
	}

	if conn.Capabilities.Resources == nil {
		return nil, fmt.Errorf("MCP server %q does not support resources", serverName)
	}

	params := ResourceReadParams{URI: uri}
	resp, err := conn.Request(ctx, MethodResourcesRead, params)
	if err != nil {
		return nil, fmt.Errorf("read resource: %w", err)
	}

	result, err := ParseResult[ResourceReadResult](resp)
	if err != nil {
		return nil, fmt.Errorf("parse resource read: %w", err)
	}

	return result, nil
}

// ─── List MCP Prompts ───────────────────────────────────────────────────────

// ListMCPPrompts lists prompts from all connected MCP servers.
func (a *MCPToolAdapter) ListMCPPrompts(ctx context.Context) ([]PromptInfo, error) {
	conns := a.pool.Connected()
	var allPrompts []PromptInfo

	for _, conn := range conns {
		if conn.Capabilities.Prompts == nil {
			continue
		}

		resp, err := conn.Request(ctx, MethodPromptsList, nil)
		if err != nil {
			a.logger.Warn("failed to list prompts",
				"server", conn.Name,
				"error", err,
			)
			continue
		}

		result, err := ParseResult[PromptsListResult](resp)
		if err != nil {
			a.logger.Warn("failed to parse prompts list",
				"server", conn.Name,
				"error", err,
			)
			continue
		}

		allPrompts = append(allPrompts, result.Prompts...)
	}

	return allPrompts, nil
}

// ─── Get MCP Prompt ─────────────────────────────────────────────────────────

// GetMCPPrompt retrieves a prompt from the appropriate MCP server.
func (a *MCPToolAdapter) GetMCPPrompt(ctx context.Context, serverName, promptName string, arguments map[string]string) (*PromptsGetResult, error) {
	conn, ok := a.pool.Get(serverName)
	if !ok {
		return nil, fmt.Errorf("MCP server %q not connected", serverName)
	}

	if conn.Capabilities.Prompts == nil {
		return nil, fmt.Errorf("MCP server %q does not support prompts", serverName)
	}

	params := PromptsGetParams{
		Name:      promptName,
		Arguments: arguments,
	}

	resp, err := conn.Request(ctx, MethodPromptsGet, params)
	if err != nil {
		return nil, fmt.Errorf("get prompt: %w", err)
	}

	result, err := ParseResult[PromptsGetResult](resp)
	if err != nil {
		return nil, fmt.Errorf("parse prompt result: %w", err)
	}

	return result, nil
}

// ─── MCPAdaptedTool ─────────────────────────────────────────────────────────

// MCPAdaptedTool represents an MCP tool adapted for local use.
type MCPAdaptedTool struct {
	Name         string         `json:"name"`
	ServerName   string         `json:"serverName"`
	OriginalName string         `json:"originalName"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema,omitempty"`
	IsMCP        bool           `json:"isMcp"`
}

// ─── Result Mapping ─────────────────────────────────────────────────────────

// ToolResultText extracts all text content from a ToolCallResult.
func ToolResultText(result *ToolCallResult) string {
	if result == nil {
		return ""
	}
	var texts []string
	for _, c := range result.Content {
		if c.Type == "text" && c.Text != "" {
			texts = append(texts, c.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// ToolResultToJSON serializes a ToolCallResult to JSON for API responses.
func ToolResultToJSON(result *ToolCallResult) (json.RawMessage, error) {
	if result == nil {
		return json.Marshal(map[string]any{
			"content": []map[string]string{},
		})
	}
	return json.Marshal(result)
}

// ─── Helpers ────────────────────────────────────────────────────────────────

const maxDescriptionLength = 2048

func truncateDescription(desc string) string {
	if len(desc) <= maxDescriptionLength {
		return desc
	}
	return desc[:maxDescriptionLength] + "... [truncated]"
}

// ─── Integrated Manager (backward-compatible) ───────────────────────────────

// IntegratedManager provides a high-level API combining connection pool, tools,
// permissions, and elicitation into a single facade. It is the primary entry
// point for the rest of the polypod codebase.
type IntegratedManager struct {
	Pool         *ConnectionPool
	Tools        *MCPToolAdapter
	Permissions  *MCPToolPermissionChecker
	Elicitation  *ElicitationQueue
	TokenStorage TokenStorage
	Logger       *slog.Logger
}

// NewIntegratedManager creates a fully wired MCP manager.
func NewIntegratedManager(logger *slog.Logger) *IntegratedManager {
	pool := NewConnectionPool(logger)
	perms := NewMCPToolPermissionChecker(logger)
	tools := NewMCPToolAdapter(pool, perms, logger)
	elicit := NewElicitationQueue(DefaultElicitationHandler, logger)

	return &IntegratedManager{
		Pool:         pool,
		Tools:        tools,
		Permissions:  perms,
		Elicitation:  elicit,
		TokenStorage: NewMemoryTokenStorage(),
		Logger:       logger,
	}
}

// ConnectServer connects to an MCP server from config.
func (m *IntegratedManager) ConnectServer(ctx context.Context, cfg *MCPServerConfig) (*MCPConnection, error) {
	opts := DefaultConnectionOptions()
	opts.Logger = m.Logger

	conn, err := m.Pool.ConnectServer(ctx, cfg, opts)
	if err != nil {
		return nil, err
	}

	// Register elicitation handler
	RegisterElicitationHandler(conn, m.Elicitation)

	// Register roots/list handler
	conn.OnRequest(MethodRootsList, func(ctx context.Context, id RequestID, method string, params json.RawMessage) (*JSONRPCMessage, error) {
		result := RootsListResult{
			Roots: []Root{
				{URI: "file:///"},
			},
		}
		return NewResult(id, result)
	})

	// Register list_changed notification handlers
	if conn.Capabilities.Tools != nil && conn.Capabilities.Tools.ListChanged {
		conn.OnNotification(MethodToolsListChanged, func(method string, params json.RawMessage) {
			m.Logger.Info("tools changed", "server", conn.Name)
			// Re-list tools (callers should poll ListMCPTools)
		})
	}

	if conn.Capabilities.Resources != nil && conn.Capabilities.Resources.ListChanged {
		conn.OnNotification(MethodResourcesListChanged, func(method string, params json.RawMessage) {
			m.Logger.Info("resources changed", "server", conn.Name)
		})
	}

	if conn.Capabilities.Prompts != nil && conn.Capabilities.Prompts.ListChanged {
		conn.OnNotification(MethodPromptsListChanged, func(method string, params json.RawMessage) {
			m.Logger.Info("prompts changed", "server", conn.Name)
		})
	}

	return conn, nil
}

// Close disconnects all servers and cleans up.
func (m *IntegratedManager) Close() error {
	m.Elicitation.CancelAll()
	return m.Pool.Close()
}
