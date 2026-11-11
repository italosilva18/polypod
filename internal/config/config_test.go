package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig is a small helper that writes content to a temp config file.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoad_MinimalYAML(t *testing.T) {
	content := `
ai:
  provider: deepseek
  model: deepseek-chat
  max_tokens: 1024
  temperature: 0.5
data:
  dir: /tmp/polypod
`
	path := writeConfig(t, content)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg == nil {
		t.Fatal("cfg nil")
	}
	if cfg.AI.Provider != "deepseek" {
		t.Errorf("AI.Provider = %q; want deepseek", cfg.AI.Provider)
	}
	if cfg.AI.Model != "deepseek-chat" {
		t.Errorf("AI.Model = %q; want deepseek-chat", cfg.AI.Model)
	}
	if cfg.AI.MaxToks != 1024 {
		t.Errorf("AI.MaxToks = %d; want 1024", cfg.AI.MaxToks)
	}
	if cfg.AI.Temp != 0.5 {
		t.Errorf("AI.Temp = %v; want 0.5", cfg.AI.Temp)
	}
	if cfg.Data.Dir != "/tmp/polypod" {
		t.Errorf("Data.Dir = %q; want /tmp/polypod", cfg.Data.Dir)
	}
}

func TestLoad_MinimalNVIDIAConfigUsesHostedDefaults(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "synthetic-nvidia-key")
	path := writeConfig(t, "ai:\n  provider: nvidia\n  api_key: ${NVIDIA_API_KEY:}\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AI.Provider != "nvidia" || cfg.AI.BaseURL != "https://integrate.api.nvidia.com/v1" ||
		cfg.AI.Model != "nvidia/nemotron-3-super-120b-a12b" || cfg.AI.APIKey != "synthetic-nvidia-key" {
		t.Fatalf("NVIDIA defaults missing: provider=%q base_url=%q model=%q key_present=%t",
			cfg.AI.Provider, cfg.AI.BaseURL, cfg.AI.Model, cfg.AI.APIKey != "")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("Load should error on missing file")
	}
}

func TestLoad_EmptyFileUsesDefaults(t *testing.T) {
	path := writeConfig(t, "")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg == nil {
		t.Fatal("cfg nil")
	}
	// All defaults from setDefaults should be applied.
	if cfg.Database.Driver != "sqlite" {
		t.Errorf("Database.Driver = %q; want sqlite", cfg.Database.Driver)
	}
	if cfg.Database.Path != "data/polypod.db" {
		t.Errorf("Database.Path = %q; want data/polypod.db", cfg.Database.Path)
	}
	if cfg.AI.Model != "deepseek-chat" {
		t.Errorf("AI.Model = %q; want deepseek-chat", cfg.AI.Model)
	}
	if cfg.AI.MaxToks != 2048 {
		t.Errorf("AI.MaxToks = %d; want 2048", cfg.AI.MaxToks)
	}
	if cfg.AI.Temp != 0.3 {
		t.Errorf("AI.Temp = %v; want 0.3", cfg.AI.Temp)
	}
	if cfg.Data.Dir != "data/conversations" {
		t.Errorf("Data.Dir = %q; want data/conversations", cfg.Data.Dir)
	}
	if cfg.Data.AgentsDir != "agents" {
		t.Errorf("Data.AgentsDir = %q; want agents", cfg.Data.AgentsDir)
	}
	if cfg.Log.Level != "info" {
		t.Errorf("Log.Level = %q; want info", cfg.Log.Level)
	}
	if cfg.Log.Format != "text" {
		t.Errorf("Log.Format = %q; want text", cfg.Log.Format)
	}
	if cfg.Sandbox.Image != "ubuntu:22.04" {
		t.Errorf("Sandbox.Image = %q; want ubuntu:22.04", cfg.Sandbox.Image)
	}
	if cfg.Sandbox.MemoryLimit != "256m" {
		t.Errorf("Sandbox.MemoryLimit = %q; want 256m", cfg.Sandbox.MemoryLimit)
	}
	if cfg.Sandbox.CPULimit != "1" {
		t.Errorf("Sandbox.CPULimit = %q; want 1", cfg.Sandbox.CPULimit)
	}
	if cfg.Sandbox.Timeout != 30 {
		t.Errorf("Sandbox.Timeout = %d; want 30", cfg.Sandbox.Timeout)
	}
	if cfg.Scheduler.DataFile != "data/scheduler.json" {
		t.Errorf("Scheduler.DataFile = %q; want data/scheduler.json", cfg.Scheduler.DataFile)
	}
	if cfg.Templates.Dir != "templates" {
		t.Errorf("Templates.Dir = %q; want templates", cfg.Templates.Dir)
	}
	if cfg.Permissions == nil || cfg.Permissions.PermissionMode() != "bypassPermissions" {
		t.Errorf("Permissions mode = %v; want bypassPermissions", cfg.Permissions)
	}
}

