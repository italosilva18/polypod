package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/costa/polypod/internal/turncheckpoint"
)

func TestDiffViewerTurnNavigation(t *testing.T) {
	v := newDiffViewer(t.TempDir(), func() ([]turncheckpoint.FileDiff, error) {
		return []turncheckpoint.FileDiff{
			{Path: "one.go", Patch: "--- a/one.go\n+++ b/one.go\n@@ -1 +1 @@\n-old\n+new\n"},
			{Path: "two.go", Patch: "--- a/two.go\n+++ b/two.go\n@@ -1 +1 @@\n-a\n+b\n"},
		}, nil
	})
	v.load("turn")
	if len(v.files) != 2 || !strings.Contains(v.View(80, 20), "one.go") {
		t.Fatalf("turn files not visible: %+v", v)
	}
	v.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if v.selected != 1 || !strings.Contains(v.View(80, 20), "two.go") {
		t.Fatalf("file navigation failed: %+v", v)
	}
	if !v.key(tea.KeyMsg{Type: tea.KeyEsc}) {
		t.Fatal("Esc did not close viewer")
	}
}

func TestDiffViewerShowsTrackedStagedAndUntrackedFiles(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "test@example.test"}, {"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	tracked := filepath.Join(root, "tracked.go")
	if err := os.WriteFile(tracked, []byte("package p\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "tracked.go"}, {"commit", "-qm", "initial"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	if err := os.WriteFile(tracked, []byte("package p\nfunc changed() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "new.go"), []byte("package p\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v := newDiffViewer(root, nil)
	if len(v.files) != 2 || !strings.Contains(v.files[0].patch+v.files[1].patch, "changed") {
		t.Fatalf("working diff omitted changes: %+v", v.files)
	}
	cmd := exec.Command("git", "add", "tracked.go")
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, output)
	}
	v.load("staged")
	if len(v.files) != 1 || v.files[0].path != "tracked.go" {
		t.Fatalf("staged scope = %+v", v.files)
	}
}
