package commands

import (
	"errors"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
)

// withCommitGitFns swaps the package-level git hooks used by the
// /commit command for the duration of a test and restores them on
// cleanup. Any function argument that is nil leaves the corresponding
// hook untouched, so individual tests can override only what they
// care about.
func withCommitGitFns(
	t *testing.T,
	status func(cwd string) (string, error),
	diff func(cwd string) (string, error),
	log func(cwd string) (string, error),
) {
	t.Helper()
	prevStatus := gitStatusFn
	prevDiff := gitDiffStagedFn
	prevLog := gitLogFn
	if status != nil {
		gitStatusFn = status
	}
	if diff != nil {
		gitDiffStagedFn = diff
	}
	if log != nil {
		gitLogFn = log
	}
	t.Cleanup(func() {
		gitStatusFn = prevStatus
		gitDiffStagedFn = prevDiff
		gitLogFn = prevLog
	})
}

func TestRegisterPromptCommit_RegistersCanonicalNameAndAliases(t *testing.T) {
	reg := command.NewPromptRegistry()
	RegisterPromptCommit(reg)

	got, ok := reg.Get("commit")
	if !ok || got == nil {
		t.Fatalf("expected /commit to be registered")
	}
	if got.Name != "commit" {
		t.Errorf("expected Name=commit, got %q", got.Name)
	}
	if got.Description == "" {
		t.Errorf("expected non-empty Description")
	}
	if !got.InjectAsUser {
		t.Errorf("expected InjectAsUser=true")
	}

	for _, a := range []string{"c", "ci"} {
		aliased, ok := reg.Get(a)
		if !ok || aliased == nil {
			t.Errorf("expected alias %q to resolve", a)
			continue
		}
		if aliased.Name != "commit" {
			t.Errorf("alias %q resolved to %q, want commit", a, aliased.Name)
		}
	}
}

func TestRegisterPromptCommit_NilRegistryIsNoOp(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RegisterPromptCommit(nil) panicked: %v", r)
		}
	}()
	RegisterPromptCommit(nil)
}

func TestCommitPromptCommand_EmptyStagedReturnsError(t *testing.T) {
	withCommitGitFns(t,
		func(cwd string) (string, error) { return "", nil },
		func(cwd string) (string, error) { return "", nil },
		func(cwd string) (string, error) { return "abc1234 previous commit\n", nil },
	)

	cmd := commitPromptCommand()
	_, err := cmd.Expand(command.PromptContext{CWD: "/tmp/fake"})
	if err == nil {
		t.Fatalf("expected error for empty staged diff, got nil")
	}
	if !strings.Contains(err.Error(), "nothing to commit") {
		t.Errorf("expected error to mention 'nothing to commit', got: %v", err)
	}
}

func TestCommitPromptCommand_NonEmptyStagedBuildsFullPrompt(t *testing.T) {
	var gotStatusCWD, gotDiffCWD, gotLogCWD string
	withCommitGitFns(t,
		func(cwd string) (string, error) {
			gotStatusCWD = cwd
			return " M foo.go\nA  bar.go\n", nil
		},
		func(cwd string) (string, error) {
			gotDiffCWD = cwd
			return "diff --git a/foo.go b/foo.go\n+hello world\n", nil
		},
		func(cwd string) (string, error) {
			gotLogCWD = cwd
			return "abc1234 feat: something\ndef5678 fix: other\n", nil
		},
	)

	cmd := commitPromptCommand()
	res, err := cmd.Expand(command.PromptContext{CWD: "/tmp/repo"})
	if err != nil {
		t.Fatalf("Expand returned error: %v", err)
	}
	if res == nil {
		t.Fatalf("Expand returned nil result")
	}
	for _, gotCWD := range []string{gotStatusCWD, gotDiffCWD, gotLogCWD} {
		if gotCWD != "/tmp/repo" {
			t.Errorf("expected git hook to receive cwd=/tmp/repo, got %q", gotCWD)
		}
	}

	text := res.PromptText
	// System directive.
	if !strings.Contains(text, "You are drafting a git commit") {
		t.Errorf("prompt missing system directive: %q", text)
	}
	if !strings.Contains(text, "conventional commit style") {
		t.Errorf("prompt missing conventional commit directive")
	}
	if !strings.Contains(text, "72 chars") {
		t.Errorf("prompt missing subject length directive")
	}
	// Status section.
	if !strings.Contains(text, "## git status --short") {
		t.Errorf("prompt missing status heading")
	}
	if !strings.Contains(text, "M foo.go") {
		t.Errorf("prompt missing status body")
	}
	// Diff section.
	if !strings.Contains(text, "## git diff --staged") {
		t.Errorf("prompt missing diff heading")
	}
	if !strings.Contains(text, "+hello world") {
		t.Errorf("prompt missing diff body")
	}
	// Log section.
	if !strings.Contains(text, "## Recent commits") {
		t.Errorf("prompt missing recent commits heading")
	}
	if !strings.Contains(text, "abc1234 feat: something") {
		t.Errorf("prompt missing log body")
	}
	// Final instruction.
	if !strings.Contains(text, "Output ONLY the commit message") {
		t.Errorf("prompt missing final instruction")
	}
	if !strings.Contains(text, "I will run `git commit` myself") {
		t.Errorf("prompt missing deferred-run instruction")
	}

	if !res.InjectAsUser {
		t.Errorf("expected InjectAsUser=true")
	}
	if res.Display == "" {
		t.Errorf("expected non-empty Display")
	}
}

