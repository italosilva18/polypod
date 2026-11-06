package agentctx

import (
	"context"
	"sync"
	"testing"

	"github.com/costa/polypod/internal/types"
)

// TestNewSubagentContext verifies the subagent constructor populates fields correctly.
func TestNewSubagentContext(t *testing.T) {
	sessionID := types.NewSessionID()
	ac := NewSubagentContext(sessionID, "Explore", true)

	if ac == nil {
		t.Fatal("NewSubagentContext returned nil")
	}
	if ac.SessionID != sessionID {
		t.Errorf("SessionID = %q, want %q", ac.SessionID, sessionID)
	}
	if ac.SubagentName != "Explore" {
		t.Errorf("SubagentName = %q, want %q", ac.SubagentName, "Explore")
	}
	if ac.AgentType != AgentTypeSubagent {
		t.Errorf("AgentType = %q, want %q", ac.AgentType, AgentTypeSubagent)
	}
	if !ac.IsBuiltIn {
		t.Error("IsBuiltIn = false, want true")
	}
	if ac.AgentID == "" {
		t.Error("AgentID should be auto-generated")
	}
}

// TestNewTeammateContext verifies teammate constructor populates all fields.
func TestNewTeammateContext(t *testing.T) {
	agentID := types.NewAgentID("alice")
	sessionID := types.NewSessionID()
	parentSessionID := types.NewSessionID()

	ac := NewTeammateContext(agentID, sessionID, parentSessionID, "red-team", true, true)

	if ac.AgentID != agentID {
		t.Errorf("AgentID = %q, want %q", ac.AgentID, agentID)
	}
	if ac.SessionID != sessionID {
		t.Errorf("SessionID = %q, want %q", ac.SessionID, sessionID)
	}
	if ac.ParentSessionID != parentSessionID {
		t.Errorf("ParentSessionID = %q, want %q", ac.ParentSessionID, parentSessionID)
	}
	if ac.AgentType != AgentTypeTeammate {
		t.Errorf("AgentType = %q, want %q", ac.AgentType, AgentTypeTeammate)
	}
	if ac.TeamName != "red-team" {
		t.Errorf("TeamName = %q, want %q", ac.TeamName, "red-team")
	}
	if !ac.IsTeamLead {
		t.Error("IsTeamLead = false, want true")
	}
	if !ac.PlanModeRequired {
		t.Error("PlanModeRequired = false, want true")
	}
}

// TestSetGetAgentContext validates the WithAgent-style context builder and retrieval helper.
func TestSetGetAgentContext(t *testing.T) {
	base := context.Background()

	// Missing context should return nil.
	if got := GetAgentContext(base); got != nil {
		t.Errorf("GetAgentContext(empty) = %v, want nil", got)
	}

	ac := NewSubagentContext(types.NewSessionID(), "Research", true)
	ctx := SetAgentContext(base, ac)

	got := GetAgentContext(ctx)
	if got == nil {
		t.Fatal("GetAgentContext returned nil after SetAgentContext")
	}
	if got != ac {
		t.Error("GetAgentContext did not return the same AgentContext pointer")
	}
	if got.SubagentName != "Research" {
		t.Errorf("SubagentName = %q, want %q", got.SubagentName, "Research")
	}

	// Original base context must remain untouched.
	if GetAgentContext(base) != nil {
		t.Error("SetAgentContext mutated parent context")
	}
}

