package command

import (
	"context"
	"os"
	"os/user"
	"reflect"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------
// ParseInvocation
// ---------------------------------------------------------------------

func TestParseInvocation(t *testing.T) {
	tests := []struct {
		name  string
		input string
		cmd   string
		args  []string
		flags map[string]string
	}{
		{
			name:  "simple no args",
			input: "/help",
			cmd:   "help",
			args:  []string{},
			flags: map[string]string{},
		},
		{
			name:  "one positional arg",
			input: "/commit initial",
			cmd:   "commit",
			args:  []string{"initial"},
			flags: map[string]string{},
		},
		{
			name:  "long flag only",
			input: "/diff --staged",
			cmd:   "diff",
			args:  []string{},
			flags: map[string]string{"staged": "true"},
		},
		{
			name:  "quoted multi-word argument",
			input: `/commit "fix: multi word message"`,
			cmd:   "commit",
			args:  []string{"fix: multi word message"},
			flags: map[string]string{},
		},
		{
			name:  "multiple positional args",
			input: "/config set ai.model gpt-4",
			cmd:   "config",
			args:  []string{"set", "ai.model", "gpt-4"},
			flags: map[string]string{},
		},
		{
			name:  "three plain args",
			input: "/help arg1 arg2 arg3",
			cmd:   "help",
			args:  []string{"arg1", "arg2", "arg3"},
			flags: map[string]string{},
		},
		{
			name:  "flag with value",
			input: "/deploy --env=staging",
			cmd:   "deploy",
			args:  []string{},
			flags: map[string]string{"env": "staging"},
		},
		{
			name:  "short flag",
			input: "/build -f",
			cmd:   "build",
			args:  []string{},
			flags: map[string]string{"f": "true"},
		},
		{
			name:  "double dash terminator",
			input: "/run -- --not-a-flag arg",
			cmd:   "run",
			args:  []string{"--not-a-flag", "arg"},
			flags: map[string]string{},
		},
		{
			name:  "mixed args and flags",
			input: `/deploy prod "us east" --force --env=staging`,
			cmd:   "deploy",
			args:  []string{"prod", "us east"},
			flags: map[string]string{"force": "true", "env": "staging"},
		},
		{
			name:  "namespaced command name",
			input: "/git:commit message",
			cmd:   "git:commit",
			args:  []string{"message"},
			flags: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseInvocation(tt.input)
			if err != nil {
				t.Fatalf("ParseInvocation(%q) error: %v", tt.input, err)
			}
			if got.Name != tt.cmd {
				t.Errorf("Name = %q; want %q", got.Name, tt.cmd)
			}
			// Normalise nil -> [] for comparison.
			gotArgs := got.Args
			if gotArgs == nil {
				gotArgs = []string{}
			}
			if !reflect.DeepEqual(gotArgs, tt.args) {
				t.Errorf("Args = %#v; want %#v", gotArgs, tt.args)
			}
			if !reflect.DeepEqual(got.Flags, tt.flags) {
				t.Errorf("Flags = %#v; want %#v", got.Flags, tt.flags)
			}
			if got.Raw != tt.input {
				t.Errorf("Raw = %q; want %q", got.Raw, tt.input)
			}
		})
	}
}

func TestParseInvocation_NoLeadingSlash(t *testing.T) {
	// Leading slash is documented as optional.
	got, err := ParseInvocation("help topic")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "help" {
		t.Errorf("Name = %q; want %q", got.Name, "help")
	}
	if len(got.Args) != 1 || got.Args[0] != "topic" {
		t.Errorf("Args = %#v; want [topic]", got.Args)
	}
}

func TestParseInvocation_Errors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"only whitespace", "   "},
		{"only slash", "/"},
		{"unclosed double quote", `/commit "unclosed`},
		{"unclosed single quote", "/commit 'unclosed"},
		{"invalid command name", "/123bad arg"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseInvocation(tt.input)
			if err == nil {
				t.Errorf("ParseInvocation(%q) expected error, got nil", tt.input)
			}
		})
	}
}

// ---------------------------------------------------------------------
// ParseMarkdownCommand
// ---------------------------------------------------------------------

func TestParseMarkdownCommand_WithFrontmatter(t *testing.T) {
	content := `---
description: Run tests
allowed-tools: [bash, read_file]
model: gpt-4
---
Run the test suite: $ARGUMENTS
`
	parsed, err := ParseMarkdownCommand(content)
	if err != nil {
		t.Fatalf("ParseMarkdownCommand error: %v", err)
	}
	if parsed.Frontmatter.Description != "Run tests" {
		t.Errorf("Description = %q; want %q", parsed.Frontmatter.Description, "Run tests")
	}
	if parsed.Frontmatter.Model != "gpt-4" {
		t.Errorf("Model = %q; want %q", parsed.Frontmatter.Model, "gpt-4")
	}
	wantTools := []string{"bash", "read_file"}
	if !reflect.DeepEqual(parsed.Frontmatter.AllowedTools, wantTools) {
		t.Errorf("AllowedTools = %#v; want %#v", parsed.Frontmatter.AllowedTools, wantTools)
	}
	wantBody := "Run the test suite: $ARGUMENTS"
	if parsed.Body != wantBody {
		t.Errorf("Body = %q; want %q", parsed.Body, wantBody)
	}
}

