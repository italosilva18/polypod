package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateRequirementsPrompt(t *testing.T) {
	desc := "Sistema de login com OAuth"
	prompt := GenerateRequirementsPrompt(desc)

	if !strings.Contains(prompt, desc) {
		t.Errorf("prompt should embed the description, got: %s", prompt)
	}

	// Should contain the key sections the prompt asks for.
	mustContain := []string{
		"Requisitos Funcionais",
		"Requisitos Nao-Funcionais",
		"Casos de Uso",
		"Restricoes",
		"Markdown",
	}
	for _, s := range mustContain {
		if !strings.Contains(prompt, s) {
			t.Errorf("requirements prompt missing %q", s)
		}
	}
}

func TestGenerateRequirementsPromptEmpty(t *testing.T) {
	// Should still produce a valid non-empty prompt even with an empty description.
	prompt := GenerateRequirementsPrompt("")
	if prompt == "" {
		t.Fatal("expected non-empty prompt for empty description")
	}
	if !strings.Contains(prompt, "Requisitos Funcionais") {
		t.Error("prompt should still contain the structural sections")
	}
}

func TestGenerateDesignPrompt(t *testing.T) {
	reqs := "RF01: usuario deve poder se cadastrar"
	prompt := GenerateDesignPrompt(reqs)

	if !strings.Contains(prompt, reqs) {
		t.Errorf("design prompt should embed the requirements, got: %s", prompt)
	}

	mustContain := []string{
		"Arquitetura",
		"Estrutura de Arquivos",
		"Interfaces/APIs",
		"Decisoes de Design",
	}
	for _, s := range mustContain {
		if !strings.Contains(prompt, s) {
			t.Errorf("design prompt missing %q", s)
		}
	}
}

func TestGenerateTasksPrompt(t *testing.T) {
	design := "Componente X chama componente Y via HTTP"
	prompt := GenerateTasksPrompt(design)

	if !strings.Contains(prompt, design) {
		t.Errorf("tasks prompt should embed the design, got: %s", prompt)
	}

	mustContain := []string{
		"tarefas",
		"Titulo",
		"Descricao",
		"Arquivos",
		"Dependencias",
		"Estimativa",
	}
	for _, s := range mustContain {
		if !strings.Contains(prompt, s) {
			t.Errorf("tasks prompt missing %q", s)
		}
	}
}

func TestPromptsAreDeterministic(t *testing.T) {
	// Calling with the same input should produce identical output.
	a := GenerateRequirementsPrompt("foo")
	b := GenerateRequirementsPrompt("foo")
	if a != b {
		t.Error("GenerateRequirementsPrompt is not deterministic")
	}

	c := GenerateDesignPrompt("bar")
	d := GenerateDesignPrompt("bar")
	if c != d {
		t.Error("GenerateDesignPrompt is not deterministic")
	}

	e := GenerateTasksPrompt("baz")
	f := GenerateTasksPrompt("baz")
	if e != f {
		t.Error("GenerateTasksPrompt is not deterministic")
	}
}

func TestSaveAndLoadSpec(t *testing.T) {
	dir := t.TempDir()
	original := &Spec{
		Name:         "Minha Feature",
		Description:  "uma feature legal",
		Requirements: "RF01: fazer X\nRF02: fazer Y",
		Design:       "# Design\n\nComponente A -> B",
		Tasks: []Task{
			{ID: 1, Title: "Criar struct", Description: "Criar User", Status: "pending", Files: []string{"user.go"}},
			{ID: 2, Title: "Testar", Description: "Escrever testes", Status: "done"},
			{ID: 3, Title: "Integrar", Status: "in_progress", Files: []string{"a.go", "b.go"}},
		},
		Status: "planning",
	}

	if err := SaveSpec(dir, original); err != nil {
		t.Fatalf("SaveSpec failed: %v", err)
	}

	// Spec directory should be created with sanitized name (lowercase, spaces -> dashes).
	expectedDir := filepath.Join(dir, ".polypod", "specs", "minha-feature")
	if _, err := os.Stat(expectedDir); err != nil {
		t.Fatalf("expected spec directory at %s: %v", expectedDir, err)
	}

	// requirements.md and design.md should exist with the right content.
	reqData, err := os.ReadFile(filepath.Join(expectedDir, "requirements.md"))
	if err != nil {
		t.Fatalf("reading requirements.md: %v", err)
	}
	if string(reqData) != original.Requirements {
		t.Errorf("requirements mismatch: got %q, want %q", reqData, original.Requirements)
	}

	designData, err := os.ReadFile(filepath.Join(expectedDir, "design.md"))
	if err != nil {
		t.Fatalf("reading design.md: %v", err)
	}
	if string(designData) != original.Design {
		t.Errorf("design mismatch")
	}

	// tasks.md should exist and contain the markdown checkboxes for each state.
	tasksData, err := os.ReadFile(filepath.Join(expectedDir, "tasks.md"))
	if err != nil {
		t.Fatalf("reading tasks.md: %v", err)
	}
	tasksStr := string(tasksData)
	if !strings.Contains(tasksStr, "[ ]") {
		t.Error("tasks.md should contain '[ ]' for pending tasks")
	}
	if !strings.Contains(tasksStr, "[x]") {
		t.Error("tasks.md should contain '[x]' for done tasks")
	}
	if !strings.Contains(tasksStr, "[~]") {
		t.Error("tasks.md should contain '[~]' for in_progress tasks")
	}
	if !strings.Contains(tasksStr, "Criar struct") {
		t.Error("tasks.md should contain task titles")
	}
	if !strings.Contains(tasksStr, "a.go, b.go") {
		t.Error("tasks.md should list joined files")
	}

	// Now load the spec back by name and verify the fields.
	loaded, err := LoadSpec(dir, "Minha Feature")
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}
	if loaded.Requirements != original.Requirements {
		t.Errorf("loaded requirements mismatch")
	}
	if loaded.Design != original.Design {
		t.Errorf("loaded design mismatch")
	}
	if loaded.Status != "planning" {
		t.Errorf("loaded status should be 'planning', got %q", loaded.Status)
	}
}

