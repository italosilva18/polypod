package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMinimalConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	os.WriteFile(configPath, []byte(`
ai:
  provider: "test"
  api_key: "sk-test"
  model: "test-model"
cli:
  enabled: true
`), 0644)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if cfg.AI.Provider != "test" {
		t.Errorf("provider = %q", cfg.AI.Provider)
	}
	if cfg.AI.APIKey != "sk-test" {
		t.Errorf("api_key = %q", cfg.AI.APIKey)
	}
	if !cfg.CLI.Enabled {
		t.Error("CLI should be enabled")
	}
}

func TestDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Server.Port != 8080 {
		t.Errorf("default port = %d, want 8080", cfg.Server.Port)
	}
	if cfg.AI.Model != "deepseek-chat" {
		t.Errorf("default model = %q", cfg.AI.Model)
	}
	if cfg.AI.MaxToks != 2048 {
		t.Errorf("default max_tokens = %d", cfg.AI.MaxToks)
	}
	if cfg.Rate.RequestsPerMinute != 20 {
		t.Errorf("default rpm = %d", cfg.Rate.RequestsPerMinute)
	}
	if cfg.Data.Dir != "data/conversations" {
		t.Errorf("default data dir = %q", cfg.Data.Dir)
	}
}

func TestEnvVarSubstitution(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	os.WriteFile(configPath, []byte(`
ai:
  api_key: "${TEST_POLYPOD_KEY}"
  model: "${TEST_MODEL:fallback-model}"
`), 0644)

	os.Setenv("TEST_POLYPOD_KEY", "my-secret-key")
	defer os.Unsetenv("TEST_POLYPOD_KEY")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if cfg.AI.APIKey != "my-secret-key" {
		t.Errorf("env var not substituted: %q", cfg.AI.APIKey)
	}
	if cfg.AI.Model != "fallback-model" {
		t.Errorf("default not applied: %q", cfg.AI.Model)
	}
}

func TestMarshal(t *testing.T) {
	cfg := DefaultConfig()
	data, err := cfg.Marshal()
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("marshaled config is empty")
	}
}
