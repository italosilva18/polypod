package projectchecks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunOnlyEnabledEventAndReportFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".polypod"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := "verifiers:\n  test: 'printf failed; exit 2'\nauto_run:\n  on_turn: true\n"
	if err := os.WriteFile(filepath.Join(root, ".polypod", "verifiers.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := Run(context.Background(), root, "save"); err != nil || len(got) != 0 {
		t.Fatalf("disabled event ran: %+v, %v", got, err)
	}
	if got, err := Run(context.Background(), root, "manual"); err != nil || len(got) != 1 {
		t.Fatalf("manual verification unavailable: %+v, %v", got, err)
	}
	got, err := Run(context.Background(), root, "turn")
	if err != nil || len(got) != 1 || got[0].Err == nil || !strings.Contains(FormatResults(got), "failed") {
		t.Fatalf("failed check lost: %+v, %v", got, err)
	}
}

func TestFormatConfiguredGoFile(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".polypod"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := "format_on_write: true\nformatters:\n  .go: [gofmt, -w, '{file}']\n"
	if err := os.WriteFile(filepath.Join(root, ".polypod", "verifiers.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "file.go")
	if err := os.WriteFile(path, []byte("package p\nfunc f(){println(1)}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	name, err := Format(context.Background(), path)
	if err != nil || name != "gofmt" {
		t.Fatalf("format: %q, %v", name, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "func f() {") {
		t.Fatalf("file not formatted: %q, %v", data, err)
	}
}

func TestConfigSnapshotBlocksNewAutomaticCommands(t *testing.T) {
	root := t.TempDir()
	ctx := WithConfigSnapshot(context.Background(), root)
	if ConfigChanged(ctx, root) {
		t.Fatal("unchanged empty configuration marked changed")
	}
	if err := os.Mkdir(filepath.Join(root, ".polypod"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".polypod", "verifiers.yaml")
	if err := os.WriteFile(path, []byte("verifiers:\n  test: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !ConfigChanged(ctx, root) {
		t.Fatal("new commands were trusted during the turn")
	}
	ctx = WithConfigSnapshot(context.Background(), root)
	if ConfigChanged(ctx, root) {
		t.Fatal("existing configuration changed without a write")
	}
	if err := os.WriteFile(path, []byte("verifiers:\n  test: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !ConfigChanged(ctx, root) {
		t.Fatal("modified commands were trusted during the turn")
	}
}
