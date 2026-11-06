package architect

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestFormatArchitectPrompt(t *testing.T) {
	task := "Refatorar o sistema de cache para usar LRU"
	got := FormatArchitectPrompt(task)

	if !strings.Contains(got, "ARQUITETO") {
		t.Errorf("expected prompt to identify role as ARQUITETO, got: %s", got)
	}
	if !strings.Contains(got, task) {
		t.Errorf("expected prompt to contain task %q, got: %s", task, got)
	}
	if !strings.Contains(got, "PLANEJAR") {
		t.Errorf("expected prompt to mention PLANEJAR, got: %s", got)
	}
	if !strings.Contains(got, "NAO escreva codigo completo") {
		t.Errorf("expected prompt to instruct against writing full code, got: %s", got)
	}
}

func TestFormatArchitectPromptEmptyTask(t *testing.T) {
	got := FormatArchitectPrompt("")
	if !strings.Contains(got, "TAREFA:") {
		t.Errorf("expected prompt to contain TAREFA label even with empty task, got: %s", got)
	}
	if !strings.Contains(got, "ARQUITETO") {
		t.Errorf("expected ARQUITETO label, got: %s", got)
	}
}

func TestFormatEditorPrompt(t *testing.T) {
	plan := "1. Modificar foo.go linha 42\n2. Adicionar nova funcao bar()"
	got := FormatEditorPrompt(plan)

	if !strings.Contains(got, "EDITOR") {
		t.Errorf("expected prompt to identify role as EDITOR, got: %s", got)
	}
	if !strings.Contains(got, plan) {
		t.Errorf("expected prompt to contain plan, got: %s", got)
	}
	if !strings.Contains(got, "edit_file") {
		t.Errorf("expected prompt to mention edit_file tool, got: %s", got)
	}
	if !strings.Contains(got, "create_file") {
		t.Errorf("expected prompt to mention create_file tool, got: %s", got)
	}
}

func TestFormatEditorPromptDistinctFromArchitect(t *testing.T) {
	// Ensure architect and editor prompts are semantically distinct.
	a := FormatArchitectPrompt("tarefa X")
	e := FormatEditorPrompt("plano Y")

	if a == e {
		t.Error("architect and editor prompts should not be identical")
	}
	if strings.Contains(e, "NAO escreva codigo completo") {
		t.Error("editor prompt should not contain architect-specific instruction")
	}
	if strings.Contains(a, "PLANO DO ARQUITETO:") {
		t.Error("architect prompt should not contain editor-specific header")
	}
}

func TestFormatLintFeedback(t *testing.T) {
	result := &LintResult{
		Passed:  false,
		Output:  "foo.go:10:1: error: undefined: bar",
		Errors:  1,
		Command: "go vet ./...",
	}
	got := FormatLintFeedback(result, 2)

	if !strings.Contains(got, "Tentativa 2") {
		t.Errorf("expected feedback to contain attempt number, got: %s", got)
	}
	if !strings.Contains(got, result.Command) {
		t.Errorf("expected feedback to contain command %q, got: %s", result.Command, got)
	}
	if !strings.Contains(got, result.Output) {
		t.Errorf("expected feedback to contain lint output, got: %s", got)
	}
	if !strings.Contains(got, "LINT-FIX") {
		t.Errorf("expected feedback to mention LINT-FIX loop, got: %s", got)
	}
}

func TestRunLintEmptyCommandNoProject(t *testing.T) {
	// Use a working dir with no known project files by running in an isolated temp dir.
	// Since detectLintCommand checks files via exec "test -f", we can't easily isolate
	// without chdir. Instead, we directly pass an empty command and rely on detection.
	// The function returns Passed=true when no linter is configured or detected.
	// To guarantee no detection, we use a command that explicitly returns success.
	ctx := context.Background()
	res, err := RunLint(ctx, "true")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if !res.Passed {
		t.Errorf("expected Passed=true for 'true' command, got output: %s", res.Output)
	}
	if res.Command != "true" {
		t.Errorf("expected Command=true, got %q", res.Command)
	}
}

func TestRunLintFailingCommand(t *testing.T) {
	ctx := context.Background()
	res, err := RunLint(ctx, "echo 'erro 1'; echo 'erro 2'; exit 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if res.Passed {
		t.Error("expected Passed=false for failing command")
	}
	if !strings.Contains(res.Output, "erro 1") {
		t.Errorf("expected output to contain 'erro 1', got: %s", res.Output)
	}
	if res.Errors < 1 {
		t.Errorf("expected Errors >= 1, got %d", res.Errors)
	}
}

func TestRunTestPassingCommand(t *testing.T) {
	ctx := context.Background()
	res, err := RunTest(ctx, "echo ok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Passed {
		t.Errorf("expected Passed=true for 'echo ok', got output: %s", res.Output)
	}
	if !strings.Contains(res.Output, "ok") {
		t.Errorf("expected output to contain 'ok', got: %s", res.Output)
	}
	if res.Command != "echo ok" {
		t.Errorf("expected Command='echo ok', got %q", res.Command)
	}
}

func TestRunTestFailingCommand(t *testing.T) {
	ctx := context.Background()
	res, err := RunTest(ctx, "exit 3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Passed {
		t.Error("expected Passed=false when test command exits non-zero")
	}
}

func TestRunLintContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	res, err := RunLint(ctx, "sleep 5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// When context times out, the command fails and Passed should be false.
	if res.Passed {
		t.Error("expected Passed=false when context times out")
	}
}

func TestLintFixConfigZeroValue(t *testing.T) {
	var c LintFixConfig
	if c.Enabled {
		t.Error("expected zero-value Enabled=false")
	}
	if c.LintCmd != "" {
		t.Errorf("expected zero-value LintCmd='', got %q", c.LintCmd)
	}
	if c.MaxRetries != 0 {
		t.Errorf("expected zero-value MaxRetries=0, got %d", c.MaxRetries)
	}
	if c.AutoFix {
		t.Error("expected zero-value AutoFix=false")
	}

	// Ensure fields are settable (guard against accidental refactors).
	c.Enabled = true
	c.LintCmd = "golangci-lint run"
	c.TestCmd = "go test ./..."
	c.MaxRetries = 3
	c.AutoFix = true
	if !c.Enabled || c.MaxRetries != 3 {
		t.Error("fields did not round-trip")
	}
}

func TestDualModelStruct(t *testing.T) {
	d := DualModel{
		ArchitectModel: "claude-opus",
		EditorModel:    "claude-haiku",
	}
	if d.ArchitectModel != "claude-opus" {
		t.Errorf("expected ArchitectModel='claude-opus', got %q", d.ArchitectModel)
	}
	if d.EditorModel != "claude-haiku" {
		t.Errorf("expected EditorModel='claude-haiku', got %q", d.EditorModel)
	}
	if d.Logger != nil {
		t.Error("expected Logger=nil by default")
	}
}
