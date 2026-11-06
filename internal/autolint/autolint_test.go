package autolint

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// discardLogger returns a slog.Logger that discards all output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// chdirTemp changes the working directory to a fresh t.TempDir() and
// writes the supplied project files. It registers a cleanup that restores
// the original working directory.
func chdirTemp(t *testing.T, files map[string]string) string {
	t.Helper()

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	dir := t.TempDir()
	for name, content := range files {
		full := filepath.Join(dir, name)
		if mkErr := os.MkdirAll(filepath.Dir(full), 0o755); mkErr != nil {
			t.Fatalf("mkdir %s: %v", full, mkErr)
		}
		if wErr := os.WriteFile(full, []byte(content), 0o644); wErr != nil {
			t.Fatalf("write %s: %v", full, wErr)
		}
	}

	if chErr := os.Chdir(dir); chErr != nil {
		t.Fatalf("chdir %s: %v", dir, chErr)
	}

	t.Cleanup(func() {
		_ = os.Chdir(origWD)
	})

	return dir
}

func TestNewRunner_DefaultsAndDetection_Go(t *testing.T) {
	chdirTemp(t, map[string]string{
		"go.mod": "module example.com/foo\n\ngo 1.21\n",
	})

	r := NewRunner(Config{Enabled: true}, discardLogger())
	if r == nil {
		t.Fatal("NewRunner returned nil")
	}
	if r.config.MaxRetries != 3 {
		t.Errorf("MaxRetries default = %d, want 3", r.config.MaxRetries)
	}
	if r.config.LintCmd != "go vet ./..." {
		t.Errorf("LintCmd = %q, want Go vet", r.config.LintCmd)
	}
	if !strings.Contains(r.config.TestCmd, "go test") {
		t.Errorf("TestCmd = %q, want go test", r.config.TestCmd)
	}
}

func TestNewRunner_Detection_Node(t *testing.T) {
	chdirTemp(t, map[string]string{
		"package.json": `{"name":"x","version":"0.0.1"}`,
	})

	r := NewRunner(Config{Enabled: true}, discardLogger())
	if !strings.Contains(r.config.LintCmd, "eslint") {
		t.Errorf("LintCmd = %q, want eslint", r.config.LintCmd)
	}
	if !strings.Contains(r.config.TestCmd, "npm test") {
		t.Errorf("TestCmd = %q, want npm test", r.config.TestCmd)
	}
}

func TestNewRunner_Detection_Python(t *testing.T) {
	chdirTemp(t, map[string]string{
		"pyproject.toml": "[tool.poetry]\nname = \"x\"\n",
	})

	r := NewRunner(Config{Enabled: true}, discardLogger())
	if !strings.Contains(r.config.LintCmd, "ruff") {
		t.Errorf("LintCmd = %q, want ruff", r.config.LintCmd)
	}
	if !strings.Contains(r.config.TestCmd, "pytest") {
		t.Errorf("TestCmd = %q, want pytest", r.config.TestCmd)
	}
}

func TestNewRunner_Detection_Rust(t *testing.T) {
	chdirTemp(t, map[string]string{
		"Cargo.toml": "[package]\nname = \"x\"\n",
	})

	r := NewRunner(Config{Enabled: true}, discardLogger())
	if !strings.Contains(r.config.LintCmd, "clippy") {
		t.Errorf("LintCmd = %q, want clippy", r.config.LintCmd)
	}
	if !strings.Contains(r.config.TestCmd, "cargo test") {
		t.Errorf("TestCmd = %q, want cargo test", r.config.TestCmd)
	}
}

func TestNewRunner_NoProjectFiles(t *testing.T) {
	chdirTemp(t, map[string]string{
		"README.md": "hi",
	})

	r := NewRunner(Config{Enabled: true}, discardLogger())
	if r.config.LintCmd != "" {
		t.Errorf("LintCmd = %q, want empty", r.config.LintCmd)
	}
	if r.config.TestCmd != "" {
		t.Errorf("TestCmd = %q, want empty", r.config.TestCmd)
	}
}

