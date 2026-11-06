// Package magicdocs auto-generates contextual documentation from project
// structure. It scans README files, manifests (go.mod, package.json),
// configuration files, and other common project artefacts to produce a
// structured summary of the project.
package magicdocs

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// DocSource represents a single documentation source discovered in the project.
type DocSource struct {
	Path    string // relative path from root
	Type    string // readme, config, manifest, changelog
	Content string // raw file content (may be truncated)
}

// ProjectDocs holds the aggregated documentation extracted from a project.
type ProjectDocs struct {
	Name          string
	Language      string
	Framework     string
	Dependencies  []string
	BuildCommands []string
	TestCommands  []string
	Sources       []DocSource
	Summary       string
}

// ---------------------------------------------------------------------------
// Scanner
// ---------------------------------------------------------------------------

// Scanner walks a project root and collects documentation sources.
type Scanner struct {
	rootDir string
	logger  *slog.Logger
}

// NewScanner returns a Scanner rooted at rootDir.  If logger is nil a
// no-op logger is used.
func NewScanner(rootDir string, logger *slog.Logger) *Scanner {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))
	}
	return &Scanner{rootDir: rootDir, logger: logger}
}

// maxSourceBytes limits how much of each file we keep in memory.
const maxSourceBytes = 64 * 1024

// ---------------------------------------------------------------------------
// Scan
// ---------------------------------------------------------------------------

// knownFiles maps relative paths (or globs) to their DocSource type.
// Order matters: first match wins.
var knownFiles = []struct {
	pattern string
	srcType string
}{
	// Readmes
	{"README.md", "readme"},
	{"README", "readme"},
	{"readme.md", "readme"},

	// Manifests
	{"go.mod", "manifest"},
	{"go.sum", "manifest"},
	{"package.json", "manifest"},
	{"Cargo.toml", "manifest"},
	{"pyproject.toml", "manifest"},
	{"requirements.txt", "manifest"},
	{"pom.xml", "manifest"},
	{"build.gradle", "manifest"},
	{"Gemfile", "manifest"},

	// Config files
	{"Makefile", "config"},
	{"Dockerfile", "config"},
	{"docker-compose.yml", "config"},
	{"docker-compose.yaml", "config"},
	{".github/workflows", "config"}, // directory — handled specially
	{"CLAUDE.md", "config"},
	{".env.example", "config"},
	{"tsconfig.json", "config"},
	{".eslintrc.json", "config"},
	{".goreleaser.yml", "config"},
	{".goreleaser.yaml", "config"},

	// Changelogs
	{"CHANGELOG.md", "changelog"},
	{"CHANGES.md", "changelog"},
	{"HISTORY.md", "changelog"},
}

// Scan walks the project root, discovers documentation sources, and returns
// a populated ProjectDocs.  The context is respected for cancellation.
func (s *Scanner) Scan(ctx context.Context) (*ProjectDocs, error) {
	docs := &ProjectDocs{}

	// Resolve project name from directory basename.
	abs, err := filepath.Abs(s.rootDir)
	if err != nil {
		return nil, fmt.Errorf("magicdocs: resolve root: %w", err)
	}
	docs.Name = filepath.Base(abs)

	// Collect sources.
	for _, kf := range knownFiles {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		full := filepath.Join(s.rootDir, kf.pattern)
		info, statErr := os.Stat(full)
		if statErr != nil {
			continue
		}

		if info.IsDir() {
			// For directories (e.g. .github/workflows) collect all YAML files inside.
			s.collectDir(ctx, full, kf.pattern, kf.srcType, docs)
			continue
		}

		content, readErr := readFileTruncated(full, maxSourceBytes)
		if readErr != nil {
			s.logger.Warn("magicdocs: read failed", "path", full, "err", readErr)
			continue
		}

		docs.Sources = append(docs.Sources, DocSource{
			Path:    kf.pattern,
			Type:    kf.srcType,
			Content: content,
		})
	}

	// Derive metadata from collected sources.
	docs.Language = DetectLanguage(s.rootDir)
	s.extractManifestInfo(docs)
	docs.Framework = DetectFramework(docs)
	docs.Summary = GenerateSummary(docs)

	return docs, nil
}

