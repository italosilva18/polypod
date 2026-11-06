package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/costa/polypod/internal/skill"
	"github.com/costa/polypod/internal/types"
)

// Run a real stdio SDK server in the test executable, without external binaries.
func TestMCPChildServer(t *testing.T) {
	if os.Getenv("POLYPOD_MCP_TEST_CHILD") != "1" {
		return
	}
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "polyclaw-test", Version: "1"}, nil)
	server.AddTool(&mcpsdk.Tool{Name: "observe", InputSchema: map[string]any{
		"type": "object", "properties": map[string]any{
			"count":   map[string]any{"type": "integer"},
			"enabled": map[string]any{"type": "boolean"},
			"targets": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"options": map[string]any{"type": "object"},
		}, "required": []string{"count", "enabled"}, "additionalProperties": false,
	}}, func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		var args map[string]any
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return nil, err
		}
		if args["count"] != float64(2) || args["enabled"] != true || len(args["targets"].([]any)) != 1 || args["options"].(map[string]any)["mode"] != "safe" {
			panic("typed arguments lost")
		}
		return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{
			&mcpsdk.TextContent{Text: os.Getenv("POLYPOD_MCP_INHERITED") + ":" + os.Getenv("POLYPOD_MCP_OVERRIDE")},
			&mcpsdk.ImageContent{Data: []byte("image bytes"), MIMEType: "image/png"},
		}}, nil
	})
	if err := server.Run(context.Background(), &mcpsdk.StdioTransport{}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestManagerStdioTypedAndImageResults(t *testing.T) {
	t.Setenv("POLYPOD_MCP_INHERITED", "inherited")
	t.Setenv("POLYPOD_MCP_OVERRIDE", "old")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	manager := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer manager.Close()
	if err := manager.Connect(ctx, ServerConfig{Name: "polyclaw", Transport: "stdio", Command: binary, Args: []string{"-test.run=^TestMCPChildServer$"}, Env: map[string]string{"POLYPOD_MCP_TEST_CHILD": "1", "POLYPOD_MCP_OVERRIDE": "override"}}); err != nil {
		t.Fatal(err)
	}
	registry := skill.NewRegistry()
	if count := manager.RegisterTools(registry); count != 1 {
		t.Fatalf("registered %d tools", count)
	}
	schema := registry.ToolDefinitions([]string{"mcp_polyclaw_observe"})[0].Function.Parameters.(map[string]any)
	if schema["additionalProperties"] != false || schema["properties"].(map[string]any)["count"].(map[string]any)["type"] != "integer" {
		t.Fatalf("schema lost: %v", schema)
	}
	args := `{"count":2,"enabled":true,"targets":["screen"],"options":{"mode":"safe"}}`
	assertResult := func(result types.ToolResult, err error) {
		t.Helper()
		if err != nil || result.IsError || !strings.Contains(result.Content, "inherited:override") {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		if len(result.Images) != 1 || result.Images[0].MIMEType != "image/png" || result.Images[0].Base64 != base64.StdEncoding.EncodeToString([]byte("image bytes")) {
			t.Fatalf("images=%+v", result.Images)
		}
	}
	assertResult(registry.ExecuteResult("mcp_polyclaw_observe", args))
	var typed map[string]any
	if err := json.Unmarshal([]byte(args), &typed); err != nil {
		t.Fatal(err)
	}
	wrapped := WrapMCPTool(manager, "polyclaw", "observe", "", schema)
	assertResult(wrapped.Execute(types.ToolContext{Context: ctx}, typed))
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, _, err := manager.CallTool(cancelled, "polyclaw", "observe", typed); err == nil {
		t.Fatal("expected cancellation")
	}
}

func TestManagerStreamableHTTPAuthenticated(t *testing.T) {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "gateway", Version: "1"}, nil)
	server.AddTool(&mcpsdk.Tool{Name: "status", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "existing gateway"}}}, nil
	})
	handler := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return server }, nil)
	var authenticated atomic.Int64
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		authenticated.Add(1)
		handler.ServeHTTP(w, r)
	}))
	defer endpoint.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	manager := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer manager.Close()
	if err := manager.Connect(ctx, ServerConfig{Name: "polyclaw", Transport: "http", URL: endpoint.URL, Headers: map[string]string{"Authorization": "Bearer test-token"}}); err != nil {
		t.Fatal(err)
	}
	result, isError, err := manager.CallTool(ctx, "polyclaw", "status", map[string]any{})
	if err != nil || isError || mcpToolResult(result).Content != "existing gateway" {
		t.Fatalf("HTTP call failed: %v", err)
	}
	if authenticated.Load() < 3 {
		t.Fatal("missing authenticated MCP lifecycle requests")
	}
}

func TestMCPHTTPClientDoesNotForwardCredentialsOnRedirect(t *testing.T) {
	var targetRequests atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetRequests.Add(1) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	response, err := mcpHTTPClient(map[string]string{"Authorization": "Bearer test-token"}).Get(source.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusTemporaryRedirect || targetRequests.Load() != 0 {
		t.Fatal("redirect followed with MCP credentials")
	}
}
