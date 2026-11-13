package workdir

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPinnedBaseSurvivesWorktreeExitAndReload(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "base")
	branch := filepath.Join(root, "branch")
	for _, dir := range []string{base, branch} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(root, "workdirs.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithScope(context.Background(), store, "acp", "user", "acp:user")
	if err := SetBase(ctx, base); err != nil {
		t.Fatal(err)
	}
	if err := Enter(ctx, branch, base, "main"); err != nil {
		t.Fatal(err)
	}
	if err := SetBase(ctx, branch); err == nil {
		t.Fatal("changed pinned base")
	}
	if previous, err := Exit(ctx, branch); err != nil || previous != base {
		t.Fatalf("exit = %q, %v", previous, err)
	}
	reloaded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	reloadedCtx := WithScope(context.Background(), reloaded, "acp", "user", "acp:user")
	if got := Dir(reloadedCtx, ""); got != base {
		t.Fatalf("restored base = %q, want %q", got, base)
	}
	if err := SetBase(reloadedCtx, base); err != nil {
		t.Fatalf("idempotent base: %v", err)
	}
	if _, err := Exit(reloadedCtx, base); !errors.Is(err, ErrNotEntered) {
		t.Fatalf("exit pinned base: %v", err)
	}
	if _, err := Exit(reloadedCtx, branch); !errors.Is(err, ErrNotEntered) {
		t.Fatalf("exit absent branch: %v", err)
	}
}