// collectDir reads YAML/YML files inside a directory and adds them as sources.
func (s *Scanner) collectDir(ctx context.Context, dir, relBase, srcType string, docs *ProjectDocs) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		s.logger.Warn("magicdocs: readdir failed", "dir", dir, "err", err)
		return
	}
	for _, e := range entries {
		if ctx.Err() != nil {
			return
		}
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".yml" && ext != ".yaml" {
			continue
		}
		full := filepath.Join(dir, e.Name())
		content, readErr := readFileTruncated(full, maxSourceBytes)
		if readErr != nil {
			continue
		}
		rel := filepath.Join(relBase, e.Name())
		docs.Sources = append(docs.Sources, DocSource{
			Path:    rel,
			Type:    srcType,
			Content: content,
		})
	}
}

// ---------------------------------------------------------------------------
// extractManifestInfo populates Dependencies, BuildCommands, TestCommands
// from the collected manifest and config sources.
// ---------------------------------------------------------------------------

func (s *Scanner) extractManifestInfo(docs *ProjectDocs) {
	for _, src := range docs.Sources {
		switch {
		case src.Path == "go.mod":
			s.parseGoMod(src.Content, docs)
		case src.Path == "package.json":
			s.parsePackageJSON(src.Content, docs)
		case src.Path == "Makefile":
			s.parseMakefile(src.Content, docs)
		case src.Path == "requirements.txt":
			s.parseRequirementsTxt(src.Content, docs)
		}
	}
}

func (s *Scanner) parseGoMod(content string, docs *ProjectDocs) {
	inRequire := false
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "require (" {
			inRequire = true
			continue
		}
		if inRequire && line == ")" {
			inRequire = false
			continue
		}
		if inRequire && line != "" && !strings.HasPrefix(line, "//") {
			parts := strings.Fields(line)
			if len(parts) >= 1 {
				docs.Dependencies = append(docs.Dependencies, parts[0])
			}
		}
		if strings.HasPrefix(line, "module ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				docs.Name = parts[1]
			}
		}
	}
}

func (s *Scanner) parsePackageJSON(content string, docs *ProjectDocs) {
	// Lightweight JSON extraction — avoids pulling in encoding/json for a
	// best-effort parse.  We look for "dependencies" and "scripts" blocks.
	extractBlock := func(key string) map[string]string {
		m := make(map[string]string)
		idx := strings.Index(content, `"`+key+`"`)
		if idx < 0 {
			return m
		}
		braceStart := strings.Index(content[idx:], "{")
		if braceStart < 0 {
			return m
		}
		braceStart += idx
		depth := 0
		var block strings.Builder
		for i := braceStart; i < len(content); i++ {
			ch := content[i]
			if ch == '{' {
				depth++
			} else if ch == '}' {
				depth--
			}
			block.WriteByte(ch)
			if depth == 0 {
				break
			}
		}
		// Parse simple key-value pairs.
		raw := block.String()
		raw = strings.Trim(raw, "{}")
		for _, entry := range strings.Split(raw, ",") {
			entry = strings.TrimSpace(entry)
			parts := strings.SplitN(entry, ":", 2)
			if len(parts) != 2 {
				continue
			}
			k := strings.Trim(strings.TrimSpace(parts[0]), `"`)
			v := strings.Trim(strings.TrimSpace(parts[1]), `"`)
			if k != "" {
				m[k] = v
			}
		}
		return m
	}

	// Name
	if idx := strings.Index(content, `"name"`); idx >= 0 {
		after := content[idx+len(`"name"`):]
		colonIdx := strings.Index(after, ":")
		if colonIdx >= 0 {
			after = after[colonIdx+1:]
			after = strings.TrimSpace(after)
			if len(after) > 0 && after[0] == '"' {
				end := strings.Index(after[1:], `"`)
				if end >= 0 {
					docs.Name = after[1 : end+1]
				}
			}
		}
	}

	for dep := range extractBlock("dependencies") {
		docs.Dependencies = append(docs.Dependencies, dep)
	}
	for dep := range extractBlock("devDependencies") {
		docs.Dependencies = append(docs.Dependencies, dep)
	}

	scripts := extractBlock("scripts")
	if v, ok := scripts["build"]; ok {
		docs.BuildCommands = append(docs.BuildCommands, "npm run build → "+v)
	}
	if v, ok := scripts["test"]; ok {
		docs.TestCommands = append(docs.TestCommands, "npm test → "+v)
	}
}

