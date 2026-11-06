package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

// ─── Transport Interface ────────────────────────────────────────────────────

// Transport is the interface for MCP communication channels.
type Transport interface {
	// Start initializes the transport. Must be called before Send/Receive.
	Start(ctx context.Context) error

	// Send sends a JSON-RPC message.
	Send(ctx context.Context, msg *JSONRPCMessage) error

	// Receive returns the next incoming message. Blocks until available.
	// Returns io.EOF when the transport is closed.
	Receive(ctx context.Context) (*JSONRPCMessage, error)

	// Close shuts down the transport.
	Close() error
}

// ─── Stdio Transport ────────────────────────────────────────────────────────

// StdioTransport communicates with an MCP server via stdin/stdout of a subprocess.
type StdioTransport struct {
	Command string
	Args    []string
	Env     map[string]string
	Logger  *slog.Logger

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	scanner *bufio.Scanner

	mu     sync.Mutex
	closed bool
}

// Start spawns the subprocess and sets up pipes.
func (t *StdioTransport) Start(ctx context.Context) error {
	t.cmd = exec.CommandContext(ctx, t.Command, t.Args...)

	// Merge env
	if len(t.Env) > 0 {
		env := t.cmd.Environ()
		for k, v := range t.Env {
			env = append(env, k+"="+v)
		}
		t.cmd.Env = env
	}

	var err error
	t.stdin, err = t.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	t.stdout, err = t.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	t.stderr, err = t.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := t.cmd.Start(); err != nil {
		return fmt.Errorf("start command %q: %w", t.Command, err)
	}

	t.scanner = bufio.NewScanner(t.stdout)
	t.scanner.Buffer(make([]byte, 0, 4*1024*1024), 4*1024*1024) // 4MB max message

	// Drain stderr in background
	if t.Logger != nil {
		go t.drainStderr()
	}

	return nil
}

func (t *StdioTransport) drainStderr() {
	scanner := bufio.NewScanner(t.stderr)
	for scanner.Scan() {
		t.Logger.Debug("mcp stdio stderr", "line", scanner.Text())
	}
}

// Send writes a JSON-RPC message to stdin.
func (t *StdioTransport) Send(_ context.Context, msg *JSONRPCMessage) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return errors.New("transport closed")
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	data = append(data, '\n')
	if _, err := t.stdin.Write(data); err != nil {
		return fmt.Errorf("write to stdin: %w", err)
	}

	return nil
}

// Receive reads the next JSON-RPC message from stdout.
func (t *StdioTransport) Receive(_ context.Context) (*JSONRPCMessage, error) {
	if !t.scanner.Scan() {
		if err := t.scanner.Err(); err != nil {
			return nil, fmt.Errorf("read from stdout: %w", err)
		}
		return nil, io.EOF
	}

	line := t.scanner.Bytes()
	var msg JSONRPCMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil, fmt.Errorf("unmarshal message: %w", err)
	}

	return &msg, nil
}

// Close terminates the subprocess.
func (t *StdioTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}
	t.closed = true

	var errs []error

	if t.stdin != nil {
		errs = append(errs, t.stdin.Close())
	}

	if t.cmd != nil && t.cmd.Process != nil {
		// Give the process a moment to exit gracefully
		done := make(chan error, 1)
		go func() { done <- t.cmd.Wait() }()

		select {
		case <-done:
			// exited
		case <-time.After(5 * time.Second):
			_ = t.cmd.Process.Kill()
			<-done
		}
	}

	return errors.Join(errs...)
}

// Stderr returns the stderr reader for the subprocess.
func (t *StdioTransport) Stderr() io.Reader {
	return t.stderr
}

// ─── SSE Transport ──────────────────────────────────────────────────────────

// SSETransport implements MCP over Server-Sent Events (for older MCP servers).
type SSETransport struct {
	URL     string
	Headers map[string]string
	Logger  *slog.Logger

	client    *http.Client
	incoming  chan *JSONRPCMessage
	postURL   string // endpoint URL received from the SSE stream
	closeOnce sync.Once
	done      chan struct{}
	closeErr  error
}

