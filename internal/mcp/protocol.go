package mcp

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
)

// ─── JSON-RPC 2.0 Message Types ─────────────────────────────────────────────

const JSONRPCVersion = "2.0"

// RequestID can be a string, int, or nil (for notifications).
type RequestID = any

// JSONRPCMessage is the union of request, response, and notification.
type JSONRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      RequestID       `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError is the error object in a JSON-RPC response.
type JSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *JSONRPCError) Error() string {
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

// Standard JSON-RPC error codes.
const (
	ErrCodeParseError     = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternalError  = -32603

	// MCP-specific error codes.
	ErrCodeSessionExpired = -32001
	ErrCodeRequestFailed  = -32000
)

// IsRequest returns true if the message is a request (has method and ID).
func (m *JSONRPCMessage) IsRequest() bool {
	return m.Method != "" && m.ID != nil
}

// IsNotification returns true if the message is a notification (has method but no ID).
func (m *JSONRPCMessage) IsNotification() bool {
	return m.Method != "" && m.ID == nil
}

// IsResponse returns true if the message is a response (has result or error but no method).
func (m *JSONRPCMessage) IsResponse() bool {
	return m.Method == "" && (m.Result != nil || m.Error != nil)
}

// ─── MCP Protocol Methods ───────────────────────────────────────────────────

const (
	MethodInitialize     = "initialize"
	MethodInitialized    = "notifications/initialized"
	MethodPing           = "ping"
	MethodToolsList      = "tools/list"
	MethodToolsCall      = "tools/call"
	MethodResourcesList  = "resources/list"
	MethodResourcesRead  = "resources/read"
	MethodPromptsList    = "prompts/list"
	MethodPromptsGet     = "prompts/get"
	MethodRootsList      = "roots/list"
	MethodElicit         = "elicitation/create"
	MethodLoggingMessage = "notifications/message"

	// List changed notifications from server.
	MethodToolsListChanged     = "notifications/tools/list_changed"
	MethodResourcesListChanged = "notifications/resources/list_changed"
	MethodPromptsListChanged   = "notifications/prompts/list_changed"
)

// ─── MCP Protocol Version ───────────────────────────────────────────────────

const (
	ProtocolVersion20241105 = "2024-11-05"
	ProtocolVersion20250326 = "2025-03-26"
	LatestProtocolVersion   = ProtocolVersion20250326
)

// ─── Capability Types ───────────────────────────────────────────────────────

// ClientCapabilities declares what the client supports.
type ClientCapabilities struct {
	Roots       *RootsCapability       `json:"roots,omitempty"`
	Sampling    *SamplingCapability    `json:"sampling,omitempty"`
	Elicitation *ElicitationCapability `json:"elicitation,omitempty"`
}

type RootsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type SamplingCapability struct{}

type ElicitationCapability struct{}

// ServerCapabilities declares what the server supports.
type ServerCapabilities struct {
	Tools        *ToolsCapability     `json:"tools,omitempty"`
	Resources    *ResourcesCapability `json:"resources,omitempty"`
	Prompts      *PromptsCapability   `json:"prompts,omitempty"`
	Logging      *LoggingCapability   `json:"logging,omitempty"`
	Experimental map[string]any       `json:"experimental,omitempty"`
}

type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type LoggingCapability struct{}

// ─── Initialize Request/Response ────────────────────────────────────────────

// InitializeParams is sent by the client.
type InitializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      Implementation     `json:"clientInfo"`
}

// Implementation describes a client or server.
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult is returned by the server.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      Implementation     `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}

// ─── Tool Types ─────────────────────────────────────────────────────────────

// ToolInfo describes an MCP tool.
type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

// ToolsListResult is the response to tools/list.
type ToolsListResult struct {
	Tools  []ToolInfo `json:"tools"`
	Cursor string     `json:"nextCursor,omitempty"`
}

// ToolCallParams is sent to tools/call.
type ToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// ContentBlock is a single content item in a tool result.
type ContentBlock struct {
	Type     string `json:"type"` // "text", "image", "resource"
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"` // base64 for images
	URI      string `json:"uri,omitempty"`
}

// ToolCallResult is the response to tools/call.
type ToolCallResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
	Meta    map[string]any `json:"_meta,omitempty"`
}

// ─── Resource Types ─────────────────────────────────────────────────────────

// ResourceInfo describes an MCP resource.
type ResourceInfo struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// ResourcesListResult is the response to resources/list.
type ResourcesListResult struct {
	Resources []ResourceInfo `json:"resources"`
	Cursor    string         `json:"nextCursor,omitempty"`
}

// ResourceReadParams is sent to resources/read.
type ResourceReadParams struct {
	URI string `json:"uri"`
}

// ResourceContent is a single content item.
type ResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"` // base64
}

// ResourceReadResult is the response to resources/read.
type ResourceReadResult struct {
	Contents []ResourceContent `json:"contents"`
}

// ─── Prompt Types ───────────────────────────────────────────────────────────

// PromptInfo describes an MCP prompt.
type PromptInfo struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptArgument is a parameter for a prompt.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// PromptsListResult is the response to prompts/list.
type PromptsListResult struct {
	Prompts []PromptInfo `json:"prompts"`
	Cursor  string       `json:"nextCursor,omitempty"`
}

// PromptsGetParams is sent to prompts/get.
type PromptsGetParams struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

// PromptMessage is a message in a prompt result.
type PromptMessage struct {
	Role    string       `json:"role"` // "user" or "assistant"
	Content ContentBlock `json:"content"`
}

// PromptsGetResult is the response to prompts/get.
type PromptsGetResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

// ─── Roots ──────────────────────────────────────────────────────────────────

// Root represents a filesystem root.
type Root struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

// RootsListResult is the response to roots/list.
type RootsListResult struct {
	Roots []Root `json:"roots"`
}

// ─── Request ID Management ──────────────────────────────────────────────────

// IDGenerator generates unique request IDs.
type IDGenerator struct {
	counter atomic.Int64
}

// Next returns the next unique request ID.
func (g *IDGenerator) Next() int64 {
	return g.counter.Add(1)
}

// ─── Message Construction Helpers ───────────────────────────────────────────

// NewRequest creates a JSON-RPC request message.
func NewRequest(id int64, method string, params any) (*JSONRPCMessage, error) {
	var raw json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		raw = data
	}
	return &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Method:  method,
		Params:  raw,
	}, nil
}

// NewNotification creates a JSON-RPC notification message (no ID).
func NewNotification(method string, params any) (*JSONRPCMessage, error) {
	var raw json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		raw = data
	}
	return &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		Method:  method,
		Params:  raw,
	}, nil
}

// NewResult creates a JSON-RPC success response.
func NewResult(id RequestID, result any) (*JSONRPCMessage, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}
	return &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Result:  data,
	}, nil
}

// NewErrorResponse creates a JSON-RPC error response.
func NewErrorResponse(id RequestID, code int, message string) *JSONRPCMessage {
	return &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
		},
	}
}

// ParseResult unmarshals the result field of a response into the given type.
func ParseResult[T any](msg *JSONRPCMessage) (*T, error) {
	if msg.Error != nil {
		return nil, msg.Error
	}
	if msg.Result == nil {
		return nil, fmt.Errorf("no result in response")
	}
	var v T
	if err := json.Unmarshal(msg.Result, &v); err != nil {
		return nil, fmt.Errorf("unmarshal result: %w", err)
	}
	return &v, nil
}
