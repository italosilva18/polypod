package agent

import (
	"reflect"
	"strings"
	"testing"
)

// --- Agent struct basics ---

func TestAgent_MinimalFields(t *testing.T) {
	a := &Agent{
		Name:        "tester",
		Description: "desc",
		Persona:     "persona",
		Skills:      []string{"read_file"},
		IsBuiltIn:   true,
	}
	if a.Name != "tester" {
		t.Errorf("Name = %q; want tester", a.Name)
	}
	if a.Description != "desc" {
		t.Errorf("Description = %q; want desc", a.Description)
	}
	if a.Persona != "persona" {
		t.Errorf("Persona = %q; want persona", a.Persona)
	}
	if len(a.Skills) != 1 || a.Skills[0] != "read_file" {
		t.Errorf("Skills = %v; want [read_file]", a.Skills)
	}
	if !a.IsBuiltIn {
		t.Error("IsBuiltIn should be true")
	}
}

func TestAgent_BuildSystemPrompt(t *testing.T) {
	a := &Agent{Persona: "You are a test agent."}
	prompt := a.BuildSystemPrompt("")
	if prompt != "You are a test agent." {
		t.Errorf("BuildSystemPrompt without context = %q", prompt)
	}
	withCtx := a.BuildSystemPrompt("some context")
	if withCtx == prompt {
		t.Error("BuildSystemPrompt should change when context is provided")
	}
	if len(withCtx) <= len(prompt) {
		t.Error("BuildSystemPrompt with context should be longer")
	}
}

func TestAgent_GetEffectiveModel(t *testing.T) {
	a := &Agent{Model: "custom-model"}
	if got := a.GetEffectiveModel("default-model"); got != "custom-model" {
		t.Errorf("GetEffectiveModel = %q; want custom-model", got)
	}
	b := &Agent{}
	if got := b.GetEffectiveModel("default-model"); got != "default-model" {
		t.Errorf("GetEffectiveModel fallback = %q; want default-model", got)
	}
}

func TestAgent_GetEffectiveMaxTokens(t *testing.T) {
	a := &Agent{MaxTokens: 4096}
	if got := a.GetEffectiveMaxTokens(2048); got != 4096 {
		t.Errorf("GetEffectiveMaxTokens = %d; want 4096", got)
	}
	b := &Agent{}
	if got := b.GetEffectiveMaxTokens(2048); got != 2048 {
		t.Errorf("GetEffectiveMaxTokens fallback = %d; want 2048", got)
	}
}

// --- Registry operations ---

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if r.Default != "default" {
		t.Errorf("Default = %q; want default", r.Default)
	}
	if r.Count() != 0 {
		t.Errorf("empty registry Count = %d; want 0", r.Count())
	}
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	a := &Agent{
		Name:    "myagent",
		Aliases: []string{"my", "alias1"},
		Persona: "p",
		Skills:  []string{"read_file"},
	}
	r.Register(a)

	got := r.Get("myagent")
	if got == nil || got.Name != "myagent" {
		t.Errorf("Get(myagent) = %v; want myagent", got)
	}
	if !r.Has("myagent") {
		t.Error("Has(myagent) = false")
	}
}

func TestRegistry_GetByAlias(t *testing.T) {
	r := NewRegistry()
	a := &Agent{
		Name:    "original",
		Aliases: []string{"alt1", "alt2"},
	}
	r.Register(a)

	for _, alias := range []string{"alt1", "alt2"} {
		got := r.Get(alias)
		if got == nil || got.Name != "original" {
			t.Errorf("Get(%q) = %v; want original", alias, got)
		}
		if !r.Has(alias) {
			t.Errorf("Has(%q) = false", alias)
		}
	}
}

func TestRegistry_GetFallback(t *testing.T) {
	r := NewRegistry()
	// No default agent registered; should fall back to DefaultAgent()
	got := r.Get("nonexistent")
	if got == nil {
		t.Fatal("Get unknown returned nil")
	}
	if got.Name != "default" {
		t.Errorf("fallback Name = %q; want default", got.Name)
	}
}

