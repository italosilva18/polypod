package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/costa/polypod/internal/types"
)

// MCPCaller is the minimal interface an MCPToolWrapper needs to execute
// a tool call against a remote MCP server. Keeping this narrow lets the
// wrapper stay decoupled from the full MCP client/manager implementation
// and makes it trivial to substitute a fake caller in tests.
type MCPCaller interface {
	CallTool(ctx context.Context, server, name string, args map[string]any) (result any, isError bool, err error)
}

// MCPToolWrapper captures the data needed to expose a remote MCP tool as
// a native polypod types.Tool. It is immutable after construction.
type MCPToolWrapper struct {
	serverName  string
	toolName    string
	description string
	inputSchema map[string]any
	caller      MCPCaller
}

// ServerName returns the underlying MCP server name.
func (w *MCPToolWrapper) ServerName() string { return w.serverName }

// ToolName returns the original (un-prefixed) tool name as exposed by
// the remote MCP server.
func (w *MCPToolWrapper) ToolName() string { return w.toolName }

// FullName returns the fully-qualified tool name used inside polypod,
// following the TS convention: mcp__<server>__<tool>.
func (w *MCPToolWrapper) FullName() string {
	return BuildMCPToolName(w.serverName, w.toolName)
}

// WrapMCPTool builds a types.Tool that proxies execution to an MCP server
// via the provided caller. The returned Tool is safe for concurrent use
// and exposes the server-provided schema verbatim.
func WrapMCPTool(caller MCPCaller, server, name, description string, inputSchema map[string]any) types.Tool {
	w := &MCPToolWrapper{
		serverName:  server,
		toolName:    name,
		description: description,
		inputSchema: inputSchema,
		caller:      caller,
	}

	fullName := w.FullName()
	schema := inputSchema
	if schema == nil {
		// Provide a permissive default so the field is never nil at the
		// provider layer. MCP servers are free to send an empty schema.
		schema = map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}

	def := types.ToolDef{
		NameFn:        func() string { return fullName },
		DescriptionFn: func() string { return description },
		InputSchemaFn: func() map[string]any { return schema },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if caller == nil {
				return types.ToolResult{IsError: true, Content: "MCP caller not configured"},
					fmt.Errorf("mcp: caller is nil for tool %s", fullName)
			}

			callCtx := ctx.Context
			if callCtx == nil {
				callCtx = context.Background()
			}

			result, isError, err := caller.CallTool(callCtx, server, name, args)
			if err != nil {
				return types.ToolResult{
					IsError: true,
					Content: fmt.Sprintf("MCP tool %s failed: %v", fullName, err),
				}, err
			}

			content := renderMCPResult(result)
			return types.ToolResult{
				Content: content,
				IsError: isError,
			}, nil
		},
		// Conservative defaults: not read-only, not destructive, but
		// concurrency-safe (each call goes through its own request id).
		IsConcurrencyFn: func(map[string]any) bool { return true },
		IsReadOnlyFn:    func(map[string]any) bool { return false },
		IsDestructiveFn: func(map[string]any) bool { return false },
		IsEnabledFn:     func() bool { return true },
	}

	return types.BuildTool(def)
}

// MCPToolInfo is a lightweight descriptor for an MCP-exposed tool.
type MCPToolInfo struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// MCPServerTools bundles all tools discovered from a single MCP server.
type MCPServerTools struct {
	ServerName string
	Tools      []MCPToolInfo
}

// WrapAllMCPTools wraps every tool in every server into a flat slice of
// types.Tool instances, all sharing the supplied caller.
func WrapAllMCPTools(caller MCPCaller, servers []MCPServerTools) []types.Tool {
	var out []types.Tool
	for _, srv := range servers {
		for _, t := range srv.Tools {
			out = append(out, WrapMCPTool(caller, srv.ServerName, t.Name, t.Description, t.InputSchema))
		}
	}
	return out
}

// ToolRegistry is a narrow interface around the concrete tool.Registry
// so that this package can register wrapped MCP tools without importing
// the internal/tool package (which would create an import cycle).
type ToolRegistry interface {
	Register(types.Tool)
}

// RegisterWrappedMCPTools wraps every tool in the provided servers list
// and registers the resulting types.Tool instances into the given
// registry. Intended to be called from the MCP connect flow in main.go
// after the initial ListMCPTools discovery.
func RegisterWrappedMCPTools(registry ToolRegistry, caller MCPCaller, servers []MCPServerTools) {
	if registry == nil {
		return
	}
	for _, t := range WrapAllMCPTools(caller, servers) {
		registry.Register(t)
	}
}

// ─── Helpers ────────────────────────────────────────────────────────────────

// renderMCPResult converts an opaque MCP call result into a string the
// model can consume. It handles the most common shapes: plain strings,
// *ToolCallResult with text content blocks, and arbitrary JSON values.
func renderMCPResult(result any) string {
	if result == nil {
		return ""
	}

	switch v := result.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case *ToolCallResult:
		return ToolResultText(v)
	case ToolCallResult:
		return ToolResultText(&v)
	}

	// Fallback: JSON-encode whatever we got.
	b, err := json.Marshal(result)
	if err != nil {
		return fmt.Sprintf("%v", result)
	}
	return string(b)
}
