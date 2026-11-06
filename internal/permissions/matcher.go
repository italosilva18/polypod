package permissions

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// RulePattern is a parsed permission rule of the form "Tool" or
// "Tool(argPattern)". An empty ArgPattern means the rule matches every
// invocation of the tool; a non-empty pattern is interpreted as a glob
// (with `*` and `**` wildcards) and matched against whichever concrete
// argument is appropriate for the tool kind.
//
// This mirrors the TS helper preparePermissionMatcher() in
// src_extracted/src/tools/*, which compiles rule strings to a closure that
// decides whether a specific tool invocation matches the rule.
type RulePattern struct {
	ToolName   string
	ArgPattern string
	Raw        string
}

// compiledRule holds a single rule with its pre-compiled glob regex. The
// regex is nil when ArgPattern is empty (match-all-for-tool).
type compiledRule struct {
	pattern RulePattern
	re      *regexp.Regexp
}

// CompiledMatcher is a batch of pre-compiled rule patterns. Use
// CompileMatchers to build one, then reuse it across many Match calls —
// regex compilation happens once up front.
type CompiledMatcher struct {
	rules []compiledRule
}

// ErrInvalidRulePattern is returned when a rule string cannot be parsed.
var ErrInvalidRulePattern = fmt.Errorf("invalid permission rule pattern")

// ParseRulePattern parses a rule string in the form "Tool" or
// "Tool(argPattern)". Leading/trailing whitespace is tolerated.
//
// Valid examples:
//
//	"Bash"                → {ToolName:"Bash"}
//	"Bash(git status)"    → {ToolName:"Bash", ArgPattern:"git status"}
//	"FileEdit(/tmp/**)"   → {ToolName:"FileEdit", ArgPattern:"/tmp/**"}
//	"Read(*.go)"          → {ToolName:"Read", ArgPattern:"*.go"}
//
// Invalid syntax (unmatched paren, empty tool name, etc.) returns an error
// so that callers surface it rather than silently allowing everything.
func ParseRulePattern(s string) (RulePattern, error) {
	raw := s
	s = strings.TrimSpace(s)
	if s == "" {
		return RulePattern{}, fmt.Errorf("%w: empty rule", ErrInvalidRulePattern)
	}

	open := strings.IndexByte(s, '(')
	if open == -1 {
		// Bare tool name — must be a reasonable identifier-ish string.
		if !isValidToolName(s) {
			return RulePattern{}, fmt.Errorf("%w: %q is not a valid tool name", ErrInvalidRulePattern, s)
		}
		return RulePattern{ToolName: s, Raw: raw}, nil
	}

	if !strings.HasSuffix(s, ")") {
		return RulePattern{}, fmt.Errorf("%w: missing closing paren in %q", ErrInvalidRulePattern, s)
	}
	tool := strings.TrimSpace(s[:open])
	arg := s[open+1 : len(s)-1]

	if tool == "" {
		return RulePattern{}, fmt.Errorf("%w: missing tool name in %q", ErrInvalidRulePattern, s)
	}
	if !isValidToolName(tool) {
		return RulePattern{}, fmt.Errorf("%w: %q is not a valid tool name", ErrInvalidRulePattern, tool)
	}
	return RulePattern{ToolName: tool, ArgPattern: arg, Raw: raw}, nil
}

// isValidToolName permits alphanumerics, underscores and the mcp__ server
// namespace separator. This is intentionally conservative; malformed names
// are rejected rather than silently accepted.
func isValidToolName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			continue
		}
		if c >= 'A' && c <= 'Z' {
			continue
		}
		if c >= '0' && c <= '9' {
			continue
		}
		if c == '_' || c == '-' {
			continue
		}
		return false
	}
	return true
}

// CompileMatchers parses and compiles a list of rule strings into a reusable
// CompiledMatcher. If any rule is malformed the whole batch fails — partial
// compilation could silently drop a deny rule and turn into a security hole.
func CompileMatchers(rules []string) (*CompiledMatcher, error) {
	m := &CompiledMatcher{}
	for i, s := range rules {
		pat, err := ParseRulePattern(s)
		if err != nil {
			return nil, fmt.Errorf("rule[%d]: %w", i, err)
		}
		cr := compiledRule{pattern: pat}
		if pat.ArgPattern != "" {
			re, err := compileGlob(pat.ArgPattern)
			if err != nil {
				return nil, fmt.Errorf("rule[%d] glob %q: %w", i, pat.ArgPattern, err)
			}
			cr.re = re
		}
		m.rules = append(m.rules, cr)
	}
	return m, nil
}

// Len reports the number of rules in the matcher. Zero is valid and simply
// means nothing will ever match.
func (m *CompiledMatcher) Len() int {
	if m == nil {
		return 0
	}
	return len(m.rules)
}

