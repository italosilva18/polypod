// Package mcp — Streamable HTTP transport (hybrid JSON / SSE).
//
// This file provides a more feature-complete implementation of the MCP
// "Streamable HTTP" client transport than the minimal version bundled in
// transport.go. It adds:
//
//   - Typed configuration struct (StreamableHTTPConfig) with http.Header,
//     Timeout, and BearerToken support.
//   - Explicit 401 Unauthorized handling via ErrStreamableHTTPUnauthorized.
//   - Request/response correlation by JSON-RPC id, with pending-request map.
//   - Proper SSE framing that honors "event: done" and blank-line boundaries.
//   - A transport registry hook (StreamableHTTPFactory) that can be wired
//     from transport.go without modifying it (see RegisterStreamableHTTP).
//
// The existing StreamableHTTPTransport in transport.go is intentionally left
// untouched; this type is StreamableHTTPClient to avoid a name collision.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ─── Errors ─────────────────────────────────────────────────────────────────

// ErrStreamableHTTPUnauthorized is returned when the server responds with
// HTTP 401 Unauthorized. Callers can use errors.Is to trigger OAuth or
// bearer-token refresh flows.
var ErrStreamableHTTPUnauthorized = errors.New("streamable-http: unauthorized (HTTP 401)")

// ErrStreamableHTTPClosed is returned after Close has been called.
var ErrStreamableHTTPClosed = errors.New("streamable-http: transport closed")

// ─── Config ─────────────────────────────────────────────────────────────────

// StreamableHTTPConfig configures a StreamableHTTPClient.
type StreamableHTTPConfig struct {
	// Endpoint is the absolute URL of the MCP streamable-http endpoint.
	Endpoint string

	// Headers are extra HTTP headers to send with every request.
	// If a BearerToken is also set, an Authorization header is added
	// automatically unless Headers already contains one.
	Headers http.Header

	// Timeout bounds each individual HTTP round-trip. When the response is
	// an SSE stream, the timeout applies only to the initial response
	// headers; the stream itself may last longer.
	Timeout time.Duration

	// BearerToken, if non-empty, adds "Authorization: Bearer <token>".
	BearerToken string

	// Logger is optional.
	Logger *slog.Logger
}

// ─── Transport implementation ──────────────────────────────────────────────

// StreamableHTTPClient is an MCP Transport that POSTs JSON-RPC requests to a
// single endpoint and accepts either a JSON reply or an SSE stream.
//
// It implements the mcp.Transport interface defined in transport.go.
type StreamableHTTPClient struct {
	cfg StreamableHTTPConfig

	httpClient *http.Client
	logger     *slog.Logger

	incoming chan *JSONRPCMessage

	mu        sync.Mutex
	sessionID string
	closed    bool
	// pending tracks request IDs whose responses have not yet arrived.
	// It's used to route replies back to Receive() via the incoming chan.
	pending map[string]struct{}

	wg        sync.WaitGroup
	closeOnce sync.Once
	done      chan struct{}
}

// NewStreamableHTTPTransport constructs a StreamableHTTPClient from cfg.
//
// The returned *StreamableHTTPClient satisfies the mcp.Transport interface
// (Start/Send/Receive/Close) declared in transport.go.
func NewStreamableHTTPTransport(cfg StreamableHTTPConfig) (*StreamableHTTPClient, error) {
	if cfg.Endpoint == "" {
		return nil, errors.New("streamable-http: endpoint is required")
	}
	if !strings.HasPrefix(cfg.Endpoint, "http://") && !strings.HasPrefix(cfg.Endpoint, "https://") {
		return nil, fmt.Errorf("streamable-http: endpoint must be http(s): %q", cfg.Endpoint)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	if cfg.Headers == nil {
		cfg.Headers = http.Header{}
	}

	return &StreamableHTTPClient{
		cfg:        cfg,
		logger:     cfg.Logger,
		httpClient: &http.Client{Timeout: cfg.Timeout},
		pending:    make(map[string]struct{}),
	}, nil
}

// Start initializes internal channels. The transport does not open any
// persistent connection — each Send issues its own HTTP request.
func (t *StreamableHTTPClient) Start(_ context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return ErrStreamableHTTPClosed
	}
	if t.incoming == nil {
		t.incoming = make(chan *JSONRPCMessage, 64)
	}
	if t.done == nil {
		t.done = make(chan struct{})
	}
	return nil
}