// Start connects to the SSE endpoint and begins reading events.
func (t *SSETransport) Start(ctx context.Context) error {
	t.incoming = make(chan *JSONRPCMessage, 64)
	t.done = make(chan struct{})

	if t.client == nil {
		t.client = &http.Client{Timeout: 0} // no timeout for SSE
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.URL, nil)
	if err != nil {
		return fmt.Errorf("create SSE request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	for k, v := range t.Headers {
		req.Header.Set(k, v)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("SSE connect: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return fmt.Errorf("SSE connect: HTTP %d", resp.StatusCode)
	}

	go t.readSSE(resp.Body)
	return nil
}

func (t *SSETransport) readSSE(body io.ReadCloser) {
	defer body.Close()
	defer close(t.incoming)

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 1*1024*1024), 1*1024*1024)

	var eventType, eventData string

	for scanner.Scan() {
		line := scanner.Text()

		select {
		case <-t.done:
			return
		default:
		}

		if line == "" {
			// Empty line = end of event
			if eventType != "" && eventData != "" {
				t.handleSSEEvent(eventType, eventData)
			}
			eventType = ""
			eventData = ""
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			eventData = strings.TrimPrefix(line, "data: ")
		} else if strings.HasPrefix(line, ":") {
			// Comment, ignore (keep-alive)
			continue
		}
	}
}

func (t *SSETransport) handleSSEEvent(eventType, data string) {
	switch eventType {
	case "endpoint":
		// The server tells us where to POST messages
		t.postURL = data
		if t.Logger != nil {
			t.Logger.Debug("SSE endpoint received", "url", data)
		}
	case "message":
		var msg JSONRPCMessage
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			if t.Logger != nil {
				t.Logger.Warn("SSE invalid message", "error", err)
			}
			return
		}
		select {
		case t.incoming <- &msg:
		case <-t.done:
		}
	}
}

// Send POSTs a JSON-RPC message to the server's endpoint.
func (t *SSETransport) Send(ctx context.Context, msg *JSONRPCMessage) error {
	if t.postURL == "" {
		return errors.New("SSE endpoint not yet received from server")
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.postURL, strings.NewReader(string(data)))
	if err != nil {
		return fmt.Errorf("create POST request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range t.Headers {
		req.Header.Set(k, v)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("SSE POST: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("SSE POST: HTTP %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// Receive returns the next message from the SSE stream.
func (t *SSETransport) Receive(ctx context.Context) (*JSONRPCMessage, error) {
	select {
	case msg, ok := <-t.incoming:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, io.EOF
	}
}

// Close stops the SSE transport.
func (t *SSETransport) Close() error {
	t.closeOnce.Do(func() {
		close(t.done)
	})
	return t.closeErr
}

// ─── StreamableHTTP Transport ───────────────────────────────────────────────

// StreamableHTTPTransport implements the MCP Streamable HTTP transport.
// Sends JSON-RPC via HTTP POST and receives responses via SSE or JSON.
type StreamableHTTPTransport struct {
	URL     string
	Headers map[string]string
	Logger  *slog.Logger

	client    *http.Client
	sessionID string
	incoming  chan *JSONRPCMessage
	closeOnce sync.Once
	done      chan struct{}
}

// Start initializes the Streamable HTTP transport.
func (t *StreamableHTTPTransport) Start(_ context.Context) error {
	t.incoming = make(chan *JSONRPCMessage, 64)
	t.done = make(chan struct{})

	if t.client == nil {
		t.client = &http.Client{Timeout: 60 * time.Second}
	}
	return nil
}

// Send sends a JSON-RPC message via HTTP POST.
func (t *StreamableHTTPTransport) Send(ctx context.Context, msg *JSONRPCMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.URL, strings.NewReader(string(data)))
	if err != nil {
		return fmt.Errorf("create POST request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if t.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", t.sessionID)
	}
	for k, v := range t.Headers {
		req.Header.Set(k, v)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP POST: %w", err)
	}

	// Capture session ID from response
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.sessionID = sid
	}

	contentType := resp.Header.Get("Content-Type")

	switch {
	case resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent:
		resp.Body.Close()
		return nil

	case strings.Contains(contentType, "text/event-stream"):
		// Read SSE response in background
		go t.readHTTPSSE(resp.Body)
		return nil

	case strings.Contains(contentType, "application/json"):
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("read response: %w", err)
		}

		if resp.StatusCode >= 400 {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
		}

		var respMsg JSONRPCMessage
		if err := json.Unmarshal(body, &respMsg); err != nil {
			return fmt.Errorf("unmarshal response: %w", err)
		}
		select {
		case t.incoming <- &respMsg:
		case <-t.done:
		}
		return nil

	default:
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			return fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return nil
	}
}

func (t *StreamableHTTPTransport) readHTTPSSE(body io.ReadCloser) {
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 1*1024*1024), 1*1024*1024)

	var eventData string

	for scanner.Scan() {
		line := scanner.Text()

		select {
		case <-t.done:
			return
		default:
		}

		if line == "" {
			if eventData != "" {
				var msg JSONRPCMessage
				if err := json.Unmarshal([]byte(eventData), &msg); err == nil {
					select {
					case t.incoming <- &msg:
					case <-t.done:
						return
					}
				}
				eventData = ""
			}
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			eventData = strings.TrimPrefix(line, "data: ")
		}
	}
}

