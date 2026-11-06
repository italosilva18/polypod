package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// MemoryFile represents a project memory file (CLAUDE.md, AGENTS.md, etc.)
// discovered on the filesystem.
type MemoryFile struct {
	Path    string
	Content string
	Type    MemoryFileType
	Source  MemoryFileSource
	Imports []string  // Other files imported via @file
	Size    int64     // Size in bytes
	ModTime time.Time // Last modification time
}

// MemoryFileType classifies a memory file by its naming convention.
type MemoryFileType string

const (
	MemoryProject MemoryFileType = "project" // Project-wide
	MemoryUser    MemoryFileType = "user"    // User-specific
	MemoryAgent   MemoryFileType = "agent"   // Agent-specific
	MemoryGeneric MemoryFileType = "generic"
)

// MemoryFileSource indicates where the file was discovered.
type MemoryFileSource string

const (
	SourceProjectRoot MemoryFileSource = "project_root"
	SourceParentDir   MemoryFileSource = "parent_dir"
	SourceHomeDir     MemoryFileSource = "home_dir"
	SourceXDGConfig   MemoryFileSource = "xdg_config"
)

// maxImportDepth is the recursion limit for @file imports.
const maxImportDepth = 5

// importRegex matches @<path> tokens, where path can include directories,
// dots, dashes and underscores. The path stops at whitespace.
var importRegex = regexp.MustCompile(`(?m)(?:^|\s)@([\w./\-]+)`)

// MemoryFileNames returns the list of recognized memory file names in priority order.
// Files earlier in the list take precedence over later ones when found in the
// same directory.
func MemoryFileNames() []string {
	return []string{
		".polypod.md", // polypod-specific (highest priority)
		"AGENTS.md",   // generic agents
		"CLAUDE.md",   // claude-style
		".cursor.md",  // cursor-style
		".aider.md",   // aider-style
		".windsurf.md",
	}
}

// classifyMemoryFile assigns a MemoryFileType based on the file name.
func classifyMemoryFile(name string) MemoryFileType {
	switch strings.ToLower(name) {
	case ".polypod.md":
		return MemoryProject
	case "agents.md":
		return MemoryAgent
	case "claude.md", ".cursor.md", ".aider.md", ".windsurf.md":
		return MemoryGeneric
	default:
		return MemoryGeneric
	}
}

// DetectMemoryFiles searches for memory files starting from workingDir going up
// to the user's home directory. It also checks ~/.polypod and the XDG config
// directory. Files are returned in priority order (project files first, then
// user files).
func DetectMemoryFiles(workingDir string) ([]MemoryFile, error) {
	if workingDir == "" {
		var err error
		workingDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get working dir: %w", err)
		}
	}
	absWD, err := filepath.Abs(workingDir)
	if err != nil {
		return nil, fmt.Errorf("absolute workingDir: %w", err)
	}

	home, _ := os.UserHomeDir()
	names := MemoryFileNames()
	var results []MemoryFile
	seen := make(map[string]bool)

	// 1. Project root
	if files := scanDirForMemoryFiles(absWD, names, SourceProjectRoot); len(files) > 0 {
		for _, f := range files {
			if !seen[f.Path] {
				seen[f.Path] = true
				results = append(results, f)
			}
		}
	}

	// 2. Parent directories up to (and including) the home directory
	current := filepath.Dir(absWD)
	for current != "" && current != "/" && current != filepath.Dir(current) {
		// Stop scanning once we've crossed above the user's home dir.
		if home != "" && !strings.HasPrefix(current, home) && current != home {
			break
		}
		files := scanDirForMemoryFiles(current, names, SourceParentDir)
		for _, f := range files {
			if !seen[f.Path] {
				seen[f.Path] = true
				results = append(results, f)
			}
		}
		if current == home {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	// 3. ~/.polypod/AGENTS.md (user-wide)
	if home != "" {
		userPath := filepath.Join(home, ".polypod", "AGENTS.md")
		if mf, err := loadIfExists(userPath, SourceHomeDir); err == nil && mf != nil {
			if !seen[mf.Path] {
				seen[mf.Path] = true
				results = append(results, *mf)
			}
		}
	}

	// 4. XDG config: ~/.config/polypod/AGENTS.md
	xdgHome := os.Getenv("XDG_CONFIG_HOME")
	if xdgHome == "" && home != "" {
		xdgHome = filepath.Join(home, ".config")
	}
	if xdgHome != "" {
		xdgPath := filepath.Join(xdgHome, "polypod", "AGENTS.md")
		if mf, err := loadIfExists(xdgPath, SourceXDGConfig); err == nil && mf != nil {
			if !seen[mf.Path] {
				seen[mf.Path] = true
				results = append(results, *mf)
			}
		}
	}

	return results, nil
}

// scanDirForMemoryFiles checks a single directory for any of the recognized
// memory file names and returns them in priority order.
func scanDirForMemoryFiles(dir string, names []string, source MemoryFileSource) []MemoryFile {
	var found []MemoryFile
	for _, name := range names {
		path := filepath.Join(dir, name)
		mf, err := loadIfExists(path, source)
		if err != nil || mf == nil {
			continue
		}
		found = append(found, *mf)
	}
	return found
}

// loadIfExists loads a memory file if it exists, returning nil if not found.
func loadIfExists(path string, source MemoryFileSource) (*MemoryFile, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if info.IsDir() {
		return nil, nil
	}
	mf, err := LoadMemoryFile(path)
	if err != nil {
		return nil, err
	}
	mf.Source = source
	mf.Size = info.Size()
	mf.ModTime = info.ModTime()
	return mf, nil
}

// LoadMemoryFile reads a memory file and resolves @imports relative to its
// own directory. Errors during import resolution are non-fatal: the base file
// is still returned.
func LoadMemoryFile(path string) (*MemoryFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read memory file %s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat memory file %s: %w", path, err)
	}

	mf := &MemoryFile{
		Path:    path,
		Content: string(data),
		Type:    classifyMemoryFile(filepath.Base(path)),
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}

	// Resolve @imports best-effort.
	_ = ResolveImports(mf, filepath.Dir(path))

	return mf, nil
}