func TestLoad_ExplicitPermissionMode(t *testing.T) {
	path := writeConfig(t, "permissions:\n  mode: plan\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Permissions.PermissionMode(); got != "plan" {
		t.Errorf("Permissions mode = %q; want plan", got)
	}
}

func TestLoad_InvalidPermissionMode(t *testing.T) {
	path := writeConfig(t, "permissions:\n  mode: unlimited-ish\n")

	if _, err := Load(path); err == nil {
		t.Fatal("Load should reject an unknown permission mode")
	}
}

func TestLoad_MalformedYAML(t *testing.T) {
	content := `
ai:
  provider: deepseek
  model: [unclosed
  max_tokens: "not a number
`
	path := writeConfig(t, content)

	_, err := Load(path)
	if err == nil {
		t.Error("Load should error on malformed YAML")
	}
}

func TestLoad_MalformedYAML_BadIndent(t *testing.T) {
	// Tab-indented YAML mapping is invalid.
	content := "ai:\n  provider: deepseek\n\tmodel: bad-tab\n"
	path := writeConfig(t, content)

	_, err := Load(path)
	if err == nil {
		t.Error("Load should error on YAML with tab indentation")
	}
}

func TestSetDefaults_OverridesOnlyUnset(t *testing.T) {
	cfg := &Config{}
	cfg.AI.Model = "custom-model"
	cfg.AI.MaxToks = 999
	cfg.Log.Level = "debug"

	setDefaults(cfg)

	// Preserved fields
	if cfg.AI.Model != "custom-model" {
		t.Errorf("AI.Model = %q; want custom-model", cfg.AI.Model)
	}
	if cfg.AI.MaxToks != 999 {
		t.Errorf("AI.MaxToks = %d; want 999", cfg.AI.MaxToks)
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("Log.Level = %q; want debug", cfg.Log.Level)
	}
	// Defaults applied where empty
	if cfg.Database.Driver != "sqlite" {
		t.Errorf("Database.Driver default not applied: %q", cfg.Database.Driver)
	}
	if cfg.Log.Format != "text" {
		t.Errorf("Log.Format default not applied: %q", cfg.Log.Format)
	}
}

func TestSetDefaults_Idempotent(t *testing.T) {
	cfg1 := &Config{}
	setDefaults(cfg1)

	cfg2 := &Config{}
	setDefaults(cfg2)
	setDefaults(cfg2) // called twice

	if cfg1.Database != cfg2.Database {
		t.Errorf("Database differs after second setDefaults: %+v vs %+v", cfg1.Database, cfg2.Database)
	}
	if cfg1.AI != cfg2.AI {
		t.Errorf("AI differs after second setDefaults: %+v vs %+v", cfg1.AI, cfg2.AI)
	}
	if cfg1.Data != cfg2.Data {
		t.Errorf("Data differs after second setDefaults: %+v vs %+v", cfg1.Data, cfg2.Data)
	}
	if cfg1.Log != cfg2.Log {
		t.Errorf("Log differs after second setDefaults: %+v vs %+v", cfg1.Log, cfg2.Log)
	}
	if cfg1.Sandbox != cfg2.Sandbox {
		t.Errorf("Sandbox differs after second setDefaults: %+v vs %+v", cfg1.Sandbox, cfg2.Sandbox)
	}
	if cfg1.Scheduler != cfg2.Scheduler {
		t.Errorf("Scheduler differs after second setDefaults: %+v vs %+v", cfg1.Scheduler, cfg2.Scheduler)
	}
	if cfg1.Templates != cfg2.Templates {
		t.Errorf("Templates differs after second setDefaults: %+v vs %+v", cfg1.Templates, cfg2.Templates)
	}
}

func TestSetDefaults_HooksTimeout(t *testing.T) {
	cfg := &Config{
		Hooks: []HookConfig{
			{Name: "a", Event: "pre", Timeout: 0},    // should become 10
			{Name: "b", Event: "post", Timeout: 123}, // should stay 123
		},
	}
	setDefaults(cfg)

	if cfg.Hooks[0].Timeout != 10 {
		t.Errorf("Hooks[0].Timeout = %d; want 10", cfg.Hooks[0].Timeout)
	}
	if cfg.Hooks[1].Timeout != 123 {
		t.Errorf("Hooks[1].Timeout = %d; want 123", cfg.Hooks[1].Timeout)
	}
}

