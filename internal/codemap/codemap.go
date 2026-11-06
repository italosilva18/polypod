package codemap

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/costa/polypod/internal/git"
)

// ProjectIndex holds a comprehensive analysis of a project's structure.
type ProjectIndex struct {
	RootDir            string
	Languages          map[string]*LanguageStats
	KeyFiles           []KeyFile
	DirectoryStructure []DirEntry
	GitInfo            *GitInfo
	BuildSystems       []string
	Frameworks         []string
	TotalFiles         int
	TotalLines         int
	TotalSize          int64
}

// LanguageStats holds statistics for a single language.
type LanguageStats struct {
	Name      string
	FileCount int
	LineCount int
	TotalSize int64
}

// KeyFile represents a significant file in the project.
type KeyFile struct {
	Path string
	Kind string // "entrypoint", "config", "readme", "test", "build", "ci"
}

// DirEntry represents a directory in the project tree.
type DirEntry struct {
	Path      string
	FileCount int
	Depth     int
}

// GitInfo holds git-related project information.
type GitInfo struct {
	Branch    string
	RemoteURL string
	IsRepo    bool
}

// buildSystemFiles maps filenames to build system identifiers.
var buildSystemFiles = map[string]string{
	"Makefile":            "Make",
	"CMakeLists.txt":      "CMake",
	"go.mod":              "Go Modules",
	"package.json":        "npm/Node.js",
	"yarn.lock":           "Yarn",
	"pnpm-lock.yaml":      "pnpm",
	"Cargo.toml":          "Cargo (Rust)",
	"pyproject.toml":      "Python (pyproject)",
	"setup.py":            "Python (setuptools)",
	"requirements.txt":    "Python (pip)",
	"Pipfile":             "Pipenv",
	"poetry.lock":         "Poetry",
	"pom.xml":             "Maven",
	"build.gradle":        "Gradle",
	"build.gradle.kts":    "Gradle (Kotlin DSL)",
	"Gemfile":             "Bundler (Ruby)",
	"pubspec.yaml":        "Dart/Flutter",
	"composer.json":       "Composer (PHP)",
	"mix.exs":             "Mix (Elixir)",
	"build.sbt":           "sbt (Scala)",
	"Dockerfile":          "Docker",
	"docker-compose.yml":  "Docker Compose",
	"docker-compose.yaml": "Docker Compose",
	"Taskfile.yml":        "Task",
	"justfile":            "just",
	"Rakefile":            "Rake",
	"Earthfile":           "Earthly",
	"flake.nix":           "Nix",
	"meson.build":         "Meson",
	"Tiltfile":            "Tilt",
}

// frameworkIndicators maps files/patterns to framework names.
var frameworkIndicators = []struct {
	file      string
	content   string // optional content check
	framework string
}{
	// JavaScript/TypeScript
	{"next.config.js", "", "Next.js"},
	{"next.config.mjs", "", "Next.js"},
	{"next.config.ts", "", "Next.js"},
	{"nuxt.config.js", "", "Nuxt.js"},
	{"nuxt.config.ts", "", "Nuxt.js"},
	{"angular.json", "", "Angular"},
	{"svelte.config.js", "", "SvelteKit"},
	{"astro.config.mjs", "", "Astro"},
	{"remix.config.js", "", "Remix"},
	{"vite.config.js", "", "Vite"},
	{"vite.config.ts", "", "Vite"},
	{"webpack.config.js", "", "Webpack"},
	{"tailwind.config.js", "", "Tailwind CSS"},
	{"tailwind.config.ts", "", "Tailwind CSS"},
	// package.json checks
	{"package.json", "\"react\"", "React"},
	{"package.json", "\"vue\"", "Vue.js"},
	{"package.json", "\"svelte\"", "Svelte"},
	{"package.json", "\"express\"", "Express.js"},
	{"package.json", "\"fastify\"", "Fastify"},
	{"package.json", "\"nestjs\"", "NestJS"},
	{"package.json", "\"@nestjs/core\"", "NestJS"},
	{"package.json", "\"electron\"", "Electron"},
	// Python
	{"manage.py", "django", "Django"},
	{"app.py", "flask", "Flask"},
	{"app.py", "fastapi", "FastAPI"},
	{"pyproject.toml", "fastapi", "FastAPI"},
	{"pyproject.toml", "django", "Django"},
	{"pyproject.toml", "flask", "Flask"},
	// Go
	{"go.mod", "gin-gonic/gin", "Gin"},
	{"go.mod", "labstack/echo", "Echo"},
	{"go.mod", "gofiber/fiber", "Fiber"},
	{"go.mod", "go-chi/chi", "Chi"},
	{"go.mod", "gorilla/mux", "Gorilla Mux"},
	{"go.mod", "bubbletea", "Bubble Tea"},
	// Ruby
	{"Gemfile", "rails", "Ruby on Rails"},
	{"Gemfile", "sinatra", "Sinatra"},
	{"Gemfile", "hanami", "Hanami"},
	// Rust
	{"Cargo.toml", "actix-web", "Actix Web"},
	{"Cargo.toml", "axum", "Axum"},
	{"Cargo.toml", "rocket", "Rocket"},
	{"Cargo.toml", "tauri", "Tauri"},
	// Java
	{"pom.xml", "spring-boot", "Spring Boot"},
	{"build.gradle", "spring-boot", "Spring Boot"},
	// PHP
	{"artisan", "", "Laravel"},
	{"composer.json", "laravel", "Laravel"},
	{"composer.json", "symfony", "Symfony"},
	// Dart
	{"pubspec.yaml", "flutter", "Flutter"},
}

