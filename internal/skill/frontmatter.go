package skill

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Frontmatter holds the parsed YAML metadata from a SKILL.md file.
//
// The TS reference (src/skills/loadSkillsDir.ts) supports many more keys;
// this Go port keeps the core subset that the runtime actually reads, and
// is permissive: unknown keys are ignored, not errors.
type Frontmatter struct {
	Name             string   `yaml:"name"`
	Description      string   `yaml:"description"`
	Model            string   `yaml:"model"`
	ExecutionContext string   `yaml:"context"`
	Aliases          []string `yaml:"aliases"`
	AllowedTools     []string `yaml:"allowed-tools"`
	DisallowedTools  []string `yaml:"disallowed-tools"`
	Visibility       string   `yaml:"visibility"` // public|private
	Version          string   `yaml:"version"`
}

// ParsedSkill is the result of parsing a single on-disk skill file: the
// frontmatter metadata, the markdown body that follows it, and the source
// path (absolute when possible) the file was loaded from.
type ParsedSkill struct {
	Frontmatter Frontmatter
	Body        string
	SourcePath  string
}

// Validate enforces the minimum invariants expected by the rest of the
// skill pipeline: a name and a description are both required, and if the
// caller set a Model it must not be blank whitespace.
func (f Frontmatter) Validate() error {
	if strings.TrimSpace(f.Name) == "" {
		return fmt.Errorf("frontmatter: name is required")
	}
	if strings.TrimSpace(f.Description) == "" {
		return fmt.Errorf("frontmatter: description is required")
	}
	if f.Model != "" && strings.TrimSpace(f.Model) == "" {
		return fmt.Errorf("frontmatter: model set but empty")
	}
	if f.Visibility != "" && f.Visibility != "public" && f.Visibility != "private" {
		return fmt.Errorf("frontmatter: visibility must be 'public' or 'private', got %q", f.Visibility)
	}
	return nil
}

// fenceMarker is the three-dash fence used by YAML frontmatter.
var fenceMarker = []byte("---")

// ParseSkillFile splits a raw SKILL.md byte slice into frontmatter and body.
//
// Rules (mirroring the TS loader):
//  1. If the first line is "---", everything up to the next line that is
//     exactly "---" is parsed as YAML frontmatter and the remainder is the
//     body.
//  2. If no frontmatter fence is present, the Frontmatter is returned zeroed
//     and the entire content is returned as Body. No error.
//  3. Invalid YAML between fences is a hard error.
func ParseSkillFile(path string, content []byte) (*ParsedSkill, error) {
	ps := &ParsedSkill{SourcePath: path}

	// Strip an optional UTF-8 BOM so the fence detection below is simple.
	trimmed := bytes.TrimPrefix(content, []byte("\xef\xbb\xbf"))

	// Must start with a `---` line — allow an optional leading CR for CRLF.
	if !startsWithFence(trimmed) {
		ps.Body = string(trimmed)
		return ps, nil
	}

	// Advance past the opening fence line.
	afterOpen := trimmed[len(fenceMarker):]
	// Consume the rest of the opening line (up to and including its newline).
	if i := bytes.IndexByte(afterOpen, '\n'); i >= 0 {
		afterOpen = afterOpen[i+1:]
	} else {
		// Content is just `---` and nothing else — treat as empty frontmatter,
		// empty body.
		ps.Body = ""
		return ps, nil
	}

	// Find the closing fence line.
	closeIdx := findClosingFence(afterOpen)
	if closeIdx < 0 {
		// No closing fence: the file happens to start with `---` but is not
		// actually frontmatter. Fall back to "no frontmatter, full body".
		ps.Body = string(trimmed)
		return ps, nil
	}

	yamlBlock := afterOpen[:closeIdx]
	// Skip past the closing fence line.
	rest := afterOpen[closeIdx+len(fenceMarker):]
	if i := bytes.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[i+1:]
	} else {
		rest = nil
	}

	var fm Frontmatter
	if len(bytes.TrimSpace(yamlBlock)) > 0 {
		if err := yaml.Unmarshal(yamlBlock, &fm); err != nil {
			return nil, fmt.Errorf("parse frontmatter %s: %w", path, err)
		}
	}
	ps.Frontmatter = fm
	ps.Body = string(rest)
	return ps, nil
}

// startsWithFence reports whether content begins with a `---` line
// (optionally followed by trailing whitespace/CR before the newline).
func startsWithFence(content []byte) bool {
	if !bytes.HasPrefix(content, fenceMarker) {
		return false
	}
	tail := content[len(fenceMarker):]
	if len(tail) == 0 {
		return true
	}
	// After `---`, only whitespace then newline (or EOF) is allowed for it
	// to count as a fence line.
	for _, b := range tail {
		if b == '\n' {
			return true
		}
		if b != ' ' && b != '\t' && b != '\r' {
			return false
		}
	}
	return true
}

// findClosingFence returns the byte offset where a `---` closing fence line
// begins inside content, or -1 if none is found. The fence must be the
// entire contents of a line (allowing trailing whitespace / CR before \n).
func findClosingFence(content []byte) int {
	offset := 0
	for offset < len(content) {
		// Work line by line.
		nl := bytes.IndexByte(content[offset:], '\n')
		var line []byte
		if nl < 0 {
			line = content[offset:]
		} else {
			line = content[offset : offset+nl]
		}
		// Strip trailing CR for CRLF line endings.
		line = bytes.TrimRight(line, "\r")
		trimmedRight := bytes.TrimRight(line, " \t")
		if bytes.Equal(trimmedRight, fenceMarker) {
			return offset
		}
		if nl < 0 {
			return -1
		}
		offset += nl + 1
	}
	return -1
}