// Receive returns the next message from the HTTP transport.
func (t *StreamableHTTPTransport) Receive(ctx context.Context) (*JSONRPCMessage, error) {
	select {
	case msg, ok := <-t.incoming:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, io.EOF
	}
}

// Close shuts down the Streamable HTTP transport.
func (t *StreamableHTTPTransport) Close() error {
	t.closeOnce.Do(func() {
		close(t.done)
	})
	return nil
}

// ─── WebSocket Transport ────────────────────────────────────────────────────

// WebSocketTransport implements MCP over WebSocket.
type WebSocketTransport struct {
	URL     string
	Headers map[string]string
	Logger  *slog.Logger

	conn      *websocket.Conn
	incoming  chan *JSONRPCMessage
	closeOnce sync.Once
	done      chan struct{}
	mu        sync.Mutex
}

// Start connects to the WebSocket server.
func (t *WebSocketTransport) Start(ctx context.Context) error {
	t.incoming = make(chan *JSONRPCMessage, 64)
	t.done = make(chan struct{})

	origin := t.URL
	if strings.HasPrefix(origin, "ws://") {
		origin = "http://" + strings.TrimPrefix(origin, "ws://")
	} else if strings.HasPrefix(origin, "wss://") {
		origin = "https://" + strings.TrimPrefix(origin, "wss://")
	}

	config, err := websocket.NewConfig(t.URL, origin)
	if err != nil {
		return fmt.Errorf("websocket config: %w", err)
	}
	config.Protocol = []string{"mcp"}
	for k, v := range t.Headers {
		config.Header.Set(k, v)
	}

	t.conn, err = websocket.DialConfig(config)
	if err != nil {
		return fmt.Errorf("websocket connect: %w", err)
	}

	go t.readLoop()
	return nil
}

func (t *WebSocketTransport) readLoop() {
	defer close(t.incoming)

	for {
		select {
		case <-t.done:
			return
		default:
		}

		var data string
		if err := websocket.Message.Receive(t.conn, &data); err != nil {
			if t.Logger != nil && !errors.Is(err, io.EOF) {
				t.Logger.Debug("websocket read error", "error", err)
			}
			return
		}

		var msg JSONRPCMessage
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			if t.Logger != nil {
				t.Logger.Warn("websocket invalid message", "error", err)
			}
			continue
		}

		select {
		case t.incoming <- &msg:
		case <-t.done:
			return
		}
	}
}

// Send writes a JSON-RPC message to the WebSocket.
func (t *WebSocketTransport) Send(_ context.Context, msg *JSONRPCMessage) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	if err := websocket.Message.Send(t.conn, string(data)); err != nil {
		return fmt.Errorf("websocket send: %w", err)
	}
	return nil
}

