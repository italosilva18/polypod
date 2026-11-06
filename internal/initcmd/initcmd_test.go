package initcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper to create an empty file inside dir
func touch(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// helper to create a file with content
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestDetect_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	stack := Detect(dir)
	if stack.Language != "" {
		t.Errorf("expected empty Language, got %q", stack.Language)
	}
	if stack.Framework != "" {
		t.Errorf("expected empty Framework, got %q", stack.Framework)
	}
	if stack.HasGit || stack.HasDocker || stack.HasCI {
		t.Errorf("expected no git/docker/ci, got %+v", stack)
	}
}

func TestDetect_Go(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/foo\n\ngo 1.21\n")

	stack := Detect(dir)
	if stack.Language != "Go" {
		t.Errorf("expected Go, got %q", stack.Language)
	}
	if stack.TestCmd != "go test ./..." {
		t.Errorf("unexpected TestCmd: %q", stack.TestCmd)
	}
	if stack.LintCmd != "go vet ./..." {
		t.Errorf("unexpected LintCmd: %q", stack.LintCmd)
	}
}

func TestDetect_GoWithGin(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/foo\n\nrequire github.com/gin-gonic/gin v1.9.0\n")

	stack := Detect(dir)
	if stack.Language != "Go" {
		t.Errorf("expected Go, got %q", stack.Language)
	}
	if stack.Framework != "Gin" {
		t.Errorf("expected Gin framework, got %q", stack.Framework)
	}
}

func TestDetect_NodePackageJSON(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"name":"foo","version":"1.0.0"}`)

	stack := Detect(dir)
	if stack.Language != "JavaScript/TypeScript" {
		t.Errorf("expected JS/TS, got %q", stack.Language)
	}
	if stack.TestCmd != "npm test" {
		t.Errorf("unexpected TestCmd: %q", stack.TestCmd)
	}
}

func TestDetect_NextJS(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"name":"foo"}`)
	touch(t, dir, "next.config.js")

	stack := Detect(dir)
	if stack.Framework != "Next.js" {
		t.Errorf("expected Next.js framework, got %q", stack.Framework)
	}
}

