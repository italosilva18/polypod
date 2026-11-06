package planmode

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Plan / PlanStep construction ---------------------------------------

func TestNewPlanConstruction(t *testing.T) {
	before := time.Now()
	p := NewPlan("My Plan")
	after := time.Now()

	if p == nil {
		t.Fatal("NewPlan returned nil")
	}
	if p.Title != "My Plan" {
		t.Errorf("Title = %q, want %q", p.Title, "My Plan")
	}
	if p.Status != PlanStatusDraft {
		t.Errorf("Status = %q, want %q", p.Status, PlanStatusDraft)
	}
	if p.Steps == nil {
		t.Error("Steps should be initialized, not nil")
	}
	if len(p.Steps) != 0 {
		t.Errorf("len(Steps) = %d, want 0", len(p.Steps))
	}
	if !strings.HasPrefix(p.ID, "plan_") {
		t.Errorf("ID = %q, expected prefix 'plan_'", p.ID)
	}
	// Plan ID hex suffix should be 16 chars (8 random bytes).
	if len(p.ID) != len("plan_")+16 {
		t.Errorf("unexpected ID length: %d (%q)", len(p.ID), p.ID)
	}
	if p.CreatedAt.Before(before) || p.CreatedAt.After(after) {
		t.Errorf("CreatedAt (%v) out of expected range [%v, %v]", p.CreatedAt, before, after)
	}
	if p.UpdatedAt.Before(before) || p.UpdatedAt.After(after) {
		t.Errorf("UpdatedAt (%v) out of expected range [%v, %v]", p.UpdatedAt, before, after)
	}
}

func TestNewPlanUniqueIDs(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		p := NewPlan("t")
		if seen[p.ID] {
			t.Fatalf("duplicate plan ID generated: %s", p.ID)
		}
		seen[p.ID] = true
	}
}

func TestPlanStepStructFields(t *testing.T) {
	// Verify a PlanStep can be constructed with all documented fields.
	now := time.Now()
	s := &PlanStep{
		ID:          "step_x",
		Description: "do the thing",
		ToolCalls: []ToolCall{
			{ToolName: "bash", Input: map[string]any{"cmd": "ls"}},
			{ToolName: "read", Input: map[string]any{"path": "/tmp"}},
		},
		Dependencies: []string{"step_1", "step_2"},
		Status:       StepStatusPending,
		Result:       "",
		Error:        "",
		StartedAt:    now,
		CompletedAt:  now.Add(time.Second),
		AgentID:      "agent-1",
	}

	if s.ID != "step_x" || s.Description != "do the thing" {
		t.Errorf("unexpected basic fields: %+v", s)
	}
	if len(s.ToolCalls) != 2 {
		t.Errorf("ToolCalls len = %d, want 2", len(s.ToolCalls))
	}
	if s.ToolCalls[0].ToolName != "bash" || s.ToolCalls[0].Input["cmd"] != "ls" {
		t.Errorf("ToolCall[0] = %+v", s.ToolCalls[0])
	}
	if len(s.Dependencies) != 2 || s.Dependencies[0] != "step_1" {
		t.Errorf("Dependencies = %v", s.Dependencies)
	}
	if s.Status != StepStatusPending {
		t.Errorf("Status = %q", s.Status)
	}
	if s.AgentID != "agent-1" {
		t.Errorf("AgentID = %q", s.AgentID)
	}
	if !s.CompletedAt.After(s.StartedAt) {
		t.Errorf("CompletedAt should be after StartedAt")
	}
}

// --- AddStep / GetStep --------------------------------------------------