func TestSaveSpecSkipsEmptyFields(t *testing.T) {
	dir := t.TempDir()
	s := &Spec{
		Name:        "empty-spec",
		Description: "nothing yet",
		// No requirements, design, or tasks.
	}
	if err := SaveSpec(dir, s); err != nil {
		t.Fatalf("SaveSpec: %v", err)
	}

	specDir := filepath.Join(dir, ".polypod", "specs", "empty-spec")
	if _, err := os.Stat(specDir); err != nil {
		t.Fatalf("spec dir should exist: %v", err)
	}

	// None of the content files should be written when the fields are empty.
	for _, f := range []string{"requirements.md", "design.md", "tasks.md"} {
		if _, err := os.Stat(filepath.Join(specDir, f)); !os.IsNotExist(err) {
			t.Errorf("%s should not exist for empty spec, err=%v", f, err)
		}
	}
}

func TestLoadSpecNotFound(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadSpec(dir, "does-not-exist"); err == nil {
		t.Error("expected error when loading a non-existent spec")
	}
}

func TestLoadSpecDraftStatus(t *testing.T) {
	// A spec directory with no content files should load with status "draft".
	dir := t.TempDir()
	specDir := filepath.Join(dir, ".polypod", "specs", "bare")
	if err := os.MkdirAll(specDir, 0755); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSpec(dir, "bare")
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	if s.Status != "draft" {
		t.Errorf("expected draft status, got %q", s.Status)
	}
	if s.Requirements != "" || s.Design != "" {
		t.Error("expected empty requirements and design for bare spec")
	}
}

func TestListSpecs(t *testing.T) {
	dir := t.TempDir()

	// Empty project - no specs directory yet.
	if got := ListSpecs(dir); got != nil && len(got) != 0 {
		t.Errorf("expected no specs in empty dir, got %v", got)
	}

	// Create several specs via SaveSpec and also a stray file that should be ignored.
	specs := []string{"Feature A", "feature-b", "CoolThing"}
	for _, name := range specs {
		if err := SaveSpec(dir, &Spec{Name: name, Requirements: "x"}); err != nil {
			t.Fatalf("SaveSpec %q: %v", name, err)
		}
	}
	// Stray file in the specs directory should not appear in the listing.
	strayPath := filepath.Join(dir, ".polypod", "specs", "not-a-dir.txt")
	if err := os.WriteFile(strayPath, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	got := ListSpecs(dir)
	if len(got) != len(specs) {
		t.Errorf("expected %d specs, got %d (%v)", len(specs), len(got), got)
	}

	// Names in the listing must match the sanitized form.
	set := map[string]bool{}
	for _, n := range got {
		set[n] = true
	}
	for _, expected := range []string{"feature-a", "feature-b", "coolthing"} {
		if !set[expected] {
			t.Errorf("expected spec %q in listing, got %v", expected, got)
		}
	}
}

func TestSanitizeViaSaveSpec(t *testing.T) {
	// Indirectly test the sanitize behavior via SaveSpec/LoadSpec round-trip.
	dir := t.TempDir()
	cases := []struct {
		input    string
		expected string
	}{
		{"Hello World", "hello-world"},
		{"path/with/slashes", "path-with-slashes"},
		{"UPPER", "upper"},
		{"with:colon", "with-colon"},
	}
	for _, tc := range cases {
		if err := SaveSpec(dir, &Spec{Name: tc.input, Requirements: "r"}); err != nil {
			t.Fatalf("SaveSpec %q: %v", tc.input, err)
		}
		want := filepath.Join(dir, ".polypod", "specs", tc.expected)
		if _, err := os.Stat(want); err != nil {
			t.Errorf("expected sanitized dir %q for input %q: %v", want, tc.input, err)
		}
	}
}
