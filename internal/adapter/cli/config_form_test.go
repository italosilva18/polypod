package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/config"
)

func TestConfigFormOnlyOffersPersistedFieldsAndPreservesFileMode(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.AI.Provider = "nvidia"
	path := filepath.Join(t.TempDir(), "config.yaml")
	data, err := cfg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	form := newConfigForm(cfg, path)
	for _, field := range form.fields {
		if strings.Contains("|ai.auto_compact|ai.compact_threshold|agents.default|ai.streaming|ai.cost_limit|ai.api_key|", "|"+field.Path+"|") {
			t.Fatalf("campo sem salvamento seguro exibido: %s", field.Path)
		}
	}
	if err := form.Save(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("permissões alteradas: %v", info.Mode().Perm())
	}
}

func TestConfigFormDoesNotCarryCredentialsToAnotherProvider(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.AI.Provider = "nvidia"
	cfg.AI.Model = "nvidia/old"
	cfg.AI.BaseURL = "https://integrate.api.nvidia.com/v1"
	cfg.AI.APIKey = "old-secret"
	path := filepath.Join(t.TempDir(), "config.yaml")
	data, err := cfg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	form := newConfigForm(cfg, path)
	for i := range form.fields {
		switch form.fields[i].Path {
		case "ai.provider":
			form.fields[i].Value = "openai"
		case "ai.model":
			form.fields[i].Value = "gpt-6.1-sol"
		}
	}
	if err := form.Save(); err != nil {
		t.Fatal(err)
	}
	if cfg.AI.APIKey != "" || cfg.AI.BaseURL != "" || cfg.AI.Provider != "openai" {
		t.Fatalf("credencial ou URL carregada para outro provedor: %+v", cfg.AI)
	}
}

func TestConfigFormPreservesUneditedEnvironmentReferences(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "synthetic-secret")
	t.Setenv("POLYPOD_DATA_DIR_TEST", "/tmp/polypod-data-test")
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := "# comentário preservado\nai:\n  provider: nvidia\n  model: nvidia/test\n  api_key: ${NVIDIA_API_KEY}\n  max_tokens: 2048\n  temperature: 0.3\ndata:\n  dir: ${POLYPOD_DATA_DIR_TEST}\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	form := newConfigForm(cfg, path)
	for i := range form.fields {
		if form.fields[i].Path == "ai.max_tokens" {
			form.fields[i].Value = "4096"
		}
	}
	if err := form.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "${NVIDIA_API_KEY}") || !strings.Contains(text, "${POLYPOD_DATA_DIR_TEST}") || !strings.Contains(text, "# comentário preservado") || strings.Contains(text, "synthetic-secret") {
		t.Fatalf("referências ou comentário alterados: %s", text)
	}
	if cfg.AI.MaxToks != 4096 {
		t.Fatalf("configuração em memória não atualizada: %d", cfg.AI.MaxToks)
	}
}