func TestRegistry_GetRegisteredDefault(t *testing.T) {
	r := NewRegistry()
	def := &Agent{Name: "default", Persona: "p"}
	r.Register(def)
	other := &Agent{Name: "other"}
	r.Register(other)

	got := r.Get("nothere")
	if got == nil || got.Name != "default" {
		t.Errorf("Get unknown = %v; want registered default", got)
	}
}

func TestRegistry_List(t *testing.T) {
	r := NewRegistry()
	r.Register(&Agent{Name: "a", Aliases: []string{"alpha"}})
	r.Register(&Agent{Name: "b"})
	r.Register(&Agent{Name: "c"})

	list := r.List()
	if len(list) != 3 {
		t.Errorf("List len = %d; want 3 (aliases should be deduped)", len(list))
	}
	seen := make(map[string]bool)
	for _, n := range list {
		seen[n] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !seen[want] {
			t.Errorf("List missing %q", want)
		}
	}
}

func TestRegistry_Count(t *testing.T) {
	r := NewRegistry()
	if r.Count() != 0 {
		t.Errorf("empty Count = %d", r.Count())
	}
	r.Register(&Agent{Name: "x"})
	r.Register(&Agent{Name: "y", Aliases: []string{"why"}})
	if r.Count() != 2 {
		t.Errorf("Count = %d; want 2", r.Count())
	}
}

func TestDefaultAgent(t *testing.T) {
	d := DefaultAgent()
	if d == nil {
		t.Fatal("DefaultAgent returned nil")
	}
	if d.Name != "default" {
		t.Errorf("Name = %q; want default", d.Name)
	}
	if d.Persona == "" {
		t.Error("Persona empty")
	}
	if len(d.Skills) == 0 {
		t.Error("Skills empty")
	}
	if !d.IsBuiltIn {
		t.Error("IsBuiltIn should be true")
	}
}

// --- Built-in autonomous agents ---

// validateAgent runs common checks on a built-in agent.
func validateAgent(t *testing.T, a *Agent, label string) {
	t.Helper()
	if a == nil {
		t.Fatalf("%s: nil agent", label)
	}
	if a.Name == "" {
		t.Errorf("%s: Name empty", label)
	}
	if a.Persona == "" {
		t.Errorf("%s: Persona empty", label)
	}
	if len(a.Skills) == 0 {
		t.Errorf("%s: Skills empty", label)
	}
	if !a.IsBuiltIn {
		t.Errorf("%s: IsBuiltIn should be true", label)
	}
	// All specialized agents must at least have read_file.
	hasRead := false
	for _, s := range a.Skills {
		if s == "read_file" {
			hasRead = true
			break
		}
	}
	if !hasRead {
		t.Errorf("%s: Skills missing read_file (got %v)", label, a.Skills)
	}
}

func TestCoderAgent(t *testing.T) {
	validateAgent(t, CoderAgent(), "CoderAgent")
	a := CoderAgent()
	if a.Name != "coder" {
		t.Errorf("Name = %q; want coder", a.Name)
	}
}

func TestTaskAgent(t *testing.T) {
	validateAgent(t, TaskAgent(), "TaskAgent")
	a := TaskAgent()
	if a.Name != "task" {
		t.Errorf("Name = %q; want task", a.Name)
	}
}

func TestReviewerAgent(t *testing.T) {
	validateAgent(t, ReviewerAgent(), "ReviewerAgent")
	a := ReviewerAgent()
	if a.Name != "reviewer" {
		t.Errorf("Name = %q; want reviewer", a.Name)
	}
}

func TestArchitectAgent(t *testing.T) {
	validateAgent(t, ArchitectAgent(), "ArchitectAgent")
	a := ArchitectAgent()
	if a.Name != "architect" {
		t.Errorf("Name = %q; want architect", a.Name)
	}
}

func TestDevOpsAgent(t *testing.T) {
	validateAgent(t, DevOpsAgent(), "DevOpsAgent")
	a := DevOpsAgent()
	if a.Name != "devops" {
		t.Errorf("Name = %q; want devops", a.Name)
	}
}

