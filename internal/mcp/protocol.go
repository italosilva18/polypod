package mcp

import "encoding/json"

// JSONRPCRequest is a JSON-RPC 2.0 request.
type JSONRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// JSONRPCResponse is a JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError carries an error code and message.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *JSONRPCError) Error() string { return e.Message }

// JSONRPCNotification is a JSON-RPC 2.0 notification (no ID).
type JSONRPCNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// InitializeParams is sent by the client to start a session.
type InitializeParams struct {
	ProtocolVersion string     `json:"protocolVersion"`
	ClientInfo      ClientInfo `json:"clientInfo"`
	Capabilities    struct{}   `json:"capabilities"`
}

// ClientInfo identifies the MCP client.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult is returned by the server.
type InitializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	ServerInfo      ServerInfo   `json:"serverInfo"`
	Capabilities    Capabilities `json:"capabilities"`
}

// ServerInfo identifies the MCP server.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Capabilities advertised by the server.
type Capabilities struct {
	Tools     *ToolsCapability     `json:"tools,omitempty"`
	Resources *ResourcesCapability `json:"resources,omitempty"`
	Prompts   *PromptsCapability   `json:"prompts,omitempty"`
}

// ToolsCapability signals the server exposes tools.
type ToolsCapability struct{}

// ResourcesCapability signals the server exposes resources.
type ResourcesCapability struct{}

// PromptsCapability signals the server exposes prompts.
type PromptsCapability struct{}

// ToolsListResult is the response to tools/list.
type ToolsListResult struct {
	Tools []MCPTool `json:"tools"`
}

// MCPTool describes a single tool exposed by an MCP server.
type MCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// CallToolParams is sent to invoke a tool.
type CallToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// CallToolResult is the tool invocation response.
type CallToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ToolContent carries a single result content block.
type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}
