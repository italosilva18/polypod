// Package commands — batch26_workspace_test.go
//
// Smoke tests for the batch26 workspace helper commands: /workspace,
// /project and /context-summary. The tests use t.TempDir() to build small
// fake project layouts so they can exercise the filesystem inspection logic
// without depending on the real repository tree.
package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
)

// newBatch26Ctx returns a CommandContext whose WorkingDir is rooted at the
// supplied path. SessionID is always fixed because batch26 commands never
// consult it.
func newBatch26Ctx(workingDir string) command.CommandContext {
	return command.CommandContext{
		WorkingDir: workingDir,
		SessionID:  "batch26-session",
	}
}

// writeFileB26 is a tiny helper that writes an empty file at path and fails
// the enclosing test if the write errors.
func writeFileB26(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func TestRegisterBatch26Workspace_RegistersAllCommands(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch26Workspace(reg)

	for _, name := range []string{"workspace", "project", "context-summary"} {
		cmd, ok := reg.Get(name)
		if !ok {
			t.Fatalf("command %q not registered", name)
		}
		if cmd.Execute == nil {
			t.Errorf("command %q has nil Execute handler", name)
		}
		if cmd.Type != command.CommandLocal {
			t.Errorf("command %q: expected CommandLocal, got %q", name, cmd.Type)
		}
		if cmd.Source != command.SourceBuiltin {
			t.Errorf("command %q: expected SourceBuiltin, got %q", name, cmd.Source)
		}
	}
}

// ---------------------------------------------------------------------------
// /workspace
// ---------------------------------------------------------------------------

func TestCmdWorkspaceB26_NoGit(t *testing.T) {
	dir := t.TempDir()
	writeFileB26(t, filepath.Join(dir, "a.txt"))
	writeFileB26(t, filepath.Join(dir, "b.txt"))

	res, err := cmdWorkspaceB26().Execute(nil, newBatch26Ctx(dir))
	if err != nil {
		t.Fatalf("cmdWorkspaceB26 error: %v", err)
	}
	if res == nil {
		t.Fatal("cmdWorkspaceB26 returned nil result")
	}
	if !strings.Contains(res.Display, dir) {
		t.Errorf("expected display to contain working dir %q, got %q", dir, res.Display)
	}
	if !strings.Contains(res.Display, "2 arquivos") {
		t.Errorf("expected display to contain %q, got %q", "2 arquivos", res.Display)
	}
	if strings.Contains(res.Display, workspaceGitSuffixB26) {
		t.Errorf("expected no git suffix, got %q", res.Display)
	}
}

func TestCmdWorkspaceB26_WithGit(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("failed to create .git dir: %v", err)
	}
	writeFileB26(t, filepath.Join(dir, "README.md"))

	res, err := cmdWorkspaceB26().Execute(nil, newBatch26Ctx(dir))
	if err != nil {
		t.Fatalf("cmdWorkspaceB26 error: %v", err)
	}
	if res == nil {
		t.Fatal("cmdWorkspaceB26 returned nil result")
	}
	if !strings.HasSuffix(res.Display, workspaceGitSuffixB26) {
		t.Errorf("expected display to end with git suffix %q, got %q",
			workspaceGitSuffixB26, res.Display)
	}
	if !strings.Contains(res.Display, "2 arquivos") {
		t.Errorf("expected display to contain %q (.git counts as an entry), got %q",
			"2 arquivos", res.Display)
	}
}