// --- New specialized agents ---

func TestSecurityAuditorAgent(t *testing.T) {
	validateAgent(t, SecurityAuditorAgent(), "SecurityAuditorAgent")
	a := SecurityAuditorAgent()
	if a.Name != "security-auditor" {
		t.Errorf("Name = %q; want security-auditor", a.Name)
	}
	// Must not include write/edit skills (read-only agent).
	for _, s := range a.Skills {
		if s == "write_file" || s == "edit_file" || s == "run_command" {
			t.Errorf("security-auditor should not have %q skill", s)
		}
	}
}

func TestPerfProfilerAgent(t *testing.T) {
	validateAgent(t, PerfProfilerAgent(), "PerfProfilerAgent")
	a := PerfProfilerAgent()
	if a.Name != "perf-profiler" {
		t.Errorf("Name = %q; want perf-profiler", a.Name)
	}
}

func TestDocsWriterAgent(t *testing.T) {
	validateAgent(t, DocsWriterAgent(), "DocsWriterAgent")
	a := DocsWriterAgent()
	if a.Name != "docs-writer" {
		t.Errorf("Name = %q; want docs-writer", a.Name)
	}
}

func TestMigrationPlannerAgent(t *testing.T) {
	validateAgent(t, MigrationPlannerAgent(), "MigrationPlannerAgent")
	a := MigrationPlannerAgent()
	if a.Name != "migration-planner" {
		t.Errorf("Name = %q; want migration-planner", a.Name)
	}
}

func TestBugTriagerAgent(t *testing.T) {
	validateAgent(t, BugTriagerAgent(), "BugTriagerAgent")
	a := BugTriagerAgent()
	if a.Name != "bug-triager" {
		t.Errorf("Name = %q; want bug-triager", a.Name)
	}
}

// --- Integration tests ---

func TestAutonomousAgents_AllValid(t *testing.T) {
	agents := AutonomousAgents()
	if len(agents) < 10 {
		t.Errorf("AutonomousAgents count = %d; want >= 10", len(agents))
	}
	seenNames := make(map[string]bool)
	for _, a := range agents {
		if a == nil {
			t.Error("nil agent in AutonomousAgents")
			continue
		}
		if a.Name == "" {
			t.Error("agent with empty name")
			continue
		}
		if seenNames[a.Name] {
			t.Errorf("duplicate agent name: %s", a.Name)
		}
		seenNames[a.Name] = true
		if a.Persona == "" {
			t.Errorf("%s: Persona empty", a.Name)
		}
		if len(a.Skills) == 0 {
			t.Errorf("%s: Skills empty", a.Name)
		}
		if !a.IsBuiltIn {
			t.Errorf("%s: IsBuiltIn should be true", a.Name)
		}
	}
}

func TestAutonomousAgents_ExpectedNames(t *testing.T) {
	agents := AutonomousAgents()
	got := make(map[string]bool)
	for _, a := range agents {
		got[a.Name] = true
	}
	expected := []string{
		"coder", "task", "reviewer", "devops", "architect",
		"security-auditor", "perf-profiler", "docs-writer",
		"migration-planner", "bug-triager",
	}
	for _, name := range expected {
		if !got[name] {
			t.Errorf("AutonomousAgents missing %q", name)
		}
	}
}

func TestRegisterAutonomousAgents(t *testing.T) {
	r := NewRegistry()
	RegisterAutonomousAgents(r)

	expected := []string{
		"coder", "task", "reviewer", "devops", "architect",
		"security-auditor", "perf-profiler", "docs-writer",
		"migration-planner", "bug-triager",
	}
	for _, name := range expected {
		if !r.Has(name) {
			t.Errorf("Registry missing %q after RegisterAutonomousAgents", name)
		}
	}
	// Check at least the expected unique agents are present.
	if r.Count() < len(expected) {
		t.Errorf("Registry Count = %d; want >= %d", r.Count(), len(expected))
	}
}