// Receive returns the next message from the WebSocket.
func (t *WebSocketTransport) Receive(ctx context.Context) (*JSONRPCMessage, error) {
	select {
	case msg, ok := <-t.incoming:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, io.EOF
	}
}

// Close shuts down the WebSocket transport.
func (t *WebSocketTransport) Close() error {
	t.closeOnce.Do(func() {
		close(t.done)
		if t.conn != nil {
			t.conn.Close()
		}
	})
	return nil
}

// ─── InProcess Transport ────────────────────────────────────────────────────

// InProcessTransport is an in-memory transport for embedded MCP servers.
// Messages sent on one side are delivered to the other.
type InProcessTransport struct {
	incoming chan *JSONRPCMessage
	peer     *InProcessTransport
	once     sync.Once
	done     chan struct{}
}

// NewInProcessTransportPair creates a linked pair of in-process transports.
// Messages sent on one are received by the other.
func NewInProcessTransportPair() (*InProcessTransport, *InProcessTransport) {
	a := &InProcessTransport{
		incoming: make(chan *JSONRPCMessage, 64),
		done:     make(chan struct{}),
	}
	b := &InProcessTransport{
		incoming: make(chan *JSONRPCMessage, 64),
		done:     make(chan struct{}),
	}
	a.peer = b
	b.peer = a
	return a, b
}

// Start is a no-op for in-process transport.
func (t *InProcessTransport) Start(_ context.Context) error { return nil }

// Send delivers a message to the peer.
func (t *InProcessTransport) Send(_ context.Context, msg *JSONRPCMessage) error {
	select {
	case <-t.done:
		return errors.New("transport closed")
	default:
	}

	select {
	case t.peer.incoming <- msg:
		return nil
	case <-t.done:
		return errors.New("transport closed")
	}
}

// Receive reads the next message.
func (t *InProcessTransport) Receive(ctx context.Context) (*JSONRPCMessage, error) {
	select {
	case msg, ok := <-t.incoming:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, io.EOF
	}
}

// Close shuts down this side and the peer.
func (t *InProcessTransport) Close() error {
	t.once.Do(func() {
		close(t.done)
		close(t.incoming)
		// Close peer too
		t.peer.once.Do(func() {
			close(t.peer.done)
			close(t.peer.incoming)
		})
	})
	return nil
}

// ─── Transport Auto-Detection ───────────────────────────────────────────────

// TransportType enumerates supported transport types.
type TransportType string

const (
	TransportStdio     TransportType = "stdio"
	TransportSSE       TransportType = "sse"
	TransportHTTP      TransportType = "http"
	TransportWebSocket TransportType = "ws"
	TransportInProcess TransportType = "inprocess"
)

