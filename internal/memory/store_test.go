package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// FileStore basics
// ---------------------------------------------------------------------------

func TestNewFileStore_Empty(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStore(dir)
	if s == nil {
		t.Fatal("NewFileStore returned nil")
	}

	list, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected empty store, got %d memories", len(list))
	}
}

func TestFileStore_SaveAndGet(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStore(dir)
	ctx := context.Background()

	if err := s.Save(ctx, "lang", "Prefer Go for CLI tools"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Get(ctx, "lang")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Content != "Prefer Go for CLI tools" {
		t.Errorf("Content = %q, want %q", got.Content, "Prefer Go for CLI tools")
	}
	if got.Topic != "lang" {
		t.Errorf("Topic = %q, want %q", got.Topic, "lang")
	}
	if got.ID == 0 {
		t.Error("expected ID to be assigned")
	}
	if got.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
}

func TestFileStore_SaveUpdatesExisting(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStore(dir)
	ctx := context.Background()

	if err := s.Save(ctx, "lang", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, "lang", "v2"); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, "lang")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Content != "v2" {
		t.Errorf("Content = %q, want v2", got.Content)
	}

	// still only one memory
	list, _ := s.List(ctx)
	if len(list) != 1 {
		t.Errorf("expected 1 memory after update, got %d", len(list))
	}
}

func TestFileStore_List(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStore(dir)
	ctx := context.Background()

	topics := []string{"a", "b", "c"}
	for _, tp := range topics {
		if err := s.Save(ctx, tp, "content-"+tp); err != nil {
			t.Fatal(err)
		}
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("expected 3 memories, got %d", len(list))
	}
}

func TestFileStore_Delete(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStore(dir)
	ctx := context.Background()

	_ = s.Save(ctx, "temp", "junk")
	if err := s.Delete(ctx, "temp"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := s.Get(ctx, "temp"); err == nil {
		t.Error("expected error after deletion")
	}

	// Deleting non-existing returns error
	if err := s.Delete(ctx, "nope"); err == nil {
		t.Error("expected error deleting missing memory")
	}
}

func TestFileStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s1 := NewFileStore(dir)
	if err := s1.Save(ctx, "persist", "data"); err != nil {
		t.Fatal(err)
	}

	// new store instance reading the same dir
	s2 := NewFileStore(dir)
	got, err := s2.Get(ctx, "persist")
	if err != nil {
		t.Fatalf("Get after reload: %v", err)
	}
	if got.Content != "data" {
		t.Errorf("Content = %q, want data", got.Content)
	}
}

// ---------------------------------------------------------------------------
// Search / Filter
// ---------------------------------------------------------------------------

func TestFileStore_Search_BySubstring(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStore(dir)
	ctx := context.Background()

	_ = s.Save(ctx, "go", "Prefer tabs over spaces in Go code")
	_ = s.Save(ctx, "py", "Use black formatter for Python")

	results, err := s.Search(ctx, "python")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Topic != "py" {
		t.Errorf("wrong match: %s", results[0].Topic)
	}
}

func TestFileStore_SaveWithMeta_TagsAndPriority(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStore(dir)
	ctx := context.Background()

	mem := Memory{
		Topic:    "architecture",
		Content:  "Use hexagonal architecture",
		Tags:     []string{"design", "important"},
		Priority: 2,
		Source:   "user",
	}
	if err := s.SaveWithMeta(ctx, mem); err != nil {
		t.Fatalf("SaveWithMeta: %v", err)
	}

	got, err := s.Get(ctx, "architecture")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "design" {
		t.Errorf("Tags = %v", got.Tags)
	}
	if got.Priority != 2 {
		t.Errorf("Priority = %d, want 2", got.Priority)
	}
	if got.Source != "user" {
		t.Errorf("Source = %q", got.Source)
	}
}