func TestAddStepAssignsIDsAndDefaults(t *testing.T) {
	p := NewPlan("plan")
	prevUpdated := p.UpdatedAt
	// ensure a detectable time gap.
	time.Sleep(2 * time.Millisecond)

	s1 := p.AddStep("first")
	s2 := p.AddStep("second", "step_1")
	s3 := p.AddStep("third", "step_1", "step_2")

	if s1.ID != "step_1" || s2.ID != "step_2" || s3.ID != "step_3" {
		t.Errorf("IDs = %s,%s,%s; want step_1,step_2,step_3", s1.ID, s2.ID, s3.ID)
	}
	if s1.Status != StepStatusPending {
		t.Errorf("default Status = %q, want %q", s1.Status, StepStatusPending)
	}
	if len(s1.Dependencies) != 0 {
		t.Errorf("s1 should have no dependencies, got %v", s1.Dependencies)
	}
	if len(s2.Dependencies) != 1 || s2.Dependencies[0] != "step_1" {
		t.Errorf("s2 dependencies = %v", s2.Dependencies)
	}
	if len(s3.Dependencies) != 2 {
		t.Errorf("s3 dependencies = %v", s3.Dependencies)
	}
	if len(p.Steps) != 3 {
		t.Errorf("len(Steps) = %d, want 3", len(p.Steps))
	}
	if !p.UpdatedAt.After(prevUpdated) {
		t.Errorf("UpdatedAt should have advanced after AddStep")
	}
}

func TestGetStep(t *testing.T) {
	p := NewPlan("plan")
	p.AddStep("a")
	p.AddStep("b")

	if got := p.GetStep("step_1"); got == nil || got.Description != "a" {
		t.Errorf("GetStep(step_1) = %+v", got)
	}
	if got := p.GetStep("step_2"); got == nil || got.Description != "b" {
		t.Errorf("GetStep(step_2) = %+v", got)
	}
	if got := p.GetStep("nope"); got != nil {
		t.Errorf("GetStep(nope) = %+v, want nil", got)
	}
}

// --- "Remove step" (package has no Remove method; do it via slice) ------

func TestRemoveStepViaSliceManipulation(t *testing.T) {
	p := NewPlan("plan")
	p.AddStep("a")
	p.AddStep("b")
	p.AddStep("c")

	// Remove step_2 by reslicing.
	filtered := p.Steps[:0:0]
	for _, s := range p.Steps {
		if s.ID != "step_2" {
			filtered = append(filtered, s)
		}
	}
	p.Steps = filtered

	if len(p.Steps) != 2 {
		t.Fatalf("len(Steps) = %d, want 2", len(p.Steps))
	}
	if p.GetStep("step_2") != nil {
		t.Error("step_2 should have been removed")
	}
	if p.GetStep("step_1") == nil || p.GetStep("step_3") == nil {
		t.Error("step_1 and step_3 should still be present")
	}
}

// --- NextExecutableSteps ------------------------------------------------

func TestNextExecutableStepsDependencies(t *testing.T) {
	p := NewPlan("plan")
	a := p.AddStep("a")           // step_1
	b := p.AddStep("b", "step_1") // step_2 depends on a
	_ = p.AddStep("c", "step_2")  // step_3 depends on b
	d := p.AddStep("d")           // step_4 independent

	// Initially: a and d are ready (no deps), b and c are blocked.
	ready := p.NextExecutableSteps()
	if len(ready) != 2 {
		t.Fatalf("initial ready = %d, want 2 (%v)", len(ready), stepIDs(ready))
	}
	readyIDs := stepIDs(ready)
	if !contains(readyIDs, a.ID) || !contains(readyIDs, d.ID) {
		t.Errorf("initial ready IDs = %v, want [%s %s]", readyIDs, a.ID, d.ID)
	}

	// Complete a: b should become ready, c still blocked on b.
	a.Status = StepStatusCompleted
	d.Status = StepStatusCompleted
	ready = p.NextExecutableSteps()
	if len(ready) != 1 || ready[0].ID != b.ID {
		t.Fatalf("after completing a+d, ready = %v, want [%s]", stepIDs(ready), b.ID)
	}

	// Complete b: c should become ready.
	b.Status = StepStatusCompleted
	ready = p.NextExecutableSteps()
	if len(ready) != 1 || ready[0].ID != "step_3" {
		t.Errorf("after completing b, ready = %v, want [step_3]", stepIDs(ready))
	}
}

