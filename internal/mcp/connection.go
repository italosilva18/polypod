package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

// ─── Connection Status ──────────────────────────────────────────────────────

// ConnectionStatus represents the state of an MCP server connection.
type ConnectionStatus string

const (
	StatusConnected ConnectionStatus = "connected"
	StatusFailed    ConnectionStatus = "failed"
	StatusNeedsAuth ConnectionStatus = "needs-auth"
	StatusPending   ConnectionStatus = "pending"
	StatusDisabled  ConnectionStatus = "disabled"
)

// ─── MCPConnection ──────────────────────────────────────────────────────────

// MCPConnection wraps a transport and manages the JSON-RPC protocol lifecycle.
type MCPConnection struct {
	Name          string
	Config        *MCPServerConfig
	Status        ConnectionStatus
	transport     Transport
	logger        *slog.Logger
	idGen         *IDGenerator
	healthMonitor *HealthMonitor

	// Server info from initialization
	ServerInfo   Implementation
	Capabilities ServerCapabilities
	Instructions string

	// Pending requests awaiting responses
	mu      sync.RWMutex
	pending map[int64]chan *JSONRPCMessage

	// Notification handlers
	notifyMu       sync.RWMutex
	notifyHandlers map[string]NotificationHandler

	// Request handlers (server → client requests like roots/list)
	reqMu       sync.RWMutex
	reqHandlers map[string]RequestHandler

	// Lifecycle
	ctx             context.Context
	cancel          context.CancelFunc
	receiveLoopDone chan struct{}
	closeOnce       sync.Once
	onClose         func()
}

// NotificationHandler handles incoming notifications from the server.
type NotificationHandler func(method string, params json.RawMessage)

// RequestHandler handles incoming requests from the server (e.g., roots/list, elicitation).
type RequestHandler func(ctx context.Context, id RequestID, method string, params json.RawMessage) (*JSONRPCMessage, error)

// ConnectionOptions configures a connection.
type ConnectionOptions struct {
	Logger         *slog.Logger
	ConnectTimeout time.Duration
	HealthInterval time.Duration
	OnClose        func()
}

// DefaultConnectionOptions returns sensible defaults.
func DefaultConnectionOptions() ConnectionOptions {
	return ConnectionOptions{
		ConnectTimeout: 30 * time.Second,
		HealthInterval: 30 * time.Second,
	}
}

// NewMCPConnection creates a new MCP connection.
func NewMCPConnection(name string, cfg *MCPServerConfig, transport Transport, opts ConnectionOptions) *MCPConnection {
	ctx, cancel := context.WithCancel(context.Background())
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &MCPConnection{
		Name:            name,
		Config:          cfg,
		Status:          StatusPending,
		transport:       transport,
		logger:          logger,
		idGen:           &IDGenerator{},
		pending:         make(map[int64]chan *JSONRPCMessage),
		notifyHandlers:  make(map[string]NotificationHandler),
		reqHandlers:     make(map[string]RequestHandler),
		ctx:             ctx,
		cancel:          cancel,
		receiveLoopDone: make(chan struct{}),
		onClose:         opts.OnClose,
	}
}

// Connect starts the transport, performs the initialization handshake, and begins
// the receive loop.
func (c *MCPConnection) Connect(ctx context.Context) error {
	c.logger.Info("connecting to MCP server", "name", c.Name, "type", c.Config.Type)

	// Start transport
	if err := c.transport.Start(ctx); err != nil {
		c.Status = StatusFailed
		return fmt.Errorf("start transport: %w", err)
	}

	// Start receive loop
	go c.receiveLoop()

	// Perform initialize handshake
	if err := c.initialize(ctx); err != nil {
		c.Status = StatusFailed
		c.transport.Close()
		return fmt.Errorf("initialize: %w", err)
	}

	// Send initialized notification
	notif, _ := NewNotification(MethodInitialized, nil)
	if err := c.transport.Send(ctx, notif); err != nil {
		c.logger.Warn("failed to send initialized notification", "error", err)
	}

	c.Status = StatusConnected
	c.logger.Info("MCP server connected",
		"name", c.Name,
		"server", c.ServerInfo.Name,
		"version", c.ServerInfo.Version,
		"hasTools", c.Capabilities.Tools != nil,
		"hasResources", c.Capabilities.Resources != nil,
		"hasPrompts", c.Capabilities.Prompts != nil,
	)

	return nil
}