// keyFilePatterns maps filenames/patterns to key file kinds.
var keyFilePatterns = []struct {
	name string
	kind string
}{
	{"main.go", "entrypoint"},
	{"main.py", "entrypoint"},
	{"main.rs", "entrypoint"},
	{"main.js", "entrypoint"},
	{"main.ts", "entrypoint"},
	{"index.js", "entrypoint"},
	{"index.ts", "entrypoint"},
	{"index.tsx", "entrypoint"},
	{"app.go", "entrypoint"},
	{"app.py", "entrypoint"},
	{"app.js", "entrypoint"},
	{"app.ts", "entrypoint"},
	{"server.go", "entrypoint"},
	{"server.js", "entrypoint"},
	{"server.ts", "entrypoint"},
	{"cmd/main.go", "entrypoint"},
	{"README.md", "readme"},
	{"README.rst", "readme"},
	{"README.txt", "readme"},
	{"README", "readme"},
	{"go.mod", "config"},
	{"package.json", "config"},
	{"Cargo.toml", "config"},
	{"pyproject.toml", "config"},
	{"tsconfig.json", "config"},
	{".env", "config"},
	{".env.example", "config"},
	{"docker-compose.yml", "config"},
	{"docker-compose.yaml", "config"},
	{"Dockerfile", "build"},
	{"Makefile", "build"},
	{"Taskfile.yml", "build"},
	{".github/workflows", "ci"},
	{".gitlab-ci.yml", "ci"},
	{"Jenkinsfile", "ci"},
	{".circleci/config.yml", "ci"},
}

// IndexProject scans a project directory and builds a comprehensive ProjectIndex.
func IndexProject(rootDir string) (*ProjectIndex, error) {
	rootDir, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolving root dir: %w", err)
	}

	idx := &ProjectIndex{
		RootDir:   rootDir,
		Languages: make(map[string]*LanguageStats),
	}

	// Scan files
	err = filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			relDir, _ := filepath.Rel(rootDir, path)
			if relDir != "." {
				depth := strings.Count(relDir, string(filepath.Separator)) + 1
				if depth <= 3 { // Only track directories up to depth 3
					idx.DirectoryStructure = append(idx.DirectoryStructure, DirEntry{
						Path:  relDir,
						Depth: depth,
					})
				}
			}
			return nil
		}

		relPath, _ := filepath.Rel(rootDir, path)
		ext := filepath.Ext(path)
		lang, isCode := langExtensions[ext]

		if isCode {
			idx.TotalFiles++
			idx.TotalSize += info.Size()

			stats, ok := idx.Languages[lang]
			if !ok {
				stats = &LanguageStats{Name: lang}
				idx.Languages[lang] = stats
			}
			stats.FileCount++
			stats.TotalSize += info.Size()

			// Count lines for reasonably sized files
			if info.Size() < 1024*1024 {
				lines := countFileLines(path)
				stats.LineCount += lines
				idx.TotalLines += lines
			}
		}

		// Update directory file counts
		dir := filepath.Dir(relPath)
		for i := range idx.DirectoryStructure {
			if idx.DirectoryStructure[i].Path == dir {
				idx.DirectoryStructure[i].FileCount++
				break
			}
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking directory: %w", err)
	}

	// Detect build systems
	idx.BuildSystems = detectBuildSystems(rootDir)

	// Detect frameworks
	idx.Frameworks = detectFrameworks(rootDir)

	// Detect key files
	idx.KeyFiles = detectKeyFiles(rootDir)

	// Detect git info
	idx.GitInfo = detectGitInfo(rootDir)

	return idx, nil
}

