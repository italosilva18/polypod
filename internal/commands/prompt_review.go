// Package commands contains slash command implementations for Polypod.
//
// This file defines the `/review` prompt command, which expands to a
// prompt body asking the model to review the current set of changes
// (either the working tree diff or a specified git ref range / PR URL).
//
// It mirrors src_extracted/src/commands/review.ts but is rewritten as a
// Go PromptCommand that uses `command.PromptRegistry`. The diff is
// gathered by invoking the local `git` binary via os/exec. Tests can
// override that behaviour by swapping the package-level gitDiffFn hook.
package commands

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/costa/polypod/internal/command"
)

// maxReviewDiffChars bounds the size of the diff embedded in the
// expanded prompt. Past this length we truncate and append a marker so
// the model knows the input was cut off.
const maxReviewDiffChars = 20000

// gitDiffExecTimeout is the deadline enforced on the `git diff` exec
// call. Kept short because we only need a snapshot of the current
// working tree and want to avoid hanging the slash command.
const gitDiffExecTimeout = 15 * time.Second

// gitDiffFn is the package-level hook used to gather the diff. Tests
// may overwrite it to return deterministic content without running the
// real git binary.
//
// The ref argument is the ref spec passed to `git diff`. When empty,
// callers should default to "HEAD" (i.e. the uncommitted changes).
var gitDiffFn = defaultGitDiffFn

// defaultGitDiffFn runs `git diff --no-color <ref>` inside cwd. When
// the git binary is missing or git returns an error (e.g. cwd is not a
// repository) the error is propagated to the caller.
func defaultGitDiffFn(cwd string, ref string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("git binary not available: %w", err)
	}

	args := []string{"diff", "--no-color"}
	if ref != "" {
		args = append(args, ref)
	}

	ctx, cancel := context.WithTimeout(context.Background(), gitDiffExecTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("git diff timed out after %s", gitDiffExecTimeout)
	}
	if err != nil {
		trimmed := strings.TrimSpace(string(out))
		if trimmed != "" {
			return "", fmt.Errorf("git diff failed: %s", trimmed)
		}
		return "", fmt.Errorf("git diff failed: %w", err)
	}
	return string(out), nil
}

// looksLikePRURL reports whether the given argument appears to be a
// GitHub/GitLab style pull-request URL. Matching is intentionally
// loose: any argument that contains "://" and "/pull/" (or "/merge_requests/")
// is treated as a PR reference.
func looksLikePRURL(s string) bool {
	if !strings.Contains(s, "://") {
		return false
	}
	return strings.Contains(s, "/pull/") || strings.Contains(s, "/merge_requests/")
}

// reviewPromptCommand builds the `/review` PromptCommand.
//
// Behaviour of GetPromptFn based on ctx.Args:
//
//   - empty           → diff of the working tree vs HEAD
//   - a ref (e.g.
//     `main..feature`) → diff of that range
//   - a PR URL        → the URL is included in the prompt; no diff is
//     collected (the model is expected to fetch
//     it via its own tools)
//
// The expanded prompt always contains a system directive, an optional
// diff payload, and a fixed review checklist so the model produces a
// consistent structure.
func reviewPromptCommand() *command.PromptCommand {
	return &command.PromptCommand{
		Name:            "review",
		Description:     "Review the current changes or a specific PR",
		Aliases:         []string{"rev", "pr-review"},
		InjectAsUser:    true,
		ProgressMessage: "reviewing changes",
		GetPromptFn: func(ctx command.PromptContext) (string, error) {
			args := strings.TrimSpace(ctx.Args)

			var (
				diff        string
				diffErr     error
				prURL       string
				refLabel    string
				includeDiff bool
			)

			switch {
			case args == "":
				refLabel = "HEAD"
				includeDiff = true
			case looksLikePRURL(args):
				prURL = args
				includeDiff = false
			default:
				refLabel = args
				includeDiff = true
			}

			if includeDiff {
				diff, diffErr = gitDiffFn(ctx.CWD, refLabel)
				if diffErr != nil {
					return "", fmt.Errorf("/review: %w", diffErr)
				}
				if len(diff) > maxReviewDiffChars {
					diff = diff[:maxReviewDiffChars] + "\n[diff truncated]"
				}
			}

			var b strings.Builder
			b.WriteString("You are reviewing code changes. Be thorough but focused.\n\n")

			if prURL != "" {
				b.WriteString("Pull request URL: ")
				b.WriteString(prURL)
				b.WriteString("\n\n")
				b.WriteString("Fetch the PR using your available tools (e.g. `gh pr view`, `gh pr diff`) and review it.\n\n")
			} else {
				b.WriteString(fmt.Sprintf("Git diff (ref: %s):\n", refLabel))
				b.WriteString("```diff\n")
				if strings.TrimSpace(diff) == "" {
					b.WriteString("(no changes)\n")
				} else {
					b.WriteString(diff)
					if !strings.HasSuffix(diff, "\n") {
						b.WriteString("\n")
					}
				}
				b.WriteString("```\n\n")
			}

			b.WriteString("Please review against this checklist:\n")
			b.WriteString("1. Correctness — logic bugs, edge cases, error handling.\n")
			b.WriteString("2. Security — input validation, secrets, auth, injection.\n")
			b.WriteString("3. Style — naming, readability, project conventions.\n")
			b.WriteString("4. Tests — coverage of new behaviour and regressions.\n\n")
			b.WriteString("Format your response with clear sections and bullet points. Cite specific files and line ranges where possible.\n")

			return b.String(), nil
		},
	}
}

// RegisterPromptReview registers the `/review` prompt command into the
// given registry. It is safe to call multiple times — the registry
// overwrites entries with the same name.
//
// A nil registry is a no-op so callers can wire the command in before
// the registry is constructed without crashing.
func RegisterPromptReview(reg *command.PromptRegistry) {
	if reg == nil {
		return
	}
	reg.Register(reviewPromptCommand())
}