// TestAgentContextTypeHelpers verifies the type predicate helpers.
func TestAgentContextTypeHelpers(t *testing.T) {
	sub := NewSubagentContext(types.NewSessionID(), "Explore", true)
	team := NewTeammateContext(types.NewAgentID("bob"), types.NewSessionID(), types.NewSessionID(), "blue", false, false)
	standalone := &AgentContext{
		AgentID:   types.NewAgentID("solo"),
		SessionID: types.NewSessionID(),
		AgentType: AgentTypeStandalone,
	}

	if !IsSubagentContext(sub) {
		t.Error("IsSubagentContext(sub) = false, want true")
	}
	if IsSubagentContext(team) {
		t.Error("IsSubagentContext(team) = true, want false")
	}
	if IsSubagentContext(nil) {
		t.Error("IsSubagentContext(nil) = true, want false")
	}

	if !IsTeammateContext(team) {
		t.Error("IsTeammateContext(team) = false, want true")
	}
	if IsTeammateContext(sub) {
		t.Error("IsTeammateContext(sub) = true, want false")
	}

	if !IsStandaloneContext(standalone) {
		t.Error("IsStandaloneContext(standalone) = false, want true")
	}
	if IsStandaloneContext(team) {
		t.Error("IsStandaloneContext(team) = true, want false")
	}
	if IsStandaloneContext(nil) {
		t.Error("IsStandaloneContext(nil) = true, want false")
	}
}

// TestGetSubagentLogName checks analytics name rules for built-in vs user-defined agents.
func TestGetSubagentLogName(t *testing.T) {
	builtin := NewSubagentContext(types.NewSessionID(), "Explore", true)
	custom := NewSubagentContext(types.NewSessionID(), "my-tool", false)
	team := NewTeammateContext(types.NewAgentID("x"), types.NewSessionID(), types.NewSessionID(), "t", false, false)
	blank := NewSubagentContext(types.NewSessionID(), "", true)

	if got := GetSubagentLogName(builtin); got != "Explore" {
		t.Errorf("builtin = %q, want %q", got, "Explore")
	}
	if got := GetSubagentLogName(custom); got != "user-defined" {
		t.Errorf("custom = %q, want %q", got, "user-defined")
	}
	if got := GetSubagentLogName(team); got != "" {
		t.Errorf("teammate = %q, want empty", got)
	}
	if got := GetSubagentLogName(blank); got != "" {
		t.Errorf("blank name = %q, want empty", got)
	}
	if got := GetSubagentLogName(nil); got != "" {
		t.Errorf("nil = %q, want empty", got)
	}
}

// TestGetStandaloneAgentName verifies standalone name resolution semantics.
func TestGetStandaloneAgentName(t *testing.T) {
	standalone := &AgentContext{
		AgentType:    AgentTypeStandalone,
		SubagentName: "batch-runner",
	}
	if got := GetStandaloneAgentName(standalone); got != "batch-runner" {
		t.Errorf("standalone = %q, want %q", got, "batch-runner")
	}

	team := NewTeammateContext(types.NewAgentID("x"), types.NewSessionID(), types.NewSessionID(), "t", false, false)
	if got := GetStandaloneAgentName(team); got != "" {
		t.Errorf("teammate = %q, want empty", got)
	}

	sub := NewSubagentContext(types.NewSessionID(), "Explore", true)
	if got := GetStandaloneAgentName(sub); got != "" {
		t.Errorf("subagent = %q, want empty", got)
	}

	if got := GetStandaloneAgentName(nil); got != "" {
		t.Errorf("nil = %q, want empty", got)
	}
}