func TestCommitPromptCommand_AmendFlagAddsAmendInstruction(t *testing.T) {
	withCommitGitFns(t,
		func(cwd string) (string, error) { return "", nil }, // clean tree is OK for --amend
		func(cwd string) (string, error) { return "", nil },
		func(cwd string) (string, error) { return "abc1234 prior work\n", nil },
	)

	cmd := commitPromptCommand()
	res, err := cmd.Expand(command.PromptContext{Args: "--amend"})
	if err != nil {
		t.Fatalf("Expand returned error: %v", err)
	}
	if !strings.Contains(res.PromptText, "--amend") {
		t.Errorf("prompt should mention --amend: %q", res.PromptText)
	}
	if !strings.Contains(res.PromptText, "amends the previous commit") {
		t.Errorf("prompt should include amend instruction: %q", res.PromptText)
	}
}

func TestCommitPromptCommand_AllFlagAddsUnstagedNote(t *testing.T) {
	withCommitGitFns(t,
		func(cwd string) (string, error) { return " M foo.go\n", nil },
		func(cwd string) (string, error) { return "diff --git a/foo.go b/foo.go\n+bar\n", nil },
		func(cwd string) (string, error) { return "abc1234 initial\n", nil },
	)

	cmd := commitPromptCommand()
	res, err := cmd.Expand(command.PromptContext{Args: "--all"})
	if err != nil {
		t.Fatalf("Expand returned error: %v", err)
	}
	if !strings.Contains(res.PromptText, "--all") {
		t.Errorf("prompt should mention --all flag")
	}
	if !strings.Contains(res.PromptText, "unstaged changes") {
		t.Errorf("prompt should mention unstaged changes for --all mode")
	}

	// The short form `-a` must behave identically.
	res2, err := cmd.Expand(command.PromptContext{Args: "-a"})
	if err != nil {
		t.Fatalf("Expand(-a) returned error: %v", err)
	}
	if !strings.Contains(res2.PromptText, "unstaged changes") {
		t.Errorf("prompt for -a should mention unstaged changes")
	}
}

func TestCommitPromptCommand_DiffTruncation(t *testing.T) {
	big := strings.Repeat("a", maxCommitDiffChars+500)
	withCommitGitFns(t,
		func(cwd string) (string, error) { return " M foo.go\n", nil },
		func(cwd string) (string, error) { return big, nil },
		func(cwd string) (string, error) { return "abc1234 seed\n", nil },
	)

	cmd := commitPromptCommand()
	res, err := cmd.Expand(command.PromptContext{})
	if err != nil {
		t.Fatalf("Expand returned error: %v", err)
	}
	if !strings.Contains(res.PromptText, "[diff truncated]") {
		t.Errorf("expected truncation marker in prompt")
	}
	if strings.Count(res.PromptText, "a") >= len(big) {
		t.Errorf("diff was not actually truncated")
	}
}

func TestCommitPromptCommand_GitErrorsPropagate(t *testing.T) {
	// status error
	withCommitGitFns(t,
		func(cwd string) (string, error) { return "", errors.New("not a repo") },
		func(cwd string) (string, error) { return "", nil },
		func(cwd string) (string, error) { return "", nil },
	)
	cmd := commitPromptCommand()
	if _, err := cmd.Expand(command.PromptContext{}); err == nil || !strings.Contains(err.Error(), "not a repo") {
		t.Errorf("expected status error to propagate, got: %v", err)
	}

	// diff error
	withCommitGitFns(t,
		func(cwd string) (string, error) { return "", nil },
		func(cwd string) (string, error) { return "", errors.New("diff boom") },
		func(cwd string) (string, error) { return "", nil },
	)
	if _, err := cmd.Expand(command.PromptContext{}); err == nil || !strings.Contains(err.Error(), "diff boom") {
		t.Errorf("expected diff error to propagate, got: %v", err)
	}

	// log error
	withCommitGitFns(t,
		func(cwd string) (string, error) { return "", nil },
		func(cwd string) (string, error) { return "", nil },
		func(cwd string) (string, error) { return "", errors.New("log boom") },
	)
	if _, err := cmd.Expand(command.PromptContext{}); err == nil || !strings.Contains(err.Error(), "log boom") {
		t.Errorf("expected log error to propagate, got: %v", err)
	}
}

func TestParseCommitArgs(t *testing.T) {
	cases := map[string]commitMode{
		"":          commitModeStaged,
		"--all":     commitModeAll,
		"-a":        commitModeAll,
		"--amend":   commitModeAmend,
		"  --amend": commitModeAmend,
		"something": commitModeStaged,
	}
	for in, want := range cases {
		if got := parseCommitArgs(in); got != want {
			t.Errorf("parseCommitArgs(%q)=%v, want %v", in, got, want)
		}
	}
}
