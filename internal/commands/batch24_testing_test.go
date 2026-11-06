// Package commands — batch24_testing_test.go
//
// Smoke tests for the batch24 testing-flavored slash commands
// (/testgen, /coverage, /lint).
package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
)

// newBatch24Ctx returns a CommandContext rooted in an isolated temp dir.
// Callers that need a "Go project" must drop a go.mod file themselves.
func newBatch24Ctx(t *testing.T) command.CommandContext {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("POLYPOD_CONFIG_DIR", filepath.Join(tmp, "polypod-home"))
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	return command.CommandContext{
		WorkingDir: tmp,
		SessionID:  "batch24-test-session",
	}
}

// writeFakeGoMod drops a minimal go.mod into `dir` so isGoProjectDir reports
// true. The module path is arbitrary — we never actually compile anything.
func writeFakeGoMod(t *testing.T, dir string) {
	t.Helper()
	content := "module example.com/batch24fake\n\ngo 1.21\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0o644); err != nil {
		t.Fatalf("falha ao escrever go.mod falso: %v", err)
	}
}

// goAvailable reports whether `go` exists on PATH.
func goAvailable() bool {
	_, err := exec.LookPath("go")
	return err == nil
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func TestRegisterBatch24Testing_RegistersAll(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch24Testing(reg)

	for _, name := range []string{"testgen", "coverage", "lint"} {
		cmd, ok := reg.Get(name)
		if !ok {
			t.Errorf("comando %q não registrado", name)
			continue
		}
		if cmd.Type != command.CommandLocal {
			t.Errorf("%q: esperava tipo local, obteve %q", name, cmd.Type)
		}
		if cmd.Execute == nil {
			t.Errorf("%q: Execute é nil", name)
		}
		if strings.TrimSpace(cmd.Description) == "" {
			t.Errorf("%q: descrição vazia", name)
		}
	}
}

// ---------------------------------------------------------------------------
// /testgen placeholder
// ---------------------------------------------------------------------------

func TestCmdTestgenB24_Placeholder(t *testing.T) {
	ctx := newBatch24Ctx(t)
	res, err := cmdTestgenB24().Execute([]string{"foo.go"}, ctx)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !strings.Contains(res.Display, "test generation pendente wiring") {
		t.Errorf("esperava mensagem de placeholder do /testgen, obteve %q", res.Display)
	}
	if !strings.Contains(res.Display, "PromptCommand") {
		t.Errorf("esperava menção a PromptCommand em %q", res.Display)
	}
}

// ---------------------------------------------------------------------------
// isGoProjectDir helper
// ---------------------------------------------------------------------------

func TestIsGoProjectDirB24(t *testing.T) {
	tmp := t.TempDir()

	if isGoProjectDir(tmp) {
		t.Error("diretório vazio não deveria ser considerado projeto Go")
	}
	if isGoProjectDir("") {
		t.Error("string vazia não deveria ser considerada projeto Go")
	}

	// Create a directory named go.mod — should still be false (not a file).
	dirAsMod := filepath.Join(tmp, "dir-variant")
	if err := os.MkdirAll(filepath.Join(dirAsMod, "go.mod"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if isGoProjectDir(dirAsMod) {
		t.Error("go.mod sendo diretório não deveria contar como projeto Go")
	}

	// Now drop a real go.mod and expect true.
	modDir := filepath.Join(tmp, "real-mod")
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFakeGoMod(t, modDir)
	if !isGoProjectDir(modDir) {
		t.Error("diretório com go.mod deveria ser considerado projeto Go")
	}
}

// ---------------------------------------------------------------------------
// /coverage
// ---------------------------------------------------------------------------

func TestCmdCoverageB24_NoGoMod(t *testing.T) {
	ctx := newBatch24Ctx(t)
	res, err := cmdCoverageB24().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !strings.Contains(res.Display, "não é um projeto Go") {
		t.Errorf("esperava placeholder de cobertura, obteve %q", res.Display)
	}
}

func TestCmdCoverageB24_WithGoMod(t *testing.T) {
	if !goAvailable() {
		t.Skip("'go' não disponível no PATH; pulando teste positivo de /coverage")
	}
	ctx := newBatch24Ctx(t)
	writeFakeGoMod(t, ctx.WorkingDir)

	// The fake module has no .go files so `go test -cover ./...` should
	// succeed with a "no Go files" style notice OR return a non-zero exit
	// that our command captures. Either way the display must not be the
	// "not a Go project" placeholder.
	res, err := cmdCoverageB24().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if strings.Contains(res.Display, "não é um projeto Go") {
		t.Errorf("/coverage reportou placeholder para um diretório com go.mod: %q", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /lint
// ---------------------------------------------------------------------------

func TestCmdLintB24_NoGoMod(t *testing.T) {
	ctx := newBatch24Ctx(t)
	res, err := cmdLintB24().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !strings.Contains(res.Display, "não é um projeto Go") {
		t.Errorf("esperava placeholder de lint, obteve %q", res.Display)
	}
}

func TestCmdLintB24_WithGoMod(t *testing.T) {
	if !goAvailable() {
		t.Skip("'go' não disponível no PATH; pulando teste positivo de /lint")
	}
	ctx := newBatch24Ctx(t)
	writeFakeGoMod(t, ctx.WorkingDir)

	res, err := cmdLintB24().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if strings.Contains(res.Display, "não é um projeto Go") {
		t.Errorf("/lint reportou placeholder para um diretório com go.mod: %q", res.Display)
	}
}