func TestParseMarkdownCommand_NoFrontmatter(t *testing.T) {
	content := "Just a plain body with $ARGUMENTS and no metadata."
	parsed, err := ParseMarkdownCommand(content)
	if err != nil {
		t.Fatalf("ParseMarkdownCommand error: %v", err)
	}
	if parsed.Frontmatter.Description != "" {
		t.Errorf("Description = %q; want empty", parsed.Frontmatter.Description)
	}
	if parsed.Frontmatter.Model != "" {
		t.Errorf("Model = %q; want empty", parsed.Frontmatter.Model)
	}
	if len(parsed.Frontmatter.AllowedTools) != 0 {
		t.Errorf("AllowedTools = %#v; want empty", parsed.Frontmatter.AllowedTools)
	}
	if parsed.Body != content {
		t.Errorf("Body = %q; want %q", parsed.Body, content)
	}
}

func TestParseMarkdownCommand_UnterminatedFrontmatter(t *testing.T) {
	content := `---
description: Oops
model: gpt-4
body without closing marker
`
	_, err := ParseMarkdownCommand(content)
	if err == nil {
		t.Error("expected error for unterminated frontmatter, got nil")
	}
}

func TestParseMarkdownCommand_CRLF(t *testing.T) {
	// Frontmatter with Windows line endings should still parse.
	content := "---\r\ndescription: CRLF test\r\n---\r\nbody line\r\n"
	parsed, err := ParseMarkdownCommand(content)
	if err != nil {
		t.Fatalf("ParseMarkdownCommand error: %v", err)
	}
	if parsed.Frontmatter.Description != "CRLF test" {
		t.Errorf("Description = %q; want %q", parsed.Frontmatter.Description, "CRLF test")
	}
	if !strings.Contains(parsed.Body, "body line") {
		t.Errorf("Body = %q; want it to contain 'body line'", parsed.Body)
	}
}

// ---------------------------------------------------------------------
// SubstituteArguments
// ---------------------------------------------------------------------

func TestSubstituteArguments_Positional(t *testing.T) {
	got := SubstituteArguments("$1 and $2", []string{"alpha", "beta"})
	want := "alpha and beta"
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestSubstituteArguments_Arguments(t *testing.T) {
	got := SubstituteArguments("run: $ARGUMENTS", []string{"foo", "bar", "baz"})
	want := "run: foo bar baz"
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestSubstituteArguments_ArgumentsWithSpaces(t *testing.T) {
	// Args containing whitespace should be shell-quoted in $ARGUMENTS.
	got := SubstituteArguments("$ARGUMENTS", []string{"hello world", "plain"})
	if !strings.Contains(got, "'hello world'") {
		t.Errorf("expected shell-quoted 'hello world' in %q", got)
	}
	if !strings.Contains(got, "plain") {
		t.Errorf("expected 'plain' in %q", got)
	}
}

func TestSubstituteArguments_User(t *testing.T) {
	got := SubstituteArguments("hello $USER", nil)
	// $USER is resolved via os/user, not env. Compare against that.
	want := ""
	if u, err := user.Current(); err == nil {
		want = u.Username
	}
	expected := "hello " + want
	if got != expected {
		t.Errorf("got %q; want %q", got, expected)
	}
}

func TestSubstituteArguments_PWD(t *testing.T) {
	got := SubstituteArguments("cwd=$PWD", nil)
	wd, _ := os.Getwd()
	expected := "cwd=" + wd
	if got != expected {
		t.Errorf("got %q; want %q", got, expected)
	}
}

func TestSubstituteArguments_EscapedDollar(t *testing.T) {
	got := SubstituteArguments(`price is \$5`, nil)
	want := "price is $5"
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestSubstituteArguments_UnknownVariableLeftAsIs(t *testing.T) {
	// Unknown named variable should be preserved verbatim (including $).
	got := SubstituteArguments("hi $NOPE there", nil)
	if !strings.Contains(got, "$NOPE") {
		t.Errorf("expected $NOPE to remain, got %q", got)
	}
}

func TestSubstituteArguments_OutOfRangePositional(t *testing.T) {
	// Out-of-range positional refs become empty strings.
	got := SubstituteArguments("[$1][$2][$3]", []string{"only"})
	want := "[only][][]"
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

// ---------------------------------------------------------------------
// ExecuteShellInterpolations
// ---------------------------------------------------------------------

func TestExecuteShellInterpolations_Simple(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh available")
	}
	got, err := ExecuteShellInterpolations(context.Background(), "say: !`echo hi`", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "say: hi" {
		t.Errorf("got %q; want %q", got, "say: hi")
	}
}

func TestExecuteShellInterpolations_Multiple(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh available")
	}
	tmpl := "a=!`echo one` b=!`echo two`"
	got, err := ExecuteShellInterpolations(context.Background(), tmpl, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "a=one b=two"
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestExecuteShellInterpolations_NoInterpolations(t *testing.T) {
	input := "plain template without any shell spans"
	got, err := ExecuteShellInterpolations(context.Background(), input, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != input {
		t.Errorf("got %q; want %q", got, input)
	}
}

func TestExecuteShellInterpolations_Failure(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh available")
	}
	got, err := ExecuteShellInterpolations(context.Background(), "result: !`false`", "")
	if err == nil {
		t.Error("expected error from failing shell command, got nil")
	}
	// Returned text should still contain an error marker.
	if !strings.Contains(got, "[error:") {
		t.Errorf("expected '[error:' marker in %q", got)
	}
}
