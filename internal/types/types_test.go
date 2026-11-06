package types

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// ---------- BuildTool tests ----------

func TestBuildTool_MinimalDefaults(t *testing.T) {
	tool := BuildTool(ToolDef{
		NameFn:        func() string { return "minimal" },
		DescriptionFn: func() string { return "a minimal tool" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object"}
		},
		ExecuteFn: func(ctx ToolContext, args map[string]any) (ToolResult, error) {
			return ToolResult{Content: "ok"}, nil
		},
	})

	if tool.Name() != "minimal" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "minimal")
	}
	if tool.Description() != "a minimal tool" {
		t.Errorf("Description() = %q, want %q", tool.Description(), "a minimal tool")
	}
	schema := tool.InputSchema()
	if schema["type"] != "object" {
		t.Errorf("InputSchema type = %v, want object", schema["type"])
	}

	// Defaults
	if !tool.IsEnabled() {
		t.Error("IsEnabled() should default to true")
	}
	if tool.IsConcurrencySafe(nil) {
		t.Error("IsConcurrencySafe should default to false")
	}
	if tool.IsReadOnly(nil) {
		t.Error("IsReadOnly should default to false")
	}
	if tool.IsDestructive(nil) {
		t.Error("IsDestructive should default to false")
	}

	v := tool.ValidateInput(map[string]any{})
	if !v.Valid {
		t.Errorf("ValidateInput default should be Valid=true, got %+v", v)
	}

	p := tool.CheckPermissions(map[string]any{}, ToolContext{})
	if p.Behavior != PermissionAllow {
		t.Errorf("CheckPermissions default behavior = %q, want %q", p.Behavior, PermissionAllow)
	}
}

func TestBuildTool_OverrideDefaults(t *testing.T) {
	tool := BuildTool(ToolDef{
		NameFn:        func() string { return "custom" },
		DescriptionFn: func() string { return "" },
		InputSchemaFn: func() map[string]any { return nil },
		ExecuteFn: func(ctx ToolContext, args map[string]any) (ToolResult, error) {
			return ToolResult{}, nil
		},
		IsEnabledFn:     func() bool { return false },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsDestructiveFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) ValidationResult {
			return ValidationResult{Valid: false, Message: "bad input"}
		},
		CheckPermFn: func(args map[string]any, ctx ToolContext) PermissionResult {
			return PermissionResult{Behavior: PermissionDeny, Message: "nope"}
		},
	})

	if tool.IsEnabled() {
		t.Error("IsEnabled override failed")
	}
	if !tool.IsConcurrencySafe(nil) {
		t.Error("IsConcurrencySafe override failed")
	}
	if !tool.IsReadOnly(nil) {
		t.Error("IsReadOnly override failed")
	}
	if !tool.IsDestructive(nil) {
		t.Error("IsDestructive override failed")
	}

	v := tool.ValidateInput(nil)
	if v.Valid || v.Message != "bad input" {
		t.Errorf("ValidateInput override = %+v", v)
	}

	p := tool.CheckPermissions(nil, ToolContext{})
	if p.Behavior != PermissionDeny || p.Message != "nope" {
		t.Errorf("CheckPermissions override = %+v", p)
	}
}