// FormatProjectSummary generates a human-readable project summary.
func FormatProjectSummary(idx *ProjectIndex) string {
	var sb strings.Builder

	sb.WriteString("## Project Summary\n\n")
	sb.WriteString(fmt.Sprintf("Root: %s\n", idx.RootDir))
	sb.WriteString(fmt.Sprintf("Total: %d files, %d lines, %s\n\n",
		idx.TotalFiles, idx.TotalLines, formatSize(idx.TotalSize)))

	// Git info
	if idx.GitInfo != nil && idx.GitInfo.IsRepo {
		sb.WriteString("### Git\n")
		sb.WriteString(fmt.Sprintf("  Branch: %s\n", idx.GitInfo.Branch))
		if idx.GitInfo.RemoteURL != "" {
			sb.WriteString(fmt.Sprintf("  Remote: %s\n", idx.GitInfo.RemoteURL))
		}
		sb.WriteString("\n")
	}

	// Languages
	if len(idx.Languages) > 0 {
		sb.WriteString("### Languages\n")
		// Sort by file count descending
		sorted := sortedLanguages(idx.Languages)
		for _, ls := range sorted {
			pct := float64(ls.FileCount) / float64(idx.TotalFiles) * 100
			sb.WriteString(fmt.Sprintf("  %-15s %4d files  %6d lines  (%4.1f%%)\n",
				ls.Name, ls.FileCount, ls.LineCount, pct))
		}
		sb.WriteString("\n")
	}

	// Build systems
	if len(idx.BuildSystems) > 0 {
		sb.WriteString("### Build Systems\n")
		for _, bs := range idx.BuildSystems {
			sb.WriteString(fmt.Sprintf("  - %s\n", bs))
		}
		sb.WriteString("\n")
	}

	// Frameworks
	if len(idx.Frameworks) > 0 {
		sb.WriteString("### Frameworks\n")
		for _, fw := range idx.Frameworks {
			sb.WriteString(fmt.Sprintf("  - %s\n", fw))
		}
		sb.WriteString("\n")
	}

	// Key files
	if len(idx.KeyFiles) > 0 {
		sb.WriteString("### Key Files\n")
		grouped := make(map[string][]string)
		for _, kf := range idx.KeyFiles {
			grouped[kf.Kind] = append(grouped[kf.Kind], kf.Path)
		}
		kindOrder := []string{"entrypoint", "config", "build", "ci", "readme", "test"}
		for _, kind := range kindOrder {
			if files, ok := grouped[kind]; ok {
				sb.WriteString(fmt.Sprintf("  [%s]\n", kind))
				for _, f := range files {
					sb.WriteString(fmt.Sprintf("    %s\n", f))
				}
			}
		}
		sb.WriteString("\n")
	}

	// Directory structure (top-level)
	if len(idx.DirectoryStructure) > 0 {
		sb.WriteString("### Directory Structure\n")
		for _, d := range idx.DirectoryStructure {
			if d.Depth <= 2 {
				indent := strings.Repeat("  ", d.Depth)
				if d.FileCount > 0 {
					sb.WriteString(fmt.Sprintf("%s%s/ (%d files)\n", indent, filepath.Base(d.Path), d.FileCount))
				} else {
					sb.WriteString(fmt.Sprintf("%s%s/\n", indent, filepath.Base(d.Path)))
				}
			}
		}
	}

	return sb.String()
}

// PrimaryLanguage returns the most used language by file count.
func (idx *ProjectIndex) PrimaryLanguage() string {
	if len(idx.Languages) == 0 {
		return "unknown"
	}
	best := ""
	bestCount := 0
	for _, ls := range idx.Languages {
		if ls.FileCount > bestCount {
			bestCount = ls.FileCount
			best = ls.Name
		}
	}
	return best
}

