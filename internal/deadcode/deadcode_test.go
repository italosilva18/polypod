package deadcode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestModule creates a minimal Go module in a temp dir and returns the path.
func setupTestModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	return dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestScan_EmptyModule(t *testing.T) {
	dir := setupTestModule(t)

	dead, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(dead) != 0 {
		t.Errorf("empty module should have no dead code; got %d: %v", len(dead), dead)
	}
}

func TestScan_UnusedExportedFunction(t *testing.T) {
	dir := setupTestModule(t)

	src := `package test

func Used() int {
	return 42
}

func Unused() int {
	return Used() + 1
}
`
	writeFile(t, dir, "file.go", src)

	dead, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	// "Unused" is only referenced by its own declaration => dead.
	foundUnused := false
	foundUsed := false
	for _, d := range dead {
		if d.Name == "Unused" {
			foundUnused = true
		}
		if d.Name == "Used" {
			foundUsed = true
		}
	}
	if !foundUnused {
		t.Errorf("'Unused' should be reported as dead; got %v", dead)
	}
	if foundUsed {
		t.Errorf("'Used' should NOT be reported; got %v", dead)
	}
}

func TestScan_FunctionReferencedByAnotherFile(t *testing.T) {
	dir := setupTestModule(t)

	writeFile(t, dir, "a.go", `package test

func Helper() int {
	return 1
}
`)
	writeFile(t, dir, "b.go", `package test

func Caller() int {
	return Helper() + 1
}

func AnotherCaller() int {
	return Caller()
}
`)

	dead, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	for _, d := range dead {
		if d.Name == "Helper" {
			t.Errorf("'Helper' is referenced by b.go; should not be dead: %v", dead)
		}
		if d.Name == "Caller" {
			t.Errorf("'Caller' is referenced by AnotherCaller; should not be dead: %v", dead)
		}
	}
}

func TestScan_MethodHasLowerConfidence(t *testing.T) {
	dir := setupTestModule(t)

	src := `package test

type Widget struct {
	Name string
}

func (w Widget) Frobnicate() string {
	return w.Name
}

func Consumer() {
	_ = Widget{Name: "x"}
}
`
	writeFile(t, dir, "file.go", src)

	dead, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	// Frobnicate is never called; should appear as dead method,
	// but with lower confidence than a plain func.
	var frob *DeadSymbol
	for i := range dead {
		if dead[i].Name == "Frobnicate" {
			frob = &dead[i]
			break
		}
	}
	if frob == nil {
		t.Fatalf("expected 'Frobnicate' in dead list; got %v", dead)
	}
	if frob.Kind != "method" {
		t.Errorf("Frobnicate.Kind = %q; want method", frob.Kind)
	}
	// Plain func baseline confidence is 0.7; method is 0.7 - 0.3 = 0.4
	if frob.Confidence >= 0.7 {
		t.Errorf("method confidence should be lower than func baseline 0.7; got %v", frob.Confidence)
	}
	if frob.Receiver == "" {
		t.Errorf("method should have a receiver; got empty")
	}
}

func TestScan_EntryPointsExcluded(t *testing.T) {
	dir := setupTestModule(t)

	src := `package test

func init() {}

func TestSomething() {}

func BenchmarkSomething() {}

func ExampleSomething() {}

func FuzzSomething() {}
`
	writeFile(t, dir, "file.go", src)

	dead, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	for _, d := range dead {
		switch d.Name {
		case "init", "TestSomething", "BenchmarkSomething", "ExampleSomething", "FuzzSomething":
			t.Errorf("entry point %q should be excluded; got dead: %v", d.Name, dead)
		}
	}
}

func TestScan_MainPackageSkipped(t *testing.T) {
	dir := setupTestModule(t)

	src := `package main

func Helper() int { return 1 }

func main() {}
`
	writeFile(t, dir, "main.go", src)

	dead, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	// main package is entirely skipped by the collector.
	if len(dead) != 0 {
		t.Errorf("main package should be skipped; got %v", dead)
	}
}

func TestScan_UnexportedExcludedByDefault(t *testing.T) {
	dir := setupTestModule(t)

	src := `package test

func unusedPrivate() int {
	return 1
}

func UsedPublic() int {
	return 2
}
`
	writeFile(t, dir, "file.go", src)

	dead, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	for _, d := range dead {
		if d.Name == "unusedPrivate" {
			t.Errorf("unexported symbols should be excluded by default; got %v", dead)
		}
	}
}