func TestCmdWorkspaceB26_UnreadableDir(t *testing.T) {
	// A path that does not exist — os.ReadDir must fail and we must surface
	// that failure as an errResult (not a Go error).
	res, err := cmdWorkspaceB26().Execute(
		nil,
		newBatch26Ctx(filepath.Join(t.TempDir(), "does-not-exist")),
	)
	if err != nil {
		t.Fatalf("cmdWorkspaceB26 unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("cmdWorkspaceB26 returned nil result")
	}
	if !strings.HasPrefix(res.Display, "error:") {
		t.Errorf("expected error result prefix, got %q", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /project
// ---------------------------------------------------------------------------

func TestCmdProjectB26_DetectsGo(t *testing.T) {
	dir := t.TempDir()
	writeFileB26(t, filepath.Join(dir, "go.mod"))

	res, err := cmdProjectB26().Execute(nil, newBatch26Ctx(dir))
	if err != nil {
		t.Fatalf("cmdProjectB26 error: %v", err)
	}
	if res == nil {
		t.Fatal("cmdProjectB26 returned nil result")
	}
	if !strings.Contains(res.Display, "Go") {
		t.Errorf("expected display to contain %q, got %q", "Go", res.Display)
	}
	if !strings.Contains(res.Display, "go.mod") {
		t.Errorf("expected display to contain %q, got %q", "go.mod", res.Display)
	}
}

func TestCmdProjectB26_DetectsNode(t *testing.T) {
	dir := t.TempDir()
	writeFileB26(t, filepath.Join(dir, "package.json"))

	res, err := cmdProjectB26().Execute(nil, newBatch26Ctx(dir))
	if err != nil {
		t.Fatalf("cmdProjectB26 error: %v", err)
	}
	if res == nil {
		t.Fatal("cmdProjectB26 returned nil result")
	}
	if !strings.Contains(res.Display, "Node") {
		t.Errorf("expected display to contain %q, got %q", "Node", res.Display)
	}
	if !strings.Contains(res.Display, "package.json") {
		t.Errorf("expected display to contain %q, got %q", "package.json", res.Display)
	}
}

func TestCmdProjectB26_DetectsPython(t *testing.T) {
	dir := t.TempDir()
	writeFileB26(t, filepath.Join(dir, "pyproject.toml"))

	res, err := cmdProjectB26().Execute(nil, newBatch26Ctx(dir))
	if err != nil {
		t.Fatalf("cmdProjectB26 error: %v", err)
	}
	if !strings.Contains(res.Display, "Python") {
		t.Errorf("expected display to contain %q, got %q", "Python", res.Display)
	}
	if !strings.Contains(res.Display, "pyproject.toml") {
		t.Errorf("expected display to contain %q, got %q", "pyproject.toml", res.Display)
	}
}

func TestCmdProjectB26_DetectsRust(t *testing.T) {
	dir := t.TempDir()
	writeFileB26(t, filepath.Join(dir, "Cargo.toml"))

	res, err := cmdProjectB26().Execute(nil, newBatch26Ctx(dir))
	if err != nil {
		t.Fatalf("cmdProjectB26 error: %v", err)
	}
	if !strings.Contains(res.Display, "Rust") {
		t.Errorf("expected display to contain %q, got %q", "Rust", res.Display)
	}
	if !strings.Contains(res.Display, "Cargo.toml") {
		t.Errorf("expected display to contain %q, got %q", "Cargo.toml", res.Display)
	}
}

func TestCmdProjectB26_GoWinsWhenBothGoAndNodePresent(t *testing.T) {
	// Go manifest appears before Node in projectTypeMarkersB26, so it must
	// win deterministically when both files exist.
	dir := t.TempDir()
	writeFileB26(t, filepath.Join(dir, "go.mod"))
	writeFileB26(t, filepath.Join(dir, "package.json"))

	res, err := cmdProjectB26().Execute(nil, newBatch26Ctx(dir))
	if err != nil {
		t.Fatalf("cmdProjectB26 error: %v", err)
	}
	if !strings.Contains(res.Display, "Go") {
		t.Errorf("expected Go to win the priority order, got %q", res.Display)
	}
	if strings.Contains(res.Display, "Node") {
		t.Errorf("expected Node to lose to Go, got %q", res.Display)
	}
}

func TestCmdProjectB26_Unknown(t *testing.T) {
	dir := t.TempDir()
	// Create an unrelated file so the directory is not empty.
	writeFileB26(t, filepath.Join(dir, "notes.txt"))

	res, err := cmdProjectB26().Execute(nil, newBatch26Ctx(dir))
	if err != nil {
		t.Fatalf("cmdProjectB26 error: %v", err)
	}
	if res.Display != projectUnknownMessageB26 {
		t.Errorf("unexpected unknown message: got %q, want %q",
			res.Display, projectUnknownMessageB26)
	}
}

// ---------------------------------------------------------------------------
// /context-summary — placeholder
// ---------------------------------------------------------------------------

func TestCmdContextSummaryB26_Placeholder(t *testing.T) {
	res, err := cmdContextSummaryB26().Execute(nil, newBatch26Ctx(t.TempDir()))
	if err != nil {
		t.Fatalf("cmdContextSummaryB26 error: %v", err)
	}
	if res == nil {
		t.Fatal("cmdContextSummaryB26 returned nil result")
	}
	if res.Display != contextSummaryPlaceholderMessageB26 {
		t.Errorf("unexpected /context-summary placeholder: got %q, want %q",
			res.Display, contextSummaryPlaceholderMessageB26)
	}
}
