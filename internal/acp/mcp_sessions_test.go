package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/costa/polypod/internal/adapter"
	"github.com/costa/polypod/internal/ai"
	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/types"
)

// TestACPMCPStdioHelper runs an SDK stdio MCP server inside the test binary
// when invoked as a child subprocess by ACP stdio MCP tests.
func TestACPMCPStdioHelper(t *testing.T) {
	if os.Getenv("POLYPOD_ACP_MCP_STDIO_CHILD") != "1" {
		return
	}
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "acp-stdio-helper", Version: "1.0.0"}, nil)
	srv.AddTool(&mcpsdk.Tool{
		Name:        "ping",
		Description: "Echoes editor stdio environment",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"note": map[string]any{"type": "string"},
			},
		},
	}, func(_ context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		var args map[string]any
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &args)
		}
		note, _ := args["note"].(string)
		text := os.Getenv("ACP_STDIO_TOKEN") + "|" + os.Getenv("PWD") + "|" + note
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: text}},
		}, nil
	})
	if err := srv.Run(context.Background(), &mcpsdk.StdioTransport{}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func startTestHTTPMCPServer(t *testing.T, expectedAuth, responsePrefix string) *httptest.Server {
	t.Helper()
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "editor-mcp", Version: "1.0.0"}, nil)
	srv.AddTool(&mcpsdk.Tool{
		Name:        "lookup",
		Description: "Editor context lookup",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol": map[string]any{"type": "string"},
			},
			"required": []string{"symbol"},
		},
	}, func(_ context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		var args map[string]any
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &args)
		}
		sym, _ := args["symbol"].(string)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: responsePrefix + ":" + sym}},
		}, nil
	})
	handler := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return srv }, nil)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if expectedAuth != "" && r.Header.Get("Authorization") != expectedAuth {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestInitializeAdvertisesMCPCapabilities(t *testing.T) {
	s := testServer(t, func(_ context.Context, _ adapter.InMessage, chunks chan<- adapter.StreamChunk) {
		close(chunks)
	})
	var out bytes.Buffer
	in := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}` + "\n"
	if err := s.Serve(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Result struct {
			AgentCapabilities struct {
				MCPCapabilities map[string]bool `json:"mcpCapabilities"`
			} `json:"agentCapabilities"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal initialize response: %v (%s)", err, out.String())
	}
	if !resp.Result.AgentCapabilities.MCPCapabilities["http"] || !resp.Result.AgentCapabilities.MCPCapabilities["sse"] {
		t.Fatalf("expected mcpCapabilities http=true sse=true, got %+v", resp.Result.AgentCapabilities.MCPCapabilities)
	}
}

