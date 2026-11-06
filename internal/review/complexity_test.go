package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile is a small helper that fails the test on error.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// --- AnalyzeFileComplexity ---

func TestAnalyzeFileComplexity_Simple(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "simple.go")
	writeFile(t, path, `package test

func Simple() int {
	return 42
}
`)

	report, err := AnalyzeFileComplexity(path)
	if err != nil {
		t.Fatalf("AnalyzeFileComplexity: %v", err)
	}
	if report == nil {
		t.Fatal("nil report")
	}
	if report.TotalFunctions != 1 {
		t.Errorf("TotalFunctions = %d; want 1", report.TotalFunctions)
	}
	if report.MaxComplexity != 1 {
		t.Errorf("MaxComplexity = %d; want 1", report.MaxComplexity)
	}
	if report.MinComplexity != 1 {
		t.Errorf("MinComplexity = %d; want 1", report.MinComplexity)
	}
	if report.AvgComplexity != 1.0 {
		t.Errorf("AvgComplexity = %v; want 1.0", report.AvgComplexity)
	}
	if len(report.Simple) != 1 {
		t.Errorf("Simple slice len = %d; want 1", len(report.Simple))
	}
	if report.Distribution["simple"] != 1 {
		t.Errorf("Distribution[simple] = %d; want 1", report.Distribution["simple"])
	}
}

func TestAnalyzeFileComplexity_NoFunctions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.go")
	writeFile(t, path, `package test

var X = 1
`)

	report, err := AnalyzeFileComplexity(path)
	if err != nil {
		t.Fatalf("AnalyzeFileComplexity: %v", err)
	}
	if report == nil {
		t.Fatal("nil report")
	}
	if report.TotalFunctions != 0 {
		t.Errorf("TotalFunctions = %d; want 0", report.TotalFunctions)
	}
	if report.MaxComplexity != 0 {
		t.Errorf("MaxComplexity = %d; want 0", report.MaxComplexity)
	}
	if report.Distribution == nil {
		t.Error("Distribution should be non-nil even when empty")
	}
}

func TestAnalyzeFileComplexity_Complex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "complex.go")
	writeFile(t, path, `package test

func Complex(x, y int) int {
	if x > 0 {
		if y > 0 {
			return x + y
		} else if y < 0 {
			return x - y
		}
	} else if x < 0 {
		if y > 0 {
			return -x + y
		}
	}
	for i := 0; i < 10; i++ {
		if i%2 == 0 {
			x++
		}
	}
	switch {
	case x > 100:
		return 100
	case x < -100:
		return -100
	}
	return x * y
}
`)

	report, err := AnalyzeFileComplexity(path)
	if err != nil {
		t.Fatalf("AnalyzeFileComplexity: %v", err)
	}
	if report.MaxComplexity < 6 {
		t.Errorf("MaxComplexity = %d; want > 5", report.MaxComplexity)
	}
	if report.TotalFunctions != 1 {
		t.Errorf("TotalFunctions = %d; want 1", report.TotalFunctions)
	}
}

func TestAnalyzeFileComplexity_MissingFile(t *testing.T) {
	_, err := AnalyzeFileComplexity("/nonexistent/definitely/not/here.go")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestAnalyzeFileComplexity_ParseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.go")
	// Missing closing brace / invalid syntax.
	writeFile(t, path, `package test

func Broken( {
	return
`)

	_, err := AnalyzeFileComplexity(path)
	if err == nil {
		t.Error("expected error for parse error")
	}
}

// --- AnalyzeDirectoryComplexity ---

func TestAnalyzeDirectoryComplexity_Multiple(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), `package test

func A() int { return 1 }
`)
	writeFile(t, filepath.Join(dir, "b.go"), `package test

func B(x int) int {
	if x > 0 {
		return x
	}
	return -x
}
`)

	report, err := AnalyzeDirectoryComplexity(dir)
	if err != nil {
		t.Fatalf("AnalyzeDirectoryComplexity: %v", err)
	}
	if report.TotalFunctions != 2 {
		t.Errorf("TotalFunctions = %d; want 2", report.TotalFunctions)
	}
	if report.MaxComplexity < 2 {
		t.Errorf("MaxComplexity = %d; want >= 2", report.MaxComplexity)
	}
	if report.MinComplexity != 1 {
		t.Errorf("MinComplexity = %d; want 1", report.MinComplexity)
	}
}

func TestAnalyzeDirectoryComplexity_NoGoFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "readme.txt"), "hello")
	writeFile(t, filepath.Join(dir, "data.json"), "{}")

	report, err := AnalyzeDirectoryComplexity(dir)
	if err != nil {
		t.Fatalf("AnalyzeDirectoryComplexity: %v", err)
	}
	if report.TotalFunctions != 0 {
		t.Errorf("TotalFunctions = %d; want 0", report.TotalFunctions)
	}
}