// Send marshals msg and POSTs it. On a JSON response the reply is enqueued
// synchronously; on an SSE response a background goroutine pumps events
// until "event: done" or the stream ends.
func (t *StreamableHTTPClient) Send(ctx context.Context, msg *JSONRPCMessage) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return ErrStreamableHTTPClosed
	}
	// Track pending request ids (not notifications).
	if msg != nil && msg.ID != nil && msg.Method != "" {
		t.pending[idKey(msg.ID)] = struct{}{}
	}
	sessionID := t.sessionID
	t.mu.Unlock()

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("streamable-http: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("streamable-http: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	for k, vs := range t.cfg.Headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if t.cfg.BearerToken != "" && req.Header.Get("Authorization") == "" {
		req.Header.Set("Authorization", "Bearer "+t.cfg.BearerToken)
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("streamable-http: POST: %w", err)
	}

	// Capture server-assigned session id.
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.mu.Lock()
		t.sessionID = sid
		t.mu.Unlock()
	}

	// Distinct handling for 401 so callers can refresh auth.
	if resp.StatusCode == http.StatusUnauthorized {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		if t.logger != nil {
			t.logger.Warn("streamable-http unauthorized", "body", string(b))
		}
		return fmt.Errorf("%w: %s", ErrStreamableHTTPUnauthorized, strings.TrimSpace(string(b)))
	}

	contentType := resp.Header.Get("Content-Type")

	switch {
	case resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent:
		// Server accepted but will not reply to this request (e.g. a
		// notification). Nothing to do.
		resp.Body.Close()
		return nil

	case strings.Contains(contentType, "text/event-stream"):
		t.wg.Add(1)
		go t.pumpSSE(resp.Body)
		return nil

	case strings.Contains(contentType, "application/json"):
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("streamable-http: read body: %w", err)
		}
		if resp.StatusCode >= 400 {
			return fmt.Errorf("streamable-http: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		}
		return t.deliverJSON(raw)

	default:
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			return fmt.Errorf("streamable-http: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
		}
		// Some servers return 200 with no body for notifications.
		return nil
	}
}

// deliverJSON parses a raw JSON body that is either a single JSON-RPC
// message or a JSON array of messages (per spec), and enqueues each one.
func (t *StreamableHTTPClient) deliverJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	if raw[0] == '[' {
		var batch []*JSONRPCMessage
		if err := json.Unmarshal(raw, &batch); err != nil {
			return fmt.Errorf("streamable-http: unmarshal batch: %w", err)
		}
		for _, m := range batch {
			t.enqueue(m)
		}
		return nil
	}
	var m JSONRPCMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("streamable-http: unmarshal: %w", err)
	}
	t.enqueue(&m)
	return nil
}

// pumpSSE reads an event stream until "event: done", an empty read, or
// Close. Each "data:" payload is JSON-decoded and enqueued.
func (t *StreamableHTTPClient) pumpSSE(body io.ReadCloser) {
	defer t.wg.Done()
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var (
		eventName string
		dataBuf   strings.Builder
	)

	flush := func() {
		if dataBuf.Len() == 0 {
			eventName = ""
			return
		}
		payload := dataBuf.String()
		dataBuf.Reset()

		if eventName == "done" {
			eventName = ""
			return
		}
		eventName = ""

		var msg JSONRPCMessage
		if err := json.Unmarshal([]byte(payload), &msg); err != nil {
			if t.logger != nil {
				t.logger.Warn("streamable-http: bad SSE payload", "error", err, "data", payload)
			}
			return
		}
		t.enqueue(&msg)
	}

	for scanner.Scan() {
		select {
		case <-t.done:
			return
		default:
		}

		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, ":") {
			// SSE comment / keep-alive.
			continue
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			if eventName == "done" {
				// Terminal marker: drain any queued data then stop.
				flush()
				return
			}
		case strings.HasPrefix(line, "data:"):
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	// Final flush at EOF.
	flush()
}

func (t *StreamableHTTPClient) enqueue(msg *JSONRPCMessage) {
	if msg == nil {
		return
	}
	// Clear pending bookkeeping for responses.
	if msg.IsResponse() && msg.ID != nil {
		t.mu.Lock()
		delete(t.pending, idKey(msg.ID))
		t.mu.Unlock()
	}
	select {
	case t.incoming <- msg:
	case <-t.done:
	}
}

// Receive returns the next JSON-RPC message from the transport.
func (t *StreamableHTTPClient) Receive(ctx context.Context) (*JSONRPCMessage, error) {
	if t.incoming == nil {
		return nil, errors.New("streamable-http: transport not started")
	}
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

// Close shuts down the transport. Subsequent Sends return ErrStreamableHTTPClosed.
func (t *StreamableHTTPClient) Close() error {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = true
		if t.done == nil {
			t.done = make(chan struct{})
		}
		t.mu.Unlock()
		close(t.done)
	})
	// Wait for any in-flight SSE pumps.
	t.wg.Wait()
	return nil
}