// TestConsumeInvokingRequestID verifies sparse-edge once-only semantics and reset.
func TestConsumeInvokingRequestID(t *testing.T) {
	// nil context should yield not-ok.
	if _, _, ok := ConsumeInvokingRequestID(nil); ok {
		t.Error("ConsumeInvokingRequestID(nil) ok = true, want false")
	}

	// Context without request ID should yield not-ok.
	empty := NewSubagentContext(types.NewSessionID(), "x", true)
	if _, _, ok := ConsumeInvokingRequestID(empty); ok {
		t.Error("empty request_id ok = true, want false")
	}

	ac := NewSubagentContext(types.NewSessionID(), "x", true)
	ac.InvokingRequestID = "req_123"
	ac.InvocationKind = InvocationSpawn

	// First call returns the value.
	reqID, kind, ok := ConsumeInvokingRequestID(ac)
	if !ok {
		t.Fatal("first consume ok = false, want true")
	}
	if reqID != "req_123" {
		t.Errorf("reqID = %q, want %q", reqID, "req_123")
	}
	if kind != InvocationSpawn {
		t.Errorf("kind = %q, want %q", kind, InvocationSpawn)
	}

	// Second call returns not-ok (sparse edge).
	if _, _, ok := ConsumeInvokingRequestID(ac); ok {
		t.Error("second consume ok = true, want false")
	}

	// After reset, it emits again.
	ResetInvocationEmitted(ac)
	if _, _, ok := ConsumeInvokingRequestID(ac); !ok {
		t.Error("consume after reset ok = false, want true")
	}

	// ResetInvocationEmitted on nil must not panic.
	ResetInvocationEmitted(nil)
}

// TestContextStack exercises push/pop/peek/depth of the nested context stack.
func TestContextStack(t *testing.T) {
	cs := NewContextStack()
	if cs == nil {
		t.Fatal("NewContextStack returned nil")
	}

	if cs.Depth() != 0 {
		t.Errorf("initial Depth = %d, want 0", cs.Depth())
	}
	if cs.Peek() != nil {
		t.Error("Peek on empty stack should return nil")
	}
	if cs.Pop() != nil {
		t.Error("Pop on empty stack should return nil")
	}

	a := NewSubagentContext(types.NewSessionID(), "A", true)
	b := NewSubagentContext(types.NewSessionID(), "B", true)
	c := NewSubagentContext(types.NewSessionID(), "C", true)

	cs.Push(a)
	cs.Push(b)
	cs.Push(c)

	if cs.Depth() != 3 {
		t.Errorf("Depth after 3 pushes = %d, want 3", cs.Depth())
	}
	if peek := cs.Peek(); peek != c {
		t.Error("Peek did not return top of stack")
	}
	// Peek must not mutate depth.
	if cs.Depth() != 3 {
		t.Errorf("Depth after Peek = %d, want 3", cs.Depth())
	}

	if top := cs.Pop(); top != c {
		t.Error("Pop did not return top of stack")
	}
	if top := cs.Pop(); top != b {
		t.Error("second Pop did not return previous top")
	}
	if cs.Depth() != 1 {
		t.Errorf("Depth after 2 pops = %d, want 1", cs.Depth())
	}
	if top := cs.Pop(); top != a {
		t.Error("final Pop did not return first pushed")
	}
	if cs.Depth() != 0 {
		t.Errorf("Depth after full pop = %d, want 0", cs.Depth())
	}
}

// TestContextStackConcurrent exercises the mutex under parallel push/pop.
func TestContextStackConcurrent(t *testing.T) {
	cs := NewContextStack()
	const workers = 32
	const perWorker = 50

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				ac := NewSubagentContext(types.NewSessionID(), "x", true)
				cs.Push(ac)
				_ = cs.Peek()
				_ = cs.Pop()
			}
		}()
	}
	wg.Wait()

	if d := cs.Depth(); d != 0 {
		t.Errorf("final Depth = %d, want 0", d)
	}
}

// TestAgentContextInheritsThroughDerivedContext ensures derived contexts carry the agent.
func TestAgentContextInheritsThroughDerivedContext(t *testing.T) {
	ac := NewSubagentContext(types.NewSessionID(), "Explore", true)
	parent := SetAgentContext(context.Background(), ac)

	// Derive a child with a separate value; agent context must still be visible.
	type otherKey struct{}
	child := context.WithValue(parent, otherKey{}, "value")

	got := GetAgentContext(child)
	if got != ac {
		t.Error("derived context did not inherit agent context")
	}

	// Cancellation-derived contexts also inherit.
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	if GetAgentContext(ctx) != ac {
		t.Error("cancel-derived context lost agent identity")
	}
}
