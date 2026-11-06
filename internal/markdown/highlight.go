package markdown

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// defaultStyle is the Chroma style used for terminal highlighting. We
// prefer "monokai" for dark terminals; callers that want a different
// palette can use HighlightCodeWithStyle.
const defaultStyle = "monokai"

// terminalFormatter is the Chroma formatter name that produces true-colour
// ANSI escapes suitable for modern terminals.
const terminalFormatter = "terminal256"

var (
	highlightInitOnce sync.Once
	highlightStyle    *chroma.Style
	highlightFmt      chroma.Formatter
)

func initHighlight() {
	highlightInitOnce.Do(func() {
		highlightStyle = styles.Get(defaultStyle)
		if highlightStyle == nil {
			// Fall back through a few well-known dark styles.
			for _, name := range []string{"dracula", "nord", "native", "fruity"} {
				if s := styles.Get(name); s != nil {
					highlightStyle = s
					break
				}
			}
		}
		if highlightStyle == nil {
			highlightStyle = styles.Fallback
		}

		highlightFmt = formatters.Get(terminalFormatter)
		if highlightFmt == nil {
			highlightFmt = formatters.Fallback
		}
	})
}

// HighlightCode returns the code annotated with ANSI escape sequences for
// syntax highlighting. If language is empty it is auto-detected from the
// code contents. Any failure falls back to returning the plain code.
func HighlightCode(code, language string) string {
	if code == "" {
		return ""
	}
	initHighlight()

	var lexer chroma.Lexer
	if language != "" {
		lexer = lexers.Get(language)
	}
	if lexer == nil {
		lexer = lexers.Analyse(code)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		return code
	}

	var buf bytes.Buffer
	if err := highlightFmt.Format(&buf, highlightStyle, it); err != nil {
		return code
	}
	return buf.String()
}

// HighlightCodeWithStyle is like HighlightCode but lets the caller choose
// a Chroma style by name (e.g. "dracula", "monokai", "github-dark").
// Unknown style names fall back to the default style.
func HighlightCodeWithStyle(code, language, style string) string {
	if code == "" {
		return ""
	}
	initHighlight()

	s := styles.Get(style)
	if s == nil {
		s = highlightStyle
	}
	f := formatters.Get(terminalFormatter)
	if f == nil {
		f = formatters.Fallback
	}

	var lexer chroma.Lexer
	if language != "" {
		lexer = lexers.Get(language)
	}
	if lexer == nil {
		lexer = lexers.Analyse(code)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		return code
	}
	var buf bytes.Buffer
	if err := f.Format(&buf, s, it); err != nil {
		return code
	}
	return buf.String()
}

// DetectLanguage guesses a language identifier from a filename extension,
// falling back to Chroma's content-based analyser. Returns an empty string
// if no lexer can be identified.
func DetectLanguage(code, filename string) string {
	if filename != "" {
		if lx := lexers.Match(filename); lx != nil {
			cfg := lx.Config()
			if cfg != nil {
				return strings.ToLower(cfg.Name)
			}
		}
		// Manual fallback for a handful of common extensions not always
		// keyed by Match (e.g. dotfiles / shebang-only).
		if lx := lexerByExt(filename); lx != "" {
			return lx
		}
	}
	if code != "" {
		if lx := lexers.Analyse(code); lx != nil {
			cfg := lx.Config()
			if cfg != nil {
				return strings.ToLower(cfg.Name)
			}
		}
	}
	return ""
}

// lexerByExt maps a small set of common extensions to Chroma lexer names.
// This is a second-chance lookup for filenames lexers.Match doesn't
// classify on its own.
func lexerByExt(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
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
	case ".java":
		return "java"
	case ".c", ".h":
		return "c"
	case ".cpp", ".cc", ".cxx", ".hpp":
		return "cpp"
	case ".rb":
		return "ruby"
	case ".sh", ".bash":
		return "bash"
	case ".zsh":
		return "zsh"
	case ".fish":
		return "fish"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	case ".md", ".markdown":
		return "markdown"
	case ".html", ".htm":
		return "html"
	case ".css":
		return "css"
	case ".sql":
		return "sql"
	case ".xml":
		return "xml"
	case ".swift":
		return "swift"
	case ".kt", ".kts":
		return "kotlin"
	case ".php":
		return "php"
	case ".lua":
		return "lua"
	case ".dockerfile":
		return "docker"
	}
	base := strings.ToLower(filepath.Base(filename))
	switch base {
	case "dockerfile":
		return "docker"
	case "makefile", "gnumakefile":
		return "makefile"
	}
	return ""
}

// HighlightFile reads a file and returns its highlighted content. The
// language is auto-detected from the filename. Returns the raw file
// contents (without ANSI codes) if anything goes wrong apart from the
// read itself.
func HighlightFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	lang := DetectLanguage(string(data), path)
	return HighlightCode(string(data), lang), nil
}

// SupportedLanguages returns the sorted list of language identifiers
// Chroma knows about. Useful for UIs that want to present a picker or
// validate user-supplied language hints.
func SupportedLanguages() []string {
	names := lexers.Names(false)
	sort.Strings(names)
	return names
}
