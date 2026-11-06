package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- ProjectConfig construction ---

func TestProjectConfigConstruction(t *testing.T) {
	cfg := &ProjectConfig{
		Content:  "hello",
		FilePath: "/tmp/.polypod.md",
	}
	if cfg.Content != "hello" {
		t.Errorf("Content = %q, want %q", cfg.Content, "hello")
	}
	if cfg.FilePath != "/tmp/.polypod.md" {
		t.Errorf("FilePath = %q, want %q", cfg.FilePath, "/tmp/.polypod.md")
	}
}

// --- itoaSimple helper ---

func TestItoaSimple(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{1, "1"},
		{9, "9"},
		{10, "10"},
		{123, "123"},
		{-7, "-7"},
		{-1234, "-1234"},
	}
	for _, c := range cases {
		got := itoaSimple(c.in)
		if got != c.want {
			t.Errorf("itoaSimple(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- countLabel helper ---

func TestCountLabel(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{1, "1 file"},
		{0, "0 files"},
		{2, "2 files"},
		{10, "10 files"},
	}
	for _, c := range cases {
		got := countLabel(c.n)
		if got != c.want {
			t.Errorf("countLabel(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

// --- DefaultTreeViewOptions ---

func TestDefaultTreeViewOptions(t *testing.T) {
	opts := DefaultTreeViewOptions()
	if opts.MaxDepth != 5 {
		t.Errorf("MaxDepth = %d, want 5", opts.MaxDepth)
	}
	if opts.ShowHidden {
		t.Error("ShowHidden should default to false")
	}
	if opts.MaxEntries != 500 {
		t.Errorf("MaxEntries = %d, want 500", opts.MaxEntries)
	}
	if len(opts.IgnoreGlobs) == 0 {
		t.Error("IgnoreGlobs should not be empty")
	}
	// sanity-check a few well known ignored dirs
	wantIgnored := map[string]bool{
		"node_modules": true,
		".git":         true,
		"vendor":       true,
		"dist":         true,
	}
	got := make(map[string]bool)
	for _, g := range opts.IgnoreGlobs {
		got[g] = true
	}
	for k := range wantIgnored {
		if !got[k] {
			t.Errorf("expected %q in IgnoreGlobs", k)
		}
	}
}

// --- RenderDirectoryTree ---

func TestRenderDirectoryTree_Basic(t *testing.T) {
	root := t.TempDir()

	// create a small directory structure
	mustWrite(t, filepath.Join(root, "main.go"), "package main")
	mustWrite(t, filepath.Join(root, "README.md"), "# hi")
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "pkg", "util.go"), "package pkg")

	out, err := RenderDirectoryTree(root, DefaultTreeViewOptions())
	if err != nil {
		t.Fatalf("RenderDirectoryTree error: %v", err)
	}
	if out == "" || out == "(empty)" {
		t.Fatalf("expected non-empty tree, got %q", out)
	}
	for _, want := range []string{"main.go", "README.md", "util.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\noutput:\n%s", want, out)
		}
	}
}

func TestRenderDirectoryTree_Empty(t *testing.T) {
	root := t.TempDir()
	// no files in root
	out, err := RenderDirectoryTree(root, DefaultTreeViewOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "(empty)" {
		t.Errorf("expected (empty), got %q", out)
	}
}

func TestRenderDirectoryTree_IgnoresIgnored(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "keep.go"), "x")
	if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "node_modules", "skipped.js"), "x")

	out, err := RenderDirectoryTree(root, DefaultTreeViewOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "keep.go") {
		t.Errorf("expected keep.go in output: %s", out)
	}
	if strings.Contains(out, "skipped.js") {
		t.Errorf("node_modules content should be ignored: %s", out)
	}
}

func TestRenderDirectoryTree_HiddenFiles(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "visible.go"), "x")
	mustWrite(t, filepath.Join(root, ".secret"), "x")

	// default: hidden files skipped
	out, err := RenderDirectoryTree(root, DefaultTreeViewOptions())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, ".secret") {
		t.Errorf("hidden files should be skipped by default: %s", out)
	}
	if !strings.Contains(out, "visible.go") {
		t.Errorf("visible.go missing: %s", out)
	}

	// with ShowHidden: included
	opts := DefaultTreeViewOptions()
	opts.ShowHidden = true
	out2, err := RenderDirectoryTree(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2, ".secret") {
		t.Errorf("hidden files should be shown when ShowHidden=true: %s", out2)
	}
}

