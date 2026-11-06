package docgen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/skill"
)

// newEmptyRegistry returns a registry with no skills pre-registered,
// so tests can observe exactly what RegisterSkills adds.
func newEmptyRegistry(t *testing.T) *skill.Registry {
	t.Helper()
	r := skill.NewRegistry()
	// Remove any pre-existing skills with the docgen names so
	// RegisterSkills results are unambiguous.
	for _, name := range []string{"docs_readme", "docs_changelog", "docs_api", "docs_godoc"} {
		r.Unregister(name)
	}
	return r
}

// execSkill is a helper that looks up a skill and runs Execute with the
// provided args, failing the test if either step errors unexpectedly.
func execSkill(t *testing.T, r *skill.Registry, name string, args map[string]string) (string, error) {
	t.Helper()
	s, ok := r.Get(name)
	if !ok {
		t.Fatalf("skill %q not registered", name)
	}
	return s.Execute(args)
}

// hasGit reports whether the `git` binary is available on PATH.
func hasGit() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// initGitRepo creates a minimal git repository in dir with one commit.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// Prevent user/global git config from interfering.
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_SYSTEM=/dev/null",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "initial commit")
}

// chdir switches to dir for the duration of the test.
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

// -------------------- Registration --------------------

func TestRegisterSkills_RegistersExpectedSkills(t *testing.T) {
	r := newEmptyRegistry(t)
	RegisterSkills(r)

	want := []string{"docs_readme", "docs_changelog", "docs_api", "docs_godoc"}
	for _, name := range want {
		s, ok := r.Get(name)
		if !ok {
			t.Errorf("skill %q not registered", name)
			continue
		}
		if s.Name != name {
			t.Errorf("skill.Name = %q, want %q", s.Name, name)
		}
		if s.Description == "" {
			t.Errorf("skill %q has empty Description", name)
		}
		if s.Execute == nil {
			t.Errorf("skill %q has nil Execute", name)
		}
	}
}

func TestRegisterSkills_GodocRequiresPath(t *testing.T) {
	r := newEmptyRegistry(t)
	RegisterSkills(r)

	s, ok := r.Get("docs_godoc")
	if !ok {
		t.Fatal("docs_godoc not registered")
	}
	found := false
	for _, req := range s.Parameters.Required {
		if req == "path" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("docs_godoc should declare 'path' as required, got %v", s.Parameters.Required)
	}
}

// -------------------- docs_readme --------------------

func TestDocsReadme_DefaultPath(t *testing.T) {
	r := newEmptyRegistry(t)
	RegisterSkills(r)

	// Run from a temp dir so "." resolves there.
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "cmd"), 0o755); err != nil {
		t.Fatalf("mkdir cmd: %v", err)
	}
	chdir(t, tmp)

	out, err := execSkill(t, r, "docs_readme", map[string]string{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "[DOC_GENERATE type=readme]") {
		t.Errorf("output missing DOC_GENERATE header: %s", out)
	}
	if !strings.Contains(out, "go.mod") {
		t.Errorf("output should mention detected go.mod: %s", out)
	}
	if !strings.Contains(out, "cmd") {
		t.Errorf("output should list top-level 'cmd' dir: %s", out)
	}
}

