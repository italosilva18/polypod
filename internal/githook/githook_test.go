package githook

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// initGitRepo creates a real git repository in a fresh temp dir and chdirs
// into it. It restores the previous working directory on test cleanup.
// The returned path is the absolute path to the repo root.
func initGitRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	cmd := exec.Command("git", "init", "-q", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v: %s", err, out)
	}

	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	return dir
}

// hooksDir returns the absolute path to .git/hooks inside repo.
func hooksDir(repo string) string {
	return filepath.Join(repo, ".git", "hooks")
}

func TestInstall_CreatesBothHooks(t *testing.T) {
	repo := initGitRepo(t)

	installed, err := Install()
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	if len(installed) != 2 {
		t.Fatalf("expected 2 hooks installed, got %d: %v", len(installed), installed)
	}

	want := map[string]bool{"prepare-commit-msg": false, "pre-push": false}
	for _, name := range installed {
		if _, ok := want[name]; !ok {
			t.Errorf("unexpected hook name: %q", name)
			continue
		}
		want[name] = true
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("expected hook %q to be reported as installed", name)
		}
	}

	for name := range want {
		path := filepath.Join(hooksDir(repo), name)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("hook file %q missing: %v", name, err)
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %q: %v", name, err)
			continue
		}
		if !strings.Contains(string(data), "Polypod") {
			t.Errorf("hook %q does not contain Polypod marker", name)
		}
		if runtime.GOOS != "windows" {
			if info.Mode().Perm()&0111 == 0 {
				t.Errorf("hook %q is not executable (mode=%v)", name, info.Mode())
			}
		}
	}
}

func TestInstall_NotAGitRepo(t *testing.T) {
	// Chdir to a non-git directory; findGitDir should fail.
	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	// Ensure git does not resolve the temp dir to any parent repo by
	// disabling the discovery across filesystems and setting ceiling.
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))

	if _, err := Install(); err == nil {
		t.Fatal("expected Install to fail outside of a git repo, got nil error")
	}
}

func TestStatus_EmptyWhenNothingInstalled(t *testing.T) {
	initGitRepo(t)

	active, err := Status()
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if len(active) != 0 {
		t.Errorf("expected no active hooks, got %v", active)
	}
}

func TestStatus_AfterInstall(t *testing.T) {
	initGitRepo(t)

	if _, err := Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}

	active, err := Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("expected 2 active hooks, got %d: %v", len(active), active)
	}
}

func TestStatus_IgnoresForeignHooks(t *testing.T) {
	repo := initGitRepo(t)
	hd := hooksDir(repo)
	if err := os.MkdirAll(hd, 0755); err != nil {
		t.Fatal(err)
	}

	// A hook that doesn't belong to Polypod should be ignored by Status.
	foreign := filepath.Join(hd, "prepare-commit-msg")
	if err := os.WriteFile(foreign, []byte("#!/bin/sh\necho foreign\n"), 0755); err != nil {
		t.Fatal(err)
	}

	active, err := Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(active) != 0 {
		t.Errorf("expected Status to ignore foreign hook, got %v", active)
	}
}

func TestUninstall_RemovesOnlyPolypodHooks(t *testing.T) {
	repo := initGitRepo(t)

	if _, err := Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}

	removed, err := Uninstall()
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("expected 2 removed hooks, got %d: %v", len(removed), removed)
	}

	for _, name := range []string{"prepare-commit-msg", "pre-push"} {
		path := filepath.Join(hooksDir(repo), name)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("expected %q to be removed, stat err=%v", name, err)
		}
	}

	// Calling Uninstall again should be a no-op.
	removed2, err := Uninstall()
	if err != nil {
		t.Fatalf("Uninstall (second call): %v", err)
	}
	if len(removed2) != 0 {
		t.Errorf("expected no hooks removed on second Uninstall, got %v", removed2)
	}
}

func TestUninstall_PreservesForeignHook(t *testing.T) {
	repo := initGitRepo(t)
	hd := hooksDir(repo)
	if err := os.MkdirAll(hd, 0755); err != nil {
		t.Fatal(err)
	}

	foreignPath := filepath.Join(hd, "pre-push")
	foreignContent := []byte("#!/bin/sh\n# user hook\necho foreign\n")
	if err := os.WriteFile(foreignPath, foreignContent, 0755); err != nil {
		t.Fatal(err)
	}

	removed, err := Uninstall()
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("expected Uninstall to skip foreign hook, got removed=%v", removed)
	}

	data, err := os.ReadFile(foreignPath)
	if err != nil {
		t.Fatalf("foreign hook was lost: %v", err)
	}
	if string(data) != string(foreignContent) {
		t.Errorf("foreign hook content changed: got %q", string(data))
	}
}

func TestInstall_BackupsExistingForeignHook(t *testing.T) {
	repo := initGitRepo(t)
	hd := hooksDir(repo)
	if err := os.MkdirAll(hd, 0755); err != nil {
		t.Fatal(err)
	}

	// Pre-existing non-Polypod hook should be backed up with .bak suffix.
	target := filepath.Join(hd, "prepare-commit-msg")
	original := []byte("#!/bin/sh\n# custom user hook\necho custom\n")
	if err := os.WriteFile(target, original, 0755); err != nil {
		t.Fatal(err)
	}

	if _, err := Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Original should now live at target + ".bak".
	backup := target + ".bak"
	data, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("expected backup file %q, err=%v", backup, err)
	}
	if string(data) != string(original) {
		t.Errorf("backup content mismatch: got %q, want %q", string(data), string(original))
	}

	// New hook at target must be the Polypod version.
	newData, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read new hook: %v", err)
	}
	if !strings.Contains(string(newData), "Polypod") {
		t.Errorf("expected target to contain Polypod marker, got %q", string(newData))
	}
}

func TestInstall_IdempotentDoesNotDoubleBackup(t *testing.T) {
	repo := initGitRepo(t)

	if _, err := Install(); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	if _, err := Install(); err != nil {
		t.Fatalf("second Install: %v", err)
	}

	// Since the existing hook is already a Polypod hook, no .bak file
	// should be created during the second install.
	for _, name := range []string{"prepare-commit-msg", "pre-push"} {
		backup := filepath.Join(hooksDir(repo), name+".bak")
		if _, err := os.Stat(backup); !os.IsNotExist(err) {
			t.Errorf("unexpected backup file %q after idempotent Install (err=%v)", backup, err)
		}
	}

	// Status must still report both hooks active.
	active, err := Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(active) != 2 {
		t.Errorf("expected 2 active hooks after double Install, got %v", active)
	}
}
