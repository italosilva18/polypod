package inputprocessing

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// MentionType classifies a resolved @-mention.
type MentionType int

const (
	// MentionFile is a mention that resolved to a regular file.
	MentionFile MentionType = iota
	// MentionDirectory is a mention that resolved to a directory.
	MentionDirectory
	// MentionInvalid is a mention that could not be resolved.
	MentionInvalid
)

// Mention represents a single @-reference extracted from user text.
type Mention struct {
	// Raw is the original token including the leading '@'.
	Raw string
	// Path is the path portion (no leading '@').
	Path string
	// Type is the resolved kind (file, directory, invalid).
	Type MentionType
	// Content is the expanded body: file contents (possibly truncated)
	// or directory listing. Empty when Error is set.
	Content string
	// Error is a short human-readable error message when resolution
	// failed. Empty on success.
	Error string
}

// mentionPattern matches @-prefixed paths.
//
// Valid chars inside the path: letters, digits, '_', '-', '.', '/', '~'.
// The leading '@' must either be at the start of the string/line or
// preceded by whitespace or an opening bracket/paren, so that tokens
// like email addresses (`foo@bar.com`) are ignored.
var mentionPattern = regexp.MustCompile(`(^|[\s(\[{>])@([a-zA-Z0-9_\-./~][a-zA-Z0-9_\-./~]*)`)

// maxInlineFileBytes is the hard cap before a file is considered "large"
// and truncated.
const maxInlineFileBytes = 10 * 1024

// maxInlineFileLines is the line cap used when a large file is
// truncated.
const maxInlineFileLines = 200

// ExtractMentions finds all @-mentions in the given text.
//
// It recognizes forms like `@filename`, `@path/to/file.ext`, and
// trailing-slash directory mentions (`@src/`). It deliberately skips:
//
//   - Tokens in the middle of a word (e.g. `user@host.com`).
//   - `@name(` style references (e.g. `@agent(task)`), which look like
//     agent invocations rather than file paths.
//
// The returned slice preserves input order and does not deduplicate.
func ExtractMentions(text string) []Mention {
	if text == "" {
		return nil
	}

	var out []Mention
	matches := mentionPattern.FindAllStringSubmatchIndex(text, -1)
	for _, m := range matches {
		// m = [fullStart, fullEnd, leadStart, leadEnd, pathStart, pathEnd]
		pathStart, pathEnd := m[4], m[5]
		if pathStart < 0 || pathEnd <= pathStart {
			continue
		}
		path := text[pathStart:pathEnd]

		// Trim trailing punctuation that is not a valid path character
		// when it appears at the very end of the mention (e.g. a period
		// ending a sentence, or a closing bracket). We do this
		// iteratively so "see @main.go." drops the trailing dot but
		// "@main.go" keeps the dot before "go".
		path = trimTrailingPunct(path)
		if path == "" {
			continue
		}

		// Skip "@agent(task)" style: if the next character after the
		// mention in the original text is '(', treat this as an agent
		// invocation, not a file path.
		if pathStart+len(path) < len(text) && text[pathStart+len(path)] == '(' {
			continue
		}

		// Skip mentions that look like email addresses that slipped
		// past the leading-char check (paranoia guard).
		if pathStart > 0 {
			prev := text[pathStart-1]
			if isEmailChar(prev) {
				continue
			}
		}

		raw := "@" + path
		mt := MentionFile
		if strings.HasSuffix(path, "/") {
			mt = MentionDirectory
		}
		out = append(out, Mention{
			Raw:  raw,
			Path: path,
			Type: mt,
		})
	}
	return out
}

// trimTrailingPunct removes trailing characters that are almost
// certainly sentence punctuation rather than part of a path.
//
// We strip trailing '.', ',', ';', ':', '!', '?', ')', ']', '}', '>'
// — but only repeatedly from the end, and only when the preceding
// character is not itself punctuation-less. For files like "main.go"
// we must NOT drop the '.' between "main" and "go"; we only drop a
// trailing '.' when the character before it is also punctuation-adjacent
// (end of path). The simplest correct rule: strip trailing punctuation
// until either the path is empty or the new last char is alphanumeric,
// '_', '-', '/', or '~'.
func trimTrailingPunct(path string) string {
	for len(path) > 0 {
		last := path[len(path)-1]
		switch last {
		case '.', ',', ';', ':', '!', '?', ')', ']', '}', '>':
			// Only strip '.' if what's left would not leave a broken
			// extension: e.g. "main.go." -> strip to "main.go"; but
			// "main." alone is probably a sentence end, so also strip.
			// Always strip '.' here since "@main.go" has no trailing
			// dot to strip.
			path = path[:len(path)-1]
		default:
			return path
		}
	}
	return path
}

