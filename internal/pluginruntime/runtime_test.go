package pluginruntime

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/costa/polypod/internal/hooks"
	"github.com/costa/polypod/internal/mcp"
	"github.com/costa/polypod/internal/skill"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSyncInstallsAndRemovesPluginHook(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "demo")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"plugin.json": `{"schema_version":1,"name":"demo","version":"1.0","hooks":["hooks.yaml"]}`,
		"hooks.yaml":  "- name: notify\n  event: PostToolUse\n  type: shell\n  command: echo ok\n",
	} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	plugins := skill.NewPluginManager([]string{filepath.Join(base, "installed")}, skill.NewRegistry(), nil)
	installed, err := plugins.Install(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	hookManager := hooks.NewManager(slog.Default())
	runtime := New(plugins, hookManager, nil)
	if err := runtime.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := hookManager.List(); len(got) != 1 || got[0].Name != "plugin:"+installed.ID+":notify" {
		t.Fatalf("hook not activated: %+v", got)
	}
	if _, err := plugins.SetEnabled(context.Background(), installed.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := hookManager.List(); len(got) != 0 {
		t.Fatalf("disabled plugin hook remains: %+v", got)
	}
}

func TestSyncConnectsAndDisconnectsPluginMCP(t *testing.T) {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "plugin-test", Version: "1"}, nil)
	server.AddTool(&mcpsdk.Tool{Name: "status", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "ok"}}}, nil
		})
	endpoint := httptest.NewServer(mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return server }, nil))
	defer endpoint.Close()
	base := t.TempDir()
	source := filepath.Join(base, "demo")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"plugin.json": `{"schema_version":1,"name":"demo","version":"1.0","mcp_servers":["mcp.json"]}`,
		"mcp.json":    `{"mcpServers":{"status":{"type":"http","url":"` + endpoint.URL + `"}}}`,
	} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	registry := skill.NewRegistry()
	plugins := skill.NewPluginManager([]string{filepath.Join(base, "installed")}, registry, nil)
	installed, err := plugins.Install(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	manager := mcp.NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer manager.Close()
	manager.RegisterTools(registry)
	runtime := New(plugins, nil, manager)
	if err := runtime.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	name := "plugin:" + installed.ID + ":status"
	if _, ok := manager.ListServers()[name]; !ok {
		t.Fatalf("MCP server was not connected: %v", manager.ListServers())
	}
	result, isError, err := manager.CallTool(context.Background(), name, "status", map[string]any{})
	if err != nil || isError {
		t.Fatalf("plugin MCP call failed: %v, %v, %v", result, isError, err)
	}
	typed, ok := result.(*mcpsdk.CallToolResult)
	if !ok || len(typed.Content) != 1 || typed.Content[0].(*mcpsdk.TextContent).Text != "ok" {
		t.Fatalf("unexpected plugin MCP result: %#v", result)
	}
	if _, err := plugins.SetEnabled(context.Background(), installed.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.ListServers()[name]; ok {
		t.Fatal("disabled plugin MCP server remains connected")
	}
}