func TestLoad_EnvSubstitution_Set(t *testing.T) {
	t.Setenv("POLYPOD_API_KEY", "secret123")

	content := `
ai:
  provider: deepseek
  api_key: ${POLYPOD_API_KEY}
`
	path := writeConfig(t, content)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AI.APIKey != "secret123" {
		t.Errorf("AI.APIKey = %q; want secret123", cfg.AI.APIKey)
	}
}

func TestLoad_EnvSubstitution_DefaultWhenUnset(t *testing.T) {
	// Ensure the variable is not set.
	if err := os.Unsetenv("POLYPOD_UNSET_VAR"); err != nil {
		t.Fatal(err)
	}

	content := `
ai:
  provider: deepseek
  base_url: ${POLYPOD_UNSET_VAR:https://fallback.example.com}
`
	path := writeConfig(t, content)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AI.BaseURL != "https://fallback.example.com" {
		t.Errorf("AI.BaseURL = %q; want https://fallback.example.com", cfg.AI.BaseURL)
	}
}

func TestLoad_EnvSubstitution_SetOverridesDefault(t *testing.T) {
	t.Setenv("POLYPOD_BASE_URL", "https://real.example.com")

	content := `
ai:
  provider: deepseek
  base_url: ${POLYPOD_BASE_URL:https://fallback.example.com}
`
	path := writeConfig(t, content)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AI.BaseURL != "https://real.example.com" {
		t.Errorf("AI.BaseURL = %q; want https://real.example.com", cfg.AI.BaseURL)
	}
}

func TestLoad_EnvSubstitution_MultipleVars(t *testing.T) {
	t.Setenv("POLYPOD_PROVIDER", "openai")
	t.Setenv("POLYPOD_MODEL", "gpt-4")
	t.Setenv("POLYPOD_KEY", "k-abc")

	content := `
ai:
  provider: ${POLYPOD_PROVIDER}
  model: ${POLYPOD_MODEL}
  api_key: ${POLYPOD_KEY}
  base_url: ${POLYPOD_URL:https://api.openai.com/v1}
`
	path := writeConfig(t, content)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AI.Provider != "openai" {
		t.Errorf("AI.Provider = %q; want openai", cfg.AI.Provider)
	}
	if cfg.AI.Model != "gpt-4" {
		t.Errorf("AI.Model = %q; want gpt-4", cfg.AI.Model)
	}
	if cfg.AI.APIKey != "k-abc" {
		t.Errorf("AI.APIKey = %q; want k-abc", cfg.AI.APIKey)
	}
	if cfg.AI.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("AI.BaseURL = %q; want https://api.openai.com/v1", cfg.AI.BaseURL)
	}
}

func TestLoad_EnvSubstitution_UnsetNoDefault(t *testing.T) {
	if err := os.Unsetenv("POLYPOD_ABSENT"); err != nil {
		t.Fatal(err)
	}

	content := `
ai:
  provider: deepseek
  api_key: "${POLYPOD_ABSENT}"
`
	path := writeConfig(t, content)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// When no default, unset var expands to empty string.
	if cfg.AI.APIKey != "" {
		t.Errorf("AI.APIKey = %q; want empty", cfg.AI.APIKey)
	}
}