func TestRenderDirectoryTree_NonExistent(t *testing.T) {
	// walkDirectory swallows walk errors and returns no paths,
	// so a non-existent root produces "(empty)" with no error.
	out, err := RenderDirectoryTree("/non/existent/path/xyz123", DefaultTreeViewOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "(empty)" {
		t.Errorf("expected (empty) for non-existent path, got %q", out)
	}
}

// --- RenderPackageTree ---

func TestRenderPackageTree_Basic(t *testing.T) {
	out := RenderPackageTree("/some/base/mypkg", []string{
		"a.go",
		"b.go",
		"sub/c.go",
	})
	if out == "" || out == "(empty)" {
		t.Fatalf("expected non-empty render, got %q", out)
	}
	for _, want := range []string{"mypkg", "a.go", "b.go", "c.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\noutput:\n%s", want, out)
		}
	}
}

func TestRenderPackageTree_Empty(t *testing.T) {
	out := RenderPackageTree("/x", nil)
	if out != "(empty)" {
		t.Errorf("expected (empty), got %q", out)
	}
}

// --- RenderFileGroups ---

func TestRenderFileGroups_Basic(t *testing.T) {
	groups := map[string][]string{
		"source": {"main.go", "util.go"},
		"tests":  {"main_test.go"},
		"docs":   {"README.md"},
	}
	out := RenderFileGroups(groups)
	if out == "" {
		t.Fatal("expected non-empty output")
	}
	for _, want := range []string{
		"Project", "source", "tests", "docs",
		"main.go", "util.go", "main_test.go", "README.md",
		"2 files", "1 file",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\noutput:\n%s", want, out)
		}
	}
}

func TestRenderFileGroups_EmptyMap(t *testing.T) {
	out := RenderFileGroups(map[string][]string{})
	if !strings.Contains(out, "Project") {
		t.Errorf("expected Project label even when empty, got %q", out)
	}
}

// --- LoadProjectFile / LoadUserFile / InjectProjectContext ---

func TestLoadProjectFile_Found(t *testing.T) {
	root := t.TempDir()
	content := "# proj\nhello"
	mustWrite(t, filepath.Join(root, ".polypod.md"), content)

	withCwd(t, root, func() {
		cfg, err := LoadProjectFile()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg == nil {
			t.Fatal("expected a config, got nil")
		}
		if cfg.Content != content {
			t.Errorf("Content = %q, want %q", cfg.Content, content)
		}
		if !strings.HasSuffix(cfg.FilePath, ".polypod.md") {
			t.Errorf("FilePath should end with .polypod.md, got %q", cfg.FilePath)
		}
	})
}

func TestLoadProjectFile_NotFound(t *testing.T) {
	// build a deep dir tree with NO .polypod.md anywhere within search depth
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0755); err != nil {
		t.Fatal(err)
	}
	withCwd(t, deep, func() {
		// We can't guarantee that ancestors of TempDir don't have a .polypod.md,
		// but in CI/test env this is overwhelmingly true. Just ensure no error
		// and accept either nil OR a non-nil config from outside the temp dir.
		cfg, err := LoadProjectFile()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg != nil && strings.HasPrefix(cfg.FilePath, root) {
			t.Errorf("did not expect a config inside temp tree, got %q", cfg.FilePath)
		}
	})
}

func TestCreateDefaultProjectFile(t *testing.T) {
	root := t.TempDir()
	if err := CreateDefaultProjectFile(root); err != nil {
		t.Fatalf("CreateDefaultProjectFile error: %v", err)
	}
	path := filepath.Join(root, ".polypod.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if !strings.Contains(string(data), "Polypod") {
		t.Errorf("template missing expected content, got: %s", data)
	}

	// second call should fail because file already exists
	if err := CreateDefaultProjectFile(root); err == nil {
		t.Error("expected error when file already exists")
	}
}

func TestInjectProjectContext_WithProjectFile(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".polypod.md"), "PROJECT_RULES")

	withCwd(t, root, func() {
		out := InjectProjectContext()
		if !strings.Contains(out, "PROJECT_RULES") {
			t.Errorf("expected project content in injected context, got %q", out)
		}
		if !strings.Contains(out, "Instrucoes do projeto") {
			t.Errorf("expected project header in injected context, got %q", out)
		}
	})
}

// --- helpers ---

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func withCwd(t *testing.T, dir string, fn func()) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(orig)
	}()
	fn()
}