func TestFileStore_SearchRelevant(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStore(dir)
	ctx := context.Background()

	_ = s.SaveWithMeta(ctx, Memory{
		Topic:   "golang",
		Content: "Use gofmt for formatting golang code",
	})
	_ = s.SaveWithMeta(ctx, Memory{
		Topic:   "unrelated",
		Content: "Weather is nice today",
	})

	results, err := s.SearchRelevant(ctx, "golang formatting", 5, 0.2)
	if err != nil {
		t.Fatalf("SearchRelevant: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one relevant result")
	}
	if results[0].Topic != "golang" {
		t.Errorf("expected top result 'golang', got %q", results[0].Topic)
	}
}

func TestMemory_IsRelevant(t *testing.T) {
	m := Memory{
		Topic:   "testing",
		Content: "Always write unit tests for critical paths",
		Tags:    []string{"quality"},
	}
	relevant, score := m.IsRelevant("testing unit")
	if !relevant {
		t.Error("expected relevant")
	}
	if score <= 0 {
		t.Errorf("score = %v, want > 0", score)
	}

	relevant2, _ := m.IsRelevant("pizza toppings")
	if relevant2 {
		t.Error("unrelated query should not be relevant")
	}
}

// ---------------------------------------------------------------------------
// DetectMemoryFiles
// ---------------------------------------------------------------------------

func TestDetectMemoryFiles_Polypod(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	projectDir := filepath.Join(home, "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	content := "# Project rules\nBe concise.\n"
	path := filepath.Join(projectDir, ".polypod.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	files, err := DetectMemoryFiles(projectDir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	found := false
	for _, f := range files {
		if f.Path == path {
			found = true
			if !strings.Contains(f.Content, "Be concise") {
				t.Error("Content mismatch")
			}
			if f.Type != MemoryProject {
				t.Errorf("Type = %q, want %q", f.Type, MemoryProject)
			}
			if f.Source != SourceProjectRoot {
				t.Errorf("Source = %q, want %q", f.Source, SourceProjectRoot)
			}
		}
	}
	if !found {
		t.Errorf("didn't find .polypod.md in %d detected files", len(files))
	}
}

func TestDetectMemoryFiles_ClaudeAndAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	projectDir := filepath.Join(home, "proj")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	claudePath := filepath.Join(projectDir, "CLAUDE.md")
	agentsPath := filepath.Join(projectDir, "AGENTS.md")
	if err := os.WriteFile(claudePath, []byte("claude content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentsPath, []byte("agents content"), 0644); err != nil {
		t.Fatal(err)
	}

	files, err := DetectMemoryFiles(projectDir)
	if err != nil {
		t.Fatal(err)
	}

	foundClaude := false
	foundAgents := false
	for _, f := range files {
		if f.Path == claudePath {
			foundClaude = true
		}
		if f.Path == agentsPath {
			foundAgents = true
			if f.Type != MemoryAgent {
				t.Errorf("AGENTS.md type = %q, want %q", f.Type, MemoryAgent)
			}
		}
	}
	if !foundClaude {
		t.Error("CLAUDE.md not detected")
	}
	if !foundAgents {
		t.Error("AGENTS.md not detected")
	}
}

func TestDetectMemoryFiles_ParentDirWalk(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	// home/project/.polypod.md
	// home/project/subdir/  <- working dir
	projectDir := filepath.Join(home, "project")
	subDir := filepath.Join(projectDir, "subdir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	parentPath := filepath.Join(projectDir, ".polypod.md")
	if err := os.WriteFile(parentPath, []byte("parent rules"), 0644); err != nil {
		t.Fatal(err)
	}

	files, err := DetectMemoryFiles(subDir)
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, f := range files {
		if f.Path == parentPath {
			found = true
			if f.Source != SourceParentDir {
				t.Errorf("Source = %q, want %q", f.Source, SourceParentDir)
			}
		}
	}
	if !found {
		t.Errorf("parent .polypod.md not detected (found %d files)", len(files))
	}
}

func TestDetectMemoryFiles_EmptyDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	empty := filepath.Join(home, "empty")
	if err := os.MkdirAll(empty, 0755); err != nil {
		t.Fatal(err)
	}

	files, err := DetectMemoryFiles(empty)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected no files, got %d: %v", len(files), files)
	}
}

// ---------------------------------------------------------------------------
// LoadMemoryFile
// ---------------------------------------------------------------------------

func TestLoadMemoryFile_Simple(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".polypod.md")
	content := "# Rules\nLine one\nLine two\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	mf, err := LoadMemoryFile(path)
	if err != nil {
		t.Fatalf("LoadMemoryFile: %v", err)
	}
	if mf.Path != path {
		t.Errorf("Path = %q, want %q", mf.Path, path)
	}
	if !strings.Contains(mf.Content, "Line one") {
		t.Errorf("Content missing expected text: %q", mf.Content)
	}
	if mf.Size == 0 {
		t.Error("expected non-zero Size")
	}
	if mf.Type != MemoryProject {
		t.Errorf("Type = %q, want %q", mf.Type, MemoryProject)
	}
}