func TestLoad_ComplexConfig(t *testing.T) {
	t.Setenv("DB_PATH", "/var/lib/polypod.db")

	content := `
database:
  enabled: true
  driver: sqlite
  path: ${DB_PATH}
ai:
  provider: deepseek
  model: deepseek-chat
  tools: true
providers:
  - name: openai
    base_url: https://api.openai.com/v1
    api_key: sk-test
    default: true
  - name: ollama
    base_url: http://localhost:11434
mcp:
  - name: fs
    transport: stdio
    command: mcp-fs
    args: ["--root", "/tmp"]
    auto_start: true
sandbox:
  enabled: true
  image: alpine:latest
  memory_limit: 512m
  network: false
hooks:
  - name: pre-run
    event: before
    type: shell
    command: echo hi
    enabled: true
`
	path := writeConfig(t, content)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Database.Enabled {
		t.Error("Database.Enabled should be true")
	}
	if cfg.Database.Path != "/var/lib/polypod.db" {
		t.Errorf("Database.Path = %q; want /var/lib/polypod.db", cfg.Database.Path)
	}
	if !cfg.AI.Tools {
		t.Error("AI.Tools should be true")
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("Providers len = %d; want 2", len(cfg.Providers))
	}
	if cfg.Providers[0].Name != "openai" || !cfg.Providers[0].Default {
		t.Errorf("Providers[0] = %+v; want openai default", cfg.Providers[0])
	}
	if len(cfg.MCP) != 1 || cfg.MCP[0].Name != "fs" {
		t.Errorf("MCP = %+v", cfg.MCP)
	}
	if len(cfg.MCP[0].Args) != 2 || cfg.MCP[0].Args[0] != "--root" {
		t.Errorf("MCP[0].Args = %+v", cfg.MCP[0].Args)
	}
	// Defaults still applied where empty
	if cfg.Sandbox.Image != "alpine:latest" {
		t.Errorf("Sandbox.Image = %q; want alpine:latest", cfg.Sandbox.Image)
	}
	if cfg.Sandbox.MemoryLimit != "512m" {
		t.Errorf("Sandbox.MemoryLimit = %q; want 512m", cfg.Sandbox.MemoryLimit)
	}
	// CPULimit/Timeout unset -> defaults
	if cfg.Sandbox.CPULimit != "1" {
		t.Errorf("Sandbox.CPULimit = %q; want 1", cfg.Sandbox.CPULimit)
	}
	if cfg.Sandbox.Timeout != 30 {
		t.Errorf("Sandbox.Timeout = %d; want 30", cfg.Sandbox.Timeout)
	}
	// Hook without timeout -> default 10
	if len(cfg.Hooks) != 1 || cfg.Hooks[0].Timeout != 10 {
		t.Errorf("Hooks = %+v", cfg.Hooks)
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg == nil {
		t.Fatal("DefaultConfig returned nil")
	}
	if !cfg.CLI.Enabled {
		t.Error("CLI.Enabled should be true")
	}
	if !cfg.AI.Tools {
		t.Error("AI.Tools should be true")
	}
	if cfg.AI.Provider != "deepseek" {
		t.Errorf("AI.Provider = %q; want deepseek", cfg.AI.Provider)
	}
	if cfg.AI.BaseURL != "https://api.deepseek.com/v1" {
		t.Errorf("AI.BaseURL = %q; want https://api.deepseek.com/v1", cfg.AI.BaseURL)
	}
	// Defaults applied
	if cfg.AI.Model != "deepseek-chat" {
		t.Errorf("AI.Model = %q; want deepseek-chat", cfg.AI.Model)
	}
	if cfg.Database.Driver != "sqlite" {
		t.Errorf("Database.Driver = %q; want sqlite", cfg.Database.Driver)
	}
	if cfg.Permissions == nil || cfg.Permissions.PermissionMode() != "bypassPermissions" {
		t.Errorf("Permissions mode = %v; want bypassPermissions", cfg.Permissions)
	}
}

func TestConfig_MarshalRoundTrip(t *testing.T) {
	original := DefaultConfig()
	original.AI.APIKey = "key-1"
	original.AI.Model = "deepseek-coder"

	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("Marshal returned empty data")
	}

	// Write and reload through Load to exercise the full pipeline.
	path := writeConfig(t, string(data))
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.AI.APIKey != "key-1" {
		t.Errorf("loaded AI.APIKey = %q; want key-1", loaded.AI.APIKey)
	}
	if loaded.AI.Model != "deepseek-coder" {
		t.Errorf("loaded AI.Model = %q; want deepseek-coder", loaded.AI.Model)
	}
	if loaded.AI.Provider != "deepseek" {
		t.Errorf("loaded AI.Provider = %q; want deepseek", loaded.AI.Provider)
	}
}

func TestMustParseInt(t *testing.T) {
	if got := MustParseInt("42"); got != 42 {
		t.Errorf("MustParseInt(42) = %d; want 42", got)
	}
	if got := MustParseInt("0"); got != 0 {
		t.Errorf("MustParseInt(0) = %d; want 0", got)
	}
	if got := MustParseInt("-7"); got != -7 {
		t.Errorf("MustParseInt(-7) = %d; want -7", got)
	}
	// Non-numeric should not panic, should return 0.
	if got := MustParseInt("not-a-number"); got != 0 {
		t.Errorf("MustParseInt(not-a-number) = %d; want 0", got)
	}
}
