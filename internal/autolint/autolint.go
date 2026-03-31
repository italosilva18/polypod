package autolint

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Config controls auto-lint/test behavior.
type Config struct {
	Enabled     bool   `yaml:"enabled"`
	LintCmd     string `yaml:"lint_cmd"`
	TestCmd     string `yaml:"test_cmd"`
	MaxRetries  int    `yaml:"max_retries"`  // default: 3
	AutoFix     bool   `yaml:"auto_fix"`     // feed errors back to AI
	RunTests    bool   `yaml:"run_tests"`    // also run tests after lint
}

// Result of an auto-lint/test run.
type Result struct {
	LintPassed  bool
	TestPassed  bool
	LintOutput  string
	TestOutput  string
	LintCmd     string
	TestCmd     string
	FixPrompt   string // prompt for AI to fix errors (if AutoFix)
}

// Runner executes lint/test after file changes.
type Runner struct {
	config Config
	logger *slog.Logger
}

// NewRunner creates an auto-lint runner.
func NewRunner(cfg Config, logger *slog.Logger) *Runner {
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}
	if cfg.LintCmd == "" {
		cfg.LintCmd = detectLint()
	}
	if cfg.TestCmd == "" {
		cfg.TestCmd = detectTest()
	}
	return &Runner{config: cfg, logger: logger}
}

// AfterEdit runs lint (and optionally test) after a file edit.
// Returns a Result with fix prompt if errors found.
func (r *Runner) AfterEdit(changedFiles []string) *Result {
	if !r.config.Enabled {
		return nil
	}

	result := &Result{}

	// Run lint
	if r.config.LintCmd != "" {
		output, passed := runCmd(r.config.LintCmd)
		result.LintPassed = passed
		result.LintOutput = output
		result.LintCmd = r.config.LintCmd

		if !passed {
			r.logger.Info("auto-lint: erros encontrados", "cmd", r.config.LintCmd)
		}
	} else {
		result.LintPassed = true
	}

	// Run tests (only if lint passed and tests enabled)
	if r.config.RunTests && result.LintPassed && r.config.TestCmd != "" {
		output, passed := runCmd(r.config.TestCmd)
		result.TestPassed = passed
		result.TestOutput = output
		result.TestCmd = r.config.TestCmd

		if !passed {
			r.logger.Info("auto-test: falhas encontradas", "cmd", r.config.TestCmd)
		}
	} else {
		result.TestPassed = true
	}

	// Build fix prompt if needed
	if r.config.AutoFix && (!result.LintPassed || !result.TestPassed) {
		result.FixPrompt = buildFixPrompt(result, changedFiles)
	}

	return result
}

// FormatResult formats the result for display.
func FormatResult(r *Result) string {
	if r == nil {
		return ""
	}

	var sb strings.Builder
	if r.LintPassed && r.TestPassed {
		sb.WriteString("[auto-check] OK\n")
		return sb.String()
	}

	if !r.LintPassed {
		fmt.Fprintf(&sb, "[auto-lint] FALHOU (%s)\n%s\n", r.LintCmd, truncate(r.LintOutput, 500))
	}
	if !r.TestPassed {
		fmt.Fprintf(&sb, "[auto-test] FALHOU (%s)\n%s\n", r.TestCmd, truncate(r.TestOutput, 500))
	}
	return sb.String()
}

func buildFixPrompt(r *Result, files []string) string {
	var sb strings.Builder
	sb.WriteString("[AUTO-FIX] Erros detectados apos edicao. Corrija:\n\n")

	if !r.LintPassed {
		fmt.Fprintf(&sb, "## Lint (%s)\n```\n%s\n```\n\n", r.LintCmd, truncate(r.LintOutput, 1000))
	}
	if !r.TestPassed {
		fmt.Fprintf(&sb, "## Tests (%s)\n```\n%s\n```\n\n", r.TestCmd, truncate(r.TestOutput, 1000))
	}

	sb.WriteString("Arquivos alterados: ")
	sb.WriteString(strings.Join(files, ", "))
	sb.WriteString("\n\nCorrija os erros com mudancas minimas.")

	return sb.String()
}

func runCmd(cmd string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c := exec.CommandContext(ctx, "bash", "-c", cmd)
	out, err := c.CombinedOutput()
	output := strings.TrimSpace(string(out))
	return output, err == nil
}

func detectLint() string {
	checks := []struct {
		file, cmd string
	}{
		{"go.mod", "go vet ./..."},
		{"package.json", "npx eslint . --quiet 2>/dev/null"},
		{"pyproject.toml", "ruff check . 2>/dev/null"},
		{"Cargo.toml", "cargo clippy --quiet 2>/dev/null"},
	}
	for _, c := range checks {
		if fileExists(c.file) {
			return c.cmd
		}
	}
	return ""
}

func detectTest() string {
	checks := []struct {
		file, cmd string
	}{
		{"go.mod", "go test ./... -count=1 -short"},
		{"package.json", "npm test --silent 2>/dev/null"},
		{"pyproject.toml", "python3 -m pytest -q 2>/dev/null"},
		{"Cargo.toml", "cargo test --quiet 2>/dev/null"},
	}
	for _, c := range checks {
		if fileExists(c.file) {
			return c.cmd
		}
	}
	return ""
}

func fileExists(name string) bool {
	_, err := os.Stat(filepath.Join(".", name))
	return err == nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n... [truncado]"
}
