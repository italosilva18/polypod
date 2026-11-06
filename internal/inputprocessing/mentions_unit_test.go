package inputprocessing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExtractMentions_NoMentions verifies that plain text without any
// '@' tokens produces no mentions.
func TestExtractMentions_NoMentions(t *testing.T) {
	got := ExtractMentions("no mentions here")
	if len(got) != 0 {
		t.Errorf("ExtractMentions(plain) = %+v; want empty", got)
	}
}

// TestExtractMentions_SingleFile verifies a single @file token is
// extracted correctly.
func TestExtractMentions_SingleFile(t *testing.T) {
	got := ExtractMentions("read @main.go")
	if len(got) != 1 {
		t.Fatalf("got %d mentions; want 1: %+v", len(got), got)
	}
	if got[0].Path != "main.go" {
		t.Errorf("Path = %q; want %q", got[0].Path, "main.go")
	}
	if got[0].Raw != "@main.go" {
		t.Errorf("Raw = %q; want %q", got[0].Raw, "@main.go")
	}
	if got[0].Type != MentionFile {
		t.Errorf("Type = %v; want MentionFile", got[0].Type)
	}
}

// TestExtractMentions_TwoFiles verifies that multiple mentions in one
// sentence are all extracted in order.
func TestExtractMentions_TwoFiles(t *testing.T) {
	got := ExtractMentions("read @a.go and @b.go")
	if len(got) != 2 {
		t.Fatalf("got %d mentions; want 2: %+v", len(got), got)
	}
	if got[0].Path != "a.go" {
		t.Errorf("[0].Path = %q; want a.go", got[0].Path)
	}
	if got[1].Path != "b.go" {
		t.Errorf("[1].Path = %q; want b.go", got[1].Path)
	}
}

// TestExtractMentions_EmailIgnored verifies that an email-shaped token
// (no leading whitespace before '@') is not treated as a mention.
func TestExtractMentions_EmailIgnored(t *testing.T) {
	got := ExtractMentions("email@example.com")
	if len(got) != 0 {
		t.Errorf("email should not produce mentions; got %+v", got)
	}
}

// TestExtractMentions_AgentInvocationIgnored verifies that the
// "@name(args)" agent-call form is skipped.
func TestExtractMentions_AgentInvocationIgnored(t *testing.T) {
	got := ExtractMentions("@agent(task)")
	if len(got) != 0 {
		t.Errorf("agent invocation should be ignored; got %+v", got)
	}
}

// TestExtractMentions_NestedPath verifies that a mention containing
// slashes is extracted as a single path.
func TestExtractMentions_NestedPath(t *testing.T) {
	got := ExtractMentions("use @src/config.go now")
	if len(got) != 1 {
		t.Fatalf("got %d mentions; want 1: %+v", len(got), got)
	}
	if got[0].Path != "src/config.go" {
		t.Errorf("Path = %q; want src/config.go", got[0].Path)
	}
}

// TestResolveMention_ExistingFile verifies that an existing file is
// read and its contents populate the Content field.
func TestResolveMention_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	body := "package test\n\nfunc Foo() {}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	m := &Mention{Raw: "@test.go", Path: "test.go", Type: MentionFile}
	if err := ResolveMention(m, dir, nil); err != nil {
		t.Fatalf("ResolveMention: %v", err)
	}
	if m.Content == "" {
		t.Error("Content empty after resolve")
	}
	if !strings.Contains(m.Content, "package test") {
		t.Errorf("Content missing body: %q", m.Content)
	}
	if m.Error != "" {
		t.Errorf("Error should be empty on success; got %q", m.Error)
	}
	if m.Type != MentionFile {
		t.Errorf("Type = %v; want MentionFile", m.Type)
	}
}

// TestResolveMention_MissingFile verifies the error path sets both
// Error and a human-readable Content placeholder.
func TestResolveMention_MissingFile(t *testing.T) {
	dir := t.TempDir()
	m := &Mention{Raw: "@ghost.go", Path: "ghost.go", Type: MentionFile}
	err := ResolveMention(m, dir, nil)
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if m.Error == "" {
		t.Error("Error should be set on failure")
	}
	if !strings.Contains(m.Content, "file not found") {
		t.Errorf("Content = %q; want placeholder containing 'file not found'", m.Content)
	}
	if !strings.Contains(m.Content, "ghost.go") {
		t.Errorf("Content should mention the path; got %q", m.Content)
	}
	if m.Type != MentionInvalid {
		t.Errorf("Type = %v; want MentionInvalid", m.Type)
	}
}

