// Package golden provides a golden-file test helper.
package golden

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// UpdateFlag is set with -update to regenerate golden files.
var UpdateFlag = flag.Bool("update", false, "update golden files")

// Options controls golden file comparison.
type Options struct {
	// Dir overrides the default testdata/golden directory.
	Dir string
	// Ext overrides the file extension (default .txt or .json).
	Ext string
	// StripANSI removes ANSI escape sequences before compare.
	StripANSI bool
	// NormalizeLineEndings converts \r\n to \n.
	NormalizeLineEndings bool
	// JSONPretty reformats JSON content for stable comparison.
	JSONPretty bool
	// TrimTrailingSpace removes trailing whitespace on each line.
	TrimTrailingSpace bool
	// IgnorePatterns are regex patterns whose matches are replaced with placeholders.
	IgnorePatterns []IgnorePattern
}

// IgnorePattern describes a regex pattern to normalize before comparison.
type IgnorePattern struct {
	Pattern     *regexp.Regexp
	Replacement string
}

// DefaultOptions returns sensible defaults.
func DefaultOptions() Options {
	return Options{
		Dir:                  "testdata/golden",
		NormalizeLineEndings: true,
		TrimTrailingSpace:    true,
	}
}

// Assert compares got against the named golden file.
// If -update is set, the golden file is overwritten with got.
// Otherwise, mismatch calls t.Errorf with a diff.
func Assert(t *testing.T, name, got string) {
	t.Helper()
	AssertOpts(t, name, got, DefaultOptions())
}

// AssertBytes is like Assert but for byte slices.
func AssertBytes(t *testing.T, name string, got []byte) {
	t.Helper()
	AssertOpts(t, name, string(got), DefaultOptions())
}

// AssertJSON asserts against JSON content, pretty-printing both sides.
func AssertJSON(t *testing.T, name string, got any) {
	t.Helper()
	data, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	opts := DefaultOptions()
	opts.Ext = ".json"
	opts.JSONPretty = true
	AssertOpts(t, name, string(data), opts)
}

// AssertOpts is like Assert but with custom options.
func AssertOpts(t *testing.T, name, got string, opts Options) {
	t.Helper()

	dir := opts.Dir
	if dir == "" {
		dir = "testdata/golden"
	}
	ext := opts.Ext
	if ext == "" {
		ext = ".txt"
	}

	path := filepath.Join(dir, name+ext)

	// Normalize got
	gotNorm := normalize(got, opts)

	if *UpdateFlag {
		if err := writeGolden(path, gotNorm); err != nil {
			t.Fatalf("golden: write %s: %v", path, err)
		}
		t.Logf("golden: updated %s", path)
		return
	}

	// Read expected
	expected, err := readGolden(path)
	if os.IsNotExist(err) {
		if err := writeGolden(path, gotNorm); err != nil {
			t.Fatalf("golden: write initial %s: %v", path, err)
		}
		t.Logf("golden: created %s (run again to verify)", path)
		return
	}
	if err != nil {
		t.Fatalf("golden: read %s: %v", path, err)
	}

	expectedNorm := normalize(expected, opts)

	if gotNorm != expectedNorm {
		diff := simpleDiff(expectedNorm, gotNorm)
		t.Errorf("golden: mismatch in %s\n%s", path, diff)
	}
}

// normalize applies the configured normalizations.
func normalize(s string, opts Options) string {
	if opts.NormalizeLineEndings {
		s = strings.ReplaceAll(s, "\r\n", "\n")
	}
	if opts.StripANSI {
		s = stripANSI(s)
	}
	if opts.TrimTrailingSpace {
		lines := strings.Split(s, "\n")
		for i, line := range lines {
			lines[i] = strings.TrimRight(line, " \t")
		}
		s = strings.Join(lines, "\n")
	}
	for _, ign := range opts.IgnorePatterns {
		s = ign.Pattern.ReplaceAllString(s, ign.Replacement)
	}
	if opts.JSONPretty {
		var buf bytes.Buffer
		if err := json.Indent(&buf, []byte(s), "", "  "); err == nil {
			s = buf.String()
		}
	}
	return strings.TrimRight(s, "\n") + "\n"
}

// stripANSI removes ANSI escape sequences.
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSI(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

// readGolden reads a golden file.
func readGolden(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// writeGolden creates the golden file (creating parent dirs if needed).
func writeGolden(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0644)
}

// simpleDiff returns a line-level diff.
func simpleDiff(expected, got string) string {
	expLines := strings.Split(expected, "\n")
	gotLines := strings.Split(got, "\n")

	var sb strings.Builder
	sb.WriteString("--- expected\n+++ got\n")

	maxLen := len(expLines)
	if len(gotLines) > maxLen {
		maxLen = len(gotLines)
	}

	for i := 0; i < maxLen; i++ {
		var e, g string
		if i < len(expLines) {
			e = expLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}

		if e == g {
			continue
		}

		if i < len(expLines) {
			sb.WriteString(fmt.Sprintf("-%s\n", e))
		}
		if i < len(gotLines) {
			sb.WriteString(fmt.Sprintf("+%s\n", g))
		}
	}

	return sb.String()
}

// ===== Pattern helpers =====

// IgnoreTimestamps returns an IgnorePattern that replaces ISO timestamps.
func IgnoreTimestamps() IgnorePattern {
	return IgnorePattern{
		Pattern:     regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})`),
		Replacement: "<TIMESTAMP>",
	}
}

// IgnoreNumbers returns an IgnorePattern that replaces all numbers.
func IgnoreNumbers() IgnorePattern {
	return IgnorePattern{
		Pattern:     regexp.MustCompile(`\d+`),
		Replacement: "<N>",
	}
}

// IgnoreUUIDs returns an IgnorePattern that replaces UUIDs.
func IgnoreUUIDs() IgnorePattern {
	return IgnorePattern{
		Pattern:     regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`),
		Replacement: "<UUID>",
	}
}

// IgnorePaths returns an IgnorePattern that replaces absolute paths.
func IgnorePaths() IgnorePattern {
	return IgnorePattern{
		Pattern:     regexp.MustCompile(`/[a-zA-Z0-9_\-./]+`),
		Replacement: "<PATH>",
	}
}

// IgnoreDurations returns an IgnorePattern that replaces durations (10.5s, 3m, etc.).
func IgnoreDurations() IgnorePattern {
	return IgnorePattern{
		Pattern:     regexp.MustCompile(`\d+(\.\d+)?(ns|µs|ms|s|m|h)`),
		Replacement: "<DURATION>",
	}
}

// ===== Custom directory setup for parallel tests =====

var (
	dirMu      sync.Mutex
	currentDir string
)

// SetDir sets the default golden directory.
func SetDir(dir string) {
	dirMu.Lock()
	defer dirMu.Unlock()
	currentDir = dir
}

// GetDir returns the current default golden directory.
func GetDir() string {
	dirMu.Lock()
	defer dirMu.Unlock()
	if currentDir == "" {
		return "testdata/golden"
	}
	return currentDir
}