func TestAnalyzeDirectoryComplexity_NotRecursive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "top.go"), `package test

func Top() int { return 1 }
`)
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Different package name in subdir so it would also parse fine alone.
	writeFile(t, filepath.Join(sub, "nested.go"), `package sub

func Nested() int { return 2 }

func AlsoNested() int { return 3 }
`)

	report, err := AnalyzeDirectoryComplexity(dir)
	if err != nil {
		t.Fatalf("AnalyzeDirectoryComplexity: %v", err)
	}
	// Only Top() should be counted - nested subdir must be skipped.
	if report.TotalFunctions != 1 {
		t.Errorf("TotalFunctions = %d; want 1 (subdir should not be walked)", report.TotalFunctions)
	}
}

// --- AnalyzeProjectComplexity ---

func TestAnalyzeProjectComplexity_Recursive(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "top.go"), `package a

func Top() int { return 1 }
`)
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	writeFile(t, filepath.Join(sub, "inner.go"), `package sub

func Inner() int { return 2 }
`)
	deep := filepath.Join(sub, "deep")
	if err := os.Mkdir(deep, 0755); err != nil {
		t.Fatalf("mkdir deep: %v", err)
	}
	writeFile(t, filepath.Join(deep, "deep.go"), `package deep

func Deep() int { return 3 }
`)

	report, err := AnalyzeProjectComplexity(root)
	if err != nil {
		t.Fatalf("AnalyzeProjectComplexity: %v", err)
	}
	if report.TotalFunctions != 3 {
		t.Errorf("TotalFunctions = %d; want 3", report.TotalFunctions)
	}
}

func TestAnalyzeProjectComplexity_IgnoresSpecialDirs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), `package main

func Main() int { return 1 }
`)

	// vendor/
	vendorDir := filepath.Join(root, "vendor")
	if err := os.Mkdir(vendorDir, 0755); err != nil {
		t.Fatalf("mkdir vendor: %v", err)
	}
	writeFile(t, filepath.Join(vendorDir, "vendored.go"), `package vendored

func Vendored() int { return 1 }

func AlsoVendored() int { return 2 }
`)

	// node_modules/
	nmDir := filepath.Join(root, "node_modules")
	if err := os.Mkdir(nmDir, 0755); err != nil {
		t.Fatalf("mkdir node_modules: %v", err)
	}
	writeFile(t, filepath.Join(nmDir, "nm.go"), `package nm

func NM() int { return 1 }
`)

	// .git/
	gitDir := filepath.Join(root, ".git")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	writeFile(t, filepath.Join(gitDir, "git.go"), `package git

func G() int { return 1 }
`)

	report, err := AnalyzeProjectComplexity(root)
	if err != nil {
		t.Fatalf("AnalyzeProjectComplexity: %v", err)
	}
	// Only Main should be counted.
	if report.TotalFunctions != 1 {
		t.Errorf("TotalFunctions = %d; want 1 (vendor/node_modules/.git must be skipped)", report.TotalFunctions)
	}
}

// --- ComplexityReport field verification ---

func TestComplexityReport_Fields(t *testing.T) {
	dir := t.TempDir()
	// Build a file with a mix of complexities.
	// - Tiny: complexity 1
	// - Small: complexity ~3
	// - Medium: complexity ~7
	writeFile(t, filepath.Join(dir, "mix.go"), `package test

func Tiny() int { return 1 }

func Small(x int) int {
	if x > 0 {
		return x
	}
	if x < 0 {
		return -x
	}
	return 0
}

func Medium(x int) int {
	if x > 0 {
		if x > 10 {
			return 10
		}
		if x > 5 {
			return 5
		}
	}
	for i := 0; i < 3; i++ {
		x += i
	}
	if x < 0 {
		return 0
	}
	return x
}
`)

	report, err := AnalyzeDirectoryComplexity(dir)
	if err != nil {
		t.Fatalf("AnalyzeDirectoryComplexity: %v", err)
	}

	if report.TotalFunctions != 3 {
		t.Errorf("TotalFunctions = %d; want 3", report.TotalFunctions)
	}
	if report.MaxComplexity <= report.MinComplexity {
		t.Errorf("MaxComplexity (%d) should exceed MinComplexity (%d)",
			report.MaxComplexity, report.MinComplexity)
	}
	if report.MinComplexity != 1 {
		t.Errorf("MinComplexity = %d; want 1", report.MinComplexity)
	}
	if report.AvgComplexity <= 0 {
		t.Errorf("AvgComplexity = %v; want > 0", report.AvgComplexity)
	}
	// Avg should be within [min, max] range.
	if report.AvgComplexity < float64(report.MinComplexity) ||
		report.AvgComplexity > float64(report.MaxComplexity) {
		t.Errorf("AvgComplexity %v outside [%d, %d]",
			report.AvgComplexity, report.MinComplexity, report.MaxComplexity)
	}
	if len(report.Distribution) == 0 {
		t.Error("Distribution should be populated")
	}
	// Sum of Simple+LowRisk+MediumRisk+HighRisk must equal TotalFunctions.
	total := len(report.Simple) + len(report.LowRisk) + len(report.MediumRisk) + len(report.HighRisk)
	if total != report.TotalFunctions {
		t.Errorf("risk bucket sum %d != TotalFunctions %d", total, report.TotalFunctions)
	}
}