// HasBuildSystem checks if a specific build system is present.
func (idx *ProjectIndex) HasBuildSystem(name string) bool {
	for _, bs := range idx.BuildSystems {
		if strings.EqualFold(bs, name) {
			return true
		}
	}
	return false
}

// HasFramework checks if a specific framework is detected.
func (idx *ProjectIndex) HasFramework(name string) bool {
	for _, fw := range idx.Frameworks {
		if strings.EqualFold(fw, name) {
			return true
		}
	}
	return false
}

// countFileLines counts the number of lines in a file efficiently.
func countFileLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		count++
	}
	return count
}

// detectBuildSystems scans for known build system files.
func detectBuildSystems(rootDir string) []string {
	seen := make(map[string]bool)
	var systems []string

	for file, system := range buildSystemFiles {
		path := filepath.Join(rootDir, file)
		if _, err := os.Stat(path); err == nil {
			if !seen[system] {
				seen[system] = true
				systems = append(systems, system)
			}
		}
	}

	sort.Strings(systems)
	return systems
}

// detectFrameworks checks for known framework indicators.
func detectFrameworks(rootDir string) []string {
	seen := make(map[string]bool)
	var frameworks []string

	// Cache file contents to avoid re-reading
	fileCache := make(map[string]string)

	for _, indicator := range frameworkIndicators {
		path := filepath.Join(rootDir, indicator.file)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}

		// File exists, check if content match is required
		if indicator.content == "" {
			// Just file existence is enough
			if !seen[indicator.framework] {
				seen[indicator.framework] = true
				frameworks = append(frameworks, indicator.framework)
			}
			continue
		}

		// Need to check file content
		if info.Size() > 512*1024 { // Skip files > 512KB
			continue
		}

		content, ok := fileCache[indicator.file]
		if !ok {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			content = string(data)
			fileCache[indicator.file] = content
		}

		if strings.Contains(content, indicator.content) {
			if !seen[indicator.framework] {
				seen[indicator.framework] = true
				frameworks = append(frameworks, indicator.framework)
			}
		}
	}

	sort.Strings(frameworks)
	return frameworks
}

// detectKeyFiles finds significant files in the project.
func detectKeyFiles(rootDir string) []KeyFile {
	var keyFiles []KeyFile

	for _, pattern := range keyFilePatterns {
		path := filepath.Join(rootDir, pattern.name)
		if info, err := os.Stat(path); err == nil {
			if info.IsDir() {
				// For directory-type checks (like .github/workflows), just note existence
				keyFiles = append(keyFiles, KeyFile{
					Path: pattern.name,
					Kind: pattern.kind,
				})
			} else {
				keyFiles = append(keyFiles, KeyFile{
					Path: pattern.name,
					Kind: pattern.kind,
				})
			}
		}
	}

	// Also check for cmd/*/main.go pattern (Go projects)
	cmdDir := filepath.Join(rootDir, "cmd")
	if entries, err := os.ReadDir(cmdDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				mainGo := filepath.Join("cmd", entry.Name(), "main.go")
				if _, err := os.Stat(filepath.Join(rootDir, mainGo)); err == nil {
					keyFiles = append(keyFiles, KeyFile{
						Path: mainGo,
						Kind: "entrypoint",
					})
				}
			}
		}
	}

	return keyFiles
}

// detectGitInfo gathers git information about the project.
func detectGitInfo(rootDir string) *GitInfo {
	if !git.IsInsideRepoAt(rootDir) {
		return &GitInfo{IsRepo: false}
	}

	info := &GitInfo{IsRepo: true}

	branch, err := git.GetCurrentBranch()
	if err == nil {
		info.Branch = branch
	}

	remote, err := git.GetRemoteURL()
	if err == nil {
		info.RemoteURL = remote
	}

	return info
}

// sortedLanguages returns languages sorted by file count descending.
func sortedLanguages(langs map[string]*LanguageStats) []*LanguageStats {
	result := make([]*LanguageStats, 0, len(langs))
	for _, ls := range langs {
		result = append(result, ls)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].FileCount > result[j].FileCount
	})
	return result
}

// formatSize formats a byte count as a human-readable string.
func formatSize(bytes int64) string {
	switch {
	case bytes >= 1024*1024*1024:
		return fmt.Sprintf("%.1f GB", float64(bytes)/1024/1024/1024)
	case bytes >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(bytes)/1024/1024)
	case bytes >= 1024:
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
