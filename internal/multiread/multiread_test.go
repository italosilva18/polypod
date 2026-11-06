package multiread

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/skill"
)

// getSkill registers multiread skills into a fresh registry and returns the
// named skill. It fails the test if the skill is not found.
func getSkill(t *testing.T, name string) *skill.Skill {
	t.Helper()
	reg := skill.NewRegistry()
	RegisterSkills(reg)
	s, ok := reg.Get(name)
	if !ok {
		t.Fatalf("skill %q not registered", name)
	}
	return s
}

// writeFile creates a file with the given content inside dir.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestRegisterSkills_RegistersBothSkills(t *testing.T) {
	reg := skill.NewRegistry()
	RegisterSkills(reg)

	if _, ok := reg.Get("read_files"); !ok {
		t.Error("read_files not registered")
	}
	if _, ok := reg.Get("read_dir"); !ok {
		t.Error("read_dir not registered")
	}
}

func TestReadFiles_CommaSeparatedList(t *testing.T) {
	dir := t.TempDir()
	f1 := writeFile(t, dir, "a.txt", "hello\nworld\n")
	f2 := writeFile(t, dir, "b.txt", "foo\nbar\n")

	s := getSkill(t, "read_files")
	out, err := s.Execute(map[string]string{
		"paths": f1 + "," + f2,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if !strings.Contains(out, "a.txt") {
		t.Errorf("output missing a.txt: %s", out)
	}
	if !strings.Contains(out, "b.txt") {
		t.Errorf("output missing b.txt: %s", out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("output missing 'hello': %s", out)
	}
	if !strings.Contains(out, "foo") {
		t.Errorf("output missing 'foo': %s", out)
	}
	if !strings.Contains(out, "2 arquivo(s) lido(s)") {
		t.Errorf("output missing file count summary: %s", out)
	}
}

func TestReadFiles_CommaListWithSpacesAndEmpty(t *testing.T) {
	dir := t.TempDir()
	f1 := writeFile(t, dir, "x.txt", "content-x")
	f2 := writeFile(t, dir, "y.txt", "content-y")

	s := getSkill(t, "read_files")
	out, err := s.Execute(map[string]string{
		"paths": "  " + f1 + " , ,  " + f2 + "  ",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if !strings.Contains(out, "content-x") {
		t.Errorf("missing content-x: %s", out)
	}
	if !strings.Contains(out, "content-y") {
		t.Errorf("missing content-y: %s", out)
	}
	// Empty entries should be filtered out -> 2 files
	if !strings.Contains(out, "2 arquivo(s) lido(s)") {
		t.Errorf("expected 2 files after trimming empties: %s", out)
	}
}

func TestReadFiles_GlobPattern(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "one.log", "log1")
	writeFile(t, dir, "two.log", "log2")
	writeFile(t, dir, "three.txt", "txt3")

	// Chdir so the relative glob works, restore afterwards.
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	s := getSkill(t, "read_files")
	out, err := s.Execute(map[string]string{
		"paths": "*.log",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if !strings.Contains(out, "one.log") || !strings.Contains(out, "two.log") {
		t.Errorf("glob should match both log files: %s", out)
	}
	if strings.Contains(out, "three.txt") {
		t.Errorf("glob should not match .txt file: %s", out)
	}
	if !strings.Contains(out, "log1") || !strings.Contains(out, "log2") {
		t.Errorf("content missing: %s", out)
	}
}

func TestReadFiles_GlobNoMatches(t *testing.T) {
	dir := t.TempDir()
	oldWD, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	s := getSkill(t, "read_files")
	out, err := s.Execute(map[string]string{
		"paths": "*.nonexistent",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "Nenhum arquivo encontrado") {
		t.Errorf("expected 'Nenhum arquivo encontrado', got: %s", out)
	}
}

func TestReadFiles_MaxLinesTruncation(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, "line")
	}
	f := writeFile(t, dir, "big.txt", strings.Join(lines, "\n"))

	s := getSkill(t, "read_files")
	out, err := s.Execute(map[string]string{
		"paths":     f,
		"max_lines": "5",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "truncado") {
		t.Errorf("expected truncation marker, got: %s", out)
	}
	if !strings.Contains(out, "50 linhas total") {
		t.Errorf("expected total line count in truncation marker: %s", out)
	}
}

func TestReadFiles_NoTruncationWhenUnderMax(t *testing.T) {
	dir := t.TempDir()
	f := writeFile(t, dir, "small.txt", "a\nb\nc\n")

	s := getSkill(t, "read_files")
	out, err := s.Execute(map[string]string{
		"paths":     f,
		"max_lines": "200",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out, "truncado") {
		t.Errorf("should not be truncated: %s", out)
	}
	if !strings.Contains(out, "a\nb\nc") {
		t.Errorf("content missing: %s", out)
	}
}

func TestReadFiles_SkipsDirectoriesAndMissing(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "subdir")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f := writeFile(t, dir, "real.txt", "realdata")
	missing := filepath.Join(dir, "ghost.txt")

	s := getSkill(t, "read_files")
	out, err := s.Execute(map[string]string{
		"paths": sub + "," + missing + "," + f,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if !strings.Contains(out, "realdata") {
		t.Errorf("real file missing from output: %s", out)
	}
	// Only the real file should appear in the "=== path ===" header sections
	// (directory and missing entries are silently skipped via os.Stat err/IsDir).
	if strings.Contains(out, "subdir (") {
		t.Errorf("directory should be skipped: %s", out)
	}
	// Summary counts total attempted paths, but only real file is read.
	if !strings.Contains(out, "arquivo(s) lido(s)") {
		t.Errorf("missing summary: %s", out)
	}
}

func TestReadFiles_InvalidGlobPattern(t *testing.T) {
	s := getSkill(t, "read_files")
	_, err := s.Execute(map[string]string{
		"paths": "[invalid",
	})
	if err == nil {
		t.Error("expected error for invalid glob pattern")
	}
	if err != nil && !strings.Contains(err.Error(), "glob") {
		t.Errorf("expected glob error, got: %v", err)
	}
}

func TestReadDir_ReadsAllFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "AAA")
	writeFile(t, dir, "b.txt", "BBB")
	writeFile(t, dir, "c.txt", "CCC")

	s := getSkill(t, "read_dir")
	out, err := s.Execute(map[string]string{
		"path": dir,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	for _, want := range []string{"AAA", "BBB", "CCC", "a.txt", "b.txt", "c.txt"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %s", want, out)
		}
	}
	if !strings.Contains(out, "3 arquivo(s)") {
		t.Errorf("expected 3 files in summary: %s", out)
	}
}

func TestReadDir_ExtensionFilter(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main")
	writeFile(t, dir, "helper.go", "package helper")
	writeFile(t, dir, "readme.md", "# readme")
	writeFile(t, dir, "notes.txt", "notes")

	s := getSkill(t, "read_dir")
	out, err := s.Execute(map[string]string{
		"path":      dir,
		"extension": ".go",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if !strings.Contains(out, "main.go") || !strings.Contains(out, "helper.go") {
		t.Errorf("expected .go files: %s", out)
	}
	if strings.Contains(out, "readme.md") || strings.Contains(out, "notes.txt") {
		t.Errorf("should not contain non-.go files: %s", out)
	}
	if !strings.Contains(out, "2 arquivo(s)") {
		t.Errorf("expected 2 files filtered: %s", out)
	}
}

func TestReadDir_SkipsSubdirectories(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "top.txt", "toplevel")
	sub := filepath.Join(dir, "nested")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, sub, "inner.txt", "innerdata")

	s := getSkill(t, "read_dir")
	out, err := s.Execute(map[string]string{
		"path": dir,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if !strings.Contains(out, "toplevel") {
		t.Errorf("expected top-level file content: %s", out)
	}
	if strings.Contains(out, "innerdata") {
		t.Errorf("non-recursive read should not include nested files: %s", out)
	}
	if !strings.Contains(out, "1 arquivo(s)") {
		t.Errorf("expected 1 file: %s", out)
	}
}

func TestReadDir_NonexistentPath(t *testing.T) {
	s := getSkill(t, "read_dir")
	_, err := s.Execute(map[string]string{
		"path": "/definitely/does/not/exist/xyzzy-polypod-test",
	})
	if err == nil {
		t.Error("expected error for nonexistent directory")
	}
}

func TestReadDir_MaxLinesTruncation(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	for i := 0; i < 300; i++ {
		lines = append(lines, "row")
	}
	writeFile(t, dir, "long.txt", strings.Join(lines, "\n"))

	s := getSkill(t, "read_dir")
	out, err := s.Execute(map[string]string{
		"path":      dir,
		"max_lines": "10",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "truncado") {
		t.Errorf("expected truncation marker: %s", out)
	}
}

func TestReadDir_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	s := getSkill(t, "read_dir")
	out, err := s.Execute(map[string]string{
		"path": dir,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "0 arquivo(s)") {
		t.Errorf("expected 0 files summary: %s", out)
	}
}