func TestDetect_Vite(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"name":"foo"}`)
	touch(t, dir, "vite.config.ts")

	stack := Detect(dir)
	if stack.Framework != "Vite" {
		t.Errorf("expected Vite framework, got %q", stack.Framework)
	}
}

func TestDetect_Rust(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Cargo.toml", "[package]\nname = \"foo\"\nversion = \"0.1.0\"\n")

	stack := Detect(dir)
	if stack.Language != "Rust" {
		t.Errorf("expected Rust, got %q", stack.Language)
	}
	if stack.TestCmd != "cargo test" {
		t.Errorf("unexpected TestCmd: %q", stack.TestCmd)
	}
	if stack.LintCmd != "cargo clippy" {
		t.Errorf("unexpected LintCmd: %q", stack.LintCmd)
	}
}

func TestDetect_PythonRequirements(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "requirements.txt", "flask==2.0\n")

	stack := Detect(dir)
	if stack.Language != "Python" {
		t.Errorf("expected Python, got %q", stack.Language)
	}
	if stack.Framework != "Flask" {
		t.Errorf("expected Flask framework, got %q", stack.Framework)
	}
}

func TestDetect_PythonDjango(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "requirements.txt", "django==4.0\n")
	touch(t, dir, "manage.py")

	stack := Detect(dir)
	if stack.Language != "Python" {
		t.Errorf("expected Python, got %q", stack.Language)
	}
	if stack.Framework != "Django" {
		t.Errorf("expected Django framework, got %q", stack.Framework)
	}
}

func TestDetect_RubyRails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Gemfile", "source 'https://rubygems.org'\ngem 'rails'\n")

	stack := Detect(dir)
	if stack.Language != "Ruby" {
		t.Errorf("expected Ruby, got %q", stack.Language)
	}
	if stack.Framework != "Rails" {
		t.Errorf("expected Rails framework, got %q", stack.Framework)
	}
}

func TestDetect_BuildToolMakefile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module foo\n")
	touch(t, dir, "Makefile")

	stack := Detect(dir)
	if stack.BuildTool != "Make" {
		t.Errorf("expected Make build tool, got %q", stack.BuildTool)
	}
}

func TestDetect_DockerAndCI(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "Dockerfile")
	if err := os.MkdirAll(filepath.Join(dir, ".github"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	stack := Detect(dir)
	if !stack.HasDocker {
		t.Error("expected HasDocker=true")
	}
	if !stack.HasCI {
		t.Error("expected HasCI=true")
	}
	if !stack.HasGit {
		t.Error("expected HasGit=true")
	}
}

func TestGeneratePolypodMD_GoStack(t *testing.T) {
	stack := DetectedStack{
		Language:  "Go",
		Framework: "Gin",
		BuildTool: "Make",
		TestCmd:   "go test ./...",
		LintCmd:   "go vet ./...",
		HasDocker: true,
		HasCI:     true,
	}
	md := GeneratePolypodMD(stack)

	expected := []string{
		"# Polypod",
		"Linguagem: Go",
		"Framework: Gin",
		"Build: Make",
		"Deploy: Docker",
		"CI/CD: Configurado",
		"go test ./...",
		"go vet ./...",
		"## Regras",
		"## Contexto",
	}
	for _, sub := range expected {
		if !strings.Contains(md, sub) {
			t.Errorf("expected MD to contain %q, got:\n%s", sub, md)
		}
	}
}

func TestGeneratePolypodMD_EmptyStack(t *testing.T) {
	md := GeneratePolypodMD(DetectedStack{})
	if !strings.Contains(md, "# Polypod") {
		t.Error("expected header even for empty stack")
	}
	if !strings.Contains(md, "## Regras") {
		t.Error("expected Regras section even for empty stack")
	}
	if strings.Contains(md, "Linguagem:") {
		t.Error("did not expect language line for empty stack")
	}
}

func TestGenerateHooksYAML_WithLint(t *testing.T) {
	stack := DetectedStack{LintCmd: "go vet ./..."}
	yaml := GenerateHooksYAML(stack)

	if !strings.Contains(yaml, "auto-lint") {
		t.Error("expected auto-lint hook")
	}
	if !strings.Contains(yaml, "go vet ./...") {
		t.Error("expected lint command in yaml")
	}
	if !strings.Contains(yaml, "block-force-push") {
		t.Error("expected block-force-push hook")
	}
}

func TestGenerateHooksYAML_NoLint(t *testing.T) {
	yaml := GenerateHooksYAML(DetectedStack{})
	if strings.Contains(yaml, "auto-lint") {
		t.Error("did not expect auto-lint hook without LintCmd")
	}
	if !strings.Contains(yaml, "block-force-push") {
		t.Error("expected block-force-push hook always")
	}
}

func TestWriteInit_CreatesFiles(t *testing.T) {
	dir := t.TempDir()
	stack := DetectedStack{
		Language: "Go",
		TestCmd:  "go test ./...",
	}

	created, err := WriteInit(dir, stack)
	if err != nil {
		t.Fatalf("WriteInit error: %v", err)
	}
	if len(created) == 0 {
		t.Fatal("expected created files, got none")
	}

	// Verify .polypod.md exists
	mdPath := filepath.Join(dir, ".polypod.md")
	if _, err := os.Stat(mdPath); err != nil {
		t.Errorf(".polypod.md not created: %v", err)
	}

	// Verify .polypod/commands/test.md exists
	testCmdPath := filepath.Join(dir, ".polypod", "commands", "test.md")
	if _, err := os.Stat(testCmdPath); err != nil {
		t.Errorf("commands/test.md not created: %v", err)
	}

	// Verify .polypod/settings.json exists
	settingsPath := filepath.Join(dir, ".polypod", "settings.json")
	if _, err := os.Stat(settingsPath); err != nil {
		t.Errorf("settings.json not created: %v", err)
	}

	// Verify recipes dir exists
	recipesDir := filepath.Join(dir, ".polypod", "recipes")
	if info, err := os.Stat(recipesDir); err != nil || !info.IsDir() {
		t.Errorf("recipes dir not created: %v", err)
	}
}

func TestWriteInit_TestCmdContent(t *testing.T) {
	dir := t.TempDir()
	stack := DetectedStack{TestCmd: "cargo test"}

	if _, err := WriteInit(dir, stack); err != nil {
		t.Fatalf("WriteInit: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".polypod", "commands", "test.md"))
	if err != nil {
		t.Fatalf("read test.md: %v", err)
	}
	if !strings.Contains(string(data), "cargo test") {
		t.Errorf("expected test.md to contain 'cargo test', got:\n%s", data)
	}
	if !strings.Contains(string(data), "name: test") {
		t.Errorf("expected frontmatter name: test, got:\n%s", data)
	}
}

func TestWriteInit_DefaultTestCmdWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	if _, err := WriteInit(dir, DetectedStack{}); err != nil {
		t.Fatalf("WriteInit: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".polypod", "commands", "test.md"))
	if err != nil {
		t.Fatalf("read test.md: %v", err)
	}
	if !strings.Contains(string(data), "nenhum teste configurado") {
		t.Errorf("expected default test message, got:\n%s", data)
	}
}

func TestWriteInit_SettingsJSONContent(t *testing.T) {
	dir := t.TempDir()
	if _, err := WriteInit(dir, DetectedStack{}); err != nil {
		t.Fatalf("WriteInit: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".polypod", "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	expected := []string{
		"denied_tools",
		"ask_tools",
		"allowed_tools",
		"git_push",
		"delete_file",
		"read_*",
	}
	for _, sub := range expected {
		if !strings.Contains(string(data), sub) {
			t.Errorf("expected settings.json to contain %q", sub)
		}
	}
}

func TestWriteInit_DoesNotOverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	original := "# Existing content - do not overwrite\n"
	if err := os.WriteFile(filepath.Join(dir, ".polypod.md"), []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	created, err := WriteInit(dir, DetectedStack{Language: "Go"})
	if err != nil {
		t.Fatalf("WriteInit: %v", err)
	}

	// .polypod.md should NOT be in created list
	for _, p := range created {
		if filepath.Base(p) == ".polypod.md" {
			t.Errorf("WriteInit overwrote existing .polypod.md")
		}
	}

	data, err := os.ReadFile(filepath.Join(dir, ".polypod.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Errorf("expected original content preserved, got:\n%s", data)
	}
}

func TestWriteInit_IdempotentSecondCall(t *testing.T) {
	dir := t.TempDir()
	stack := DetectedStack{Language: "Go", TestCmd: "go test ./..."}

	created1, err := WriteInit(dir, stack)
	if err != nil {
		t.Fatalf("first WriteInit: %v", err)
	}
	if len(created1) == 0 {
		t.Fatal("expected files created on first call")
	}

	created2, err := WriteInit(dir, stack)
	if err != nil {
		t.Fatalf("second WriteInit: %v", err)
	}
	if len(created2) != 0 {
		t.Errorf("expected no new files on second call, got %v", created2)
	}
}
