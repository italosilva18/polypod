package architect

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// LintFixConfig controls the lint-then-fix loop.
type LintFixConfig struct {
	Enabled    bool     `yaml:"enabled"`
	LintCmd    string   `yaml:"lint_cmd"`    // e.g., "golangci-lint run"
	TestCmd    string   `yaml:"test_cmd"`    // e.g., "go test ./..."
	MaxRetries int     `yaml:"max_retries"` // default: 3
	AutoFix    bool     `yaml:"auto_fix"`    // feed errors back to AI
}

// LintResult holds the outcome of a lint check.
type LintResult struct {
	Passed  bool
	Output  string
	Errors  int
	Command string
}

// RunLint executes the configured lint command.
func RunLint(ctx context.Context, cmd string) (*LintResult, error) {
	if cmd == "" {
		// Auto-detect
		cmd = detectLintCommand()
	}
	if cmd == "" {
		return &LintResult{Passed: true, Output: "nenhum linter configurado"}, nil
	}

	execCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	c := exec.CommandContext(execCtx, "bash", "-c", cmd)
	out, err := c.CombinedOutput()
	output := strings.TrimSpace(string(out))

	if err != nil {
		errorCount := strings.Count(output, "\n") + 1
		return &LintResult{
			Passed:  false,
			Output:  output,
			Errors:  errorCount,
			Command: cmd,
		}, nil
	}

	return &LintResult{
		Passed:  true,
		Output:  output,
		Command: cmd,
	}, nil
}

// RunTest executes the configured test command.
func RunTest(ctx context.Context, cmd string) (*LintResult, error) {
	if cmd == "" {
		cmd = detectTestCommand()
	}
	if cmd == "" {
		return &LintResult{Passed: true, Output: "nenhum teste configurado"}, nil
	}

	execCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	c := exec.CommandContext(execCtx, "bash", "-c", cmd)
	out, err := c.CombinedOutput()
	output := strings.TrimSpace(string(out))

	return &LintResult{
		Passed:  err == nil,
		Output:  output,
		Command: cmd,
	}, nil
}

// FormatLintFeedback creates a prompt for the AI to fix lint errors.
func FormatLintFeedback(result *LintResult, attempt int) string {
	return fmt.Sprintf(`[LINT-FIX Loop - Tentativa %d]

O comando %s encontrou erros:

%s

Corrija TODOS os erros acima. Faca as mudancas minimas necessarias.`, attempt, result.Command, result.Output)
}

func detectLintCommand() string {
	checks := []struct {
		file string
		cmd  string
	}{
		{"go.mod", "go vet ./..."},
		{"package.json", "npx eslint . --quiet"},
		{"pyproject.toml", "ruff check ."},
		{"Cargo.toml", "cargo clippy"},
	}
	for _, c := range checks {
		if fileExists(c.file) {
			return c.cmd
		}
	}
	return ""
}

func detectTestCommand() string {
	checks := []struct {
		file string
		cmd  string
	}{
		{"go.mod", "go test ./..."},
		{"package.json", "npm test"},
		{"pyproject.toml", "python3 -m pytest -q"},
		{"Cargo.toml", "cargo test"},
	}
	for _, c := range checks {
		if fileExists(c.file) {
			return c.cmd
		}
	}
	return ""
}

func fileExists(path string) bool {
	_, err := exec.LookPath(path)
	if err == nil {
		return true
	}
	// Check as file
	_, err = exec.Command("test", "-f", path).CombinedOutput()
	return err == nil
}

// DualModel holds config for architect/editor pattern.
type DualModel struct {
	ArchitectModel string // strong model for planning
	EditorModel    string // fast model for applying changes
	Logger         *slog.Logger
}

// FormatArchitectPrompt creates the architect's planning prompt.
func FormatArchitectPrompt(task string) string {
	return fmt.Sprintf(`Voce e o ARQUITETO. Sua funcao e PLANEJAR, nao implementar.

TAREFA: %s

Instrucoes:
1. Analise o problema completamente
2. Liste QUAIS arquivos precisam ser modificados
3. Para cada arquivo, descreva EXATAMENTE o que mudar:
   - Qual funcao/bloco alterar
   - O que adicionar/remover/modificar
   - Trecho de codigo ANTES e DEPOIS (se necessario)
4. Indique a ORDEM de execucao das mudancas
5. Aponte riscos e edge cases

NAO escreva codigo completo. Descreva as mudancas em linguagem natural precisa.`, task)
}

// FormatEditorPrompt creates the editor's execution prompt from architect output.
func FormatEditorPrompt(architectPlan string) string {
	return fmt.Sprintf(`Voce e o EDITOR. Execute o plano do Arquiteto com precisao.

PLANO DO ARQUITETO:
%s

Instrucoes:
1. Siga o plano EXATAMENTE como descrito
2. Use edit_file para mudancas cirurgicas
3. Use create_file para novos arquivos
4. Nao adicione nada alem do que foi planejado
5. Apos cada mudanca, verifique se compila`, architectPlan)
}