func TestComplexityReport_HighRisk(t *testing.T) {
	dir := t.TempDir()
	// Build a function with very high complexity by chaining many ifs.
	var sb strings.Builder
	sb.WriteString("package test\n\nfunc Huge(x int) int {\n")
	for i := 0; i < 25; i++ {
		sb.WriteString("\tif x > 0 { x++ }\n")
	}
	sb.WriteString("\treturn x\n}\n")
	writeFile(t, filepath.Join(dir, "huge.go"), sb.String())

	report, err := AnalyzeDirectoryComplexity(dir)
	if err != nil {
		t.Fatalf("AnalyzeDirectoryComplexity: %v", err)
	}
	if len(report.HighRisk) < 1 {
		t.Errorf("HighRisk len = %d; want >= 1 (complexity should exceed 20)", len(report.HighRisk))
	}
	if report.MaxComplexity <= 20 {
		t.Errorf("MaxComplexity = %d; want > 20", report.MaxComplexity)
	}
}

// --- FormatComplexityReport ---

func TestFormatComplexityReport_Empty(t *testing.T) {
	empty := &ComplexityReport{
		Distribution: make(map[string]int),
	}
	out := FormatComplexityReport(empty)
	if out == "" {
		t.Error("format of empty report should not be empty")
	}
	if !strings.Contains(strings.ToLower(out), "no functions") {
		t.Errorf("expected 'no functions' message, got: %q", out)
	}
}

func TestFormatComplexityReport_NonEmpty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), `package test

func A() int { return 1 }

func B(x int) int {
	if x > 0 {
		return x
	}
	return 0
}
`)

	report, err := AnalyzeDirectoryComplexity(dir)
	if err != nil {
		t.Fatalf("AnalyzeDirectoryComplexity: %v", err)
	}

	out := FormatComplexityReport(report)
	if out == "" {
		t.Fatal("format output empty")
	}
	// Must mention total/avg/max/min.
	for _, needle := range []string{"Total functions", "Avg complexity", "Max complexity", "Min complexity", "Distribution"} {
		if !strings.Contains(out, needle) {
			t.Errorf("format output missing %q:\n%s", needle, out)
		}
	}
}

// --- RecommendRefactoring ---

func TestRecommendRefactoring_HighRisk(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	sb.WriteString("package test\n\nfunc Huge(x int) int {\n")
	for i := 0; i < 25; i++ {
		sb.WriteString("\tif x > 0 { x++ }\n")
	}
	sb.WriteString("\treturn x\n}\n")
	writeFile(t, filepath.Join(dir, "huge.go"), sb.String())

	report, err := AnalyzeDirectoryComplexity(dir)
	if err != nil {
		t.Fatalf("AnalyzeDirectoryComplexity: %v", err)
	}
	suggestions := RecommendRefactoring(report, 5)
	if len(suggestions) < 1 {
		t.Errorf("expected at least 1 suggestion, got %d", len(suggestions))
	}
	if len(suggestions) > 0 && !strings.Contains(suggestions[0], "Huge") {
		t.Errorf("suggestion should mention Huge, got: %q", suggestions[0])
	}
}

func TestRecommendRefactoring_NoHighRisk(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "simple.go"), `package test

func A() int { return 1 }
func B() int { return 2 }
`)

	report, err := AnalyzeDirectoryComplexity(dir)
	if err != nil {
		t.Fatalf("AnalyzeDirectoryComplexity: %v", err)
	}
	suggestions := RecommendRefactoring(report, 5)
	if len(suggestions) != 0 {
		t.Errorf("expected 0 suggestions, got %d: %v", len(suggestions), suggestions)
	}
}

func TestRecommendRefactoring_MaxSuggestionsRespected(t *testing.T) {
	dir := t.TempDir()
	// Build 4 separate very complex functions.
	var sb strings.Builder
	sb.WriteString("package test\n\n")
	for fn := 0; fn < 4; fn++ {
		sb.WriteString("func ")
		sb.WriteByte(byte('A' + fn))
		sb.WriteString("(x int) int {\n")
		for i := 0; i < 25; i++ {
			sb.WriteString("\tif x > 0 { x++ }\n")
		}
		sb.WriteString("\treturn x\n}\n\n")
	}
	writeFile(t, filepath.Join(dir, "many.go"), sb.String())

	report, err := AnalyzeDirectoryComplexity(dir)
	if err != nil {
		t.Fatalf("AnalyzeDirectoryComplexity: %v", err)
	}
	if len(report.HighRisk) < 4 {
		t.Fatalf("expected 4 high-risk functions, got %d", len(report.HighRisk))
	}

	// Limit to 2.
	suggestions := RecommendRefactoring(report, 2)
	if len(suggestions) != 2 {
		t.Errorf("with maxSuggestions=2, got %d suggestions", len(suggestions))
	}

	// Zero/negative should fall back to default 5.
	suggestionsDefault := RecommendRefactoring(report, 0)
	if len(suggestionsDefault) != 4 {
		// We only have 4 high-risk, default cap is 5, so all 4 should be returned.
		t.Errorf("with maxSuggestions=0 (default 5), got %d suggestions; want 4", len(suggestionsDefault))
	}
}
