package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// helper: decode the JSON-RPC request posted by the client.
func readRPCRequest(t *testing.T, r *http.Request) *JSONRPCMessage {
	t.Helper()
	var msg JSONRPCMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return &msg
}

// TestStreamableHTTPClient_JSONResponse verifies the JSON-inline code path.
func TestStreamableHTTPClient_JSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("want POST, got %s", r.Method)
		}
		if got := r.Header.Get("Accept"); !strings.Contains(got, "application/json") || !strings.Contains(got, "text/event-stream") {
			t.Errorf("Accept header missing hybrid values: %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Errorf("Authorization = %q, want Bearer secret-token", got)
		}
		if got := r.Header.Get("X-Custom"); got != "abc" {
			t.Errorf("X-Custom = %q, want abc", got)
		}

		req := readRPCRequest(t, r)
		if req.Method != "tools/list" {
			t.Errorf("method = %q, want tools/list", req.Method)
		}

		// Reply inline as JSON.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "sess-123")
		resp := JSONRPCMessage{
			JSONRPC: JSONRPCVersion,
			ID:      req.ID,
			Result:  json.RawMessage(`{"tools":[]}`),
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	hdr := http.Header{}
	hdr.Set("X-Custom", "abc")

	tr, err := NewStreamableHTTPTransport(StreamableHTTPConfig{
		Endpoint:    srv.URL,
		Headers:     hdr,
		BearerToken: "secret-token",
		Timeout:     5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewStreamableHTTPTransport: %v", err)
	}
	defer tr.Close()

	ctx := context.Background()
	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	req, err := NewRequest(1, "tools/list", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	if err := tr.Send(ctx, req); err != nil {
		t.Fatalf("Send: %v", err)
	}

	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	got, err := tr.Receive(rctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if !got.IsResponse() {
		t.Errorf("expected response, got %+v", got)
	}
	// The JSON-RPC id round-trips as float64 because encoding/json defaults
	// to that for untyped numbers — verify via string comparison.
	if fmt.Sprint(got.ID) != "1" {
		t.Errorf("id = %v, want 1", got.ID)
	}
	if tr.SessionID() != "sess-123" {
		t.Errorf("session id not captured: %q", tr.SessionID())
	}
	if tr.HasPending() {
		t.Errorf("pending should be empty after response delivered")
	}
}

// TestStreamableHTTPClient_SSEStream verifies the SSE code path, including
// multiple data events and termination via "event: done".
func TestStreamableHTTPClient_SSEStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := readRPCRequest(t, r)

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("httptest.ResponseWriter does not support Flusher")
		}

		// Event 1: a progress notification.
		note := JSONRPCMessage{
			JSONRPC: JSONRPCVersion,
			Method:  "notifications/progress",
			Params:  json.RawMessage(`{"progress":0.5}`),
		}
		b1, _ := json.Marshal(note)
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", b1)
		flusher.Flush()

		// Event 2: the final response correlated by id.
		resp := JSONRPCMessage{
			JSONRPC: JSONRPCVersion,
			ID:      req.ID,
			Result:  json.RawMessage(`{"ok":true}`),
		}
		b2, _ := json.Marshal(resp)
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", b2)
		flusher.Flush()

		// Terminate the stream.
		fmt.Fprintf(w, "event: done\ndata: bye\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	tr, err := NewStreamableHTTPTransport(StreamableHTTPConfig{
		Endpoint: srv.URL,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewStreamableHTTPTransport: %v", err)
	}
	defer tr.Close()

	ctx := context.Background()
	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	req, err := NewRequest(42, "tools/call", map[string]any{"name": "echo"})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if err := tr.Send(ctx, req); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Drain both messages.
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	m1, err := tr.Receive(rctx)
	if err != nil {
		t.Fatalf("Receive 1: %v", err)
	}
	if !m1.IsNotification() || m1.Method != "notifications/progress" {
		t.Errorf("first message not a progress notification: %+v", m1)
	}

	m2, err := tr.Receive(rctx)
	if err != nil {
		t.Fatalf("Receive 2: %v", err)
	}
	if !m2.IsResponse() {
		t.Errorf("second message not a response: %+v", m2)
	}
	if fmt.Sprint(m2.ID) != "42" {
		t.Errorf("id correlation failed: got %v want 42", m2.ID)
	}
	if tr.HasPending() {
		t.Errorf("pending should be empty after correlated response")
	}
}

// TestStreamableHTTPClient_Unauthorized verifies that a 401 response is
// surfaced distinctly via ErrStreamableHTTPUnauthorized.
func TestStreamableHTTPClient_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="mcp"`)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
	}))
	defer srv.Close()

	tr, err := NewStreamableHTTPTransport(StreamableHTTPConfig{
		Endpoint:    srv.URL,
		BearerToken: "expired",
		Timeout:     3 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewStreamableHTTPTransport: %v", err)
	}
	defer tr.Close()

	ctx := context.Background()
	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	req, err := NewRequest(7, "initialize", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	err = tr.Send(ctx, req)
	if err == nil {
		t.Fatalf("expected error on 401, got nil")
	}
	if !errors.Is(err, ErrStreamableHTTPUnauthorized) {
		t.Errorf("error does not wrap ErrStreamableHTTPUnauthorized: %v", err)
	}
}

// TestStreamableHTTPClient_FactoryRegistration confirms that the init()
// factory is wired and produces a working transport from MCPServerConfig.
func TestStreamableHTTPClient_FactoryRegistration(t *testing.T) {
	f := LookupStreamableHTTP()
	if f == nil {
		t.Fatalf("no streamable-http factory registered")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := readRPCRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		resp := JSONRPCMessage{
			JSONRPC: JSONRPCVersion,
			ID:      req.ID,
			Result:  json.RawMessage(`{"ack":true}`),
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := &MCPServerConfig{
		Name:    "test",
		URL:     srv.URL,
		Headers: map[string]string{"X-Trace": "1"},
	}
	tr, err := f(cfg, nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	defer tr.Close()

	ctx := context.Background()
	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	req, _ := NewRequest(99, "ping", nil)
	if err := tr.Send(ctx, req); err != nil {
		t.Fatalf("Send: %v", err)
	}

	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	msg, err := tr.Receive(rctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if !msg.IsResponse() {
		t.Errorf("expected response, got %+v", msg)
	}
}