// NewTransportFromConfig creates the appropriate transport from a server configuration.
func NewTransportFromConfig(cfg *MCPServerConfig, logger *slog.Logger) (Transport, error) {
	switch cfg.Type {
	case TransportStdio, "":
		return &StdioTransport{
			Command: cfg.Command,
			Args:    cfg.Args,
			Env:     cfg.Env,
			Logger:  logger,
		}, nil

	case TransportSSE:
		return &SSETransport{
			URL:     cfg.URL,
			Headers: cfg.Headers,
			Logger:  logger,
		}, nil

	case TransportHTTP:
		return &StreamableHTTPTransport{
			URL:     cfg.URL,
			Headers: cfg.Headers,
			Logger:  logger,
		}, nil

	case TransportWebSocket:
		return &WebSocketTransport{
			URL:     cfg.URL,
			Headers: cfg.Headers,
			Logger:  logger,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported transport type: %s", cfg.Type)
	}
}

// ─── MCPServerConfig (shared config type) ───────────────────────────────────

// MCPServerConfig describes how to connect to an MCP server.
type MCPServerConfig struct {
	Name    string            `json:"name" yaml:"name"`
	Type    TransportType     `json:"type,omitempty" yaml:"type,omitempty"`
	Command string            `json:"command,omitempty" yaml:"command,omitempty"`
	Args    []string          `json:"args,omitempty" yaml:"args,omitempty"`
	URL     string            `json:"url,omitempty" yaml:"url,omitempty"`
	Env     map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	Headers map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	Scope   string            `json:"scope,omitempty" yaml:"scope,omitempty"` // user, project, local, etc.

	// OAuth configuration for remote servers.
	OAuth *OAuthConfig `json:"oauth,omitempty" yaml:"oauth,omitempty"`
}

// ─── Connection Health Monitoring ───────────────────────────────────────────

// HealthMonitor periodically pings a transport and reports connectivity.
type HealthMonitor struct {
	transport Transport
	interval  time.Duration
	logger    *slog.Logger
	idGen     *IDGenerator
	onFailure func(error)

	cancel context.CancelFunc
	done   chan struct{}
}

// NewHealthMonitor creates a health monitor for a transport.
func NewHealthMonitor(t Transport, interval time.Duration, logger *slog.Logger, onFailure func(error)) *HealthMonitor {
	return &HealthMonitor{
		transport: t,
		interval:  interval,
		logger:    logger,
		idGen:     &IDGenerator{},
		onFailure: onFailure,
		done:      make(chan struct{}),
	}
}

// Start begins the health monitoring loop.
func (h *HealthMonitor) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel

	go func() {
		defer close(h.done)
		ticker := time.NewTicker(h.interval)
		defer ticker.Stop()

		consecutiveFailures := 0
		const maxFailures = 3

		for {
			select {
			case <-ticker.C:
				pingMsg, _ := NewRequest(h.idGen.Next(), MethodPing, nil)
				ctx2, cancel2 := context.WithTimeout(ctx, 10*time.Second)
				err := h.transport.Send(ctx2, pingMsg)
				cancel2()

				if err != nil {
					consecutiveFailures++
					if h.logger != nil {
						h.logger.Warn("health ping failed",
							"failures", consecutiveFailures,
							"error", err,
						)
					}
					if consecutiveFailures >= maxFailures && h.onFailure != nil {
						h.onFailure(fmt.Errorf("health check failed %d times: %w", consecutiveFailures, err))
						return
					}
				} else {
					consecutiveFailures = 0
				}

			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stop stops the health monitoring.
func (h *HealthMonitor) Stop() {
	if h.cancel != nil {
		h.cancel()
		<-h.done
	}
}

// ─── Reconnection with Exponential Backoff ──────────────────────────────────

// ReconnectConfig configures reconnection behavior.
type ReconnectConfig struct {
	MaxAttempts       int
	InitialBackoff    time.Duration
	MaxBackoff        time.Duration
	Multiplier        float64       // backoff growth factor; 0 defaults to 2.0
	HeartbeatInterval time.Duration // 0 disables heartbeats
	Logger            *slog.Logger  // nil uses slog.Default()
}

// DefaultReconnectConfig returns sensible defaults.
func DefaultReconnectConfig() ReconnectConfig {
	return ReconnectConfig{
		MaxAttempts:    5,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
	}
}

// ReconnectLoop attempts to re-establish a transport connection with exponential backoff.
// It calls connect() on each attempt and returns the error of the final failed attempt,
// or nil on success.
func ReconnectLoop(ctx context.Context, cfg ReconnectConfig, logger *slog.Logger, connect func(ctx context.Context) error) error {
	backoff := cfg.InitialBackoff

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		if logger != nil {
			logger.Info("reconnection attempt",
				"attempt", attempt,
				"max", cfg.MaxAttempts,
				"backoff", backoff,
			)
		}

		err := connect(ctx)
		if err == nil {
			if logger != nil {
				logger.Info("reconnection successful", "attempt", attempt)
			}
			return nil
		}

		if attempt == cfg.MaxAttempts {
			return fmt.Errorf("reconnection failed after %d attempts: %w", cfg.MaxAttempts, err)
		}

		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}

		// Exponential backoff
		backoff = backoff * 2
		if backoff > cfg.MaxBackoff {
			backoff = cfg.MaxBackoff
		}
	}

	return errors.New("reconnection: max attempts reached")
}