func TestRegisterAutonomousAgents_AliasesResolve(t *testing.T) {
	r := NewRegistry()
	RegisterAutonomousAgents(r)

	// Some aliases defined in autonomous.go & specialized agents.
	aliasChecks := map[string]string{
		"code":     "coder",
		"dev":      "coder",
		"search":   "task",
		"review":   "reviewer",
		"arch":     "architect",
		"ops":      "devops",
		"security": "security-auditor",
		"perf":     "perf-profiler",
		"docs":     "docs-writer",
		"migrate":  "migration-planner",
		"bug":      "bug-triager",
	}
	for alias, wantName := range aliasChecks {
		got := r.Get(alias)
		if got == nil {
			t.Errorf("Get(%q) = nil", alias)
			continue
		}
		if got.Name != wantName {
			t.Errorf("Get(%q).Name = %q; want %q", alias, got.Name, wantName)
		}
	}
}

func TestRegisterAutonomousAgents_DoesNotOverwrite(t *testing.T) {
	r := NewRegistry()
	// Pre-register a "coder" with a marker persona.
	custom := &Agent{
		Name:      "coder",
		Persona:   "CUSTOM_PERSONA_MARKER",
		Skills:    []string{"read_file"},
		IsBuiltIn: false,
	}
	r.Register(custom)

	RegisterAutonomousAgents(r)

	got := r.Get("coder")
	if got == nil {
		t.Fatal("coder not registered")
	}
	if got.Persona != "CUSTOM_PERSONA_MARKER" {
		t.Errorf("RegisterAutonomousAgents overwrote existing coder: persona = %q", got.Persona)
	}
}

func TestRegisterBuiltInAgents(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltInAgents(r)

	// Default must always be present.
	if !r.Has("default") {
		t.Error("Registry missing default after RegisterBuiltInAgents")
	}
	// At least the six built-in agent types should be present.
	builtIns := []string{
		"general-purpose", "Explore", "Plan", "verification",
		"claude-code-guide", "statusline-setup",
	}
	for _, name := range builtIns {
		if !r.Has(name) {
			t.Errorf("Registry missing built-in %q", name)
		}
	}
}

func TestBuiltInAgents(t *testing.T) {
	defs := BuiltInAgents()
	if len(defs) < 6 {
		t.Errorf("BuiltInAgents count = %d; want >= 6", len(defs))
	}
	for _, d := range defs {
		if d == nil {
			t.Error("nil AgentDefinition")
			continue
		}
		if d.AgentType == "" {
			t.Error("AgentType empty")
		}
		if d.SystemPrompt == "" {
			t.Errorf("%s: SystemPrompt empty", d.AgentType)
		}
		if d.Source == "" {
			t.Errorf("%s: Source empty", d.AgentType)
		}
	}
}

// --- Docs writer path allowlist ---

func TestDocPathAllowed(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"README.md", true},
		{"docs/foo.md", true},
		{"docs/subdir/bar.md", true},
		{"doc.go", true},
		{"CHANGELOG.md", true},
		{"CONTRIBUTING.md", true},
		{"guides/tutorial.md", true},
		{"notes.rst", true},
		{"notes.txt", true},

		{"main.go", false},
		{"internal/foo/bar.go", false},
		{"config.yaml", false},
		{"Makefile", false},
		{"script.sh", false},
	}
	for _, tc := range cases {
		got := DocPathAllowed(tc.path)
		if got != tc.want {
			t.Errorf("DocPathAllowed(%q) = %v; want %v", tc.path, got, tc.want)
		}
	}
}

func TestDocsWriterAgent_HasWriteSkills(t *testing.T) {
	// Unlike other specialized read-only agents, docs-writer needs write access.
	a := DocsWriterAgent()
	hasWrite := false
	hasEdit := false
	for _, s := range a.Skills {
		if s == "write_file" {
			hasWrite = true
		}
		if s == "edit_file" {
			hasEdit = true
		}
	}
	if !hasWrite {
		t.Error("docs-writer missing write_file skill")
	}
	if !hasEdit {
		t.Error("docs-writer missing edit_file skill")
	}
}

