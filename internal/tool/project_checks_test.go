package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/types"
)

func TestRunProjectChecksReportsExternalFailures(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, ".polypod")
	if err := os.Mkdir(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := "verifiers:\n  pass: 'printf success'\n  fail: 'exit 1'\n"
	if err := os.WriteFile(filepath.Join(configDir, "verifiers.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	RegisterProjectChecksTool(registry, root)
	result, err := registry.Execute("run_project_checks", map[string]any{}, types.ToolContext{Context: context.Background()})
	if err != nil || !result.IsError || !strings.Contains(result.Content, "pass: PASSOU") ||
		!strings.Contains(result.Content, "fail: FALHOU") {
		t.Fatalf("check results lost: %+v, %v", result, err)
	}
}