func isEmailChar(b byte) bool {
	return (b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') ||
		b == '_' || b == '-' || b == '.'
}

// ResolveMention reads the file or lists the directory referenced by m
// and fills its Content or Error fields in place.
//
// workingDir is the primary project root. extraDirs are additional
// search roots (e.g. directories added via /add-dir). A path starting
// with '~' is expanded against $HOME.
func ResolveMention(m *Mention, workingDir string, extraDirs []string) error {
	if m == nil {
		return fmt.Errorf("nil mention")
	}

	full, err := resolvePath(m.Path, workingDir, extraDirs)
	if err != nil {
		m.Type = MentionInvalid
		m.Error = err.Error()
		m.Content = fmt.Sprintf("(file not found: %s)", m.Path)
		return err
	}

	info, statErr := os.Stat(full)
	if statErr != nil {
		m.Type = MentionInvalid
		m.Error = statErr.Error()
		m.Content = fmt.Sprintf("(file not found: %s)", m.Path)
		return statErr
	}

	if info.IsDir() {
		m.Type = MentionDirectory
		listing, lerr := listDirectory(full)
		if lerr != nil {
			m.Error = lerr.Error()
			m.Content = fmt.Sprintf("(cannot list directory: %s)", m.Path)
			return lerr
		}
		m.Content = listing
		return nil
	}

	// Regular file.
	m.Type = MentionFile
	data, rerr := os.ReadFile(full)
	if rerr != nil {
		m.Error = rerr.Error()
		m.Content = fmt.Sprintf("(cannot read file: %s)", m.Path)
		return rerr
	}
	if isBinary(data) {
		m.Content = "(binary file)"
		return nil
	}
	m.Content = truncateFileBody(string(data))
	return nil
}

// resolvePath finds the first existing path for p, trying (in order):
//
//  1. `~` expansion if p starts with `~`.
//  2. Absolute paths as-is.
//  3. workingDir + p.
//  4. each extraDir + p.
//
// Returns the resolved absolute path, or an error if none exists.
func resolvePath(p string, workingDir string, extraDirs []string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty path")
	}

	// Strip a single trailing slash for directory mentions so Stat
	// works consistently.
	clean := strings.TrimSuffix(p, "/")
	if clean == "" {
		clean = p
	}

	// ~ expansion
	if strings.HasPrefix(clean, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			expanded := filepath.Join(home, strings.TrimPrefix(clean, "~"))
			if _, err := os.Stat(expanded); err == nil {
				return expanded, nil
			}
		}
	}

	// Absolute path
	if filepath.IsAbs(clean) {
		if _, err := os.Stat(clean); err == nil {
			return clean, nil
		}
		return "", fmt.Errorf("not found: %s", clean)
	}

	// workingDir-relative
	if workingDir != "" {
		candidate := filepath.Join(workingDir, clean)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	// extraDirs-relative
	for _, d := range extraDirs {
		if d == "" {
			continue
		}
		candidate := filepath.Join(d, clean)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("not found: %s", p)
}

// listDirectory returns a formatted multi-line listing of dir entries,
// separating directories (suffixed with "/") from files.
func listDirectory(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, "\n"), nil
}

// truncateFileBody trims the body of a file if it exceeds the
// configured size/line caps and appends a "(truncated, N more lines)"
// marker when it does.
func truncateFileBody(body string) string {
	if len(body) <= maxInlineFileBytes {
		return body
	}
	lines := strings.Split(body, "\n")
	if len(lines) <= maxInlineFileLines {
		// Byte limit exceeded but line count small: just byte-truncate.
		return body[:maxInlineFileBytes] + "\n... (truncated)"
	}
	head := strings.Join(lines[:maxInlineFileLines], "\n")
	remaining := len(lines) - maxInlineFileLines
	return fmt.Sprintf("%s\n... (truncated, %d more lines)", head, remaining)
}