func TestScanWithConfig_IncludeUnexported(t *testing.T) {
	dir := setupTestModule(t)

	src := `package test

func unusedPrivate() int {
	return 1
}

func UsedPublic() int {
	return unusedPrivate()
}
`
	writeFile(t, dir, "file.go", src)

	cfg := DefaultConfig()
	cfg.IncludeUnexported = true

	dead, err := ScanWithConfig(dir, cfg)
	if err != nil {
		t.Fatalf("ScanWithConfig: %v", err)
	}

	// Now unexported symbols are scanned, but unusedPrivate IS referenced by UsedPublic,
	// so it should not appear. Let's instead verify truly-unused unexported is flagged.
	src2 := `package test

func neverCalled() int {
	return 99
}

func AnotherPublic() int {
	return 1
}
`
	dir2 := setupTestModule(t)
	writeFile(t, dir2, "file.go", src2)

	dead2, err := ScanWithConfig(dir2, cfg)
	if err != nil {
		t.Fatalf("ScanWithConfig: %v", err)
	}
	foundPrivate := false
	for _, d := range dead2 {
		if d.Name == "neverCalled" {
			foundPrivate = true
		}
	}
	if !foundPrivate {
		t.Errorf("with IncludeUnexported=true, 'neverCalled' should be flagged; got %v", dead2)
	}

	// Also sanity-check the first scan returned without error.
	_ = dead
}

func TestScanWithConfig_CustomSkipDirs(t *testing.T) {
	dir := setupTestModule(t)

	// Top-level file with a dead symbol.
	writeFile(t, dir, "top.go", `package test

func TopLevelDead() int { return 1 }
`)

	// A file inside a custom subdir that we'll tell the scanner to skip.
	writeFile(t, dir, "generated/gen.go", `package generated

func GeneratedDead() int { return 2 }
`)

	cfg := DefaultConfig()
	cfg.SkipDirs = append(cfg.SkipDirs, "generated")

	dead, err := ScanWithConfig(dir, cfg)
	if err != nil {
		t.Fatalf("ScanWithConfig: %v", err)
	}

	foundTop := false
	for _, d := range dead {
		if d.Name == "GeneratedDead" {
			t.Errorf("'generated' dir should be skipped; got %v", dead)
		}
		if d.Name == "TopLevelDead" {
			foundTop = true
		}
	}
	if !foundTop {
		t.Errorf("TopLevelDead should still be reported; got %v", dead)
	}
}

func TestScan_TestFilesSkippedByDefault(t *testing.T) {
	dir := setupTestModule(t)

	writeFile(t, dir, "file.go", `package test

func Helper() int { return 1 }
`)
	// This _test.go references Helper, but SkipTest=true by default,
	// so Helper should still look dead.
	writeFile(t, dir, "file_test.go", `package test

func useHelper() int { return Helper() }
`)

	dead, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	found := false
	for _, d := range dead {
		if d.Name == "Helper" {
			found = true
		}
	}
	if !found {
		t.Errorf("with SkipTest=true, Helper (only referenced in _test.go) should be reported; got %v", dead)
	}
}

func TestFormatReport_Empty(t *testing.T) {
	out := FormatReport(nil, 0)
	if !strings.Contains(out, "No dead code") {
		t.Errorf("FormatReport(nil) = %q; should contain 'No dead code'", out)
	}

	out2 := FormatReport([]DeadSymbol{}, 0)
	if !strings.Contains(out2, "No dead code") {
		t.Errorf("FormatReport([]) = %q; should contain 'No dead code'", out2)
	}
}

func TestFormatReport_NonEmpty(t *testing.T) {
	dead := []DeadSymbol{
		{
			Package:    "foo",
			Name:       "Bar",
			Kind:       "func",
			File:       "/abs/path/foo.go",
			Line:       42,
			Confidence: 0.7,
			Reason:     "No static references found",
		},
		{
			Package:    "foo",
			Name:       "Frob",
			Kind:       "method",
			File:       "/abs/path/foo.go",
			Line:       99,
			Receiver:   "*Widget",
			Confidence: 0.4,
			Reason:     "May implement an interface; verify by checking type implementations",
		},
	}

	out := FormatReport(dead, 0)

	// Should contain file basename and line numbers.
	if !strings.Contains(out, "foo.go") {
		t.Errorf("report missing filename: %q", out)
	}
	if !strings.Contains(out, "42") {
		t.Errorf("report missing line 42: %q", out)
	}
	if !strings.Contains(out, "99") {
		t.Errorf("report missing line 99: %q", out)
	}
	if !strings.Contains(out, "Bar") {
		t.Errorf("report missing name Bar: %q", out)
	}
	// Method name should be rendered with its receiver prefix.
	if !strings.Contains(out, "*Widget.Frob") {
		t.Errorf("report should render receiver-qualified method name; got: %q", out)
	}
	// Header should show count.
	if !strings.Contains(out, "2") {
		t.Errorf("report should mention count 2: %q", out)
	}
}

