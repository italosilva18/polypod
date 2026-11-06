package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupRepo creates a fresh git repo in t.TempDir() with a configured identity.
// Returns the absolute path to the repo directory.
func setupRepo(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	dir := t.TempDir()

	run := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v: %s", name, args, err, string(out))
		}
	}

	run("git", "init", "-b", "main")
	run("git", "config", "user.email", "test@example.com")
	run("git", "config", "user.name", "Test User")
	run("git", "config", "commit.gpgsign", "false")

	// Resolve symlinks so comparisons against GetRepoRoot() match
	// (t.TempDir() on some systems returns a path with symlinks like /tmp -> /private/tmp).
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return dir
	}
	return resolved
}

// runInDir executes a command inside the given directory, failing the test on error.
func runInDir(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v: %s", name, args, err, string(out))
	}
}

// chdir changes to dir and registers a cleanup that restores the original directory.
// Test uses t.Chdir-like semantics but is compatible with older Go versions.
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(orig)
	})
}

// commitInitial creates an initial commit in dir with a README.md file.
func commitInitial(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runInDir(t, dir, "git", "add", ".")
	runInDir(t, dir, "git", "commit", "-m", "initial commit")
}

func TestIsInsideRepo_TrueInRepo(t *testing.T) {
	dir := setupRepo(t)
	chdir(t, dir)

	if !IsInsideRepo() {
		t.Error("IsInsideRepo should be true inside a git repo")
	}
}

func TestIsInsideRepo_FalseOutside(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	chdir(t, dir)

	if IsInsideRepo() {
		t.Error("IsInsideRepo should be false outside git repo")
	}
}

func TestIsInsideRepoAt(t *testing.T) {
	dir := setupRepo(t)

	if !IsInsideRepoAt(dir) {
		t.Error("IsInsideRepoAt should be true for a git repo directory")
	}

	nonRepo := t.TempDir()
	if IsInsideRepoAt(nonRepo) {
		t.Error("IsInsideRepoAt should be false for a non-repo directory")
	}
}

func TestGetCurrentBranch(t *testing.T) {
	dir := setupRepo(t)
	commitInitial(t, dir)
	chdir(t, dir)

	branch, err := GetCurrentBranch()
	if err != nil {
		t.Fatalf("GetCurrentBranch: %v", err)
	}
	if branch == "" {
		t.Error("branch should not be empty")
	}
	if branch != "main" {
		t.Logf("branch = %q (expected main, but acceptable)", branch)
	}
}

func TestGetRepoRoot(t *testing.T) {
	dir := setupRepo(t)
	chdir(t, dir)

	root, err := GetRepoRoot()
	if err != nil {
		t.Fatalf("GetRepoRoot: %v", err)
	}
	// Resolve both sides since git may canonicalize paths differently.
	rootResolved, _ := filepath.EvalSymlinks(root)
	dirResolved, _ := filepath.EvalSymlinks(dir)
	if rootResolved != dirResolved && root != dir {
		t.Errorf("GetRepoRoot = %q, want %q", root, dir)
	}
}

func TestGetRepoRootAt(t *testing.T) {
	dir := setupRepo(t)

	root, err := GetRepoRootAt(dir)
	if err != nil {
		t.Fatalf("GetRepoRootAt: %v", err)
	}
	if root == "" {
		t.Error("repo root should not be empty")
	}
}