// isBinary returns true if the given bytes look like non-text content.
// The heuristic: presence of a NUL byte in the first 8KB.
func isBinary(data []byte) bool {
	limit := len(data)
	if limit > 8192 {
		limit = 8192
	}
	for i := 0; i < limit; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

// ExpandMentions replaces @-mentions in the text with an appendix
// containing the resolved contents, returning both the expanded text
// and the resolved mentions.
//
// The original text is kept intact; the resolved content is appended
// after a "---" separator so that the AI sees both the user's prompt
// and the inlined file bodies, while the display layer can still show
// the raw input.
//
// This is the function the CLI adapter calls on Enter to enrich the
// outgoing prompt with the contents of any @file and @dir/ tokens.
func ExpandMentions(text string, workingDir string, extraDirs []string) (string, []Mention) {
	mentions := ExtractMentions(text)
	if len(mentions) == 0 {
		return text, nil
	}

	resolved := make([]Mention, 0, len(mentions))
	for i := range mentions {
		m := mentions[i]
		_ = ResolveMention(&m, workingDir, extraDirs)
		resolved = append(resolved, m)
	}

	appendix := FormatExpandedMentions(resolved)
	if appendix == "" {
		return text, resolved
	}
	return text + "\n\n---\n" + appendix + "---", resolved
}

// FormatExpandedMentions renders resolved mentions into a single block
// suitable for appending to a prompt. Files are wrapped in fenced code
// blocks tagged with a language hint based on extension; directories
// are listed under a "[Diretorio]" header.
func FormatExpandedMentions(mentions []Mention) string {
	if len(mentions) == 0 {
		return ""
	}
	var b strings.Builder
	for _, m := range mentions {
		switch m.Type {
		case MentionFile:
			lang := MatchExtension(m.Path)
			b.WriteString(fmt.Sprintf("[Conteudo do arquivo %s]\n", m.Path))
			b.WriteString("```")
			b.WriteString(lang)
			b.WriteString("\n")
			b.WriteString(m.Content)
			if !strings.HasSuffix(m.Content, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("```\n\n")
		case MentionDirectory:
			b.WriteString(fmt.Sprintf("[Diretorio %s]\n", m.Path))
			b.WriteString(m.Content)
			if !strings.HasSuffix(m.Content, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("\n")
		case MentionInvalid:
			b.WriteString(fmt.Sprintf("[%s] %s\n\n", m.Path, m.Content))
		}
	}
	return b.String()
}

// MatchExtension returns a language identifier suitable for a Markdown
// fenced code block based on the file extension. Returns an empty
// string for unknown or missing extensions.
func MatchExtension(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "tsx"
	case ".jsx":
		return "jsx"
	case ".rs":
		return "rust"
	case ".c", ".h":
		return "c"
	case ".cc", ".cpp", ".cxx", ".hpp":
		return "cpp"
	case ".java":
		return "java"
	case ".kt", ".kts":
		return "kotlin"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	case ".sh", ".bash", ".zsh":
		return "bash"
	case ".fish":
		return "fish"
	case ".ps1":
		return "powershell"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	case ".xml":
		return "xml"
	case ".html", ".htm":
		return "html"
	case ".css":
		return "css"
	case ".scss", ".sass":
		return "scss"
	case ".sql":
		return "sql"
	case ".md", ".markdown":
		return "markdown"
	case ".swift":
		return "swift"
	case ".lua":
		return "lua"
	case ".r":
		return "r"
	case ".dart":
		return "dart"
	case ".ex", ".exs":
		return "elixir"
	case ".erl":
		return "erlang"
	case ".hs":
		return "haskell"
	case ".clj", ".cljs":
		return "clojure"
	case ".scala":
		return "scala"
	case ".proto":
		return "protobuf"
	case ".dockerfile":
		return "dockerfile"
	case ".tf":
		return "terraform"
	case ".mk":
		return "makefile"
	}
	// Filename-based fallbacks.
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case "dockerfile":
		return "dockerfile"
	case "makefile":
		return "makefile"
	}
	return ""
}