func TestSessionScopedMCPServersIsolationAndLifecycle(t *testing.T) {
	srvA := startTestHTTPMCPServer(t, "Bearer token-alpha", "alpha")
	srvB := startTestHTTPMCPServer(t, "Bearer token-beta", "beta")

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	globalReg := tool.NewRegistry()
	bridge := ai.NewToolRegistryAdapter(globalReg, nil)

	var mu sync.Mutex
	observed := make(map[string]string)

	s := testServer(t, func(ctx context.Context, msg adapter.InMessage, chunks chan<- adapter.StreamChunk) {
		defer close(chunks)
		switch msg.Text {
		case "check-http":
			res, execErr := globalReg.Execute("mcp__editor__lookup", map[string]any{"symbol": "Run"}, types.ToolContext{
				Context:   ctx,
				SessionID: types.SessionID(msg.UserID),
			})
			val := res.Content
			if execErr != nil {
				val = "ERR:" + execErr.Error()
			}
			mu.Lock()
			observed[msg.UserID] = val
			mu.Unlock()
			chunks <- adapter.StreamChunk{Delta: val}
		case "check-stdio":
			res, execErr := bridge.ExecuteResultContext(ctx, "mcp__localstdio__ping", `{"note":"ok"}`)
			val := res.Content
			if execErr != nil {
				val = "ERR:" + execErr.Error()
			}
			mu.Lock()
			observed[msg.UserID+":stdio"] = val
			mu.Unlock()
			chunks <- adapter.StreamChunk{Delta: val}
		}
	})

	dirA := t.TempDir()
	dirB := t.TempDir()
	dirC := t.TempDir()

	newA := map[string]any{
		"cwd": dirA,
		"mcpServers": []any{
			map[string]any{
				"type": "http",
				"name": "editor",
				"url":  srvA.URL,
				"headers": []map[string]string{
					{"name": "Authorization", "value": "Bearer token-alpha"},
				},
			},
			map[string]any{
				"type":    "stdio",
				"name":    "localstdio",
				"command": exe,
				"args":    []string{"-test.run=^TestACPMCPStdioHelper$"},
				"env": []map[string]string{
					{"name": "POLYPOD_ACP_MCP_STDIO_CHILD", "value": "1"},
					{"name": "ACP_STDIO_TOKEN", "value": "secret-a"},
				},
			},
		},
	}
	newB := map[string]any{
		"cwd": dirB,
		"mcpServers": []any{
			map[string]any{
				"type":    "http",
				"name":    "editor",
				"url":     srvB.URL,
				"headers": map[string]string{"Authorization": "Bearer token-beta"},
			},
		},
	}
	newC := map[string]any{
		"cwd":        dirC,
		"mcpServers": []any{},
	}

	rawA, _ := json.Marshal(newA)
	rawB, _ := json.Marshal(newB)
	rawC, _ := json.Marshal(newC)

	var transcript bytes.Buffer
	var lines []string
	lines = append(lines,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}`,
		`{"jsonrpc":"2.0","id":2,"method":"session/new","params":`+string(rawA)+`}`,
		`{"jsonrpc":"2.0","id":3,"method":"session/new","params":`+string(rawB)+`}`,
		`{"jsonrpc":"2.0","id":4,"method":"session/new","params":`+string(rawC)+`}`,
	)
	if err := s.Serve(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &transcript); err != nil {
		t.Fatal(err)
	}

	extractSessionID := func(respLine string) string {
		var r struct {
			Result struct {
				SessionID string `json:"sessionId"`
			} `json:"result"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(respLine), &r); err != nil {
			t.Fatalf("decode response %q: %v", respLine, err)
		}
		if r.Error != nil {
			t.Fatalf("unexpected RPC error: %s", r.Error.Message)
		}
		if r.Result.SessionID == "" {
			t.Fatalf("missing sessionId in %s", respLine)
		}
		return r.Result.SessionID
	}

	outLines := strings.Split(strings.TrimSpace(transcript.String()), "\n")
	if len(outLines) != 4 {
		t.Fatalf("expected 4 responses, got %d: %s", len(outLines), transcript.String())
	}
	sessA := extractSessionID(outLines[1])
	sessB := extractSessionID(outLines[2])
	sessC := extractSessionID(outLines[3])

	// Serve() closes session MCP managers when the transport closes; now test
	// session/load + session/resume re-attaching isolated MCP servers in a live connection!
	loadA, _ := json.Marshal(map[string]any{
		"sessionId":  sessA,
		"cwd":        dirA,
		"mcpServers": newA["mcpServers"],
	})
	resumeB, _ := json.Marshal(map[string]any{
		"sessionId":  sessB,
		"cwd":        dirB,
		"mcpServers": newB["mcpServers"],
	})
	loadC, _ := json.Marshal(map[string]any{
		"sessionId":  sessC,
		"cwd":        dirC,
		"mcpServers": []any{},
	})

	promptLines := []string{
		`{"jsonrpc":"2.0","id":10,"method":"initialize","params":{"protocolVersion":1}}`,
		`{"jsonrpc":"2.0","id":11,"method":"session/load","params":` + string(loadA) + `}`,
		`{"jsonrpc":"2.0","id":12,"method":"session/resume","params":` + string(resumeB) + `}`,
		`{"jsonrpc":"2.0","id":13,"method":"session/load","params":` + string(loadC) + `}`,
		`{"jsonrpc":"2.0","id":14,"method":"session/prompt","params":{"sessionId":` + jsonString(sessA) + `,"prompt":[{"type":"text","text":"check-http"}]}}`,
		`{"jsonrpc":"2.0","id":15,"method":"session/prompt","params":{"sessionId":` + jsonString(sessA) + `,"prompt":[{"type":"text","text":"check-stdio"}]}}`,
		`{"jsonrpc":"2.0","id":16,"method":"session/prompt","params":{"sessionId":` + jsonString(sessB) + `,"prompt":[{"type":"text","text":"check-http"}]}}`,
		`{"jsonrpc":"2.0","id":17,"method":"session/prompt","params":{"sessionId":` + jsonString(sessC) + `,"prompt":[{"type":"text","text":"check-http"}]}}`,
	}
	// Note: prompts 14 and 15 target the same session (sessA); send 14/16/17 first, then 15 so sessA isn't busy concurrently with itself.
	batch1 := append([]string(nil), promptLines[:5]...)
	batch1 = append(batch1, promptLines[6], promptLines[7])

	transcript.Reset()
	if err := s.Serve(context.Background(), strings.NewReader(strings.Join(batch1, "\n")+"\n"), &transcript); err != nil {
		t.Fatal(err)
	}

	// Global registry must remain completely untouched.
	if got := len(globalReg.Enabled()); got != 0 {
		t.Fatalf("expected global registry to remain empty, got %d tools", got)
	}

	mu.Lock()
	gotA := observed[sessA]
	gotB := observed[sessB]
	gotC := observed[sessC]
	mu.Unlock()

	if !strings.Contains(gotA, "alpha:Run") {
		t.Fatalf("sessA got %q, want alpha:Run", gotA)
	}
	if !strings.Contains(gotB, "beta:Run") {
		t.Fatalf("sessB got %q, want beta:Run", gotB)
	}
	if !strings.Contains(gotC, "ferramenta desconhecida: mcp__editor__lookup") {
		t.Fatalf("sessC leaked MCP tool! got %q", gotC)
	}

	// Now verify stdio MCP tool in sessA via session/resume.
	resumeA, _ := json.Marshal(map[string]any{
		"sessionId":  sessA,
		"cwd":        dirA,
		"mcpServers": newA["mcpServers"],
	})
	batch2 := []string{
		`{"jsonrpc":"2.0","id":20,"method":"initialize","params":{"protocolVersion":1}}`,
		`{"jsonrpc":"2.0","id":21,"method":"session/resume","params":` + string(resumeA) + `}`,
		promptLines[5],
	}
	transcript.Reset()
	if err := s.Serve(context.Background(), strings.NewReader(strings.Join(batch2, "\n")+"\n"), &transcript); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	gotStdio := observed[sessA+":stdio"]
	mu.Unlock()
	wantStdio := "secret-a|" + dirA + "|ok"
	if !strings.Contains(gotStdio, wantStdio) {
		t.Fatalf("sessA stdio MCP tool got %q, want substring %q", gotStdio, wantStdio)
	}
}
