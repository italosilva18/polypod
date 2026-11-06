package permissions

import (
	"errors"
	"testing"
)

func TestParseRulePattern_Bare(t *testing.T) {
	p, err := ParseRulePattern("Bash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.ToolName != "Bash" || p.ArgPattern != "" {
		t.Fatalf("unexpected parse result: %#v", p)
	}
}

func TestParseRulePattern_WithArg(t *testing.T) {
	p, err := ParseRulePattern("Bash(git status)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.ToolName != "Bash" {
		t.Fatalf("expected tool Bash, got %q", p.ToolName)
	}
	if p.ArgPattern != "git status" {
		t.Fatalf("expected arg 'git status', got %q", p.ArgPattern)
	}
}

func TestParseRulePattern_Invalid(t *testing.T) {
	cases := []string{
		"",
		"  ",
		"Bash(",
		"(foo)",
		"Bad Name(x)",
		"!?(x)",
	}
	for _, c := range cases {
		if _, err := ParseRulePattern(c); err == nil {
			t.Fatalf("expected error parsing %q, got nil", c)
		} else if !errors.Is(err, ErrInvalidRulePattern) {
			t.Fatalf("expected ErrInvalidRulePattern for %q, got %v", c, err)
		}
	}
}

func TestCompileMatchers_EmptyList(t *testing.T) {
	m, err := CompileMatchers(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Match("Bash", map[string]any{"command": "rm -rf /"}) {
		t.Fatalf("empty matcher should not match")
	}
}

func TestMatch_ExactBashCommand(t *testing.T) {
	m, err := CompileMatchers([]string{"Bash(git status)"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !m.Match("Bash", map[string]any{"command": "git status"}) {
		t.Fatalf("expected exact bash command to match")
	}
	if m.Match("Bash", map[string]any{"command": "git push"}) {
		t.Fatalf("different command should not match")
	}
}

func TestMatch_BareToolMatchesAnyInput(t *testing.T) {
	m, err := CompileMatchers([]string{"Bash"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !m.Match("Bash", map[string]any{"command": "anything"}) {
		t.Fatalf("bare Bash rule should match any bash invocation")
	}
	if !m.Match("Bash", map[string]any{}) {
		t.Fatalf("bare Bash rule should match empty input too")
	}
}

func TestMatch_GlobSingleStar(t *testing.T) {
	m, err := CompileMatchers([]string{"FileEdit(/tmp/*.log)"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !m.Match("FileEdit", map[string]any{"file_path": "/tmp/server.log"}) {
		t.Fatalf("expected single-star glob to match /tmp/server.log")
	}
	if m.Match("FileEdit", map[string]any{"file_path": "/tmp/sub/server.log"}) {
		t.Fatalf("single-star should NOT cross directory boundary")
	}
	if m.Match("FileEdit", map[string]any{"file_path": "/var/log/x.log"}) {
		t.Fatalf("unrelated path should not match")
	}
}

func TestMatch_GlobDoubleStar(t *testing.T) {
	m, err := CompileMatchers([]string{"FileEdit(/tmp/**)"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	cases := []string{
		"/tmp/a.txt",
		"/tmp/sub/dir/file.go",
		"/tmp/",
	}
	for _, p := range cases {
		if !m.Match("FileEdit", map[string]any{"file_path": p}) {
			t.Fatalf("expected /tmp/** to match %q", p)
		}
	}
	if m.Match("FileEdit", map[string]any{"file_path": "/var/tmp/x"}) {
		t.Fatalf("/tmp/** should not match /var/tmp/x")
	}
}

func TestMatch_DifferentToolName(t *testing.T) {
	m, err := CompileMatchers([]string{"Bash(ls)"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if m.Match("Shell", map[string]any{"command": "ls"}) {
		t.Fatalf("rule for Bash should not match tool named Shell")
	}
}

func TestMatch_CaseSensitive(t *testing.T) {
	m, err := CompileMatchers([]string{"Bash(git status)"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if m.Match("bash", map[string]any{"command": "git status"}) {
		t.Fatalf("tool name match should be case-sensitive")
	}
	if m.Match("Bash", map[string]any{"command": "GIT STATUS"}) {
		t.Fatalf("arg pattern should be case-sensitive")
	}
}

func TestMatchAny_ConvenienceSplit(t *testing.T) {
	m, err := CompileMatchers([]string{"Bash(git *)"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !m.MatchAny("Bash", "ls -la", "git status") {
		t.Fatalf("expected one of the candidates to match the glob")
	}
	if m.MatchAny("Bash", "ls", "pwd") {
		t.Fatalf("no candidate should have matched")
	}
}

func TestEscapeGlob_EscapesMetacharacters(t *testing.T) {
	in := "a*b?[c]{d}(e)^$+.|\\"
	out := EscapeGlob(in)
	// Every meta char must be preceded by a backslash in the output.
	for _, c := range []byte{'*', '?', '[', ']', '{', '}', '(', ')', '^', '$', '+', '.', '|', '\\'} {
		if !containsEscaped(out, c) {
			t.Fatalf("EscapeGlob(%q) did not escape %q: got %q", in, string(c), out)
		}
	}
}

func TestCompileMatchers_PropagatesParseError(t *testing.T) {
	if _, err := CompileMatchers([]string{"Bash(", "Read"}); err == nil {
		t.Fatalf("expected error on malformed rule")
	}
}

func TestMatch_GlobWithEscapedLiteral(t *testing.T) {
	// Using the EscapeGlob helper to embed a literal path fragment.
	literal := EscapeGlob("/tmp/a.b")
	m, err := CompileMatchers([]string{"FileEdit(" + literal + ")"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !m.Match("FileEdit", map[string]any{"file_path": "/tmp/a.b"}) {
		t.Fatalf("escaped literal should match itself exactly")
	}
	if m.Match("FileEdit", map[string]any{"file_path": "/tmp/aXb"}) {
		t.Fatalf("escaped literal should not treat '.' as a wildcard")
	}
}

// containsEscaped reports whether the given byte appears preceded by a
// backslash anywhere in s.
func containsEscaped(s string, c byte) bool {
	for i := 1; i < len(s); i++ {
		if s[i] == c && s[i-1] == '\\' {
			return true
		}
	}
	return false
}