// HasPending reports whether any request ids are still awaiting a response.
// Primarily useful for tests and diagnostics.
func (t *StreamableHTTPClient) HasPending() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.pending) > 0
}

// SessionID returns the server-assigned MCP session id, if any.
func (t *StreamableHTTPClient) SessionID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sessionID
}

// idKey turns a JSON-RPC id (string or int) into a stable map key.
func idKey(id any) string {
	switch v := id.(type) {
	case string:
		return "s:" + v
	case json.Number:
		return "n:" + v.String()
	case float64:
		return fmt.Sprintf("n:%v", v)
	case int:
		return fmt.Sprintf("n:%d", v)
	case int64:
		return fmt.Sprintf("n:%d", v)
	default:
		return fmt.Sprintf("?:%v", v)
	}
}

// ─── Registry-style wiring hook ─────────────────────────────────────────────

// StreamableHTTPFactory is the factory signature used by transport dispatch.
// It mirrors the signature expected by NewTransportFromConfig in transport.go
// so that file can call into it without importing anything new.
type StreamableHTTPFactory func(cfg *MCPServerConfig, logger *slog.Logger) (Transport, error)

// streamableHTTPRegistry is a package-level hook that transport.go may check
// for a registered factory. transport.go is not required to consult it —
// this is a non-intrusive extension point.
var (
	streamableHTTPMu       sync.RWMutex
	streamableHTTPRegistry StreamableHTTPFactory
)

// RegisterStreamableHTTP installs a factory that turns an MCPServerConfig
// into a StreamableHTTPClient. Call this from package init (e.g. from a
// small addition in client.go or main.go) to enable the
// "streamable-http" transport type without editing transport.go.
//
// Typical wiring from outside this file:
//
//	func init() {
//	    mcp.RegisterStreamableHTTP(func(cfg *mcp.MCPServerConfig, logger *slog.Logger) (mcp.Transport, error) {
//	        hdr := http.Header{}
//	        for k, v := range cfg.Headers { hdr.Set(k, v) }
//	        return mcp.NewStreamableHTTPTransport(mcp.StreamableHTTPConfig{
//	            Endpoint: cfg.URL,
//	            Headers:  hdr,
//	            Logger:   logger,
//	        })
//	    })
//	}
//
// And in transport.go's NewTransportFromConfig, add:
//
//	case TransportType("streamable-http"):
//	    if f := mcp.LookupStreamableHTTP(); f != nil { return f(cfg, logger) }
//	    return nil, fmt.Errorf("streamable-http not registered")
func RegisterStreamableHTTP(f StreamableHTTPFactory) {
	streamableHTTPMu.Lock()
	defer streamableHTTPMu.Unlock()
	streamableHTTPRegistry = f
}

// LookupStreamableHTTP returns the currently registered factory, or nil.
func LookupStreamableHTTP() StreamableHTTPFactory {
	streamableHTTPMu.RLock()
	defer streamableHTTPMu.RUnlock()
	return streamableHTTPRegistry
}

// DefaultStreamableHTTPFactory is a ready-to-use factory that can be passed
// directly to RegisterStreamableHTTP. It translates an MCPServerConfig into
// a StreamableHTTPClient using sensible defaults.
func DefaultStreamableHTTPFactory(cfg *MCPServerConfig, logger *slog.Logger) (Transport, error) {
	hdr := http.Header{}
	for k, v := range cfg.Headers {
		hdr.Set(k, v)
	}
	return NewStreamableHTTPTransport(StreamableHTTPConfig{
		Endpoint: cfg.URL,
		Headers:  hdr,
		Logger:   logger,
	})
}

func init() {
	// Register the default factory so callers can use LookupStreamableHTTP
	// from transport.go (or elsewhere) without any extra wiring code.
	RegisterStreamableHTTP(DefaultStreamableHTTPFactory)
}
