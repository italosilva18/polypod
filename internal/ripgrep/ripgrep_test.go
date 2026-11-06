package ripgrep

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile is a small helper that writes content to a file under dir,
// failing the test on error.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// requireRg skips the test if the rg binary is not available.
func requireRg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg not installed; skipping integration test")
	}
}

// ---------------------------------------------------------------------------
// BuildRipgrepArgs tests (no rg binary required)
// ---------------------------------------------------------------------------

func TestBuildRipgrepArgs_Defaults(t *testing.T) {
	args := BuildRipgrepArgs("foo", RipgrepOptions{})

	// Defaults: --smart-case, -F (literal), terminator then pattern.
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--smart-case") {
		t.Errorf("expected --smart-case in default args, got %v", args)
	}
	if !strings.Contains(joined, "-F") {
		t.Errorf("expected -F (literal) in default args, got %v", args)
	}
	if args[len(args)-1] != "foo" {
		t.Errorf("expected last arg to be pattern 'foo', got %q", args[len(args)-1])
	}
	if args[len(args)-2] != "--" {
		t.Errorf("expected '--' terminator before pattern, got %q", args[len(args)-2])
	}
}

func TestBuildRipgrepArgs_CaseSensitivity(t *testing.T) {
	cs := BuildRipgrepArgs("p", RipgrepOptions{CaseSensitive: true})
	if !contains(cs, "-s") {
		t.Errorf("CaseSensitive: expected -s, got %v", cs)
	}
	if contains(cs, "--smart-case") {
		t.Errorf("CaseSensitive: should not contain --smart-case, got %v", cs)
	}

	ic := BuildRipgrepArgs("p", RipgrepOptions{IgnoreCase: true})
	if !contains(ic, "-i") {
		t.Errorf("IgnoreCase: expected -i, got %v", ic)
	}
}

func TestBuildRipgrepArgs_RegexAndWholeWord(t *testing.T) {
	// Regex=true should NOT add -F (literal); default (Regex=false) does.
	regex := BuildRipgrepArgs("fo+", RipgrepOptions{Regex: true, WholeWord: true})
	if contains(regex, "-F") {
		t.Errorf("Regex=true: should not contain -F, got %v", regex)
	}
	if !contains(regex, "-w") {
		t.Errorf("WholeWord: expected -w, got %v", regex)
	}
}

func TestBuildRipgrepArgs_FileFilterAndExcludes(t *testing.T) {
	args := BuildRipgrepArgs("x", RipgrepOptions{
		FilePattern:     "*.go",
		ExcludePatterns: []string{"vendor", "!node_modules"},
		MaxResults:      42,
	})

	// FilePattern -g *.go
	if !hasPair(args, "-g", "*.go") {
		t.Errorf("expected -g *.go, got %v", args)
	}
	// ExcludePatterns should be prefixed with "!"
	if !hasPair(args, "-g", "!vendor") {
		t.Errorf("expected exclude -g !vendor, got %v", args)
	}
	if !hasPair(args, "-g", "!node_modules") {
		t.Errorf("expected exclude -g !node_modules (kept prefix), got %v", args)
	}
	if !hasPair(args, "--max-count", "42") {
		t.Errorf("expected --max-count 42, got %v", args)
	}
}

func TestBuildRipgrepArgs_OtherFlags(t *testing.T) {
	args := BuildRipgrepArgs("x", RipgrepOptions{
		IncludeHidden:  true,
		FollowSymlinks: true,
		JSONOutput:     true,
		NoIgnore:       true,
		SingleThread:   true,
	})
	for _, want := range []string{"--hidden", "-L", "--json", "--no-ignore"} {
		if !contains(args, want) {
			t.Errorf("expected %q in args, got %v", want, args)
		}
	}
	if !hasPair(args, "-j", "1") {
		t.Errorf("SingleThread: expected -j 1, got %v", args)
	}
}

// ---------------------------------------------------------------------------
// ParseRipgrepOutput tests (no rg binary required)
// ---------------------------------------------------------------------------

