// Package commands — batch8_git_test.go
// Smoke tests for the batch8 git-flavored slash commands.
package commands

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
)

// newBatch8Ctx returns a CommandContext rooted in an isolated temp dir that
// is initialized as a git repository (when git is available). When git is
// not on PATH the test using it should call t.Skip; this helper just sets
// up directories.
func newBatch8Ctx(t *testing.T) command.CommandContext {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("POLYPOD_CONFIG_DIR", tmp+"/polypod-home")
	t.Setenv("HOME", tmp+"/home")
	return command.CommandContext{
		WorkingDir: tmp,
		SessionID:  "batch8-test-session",
	}
}

// gitAvailable reports whether `git` exists on PATH.
func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// initRepo initializes a minimal git repo with one commit so /branch and
// /release-notes have something to chew on.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	if !gitAvailable() {
		t.Skip("git not available; skipping repo-backed test")
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "tester@example.com")
	run("config", "user.name", "Tester")
	run("commit", "--allow-empty", "-q", "-m", "initial commit")
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func TestRegisterBatch8Git_RegistersAll(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch8Git(reg)

	for _, name := range []string{
		"branch",
		"pr_comments",
		"autofix-pr",
		"security-review",
		"release-notes",
	} {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("comando %q não registrado", name)
		}
	}
	// pr_comments alias
	if _, ok := reg.Get("pr-comments"); !ok {
		t.Error("alias 'pr-comments' não registrado para /pr_comments")
	}
}

// ---------------------------------------------------------------------------
// Placeholder commands
// ---------------------------------------------------------------------------

func TestBatch8_Placeholders(t *testing.T) {
	ctx := newBatch8Ctx(t)

	cases := []struct {
		cmd  *command.Command
		want string
	}{
		{cmdPRCommentsG8(), "PR comments feature pending MCP wiring"},
		{cmdAutofixPRG8(), "autofix-pr queued for wiring"},
		{cmdSecurityReviewG8(), "security review command pending wiring"},
	}
	for _, tc := range cases {
		res, err := tc.cmd.Execute(nil, ctx)
		if err != nil {
			t.Fatalf("%s: erro inesperado: %v", tc.cmd.Name, err)
		}
		if !strings.Contains(res.Display, tc.want) {
			t.Errorf("%s: esperava conter %q, obteve %q", tc.cmd.Name, tc.want, res.Display)
		}
	}
}

// ---------------------------------------------------------------------------
// /branch (smoke test against a real temp git repo)
// ---------------------------------------------------------------------------

func TestCmdBranchG8_RealRepo(t *testing.T) {
	ctx := newBatch8Ctx(t)
	initRepo(t, ctx.WorkingDir)

	res, err := cmdBranchG8().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !strings.Contains(res.Display, "Branch atual:") {
		t.Errorf("saída esperava 'Branch atual:', obteve %q", res.Display)
	}
	if !strings.Contains(res.Display, "Status:") {
		t.Errorf("saída esperava 'Status:', obteve %q", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /release-notes (smoke test against a real temp git repo)
// ---------------------------------------------------------------------------

func TestCmdReleaseNotesG8_RealRepo(t *testing.T) {
	ctx := newBatch8Ctx(t)
	initRepo(t, ctx.WorkingDir)

	res, err := cmdReleaseNotesG8().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !strings.Contains(res.Display, "Notas de release") {
		t.Errorf("saída esperava cabeçalho 'Notas de release', obteve %q", res.Display)
	}
	if !strings.Contains(res.Display, "initial commit") {
		t.Errorf("saída esperava conter 'initial commit', obteve %q", res.Display)
	}
}