// TestResolveMention_BinaryFile verifies binary files yield the
// "(binary file)" placeholder instead of raw bytes.
func TestResolveMention_BinaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blob.bin")
	// NUL byte in first 8KB is the binary heuristic.
	data := []byte{'M', 'Z', 0x00, 0x01, 0x02, 0x00, 0xff, 0xfe}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	m := &Mention{Raw: "@blob.bin", Path: "blob.bin", Type: MentionFile}
	if err := ResolveMention(m, dir, nil); err != nil {
		t.Fatalf("ResolveMention: %v", err)
	}
	if m.Content != "(binary file)" {
		t.Errorf("Content = %q; want %q", m.Content, "(binary file)")
	}
}

// TestExpandMentions_WithExistingFile verifies ExpandMentions returns
// text plus an appendix when a mention resolves successfully.
func TestExpandMentions_WithExistingFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	text := "please check @main.go"
	expanded, mentions := ExpandMentions(text, dir, nil)

	if len(mentions) != 1 {
		t.Fatalf("got %d mentions; want 1", len(mentions))
	}
	if !strings.Contains(expanded, text) {
		t.Errorf("expanded text should contain original; got %q", expanded)
	}
	if !strings.Contains(expanded, "---") {
		t.Errorf("expanded text should contain separator '---'; got %q", expanded)
	}
	if !strings.Contains(expanded, "package main") {
		t.Errorf("expanded text should contain file body; got %q", expanded)
	}
	if len(expanded) <= len(text) {
		t.Errorf("expanded (%d) should be longer than original (%d)", len(expanded), len(text))
	}
}

// TestExpandMentions_NoMentionsUnchanged verifies that text without
// mentions is returned as-is with a nil mention list.
func TestExpandMentions_NoMentionsUnchanged(t *testing.T) {
	text := "hello world, nothing to expand"
	expanded, mentions := ExpandMentions(text, "/tmp", nil)
	if expanded != text {
		t.Errorf("expanded = %q; want unchanged %q", expanded, text)
	}
	if mentions != nil {
		t.Errorf("mentions = %+v; want nil", mentions)
	}
}

// TestFormatExpandedMentions_Empty verifies the empty-list fast path.
func TestFormatExpandedMentions_Empty(t *testing.T) {
	got := FormatExpandedMentions(nil)
	if got != "" {
		t.Errorf("FormatExpandedMentions(nil) = %q; want empty", got)
	}
	got = FormatExpandedMentions([]Mention{})
	if got != "" {
		t.Errorf("FormatExpandedMentions([]) = %q; want empty", got)
	}
}

// TestFormatExpandedMentions_FileContent verifies rendering of a
// resolved file mention includes the header, fence, and body.
func TestFormatExpandedMentions_FileContent(t *testing.T) {
	mentions := []Mention{
		{
			Raw:     "@util.py",
			Path:    "util.py",
			Type:    MentionFile,
			Content: "def foo():\n    return 1\n",
		},
	}
	got := FormatExpandedMentions(mentions)
	if !strings.Contains(got, "util.py") {
		t.Errorf("output missing path; got %q", got)
	}
	if !strings.Contains(got, "def foo()") {
		t.Errorf("output missing file body; got %q", got)
	}
	if !strings.Contains(got, "```python") {
		t.Errorf("output missing python fence; got %q", got)
	}
}

// TestMatchExtension_Common verifies a spread of common languages plus
// filename fallbacks and the unknown-extension case.
func TestMatchExtension_Common(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"foo.go", "go"},
		{"foo.py", "python"},
		{"foo.js", "javascript"},
		{"foo.mjs", "javascript"},
		{"README.md", "markdown"},
		{"notes.markdown", "markdown"},
		{"Dockerfile", "dockerfile"},
		{"Makefile", "makefile"},
		{"unknown.xyz", ""},
		{"noext", ""},
	}
	for _, tc := range cases {
		got := MatchExtension(tc.path)
		if got != tc.want {
			t.Errorf("MatchExtension(%q) = %q; want %q", tc.path, got, tc.want)
		}
	}
}