func TestNewRunner_CustomMaxRetriesPreserved(t *testing.T) {
	chdirTemp(t, nil)

	r := NewRunner(Config{Enabled: true, MaxRetries: 7, LintCmd: "true", TestCmd: "true"}, discardLogger())
	if r.config.MaxRetries != 7 {
		t.Errorf("MaxRetries = %d, want 7 (user value preserved)", r.config.MaxRetries)
	}
	if r.config.LintCmd != "true" {
		t.Errorf("LintCmd = %q, want 'true' (user value preserved)", r.config.LintCmd)
	}
}

func TestAfterEdit_Disabled(t *testing.T) {
	chdirTemp(t, nil)

	r := NewRunner(Config{Enabled: false, LintCmd: "true"}, discardLogger())
	if got := r.AfterEdit([]string{"a.go"}); got != nil {
		t.Errorf("AfterEdit with Enabled=false returned %+v, want nil", got)
	}
}

func TestAfterEdit_LintPasses(t *testing.T) {
	chdirTemp(t, nil)

	r := NewRunner(Config{
		Enabled: true,
		LintCmd: "true",
	}, discardLogger())

	res := r.AfterEdit([]string{"a.go"})
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if !res.LintPassed {
		t.Errorf("LintPassed = false, want true (output=%q)", res.LintOutput)
	}
	if !res.TestPassed {
		t.Errorf("TestPassed = false, want true (RunTests disabled)")
	}
	if res.FixPrompt != "" {
		t.Errorf("FixPrompt = %q, want empty", res.FixPrompt)
	}
	if res.LintCmd != "true" {
		t.Errorf("LintCmd = %q, want 'true'", res.LintCmd)
	}
}

func TestAfterEdit_LintFailsBuildsFixPrompt(t *testing.T) {
	chdirTemp(t, nil)

	r := NewRunner(Config{
		Enabled: true,
		LintCmd: "echo lint-broken >&2; exit 1",
		AutoFix: true,
	}, discardLogger())

	res := r.AfterEdit([]string{"a.go", "b.go"})
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if res.LintPassed {
		t.Error("LintPassed = true, want false")
	}
	if !strings.Contains(res.LintOutput, "lint-broken") {
		t.Errorf("LintOutput = %q, want to contain 'lint-broken'", res.LintOutput)
	}
	if res.FixPrompt == "" {
		t.Error("FixPrompt empty, want populated (AutoFix=true)")
	}
	if !strings.Contains(res.FixPrompt, "a.go, b.go") {
		t.Errorf("FixPrompt missing files: %s", res.FixPrompt)
	}
	if !strings.Contains(res.FixPrompt, "lint-broken") {
		t.Errorf("FixPrompt missing lint output: %s", res.FixPrompt)
	}
}

func TestAfterEdit_RunTestsSkippedWhenLintFails(t *testing.T) {
	chdirTemp(t, nil)

	r := NewRunner(Config{
		Enabled:  true,
		LintCmd:  "false",
		TestCmd:  "echo should-not-run",
		RunTests: true,
	}, discardLogger())

	res := r.AfterEdit([]string{"a.go"})
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if res.LintPassed {
		t.Error("LintPassed = true, want false")
	}
	// TestPassed is set to true in the "else" branch when tests are skipped.
	if !res.TestPassed {
		t.Error("TestPassed = false, want true (tests skipped when lint fails)")
	}
	if strings.Contains(res.TestOutput, "should-not-run") {
		t.Errorf("TestOutput = %q; tests should not have executed", res.TestOutput)
	}
}

func TestAfterEdit_RunTestsExecutesWhenLintPasses(t *testing.T) {
	chdirTemp(t, nil)

	r := NewRunner(Config{
		Enabled:  true,
		LintCmd:  "true",
		TestCmd:  "echo test-ran; false",
		RunTests: true,
		AutoFix:  true,
	}, discardLogger())

	res := r.AfterEdit([]string{"a.go"})
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if !res.LintPassed {
		t.Errorf("LintPassed = false, want true")
	}
	if res.TestPassed {
		t.Error("TestPassed = true, want false")
	}
	if !strings.Contains(res.TestOutput, "test-ran") {
		t.Errorf("TestOutput = %q, want to contain 'test-ran'", res.TestOutput)
	}
	if res.FixPrompt == "" {
		t.Error("FixPrompt empty, want populated")
	}
	if !strings.Contains(res.FixPrompt, "## Tests") {
		t.Errorf("FixPrompt missing tests section: %s", res.FixPrompt)
	}
}

