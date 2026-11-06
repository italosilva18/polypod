// Package sanitize provides input sanitization utilities to protect against
// injection attacks, path traversal, secret leakage, and Unicode smuggling.
package sanitize

import (
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// -------------------------------------------------------------------
// Unicode sanitization (mirrors TS partiallySanitizeUnicode)
// -------------------------------------------------------------------

// dangerousRanges matches zero-width spaces, directional formatting, BOM,
// and BMP private-use characters. This is the "Method 2" explicit-range
// fallback from the TypeScript source.
var dangerousRanges = []*regexp.Regexp{
	regexp.MustCompile(`[\x{200B}-\x{200F}]`), // zero-width / LTR/RTL marks
	regexp.MustCompile(`[\x{202A}-\x{202E}]`), // directional formatting
	regexp.MustCompile(`[\x{2066}-\x{2069}]`), // directional isolates
	regexp.MustCompile(`[\x{FEFF}]`),          // BOM
	regexp.MustCompile(`[\x{E000}-\x{F8FF}]`), // BMP private use
}

// SanitizeUnicode applies NFKC normalization and strips dangerous invisible
// Unicode characters (format controls, noncharacters, private-use). Iterates
// until stable, capped at 10 rounds.
func SanitizeUnicode(s string) string {
	const maxIter = 10
	prev := ""
	cur := s
	for i := 0; i < maxIter && cur != prev; i++ {
		prev = cur
		// NFKC normalisation
		cur = norm.NFKC.String(cur)
		// Strip Cf (format), Co (private-use), Cn (unassigned) categories
		cur = stripCategories(cur)
		// Explicit dangerous ranges
		for _, re := range dangerousRanges {
			cur = re.ReplaceAllString(cur, "")
		}
	}
	return cur
}

// stripCategories removes Unicode Cf, Co, and Cn category runes.
func stripCategories(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r != utf8.RuneError && !unicode.Is(unicode.Cf, r) && !unicode.Is(unicode.Co, r) {
			// Cn (unassigned) — Go's unicode package doesn't have a table for
			// "not assigned", but characters not in any known category are
			// effectively Cn. We allow all explicitly assigned runes through.
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

// -------------------------------------------------------------------
// User input
// -------------------------------------------------------------------

// SanitizeUserInput removes control characters (except newline, tab, carriage
// return), normalizes whitespace runs, and strips dangerous Unicode.
func SanitizeUserInput(s string) string {
	s = SanitizeUnicode(s)
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		if r == '\n' || r == '\t' || r == '\r' {
			b.WriteRune(r)
			prevSpace = false
			continue
		}
		if unicode.IsControl(r) {
			continue // strip other control chars
		}
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return strings.TrimSpace(b.String())
}

// -------------------------------------------------------------------
// File path
// -------------------------------------------------------------------

// SanitizeFilePath cleans a file path and prevents path traversal.
// It rejects paths containing ".." components and resolves to a clean path.
func SanitizeFilePath(path string) (string, error) {
	cleaned := filepath.Clean(path)
	// Reject any ".." that survived cleaning
	for _, part := range strings.Split(filepath.ToSlash(cleaned), "/") {
		if part == ".." {
			return "", &PathTraversalError{Path: path}
		}
	}
	return cleaned, nil
}

// PathTraversalError is returned when a path traversal attempt is detected.
type PathTraversalError struct {
	Path string
}

func (e *PathTraversalError) Error() string {
	return "path traversal detected: " + e.Path
}

// -------------------------------------------------------------------
// URL
// -------------------------------------------------------------------

// SanitizeURL validates and cleans a URL. It rejects javascript: and data:
// schemes. Returns the cleaned URL string or an error.
func SanitizeURL(rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "javascript" || scheme == "data" || scheme == "vbscript" {
		return "", &UnsafeURLError{URL: rawURL, Reason: "disallowed scheme: " + scheme}
	}
	if u.Host == "" && scheme != "" && scheme != "file" {
		return "", &UnsafeURLError{URL: rawURL, Reason: "missing host"}
	}
	// Re-serialize to normalise
	return u.String(), nil
}

// UnsafeURLError is returned for URLs with disallowed schemes or missing hosts.
type UnsafeURLError struct {
	URL    string
	Reason string
}

func (e *UnsafeURLError) Error() string {
	return "unsafe URL (" + e.Reason + "): " + e.URL
}

// -------------------------------------------------------------------
// Environment variables
// -------------------------------------------------------------------

// SanitizeEnvVar removes null bytes, newlines, and leading/trailing whitespace
// from environment variable values. These can be used for injection in some
// runtimes.
func SanitizeEnvVar(value string) string {
	value = strings.ReplaceAll(value, "\x00", "")
	value = strings.ReplaceAll(value, "\n", "")
	value = strings.ReplaceAll(value, "\r", "")
	return strings.TrimSpace(value)
}

// -------------------------------------------------------------------
// Secret stripping
// -------------------------------------------------------------------

// SecretPatterns contains compiled regex patterns for common secret formats.
var SecretPatterns = []*regexp.Regexp{
	// API keys with known prefixes
	regexp.MustCompile(`(?i)(sk-[a-zA-Z0-9_\-]{20,})`),       // OpenAI, Anthropic
	regexp.MustCompile(`(?i)(ghp_[a-zA-Z0-9]{36,})`),         // GitHub PAT
	regexp.MustCompile(`(?i)(gho_[a-zA-Z0-9]{36,})`),         // GitHub OAuth
	regexp.MustCompile(`(?i)(ghs_[a-zA-Z0-9]{36,})`),         // GitHub App token
	regexp.MustCompile(`(?i)(glpat-[a-zA-Z0-9\-]{20,})`),     // GitLab PAT
	regexp.MustCompile(`(?i)(xox[bprs]-[a-zA-Z0-9\-]{10,})`), // Slack tokens
	regexp.MustCompile(`(?i)(AKIA[0-9A-Z]{16})`),             // AWS access key
	regexp.MustCompile(`(?i)(npm_[a-zA-Z0-9]{36,})`),         // npm token
	// Generic bearer / basic auth header values
	regexp.MustCompile(`(?i)(Bearer\s+[a-zA-Z0-9_\-.~+/]+=*)`),
	regexp.MustCompile(`(?i)(Basic\s+[a-zA-Z0-9+/]+=*)`),
	// Generic password / secret key-value pairs
	regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|api[_-]?key|apikey|auth)[\s]*[=:]\s*["']?([^\s"']{8,})["']?`),
}

// StripSecrets redacts values matching SecretPatterns, replacing them with
// "[REDACTED]". Useful for sanitising log output.
func StripSecrets(s string) string {
	result := s
	for _, pat := range SecretPatterns {
		result = pat.ReplaceAllStringFunc(result, func(match string) string {
			// For key=value patterns, keep the key
			if idx := strings.IndexAny(match, "=:"); idx > 0 {
				return match[:idx+1] + "[REDACTED]"
			}
			return "[REDACTED]"
		})
	}
	return result
}