func TestFormatReport_TopLimit(t *testing.T) {
	dead := []DeadSymbol{
		{Name: "A", Kind: "func", File: "a.go", Line: 1, Confidence: 0.7},
		{Name: "B", Kind: "func", File: "b.go", Line: 2, Confidence: 0.7},
		{Name: "C", Kind: "func", File: "c.go", Line: 3, Confidence: 0.7},
	}

	out := FormatReport(dead, 2)
	if !strings.Contains(out, "A") || !strings.Contains(out, "B") {
		t.Errorf("top=2 report should contain A and B; got %q", out)
	}
	if strings.Contains(out, "c.go") {
		t.Errorf("top=2 report should NOT contain c.go; got %q", out)
	}
}

func TestDefaultConfig_Defaults(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.SkipTest {
		t.Errorf("DefaultConfig.SkipTest = false; want true")
	}
	if cfg.IncludeUnexported {
		t.Errorf("DefaultConfig.IncludeUnexported = true; want false")
	}
	// Should skip at least the common noise dirs.
	wantSkips := map[string]bool{"vendor": false, "node_modules": false, ".git": false, "testdata": false}
	for _, d := range cfg.SkipDirs {
		if _, ok := wantSkips[d]; ok {
			wantSkips[d] = true
		}
	}
	for name, seen := range wantSkips {
		if !seen {
			t.Errorf("DefaultConfig.SkipDirs missing %q; got %v", name, cfg.SkipDirs)
		}
	}
}

func TestIsExported(t *testing.T) {
	cases := map[string]bool{
		"Foo":    true,
		"foo":    false,
		"":       false,
		"_Priv":  false,
		"Zebra":  true,
		"snake":  false,
		"Snake2": true,
	}
	for name, want := range cases {
		if got := isExported(name); got != want {
			t.Errorf("isExported(%q) = %v; want %v", name, got, want)
		}
	}
}

func TestIsEntryPoint(t *testing.T) {
	cases := []struct {
		sym  Symbol
		want bool
	}{
		{Symbol{Name: "main", Kind: "func"}, true},
		{Symbol{Name: "init", Kind: "func"}, true},
		{Symbol{Name: "TestFoo", Kind: "func"}, true},
		{Symbol{Name: "BenchmarkFoo", Kind: "func"}, true},
		{Symbol{Name: "ExampleFoo", Kind: "func"}, true},
		{Symbol{Name: "FuzzFoo", Kind: "func"}, true},
		{Symbol{Name: "Regular", Kind: "func"}, false},
		{Symbol{Name: "String", Kind: "method"}, true},
		{Symbol{Name: "Error", Kind: "method"}, true},
		{Symbol{Name: "ServeHTTP", Kind: "method"}, true},
		{Symbol{Name: "Custom", Kind: "method"}, false},
		{Symbol{Name: "main", Kind: "var"}, false}, // only func/method can be entry points
	}
	for _, c := range cases {
		if got := isEntryPoint(c.sym); got != c.want {
			t.Errorf("isEntryPoint(%+v) = %v; want %v", c.sym, got, c.want)
		}
	}
}

func TestComputeConfidence(t *testing.T) {
	// Plain func: baseline 0.7
	if got := computeConfidence(Symbol{Name: "F", Kind: "func"}); got != 0.7 {
		t.Errorf("func confidence = %v; want 0.7", got)
	}
	// Method: 0.7 - 0.3 = 0.4
	methodConf := computeConfidence(Symbol{Name: "M", Kind: "method"})
	if methodConf >= 0.7 {
		t.Errorf("method confidence = %v; want < 0.7", methodConf)
	}
	// Exported type: 0.7 - 0.1 = 0.6
	typeConf := computeConfidence(Symbol{Name: "T", Kind: "type"})
	if typeConf >= 0.7 {
		t.Errorf("exported type confidence = %v; want < 0.7", typeConf)
	}
	// Confidence is clamped >= 0.
	if got := computeConfidence(Symbol{Name: "x", Kind: "method"}); got < 0 {
		t.Errorf("confidence should be clamped to 0; got %v", got)
	}
}