func TestAfterEdit_NoLintCmdMeansLintPassed(t *testing.T) {
	chdirTemp(t, nil)

	r := NewRunner(Config{Enabled: true, LintCmd: ""}, discardLogger())
	res := r.AfterEdit([]string{"a.go"})
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if !res.LintPassed {
		t.Error("LintPassed = false, want true when LintCmd is empty")
	}
	if res.LintOutput != "" {
		t.Errorf("LintOutput = %q, want empty", res.LintOutput)
	}
}

func TestFormatResult_Nil(t *testing.T) {
	if got := FormatResult(nil); got != "" {
		t.Errorf("FormatResult(nil) = %q, want empty", got)
	}
}

func TestFormatResult_AllPass(t *testing.T) {
	r := &Result{LintPassed: true, TestPassed: true}
	got := FormatResult(r)
	if !strings.Contains(got, "OK") {
		t.Errorf("FormatResult = %q, want to contain 'OK'", got)
	}
}

func TestFormatResult_LintFail(t *testing.T) {
	r := &Result{
		LintPassed: false,
		TestPassed: true,
		LintOutput: "boom",
		LintCmd:    "mylint",
	}
	got := FormatResult(r)
	if !strings.Contains(got, "auto-lint") || !strings.Contains(got, "FALHOU") {
		t.Errorf("FormatResult = %q, want auto-lint FALHOU", got)
	}
	if !strings.Contains(got, "mylint") {
		t.Errorf("FormatResult = %q, want to contain lint cmd", got)
	}
	if !strings.Contains(got, "boom") {
		t.Errorf("FormatResult = %q, want to contain lint output", got)
	}
}

func TestFormatResult_TestFail(t *testing.T) {
	r := &Result{
		LintPassed: true,
		TestPassed: false,
		TestOutput: "kaboom",
		TestCmd:    "mytest",
	}
	got := FormatResult(r)
	if !strings.Contains(got, "auto-test") || !strings.Contains(got, "FALHOU") {
		t.Errorf("FormatResult = %q, want auto-test FALHOU", got)
	}
	if !strings.Contains(got, "mytest") {
		t.Errorf("FormatResult = %q, want to contain test cmd", got)
	}
	if !strings.Contains(got, "kaboom") {
		t.Errorf("FormatResult = %q, want to contain test output", got)
	}
}

func TestFormatResult_TruncatesLongOutput(t *testing.T) {
	long := strings.Repeat("x", 1000)
	r := &Result{
		LintPassed: false,
		TestPassed: true,
		LintOutput: long,
		LintCmd:    "l",
	}
	got := FormatResult(r)
	if !strings.Contains(got, "[truncado]") {
		t.Errorf("FormatResult did not truncate long output: %q", got)
	}
}

func TestTruncateHelper(t *testing.T) {
	if got := truncate("abc", 10); got != "abc" {
		t.Errorf("truncate short = %q, want 'abc'", got)
	}
	long := strings.Repeat("y", 20)
	got := truncate(long, 5)
	if !strings.HasPrefix(got, "yyyyy") {
		t.Errorf("truncate prefix = %q", got)
	}
	if !strings.Contains(got, "[truncado]") {
		t.Errorf("truncate missing marker: %q", got)
	}
}

func TestFileExists(t *testing.T) {
	chdirTemp(t, map[string]string{
		"present.txt": "hello",
	})

	if !fileExists("present.txt") {
		t.Error("fileExists(present.txt) = false, want true")
	}
	if fileExists("missing.txt") {
		t.Error("fileExists(missing.txt) = true, want false")
	}
}
