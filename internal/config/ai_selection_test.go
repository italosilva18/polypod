package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveAISelectionClearsPreviousAPIKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.yaml")
	before := "ai:\n  provider: chatgpt\n  model: gpt-6.1-sol\n  api_key: ${NVIDIA_API_KEY}\nother: keep\n"
	if err := os.WriteFile(path, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	if err := SaveAISelection(path, "nvidia", "nvidia/test-model", "https://example.test/v1"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"provider: nvidia", "model: nvidia/test-model", "api_key: \"\"", "other: keep"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("configuração não contém %q", want)
		}
	}
	if strings.Contains(string(data), "${NVIDIA_API_KEY}") {
		t.Fatal("chave antiga permaneceu no perfil")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("permissões = %o, esperado 600", info.Mode().Perm())
	}
}