// initialize performs the MCP initialize handshake.
func (c *MCPConnection) initialize(ctx context.Context) error {
	params := InitializeParams{
		ProtocolVersion: LatestProtocolVersion,
		Capabilities: ClientCapabilities{
			Roots:       &RootsCapability{},
			Elicitation: &ElicitationCapability{},
		},
		ClientInfo: Implementation{
			Name:    "polypod",
			Version: "0.6.0",
		},
	}

	resp, err := c.Request(ctx, MethodInitialize, params)
	if err != nil {
		return fmt.Errorf("initialize request: %w", err)
	}

	result, err := ParseResult[InitializeResult](resp)
	if err != nil {
		return fmt.Errorf("parse initialize result: %w", err)
	}

	c.ServerInfo = result.ServerInfo
	c.Capabilities = result.Capabilities
	c.Instructions = result.Instructions

	return nil
}

// Disconnect gracefully shuts down the connection.
func (c *MCPConnection) Disconnect() error {
	var err error
	c.closeOnce.Do(func() {
		c.logger.Info("disconnecting from MCP server", "name", c.Name)

		if c.healthMonitor != nil {
			c.healthMonitor.Stop()
		}

		c.cancel()

		// Close transport
		err = c.transport.Close()

		// Wait for receive loop
		select {
		case <-c.receiveLoopDone:
		case <-time.After(5 * time.Second):
			c.logger.Warn("receive loop did not exit in time", "name", c.Name)
		}

		// Reject all pending requests
		c.mu.Lock()
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.mu.Unlock()

		c.Status = StatusFailed

		if c.onClose != nil {
			c.onClose()
		}
	})
	return err
}

// ─── Request/Response ───────────────────────────────────────────────────────

// Request sends a JSON-RPC request and waits for the response.
func (c *MCPConnection) Request(ctx context.Context, method string, params any) (*JSONRPCMessage, error) {
	id := c.idGen.Next()

	msg, err := NewRequest(id, method, params)
	if err != nil {
		return nil, err
	}

	// Register pending response channel
	ch := make(chan *JSONRPCMessage, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	// Send request
	if err := c.transport.Send(ctx, msg); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	// Wait for response
	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, errors.New("connection closed while waiting for response")
		}
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Notify sends a JSON-RPC notification (no response expected).
func (c *MCPConnection) Notify(ctx context.Context, method string, params any) error {
	msg, err := NewNotification(method, params)
	if err != nil {
		return err
	}
	return c.transport.Send(ctx, msg)
}

// ─── Handler Registration ───────────────────────────────────────────────────

// OnNotification registers a handler for server notifications.
func (c *MCPConnection) OnNotification(method string, handler NotificationHandler) {
	c.notifyMu.Lock()
	defer c.notifyMu.Unlock()
	c.notifyHandlers[method] = handler
}

// OnRequest registers a handler for server-initiated requests (like roots/list).
func (c *MCPConnection) OnRequest(method string, handler RequestHandler) {
	c.reqMu.Lock()
	defer c.reqMu.Unlock()
	c.reqHandlers[method] = handler
}

// ─── Receive Loop ───────────────────────────────────────────────────────────

func (c *MCPConnection) receiveLoop() {
	defer close(c.receiveLoopDone)

	for {
		msg, err := c.transport.Receive(c.ctx)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				return
			}
			c.logger.Warn("receive error", "name", c.Name, "error", err)
			return
		}

		c.handleMessage(msg)
	}
}

func (c *MCPConnection) handleMessage(msg *JSONRPCMessage) {
	if msg.IsResponse() {
		c.handleResponse(msg)
	} else if msg.IsNotification() {
		c.handleNotification(msg)
	} else if msg.IsRequest() {
		c.handleServerRequest(msg)
	}
}

func (c *MCPConnection) handleResponse(msg *JSONRPCMessage) {
	// Extract ID as int64
	var id int64
	switch v := msg.ID.(type) {
	case float64:
		id = int64(v)
	case int64:
		id = v
	case json.Number:
		i, _ := v.Int64()
		id = i
	default:
		c.logger.Warn("response with non-numeric ID", "id", msg.ID)
		return
	}

	c.mu.RLock()
	ch, ok := c.pending[id]
	c.mu.RUnlock()

	if !ok {
		c.logger.Debug("response for unknown request", "id", id)
		return
	}

	select {
	case ch <- msg:
	default:
		c.logger.Warn("response channel full", "id", id)
	}
}

func (c *MCPConnection) handleNotification(msg *JSONRPCMessage) {
	c.notifyMu.RLock()
	handler, ok := c.notifyHandlers[msg.Method]
	c.notifyMu.RUnlock()

	if ok {
		handler(msg.Method, msg.Params)
	} else {
		c.logger.Debug("unhandled notification", "method", msg.Method)
	}
}