// --- ValidatePlan -------------------------------------------------------

func TestValidatePlanEmpty(t *testing.T) {
	p := NewPlan("empty")
	if err := ValidatePlan(p); err == nil {
		t.Error("ValidatePlan on empty plan should error")
	}
}

func TestValidatePlanValid(t *testing.T) {
	p := NewPlan("ok")
	p.AddStep("a")
	p.AddStep("b", "step_1")
	if err := ValidatePlan(p); err != nil {
		t.Errorf("ValidatePlan(valid) returned %v", err)
	}
}

func TestValidatePlanUnknownDependency(t *testing.T) {
	p := NewPlan("bad")
	p.AddStep("a", "step_missing")
	err := ValidatePlan(p)
	if err == nil || !strings.Contains(err.Error(), "unknown step") {
		t.Errorf("ValidatePlan(unknown dep) = %v, want error containing 'unknown step'", err)
	}
}

func TestValidatePlanSelfDependency(t *testing.T) {
	p := NewPlan("self")
	// AddStep assigns sequential IDs, so inject self-dep by appending manually
	// through AddStep and then mutating before validation. Since the new step
	// gets step_1, pass "step_1" as a dependency at creation.
	p.AddStep("a", "step_1")
	err := ValidatePlan(p)
	if err == nil || !strings.Contains(err.Error(), "itself") {
		t.Errorf("ValidatePlan(self dep) = %v, want 'depends on itself'", err)
	}
}

func TestValidatePlanDuplicateIDs(t *testing.T) {
	p := NewPlan("dup")
	p.AddStep("a")
	p.AddStep("b")
	// Force duplicate IDs (cannot happen via AddStep normally).
	p.Steps[1].ID = "step_1"
	err := ValidatePlan(p)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("ValidatePlan(duplicate) = %v, want error containing 'duplicate'", err)
	}
}

func TestValidatePlanCircularDependency(t *testing.T) {
	p := NewPlan("cycle")
	p.AddStep("a", "step_2") // step_1 -> step_2
	p.AddStep("b", "step_1") // step_2 -> step_1
	err := ValidatePlan(p)
	if err == nil || !strings.Contains(err.Error(), "circular") {
		t.Errorf("ValidatePlan(cycle) = %v, want error containing 'circular'", err)
	}
}

// --- ExecutePlan --------------------------------------------------------

func TestExecutePlanSuccess(t *testing.T) {
	p := NewPlan("exec")
	p.AddStep("a")
	p.AddStep("b", "step_1")
	p.AddStep("c", "step_2")

	var executed []string
	err := ExecutePlan(p, func(step *PlanStep) (string, error) {
		executed = append(executed, step.ID)
		return "done:" + step.ID, nil
	})
	if err != nil {
		t.Fatalf("ExecutePlan returned error: %v", err)
	}
	if p.Status != PlanStatusCompleted {
		t.Errorf("plan status = %q, want %q", p.Status, PlanStatusCompleted)
	}
	if len(executed) != 3 {
		t.Fatalf("executed %d steps, want 3 (%v)", len(executed), executed)
	}
	// Execution must respect dependency order.
	if executed[0] != "step_1" || executed[1] != "step_2" || executed[2] != "step_3" {
		t.Errorf("execution order = %v, want [step_1 step_2 step_3]", executed)
	}
	for _, s := range p.Steps {
		if s.Status != StepStatusCompleted {
			t.Errorf("step %s status = %q, want completed", s.ID, s.Status)
		}
		if s.Result != "done:"+s.ID {
			t.Errorf("step %s result = %q", s.ID, s.Result)
		}
		if s.StartedAt.IsZero() || s.CompletedAt.IsZero() {
			t.Errorf("step %s missing timestamps", s.ID)
		}
	}
}

