// Package commands — batch8_git.go
// Git-flavored slash commands (batch 8): /branch, /autofix-pr,
// /security-review, /release-notes.
//
// These wire-level commands replace the batch1 stubs registered earlier
// with richer behavior backed by `git` via os/exec. All user-facing strings
// are in Brazilian Portuguese, per project convention.
package commands

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/costa/polypod/internal/command"
)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch8Git registers the git-flavored slash commands. Because the
// underlying registry stores commands by name, calling this after
// RegisterBatch1 overwrites the earlier stubs for /branch and /release-notes
// with the implementations defined in this file.
func RegisterBatch8Git(reg *command.Registry) {
	reg.Register(cmdBranchG8())
	reg.Register(cmdAutofixPRG8())
	reg.Register(cmdSecurityReviewG8())
	reg.Register(cmdReleaseNotesG8())
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// gitTimeout is the maximum time we allow any single `git` invocation to run.
const gitTimeout = 10 * time.Second

// gitExecCtx runs a git command in `wd` with a hard timeout. It returns the
// trimmed combined output and an error. If git itself is missing from PATH,
// the returned error is wrapped so callers can present a friendly message.
func gitExecCtx(wd string, args ...string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("git não encontrado no PATH: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = wd
	out, err := cmd.CombinedOutput()
	trimmed := strings.TrimRight(string(out), "\n")
	if ctx.Err() == context.DeadlineExceeded {
		return trimmed, fmt.Errorf("git %s: tempo esgotado após %s", strings.Join(args, " "), gitTimeout)
	}
	return trimmed, err
}

// ---------------------------------------------------------------------------
// /branch — show current branch + short status
// ---------------------------------------------------------------------------

func cmdBranchG8() *command.Command {
	return &command.Command{
		Name:        "branch",
		Aliases:     []string{"fork"},
		Description: "Mostra o branch atual e o status curto do repositório git",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			cur, err := gitExecCtx(ctx.WorkingDir, "branch", "--show-current")
			if err != nil {
				return errResult(fmt.Sprintf("falha ao obter branch atual: %s\n%s", err, cur)), nil
			}
			status, err := gitExecCtx(ctx.WorkingDir, "status", "-sb")
			if err != nil {
				return errResult(fmt.Sprintf("falha em git status -sb: %s\n%s", err, status)), nil
			}
			out := fmt.Sprintf("Branch atual: %s\n\nStatus:\n%s", strings.TrimSpace(cur), status)
			return okResult(out), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /autofix-pr — placeholder
// ---------------------------------------------------------------------------

func cmdAutofixPRG8() *command.Command {
	return &command.Command{
		Name:        "autofix-pr",
		Description: "Aplica correções automáticas em um PR (na fila de wiring)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult("autofix-pr queued for wiring"), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /security-review — placeholder
// ---------------------------------------------------------------------------

func cmdSecurityReviewG8() *command.Command {
	return &command.Command{
		Name:        "security-review",
		Description: "Revisão de segurança do diff atual (pendente de wiring)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult("security review command pending wiring (será delegado ao modelo via PromptCommand)"), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /release-notes — show recent commits as release notes
// ---------------------------------------------------------------------------

func cmdReleaseNotesG8() *command.Command {
	return &command.Command{
		Name:        "release-notes",
		Description: "Mostra os 20 commits mais recentes como notas de release",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			out, err := gitExecCtx(ctx.WorkingDir, "log", "--oneline", "-20")
			if err != nil {
				return errResult(fmt.Sprintf("falha em git log: %s\n%s", err, out)), nil
			}
			if strings.TrimSpace(out) == "" {
				return okResult("Nenhum commit encontrado no repositório."), nil
			}
			return okResult("Notas de release (últimos 20 commits):\n" + out), nil
		},
	}
}