func TestGetStatus_UntrackedFile(t *testing.T) {
	dir := setupRepo(t)
	commitInitial(t, dir)
	chdir(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	statuses, err := GetStatus()
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if len(statuses) == 0 {
		t.Fatal("expected at least one status entry")
	}

	found := false
	for _, s := range statuses {
		if s.Path == "new.txt" && s.Status == "??" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected untracked new.txt, got %+v", statuses)
	}
}

func TestGetStatus_CleanTree(t *testing.T) {
	dir := setupRepo(t)
	commitInitial(t, dir)
	chdir(t, dir)

	statuses, err := GetStatus()
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if len(statuses) != 0 {
		t.Errorf("expected no statuses on clean tree, got %+v", statuses)
	}
}

func TestGetDiff_Unstaged(t *testing.T) {
	dir := setupRepo(t)
	commitInitial(t, dir)
	chdir(t, dir)

	// Modify the committed file
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Changed\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	diff, err := GetDiff(false)
	if err != nil {
		t.Fatalf("GetDiff: %v", err)
	}
	if diff == "" {
		t.Error("expected non-empty diff after modification")
	}
	if !strings.Contains(diff, "Changed") {
		t.Errorf("diff should contain 'Changed', got: %s", diff)
	}
}

func TestGetDiff_Staged(t *testing.T) {
	dir := setupRepo(t)
	commitInitial(t, dir)
	chdir(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Staged Change\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runInDir(t, dir, "git", "add", "README.md")

	diff, err := GetDiff(true)
	if err != nil {
		t.Fatalf("GetDiff(staged): %v", err)
	}
	if !strings.Contains(diff, "Staged Change") {
		t.Errorf("staged diff should contain 'Staged Change', got: %s", diff)
	}

	// Unstaged diff should be empty
	unstaged, err := GetDiff(false)
	if err != nil {
		t.Fatalf("GetDiff(unstaged): %v", err)
	}
	if unstaged != "" {
		t.Errorf("unstaged diff should be empty, got: %s", unstaged)
	}
}

func TestGetDiffStat(t *testing.T) {
	dir := setupRepo(t)
	commitInitial(t, dir)
	chdir(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# New\nLine2\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	stat, err := GetDiffStat(false)
	if err != nil {
		t.Fatalf("GetDiffStat: %v", err)
	}
	if stat == "" {
		t.Error("expected non-empty diff stat")
	}
}

func TestHasUncommittedChanges(t *testing.T) {
	dir := setupRepo(t)
	commitInitial(t, dir)
	chdir(t, dir)

	if HasUncommittedChanges() {
		t.Error("clean repo should have no uncommitted changes")
	}

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Edit\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if !HasUncommittedChanges() {
		t.Error("repo with modified file should have uncommitted changes")
	}
}

func TestHasStagedChanges(t *testing.T) {
	dir := setupRepo(t)
	commitInitial(t, dir)
	chdir(t, dir)

	if HasStagedChanges() {
		t.Error("clean repo should have no staged changes")
	}

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Edit\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runInDir(t, dir, "git", "add", "README.md")

	if !HasStagedChanges() {
		t.Error("repo with staged file should have staged changes")
	}
}

func TestStageFiles(t *testing.T) {
	dir := setupRepo(t)
	commitInitial(t, dir)
	chdir(t, dir)

	path := filepath.Join(dir, "foo.txt")
	if err := os.WriteFile(path, []byte("foo"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := StageFiles([]string{"foo.txt"}); err != nil {
		t.Fatalf("StageFiles: %v", err)
	}

	if !HasStagedChanges() {
		t.Error("expected staged changes after StageFiles")
	}
}

func TestStageFiles_EmptyPaths(t *testing.T) {
	dir := setupRepo(t)
	chdir(t, dir)

	err := StageFiles(nil)
	if err == nil {
		t.Error("expected error when staging empty list")
	}
}

func TestStageAll(t *testing.T) {
	dir := setupRepo(t)
	commitInitial(t, dir)
	chdir(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := StageAll(); err != nil {
		t.Fatalf("StageAll: %v", err)
	}

	if !HasStagedChanges() {
		t.Error("expected staged changes after StageAll")
	}
}

func TestCreateCommit(t *testing.T) {
	dir := setupRepo(t)
	commitInitial(t, dir)
	chdir(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := StageFiles([]string{"x.txt"}); err != nil {
		t.Fatalf("StageFiles: %v", err)
	}

	if err := CreateCommit("add x.txt"); err != nil {
		t.Fatalf("CreateCommit: %v", err)
	}

	if HasUncommittedChanges() {
		t.Error("expected clean working tree after commit")
	}

	count, err := GetCommitCount()
	if err != nil {
		t.Fatalf("GetCommitCount: %v", err)
	}
	if count != 2 {
		t.Errorf("commit count = %d, want 2", count)
	}
}

func TestCreateCommit_EmptyMessage(t *testing.T) {
	dir := setupRepo(t)
	chdir(t, dir)

	err := CreateCommit("   ")
	if err == nil {
		t.Error("expected error for empty commit message")
	}
}

func TestGetRecentCommits(t *testing.T) {
	dir := setupRepo(t)
	commitInitial(t, dir)
	chdir(t, dir)

	// Add another commit
	if err := os.WriteFile(filepath.Join(dir, "second.txt"), []byte("2"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runInDir(t, dir, "git", "add", "second.txt")
	runInDir(t, dir, "git", "commit", "-m", "second commit")

	commits, err := GetRecentCommits(10)
	if err != nil {
		t.Fatalf("GetRecentCommits: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("expected 2 commits, got %d", len(commits))
	}

	if commits[0].Subject != "second commit" {
		t.Errorf("first commit subject = %q, want 'second commit'", commits[0].Subject)
	}
	if commits[1].Subject != "initial commit" {
		t.Errorf("second commit subject = %q, want 'initial commit'", commits[1].Subject)
	}
	if commits[0].Author != "Test User" {
		t.Errorf("author = %q, want 'Test User'", commits[0].Author)
	}
	if commits[0].Hash == "" {
		t.Error("hash should not be empty")
	}
	if commits[0].Date.IsZero() {
		t.Error("date should not be zero")
	}
}

func TestGetRecentCommits_DefaultLimit(t *testing.T) {
	dir := setupRepo(t)
	commitInitial(t, dir)
	chdir(t, dir)

	// n <= 0 should default to 10
	commits, err := GetRecentCommits(0)
	if err != nil {
		t.Fatalf("GetRecentCommits(0): %v", err)
	}
	if len(commits) != 1 {
		t.Errorf("expected 1 commit, got %d", len(commits))
	}
}

func TestGetBranches(t *testing.T) {
	dir := setupRepo(t)
	commitInitial(t, dir)
	chdir(t, dir)

	// Create an extra branch
	runInDir(t, dir, "git", "branch", "feature")

	branches, err := GetBranches()
	if err != nil {
		t.Fatalf("GetBranches: %v", err)
	}
	if len(branches) < 2 {
		t.Errorf("expected at least 2 branches, got %v", branches)
	}

	hasFeature := false
	for _, b := range branches {
		if b == "feature" {
			hasFeature = true
			break
		}
	}
	if !hasFeature {
		t.Errorf("expected 'feature' branch in %v", branches)
	}
}

func TestGetFilesDiff(t *testing.T) {
	dir := setupRepo(t)
	commitInitial(t, dir)
	chdir(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Edited\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	files, err := GetFilesDiff(false)
	if err != nil {
		t.Fatalf("GetFilesDiff: %v", err)
	}
	if len(files) != 1 || files[0] != "README.md" {
		t.Errorf("GetFilesDiff = %v, want [README.md]", files)
	}

	// Staged diff should be empty before adding
	stagedFiles, err := GetFilesDiff(true)
	if err != nil {
		t.Fatalf("GetFilesDiff(staged): %v", err)
	}
	if len(stagedFiles) != 0 {
		t.Errorf("expected no staged files, got %v", stagedFiles)
	}
}

func TestGetCommitCount(t *testing.T) {
	dir := setupRepo(t)
	commitInitial(t, dir)
	chdir(t, dir)

	count, err := GetCommitCount()
	if err != nil {
		t.Fatalf("GetCommitCount: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestGetRemoteURL_NoRemote(t *testing.T) {
	dir := setupRepo(t)
	chdir(t, dir)

	_, err := GetRemoteURL()
	if err == nil {
		t.Error("expected error when no remote configured")
	}
}

func TestGetRemoteURL_WithRemote(t *testing.T) {
	dir := setupRepo(t)
	chdir(t, dir)

	runInDir(t, dir, "git", "remote", "add", "origin", "https://example.com/repo.git")

	url, err := GetRemoteURL()
	if err != nil {
		t.Fatalf("GetRemoteURL: %v", err)
	}
	if url != "https://example.com/repo.git" {
		t.Errorf("url = %q, want https://example.com/repo.git", url)
	}
}

func TestFormatStatus_Empty(t *testing.T) {
	out := FormatStatus(nil)
	if !strings.Contains(out, "clean") {
		t.Errorf("FormatStatus(nil) = %q, want contains 'clean'", out)
	}
}

func TestFormatStatus_Mixed(t *testing.T) {
	statuses := []FileStatus{
		{Path: "a.txt", Status: "M", Staged: true},
		{Path: "b.txt", Status: "M", Staged: false},
		{Path: "c.txt", Status: "??", Staged: false},
	}
	out := FormatStatus(statuses)
	if !strings.Contains(out, "Staged changes") {
		t.Error("expected 'Staged changes' section")
	}
	if !strings.Contains(out, "Unstaged changes") {
		t.Error("expected 'Unstaged changes' section")
	}
	if !strings.Contains(out, "Untracked files") {
		t.Error("expected 'Untracked files' section")
	}
	if !strings.Contains(out, "a.txt") || !strings.Contains(out, "b.txt") || !strings.Contains(out, "c.txt") {
		t.Errorf("missing file paths in output: %s", out)
	}
}

func TestFormatCommits_Empty(t *testing.T) {
	out := FormatCommits(nil)
	if !strings.Contains(out, "No commits") {
		t.Errorf("FormatCommits(nil) = %q", out)
	}
}

func TestFormatCommits_NonEmpty(t *testing.T) {
	dir := setupRepo(t)
	commitInitial(t, dir)
	chdir(t, dir)

	commits, err := GetRecentCommits(10)
	if err != nil {
		t.Fatalf("GetRecentCommits: %v", err)
	}

	out := FormatCommits(commits)
	if !strings.Contains(out, "initial commit") {
		t.Errorf("FormatCommits output missing subject: %s", out)
	}
	if !strings.Contains(out, "Test User") {
		t.Errorf("FormatCommits output missing author: %s", out)
	}
}
