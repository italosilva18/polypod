package mentions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMentionPattern(t *testing.T) {
	tests := []struct {
		input string
		want  int // number of matches
	}{
		{"revise @internal/router/router.go", 1},
		{"compare @go.mod com @go.sum", 2},
		{"sem mention aqui", 0},
		{"veja @src/", 1},
		{"@file.go e @other.py", 2},
	}

	for _, tt := range tests {
		matches := MentionPattern.FindAllString(tt.input, -1)
		if len(matches) != tt.want {
			t.Errorf("MentionPattern(%q) found %d matches, want %d", tt.input, len(matches), tt.want)
		}
	}
}

func TestExpandMentionsLineRanges(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte("one\ntwo\nthree\nfour"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, input, want, absent string
	}{
		{"range", "Read @sample.go#2-3", "two\nthree", "one\n"},
		{"single", "Read @sample.go#4", "four", "three\n"},
		{"invalid", "Read @sample.go#4-2", "Intervalo de linhas inválido", "[Arquivo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := ExpandMentions(tc.input, root)
			if !strings.Contains(got, tc.want) || strings.Contains(got, tc.absent) {
				t.Fatalf("unexpected expansion: %q", got)
			}
		})
	}
}

func TestExpandMentionsDoesNotFollowOutsideSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "private.go")
	if err := os.WriteFile(outside, []byte("SECRET_CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.go")); err != nil {
		t.Fatal(err)
	}
	got, files := ExpandMentions("Read @link.go", root)
	if strings.Contains(got, "SECRET_CONTENT") || len(files) != 0 {
		t.Fatalf("outside symlink expanded: %q, %v", got, files)
	}
}

func TestFuzzyMatch(t *testing.T) {
	tests := []struct {
		query, target string
		want          bool
	}{
		{"rtr", "router", true},
		{"mgo", "main.go", true},
		{"xyz", "main.go", false},
		{"", "anything", true},
	}

	for _, tt := range tests {
		got := fuzzyMatch(tt.query, tt.target)
		if got != tt.want {
			t.Errorf("fuzzyMatch(%q, %q) = %v, want %v", tt.query, tt.target, got, tt.want)
		}
	}
}