func TestDocsReadme_ExplicitPath(t *testing.T) {
	r := newEmptyRegistry(t)
	RegisterSkills(r)

	tmp := t.TempDir()
	// Add a few marker files the collector should detect.
	for _, f := range []string{"package.json", "Dockerfile", "Makefile"} {
		if err := os.WriteFile(filepath.Join(tmp, f), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(tmp, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	// Hidden dir should be skipped in the Estrutura listing.
	if err := os.MkdirAll(filepath.Join(tmp, ".hidden"), 0o755); err != nil {
		t.Fatalf("mkdir .hidden: %v", err)
	}

	out, err := execSkill(t, r, "docs_readme", map[string]string{"path": tmp})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	wantSubstrings := []string{
		"Node.js: package.json",
		"Docker: Dockerfile",
		"Makefile: Makefile",
		"src (dir)",
	}
	for _, sub := range wantSubstrings {
		if !strings.Contains(out, sub) {
			t.Errorf("output missing %q\n--- got ---\n%s", sub, out)
		}
	}
	if strings.Contains(out, ".hidden") {
		t.Errorf("hidden dir should be omitted, got:\n%s", out)
	}
}

// -------------------- docs_changelog --------------------

func TestDocsChangelog_NoSinceUsesLimit(t *testing.T) {
	if !hasGit() {
		t.Skip("git not available")
	}
	r := newEmptyRegistry(t)
	RegisterSkills(r)

	tmp := t.TempDir()
	initGitRepo(t, tmp)
	chdir(t, tmp)

	out, err := execSkill(t, r, "docs_changelog", map[string]string{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "[DOC_GENERATE type=changelog") {
		t.Errorf("output missing DOC_GENERATE header: %s", out)
	}
	if !strings.Contains(out, "initial commit") {
		t.Errorf("output should contain commit subject 'initial commit':\n%s", out)
	}
	if !strings.Contains(out, "Keep a Changelog") {
		t.Errorf("output should reference Keep a Changelog format:\n%s", out)
	}
}

func TestDocsChangelog_WithSinceMissingRefErrors(t *testing.T) {
	if !hasGit() {
		t.Skip("git not available")
	}
	r := newEmptyRegistry(t)
	RegisterSkills(r)

	tmp := t.TempDir()
	initGitRepo(t, tmp)
	chdir(t, tmp)

	// A nonexistent ref should cause git log to fail and propagate as error.
	_, err := execSkill(t, r, "docs_changelog", map[string]string{"since": "v999.999.999"})
	if err == nil {
		t.Fatal("expected error for nonexistent ref, got nil")
	}
	if !strings.Contains(err.Error(), "git log") {
		t.Errorf("error should mention 'git log', got: %v", err)
	}
}

// -------------------- docs_api --------------------

func TestDocsApi_SingleFile(t *testing.T) {
	r := newEmptyRegistry(t)
	RegisterSkills(r)

	tmp := t.TempDir()
	src := filepath.Join(tmp, "lib.go")
	content := "package lib\n\nfunc Hello() string { return \"hi\" }\n"
	if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	out, err := execSkill(t, r, "docs_api", map[string]string{"path": src})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "[DOC_GENERATE type=api format=markdown]") {
		t.Errorf("output missing markdown header: %s", out)
	}
	if !strings.Contains(out, "func Hello()") {
		t.Errorf("output should include file contents: %s", out)
	}
}

func TestDocsApi_DirectoryScansSources(t *testing.T) {
	r := newEmptyRegistry(t)
	RegisterSkills(r)

	tmp := t.TempDir()
	files := map[string]string{
		"a.go":      "package a\nfunc A() {}\n",
		"b.py":      "def b(): pass\n",
		"README.md": "# ignored\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	out, err := execSkill(t, r, "docs_api", map[string]string{
		"path":   tmp,
		"format": "openapi",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "format=openapi") {
		t.Errorf("output should echo format=openapi: %s", out)
	}
	if !strings.Contains(out, "a.go") || !strings.Contains(out, "func A()") {
		t.Errorf("output should include a.go contents: %s", out)
	}
	if !strings.Contains(out, "b.py") || !strings.Contains(out, "def b()") {
		t.Errorf("output should include b.py contents: %s", out)
	}
	if strings.Contains(out, "# ignored") {
		t.Errorf("README.md should be skipped (not a source extension), got:\n%s", out)
	}
}

func TestDocsApi_NonexistentPathErrors(t *testing.T) {
	r := newEmptyRegistry(t)
	RegisterSkills(r)

	_, err := execSkill(t, r, "docs_api", map[string]string{
		"path": filepath.Join(t.TempDir(), "does-not-exist"),
	})
	if err == nil {
		t.Fatal("expected error for nonexistent path, got nil")
	}
}

// -------------------- docs_godoc --------------------

func TestDocsGodoc_ReadsFile(t *testing.T) {
	r := newEmptyRegistry(t)
	RegisterSkills(r)

	tmp := t.TempDir()
	src := filepath.Join(tmp, "x.go")
	body := "package x\n\nfunc Foo() {}\n"
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	out, err := execSkill(t, r, "docs_godoc", map[string]string{"path": src})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "[DOC_GENERATE type=godoc]") {
		t.Errorf("output missing header: %s", out)
	}
	if !strings.Contains(out, src) {
		t.Errorf("output should include file path %s: %s", src, out)
	}
	if !strings.Contains(out, "func Foo()") {
		t.Errorf("output should include file body: %s", out)
	}
	if !strings.Contains(out, "```go") {
		t.Errorf("output should wrap code in go fenced block: %s", out)
	}
}

func TestDocsGodoc_MissingFileErrors(t *testing.T) {
	r := newEmptyRegistry(t)
	RegisterSkills(r)

	_, err := execSkill(t, r, "docs_godoc", map[string]string{
		"path": filepath.Join(t.TempDir(), "missing.go"),
	})
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}