func TestParseRipgrepOutput_SimpleMatch(t *testing.T) {
	// Minimal rg --json "match" event.
	jsonOut := `{"type":"match","data":{"path":{"text":"src/a.go"},"lines":{"text":"hello world\n"},"line_number":3,"submatches":[{"match":{"text":"hello"},"start":0,"end":5}]}}
`
	matches := ParseRipgrepOutput([]byte(jsonOut))
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	m := matches[0]
	if m.File != "src/a.go" {
		t.Errorf("File: got %q, want src/a.go", m.File)
	}
	if m.Line != 3 {
		t.Errorf("Line: got %d, want 3", m.Line)
	}
	if m.Column != 1 {
		t.Errorf("Column: got %d, want 1 (1-indexed)", m.Column)
	}
	if m.Match != "hello" {
		t.Errorf("Match: got %q, want hello", m.Match)
	}
}

func TestParseRipgrepOutput_MatchWithContext(t *testing.T) {
	jsonOut := `{"type":"match","data":{"path":{"text":"f.txt"},"lines":{"text":"target\n"},"line_number":5,"submatches":[{"match":{"text":"target"},"start":0,"end":6}]}}
{"type":"context","data":{"path":{"text":"f.txt"},"lines":{"text":"after line\n"},"line_number":6}}
`
	matches := ParseRipgrepOutput([]byte(jsonOut))
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if len(matches[0].Context) != 1 {
		t.Fatalf("expected 1 context line, got %d", len(matches[0].Context))
	}
	if !strings.Contains(matches[0].Context[0], "after line") {
		t.Errorf("context missing expected text: %q", matches[0].Context[0])
	}
	if !strings.HasPrefix(matches[0].Context[0], "6:") {
		t.Errorf("context should be prefixed with line number: %q", matches[0].Context[0])
	}
}

