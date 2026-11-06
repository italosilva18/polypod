package summary

import (
	"strings"
	"testing"
)

func TestSummarizeToolUse_Bash(t *testing.T) {
	input := map[string]any{
		"command": "git status",
	}
	output := "On branch main\nnothing to commit\nexit code: 0\n"

	got := SummarizeToolUse("Bash", input, output)

	if got.ToolName != "Bash" {
		t.Errorf("ToolName = %q, want %q", got.ToolName, "Bash")
	}
	if got.InputDigest != "git status" {
		t.Errorf("InputDigest = %q, want %q", got.InputDigest, "git status")
	}
	if got.LineCount != 3 {
		t.Errorf("LineCount = %d, want 3", got.LineCount)
	}
	if got.WasError {
		t.Errorf("WasError = true, want false")
	}
	if !strings.Contains(got.OutputDigest, "exit=0") {
		t.Errorf("OutputDigest = %q, want to contain exit=0", got.OutputDigest)
	}
	if !strings.Contains(got.OutputDigest, "(3 lines of output)") {
		t.Errorf("OutputDigest = %q, want to contain (3 lines of output)", got.OutputDigest)
	}

	// Long command should be truncated to 60 chars.
	long := strings.Repeat("a", 200)
	got2 := SummarizeToolUse("Bash", map[string]any{"command": long}, "ok\n")
	if len(got2.InputDigest) != 60 {
		t.Errorf("long command truncation len = %d, want 60", len(got2.InputDigest))
	}

	// Determinism.
	again := SummarizeToolUse("Bash", input, output)
	if again != got {
		t.Errorf("non-deterministic: %+v vs %+v", again, got)
	}
}

func TestSummarizeToolUse_FileRead(t *testing.T) {
	input := map[string]any{
		"file_path": "/tmp/foo.go",
		"offset":    10,
		"limit":     20,
	}
	output := "line1\nline2\nline3\n"

	got := SummarizeToolUse("FileRead", input, output)

	if !strings.Contains(got.InputDigest, "/tmp/foo.go") {
		t.Errorf("InputDigest = %q, want to contain /tmp/foo.go", got.InputDigest)
	}
	if !strings.Contains(got.InputDigest, "[10:30]") {
		t.Errorf("InputDigest = %q, want to contain [10:30]", got.InputDigest)
	}
	if !strings.Contains(got.OutputDigest, "read 3 lines") {
		t.Errorf("OutputDigest = %q, want to contain 'read 3 lines'", got.OutputDigest)
	}
	if got.ByteCount != len(output) {
		t.Errorf("ByteCount = %d, want %d", got.ByteCount, len(output))
	}
}

func TestSummarizeToolUse_FileEditWrite(t *testing.T) {
	edit := SummarizeToolUse("FileEdit", map[string]any{"file_path": "/a/b.go"}, "")
	if edit.InputDigest != "/a/b.go" {
		t.Errorf("edit.InputDigest = %q", edit.InputDigest)
	}
	if edit.OutputDigest != "modified" {
		t.Errorf("edit.OutputDigest = %q, want modified", edit.OutputDigest)
	}

	write := SummarizeToolUse("FileWrite", map[string]any{"file_path": "/a/c.go"}, "")
	if write.OutputDigest != "created" {
		t.Errorf("write.OutputDigest = %q, want created", write.OutputDigest)
	}
}

func TestSummarizeToolUse_GlobGrep(t *testing.T) {
	glob := SummarizeToolUse("Glob", map[string]any{"pattern": "**/*.go"}, "a.go\nb.go\nc.go\n")
	if glob.InputDigest != "**/*.go" {
		t.Errorf("glob.InputDigest = %q", glob.InputDigest)
	}
	if glob.OutputDigest != "3 matches" {
		t.Errorf("glob.OutputDigest = %q, want '3 matches'", glob.OutputDigest)
	}

	grep := SummarizeToolUse("Grep", map[string]any{"pattern": "func main"}, "main.go:5\nmain.go:9\n")
	if grep.OutputDigest != "2 matches" {
		t.Errorf("grep.OutputDigest = %q, want '2 matches'", grep.OutputDigest)
	}
}

func TestSummarizeToolUse_WebFetchSearch(t *testing.T) {
	body := strings.Repeat("x", 500)

	fetch := SummarizeToolUse("WebFetch", map[string]any{"url": "https://example.com"}, body)
	if !strings.HasSuffix(fetch.OutputDigest, "...") {
		t.Errorf("WebFetch OutputDigest should end with ..., got %q", fetch.OutputDigest)
	}
	if len(fetch.OutputDigest) != 203 {
		t.Errorf("WebFetch OutputDigest length = %d, want 203", len(fetch.OutputDigest))
	}
	if fetch.InputDigest != "https://example.com" {
		t.Errorf("WebFetch InputDigest = %q", fetch.InputDigest)
	}

	search := SummarizeToolUse("WebSearch", map[string]any{"query": "golang generics"}, "result 1\nresult 2\n")
	if search.InputDigest != "golang generics" {
		t.Errorf("WebSearch InputDigest = %q", search.InputDigest)
	}
}

func TestSummarizeToolUse_Default(t *testing.T) {
	input := map[string]any{"alpha": 1, "beta": "two", "gamma": true}
	output := strings.Repeat("y", 250)

	got := SummarizeToolUse("UnknownTool", input, output)

	if !strings.HasPrefix(got.InputDigest, "{...}") {
		t.Errorf("InputDigest = %q, want to start with {...}", got.InputDigest)
	}
	if !strings.Contains(got.InputDigest, "3 keys") {
		t.Errorf("InputDigest = %q, want to contain '3 keys'", got.InputDigest)
	}
	if !strings.HasSuffix(got.OutputDigest, "...") {
		t.Errorf("OutputDigest should end with ..., got %q", got.OutputDigest)
	}
	if len(got.OutputDigest) != 103 {
		t.Errorf("OutputDigest length = %d, want 103", len(got.OutputDigest))
	}
}

func TestDetectError(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"plain ok", "All good\n", false},
		{"Error prefix", "Error: failed\n", true},
		{"lowercase error", "error: something\n", true},
		{"panic", "panic: runtime error\n", true},
		{"Exception", "Exception in thread main\n", true},
		{"Python traceback", "Some prelude\nTraceback (most recent call last)\n  File x\n", true},
		{"leading whitespace", "   \n  Error: boom\n", true},
		{"fatal git", "fatal: not a git repo\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DetectError(tc.in); got != tc.want {
				t.Errorf("DetectError(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestCompactToolUse_StringAndBlock(t *testing.T) {
	s := SummarizeToolUse("Bash", map[string]any{"command": "git status"},
		"On branch main\nexit code: 0\n")

	line := s.String()
	if !strings.Contains(line, "Bash") || !strings.Contains(line, "git status") {
		t.Errorf("String() = %q", line)
	}

	block := s.ToCompactBlock()
	if !strings.HasPrefix(block, "[tool:Bash]") {
		t.Errorf("ToCompactBlock() = %q, want prefix [tool:Bash]", block)
	}

	// Error case should use tool-error marker.
	errS := SummarizeToolUse("Bash", map[string]any{"command": "false"}, "Error: boom\n")
	if !errS.WasError {
		t.Errorf("expected WasError = true")
	}
	errBlock := errS.ToCompactBlock()
	if !strings.HasPrefix(errBlock, "[tool-error:Bash]") {
		t.Errorf("error block = %q, want prefix [tool-error:Bash]", errBlock)
	}
}