func TestBuildTool_ExecuteRoundTrip(t *testing.T) {
	wantContent := "executed with foo=bar"
	tool := BuildTool(ToolDef{
		NameFn:        func() string { return "echo" },
		DescriptionFn: func() string { return "echoes args" },
		InputSchemaFn: func() map[string]any { return map[string]any{} },
		ExecuteFn: func(ctx ToolContext, args map[string]any) (ToolResult, error) {
			if ctx.WorkingDir != "/tmp" {
				return ToolResult{}, errors.New("bad working dir")
			}
			val, _ := args["foo"].(string)
			return ToolResult{
				Content: "executed with foo=" + val,
				NewMessages: []ChatMessage{
					{Role: "assistant", Content: "done"},
				},
			}, nil
		},
	})

	tctx := ToolContext{
		Context:    context.Background(),
		SessionID:  SessionID("ses_test"),
		AgentID:    AgentID("atest-0000000000000000"),
		WorkingDir: "/tmp",
	}
	res, err := tool.Execute(tctx, map[string]any{"foo": "bar"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if res.Content != wantContent {
		t.Errorf("Content = %q, want %q", res.Content, wantContent)
	}
	if res.IsError {
		t.Error("expected IsError=false")
	}
	if len(res.NewMessages) != 1 || res.NewMessages[0].Role != "assistant" {
		t.Errorf("NewMessages = %+v", res.NewMessages)
	}
}

func TestBuildTool_ExecuteError(t *testing.T) {
	sentinel := errors.New("boom")
	tool := BuildTool(ToolDef{
		NameFn:        func() string { return "fail" },
		DescriptionFn: func() string { return "" },
		InputSchemaFn: func() map[string]any { return nil },
		ExecuteFn: func(ctx ToolContext, args map[string]any) (ToolResult, error) {
			return ToolResult{IsError: true, Content: "failed"}, sentinel
		},
	})

	res, err := tool.Execute(ToolContext{}, nil)
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want %v", err, sentinel)
	}
	if !res.IsError {
		t.Error("expected IsError=true")
	}
}

func TestBuildTool_ArgsAwareReadOnly(t *testing.T) {
	tool := BuildTool(ToolDef{
		NameFn:        func() string { return "smart" },
		DescriptionFn: func() string { return "" },
		InputSchemaFn: func() map[string]any { return nil },
		ExecuteFn: func(ctx ToolContext, args map[string]any) (ToolResult, error) {
			return ToolResult{}, nil
		},
		IsReadOnlyFn: func(args map[string]any) bool {
			mode, _ := args["mode"].(string)
			return mode == "read"
		},
	})

	if !tool.IsReadOnly(map[string]any{"mode": "read"}) {
		t.Error("expected read-only when mode=read")
	}
	if tool.IsReadOnly(map[string]any{"mode": "write"}) {
		t.Error("expected not read-only when mode=write")
	}
}

// ---------- ID tests ----------

func TestNewSessionID(t *testing.T) {
	id := NewSessionID()
	if id.IsZero() {
		t.Fatal("new session id should not be zero")
	}
	if !strings.HasPrefix(id.String(), "ses_") {
		t.Errorf("session id = %q, want prefix ses_", id)
	}
	// 4 char prefix + 16 hex chars = 20
	if len(id.String()) != 20 {
		t.Errorf("session id length = %d, want 20", len(id.String()))
	}

	id2 := NewSessionID()
	if id == id2 {
		t.Error("two new session ids should differ")
	}
}

func TestNewAgentID(t *testing.T) {
	plain := NewAgentID("")
	if plain.IsZero() {
		t.Fatal("agent id should not be zero")
	}
	if !ValidateAgentID(plain.String()) {
		t.Errorf("plain agent id %q failed validation", plain)
	}

	labeled := NewAgentID("worker")
	if !strings.Contains(labeled.String(), "worker") {
		t.Errorf("labeled agent id = %q, want contains 'worker'", labeled)
	}
	if !ValidateAgentID(labeled.String()) {
		t.Errorf("labeled agent id %q failed validation", labeled)
	}

	if ValidateAgentID("not-an-agent") {
		t.Error("ValidateAgentID should reject malformed id")
	}
	if ValidateAgentID("") {
		t.Error("ValidateAgentID should reject empty string")
	}
}

func TestNewTaskID(t *testing.T) {
	cases := []string{
		TaskPrefixBash,
		TaskPrefixAgent,
		TaskPrefixRemote,
		TaskPrefixTeammate,
		TaskPrefixWorkflow,
		TaskPrefixMonitor,
	}
	seen := make(map[TaskID]bool)
	for _, p := range cases {
		id := NewTaskID(p)
		if id.IsZero() {
			t.Errorf("task id with prefix %q should not be zero", p)
		}
		if !strings.HasPrefix(id.String(), p) {
			t.Errorf("task id %q missing prefix %q", id, p)
		}
		if len(id.String()) != len(p)+8 {
			t.Errorf("task id %q length = %d, want %d", id, len(id.String()), len(p)+8)
		}
		if seen[id] {
			t.Errorf("duplicate task id %q", id)
		}
		seen[id] = true
	}
}

func TestIDStringAndIsZero(t *testing.T) {
	var s SessionID
	var a AgentID
	var tk TaskID
	if !s.IsZero() || !a.IsZero() || !tk.IsZero() {
		t.Error("zero-valued IDs should report IsZero")
	}
	if s.String() != "" || a.String() != "" || tk.String() != "" {
		t.Error("zero-valued IDs should stringify as empty")
	}

	s2 := SessionID("ses_abc")
	if s2.IsZero() {
		t.Error("non-empty session id should not be zero")
	}
	if s2.String() != "ses_abc" {
		t.Errorf("String() = %q", s2.String())
	}
}

// ---------- Permission tests ----------

func TestPermissionBehaviorConstants(t *testing.T) {
	if PermissionAllow != "allow" || PermissionDeny != "deny" || PermissionAsk != "ask" {
		t.Errorf("permission behavior constants drifted: %q %q %q",
			PermissionAllow, PermissionDeny, PermissionAsk)
	}
}

func TestDefaultPermissionContext(t *testing.T) {
	ctx := DefaultPermissionContext()
	if ctx.Mode != ModeDefault {
		t.Errorf("default mode = %q, want %q", ctx.Mode, ModeDefault)
	}
	if ctx.IsBypassPermissionsAvailable {
		t.Error("bypass should not be available by default")
	}
	if ctx.AdditionalWorkingDirs == nil {
		t.Error("AdditionalWorkingDirs should be initialized")
	}
	// Should be writable
	ctx.AdditionalWorkingDirs["foo"] = "bar"
	if ctx.AdditionalWorkingDirs["foo"] != "bar" {
		t.Error("AdditionalWorkingDirs not writable")
	}
}

func TestPermissionRuleAndDecision(t *testing.T) {
	rule := PermissionRule{
		Source:      SourceUserSettings,
		Behavior:    PermissionAllow,
		ToolName:    "Read",
		RuleContent: "*.txt",
	}
	if rule.Source != "userSettings" {
		t.Errorf("source = %q", rule.Source)
	}

	reason := &PermissionDecisionReason{
		Type:   "rule",
		Detail: "matched user rule",
		Rule:   &rule,
	}
	decision := PermissionDecision{
		Behavior:       PermissionAllow,
		Message:        "allowed by rule",
		DecisionReason: reason,
		ToolUseID:      "tu_1",
	}
	if decision.DecisionReason.Rule.ToolName != "Read" {
		t.Error("decision reason rule did not round trip")
	}
	if decision.ToolUseID != "tu_1" {
		t.Errorf("ToolUseID = %q", decision.ToolUseID)
	}
}

func TestPermissionModeConstants(t *testing.T) {
	modes := []PermissionMode{
		ModeDefault,
		ModeAcceptEdits,
		ModePlan,
		ModeDontAsk,
		ModeBypass,
		ModeAuto,
	}
	seen := make(map[PermissionMode]bool)
	for _, m := range modes {
		if m == "" {
			t.Errorf("empty permission mode in list")
		}
		if seen[m] {
			t.Errorf("duplicate permission mode %q", m)
		}
		seen[m] = true
	}
	if ModeBypass != "bypassPermissions" {
		t.Errorf("ModeBypass = %q", ModeBypass)
	}
}

// ---------- Task tests ----------

func TestTaskStatusIsTerminal(t *testing.T) {
	terminal := []TaskStatus{TaskStatusCompleted, TaskStatusFailed, TaskStatusKilled}
	nonTerminal := []TaskStatus{TaskStatusPending, TaskStatusRunning}

	for _, s := range terminal {
		if !s.IsTerminal() {
			t.Errorf("%q should be terminal", s)
		}
	}
	for _, s := range nonTerminal {
		if s.IsTerminal() {
			t.Errorf("%q should not be terminal", s)
		}
	}
}

func TestNewTaskState(t *testing.T) {
	before := time.Now()
	state := NewTaskState(TaskTypeLocalBash, "run ls", "/tmp/out.log")
	after := time.Now()

	if state.Type != TaskTypeLocalBash {
		t.Errorf("Type = %q", state.Type)
	}
	if state.Status != TaskStatusPending {
		t.Errorf("Status = %q, want pending", state.Status)
	}
	if state.Description != "run ls" {
		t.Errorf("Description = %q", state.Description)
	}
	if state.OutputFile != "/tmp/out.log" {
		t.Errorf("OutputFile = %q", state.OutputFile)
	}
	if state.ID.IsZero() {
		t.Error("ID should be set")
	}
	if !strings.HasPrefix(state.ID.String(), TaskPrefixBash) {
		t.Errorf("ID = %q, want prefix %q", state.ID, TaskPrefixBash)
	}
	if state.StartTime.Before(before) || state.StartTime.After(after) {
		t.Errorf("StartTime %v not in [%v, %v]", state.StartTime, before, after)
	}
	if state.EndTime != nil {
		t.Errorf("EndTime should be nil, got %v", state.EndTime)
	}
}

func TestNewTaskState_PrefixesByType(t *testing.T) {
	cases := map[TaskType]string{
		TaskTypeLocalBash:   TaskPrefixBash,
		TaskTypeLocalAgent:  TaskPrefixAgent,
		TaskTypeRemoteAgent: TaskPrefixRemote,
		TaskTypeTeammate:    TaskPrefixTeammate,
		TaskTypeWorkflow:    TaskPrefixWorkflow,
		TaskTypeMonitorMCP:  TaskPrefixMonitor,
	}
	for tt, prefix := range cases {
		state := NewTaskState(tt, "x", "out")
		if !strings.HasPrefix(state.ID.String(), prefix) {
			t.Errorf("type %q produced id %q, want prefix %q", tt, state.ID, prefix)
		}
	}

	// Unknown type should fall back to "x" prefix
	state := NewTaskState(TaskType("unknown"), "x", "out")
	if !strings.HasPrefix(state.ID.String(), "x") {
		t.Errorf("unknown task type id = %q, want prefix x", state.ID)
	}
}
