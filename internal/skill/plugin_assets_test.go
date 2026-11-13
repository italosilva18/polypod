package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePluginAssetFixture(t *testing.T, source string, valid bool) {
	t.Helper()
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"schema_version":1,"name":"demo","version":"1.0.0","skills":[],"hooks":["hooks.yaml"],"mcp_servers":["mcp.json"]}`
	hook := "- name: notify\n  event: PostToolUse\n  type: shell\n  command: echo ok\n  enabled: true\n"
	if !valid {
		hook = "- name: notify\n  event: UnknownEvent\n  type: shell\n  command: echo ok\n"
	}
	for name, content := range map[string]string{
		"plugin.json": manifest,
		"hooks.yaml":  hook,
		"mcp.json":    `{"mcpServers":{"echo":{"type":"stdio","command":"echo"}}}`,
	} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPluginPackageStagesHooksMCPAndRollsBackInvalidUpdate(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "installed")
	source := filepath.Join(base, "demo")
	writePluginAssetFixture(t, source, true)
	manager := NewPluginManager([]string{root}, NewRegistry(), nil)
	installed, err := manager.Install(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if len(installed.HookFiles) != 1 || len(installed.MCPFiles) != 1 {
		t.Fatalf("package assets missing: %+v", installed)
	}
	assets, err := ReadPluginAssets(installed)
	if err != nil || len(assets.Hooks) != 1 || len(assets.MCPServers) != 1 {
		t.Fatalf("read assets: %+v, %v", assets, err)
	}
	writePluginAssetFixture(t, source, false)
	if _, err := manager.Update(context.Background(), installed.ID, source); err == nil {
		t.Fatal("invalid hook update was published")
	}
	current, err := manager.List(context.Background())
	if err != nil || len(current) != 1 {
		t.Fatalf("installed package missing after rejected update: %+v, %v", current, err)
	}
	assets, err = ReadPluginAssets(current[0])
	if err != nil || len(assets.Hooks) != 1 || assets.Hooks[0].Event != "PostToolUse" {
		t.Fatalf("rollback lost original hook: %+v, %v", assets, err)
	}
}

func TestPluginPackageRejectsIncompatibleManifest(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "future")
	writePluginAssetFixture(t, source, true)
	manifest := filepath.Join(source, "plugin.json")
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(strings.Replace(string(data), `"schema_version":1`, `"schema_version":2`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewPluginManager([]string{filepath.Join(base, "installed")}, NewRegistry(), nil)
	if _, err := manager.Install(context.Background(), source); err == nil {
		t.Fatal("future schema was accepted")
	}
}
