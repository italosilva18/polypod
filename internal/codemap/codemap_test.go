package codemap

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile is a test helper that writes a file with the given content.
// It creates any necessary parent directories and fails the test on error.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

// makeGoProject creates a minimal fake Go project in dir.
func makeGoProject(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, dir, "go.mod", "module example.com/test\n\ngo 1.21\n")
	writeFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	writeFile(t, dir, "util.go", "package main\n\nfunc helper() {}\n")
}

// makeJSProject creates a minimal fake JavaScript project in dir.
func makeJSProject(t *testing.T, dir string, withReact bool) {
	t.Helper()
	pkg := `{"name":"test","version":"1.0.0","dependencies":{}}`
	if withReact {
		pkg = `{"name":"test","version":"1.0.0","dependencies":{"react":"^18.0.0"}}`
	}
	writeFile(t, dir, "package.json", pkg)
	writeFile(t, dir, "index.js", "console.log('hi');\n")
	writeFile(t, dir, "app.js", "const x = 1;\n")
}

func TestIndexProject_Empty(t *testing.T) {
	dir := t.TempDir()

	idx, err := IndexProject(dir)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	if idx == nil {
		t.Fatal("index is nil")
	}
	if idx.TotalFiles != 0 {
		t.Errorf("TotalFiles = %d; want 0", idx.TotalFiles)
	}
	if len(idx.Languages) != 0 {
		t.Errorf("Languages = %d; want 0", len(idx.Languages))
	}
	if idx.PrimaryLanguage() != "unknown" {
		t.Errorf("PrimaryLanguage = %q; want unknown", idx.PrimaryLanguage())
	}
}

func TestIndexProject_Go(t *testing.T) {
	dir := t.TempDir()
	makeGoProject(t, dir)

	idx, err := IndexProject(dir)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	if idx == nil {
		t.Fatal("index is nil")
	}
	if idx.PrimaryLanguage() != "go" {
		t.Errorf("PrimaryLanguage = %q; want go", idx.PrimaryLanguage())
	}
	if _, ok := idx.Languages["go"]; !ok {
		t.Errorf("expected go in Languages, got %v", idx.Languages)
	}
	if idx.Languages["go"].FileCount != 2 {
		t.Errorf("go FileCount = %d; want 2", idx.Languages["go"].FileCount)
	}
	if !idx.HasBuildSystem("Go Modules") {
		t.Errorf("expected Go Modules build system; got %v", idx.BuildSystems)
	}
}

func TestIndexProject_JavaScript(t *testing.T) {
	dir := t.TempDir()
	makeJSProject(t, dir, false)

	idx, err := IndexProject(dir)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	if idx.PrimaryLanguage() != "javascript" {
		t.Errorf("PrimaryLanguage = %q; want javascript", idx.PrimaryLanguage())
	}
	if !idx.HasBuildSystem("npm/Node.js") {
		t.Errorf("expected npm/Node.js build system; got %v", idx.BuildSystems)
	}
}

func TestIndexProject_MixedLanguage(t *testing.T) {
	dir := t.TempDir()
	// More Go files than Python
	writeFile(t, dir, "main.go", "package main\n")
	writeFile(t, dir, "a.go", "package main\n")
	writeFile(t, dir, "b.go", "package main\n")
	writeFile(t, dir, "script.py", "print('hi')\n")

	idx, err := IndexProject(dir)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	if len(idx.Languages) < 2 {
		t.Errorf("expected at least 2 languages; got %d: %v", len(idx.Languages), idx.Languages)
	}
	if _, ok := idx.Languages["go"]; !ok {
		t.Errorf("expected go in Languages")
	}
	if _, ok := idx.Languages["python"]; !ok {
		t.Errorf("expected python in Languages")
	}
	if idx.PrimaryLanguage() != "go" {
		t.Errorf("PrimaryLanguage = %q; want go (majority)", idx.PrimaryLanguage())
	}
}

func TestIndexProject_ReactFramework(t *testing.T) {
	dir := t.TempDir()
	makeJSProject(t, dir, true)

	idx, err := IndexProject(dir)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	if !idx.HasFramework("React") {
		t.Errorf("expected React framework detected; got %v", idx.Frameworks)
	}
}

func TestIndexProject_LanguageDetectionByExtension(t *testing.T) {
	cases := []struct {
		ext  string
		lang string
	}{
		{".go", "go"},
		{".js", "javascript"},
		{".py", "python"},
		{".rs", "rust"},
		{".ts", "typescript"},
	}

	for _, tc := range cases {
		t.Run(tc.ext, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "file"+tc.ext, "// content\n")

			idx, err := IndexProject(dir)
			if err != nil {
				t.Fatalf("IndexProject: %v", err)
			}
			if _, ok := idx.Languages[tc.lang]; !ok {
				t.Errorf("expected language %q for ext %q; got %v", tc.lang, tc.ext, idx.Languages)
			}
			if idx.PrimaryLanguage() != tc.lang {
				t.Errorf("PrimaryLanguage = %q; want %q", idx.PrimaryLanguage(), tc.lang)
			}
		})
	}
}