// --- Uniqueness across all built-ins ---

func TestAllBuiltInAgentNames_Unique(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltInAgents(r)
	RegisterAutonomousAgents(r)

	names := r.List()
	seen := make(map[string]int)
	for _, n := range names {
		seen[n]++
	}
	for name, count := range seen {
		if count > 1 {
			t.Errorf("agent name %q appears %d times", name, count)
		}
	}
	// Sanity: should have at least 6 built-ins + 10 autonomous + default.
	if len(names) < 16 {
		t.Errorf("total unique agents = %d; want >= 16", len(names))
	}
}

// --- Persona-based first-class *Agent built-ins ---
// These tests cover VerificationPersonaAgent, ExplorePersonaAgent, and
// PlanPersonaAgent from builtin_verification.go / builtin_explore.go /
// builtin_plan.go, plus the RegisterBuiltinAgents (lowercase "i") entry
// point that wraps them.

// readOnlyToolSet is the exact skill list each persona agent must expose.
var readOnlyToolSet = []string{"read_file", "list_directory", "search_files", "grep"}

func assertReadOnlyPersona(t *testing.T, a *Agent, label string) {
	t.Helper()
	if a == nil {
		t.Fatalf("%s: agent is nil", label)
	}
	if strings.TrimSpace(a.Persona) == "" {
		t.Errorf("%s: persona is empty", label)
	}
	if !a.IsBuiltIn {
		t.Errorf("%s: IsBuiltIn should be true", label)
	}
	if !reflect.DeepEqual(a.Skills, readOnlyToolSet) {
		t.Errorf("%s: skills = %v; want %v", label, a.Skills, readOnlyToolSet)
	}
	if len(a.Persona) < 200 {
		t.Errorf("%s: persona is suspiciously short (%d chars)", label, len(a.Persona))
	}
	for _, s := range a.Skills {
		switch s {
		case "write_file", "edit_file", "run_command":
			t.Errorf("%s: persona must not include mutating skill %q", label, s)
		}
	}
}

func TestVerificationPersonaAgent(t *testing.T) {
	a := VerificationPersonaAgent()
	assertReadOnlyPersona(t, a, "VerificationPersonaAgent")
	if a.Name != "verification-persona" {
		t.Errorf("Name = %q; want verification-persona", a.Name)
	}
	lower := strings.ToLower(a.Persona)
	if !strings.Contains(lower, "verif") && !strings.Contains(lower, "review") {
		t.Errorf("verification persona is missing verification/review framing")
	}
}

func TestExplorePersonaAgent(t *testing.T) {
	a := ExplorePersonaAgent()
	assertReadOnlyPersona(t, a, "ExplorePersonaAgent")
	if a.Name != "explore-persona" {
		t.Errorf("Name = %q; want explore-persona", a.Name)
	}
	if !strings.Contains(strings.ToLower(a.Description), "explor") {
		t.Errorf("explore description is missing exploration framing: %q", a.Description)
	}
}

func TestPlanPersonaAgent(t *testing.T) {
	a := PlanPersonaAgent()
	assertReadOnlyPersona(t, a, "PlanPersonaAgent")
	if a.Name != "plan-persona" {
		t.Errorf("Name = %q; want plan-persona", a.Name)
	}
	lower := strings.ToLower(a.Persona)
	if !strings.Contains(lower, "plan") && !strings.Contains(lower, "architect") {
		t.Errorf("plan persona is missing planning/architect framing")
	}
}

func TestRegisterBuiltinAgents_PersonaRegistry(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinAgents(r)

	for _, name := range []string{"verification-persona", "explore-persona", "plan-persona"} {
		if !r.Has(name) {
			t.Errorf("Registry missing %q after RegisterBuiltinAgents", name)
		}
	}
}

func TestRegisterBuiltinAgents_NilSafe(t *testing.T) {
	// Must not panic on nil or unknown shapes.
	RegisterBuiltinAgents(nil)
	RegisterBuiltinAgents(struct{}{})
}