// CombineMemoryFiles concatenates multiple memory files into a single context
// block suitable for prepending to a system prompt. Each file is preceded by a
// header indicating its source path.
func CombineMemoryFiles(files []MemoryFile) string {
	if len(files) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("# PROJECT MEMORY\n\n")
	for i, f := range files {
		if i > 0 {
			sb.WriteString("\n---\n\n")
		}
		fmt.Fprintf(&sb, "## %s (%s)\n\n", filepath.Base(f.Path), f.Source)
		sb.WriteString(strings.TrimSpace(f.Content))
		sb.WriteString("\n")
		// Append imported content if any was inlined into Imports field
		// (Imports holds the list of imported file paths, not their content).
	}
	return sb.String()
}

// ResolveImports recursively follows @file references in memory file content.
// It populates file.Imports with the list of resolved import paths and inlines
// the imported content into file.Content. baseDir is the directory used to
// resolve relative import paths. Recursion is bounded by maxImportDepth and
// cycles are prevented via a visited set.
func ResolveImports(file *MemoryFile, baseDir string) error {
	visited := make(map[string]bool)
	visited[file.Path] = true
	resolved, imports, err := resolveImportsRec(file.Content, baseDir, visited, 0)
	if err != nil {
		return err
	}
	file.Content = resolved
	file.Imports = imports
	return nil
}

// resolveImportsRec is the worker that walks @imports recursively up to
// maxImportDepth, returning the expanded content and the list of imported
// file paths.
func resolveImportsRec(content, baseDir string, visited map[string]bool, depth int) (string, []string, error) {
	if depth >= maxImportDepth {
		return content, nil, nil
	}

	matches := importRegex.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return content, nil, nil
	}

	var imports []string
	var sb strings.Builder
	last := 0

	for _, m := range matches {
		// m[0]:m[1] is the full match (may include leading whitespace),
		// m[2]:m[3] is the path capture group.
		fullStart, fullEnd := m[0], m[1]
		pathStart, pathEnd := m[2], m[3]
		importPath := content[pathStart:pathEnd]

		// Skip if it doesn't look like a file reference
		if !looksLikeFilePath(importPath) {
			continue
		}

		// Resolve relative to baseDir
		resolvedPath := importPath
		if !filepath.IsAbs(resolvedPath) {
			resolvedPath = filepath.Join(baseDir, importPath)
		}
		absPath, err := filepath.Abs(resolvedPath)
		if err != nil {
			continue
		}

		// Cycle prevention
		if visited[absPath] {
			continue
		}

		// Read imported file
		data, err := os.ReadFile(absPath)
		if err != nil {
			// Non-fatal: leave the @ref alone.
			continue
		}

		// Recursively resolve imports inside the imported file
		visited[absPath] = true
		nestedDir := filepath.Dir(absPath)
		nestedContent, nestedImports, _ := resolveImportsRec(string(data), nestedDir, visited, depth+1)

		// Write everything before this match
		sb.WriteString(content[last:fullStart])
		// Preserve a newline if the original match started with whitespace
		if fullStart < pathStart {
			sb.WriteString(content[fullStart:pathStart-1] + "\n")
		}
		fmt.Fprintf(&sb, "\n<!-- imported from %s -->\n", importPath)
		sb.WriteString(nestedContent)
		sb.WriteString("\n<!-- end import -->\n")

		last = fullEnd

		imports = append(imports, absPath)
		imports = append(imports, nestedImports...)
	}
	sb.WriteString(content[last:])

	// Deduplicate imports while preserving order
	imports = dedupStrings(imports)
	sort.Strings(imports)
	return sb.String(), imports, nil
}

// looksLikeFilePath returns true if the captured @ token plausibly refers to a
// file (contains a slash or a dot+extension), avoiding false positives like
// @user mentions.
func looksLikeFilePath(s string) bool {
	if s == "" {
		return false
	}
	if strings.Contains(s, "/") {
		return true
	}
	// Has an extension like name.md, name.txt
	if dot := strings.LastIndex(s, "."); dot > 0 && dot < len(s)-1 {
		return true
	}
	return false
}

// dedupStrings removes duplicate strings preserving the first occurrence order.
func dedupStrings(in []string) []string {
	if len(in) == 0 {
		return in
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// FormatDetectionReport renders a human-readable summary of detected memory
// files showing source, path and size. Useful for debug commands.
func FormatDetectionReport(files []MemoryFile) string {
	if len(files) == 0 {
		return "No memory files detected.\n"
	}
	var sb strings.Builder
	var total int64
	sb.WriteString("Memory files detected:\n")
	for _, f := range files {
		fmt.Fprintf(&sb, "  %s (%s) - %s\n",
			f.Path, f.Source, formatBytes(f.Size))
		total += f.Size
	}
	fmt.Fprintf(&sb, "\nTotal: %s across %d files\n", formatBytes(total), len(files))
	return sb.String()
}

// formatBytes returns a human-readable size like "2.3 KB" or "512 B".
func formatBytes(n int64) string {
	const (
		_  = iota
		KB = 1 << (10 * iota)
		MB
	)
	switch {
	case n >= MB:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(MB))
	case n >= KB:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(KB))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
