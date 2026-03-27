package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
)

// Client manages a connection to a single MCP server.
type Client struct {
	transport  Transport
	nextID     int
	serverInfo *InitializeResult
	logger     *slog.Logger
	mu         sync.Mutex
}

// NewClient wraps a transport into an MCP client.
func NewClient(transport Transport, logger *slog.Logger) *Client {
	return &Client{
		transport: transport,
		nextID:    1,
		logger:    logger,
	}
}

func (c *Client) newID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextID
	c.nextID++
	return id
}

// Initialize performs the MCP handshake with the server.
func (c *Client) Initialize(_ context.Context) (*InitializeResult, error) {
	id := c.newID()
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "initialize",
		Params: InitializeParams{
			ProtocolVersion: "2024-11-05",
			ClientInfo:      ClientInfo{Name: "polypod", Version: "0.3.0"},
		},
	}

	if err := c.transport.Send(req); err != nil {
		return nil, fmt.Errorf("mcp initialize send: %w", err)
	}

	resp, err := c.transport.Receive()
	if err != nil {
		return nil, fmt.Errorf("mcp initialize receive: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}

	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("mcp initialize decode: %w", err)
	}

	// Send the initialized notification
	notif := JSONRPCNotification{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	if err := c.transport.SendNotification(notif); err != nil {
		c.logger.Warn("mcp: falha ao enviar notifications/initialized", "error", err)
	}

	c.serverInfo = &result
	c.logger.Info("mcp: servidor conectado",
		"server", result.ServerInfo.Name,
		"version", result.ServerInfo.Version,
		"protocol", result.ProtocolVersion,
	)

	return &result, nil
}

// ListTools returns all tools exposed by the server.
func (c *Client) ListTools(_ context.Context) ([]MCPTool, error) {
	id := c.newID()
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "tools/list",
	}

	if err := c.transport.Send(req); err != nil {
		return nil, fmt.Errorf("mcp tools/list send: %w", err)
	}

	resp, err := c.transport.Receive()
	if err != nil {
		return nil, fmt.Errorf("mcp tools/list receive: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}

	var result ToolsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("mcp tools/list decode: %w", err)
	}

	c.logger.Info("mcp: ferramentas listadas", "count", len(result.Tools))
	return result.Tools, nil
}

// CallTool invokes a tool on the server.
func (c *Client) CallTool(_ context.Context, name string, args map[string]any) (*CallToolResult, error) {
	id := c.newID()
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "tools/call",
		Params:  CallToolParams{Name: name, Arguments: args},
	}

	if err := c.transport.Send(req); err != nil {
		return nil, fmt.Errorf("mcp tools/call send: %w", err)
	}

	resp, err := c.transport.Receive()
	if err != nil {
		return nil, fmt.Errorf("mcp tools/call receive: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}

	var result CallToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("mcp tools/call decode: %w", err)
	}

	return &result, nil
}

// ServerInfo returns info about the connected server, or nil if not initialized.
func (c *Client) ServerInfo() *InitializeResult {
	return c.serverInfo
}

// Close shuts down the transport.
func (c *Client) Close() error {
	return c.transport.Close()
}