// Patterns returns a copy of the parsed RulePattern list, preserving order.
// Useful for diagnostics and explain-mode output.
func (m *CompiledMatcher) Patterns() []RulePattern {
	if m == nil {
		return nil
	}
	out := make([]RulePattern, 0, len(m.rules))
	for _, r := range m.rules {
		out = append(out, r.pattern)
	}
	return out
}

// Match returns true when at least one compiled rule matches the given tool
// call. Matching semantics:
//
//   - Tool name must match exactly (case-sensitive).
//   - Empty ArgPattern matches every invocation of that tool.
//   - Bash-like tools match against the "command" string.
//   - File tools (FileEdit/FileRead/FileWrite/Write/Edit/Read) match against
//     the path field.
//   - Any other tool falls back to matching against a deterministic string
//     representation of the input values.
func (m *CompiledMatcher) Match(toolName string, input map[string]any) bool {
	if m == nil || len(m.rules) == 0 {
		return false
	}
	candidates := candidateStrings(toolName, input)
	for _, r := range m.rules {
		if r.pattern.ToolName != toolName {
			continue
		}
		if r.re == nil {
			return true
		}
		for _, c := range candidates {
			if r.re.MatchString(c) {
				return true
			}
		}
	}
	return false
}

// MatchAny is a convenience entry point for callers that already hold the
// relevant string candidates (for example, a bash command split by the bash
// parser). It applies the same tool-name matching rules as Match but uses
// the provided strings directly.
func (m *CompiledMatcher) MatchAny(toolName string, args ...string) bool {
	if m == nil || len(m.rules) == 0 {
		return false
	}
	for _, r := range m.rules {
		if r.pattern.ToolName != toolName {
			continue
		}
		if r.re == nil {
			return true
		}
		for _, a := range args {
			if r.re.MatchString(a) {
				return true
			}
		}
	}
	return false
}

// candidateStrings builds the list of argument strings a rule pattern may be
// matched against for a given tool invocation. The ordering is intentional:
// the most specific candidate comes first so that a positive match happens
// as early as possible.
func candidateStrings(toolName string, input map[string]any) []string {
	if input == nil {
		return nil
	}
	switch {
	case isBashToolName(toolName):
		if cmd, ok := input["command"].(string); ok && cmd != "" {
			return []string{cmd}
		}
	case isFileToolName(toolName):
		for _, key := range []string{"file_path", "path", "filePath"} {
			if v, ok := input[key].(string); ok && v != "" {
				return []string{v}
			}
		}
	}
	// Generic fallback: stringify every string-valued arg in a stable key
	// order so matchers remain deterministic regardless of map iteration.
	keys := make([]string, 0, len(input))
	for k := range input {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		switch v := input[k].(type) {
		case string:
			if v != "" {
				out = append(out, v)
			}
		case fmt.Stringer:
			s := v.String()
			if s != "" {
				out = append(out, s)
			}
		default:
			out = append(out, fmt.Sprintf("%v", v))
		}
	}
	return out
}

// isBashToolName mirrors the helper in enforcer.go but is kept local so the
// matcher package can be used in isolation (for example in unit tests).
func isBashToolName(name string) bool {
	switch name {
	case "Bash", "bash", "Shell", "RunCommand", "run_command":
		return true
	}
	return false
}

// isFileToolName returns true for the set of tools whose primary argument is
// a filesystem path, so their rule patterns match against that path.
func isFileToolName(name string) bool {
	switch name {
	case "FileEdit", "FileRead", "FileWrite",
		"Edit", "MultiEdit", "Write", "Read",
		"write_file", "edit_file", "read_file", "create_file", "patch_file":
		return true
	}
	return false
}

// compileGlob converts a glob pattern to an anchored regex. It supports:
//   - `*`  — any run of chars except `/`
//   - `**` — any run of chars including `/` (recursive directory wildcard)
//   - `?`  — exactly one character (not `/`)
//   - literal text for everything else
//
// This is a superset of path/filepath.Match, which notably does not
// understand `**` — so we implement the conversion ourselves.
func compileGlob(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString(`\A`)
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch c {
		case '\\':
			// Backslash escapes the next character as a literal. A trailing
			// backslash with nothing after it is treated as a literal too.
			if i+1 < len(glob) {
				b.WriteString(regexp.QuoteMeta(string(glob[i+1])))
				i++
			} else {
				b.WriteString(regexp.QuoteMeta(`\`))
			}
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				b.WriteString(`.*`)
				i++ // consume second '*'
			} else {
				b.WriteString(`[^/]*`)
			}
		case '?':
			b.WriteString(`[^/]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString(`\z`)
	return regexp.Compile(b.String())
}

// EscapeGlob escapes a literal string so that embedding it inside a glob
// pattern (or a regex built from one) does not accidentally introduce
// wildcards or regex metacharacters. Useful for building patterns
// programmatically from untrusted user input.
func EscapeGlob(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\', '*', '?', '[', ']', '{', '}', '(', ')', '^', '$', '+', '.', '|':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
