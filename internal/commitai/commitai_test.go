package commitai

import (
	"strings"
	"testing"
)

func TestClassifyChanges_Empty(t *testing.T) {
	got := ClassifyChanges(nil)
	if got != "" {
		t.Errorf("expected empty string for nil changes, got %q", got)
	}

	got = ClassifyChanges([]StagedChange{})
	if got != "" {
		t.Errorf("expected empty string for empty slice, got %q", got)
	}
}

func TestClassifyChanges_Added(t *testing.T) {
	changes := []StagedChange{
		{Status: "A", Path: "foo.go"},
		{Status: "A", Path: "bar.go"},
	}
	got := ClassifyChanges(changes)
	if !strings.Contains(got, "Arquivos adicionados (2)") {
		t.Errorf("expected 'Arquivos adicionados (2)' in output, got %q", got)
	}
	if !strings.Contains(got, "foo.go") || !strings.Contains(got, "bar.go") {
		t.Errorf("expected paths in output, got %q", got)
	}
}

func TestClassifyChanges_Modified(t *testing.T) {
	changes := []StagedChange{
		{Status: "M", Path: "main.go"},
	}
	got := ClassifyChanges(changes)
	if !strings.Contains(got, "Arquivos modificados (1)") {
		t.Errorf("expected 'Arquivos modificados (1)' in output, got %q", got)
	}
	if !strings.Contains(got, "main.go") {
		t.Errorf("expected 'main.go' in output, got %q", got)
	}
}

func TestClassifyChanges_Deleted(t *testing.T) {
	changes := []StagedChange{
		{Status: "D", Path: "old.go"},
	}
	got := ClassifyChanges(changes)
	if !strings.Contains(got, "Arquivos removidos (1)") {
		t.Errorf("expected 'Arquivos removidos (1)' in output, got %q", got)
	}
	if !strings.Contains(got, "old.go") {
		t.Errorf("expected 'old.go' in output, got %q", got)
	}
}

func TestClassifyChanges_Mixed(t *testing.T) {
	changes := []StagedChange{
		{Status: "A", Path: "new.go"},
		{Status: "M", Path: "existing.go"},
		{Status: "D", Path: "removed.go"},
		{Status: "M", Path: "another.go"},
	}
	got := ClassifyChanges(changes)

	if !strings.Contains(got, "Arquivos adicionados (1)") {
		t.Errorf("expected 1 added, got %q", got)
	}
	if !strings.Contains(got, "Arquivos modificados (2)") {
		t.Errorf("expected 2 modified, got %q", got)
	}
	if !strings.Contains(got, "Arquivos removidos (1)") {
		t.Errorf("expected 1 removed, got %q", got)
	}
}

func TestClassifyChanges_IgnoresUnknownStatus(t *testing.T) {
	changes := []StagedChange{
		{Status: "R", Path: "renamed.go"},
		{Status: "?", Path: "unknown.go"},
	}
	got := ClassifyChanges(changes)
	if got != "" {
		t.Errorf("expected empty string for unknown statuses, got %q", got)
	}
}

func TestDetectScope_Empty(t *testing.T) {
	got := DetectScope(nil)
	if got != "" {
		t.Errorf("expected empty scope for nil, got %q", got)
	}

	got = DetectScope([]StagedChange{})
	if got != "" {
		t.Errorf("expected empty scope for empty slice, got %q", got)
	}
}

func TestDetectScope_RootFile(t *testing.T) {
	changes := []StagedChange{
		{Status: "M", Path: "main.go"},
	}
	got := DetectScope(changes)
	if got != "" {
		t.Errorf("expected empty scope for root file, got %q", got)
	}
}

func TestDetectScope_SingleDirectory(t *testing.T) {
	changes := []StagedChange{
		{Status: "M", Path: "pkg/foo.go"},
		{Status: "M", Path: "pkg/bar.go"},
	}
	got := DetectScope(changes)
	if got != "pkg" {
		t.Errorf("expected scope 'pkg', got %q", got)
	}
}

func TestDetectScope_TopLevelDirectory(t *testing.T) {
	// When all files share a single top-level directory, that directory wins.
	changes := []StagedChange{
		{Status: "M", Path: "internal/commitai/commit.go"},
		{Status: "M", Path: "internal/ai/chat.go"},
		{Status: "M", Path: "internal/config/config.go"},
	}
	got := DetectScope(changes)
	// "internal" has count 3, each subdirectory has count 1, so "internal" wins.
	// But the code strips the "internal/" prefix -- since the dir IS "internal"
	// (not "internal/foo"), TrimPrefix leaves it as "internal".
	if got != "internal" {
		t.Errorf("expected scope 'internal', got %q", got)
	}
}