func TestExecutePlanFailurePropagates(t *testing.T) {
	p := NewPlan("fail")
	p.AddStep("a")
	p.AddStep("b", "step_1")

	boom := errors.New("boom")
	err := ExecutePlan(p, func(step *PlanStep) (string, error) {
		if step.ID == "step_1" {
			return "", boom
		}
		return "ok", nil
	})
	if err == nil {
		t.Fatal("ExecutePlan should return error when a step fails")
	}
	if !strings.Contains(err.Error(), "step_1") {
		t.Errorf("error = %v, want reference to step_1", err)
	}
	if p.Status != PlanStatusFailed {
		t.Errorf("plan status = %q, want %q", p.Status, PlanStatusFailed)
	}
	if p.Steps[0].Status != StepStatusFailed {
		t.Errorf("step_1 status = %q, want failed", p.Steps[0].Status)
	}
	if p.Steps[0].Error != "boom" {
		t.Errorf("step_1 error = %q", p.Steps[0].Error)
	}
	// step_2 should never have been started (dep failed).
	if p.Steps[1].Status != StepStatusPending {
		t.Errorf("step_2 status = %q, want still pending", p.Steps[1].Status)
	}
}

// --- Plan formatting (JSON tags) ---------------------------------------

func TestPlanJSONSerialization(t *testing.T) {
	p := NewPlan("formatted")
	p.AddStep("do a thing")
	p.Steps[0].ToolCalls = []ToolCall{
		{ToolName: "read", Input: map[string]any{"path": "/x"}},
	}

	data, err := marshalJSON(p)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	s := string(data)
	mustContain := []string{
		`"id"`,
		`"title":"formatted"`,
		`"steps"`,
		`"status":"draft"`,
		`"description":"do a thing"`,
		`"tool_calls"`,
		`"tool_name":"read"`,
		`"created_at"`,
		`"updated_at"`,
	}
	for _, m := range mustContain {
		if !strings.Contains(s, m) {
			t.Errorf("JSON missing %q\nfull output: %s", m, s)
		}
	}
}

// --- PlanFileManager ----------------------------------------------------

func TestPlanFileManagerSlugs(t *testing.T) {
	dir := t.TempDir()
	m := NewPlanFileManager(dir)

	slugA := m.GetOrCreateSlug("sess-a")
	if slugA == "" {
		t.Fatal("slug should not be empty")
	}
	// Returned again for same session -> identical.
	if again := m.GetOrCreateSlug("sess-a"); again != slugA {
		t.Errorf("slug for same session changed: %q vs %q", slugA, again)
	}
	// Different session -> (extremely likely) different slug.
	slugB := m.GetOrCreateSlug("sess-b")
	if slugB == slugA {
		t.Log("warning: collision on random slug between sessions (not a test failure)")
	}

	// SetSlug overrides.
	m.SetSlug("sess-c", "custom-slug")
	if got := m.GetOrCreateSlug("sess-c"); got != "custom-slug" {
		t.Errorf("after SetSlug, GetOrCreateSlug = %q, want %q", got, "custom-slug")
	}

	// ClearSlug should drop the entry (a subsequent call re-generates a new one).
	m.ClearSlug("sess-c")
	if got := m.GetOrCreateSlug("sess-c"); got == "custom-slug" {
		t.Errorf("after ClearSlug, slug should be regenerated, got %q", got)
	}

	// ClearAllSlugs wipes everything.
	m.ClearAllSlugs()
	if got := m.GetOrCreateSlug("sess-a"); got == slugA {
		t.Log("warning: post-ClearAllSlugs regenerated the same slug (not a failure)")
	}
}