func TestParseRipgrepOutput_IgnoresInvalidLines(t *testing.T) {
	// Empty input → nil/empty.
	if got := ParseRipgrepOutput(nil); len(got) != 0 {
		t.Errorf("nil input: expected 0 matches, got %d", len(got))
	}
	// Garbage lines should be skipped without panicking.
	bad := []byte("not json\n{invalid}\n\n{\"type\":\"summary\",\"data\":{}}\n")
	if got := ParseRipgrepOutput(bad); len(got) != 0 {
		t.Errorf("bad input: expected 0 matches, got %d", len(got))
	}
	// Context without preceding match is dropped.
	onlyCtx := []byte(`{"type":"context","data":{"path":{"text":"x"},"lines":{"text":"y\n"},"line_number":1}}`)
	if got := ParseRipgrepOutput(onlyCtx); len(got) != 0 {
		t.Errorf("context-only: expected 0 matches, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// parseLines helper tests (lowercase but in-package)
// ---------------------------------------------------------------------------

func TestParseLines_TrimsAndSkipsEmpty(t *testing.T) {
	in := []byte("alpha\r\nbeta\n\n  \ngamma\n")
	got := parseLines(in)
	// parseLines trims CR and filters empties after split but not whitespace-only.
	want := []string{"alpha", "beta", "  ", "gamma"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %q, want %q", i, got[i], want[i])
		}
	}

	if got := parseLines([]byte("   \n")); len(got) != 0 {
		t.Errorf("whitespace-only: expected empty, got %v", got)
	}
	if got := parseLines(nil); got != nil {
		t.Errorf("nil: expected nil, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// LimitWriter tests (no rg binary required)
// ---------------------------------------------------------------------------

func TestLimitWriter_TruncatesAtLimit(t *testing.T) {
	var buf bytes.Buffer
	lw := &LimitWriter{buf: &buf, limit: 5}

	n, err := lw.Write([]byte("abc"))
	if err != nil || n != 3 {
		t.Fatalf("first write: n=%d err=%v", n, err)
	}
	// Second write exceeds limit; LimitWriter truncates silently.
	if _, err := lw.Write([]byte("defgh")); err != nil {
		t.Fatalf("second write error: %v", err)
	}
	if got := buf.Len(); got != 5 {
		t.Errorf("buffer length: got %d, want 5 (capped)", got)
	}

	// Further writes are silently discarded; return value echoes len(p).
	n, err = lw.Write([]byte("ijkl"))
	if err != nil || n != 4 {
		t.Errorf("overflow write: n=%d err=%v (expected n=4, nil)", n, err)
	}
	if got := buf.Len(); got != 5 {
		t.Errorf("buffer length after overflow: got %d, want 5", got)
	}
	if got := buf.String(); got != "abcde" {
		t.Errorf("buffer content: got %q, want %q", got, "abcde")
	}
}

// ---------------------------------------------------------------------------
// Integration tests (require rg on PATH)
// ---------------------------------------------------------------------------

func TestExecRipgrep_SimplePattern(t *testing.T) {
	requireRg(t)
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "hello world\nfoo bar\n")
	writeFile(t, dir, "b.txt", "nothing to see\n")

	args := BuildRipgrepArgs("hello", RipgrepOptions{})
	args = append(args, ".")

	out, err := ExecRipgrep(context.Background(), args, dir)
	if err != nil {
		t.Fatalf("ExecRipgrep: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "hello world") {
		t.Errorf("expected output to contain 'hello world', got: %s", s)
	}
	if !strings.Contains(s, "a.txt") {
		t.Errorf("expected output to contain filename 'a.txt', got: %s", s)
	}
}

func TestExecRipgrep_Regex(t *testing.T) {
	requireRg(t)
	dir := t.TempDir()
	writeFile(t, dir, "nums.txt", "abc123\nxyz\nabc9\n")

	// Regex-enabled pattern: match lines with abc + digits.
	args := BuildRipgrepArgs(`abc\d+`, RipgrepOptions{Regex: true})
	args = append(args, ".")

	out, err := ExecRipgrep(context.Background(), args, dir)
	if err != nil {
		t.Fatalf("ExecRipgrep: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "abc123") || !strings.Contains(s, "abc9") {
		t.Errorf("expected both abc123 and abc9, got: %s", s)
	}
	if strings.Contains(s, "xyz") {
		t.Errorf("did not expect 'xyz' in output: %s", s)
	}
}

func TestExecRipgrep_NoMatchReturnsEmpty(t *testing.T) {
	requireRg(t)
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "apples\noranges\n")

	args := BuildRipgrepArgs("definitelynothere", RipgrepOptions{})
	args = append(args, ".")

	out, err := ExecRipgrep(context.Background(), args, dir)
	if err != nil {
		t.Fatalf("ExecRipgrep (no match should be success): %v", err)
	}
	if len(strings.TrimSpace(string(out))) != 0 {
		t.Errorf("expected empty output on no match, got: %q", string(out))
	}
}

func TestExecRipgrep_FileFilter(t *testing.T) {
	requireRg(t)
	dir := t.TempDir()
	writeFile(t, dir, "keep.go", "package main // TARGET\n")
	writeFile(t, dir, "skip.txt", "TARGET appears here too\n")

	args := BuildRipgrepArgs("TARGET", RipgrepOptions{FilePattern: "*.go"})
	args = append(args, ".")

	out, err := ExecRipgrep(context.Background(), args, dir)
	if err != nil {
		t.Fatalf("ExecRipgrep: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "keep.go") {
		t.Errorf("expected keep.go in output, got: %s", s)
	}
	if strings.Contains(s, "skip.txt") {
		t.Errorf("file filter failed; skip.txt appeared: %s", s)
	}
}

func TestExecRipgrep_JSONOutputParseable(t *testing.T) {
	requireRg(t)
	dir := t.TempDir()
	writeFile(t, dir, "data.txt", "line one\nneedle here\nline three\n")

	args := BuildRipgrepArgs("needle", RipgrepOptions{JSONOutput: true})
	args = append(args, ".")

	out, err := ExecRipgrep(context.Background(), args, dir)
	if err != nil {
		t.Fatalf("ExecRipgrep: %v", err)
	}

	matches := ParseRipgrepOutput(out)
	if len(matches) == 0 {
		t.Fatalf("expected at least one parsed match, got 0. raw output: %s", string(out))
	}
	found := false
	for _, m := range matches {
		if strings.Contains(m.Match, "needle") || strings.Contains(m.File, "data.txt") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no parsed match referenced 'needle' or 'data.txt': %+v", matches)
	}
}

func TestRun_ReturnsLines(t *testing.T) {
	requireRg(t)
	dir := t.TempDir()
	writeFile(t, dir, "x.txt", "foo\nbar\nfoo\n")

	args := BuildRipgrepArgs("foo", RipgrepOptions{})
	lines, err := Run(context.Background(), args, ".", dir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(lines) == 0 {
		t.Errorf("expected non-empty lines")
	}
	for _, l := range lines {
		if l == "" {
			t.Errorf("Run returned empty line in result")
		}
	}
}

func TestFindRipgrepBinary_Available(t *testing.T) {
	requireRg(t)
	path, err := FindRipgrepBinary()
	if err != nil {
		t.Fatalf("FindRipgrepBinary: %v", err)
	}
	if path == "" {
		t.Errorf("expected non-empty binary path")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// hasPair returns true if args contains flag immediately followed by value.
func hasPair(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}