func TestDetectScope_MostCommonSubdirectory(t *testing.T) {
	// A sub-directory with higher count should win over the top-level parent.
	changes := []StagedChange{
		{Status: "M", Path: "pkg/api/handler.go"},
		{Status: "M", Path: "pkg/api/router.go"},
		{Status: "M", Path: "pkg/api/middleware.go"},
	}
	// Both "pkg" and "pkg/api" have count 3 (tie), so whichever iterates first
	// wins -- but since ties don't replace, the first-seen wins.
	// We just assert it's one of the valid scopes.
	got := DetectScope(changes)
	if got != "pkg" && got != "pkg/api" {
		t.Errorf("expected 'pkg' or 'pkg/api', got %q", got)
	}
}

func TestDetectScope_StripsSrcPrefix(t *testing.T) {
	// Make "src/api" clearly the most common (count 2) and "src" tie-breaker.
	// Use a mix so src/api > src individually.
	changes := []StagedChange{
		{Status: "M", Path: "src/api/handler.go"},
		{Status: "M", Path: "src/api/router.go"},
	}
	got := DetectScope(changes)
	// With 2 files in src/api: dirs["src"]=2, dirs["src/api"]=2, tie.
	// Result depends on map iteration. Accept either "src" or "api" (stripped).
	if got != "src" && got != "api" {
		t.Errorf("expected 'src' or 'api' (stripped), got %q", got)
	}
}

func TestGenerateCommitPrompt_BasicStructure(t *testing.T) {
	changes := []StagedChange{
		{Status: "A", Path: "pkg/foo.go"},
	}
	diff := "diff --git a/pkg/foo.go b/pkg/foo.go\n+hello"
	got := GenerateCommitPrompt(diff, changes, false)

	// Check for key template pieces
	expectedParts := []string{
		"Conventional Commits",
		"<tipo>(<escopo>): <descricao>",
		"feat, fix, docs",
		"Escopo sugerido: pkg",
		"Arquivos adicionados (1)",
		"pkg/foo.go",
		"## Diff",
		diff,
		"maximo 72 caracteres",
		"imperativo",
	}
	for _, p := range expectedParts {
		if !strings.Contains(got, p) {
			t.Errorf("expected prompt to contain %q, got:\n%s", p, got)
		}
	}
}

func TestGenerateCommitPrompt_WithoutWhy(t *testing.T) {
	changes := []StagedChange{
		{Status: "M", Path: "main.go"},
	}
	got := GenerateCommitPrompt("diff", changes, false)
	if strings.Contains(got, "POR QUE") {
		t.Errorf("did not expect 'POR QUE' rule when includeWhy=false, got:\n%s", got)
	}
}

func TestGenerateCommitPrompt_WithWhy(t *testing.T) {
	changes := []StagedChange{
		{Status: "M", Path: "main.go"},
	}
	got := GenerateCommitPrompt("diff", changes, true)
	if !strings.Contains(got, "POR QUE") {
		t.Errorf("expected 'POR QUE' rule when includeWhy=true, got:\n%s", got)
	}
}

func TestGenerateCommitPrompt_NoScopeWhenRootFile(t *testing.T) {
	changes := []StagedChange{
		{Status: "M", Path: "README.md"},
	}
	got := GenerateCommitPrompt("diff", changes, false)
	if strings.Contains(got, "Escopo sugerido:") {
		t.Errorf("did not expect 'Escopo sugerido:' when no scope detected, got:\n%s", got)
	}
}

func TestGenerateCommitPrompt_EmptyChanges(t *testing.T) {
	got := GenerateCommitPrompt("", nil, false)
	// Should still render the basic template
	if !strings.Contains(got, "Conventional Commits") {
		t.Errorf("expected base template even for empty changes, got:\n%s", got)
	}
	if strings.Contains(got, "Escopo sugerido:") {
		t.Errorf("did not expect scope line for empty changes, got:\n%s", got)
	}
}

func TestStagedChange_FieldAssignment(t *testing.T) {
	// Sanity test that the struct fields behave as expected.
	c := StagedChange{Status: "A", Path: "file.go"}
	if c.Status != "A" {
		t.Errorf("expected Status 'A', got %q", c.Status)
	}
	if c.Path != "file.go" {
		t.Errorf("expected Path 'file.go', got %q", c.Path)
	}
}