func TestPlanFileManagerReadWrite(t *testing.T) {
	dir := t.TempDir()
	m := NewPlanFileManager(filepath.Join(dir, "plans"))

	// EnsureDir creates the plans directory.
	if err := m.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	// Reading a non-existent plan returns empty string, no error.
	content, err := m.ReadPlan("sess", "")
	if err != nil {
		t.Errorf("ReadPlan on missing file: %v", err)
	}
	if content != "" {
		t.Errorf("ReadPlan on missing file = %q, want empty", content)
	}

	// Write + read round trip.
	want := "# Plan\n\n- step 1\n- step 2\n"
	if err := m.WritePlan("sess", "", want); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	got, err := m.ReadPlan("sess", "")
	if err != nil {
		t.Fatalf("ReadPlan: %v", err)
	}
	if got != want {
		t.Errorf("ReadPlan = %q, want %q", got, want)
	}

	// Agent-specific plan path is distinct from the main plan path.
	mainPath := m.GetPlanFilePath("sess", "")
	agentPath := m.GetPlanFilePath("sess", "worker1")
	if mainPath == agentPath {
		t.Errorf("agent path should differ from main: main=%q agent=%q", mainPath, agentPath)
	}
	if !strings.Contains(agentPath, "agent-worker1") {
		t.Errorf("agent path = %q, expected it to embed agent id", agentPath)
	}

	// Write an agent plan and verify round trip.
	if err := m.WritePlan("sess", "worker1", "agent content"); err != nil {
		t.Fatalf("WritePlan(agent): %v", err)
	}
	ac, err := m.ReadPlan("sess", "worker1")
	if err != nil || ac != "agent content" {
		t.Errorf("ReadPlan(agent) = %q, err=%v", ac, err)
	}
}

// --- DefaultPlanModeV2Config --------------------------------------------

func TestDefaultPlanModeV2Config(t *testing.T) {
	// Clear env overrides so we get defaults.
	t.Setenv("CLAUDE_CODE_PLAN_V2_AGENT_COUNT", "")
	t.Setenv("CLAUDE_CODE_PLAN_V2_EXPLORE_AGENT_COUNT", "")

	cfg := DefaultPlanModeV2Config()
	if cfg.AgentCount != 1 {
		t.Errorf("default AgentCount = %d, want 1", cfg.AgentCount)
	}
	if cfg.ExploreAgentCount != 3 {
		t.Errorf("default ExploreAgentCount = %d, want 3", cfg.ExploreAgentCount)
	}

	// Env overrides honoured within valid range.
	t.Setenv("CLAUDE_CODE_PLAN_V2_AGENT_COUNT", "5")
	t.Setenv("CLAUDE_CODE_PLAN_V2_EXPLORE_AGENT_COUNT", "7")
	cfg = DefaultPlanModeV2Config()
	if cfg.AgentCount != 5 {
		t.Errorf("override AgentCount = %d, want 5", cfg.AgentCount)
	}
	if cfg.ExploreAgentCount != 7 {
		t.Errorf("override ExploreAgentCount = %d, want 7", cfg.ExploreAgentCount)
	}

	// Invalid values are ignored (out of range / not numeric).
	t.Setenv("CLAUDE_CODE_PLAN_V2_AGENT_COUNT", "0")
	t.Setenv("CLAUDE_CODE_PLAN_V2_EXPLORE_AGENT_COUNT", "abc")
	cfg = DefaultPlanModeV2Config()
	if cfg.AgentCount != 1 {
		t.Errorf("invalid AgentCount override accepted: %d", cfg.AgentCount)
	}
	if cfg.ExploreAgentCount != 3 {
		t.Errorf("invalid ExploreAgentCount override accepted: %d", cfg.ExploreAgentCount)
	}
}

// --- Concurrency smoke test --------------------------------------------

func TestAddStepConcurrentSafe(t *testing.T) {
	p := NewPlan("concurrent")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p.AddStep(fmt.Sprintf("desc-%d", i))
		}(i)
	}
	wg.Wait()
	if len(p.Steps) != 50 {
		t.Errorf("concurrent AddStep produced %d steps, want 50", len(p.Steps))
	}
	// IDs should still be unique.
	seen := make(map[string]bool)
	for _, s := range p.Steps {
		if seen[s.ID] {
			t.Errorf("duplicate step ID under concurrency: %s", s.ID)
		}
		seen[s.ID] = true
	}
}

// --- helpers ------------------------------------------------------------

func stepIDs(steps []*PlanStep) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.ID
	}
	return out
}

func contains(xs []string, target string) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}

// marshalJSON is a small wrapper around encoding/json.Marshal.
func marshalJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}