func TestLoadMemoryFile_WithImports(t *testing.T) {
	dir := t.TempDir()

	importedPath := filepath.Join(dir, "shared.md")
	if err := os.WriteFile(importedPath, []byte("shared content"), 0644); err != nil {
		t.Fatal(err)
	}

	mainPath := filepath.Join(dir, ".polypod.md")
	mainContent := "# Main\nSome text\n@shared.md\nMore text\n"
	if err := os.WriteFile(mainPath, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	mf, err := LoadMemoryFile(mainPath)
	if err != nil {
		t.Fatalf("LoadMemoryFile: %v", err)
	}
	if !strings.Contains(mf.Content, "shared content") {
		t.Errorf("expected imported content inlined, got: %q", mf.Content)
	}
	if len(mf.Imports) == 0 {
		t.Error("expected Imports to be populated")
	}
}

func TestLoadMemoryFile_Missing(t *testing.T) {
	_, err := LoadMemoryFile("/nonexistent/does_not_exist.md")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

// ---------------------------------------------------------------------------
// CombineMemoryFiles
// ---------------------------------------------------------------------------

func TestCombineMemoryFiles_Multiple(t *testing.T) {
	files := []MemoryFile{
		{
			Path:    "/a/.polypod.md",
			Content: "alpha rules",
			Source:  SourceProjectRoot,
		},
		{
			Path:    "/b/AGENTS.md",
			Content: "beta rules",
			Source:  SourceParentDir,
		},
	}
	out := CombineMemoryFiles(files)
	if !strings.Contains(out, "alpha rules") {
		t.Error("missing alpha content")
	}
	if !strings.Contains(out, "beta rules") {
		t.Error("missing beta content")
	}
	if !strings.Contains(out, "PROJECT MEMORY") {
		t.Error("missing header")
	}
	if !strings.Contains(out, ".polypod.md") {
		t.Error("missing file name header")
	}
}

func TestCombineMemoryFiles_Empty(t *testing.T) {
	out := CombineMemoryFiles(nil)
	if out != "" {
		t.Errorf("expected empty string for nil input, got %q", out)
	}
}

// ---------------------------------------------------------------------------
// Extraction helpers
// ---------------------------------------------------------------------------

func TestExtractDecision(t *testing.T) {
	mem := ExtractDecision("Decidi usar postgres em vez de mysql")
	if mem == nil {
		t.Fatal("expected decision memory")
	}
	if mem.Source != "auto_extracted" {
		t.Errorf("Source = %q", mem.Source)
	}

	if ExtractDecision("just random text") != nil {
		t.Error("expected nil for non-decision text")
	}
}

func TestExtractPattern(t *testing.T) {
	mem := ExtractPattern("Importante: sempre use context.Context como primeiro parametro")
	if mem == nil {
		t.Fatal("expected pattern memory")
	}
	if len(mem.Tags) == 0 {
		t.Error("expected tags on extracted pattern")
	}
}