func TestIndexProject_BuildSystemDetection(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		content  string
		expected string
	}{
		{"go_modules", "go.mod", "module x\n", "Go Modules"},
		{"make", "Makefile", "all:\n\techo hi\n", "Make"},
		{"npm", "package.json", `{"name":"x"}`, "npm/Node.js"},
		{"cargo", "Cargo.toml", "[package]\nname=\"x\"\n", "Cargo (Rust)"},
		{"pyproject", "pyproject.toml", "[project]\nname=\"x\"\n", "Python (pyproject)"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, tc.filename, tc.content)

			idx, err := IndexProject(dir)
			if err != nil {
				t.Fatalf("IndexProject: %v", err)
			}
			if !idx.HasBuildSystem(tc.expected) {
				t.Errorf("expected build system %q; got %v", tc.expected, idx.BuildSystems)
			}
		})
	}
}

func TestIndexProject_KeyFiles(t *testing.T) {
	dir := t.TempDir()
	makeGoProject(t, dir)
	writeFile(t, dir, "README.md", "# Test\n")
	writeFile(t, dir, "Dockerfile", "FROM scratch\n")

	idx, err := IndexProject(dir)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}

	kinds := make(map[string]bool)
	for _, kf := range idx.KeyFiles {
		kinds[kf.Kind] = true
	}
	if !kinds["entrypoint"] {
		t.Errorf("expected entrypoint key file; got %v", idx.KeyFiles)
	}
	if !kinds["readme"] {
		t.Errorf("expected readme key file; got %v", idx.KeyFiles)
	}
	if !kinds["build"] {
		t.Errorf("expected build key file; got %v", idx.KeyFiles)
	}
	if !kinds["config"] {
		t.Errorf("expected config key file (go.mod); got %v", idx.KeyFiles)
	}
}

func TestIndexProject_SkipsVendorAndNodeModules(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")
	// These should be skipped
	writeFile(t, dir, "node_modules/foo/index.js", "console.log('x');\n")
	writeFile(t, dir, "vendor/example.com/pkg/file.go", "package pkg\n")

	idx, err := IndexProject(dir)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	// Should only count the top-level main.go
	if got := idx.Languages["go"].FileCount; got != 1 {
		t.Errorf("go FileCount = %d; want 1 (vendor skipped)", got)
	}
	if _, ok := idx.Languages["javascript"]; ok {
		t.Errorf("expected no javascript (node_modules should be skipped); got %v", idx.Languages)
	}
}

func TestIndexProject_TotalCounts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package main\n\nfunc main() {}\n")
	writeFile(t, dir, "b.go", "package main\n")

	idx, err := IndexProject(dir)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	if idx.TotalFiles != 2 {
		t.Errorf("TotalFiles = %d; want 2", idx.TotalFiles)
	}
	if idx.TotalLines < 1 {
		t.Errorf("TotalLines = %d; want >= 1", idx.TotalLines)
	}
	if idx.TotalSize <= 0 {
		t.Errorf("TotalSize = %d; want > 0", idx.TotalSize)
	}
}

func TestIndexProject_RootDirAbsolute(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")

	idx, err := IndexProject(dir)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	if !filepath.IsAbs(idx.RootDir) {
		t.Errorf("RootDir = %q; want absolute path", idx.RootDir)
	}
}

func TestFormatProjectSummary_NonEmpty(t *testing.T) {
	dir := t.TempDir()
	makeGoProject(t, dir)

	idx, err := IndexProject(dir)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	summary := FormatProjectSummary(idx)
	if summary == "" {
		t.Fatal("FormatProjectSummary returned empty string")
	}
	// Sanity check - must contain some known headers
	if !contains(summary, "Project Summary") {
		t.Errorf("summary missing 'Project Summary' header; got:\n%s", summary)
	}
	if !contains(summary, "Languages") {
		t.Errorf("summary missing 'Languages' section; got:\n%s", summary)
	}
}

func TestFormatProjectSummary_Empty(t *testing.T) {
	dir := t.TempDir()
	idx, err := IndexProject(dir)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	summary := FormatProjectSummary(idx)
	if summary == "" {
		t.Fatal("FormatProjectSummary should not return empty even for empty project")
	}
	if !contains(summary, "Project Summary") {
		t.Errorf("summary missing 'Project Summary' header")
	}
}

func TestProjectIndex_HasBuildSystem_CaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module x\n")

	idx, err := IndexProject(dir)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	if !idx.HasBuildSystem("go modules") {
		t.Errorf("HasBuildSystem should be case-insensitive")
	}
	if !idx.HasBuildSystem("GO MODULES") {
		t.Errorf("HasBuildSystem should be case-insensitive")
	}
	if idx.HasBuildSystem("nonexistent") {
		t.Errorf("HasBuildSystem should return false for unknown")
	}
}

func TestProjectIndex_HasFramework_CaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	makeJSProject(t, dir, true)

	idx, err := IndexProject(dir)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	if !idx.HasFramework("react") {
		t.Errorf("HasFramework should be case-insensitive")
	}
	if idx.HasFramework("Angular") {
		t.Errorf("HasFramework should return false for missing framework")
	}
}

// contains reports whether substr is within s (avoids importing strings just for this).
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
