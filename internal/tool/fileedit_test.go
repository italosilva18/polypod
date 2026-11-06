package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/types"
)

// ----------------------------------------------------------------------------
// Test helpers
// ----------------------------------------------------------------------------

// setupFileEdit creates a Registry with the file_edit/file_write/undo_edit
// tools registered for the given working directory.
func setupFileEdit(t *testing.T, workingDir string) *Registry {
	t.Helper()
	reg := NewRegistry()
	RegisterFileEditTools(reg, workingDir)
	return reg
}

// mustGet fetches a tool by name or fails the test.
func mustGetTool(t *testing.T, reg *Registry, name string) types.Tool {
	t.Helper()
	tool, ok := reg.Get(name)
	if !ok {
		t.Fatalf("tool %q not registered", name)
	}
	return tool
}

// writeTempFile creates a file with the given content in dir.
func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// readFile reads a file or fails the test.
func readFileStr(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// ----------------------------------------------------------------------------
// Basic edit behaviour
// ----------------------------------------------------------------------------

func TestFileEdit_BasicEdit(t *testing.T) {
	dir := t.TempDir()
	path := writeTempFile(t, dir, "test.txt", "hello world\nfoo bar\nbaz qux\n")

	reg := setupFileEdit(t, dir)
	edit := mustGetTool(t, reg, "file_edit")

	result, err := edit.Execute(types.ToolContext{WorkingDir: dir}, map[string]any{
		"file_path":  "test.txt",
		"old_string": "foo bar",
		"new_string": "FOO BAR",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected IsError: %s", result.Content)
	}

	got := readFileStr(t, path)
	if !strings.Contains(got, "FOO BAR") {
		t.Errorf("file not updated: %q", got)
	}
	if strings.Contains(got, "foo bar") {
		t.Errorf("old content still present: %q", got)
	}
	if !strings.Contains(got, "hello world") || !strings.Contains(got, "baz qux") {
		t.Errorf("surrounding content altered: %q", got)
	}
}

func TestFileEdit_ExactMatchSucceeds(t *testing.T) {
	dir := t.TempDir()
	path := writeTempFile(t, dir, "a.txt", "only one line here\n")

	reg := setupFileEdit(t, dir)
	edit := mustGetTool(t, reg, "file_edit")

	result, err := edit.Execute(types.ToolContext{WorkingDir: dir}, map[string]any{
		"file_path":  "a.txt",
		"old_string": "only one line here",
		"new_string": "only one line there",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("IsError: %s", result.Content)
	}
	if got := readFileStr(t, path); !strings.Contains(got, "only one line there") {
		t.Errorf("replacement missing: %q", got)
	}
}

func TestFileEdit_NoMatchReturnsError(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "a.txt", "this is a file\n")

	reg := setupFileEdit(t, dir)
	edit := mustGetTool(t, reg, "file_edit")

	result, err := edit.Execute(types.ToolContext{WorkingDir: dir}, map[string]any{
		"file_path":  "a.txt",
		"old_string": "nonexistent",
		"new_string": "replacement",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "not found") {
		t.Errorf("expected 'not found' message, got: %s", result.Content)
	}
}

func TestFileEdit_MultipleMatchesReturnsError(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "a.txt", "dup\ndup\ndup\n")

	reg := setupFileEdit(t, dir)
	edit := mustGetTool(t, reg, "file_edit")

	result, err := edit.Execute(types.ToolContext{WorkingDir: dir}, map[string]any{
		"file_path":  "a.txt",
		"old_string": "dup",
		"new_string": "DUP",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError for multiple matches, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "3") {
		t.Errorf("expected match count in error, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "replace_all") {
		t.Errorf("expected replace_all hint, got: %s", result.Content)
	}
}

func TestFileEdit_ReplaceAllTrue(t *testing.T) {
	dir := t.TempDir()
	path := writeTempFile(t, dir, "a.txt", "dup\ndup\ndup\nend\n")

	reg := setupFileEdit(t, dir)
	edit := mustGetTool(t, reg, "file_edit")

	result, err := edit.Execute(types.ToolContext{WorkingDir: dir}, map[string]any{
		"file_path":   "a.txt",
		"old_string":  "dup",
		"new_string":  "DUP",
		"replace_all": true,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("IsError: %s", result.Content)
	}

	got := readFileStr(t, path)
	if strings.Contains(got, "dup") {
		t.Errorf("lowercase dup still present: %q", got)
	}
	if c := strings.Count(got, "DUP"); c != 3 {
		t.Errorf("expected 3 DUP, got %d: %q", c, got)
	}
	if !strings.Contains(result.Content, "Replaced 3 occurrences") {
		t.Errorf("expected replacement count message, got: %s", result.Content)
	}
}

func TestFileEdit_PreservesIndentation(t *testing.T) {
	dir := t.TempDir()
	content := "func main() {\n\tfmt.Println(\"hi\")\n\treturn\n}\n"
	path := writeTempFile(t, dir, "main.go", content)

	reg := setupFileEdit(t, dir)
	edit := mustGetTool(t, reg, "file_edit")

	result, err := edit.Execute(types.ToolContext{WorkingDir: dir}, map[string]any{
		"file_path":  "main.go",
		"old_string": "\tfmt.Println(\"hi\")",
		"new_string": "\tfmt.Println(\"bye\")",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("IsError: %s", result.Content)
	}

	got := readFileStr(t, path)
	if !strings.Contains(got, "\tfmt.Println(\"bye\")") {
		t.Errorf("tab indentation not preserved: %q", got)
	}
	if strings.Contains(got, "fmt.Println(\"hi\")") {
		t.Errorf("old line still present: %q", got)
	}
}

func TestFileEdit_MiddleOfMultilineContent(t *testing.T) {
	dir := t.TempDir()
	content := "line1\nline2\nMIDDLE\nline4\nline5\n"
	path := writeTempFile(t, dir, "multi.txt", content)

	reg := setupFileEdit(t, dir)
	edit := mustGetTool(t, reg, "file_edit")

	_, err := edit.Execute(types.ToolContext{WorkingDir: dir}, map[string]any{
		"file_path":  "multi.txt",
		"old_string": "line2\nMIDDLE\nline4",
		"new_string": "line2\nREPLACED\nline4",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := readFileStr(t, path)
	if !strings.Contains(got, "REPLACED") {
		t.Errorf("middle not replaced: %q", got)
	}
	if strings.Contains(got, "MIDDLE") {
		t.Errorf("old middle still there: %q", got)
	}
	if !strings.Contains(got, "line1") || !strings.Contains(got, "line5") {
		t.Errorf("boundary lines altered: %q", got)
	}
}

// ----------------------------------------------------------------------------
// Diff output
// ----------------------------------------------------------------------------

func TestFileEdit_DiffOutputContainsUnifiedFormat(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "d.txt", "alpha\nbeta\ngamma\n")

	reg := setupFileEdit(t, dir)
	edit := mustGetTool(t, reg, "file_edit")

	result, err := edit.Execute(types.ToolContext{WorkingDir: dir}, map[string]any{
		"file_path":  "d.txt",
		"old_string": "beta",
		"new_string": "BETA",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("IsError: %s", result.Content)
	}

	content := result.Content
	if !strings.Contains(content, "---") || !strings.Contains(content, "+++") {
		t.Errorf("unified diff headers missing: %s", content)
	}
	if !strings.Contains(content, "@@") {
		t.Errorf("hunk header missing: %s", content)
	}
	if !strings.Contains(content, "-beta") {
		t.Errorf("removed line marker missing: %s", content)
	}
	if !strings.Contains(content, "+BETA") {
		t.Errorf("added line marker missing: %s", content)
	}
	if !strings.Contains(content, "d.txt") {
		t.Errorf("file path missing from diff: %s", content)
	}
}

// ----------------------------------------------------------------------------
// Edge cases
// ----------------------------------------------------------------------------

func TestFileEdit_MissingFileReturnsError(t *testing.T) {
	dir := t.TempDir()
	reg := setupFileEdit(t, dir)
	edit := mustGetTool(t, reg, "file_edit")

	result, err := edit.Execute(types.ToolContext{WorkingDir: dir}, map[string]any{
		"file_path":  "does_not_exist.txt",
		"old_string": "something",
		"new_string": "else",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError for missing file, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "File not found") {
		t.Errorf("expected 'File not found' message, got: %s", result.Content)
	}
}

func TestFileEdit_EmptyOldStringOnExistingFileRejected(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "full.txt", "not empty at all\n")

	reg := setupFileEdit(t, dir)
	edit := mustGetTool(t, reg, "file_edit")

	result, err := edit.Execute(types.ToolContext{WorkingDir: dir}, map[string]any{
		"file_path":  "full.txt",
		"old_string": "",
		"new_string": "whatever",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError for empty old_string on existing file, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "empty old_string") {
		t.Errorf("expected explanation about empty old_string, got: %s", result.Content)
	}
}

func TestFileEdit_IdenticalStringsIsError(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "a.txt", "hello\n")

	reg := setupFileEdit(t, dir)
	edit := mustGetTool(t, reg, "file_edit")

	result, err := edit.Execute(types.ToolContext{WorkingDir: dir}, map[string]any{
		"file_path":  "a.txt",
		"old_string": "hello",
		"new_string": "hello",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError for identical strings, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "identical") {
		t.Errorf("expected 'identical' in message, got: %s", result.Content)
	}
}

func TestFileEdit_LargeFile(t *testing.T) {
	dir := t.TempDir()

	// Build content >1MB: 20,000 lines of ~60 bytes each ≈ 1.2MB
	var sb strings.Builder
	for i := 0; i < 20000; i++ {
		sb.WriteString("this is line number with some padding content here\n")
	}
	// Insert a unique marker we'll edit
	content := sb.String() + "UNIQUE_MARKER_LINE\ntail line\n"
	if len(content) < 1_000_000 {
		t.Fatalf("test content not large enough: %d bytes", len(content))
	}

	path := writeTempFile(t, dir, "big.txt", content)

	reg := setupFileEdit(t, dir)
	edit := mustGetTool(t, reg, "file_edit")

	result, err := edit.Execute(types.ToolContext{WorkingDir: dir}, map[string]any{
		"file_path":  "big.txt",
		"old_string": "UNIQUE_MARKER_LINE",
		"new_string": "REPLACED_MARKER",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("IsError: %s", result.Content)
	}

	got := readFileStr(t, path)
	if !strings.Contains(got, "REPLACED_MARKER") {
		t.Error("large-file marker not replaced")
	}
	if strings.Contains(got, "UNIQUE_MARKER_LINE") {
		t.Error("old marker still present in large file")
	}
}

// ----------------------------------------------------------------------------
// undo_edit
// ----------------------------------------------------------------------------

func TestUndoEdit_RestoresOriginal(t *testing.T) {
	dir := t.TempDir()
	original := "original content\nsecond line\n"
	path := writeTempFile(t, dir, "u.txt", original)

	reg := setupFileEdit(t, dir)
	edit := mustGetTool(t, reg, "file_edit")
	undo := mustGetTool(t, reg, "undo_edit")

	// Make an edit first.
	if _, err := edit.Execute(types.ToolContext{WorkingDir: dir}, map[string]any{
		"file_path":  "u.txt",
		"old_string": "original content",
		"new_string": "changed content",
	}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if got := readFileStr(t, path); !strings.Contains(got, "changed content") {
		t.Fatalf("edit did not apply: %q", got)
	}

	// Undo.
	result, err := undo.Execute(types.ToolContext{WorkingDir: dir}, map[string]any{
		"file_path": "u.txt",
	})
	if err != nil {
		t.Fatalf("undo Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("undo IsError: %s", result.Content)
	}

	got := readFileStr(t, path)
	if got != original {
		t.Errorf("undo did not restore original.\nwant: %q\ngot : %q", original, got)
	}
}

func TestUndoEdit_NoPriorEditReturnsError(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "u.txt", "some content\n")

	reg := setupFileEdit(t, dir)
	undo := mustGetTool(t, reg, "undo_edit")

	result, err := undo.Execute(types.ToolContext{WorkingDir: dir}, map[string]any{
		"file_path": "u.txt",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError when no backup exists, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "No edit history") {
		t.Errorf("expected 'No edit history' message, got: %s", result.Content)
	}
}

func TestUndoEdit_MultipleUndosStepBack(t *testing.T) {
	dir := t.TempDir()
	path := writeTempFile(t, dir, "u.txt", "v0\n")

	reg := setupFileEdit(t, dir)
	edit := mustGetTool(t, reg, "file_edit")
	undo := mustGetTool(t, reg, "undo_edit")

	// v0 -> v1
	if _, err := edit.Execute(types.ToolContext{WorkingDir: dir}, map[string]any{
		"file_path":  "u.txt",
		"old_string": "v0",
		"new_string": "v1",
	}); err != nil {
		t.Fatalf("edit1: %v", err)
	}
	// v1 -> v2
	if _, err := edit.Execute(types.ToolContext{WorkingDir: dir}, map[string]any{
		"file_path":  "u.txt",
		"old_string": "v1",
		"new_string": "v2",
	}); err != nil {
		t.Fatalf("edit2: %v", err)
	}
	if got := strings.TrimSpace(readFileStr(t, path)); got != "v2" {
		t.Fatalf("want v2, got %q", got)
	}

	// Undo once -> v1
	if _, err := undo.Execute(types.ToolContext{WorkingDir: dir}, map[string]any{
		"file_path": "u.txt",
	}); err != nil {
		t.Fatalf("undo1: %v", err)
	}
	if got := strings.TrimSpace(readFileStr(t, path)); got != "v1" {
		t.Errorf("after 1st undo want v1, got %q", got)
	}

	// Undo again -> v0
	if _, err := undo.Execute(types.ToolContext{WorkingDir: dir}, map[string]any{
		"file_path": "u.txt",
	}); err != nil {
		t.Fatalf("undo2: %v", err)
	}
	if got := strings.TrimSpace(readFileStr(t, path)); got != "v0" {
		t.Errorf("after 2nd undo want v0, got %q", got)
	}

	// Third undo should fail (history empty).
	result, err := undo.Execute(types.ToolContext{WorkingDir: dir}, map[string]any{
		"file_path": "u.txt",
	})
	if err != nil {
		t.Fatalf("undo3 Execute: %v", err)
	}
	if !result.IsError {
		t.Errorf("expected error on empty history, got: %s", result.Content)
	}
}

// ----------------------------------------------------------------------------
// generateUnifiedDiff helper
// ----------------------------------------------------------------------------

func TestGenerateUnifiedDiff_AddLine(t *testing.T) {
	old := "a\nb\nc\n"
	new_ := "a\nb\nc\nd\n"
	diff := generateUnifiedDiff("f.txt", old, new_)
	if diff == "" {
		t.Fatal("expected non-empty diff")
	}
	if !strings.Contains(diff, "--- a/f.txt") {
		t.Errorf("missing old header: %s", diff)
	}
	if !strings.Contains(diff, "+++ b/f.txt") {
		t.Errorf("missing new header: %s", diff)
	}
	if !strings.Contains(diff, "+d") {
		t.Errorf("missing added line '+d': %s", diff)
	}
}

func TestGenerateUnifiedDiff_RemoveLine(t *testing.T) {
	old := "a\nb\nc\n"
	new_ := "a\nc\n"
	diff := generateUnifiedDiff("f.txt", old, new_)
	if !strings.Contains(diff, "-b") {
		t.Errorf("missing removed line '-b': %s", diff)
	}
	if !strings.Contains(diff, "@@") {
		t.Errorf("missing hunk header: %s", diff)
	}
}

func TestGenerateUnifiedDiff_ModifyLine(t *testing.T) {
	old := "a\nb\nc\n"
	new_ := "a\nB\nc\n"
	diff := generateUnifiedDiff("f.txt", old, new_)
	if !strings.Contains(diff, "-b") {
		t.Errorf("missing '-b': %s", diff)
	}
	if !strings.Contains(diff, "+B") {
		t.Errorf("missing '+B': %s", diff)
	}
}

func TestGenerateUnifiedDiff_EmptyInputs(t *testing.T) {
	// Both empty → no diff.
	if diff := generateUnifiedDiff("f.txt", "", ""); diff != "" {
		t.Errorf("expected empty diff for identical empty inputs, got: %q", diff)
	}

	// Empty old → pure insertions.
	if diff := generateUnifiedDiff("f.txt", "", "new\nlines\n"); diff == "" {
		t.Error("expected non-empty diff for empty→content")
	} else if !strings.Contains(diff, "+new") || !strings.Contains(diff, "+lines") {
		t.Errorf("expected insertions in diff, got: %s", diff)
	}

	// Empty new → pure deletions.
	if diff := generateUnifiedDiff("f.txt", "gone\nforever\n", ""); diff == "" {
		t.Error("expected non-empty diff for content→empty")
	} else if !strings.Contains(diff, "-gone") || !strings.Contains(diff, "-forever") {
		t.Errorf("expected deletions in diff, got: %s", diff)
	}
}

func TestGenerateUnifiedDiff_IdenticalInputsProducesNoDiff(t *testing.T) {
	content := "same\ncontent\nhere\n"
	if diff := generateUnifiedDiff("f.txt", content, content); diff != "" {
		t.Errorf("expected empty diff for identical content, got: %s", diff)
	}
}

func TestGenerateUnifiedDiff_LargeDiff(t *testing.T) {
	var oldSB, newSB strings.Builder
	for i := 0; i < 500; i++ {
		oldSB.WriteString("old-line-entry\n")
		newSB.WriteString("new-line-entry\n")
	}
	diff := generateUnifiedDiff("big.txt", oldSB.String(), newSB.String())
	if diff == "" {
		t.Fatal("expected non-empty diff for large change")
	}
	if !strings.Contains(diff, "-old-line-entry") {
		t.Error("expected removed markers in large diff")
	}
	if !strings.Contains(diff, "+new-line-entry") {
		t.Error("expected added markers in large diff")
	}
}