func (s *Scanner) parseMakefile(content string, docs *ProjectDocs) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, ":") || (strings.Contains(line, ":") && !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, "#")) {
			target := strings.SplitN(line, ":", 2)[0]
			target = strings.TrimSpace(target)
			if target == "" || strings.ContainsAny(target, " $%") {
				continue
			}
			switch {
			case strings.Contains(target, "build"), strings.Contains(target, "compile"):
				docs.BuildCommands = append(docs.BuildCommands, "make "+target)
			case strings.Contains(target, "test"), strings.Contains(target, "check"):
				docs.TestCommands = append(docs.TestCommands, "make "+target)
			}
		}
	}
}

func (s *Scanner) parseRequirementsTxt(content string, docs *ProjectDocs) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		// Strip version specifier.
		for _, sep := range []string{"==", ">=", "<=", "~=", "!="} {
			if idx := strings.Index(line, sep); idx > 0 {
				line = line[:idx]
				break
			}
		}
		docs.Dependencies = append(docs.Dependencies, strings.TrimSpace(line))
	}
}

// ---------------------------------------------------------------------------
// DetectLanguage — heuristic from file extensions in rootDir.
// ---------------------------------------------------------------------------

// DetectLanguage inspects the top-level and first level of subdirectories for
// common source file extensions and returns the dominant language name.
func DetectLanguage(rootDir string) string {
	counts := map[string]int{}
	extLang := map[string]string{
		".go":    "Go",
		".ts":    "TypeScript",
		".tsx":   "TypeScript",
		".js":    "JavaScript",
		".jsx":   "JavaScript",
		".py":    "Python",
		".rs":    "Rust",
		".java":  "Java",
		".rb":    "Ruby",
		".cs":    "C#",
		".cpp":   "C++",
		".c":     "C",
		".swift": "Swift",
		".kt":    "Kotlin",
	}

	walkMax := 2000 // avoid scanning huge trees
	n := 0
	_ = filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		n++
		if n > walkMax {
			return filepath.SkipAll
		}
		// Skip hidden dirs and vendor/node_modules.
		if d.IsDir() {
			base := d.Name()
			if strings.HasPrefix(base, ".") || base == "vendor" || base == "node_modules" || base == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if lang, ok := extLang[ext]; ok {
			counts[lang]++
		}
		return nil
	})

	if len(counts) == 0 {
		return "Unknown"
	}

	// Return the language with the most files.
	best := ""
	bestN := 0
	for lang, c := range counts {
		if c > bestN {
			best = lang
			bestN = c
		}
	}
	return best
}

// ---------------------------------------------------------------------------
// DetectFramework — from dependencies and config files.
// ---------------------------------------------------------------------------

