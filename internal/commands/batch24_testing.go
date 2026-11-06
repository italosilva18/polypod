// Package commands — batch24_testing.go
//
// Testing-flavored slash commands for rodada 3 wave 1 batch 24:
//
//   - /testgen [file] — placeholder that will eventually be delegated to the
//     model via PromptCommand to generate unit tests for the
//     given file. Currently returns a pending-wiring notice.
//
//   - /coverage       — runs `go test -cover ./...` inside the working
//     directory with a 60s timeout, but only when the
//     current working directory looks like a Go project
//     (has a go.mod at its root). Otherwise falls back to
//     a Portuguese placeholder message.
//
//   - /lint           — runs `go vet ./...` inside the working directory,
//     also gated by the presence of go.mod. Otherwise
//     falls back to a Portuguese placeholder message.
//
// All three commands are implemented as *command.Command values so they can
// be plugged into the shared command.Registry used by the rest of the CLI.
//
// Identifiers are suffixed with "B24" to keep them isolated from other
// batches in the same package and avoid collisions with older batches.
package commands

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/costa/polypod/internal/command"
)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch24Testing registers /testgen, /coverage and /lint on the
// supplied registry.
func RegisterBatch24Testing(reg *command.Registry) {
	reg.Register(cmdTestgenB24())
	reg.Register(cmdCoverageB24())
	reg.Register(cmdLintB24())
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// goTestTimeout caps how long `go test -cover ./...` and similar commands
// are allowed to run before we give up with a deadline-exceeded error.
const goTestTimeout = 60 * time.Second

// testgenPlaceholderMessage is the fixed string returned by /testgen while
// real test generation is pending wiring into PromptCommand.
const testgenPlaceholderMessage = "test generation pendente wiring — use PromptCommand"

// coverageNotGoProjectMessage is the placeholder returned by /coverage when
// the current working directory does not look like a Go project.
const coverageNotGoProjectMessage = "cobertura de testes pendente: diretório atual não é um projeto Go (go.mod ausente)"

// lintNotGoProjectMessage is the placeholder returned by /lint when the
// current working directory does not look like a Go project.
const lintNotGoProjectMessage = "lint pendente: diretório atual não é um projeto Go (go.mod ausente)"

// isGoProjectDir reports whether `wd` contains a regular go.mod file at its
// root. A blank working directory is treated as "not a Go project".
func isGoProjectDir(wd string) bool {
	if strings.TrimSpace(wd) == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(wd, "go.mod"))
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// runGoCmdB24 runs a `go` subcommand in `wd` with a hard timeout. It returns
// the trimmed combined output along with any execution error. If `go` itself
// is missing from PATH the returned error is wrapped with a friendly message.
func runGoCmdB24(wd string, args ...string) (string, error) {
	if _, err := exec.LookPath("go"); err != nil {
		return "", fmt.Errorf("binário 'go' não encontrado no PATH: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), goTestTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = wd
	out, err := cmd.CombinedOutput()
	trimmed := strings.TrimRight(string(out), "\n")
	if ctx.Err() == context.DeadlineExceeded {
		return trimmed, fmt.Errorf("go %s: tempo esgotado após %s", strings.Join(args, " "), goTestTimeout)
	}
	return trimmed, err
}

// ---------------------------------------------------------------------------
// /testgen — placeholder, awaits PromptCommand wiring
// ---------------------------------------------------------------------------

func cmdTestgenB24() *command.Command {
	return &command.Command{
		Name:        "testgen",
		Description: "Gerar testes unitários para o arquivo informado (pendente de wiring com PromptCommand)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult(testgenPlaceholderMessage), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /coverage — runs `go test -cover ./...` when in a Go project
// ---------------------------------------------------------------------------

func cmdCoverageB24() *command.Command {
	return &command.Command{
		Name:        "coverage",
		Description: "Executar 'go test -cover ./...' no projeto Go atual (timeout de 60s)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if !isGoProjectDir(ctx.WorkingDir) {
				return okResult(coverageNotGoProjectMessage), nil
			}
			out, err := runGoCmdB24(ctx.WorkingDir, "test", "-cover", "./...")
			if err != nil {
				return errResult(fmt.Sprintf("falha em 'go test -cover ./...': %s\n%s", err, out)), nil
			}
			if strings.TrimSpace(out) == "" {
				return okResult("Cobertura de testes: nenhuma saída capturada."), nil
			}
			return okResult("Cobertura de testes:\n" + out), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /lint — runs `go vet ./...` when in a Go project
// ---------------------------------------------------------------------------

func cmdLintB24() *command.Command {
	return &command.Command{
		Name:        "lint",
		Description: "Executar 'go vet ./...' no projeto Go atual",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if !isGoProjectDir(ctx.WorkingDir) {
				return okResult(lintNotGoProjectMessage), nil
			}
			out, err := runGoCmdB24(ctx.WorkingDir, "vet", "./...")
			if err != nil {
				return errResult(fmt.Sprintf("falha em 'go vet ./...': %s\n%s", err, out)), nil
			}
			if strings.TrimSpace(out) == "" {
				return okResult("Lint (go vet): nenhum problema reportado."), nil
			}
			return okResult("Lint (go vet):\n" + out), nil
		},
	}
}
