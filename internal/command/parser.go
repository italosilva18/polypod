package command

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ParsedInvocation represents the result of parsing a slash command line.
//
// Given input such as:
//
//	/deploy prod "us east" --force --env=staging
//
// the fields are:
//
//	Name  = "deploy"
//	Args  = ["prod", "us east"]
//	Flags = {"force": "true", "env": "staging"}
//	Raw   = "/deploy prod \"us east\" --force --env=staging"
type ParsedInvocation struct {
	Name  string
	Args  []string
	Flags map[string]string
	Raw   string
}

// ParseInvocation parses a slash command invocation. The leading slash is
// optional. Quoted arguments preserve spaces. Flags in the form "--flag"
// and "--flag=value" are extracted into Flags; bare "--flag" becomes "true".
// A single "--" terminator stops flag parsing and treats the rest as args.
func ParseInvocation(input string) (*ParsedInvocation, error) {
	raw := input
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, fmt.Errorf("empty command")
	}
	// Leading slash is optional.
	input = strings.TrimPrefix(input, "/")
	if input == "" {
		return nil, fmt.Errorf("empty command")
	}

	tokens, err := tokenize(input)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("empty command")
	}

	inv := &ParsedInvocation{
		Name:  tokens[0],
		Args:  make([]string, 0, len(tokens)-1),
		Flags: make(map[string]string),
		Raw:   raw,
	}

	// Command names may not contain spaces; allow dots/dashes/colons for
	// namespaced commands (e.g. "git:commit", "skill.build").
	if !isValidCommandName(inv.Name) {
		return nil, fmt.Errorf("invalid command name %q", inv.Name)
	}

	terminated := false
	for _, tok := range tokens[1:] {
		if !terminated && tok == "--" {
			terminated = true
			continue
		}
		if !terminated && strings.HasPrefix(tok, "--") && len(tok) > 2 {
			body := tok[2:]
			if eq := strings.Index(body, "="); eq >= 0 {
				inv.Flags[body[:eq]] = body[eq+1:]
			} else {
				inv.Flags[body] = "true"
			}
			continue
		}
		if !terminated && strings.HasPrefix(tok, "-") && len(tok) > 1 && !isNumeric(tok[1:]) {
			// Short flag "-f" or "-f=val"; treated like long flags.
			body := tok[1:]
			if eq := strings.Index(body, "="); eq >= 0 {
				inv.Flags[body[:eq]] = body[eq+1:]
			} else {
				inv.Flags[body] = "true"
			}
			continue
		}
		inv.Args = append(inv.Args, tok)
	}

	return inv, nil
}

// tokenize splits a command line respecting double- and single-quoted
// segments and backslash escapes (for escaping quotes / spaces).
func tokenize(input string) ([]string, error) {
	var tokens []string
	var cur strings.Builder
	inSingle, inDouble := false, false
	escape := false
	hasToken := false

	flush := func() {
		if hasToken {
			tokens = append(tokens, cur.String())
			cur.Reset()
			hasToken = false
		}
	}

	for i := 0; i < len(input); i++ {
		c := input[i]
		if escape {
			cur.WriteByte(c)
			hasToken = true
			escape = false
			continue
		}
		switch {
		case c == '\\' && !inSingle:
			escape = true
		case c == '"' && !inSingle:
			inDouble = !inDouble
			hasToken = true
		case c == '\'' && !inDouble:
			inSingle = !inSingle
			hasToken = true
		case (c == ' ' || c == '\t') && !inSingle && !inDouble:
			flush()
		default:
			cur.WriteByte(c)
			hasToken = true
		}
	}
	if inSingle || inDouble {
		return nil, fmt.Errorf("unterminated quote in command")
	}
	if escape {
		return nil, fmt.Errorf("dangling escape at end of command")
	}
	flush()
	return tokens, nil
}

var commandNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_\-.:]*$`)

func isValidCommandName(name string) bool {
	return commandNameRe.MatchString(name)
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if c == '-' || c == '+' {
			if i != 0 {
				return false
			}
			continue
		}
		if c == '.' {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------
// Markdown command files with YAML frontmatter
// ---------------------------------------------------------------------

// Frontmatter holds fields commonly supported in .md command files.
//
// Example:
//
//	---
//	description: Run tests
//	allowed-tools: [bash, read_file]
//	model: deepseek-chat
//	---
//
// Unknown keys are captured in Extra so callers can read custom metadata.
type Frontmatter struct {
	Description  string         `yaml:"description"`
	AllowedTools []string       `yaml:"allowed-tools"`
	Model        string         `yaml:"model"`
	Agent        string         `yaml:"agent"`
	Extra        map[string]any `yaml:",inline"`
}

// ParsedCommand is a .md command file split into metadata and body.
type ParsedCommand struct {
	Frontmatter Frontmatter
	Body        string
}

// ParseMarkdownCommand parses a .md command definition. The frontmatter
// block is optional; when absent the whole content is treated as Body and
// Frontmatter is zero-valued. The frontmatter is a YAML block delimited by
// lines containing only "---" at the very top of the file.
func ParseMarkdownCommand(content string) (*ParsedCommand, error) {
	// Normalise line endings.
	content = strings.ReplaceAll(content, "\r\n", "\n")
	trimmed := strings.TrimLeft(content, " \t\n")

	// No frontmatter: whole content is the body.
	if !strings.HasPrefix(trimmed, "---") {
		return &ParsedCommand{Body: strings.TrimSpace(content)}, nil
	}

	// Find the opening "---" line in the original content to preserve
	// any leading whitespace accounting.
	lines := strings.Split(content, "\n")
	// Skip blank lines before "---".
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	if start >= len(lines) || strings.TrimSpace(lines[start]) != "---" {
		return &ParsedCommand{Body: strings.TrimSpace(content)}, nil
	}

	// Find closing "---".
	end := -1
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, fmt.Errorf("unterminated frontmatter block")
	}

	rawYAML := strings.Join(lines[start+1:end], "\n")
	var fm Frontmatter
	if strings.TrimSpace(rawYAML) != "" {
		if err := yaml.Unmarshal([]byte(rawYAML), &fm); err != nil {
			return nil, fmt.Errorf("parse frontmatter: %w", err)
		}
	}

	body := ""
	if end+1 < len(lines) {
		body = strings.Join(lines[end+1:], "\n")
	}
	body = strings.TrimLeft(body, "\n")
	body = strings.TrimRight(body, " \t\n")

	return &ParsedCommand{Frontmatter: fm, Body: body}, nil
}

// ---------------------------------------------------------------------
// Template substitution
// ---------------------------------------------------------------------

// SubstituteArguments replaces variables in a command template:
//
//	$1, $2, ... $N   → positional arguments (out of range → empty)
//	$ARGUMENTS       → all arguments joined by spaces (shell-quoted if
//	                   they contain whitespace)
//	$USER            → current user's login (best effort)
//	$PWD             → current working directory
//
// Dollar signs can be escaped as "\$" which becomes a literal "$".
// Unknown variables are left intact, matching Claude Code behaviour.
func SubstituteArguments(template string, args []string) string {
	username := ""
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	pwd, _ := os.Getwd()

	var out strings.Builder
	out.Grow(len(template))

	i := 0
	for i < len(template) {
		c := template[i]
		// Escape: "\$" → "$"
		if c == '\\' && i+1 < len(template) && template[i+1] == '$' {
			out.WriteByte('$')
			i += 2
			continue
		}
		if c != '$' {
			out.WriteByte(c)
			i++
			continue
		}

		// "$$" → "$"
		if i+1 < len(template) && template[i+1] == '$' {
			out.WriteByte('$')
			i += 2
			continue
		}

		// Identify variable name.
		name, consumed := scanVarName(template[i+1:])
		if name == "" {
			out.WriteByte('$')
			i++
			continue
		}
		switch name {
		case "ARGUMENTS":
			out.WriteString(joinArgs(args))
		case "USER":
			out.WriteString(username)
		case "PWD":
			out.WriteString(pwd)
		default:
			if idx, ok := positionalIndex(name); ok {
				if idx >= 1 && idx <= len(args) {
					out.WriteString(args[idx-1])
				}
				// out-of-range positional → empty
			} else {
				// Unknown: preserve original.
				out.WriteByte('$')
				out.WriteString(name)
			}
		}
		i += 1 + consumed
	}

	return out.String()
}

// scanVarName returns the variable name starting at s (already past the '$')
// and the number of bytes consumed. Valid names are [A-Z_][A-Z0-9_]* for
// named vars, or digits for positional refs.
func scanVarName(s string) (string, int) {
	if s == "" {
		return "", 0
	}
	// Positional: $1, $12
	if s[0] >= '0' && s[0] <= '9' {
		j := 0
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		return s[:j], j
	}
	// Named: letters + underscores
	if !(s[0] == '_' || (s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= 'a' && s[0] <= 'z')) {
		return "", 0
	}
	j := 0
	for j < len(s) {
		c := s[j]
		if c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			j++
			continue
		}
		break
	}
	return strings.ToUpper(s[:j]), j
}

func positionalIndex(name string) (int, bool) {
	if name == "" {
		return 0, false
	}
	n := 0
	for _, c := range name {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

func joinArgs(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t\"'\\") {
			parts[i] = shellQuote(a)
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	// POSIX-style single-quote escape: 'it'\''s'
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ---------------------------------------------------------------------
// Shell interpolation: !`command`
// ---------------------------------------------------------------------

// shellInterpRe matches `!`…“ spans. We use a simple scanner instead of
// a regex so that backticks inside the command don't confuse it — but for
// our supported syntax (single-backtick spans without nested backticks)
// a regex is sufficient and much clearer.
var shellInterpRe = regexp.MustCompile("!`([^`]*)`")

// defaultShellTimeout bounds each individual shell interpolation.
const defaultShellTimeout = 10 * time.Second

// ExecuteShellInterpolations replaces each `!`command“ span in template
// with the stdout of running that command via /bin/sh -c (or cmd on
// Windows). Failures are returned as an error; the partially substituted
// text is still returned for inspection.
//
// The workingDir argument is the directory in which each command runs;
// pass "" for the process default. A zero ctx deadline is upgraded to
// defaultShellTimeout per command.
func ExecuteShellInterpolations(ctx context.Context, template string, workingDir string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	matches := shellInterpRe.FindAllStringSubmatchIndex(template, -1)
	if len(matches) == 0 {
		return template, nil
	}

	var out strings.Builder
	out.Grow(len(template))
	last := 0
	var firstErr error

	for _, m := range matches {
		// m = [fullStart, fullEnd, cmdStart, cmdEnd]
		out.WriteString(template[last:m[0]])
		cmdStr := template[m[2]:m[3]]
		stdout, err := runShell(ctx, cmdStr, workingDir)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("shell interpolation %q failed: %w", cmdStr, err)
			}
			// On failure insert an error marker so the template still makes
			// syntactic sense for downstream consumers.
			out.WriteString(fmt.Sprintf("[error: %v]", err))
		} else {
			out.WriteString(strings.TrimRight(stdout, "\n"))
		}
		last = m[1]
	}
	out.WriteString(template[last:])

	return out.String(), firstErr
}

func runShell(ctx context.Context, cmdStr, workingDir string) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, defaultShellTimeout)
	defer cancel()

	var cmd *exec.Cmd
	if isWindows() {
		cmd = exec.CommandContext(cmdCtx, "cmd", "/C", cmdStr)
	} else {
		cmd = exec.CommandContext(cmdCtx, "/bin/sh", "-c", cmdStr)
	}
	if workingDir != "" {
		cmd.Dir = workingDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return stdout.String(), err
		}
		return stdout.String(), fmt.Errorf("%s: %w", msg, err)
	}
	return stdout.String(), nil
}

func isWindows() bool {
	return os.PathSeparator == '\\'
}