// DetectFramework tries to identify the project's primary framework from its
// dependencies and configuration files.
func DetectFramework(docs *ProjectDocs) string {
	depSet := make(map[string]struct{}, len(docs.Dependencies))
	for _, d := range docs.Dependencies {
		depSet[d] = struct{}{}
	}

	// Check well-known framework dependencies.
	frameworkHints := []struct {
		dep       string
		framework string
	}{
		// Go
		{"github.com/gin-gonic/gin", "Gin"},
		{"github.com/labstack/echo/v4", "Echo"},
		{"github.com/gofiber/fiber/v2", "Fiber"},
		{"github.com/charmbracelet/bubbletea", "Bubble Tea"},
		{"github.com/gorilla/mux", "Gorilla Mux"},
		{"github.com/go-chi/chi", "Chi"},
		// JS/TS
		{"next", "Next.js"},
		{"react", "React"},
		{"vue", "Vue"},
		{"@angular/core", "Angular"},
		{"express", "Express"},
		{"fastify", "Fastify"},
		{"svelte", "Svelte"},
		{"nuxt", "Nuxt"},
		// Python
		{"django", "Django"},
		{"flask", "Flask"},
		{"fastapi", "FastAPI"},
		// Rust
		{"actix-web", "Actix Web"},
		{"axum", "Axum"},
		{"rocket", "Rocket"},
		// Ruby
		{"rails", "Rails"},
	}

	for _, hint := range frameworkHints {
		if _, ok := depSet[hint.dep]; ok {
			return hint.framework
		}
	}

	// Fallback: look at config sources for clues.
	for _, src := range docs.Sources {
		if src.Type != "config" {
			continue
		}
		low := strings.ToLower(src.Content)
		switch {
		case strings.Contains(low, "next.config"):
			return "Next.js"
		case strings.Contains(low, "angular.json"):
			return "Angular"
		}
	}

	return ""
}

// ---------------------------------------------------------------------------
// GenerateSummary — builds a concise markdown summary.
// ---------------------------------------------------------------------------

// GenerateSummary builds a concise project summary in markdown from the
// collected ProjectDocs.
func GenerateSummary(docs *ProjectDocs) string {
	var sb strings.Builder

	sb.WriteString("# Project Summary\n\n")
	fmt.Fprintf(&sb, "**Name:** %s\n\n", docs.Name)

	if docs.Language != "" && docs.Language != "Unknown" {
		fmt.Fprintf(&sb, "**Language:** %s\n\n", docs.Language)
	}
	if docs.Framework != "" {
		fmt.Fprintf(&sb, "**Framework:** %s\n\n", docs.Framework)
	}

	// Dependencies
	if len(docs.Dependencies) > 0 {
		sb.WriteString("## Dependencies\n\n")
		sorted := make([]string, len(docs.Dependencies))
		copy(sorted, docs.Dependencies)
		sort.Strings(sorted)
		for _, d := range sorted {
			fmt.Fprintf(&sb, "- %s\n", d)
		}
		sb.WriteString("\n")
	}

	// Build commands
	if len(docs.BuildCommands) > 0 {
		sb.WriteString("## Build\n\n")
		for _, cmd := range docs.BuildCommands {
			fmt.Fprintf(&sb, "- `%s`\n", cmd)
		}
		sb.WriteString("\n")
	}

	// Test commands
	if len(docs.TestCommands) > 0 {
		sb.WriteString("## Test\n\n")
		for _, cmd := range docs.TestCommands {
			fmt.Fprintf(&sb, "- `%s`\n", cmd)
		}
		sb.WriteString("\n")
	}

	// Sources overview
	if len(docs.Sources) > 0 {
		sb.WriteString("## Documentation Sources\n\n")
		typeGroups := map[string][]string{}
		for _, src := range docs.Sources {
			typeGroups[src.Type] = append(typeGroups[src.Type], src.Path)
		}
		typeOrder := []string{"readme", "manifest", "config", "changelog"}
		for _, t := range typeOrder {
			paths, ok := typeGroups[t]
			if !ok {
				continue
			}
			fmt.Fprintf(&sb, "### %s\n\n", strings.Title(t))
			for _, p := range paths {
				fmt.Fprintf(&sb, "- `%s`\n", p)
			}
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func readFileTruncated(path string, maxBytes int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	buf := make([]byte, maxBytes)
	n, _ := f.Read(buf)
	return string(buf[:n]), nil
}
