// Package commands contains slash command implementations for Polypod.
//
// This file defines the `/commit` prompt command. It gathers the
// current git status, staged diff, and recent commit history, and
// expands to a prompt body asking the model to draft a conventional
// commit message. The actual `git commit` invocation is left to the
// user (or to a downstream hook) — this command only produces the
// message.
//
// It mirrors src_extracted/src/commands/commit.ts, but is reshaped as a
// Go PromptCommand registered into `command.PromptRegistry`. All git
// work happens via package-level hooks so tests can swap the real
// binary for deterministic fakes.
package commands

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/costa/polypod/internal/command"
)

// maxCommitDiffChars bounds the size of the diff embedded in the
// expanded prompt. Past this length the diff is truncated and a marker
// is appended so the model knows the input was cut off.
const maxCommitDiffChars = 15000

// gitCommitExecTimeout is the deadline enforced on each git subprocess
// invocation used by the /commit command. Kept short because we only
// need snapshot data and want to avoid hanging the slash command.
const gitCommitExecTimeout = 15 * time.Second

// Package-level hooks overridable by tests. Each defaults to running
// the real git binary in the provided working directory.
//
//   - gitStatusFn returns `git status --short` output.
//   - gitDiffStagedFn returns the staged diff (optionally including
//     unstaged changes when the --all flag is used — callers choose
//     which variant to run).
//   - gitLogFn returns the last few commits for style cues.
var (
	gitStatusFn     func(cwd string) (string, error) = defaultGitStatusFn
	gitDiffStagedFn func(cwd string) (string, error) = defaultGitDiffStagedFn
	gitLogFn        func(cwd string) (string, error) = defaultGitLogFn
)

// runGit is a small helper that runs a git subcommand in cwd under the
// package-level timeout and returns combined output as a string.
func runGit(cwd string, args ...string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("git binary not available: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitCommitExecTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("git %s timed out after %s", strings.Join(args, " "), gitCommitExecTimeout)
	}
	if err != nil {
		trimmed := strings.TrimSpace(string(out))
		if trimmed != "" {
			return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), trimmed)
		}
		return "", fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

func defaultGitStatusFn(cwd string) (string, error) {
	return runGit(cwd, "status", "--short")
}

func defaultGitDiffStagedFn(cwd string) (string, error) {
	return runGit(cwd, "diff", "--staged", "--no-color")
}

func defaultGitLogFn(cwd string) (string, error) {
	return runGit(cwd, "log", "-5", "--oneline")
}

// commitMode enumerates the flavour of commit the user requested.
type commitMode int

const (
	commitModeStaged commitMode = iota // default: staged changes only
	commitModeAll                      // --all / -a: include unstaged changes
	commitModeAmend                    // --amend: rewrite the last commit
)

// parseCommitArgs maps the raw argument string to a commitMode. Any
// unknown token falls back to commitModeStaged so new/unknown flags
// don't accidentally mutate history.
func parseCommitArgs(args string) commitMode {
	switch strings.TrimSpace(args) {
	case "--all", "-a":
		return commitModeAll
	case "--amend":
		return commitModeAmend
	default:
		return commitModeStaged
	}
}

// commitPromptCommand builds the `/commit` PromptCommand.
//
// The expanded prompt always contains:
//   - a system directive about conventional commit style and subject
//     length limits;
//   - the output of `git status --short` so the model sees which files
//     are touched;
//   - the staged diff (truncated at maxCommitDiffChars);
//   - the last five commits as style cues;
//   - a final instruction asking for ONLY the commit message, with no
//     explanation, because the user will run `git commit` themselves.
//
// When args is `--all` or `-a`, the prompt includes a note that the
// user wants unstaged changes considered too. When args is `--amend`,
// an additional instruction about amending the previous commit is
// injected. Otherwise the command operates on staged changes only.
//
// If nothing is staged (and the mode is not --amend), GetPromptFn
// returns an error so the dispatcher can surface a friendly message
// instead of expanding an empty prompt.
func commitPromptCommand() *command.PromptCommand {
	return &command.PromptCommand{
		Name:            "commit",
		Description:     "Draft and create a git commit from current staged changes",
		Aliases:         []string{"c", "ci"},
		InjectAsUser:    true,
		ProgressMessage: "drafting commit",
		GetPromptFn: func(ctx command.PromptContext) (string, error) {
			mode := parseCommitArgs(ctx.Args)

			status, err := gitStatusFn(ctx.CWD)
			if err != nil {
				return "", fmt.Errorf("/commit: git status: %w", err)
			}
			diff, err := gitDiffStagedFn(ctx.CWD)
			if err != nil {
				return "", fmt.Errorf("/commit: git diff: %w", err)
			}
			log, err := gitLogFn(ctx.CWD)
			if err != nil {
				return "", fmt.Errorf("/commit: git log: %w", err)
			}

			// "Nothing to commit" guard: for the default and --all
			// modes, we need at least some staged content. --amend
			// legitimately has nothing new to stage, so skip the
			// check there.
			if mode != commitModeAmend && strings.TrimSpace(diff) == "" {
				return "", fmt.Errorf("/commit: nothing to commit (no staged changes)")
			}

			truncated := false
			if len(diff) > maxCommitDiffChars {
				diff = diff[:maxCommitDiffChars]
				truncated = true
			}

			var b strings.Builder
			b.WriteString("You are drafting a git commit. Follow conventional commit style. Keep the subject \u226472 chars.\n\n")

			switch mode {
			case commitModeAll:
				b.WriteString("Note: the user passed --all, so unstaged changes should be considered as well.\n\n")
			case commitModeAmend:
				b.WriteString("Note: the user passed --amend — draft a commit message that amends the previous commit. Respect the existing scope of the prior change when phrasing the subject.\n\n")
			}

			b.WriteString("## git status --short\n")
			b.WriteString("```\n")
			if strings.TrimSpace(status) == "" {
				b.WriteString("(clean working tree)\n")
			} else {
				b.WriteString(status)
				if !strings.HasSuffix(status, "\n") {
					b.WriteString("\n")
				}
			}
			b.WriteString("```\n\n")

			b.WriteString("## git diff --staged\n")
			b.WriteString("```diff\n")
			if strings.TrimSpace(diff) == "" {
				b.WriteString("(no staged changes)\n")
			} else {
				b.WriteString(diff)
				if !strings.HasSuffix(diff, "\n") {
					b.WriteString("\n")
				}
			}
			if truncated {
				b.WriteString("[diff truncated]\n")
			}
			b.WriteString("```\n\n")

			b.WriteString("## Recent commits (git log -5 --oneline)\n")
			b.WriteString("```\n")
			if strings.TrimSpace(log) == "" {
				b.WriteString("(no prior commits)\n")
			} else {
				b.WriteString(log)
				if !strings.HasSuffix(log, "\n") {
					b.WriteString("\n")
				}
			}
			b.WriteString("```\n\n")

			b.WriteString("Draft a commit message. Output ONLY the commit message, no explanation. I will run `git commit` myself after you respond.\n")

			return b.String(), nil
		},
	}
}

// RegisterPromptCommit registers the `/commit` prompt command into the
// given registry. It is safe to call multiple times — the registry
// overwrites entries with the same name.
//
// A nil registry is a no-op so callers can wire the command in before
// the registry is constructed without crashing.
func RegisterPromptCommit(reg *command.PromptRegistry) {
	if reg == nil {
		return
	}
	reg.Register(commitPromptCommand())
}
