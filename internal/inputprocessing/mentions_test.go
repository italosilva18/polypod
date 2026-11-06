package inputprocessing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractMentions_Basic(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string // expected Path values, in order
	}{
		{
			name: "single file at start",
			in:   "@main.go",
			want: []string{"main.go"},
		},
		{
			name: "file after text",
			in:   "explain @main.go please",
			want: []string{"main.go"},
		},
		{
			name: "multiple files",
			in:   "@main.go and @internal/config/config.go",
			want: []string{"main.go", "internal/config/config.go"},
		},
		{
			name: "directory mention",
			in:   "look in @src/ for clues",
			want: []string{"src/"},
		},
		{
			name: "email is ignored",
			in:   "contact me at foo@bar.com for details",
			want: nil,
		},
		{
			name: "agent invocation is ignored",
			in:   "use @agent(task) to run it",
			want: nil,
		},
		{
			name: "trailing sentence punctuation stripped",
			in:   "read @main.go.",
			want: []string{"main.go"},
		},
		{
			name: "closing paren not included",
			in:   "(see @main.go)",
			want: []string{"main.go"},
		},
		{
			name: "mention after newline",
			in:   "line1\n@main.go",
			want: []string{"main.go"},
		},
		{
			name: "no mentions",
			in:   "just a plain sentence",
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractMentions(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d mentions, want %d: %+v", len(got), len(tc.want), got)
			}
			for i, m := range got {
				if m.Path != tc.want[i] {
					t.Errorf("mention %d: got %q, want %q", i, m.Path, tc.want[i])
				}
			}
		})
	}
}

func TestMatchExtension(t *testing.T) {
	cases := map[string]string{
		"main.go":              "go",
		"script.py":            "python",
		"app.ts":               "typescript",
		"component.tsx":        "tsx",
		"page.jsx":             "jsx",
		"lib.rs":               "rust",
		"Makefile":             "makefile",
		"Dockerfile":           "dockerfile",
		"config.yaml":          "yaml",
		"data.json":            "json",
		"README.md":            "markdown",
		"unknown.xyz":          "",
		"no-extension":         "",
		"path/to/file.sh":      "bash",
		"deep/nested/file.cpp": "cpp",
	}
	for path, want := range cases {
		if got := MatchExtension(path); got != want {
			t.Errorf("MatchExtension(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestResolveMention_FileAndDir(t *testing.T) {
	dir := t.TempDir()
	// Create a sample file and a subdirectory with some content.
	filePath := filepath.Join(dir, "hello.go")
	content := "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(dir, "sub")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}

	// File mention.
	m := Mention{Raw: "@hello.go", Path: "hello.go"}
	if err := ResolveMention(&m, dir, nil); err != nil {
		t.Fatalf("ResolveMention file: %v", err)
	}
	if m.Type != MentionFile {
		t.Errorf("got type %v, want MentionFile", m.Type)
	}
	if !strings.Contains(m.Content, "package main") {
		t.Errorf("content missing file body: %q", m.Content)
	}

	// Directory mention.
	md := Mention{Raw: "@sub/", Path: "sub/"}
	if err := ResolveMention(&md, dir, nil); err != nil {
		t.Fatalf("ResolveMention dir: %v", err)
	}
	if md.Type != MentionDirectory {
		t.Errorf("got type %v, want MentionDirectory", md.Type)
	}
	if !strings.Contains(md.Content, "a.txt") {
		t.Errorf("dir listing missing entry: %q", md.Content)
	}

	// Missing file.
	mm := Mention{Raw: "@nope.go", Path: "nope.go"}
	if err := ResolveMention(&mm, dir, nil); err == nil {
		t.Errorf("expected error for missing file")
	}
	if mm.Type != MentionInvalid {
		t.Errorf("got type %v, want MentionInvalid", mm.Type)
	}
	if !strings.Contains(mm.Content, "file not found") {
		t.Errorf("missing-file content: %q", mm.Content)
	}
}

func TestExpandMentions_AppendsAppendix(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "hello.go")
	if err := os.WriteFile(filePath, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	text := "explain @hello.go please"
	expanded, mentions := ExpandMentions(text, dir, nil)

	if len(mentions) != 1 {
		t.Fatalf("got %d mentions, want 1", len(mentions))
	}
	if !strings.Contains(expanded, text) {
		t.Errorf("expanded text dropped original: %q", expanded)
	}
	if !strings.Contains(expanded, "---") {
		t.Errorf("expanded text missing separator: %q", expanded)
	}
	if !strings.Contains(expanded, "[Conteudo do arquivo hello.go]") {
		t.Errorf("expanded text missing appendix header: %q", expanded)
	}
	if !strings.Contains(expanded, "```go") {
		t.Errorf("expanded text missing go fence: %q", expanded)
	}
	if !strings.Contains(expanded, "package main") {
		t.Errorf("expanded text missing file body: %q", expanded)
	}
}

func TestExpandMentions_NoMentionsIsNoop(t *testing.T) {
	text := "just a plain sentence"
	expanded, mentions := ExpandMentions(text, "/nonexistent", nil)
	if expanded != text {
		t.Errorf("expected no-op, got %q", expanded)
	}
	if mentions != nil {
		t.Errorf("expected no mentions, got %+v", mentions)
	}
}

func TestExpandMentions_Truncation(t *testing.T) {
	dir := t.TempDir()
	// Create a file with > maxInlineFileBytes and > maxInlineFileLines.
	var sb strings.Builder
	for i := 0; i < maxInlineFileLines+50; i++ {
		sb.WriteString("line of filler text goes here\n")
	}
	big := sb.String()
	if len(big) <= maxInlineFileBytes {
		// Pad until we exceed the byte limit too.
		for len(big) <= maxInlineFileBytes {
			big += "padding padding padding padding\n"
		}
	}
	path := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(path, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}

	_, mentions := ExpandMentions("see @big.txt", dir, nil)
	if len(mentions) != 1 {
		t.Fatalf("got %d mentions, want 1", len(mentions))
	}
	if !strings.Contains(mentions[0].Content, "truncated") {
		t.Errorf("expected truncation marker, got: %q", mentions[0].Content)
	}
}

func TestExpandMentions_BinaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bin.dat")
	data := []byte{0x00, 0x01, 0x02, 0x03, 0x00, 0xff}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	_, mentions := ExpandMentions("@bin.dat", dir, nil)
	if len(mentions) != 1 {
		t.Fatalf("got %d mentions, want 1", len(mentions))
	}
	if mentions[0].Content != "(binary file)" {
		t.Errorf("expected binary-file marker, got: %q", mentions[0].Content)
	}
}

func TestExpandMentions_ExtraDirs(t *testing.T) {
	primary := t.TempDir()
	secondary := t.TempDir()
	// Put the file in secondary only.
	path := filepath.Join(secondary, "other.go")
	if err := os.WriteFile(path, []byte("package other\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, mentions := ExpandMentions("@other.go", primary, []string{secondary})
	if len(mentions) != 1 {
		t.Fatalf("got %d mentions, want 1", len(mentions))
	}
	if mentions[0].Type != MentionFile {
		t.Errorf("got type %v, want MentionFile", mentions[0].Type)
	}
	if !strings.Contains(mentions[0].Content, "package other") {
		t.Errorf("expected file content from extraDir, got: %q", mentions[0].Content)
	}
}