func (c *MCPConnection) handleServerRequest(msg *JSONRPCMessage) {
	c.reqMu.RLock()
	handler, ok := c.reqHandlers[msg.Method]
	c.reqMu.RUnlock()

	if ok {
		resp, err := handler(c.ctx, msg.ID, msg.Method, msg.Params)
		if err != nil {
			errResp := NewErrorResponse(msg.ID, ErrCodeInternalError, err.Error())
			_ = c.transport.Send(c.ctx, errResp)
			return
		}
		if resp != nil {
			_ = c.transport.Send(c.ctx, resp)
		}
	} else {
		// Unknown method
		errResp := NewErrorResponse(msg.ID, ErrCodeMethodNotFound, fmt.Sprintf("method not found: %s", msg.Method))
		_ = c.transport.Send(c.ctx, errResp)
	}
}

// ─── Connection Pool ────────────────────────────────────────────────────────

// ConnectionPool manages multiple MCP connections.
type ConnectionPool struct {
	mu          sync.RWMutex
	connections map[string]*MCPConnection
	logger      *slog.Logger
}

// NewConnectionPool creates a new connection pool.
func NewConnectionPool(logger *slog.Logger) *ConnectionPool {
	return &ConnectionPool{
		connections: make(map[string]*MCPConnection),
		logger:      logger,
	}
}

// Add adds a connection to the pool.
func (p *ConnectionPool) Add(conn *MCPConnection) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.connections[conn.Name] = conn
}

// Get retrieves a connection by name.
func (p *ConnectionPool) Get(name string) (*MCPConnection, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	conn, ok := p.connections[name]
	return conn, ok
}

// Remove removes and disconnects a connection.
func (p *ConnectionPool) Remove(name string) error {
	p.mu.Lock()
	conn, ok := p.connections[name]
	if ok {
		delete(p.connections, name)
	}
	p.mu.Unlock()

	if !ok {
		return fmt.Errorf("connection %q not found", name)
	}
	return conn.Disconnect()
}

// All returns all connections.
func (p *ConnectionPool) All() []*MCPConnection {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]*MCPConnection, 0, len(p.connections))
	for _, conn := range p.connections {
		result = append(result, conn)
	}
	return result
}

// Connected returns all connections with StatusConnected.
func (p *ConnectionPool) Connected() []*MCPConnection {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var result []*MCPConnection
	for _, conn := range p.connections {
		if conn.Status == StatusConnected {
			result = append(result, conn)
		}
	}
	return result
}

// Close disconnects all connections.
func (p *ConnectionPool) Close() error {
	p.mu.Lock()
	conns := make([]*MCPConnection, 0, len(p.connections))
	for _, conn := range p.connections {
		conns = append(conns, conn)
	}
	p.connections = make(map[string]*MCPConnection)
	p.mu.Unlock()

	var lastErr error
	for _, conn := range conns {
		if err := conn.Disconnect(); err != nil {
			lastErr = err
			p.logger.Warn("error disconnecting", "name", conn.Name, "error", err)
		}
	}
	return lastErr
}

// ConnectServer creates a connection from config, connects it, and adds it to the pool.
func (p *ConnectionPool) ConnectServer(ctx context.Context, cfg *MCPServerConfig, opts ConnectionOptions) (*MCPConnection, error) {
	transport, err := NewTransportFromConfig(cfg, opts.Logger)
	if err != nil {
		return nil, fmt.Errorf("create transport: %w", err)
	}

	conn := NewMCPConnection(cfg.Name, cfg, transport, opts)

	connectCtx := ctx
	if opts.ConnectTimeout > 0 {
		var cancel context.CancelFunc
		connectCtx, cancel = context.WithTimeout(ctx, opts.ConnectTimeout)
		defer cancel()
	}

	if err := conn.Connect(connectCtx); err != nil {
		return nil, err
	}

	p.Add(conn)
	return conn, nil
}

// ReconnectServer disconnects an existing connection and reconnects.
func (p *ConnectionPool) ReconnectServer(ctx context.Context, name string, opts ConnectionOptions) (*MCPConnection, error) {
	p.mu.RLock()
	existing, ok := p.connections[name]
	p.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("connection %q not found", name)
	}

	cfg := existing.Config

	// Disconnect old
	_ = p.Remove(name)

	// Reconnect
	return p.ConnectServer(ctx, cfg, opts)
}
