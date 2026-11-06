package testgen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/skill"
)

func TestDetectLanguage(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{"go file", "foo.go", "go"},
		{"python file", "foo.py", "python"},
		{"javascript file", "foo.js", "javascript"},
		{"typescript file", "foo.ts", "typescript"},
		{"jsx file", "foo.jsx", "javascript"},
		{"tsx file", "foo.tsx", "typescript"},
		{"rust file", "foo.rs", "rust"},
		{"ruby file", "foo.rb", "ruby"},
		{"java file", "foo.java", "java"},
		{"csharp file", "foo.cs", "csharp"},
		{"uppercase extension", "FOO.GO", "go"},
		{"unknown extension", "foo.xyz", "unknown"},
		{"no extension", "Makefile", "unknown"},
		{"nested path", "/abs/path/to/file.py", "python"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectLanguage(tc.path)
			if got != tc.want {
				t.Errorf("DetectLanguage(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestTestFilePath(t *testing.T) {
	cases := []struct {
		name     string
		source   string
		language string
		want     string
	}{
		{
			name:     "go simple",
			source:   "foo.go",
			language: "go",
			want:     "foo_test.go",
		},
		{
			name:     "go with directory",
			source:   filepath.Join("pkg", "sub", "foo.go"),
			language: "go",
			want:     filepath.Join("pkg", "sub", "foo_test.go"),
		},
		{
			name:     "python",
			source:   filepath.Join("src", "module.py"),
			language: "python",
			want:     filepath.Join("src", "test_module.py"),
		},
		{
			name:     "javascript",
			source:   filepath.Join("app", "index.js"),
			language: "javascript",
			want:     filepath.Join("app", "index.test.js"),
		},
		{
			name:     "typescript",
			source:   filepath.Join("app", "index.ts"),
			language: "typescript",
			want:     filepath.Join("app", "index.test.ts"),
		},
		{
			name:     "rust",
			source:   filepath.Join("src", "lib.rs"),
			language: "rust",
			want:     filepath.Join("src", "lib_test.rs"),
		},
		{
			name:     "unknown language fallback",
			source:   filepath.Join("src", "foo.rb"),
			language: "ruby",
			want:     filepath.Join("src", "foo_test.rb"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TestFilePath(tc.source, tc.language)
			if got != tc.want {
				t.Errorf("TestFilePath(%q, %q) = %q, want %q",
					tc.source, tc.language, got, tc.want)
			}
		})
	}
}

func TestGenerateTestPromptGo(t *testing.T) {
	content := "package foo\nfunc Add(a, b int) int { return a + b }"
	prompt := GenerateTestPrompt("add.go", content, "go")

	mustContain := []string{
		"Gere testes unitarios",
		"Regras para Go:",
		"table-driven",
		"add_test.go",
		"Arquivo: add.go",
		"```go",
		content,
		"Retorne APENAS",
	}
	for _, s := range mustContain {
		if !strings.Contains(prompt, s) {
			t.Errorf("prompt missing %q; got:\n%s", s, prompt)
		}
	}
}

func TestGenerateTestPromptPython(t *testing.T) {
	content := "def add(a, b): return a + b"
	prompt := GenerateTestPrompt("add.py", content, "python")

	mustContain := []string{
		"Regras para Python:",
		"pytest",
		"test_add.py",
		"```python",
		content,
	}
	for _, s := range mustContain {
		if !strings.Contains(prompt, s) {
			t.Errorf("python prompt missing %q", s)
		}
	}
	// should not include Go-specific rules
	if strings.Contains(prompt, "table-driven") {
		t.Error("python prompt should not contain go-specific rules")
	}
}

func TestGenerateTestPromptJSTS(t *testing.T) {
	jsPrompt := GenerateTestPrompt("app.js", "function x(){}", "javascript")
	if !strings.Contains(jsPrompt, "app.test.js") {
		t.Errorf("js prompt missing app.test.js filename; got:\n%s", jsPrompt)
	}
	if !strings.Contains(jsPrompt, "describe/it") {
		t.Error("js prompt missing describe/it instructions")
	}

	tsPrompt := GenerateTestPrompt("app.ts", "function x(){}", "typescript")
	if !strings.Contains(tsPrompt, "app.test.ts") {
		t.Errorf("ts prompt missing app.test.ts filename; got:\n%s", tsPrompt)
	}

	rustPrompt := GenerateTestPrompt("lib.rs", "fn foo(){}", "rust")
	if !strings.Contains(rustPrompt, "#[cfg(test)]") {
		t.Error("rust prompt missing #[cfg(test)] instruction")
	}
}

func TestGenerateTestPromptUnknownLanguage(t *testing.T) {
	content := "some code"
	prompt := GenerateTestPrompt("file.xyz", content, "brainfuck")

	// unknown languages should still produce a baseline prompt
	if !strings.Contains(prompt, "Gere testes unitarios") {
		t.Error("baseline header missing for unknown language")
	}
	if !strings.Contains(prompt, "Arquivo: file.xyz") {
		t.Error("filename line missing")
	}
	if !strings.Contains(prompt, content) {
		t.Error("source content missing from prompt")
	}
	// no language-specific rules should appear
	forbidden := []string{"Regras para Go:", "Regras para Python:", "Regras para JS/TS:", "Regras para Rust:"}
	for _, s := range forbidden {
		if strings.Contains(prompt, s) {
			t.Errorf("unknown-language prompt should not contain %q", s)
		}
	}
}

func TestRegisterSkillsAddsTestGenerate(t *testing.T) {
	reg := skill.NewRegistry()
	RegisterSkills(reg)

	s, ok := reg.Get("test_generate")
	if !ok {
		t.Fatal("test_generate skill not registered")
	}
	if s.Name != "test_generate" {
		t.Errorf("unexpected skill name: %s", s.Name)
	}
	if s.Description == "" {
		t.Error("skill description should not be empty")
	}
	if s.Execute == nil {
		t.Error("skill Execute func should not be nil")
	}
}

func TestRegisterSkillsExecuteSuccess(t *testing.T) {
	reg := skill.NewRegistry()
	RegisterSkills(reg)

	dir := t.TempDir()
	goFile := filepath.Join(dir, "calc.go")
	src := "package calc\n\nfunc Add(a, b int) int { return a + b }\n"
	if err := os.WriteFile(goFile, []byte(src), 0644); err != nil {
		t.Fatalf("writing fake source: %v", err)
	}

	argsJSON, _ := json.Marshal(map[string]string{"path": goFile})
	out, err := reg.Execute("test_generate", string(argsJSON))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	expectedTestPath := filepath.Join(dir, "calc_test.go")
	mustContain := []string{
		"[TEST_GENERATE]",
		"fonte: " + goFile,
		"testes: " + expectedTestPath,
		"linguagem: go",
		"Regras para Go:",
		src,
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q; got:\n%s", s, out)
		}
	}
}

func TestRegisterSkillsExecuteFileNotFound(t *testing.T) {
	reg := skill.NewRegistry()
	RegisterSkills(reg)

	dir := t.TempDir()
	missing := filepath.Join(dir, "ghost.go")

	argsJSON, _ := json.Marshal(map[string]string{"path": missing})
	out, err := reg.Execute("test_generate", string(argsJSON))
	// Registry.Execute wraps skill errors into the output string, returning nil err.
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !strings.Contains(out, "Erro:") {
		t.Errorf("expected error message in output; got: %s", out)
	}
	if !strings.Contains(out, "lendo arquivo") {
		t.Errorf("expected 'lendo arquivo' context in error; got: %s", out)
	}
}

func TestRegisterSkillsExecuteUnknownLanguage(t *testing.T) {
	reg := skill.NewRegistry()
	RegisterSkills(reg)

	dir := t.TempDir()
	weird := filepath.Join(dir, "data.xyz")
	if err := os.WriteFile(weird, []byte("content"), 0644); err != nil {
		t.Fatalf("writing fake source: %v", err)
	}

	argsJSON, _ := json.Marshal(map[string]string{"path": weird})
	out, err := reg.Execute("test_generate", string(argsJSON))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !strings.Contains(out, "linguagem nao reconhecida") {
		t.Errorf("expected unknown language error in output; got: %s", out)
	}
}

func TestRegisterSkillsPythonFlow(t *testing.T) {
	reg := skill.NewRegistry()
	RegisterSkills(reg)

	dir := t.TempDir()
	pyFile := filepath.Join(dir, "mod.py")
	src := "def add(a, b):\n    return a + b\n"
	if err := os.WriteFile(pyFile, []byte(src), 0644); err != nil {
		t.Fatalf("writing fake python source: %v", err)
	}

	argsJSON, _ := json.Marshal(map[string]string{"path": pyFile})
	out, err := reg.Execute("test_generate", string(argsJSON))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	expectedTestPath := filepath.Join(dir, "test_mod.py")
	if !strings.Contains(out, "linguagem: python") {
		t.Errorf("expected python language in output; got: %s", out)
	}
	if !strings.Contains(out, "testes: "+expectedTestPath) {
		t.Errorf("expected test path %q in output; got: %s", expectedTestPath, out)
	}
	if !strings.Contains(out, "pytest") {
		t.Errorf("expected pytest reference in output; got: %s", out)
	}
}
