package permissions

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/costa/polypod/internal/modes"
	"github.com/costa/polypod/internal/types"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// silentLogger returns a logger that discards all output.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// mockApprover is a minimal ApprovalHandler used to drive the enforcer's
// approval flow during tests. It records how many times it was called and
// returns a canned response (or error).
type mockApprover struct {
	response ApprovalResponse
	err      error
	calls    int32
	lastReq  ApprovalRequest
}

// AskApproval implements the AskApprovalFn signature.
func (m *mockApprover) AskApproval(ctx context.Context, req ApprovalRequest) (ApprovalResponse, error) {
	atomic.AddInt32(&m.calls, 1)
	m.lastReq = req
	if m.err != nil {
		return ApprovalResponse{}, m.err
	}
	return m.response, nil
}

// Calls returns the number of times AskApproval was invoked.
func (m *mockApprover) Calls() int {
	return int(atomic.LoadInt32(&m.calls))
}

// newTestEnforcer creates an enforcer with a silent logger and sensible
// defaults that can be overridden via the mutator callback.
func newTestEnforcer(t *testing.T, mutate func(cfg *EnforcerConfig)) *Enforcer {
	t.Helper()
	cfg := EnforcerConfig{
		Logger:     silentLogger(),
		ProjectDir: t.TempDir(),
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return NewEnforcer(cfg)
}

// checkDecision is a small helper that runs a tool call and asserts the
// behavior returned by the enforcer. It returns the decision so callers can
// make additional assertions.
func checkDecision(t *testing.T, e *Enforcer, tool string, args map[string]any, want types.PermissionBehavior) types.PermissionDecision {
	t.Helper()
	dec, err := e.CheckToolCall(context.Background(), tool, args)
	if err != nil {
		t.Fatalf("CheckToolCall(%q) returned unexpected error: %v", tool, err)
	}
	if dec.Behavior != want {
		t.Fatalf("CheckToolCall(%q) behavior = %q, want %q (msg=%q)", tool, dec.Behavior, want, dec.Message)
	}
	return dec
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

func TestNewEnforcer_NilDependencies(t *testing.T) {
	e := NewEnforcer(EnforcerConfig{})
	if e == nil {
		t.Fatal("NewEnforcer returned nil")
	}
	if e.logger == nil {
		t.Error("expected NewEnforcer to fall back to a default logger when none is provided")
	}
	if e.manager != nil || e.permCtx != nil || e.modes != nil {
		t.Error("expected nil dependencies to remain nil")
	}
	if e.yolo {
		t.Error("expected YOLO mode to default to false")
	}
	// A stats call should work even with no dependencies.
	stats := e.Stats()
	if stats["yolo_mode"] != false {
		t.Errorf("expected yolo_mode=false in stats, got %v", stats["yolo_mode"])
	}
	if stats["total_checks"].(int64) != 0 {
		t.Errorf("expected total_checks=0, got %v", stats["total_checks"])
	}
}

func TestNewEnforcer_FullConfig(t *testing.T) {
	logger := silentLogger()
	mgr := NewManager(logger)
	state := modes.NewState()
	dir := t.TempDir()

	e := NewEnforcer(EnforcerConfig{
		Logger:     logger,
		Manager:    mgr,
		Modes:      state,
		ProjectDir: dir,
		AskFn: func(ctx context.Context, req ApprovalRequest) (ApprovalResponse, error) {
			return ApprovalResponse{Allowed: true}, nil
		},
		YOLO: true,
	})
	if e == nil {
		t.Fatal("NewEnforcer returned nil")
	}
	if e.manager != mgr {
		t.Error("manager not wired into enforcer")
	}
	if e.modes != state {
		t.Error("modes not wired into enforcer")
	}
	if e.projectDir != dir {
		t.Error("project dir not wired into enforcer")
	}
	if !e.isYOLO() {
		t.Error("expected YOLO mode to be enabled when configured")
	}
	if e.askFn == nil {
		t.Error("expected askFn to be wired into enforcer")
	}
}

// ---------------------------------------------------------------------------
// CheckToolCall: defaults by risk level
// ---------------------------------------------------------------------------

func TestCheckToolCall_ReadOnlyToolsAllowed(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args map[string]any
	}{
		{"Read", "Read", map[string]any{"file_path": "/tmp/x.txt"}},
		{"read_file alias", "read_file", map[string]any{"path": "/tmp/x.txt"}},
		{"Glob", "Glob", map[string]any{"pattern": "*.go"}},
		{"Grep", "Grep", map[string]any{"pattern": "foo"}},
		{"list_directory", "list_directory", map[string]any{"path": "/tmp"}},
		{"LSP", "LSP", map[string]any{"method": "hover"}},
		{"TodoWrite", "TodoWrite", map[string]any{"items": []any{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnforcer(t, nil)
			checkDecision(t, e, tc.tool, tc.args, types.PermissionAllow)
		})
	}
}

func TestCheckToolCall_WriteToolsWithinProjectAllowed(t *testing.T) {
	// A plain file edit inside the project dir is classified Low risk, and
	// validateFilePath short-circuits to Allow because the file lives inside
	// the project directory.
	projectDir := t.TempDir()
	target := filepath.Join(projectDir, "main.go")

	e := newTestEnforcer(t, func(cfg *EnforcerConfig) {
		cfg.ProjectDir = projectDir
	})

	t.Run("Edit", func(t *testing.T) {
		checkDecision(t, e, "Edit", map[string]any{"file_path": target}, types.PermissionAllow)
	})
	t.Run("Write", func(t *testing.T) {
		checkDecision(t, e, "Write", map[string]any{"file_path": target}, types.PermissionAllow)
	})
	t.Run("MultiEdit", func(t *testing.T) {
		checkDecision(t, e, "MultiEdit", map[string]any{"file_path": target}, types.PermissionAllow)
	})
}

func TestCheckToolCall_DangerousBashCommand(t *testing.T) {
	// With no approval handler registered, dangerous bash commands must be
	// blocked outright rather than falling through to an approval prompt.
	e := newTestEnforcer(t, nil)

	t.Run("rm -rf /", func(t *testing.T) {
		dec := checkDecision(t, e, "Bash", map[string]any{"command": "rm -rf /"}, types.PermissionDeny)
		if dec.Message == "" {
			t.Error("expected a non-empty denial message")
		}
	})

	t.Run("fork bomb", func(t *testing.T) {
		checkDecision(t, e, "Bash", map[string]any{"command": ":(){ :|:& };:"}, types.PermissionDeny)
	})
}

func TestCheckToolCall_SafeBashCommand(t *testing.T) {
	e := newTestEnforcer(t, nil)

	cases := []struct {
		name    string
		command string
	}{
		{"ls", "ls"},
		{"ls -la", "ls -la"},
		{"pwd", "pwd"},
		{"cat file.txt", "cat file.txt"},
		{"git status", "git status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkDecision(t, e, "Bash", map[string]any{"command": tc.command}, types.PermissionAllow)
		})
	}
}

func TestCheckToolCall_UnknownToolRequiresApproval(t *testing.T) {
	// Unknown tools are classified as Medium risk by ClassifyToolRisk. With
	// no approval handler, they are denied with a "no approval handler"
	// message; with a handler returning Allowed, they are allowed.
	t.Run("without approver denies", func(t *testing.T) {
		e := newTestEnforcer(t, nil)
		checkDecision(t, e, "some_random_tool", map[string]any{}, types.PermissionDeny)
	})

	t.Run("with approver allows", func(t *testing.T) {
		approver := &mockApprover{response: ApprovalResponse{Allowed: true}}
		e := newTestEnforcer(t, func(cfg *EnforcerConfig) {
			cfg.AskFn = approver.AskApproval
		})
		checkDecision(t, e, "some_random_tool", map[string]any{}, types.PermissionAllow)
		if approver.Calls() != 1 {
			t.Errorf("expected approver to be called once, got %d", approver.Calls())
		}
	})
}

// ---------------------------------------------------------------------------
// YOLO mode
// ---------------------------------------------------------------------------

func TestCheckToolCall_YOLOAlwaysAllows(t *testing.T) {
	e := newTestEnforcer(t, func(cfg *EnforcerConfig) { cfg.YOLO = true })

	cases := []struct {
		name string
		tool string
		args map[string]any
	}{
		{"read", "Read", map[string]any{"file_path": "/etc/passwd"}},
		{"edit system file", "Edit", map[string]any{"file_path": "/etc/hosts"}},
		{"rm -rf", "Bash", map[string]any{"command": "rm -rf /"}},
		{"unknown tool", "some_weird_tool", map[string]any{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dec := checkDecision(t, e, tc.tool, tc.args, types.PermissionAllow)
			if dec.DecisionReason == nil || dec.DecisionReason.Type != "mode" {
				t.Errorf("expected YOLO decision reason to have Type=mode, got %+v", dec.DecisionReason)
			}
		})
	}

	stats := e.Stats()
	if stats["total_yolo"].(int64) == 0 {
		t.Error("expected YOLO counter to be incremented")
	}
}

func TestSetYOLOTogglesBehavior(t *testing.T) {
	e := newTestEnforcer(t, nil)
	// Unknown tool without approver denies by default.
	checkDecision(t, e, "strange_tool", map[string]any{}, types.PermissionDeny)

	// Enable YOLO and retry: now it must be allowed.
	e.SetYOLO(true)
	checkDecision(t, e, "strange_tool", map[string]any{}, types.PermissionAllow)

	// Disable again and verify denial comes back.
	e.SetYOLO(false)
	checkDecision(t, e, "strange_tool", map[string]any{}, types.PermissionDeny)
}

// ---------------------------------------------------------------------------
// Rule matching: session allow/deny
// ---------------------------------------------------------------------------

func TestSessionAllowRule_GrantsPermission(t *testing.T) {
	approver := &mockApprover{response: ApprovalResponse{Allowed: false}} // would deny
	e := newTestEnforcer(t, func(cfg *EnforcerConfig) {
		cfg.AskFn = approver.AskApproval
	})

	// Before the rule exists, the unknown tool would trigger the approver
	// (and be denied).
	checkDecision(t, e, "some_random_tool", map[string]any{}, types.PermissionDeny)
	callsBefore := approver.Calls()

	// After adding a session allow, the approver should not be invoked.
	e.AddSessionAllow("some_random_tool", "")
	checkDecision(t, e, "some_random_tool", map[string]any{}, types.PermissionAllow)
	if approver.Calls() != callsBefore {
		t.Errorf("approver should not be called once a session allow rule matches; calls: before=%d after=%d", callsBefore, approver.Calls())
	}
}

func TestSessionDenyRule_BlocksPermission(t *testing.T) {
	e := newTestEnforcer(t, func(cfg *EnforcerConfig) { cfg.YOLO = false })

	// Read is normally allowed.
	checkDecision(t, e, "Read", map[string]any{"file_path": "/tmp/x.txt"}, types.PermissionAllow)

	e.AddSessionDeny("Read", "")
	checkDecision(t, e, "Read", map[string]any{"file_path": "/tmp/x.txt"}, types.PermissionDeny)
}

func TestSessionRulePriority_DenyBeatsAllow(t *testing.T) {
	e := newTestEnforcer(t, nil)

	// Add both a deny and an allow for the same tool; deny is checked first
	// (step 3) before allow (step 4) and must win.
	e.AddSessionAllow("Write", "")
	e.AddSessionDeny("Write", "")

	projectDir := e.projectDir
	checkDecision(t, e, "Write", map[string]any{"file_path": filepath.Join(projectDir, "a.txt")}, types.PermissionDeny)
}

func TestSessionRule_BashPatternMatching(t *testing.T) {
	e := newTestEnforcer(t, nil)

	// Legacy "git:*" prefix pattern should match "git status".
	e.AddSessionAllow("Bash", "git:*")

	// Although "git status" is already allowed because it is read-only, we
	// primarily want to assert the pattern matcher does not error and that
	// the rule is wired correctly for a write-ish command as well.
	checkDecision(t, e, "Bash", map[string]any{"command": "git status"}, types.PermissionAllow)

	// A deny rule with wildcard should also block.
	e.AddSessionDeny("Bash", "curl *")
	checkDecision(t, e, "Bash", map[string]any{"command": "curl http://example.com"}, types.PermissionDeny)
}

func TestSessionRule_ToolNameGlob(t *testing.T) {
	e := newTestEnforcer(t, nil)
	// Glob on tool name: "mcp__*" should match an mcp tool.
	e.AddSessionDeny("mcp__*", "")
	checkDecision(t, e, "mcp__github__create_issue", map[string]any{}, types.PermissionDeny)
}

// ---------------------------------------------------------------------------
// Mode interactions
// ---------------------------------------------------------------------------

func TestPlanMode_BlocksWriteTools(t *testing.T) {
	state := modes.NewState()
	if _, err := state.SetMode("plan"); err != nil {
		t.Fatalf("SetMode(plan) failed: %v", err)
	}
	projectDir := t.TempDir()
	e := newTestEnforcer(t, func(cfg *EnforcerConfig) {
		cfg.Modes = state
		cfg.ProjectDir = projectDir
	})

	t.Run("Edit is blocked", func(t *testing.T) {
		dec := checkDecision(t, e, "Edit",
			map[string]any{"file_path": filepath.Join(projectDir, "main.go")},
			types.PermissionDeny,
		)
		if dec.DecisionReason == nil || dec.DecisionReason.Type != "mode" {
			t.Errorf("expected mode reason, got %+v", dec.DecisionReason)
		}
	})

	t.Run("Write is blocked", func(t *testing.T) {
		checkDecision(t, e, "Write",
			map[string]any{"file_path": filepath.Join(projectDir, "main.go")},
			types.PermissionDeny,
		)
	})

	t.Run("Read is still allowed", func(t *testing.T) {
		checkDecision(t, e, "Read",
			map[string]any{"file_path": filepath.Join(projectDir, "main.go")},
			types.PermissionAllow,
		)
	})
}

func TestEditMode_AllowsWrites(t *testing.T) {
	state := modes.NewState() // default = ModeEdit
	projectDir := t.TempDir()
	e := newTestEnforcer(t, func(cfg *EnforcerConfig) {
		cfg.Modes = state
		cfg.ProjectDir = projectDir
	})

	checkDecision(t, e, "Edit",
		map[string]any{"file_path": filepath.Join(projectDir, "main.go")},
		types.PermissionAllow,
	)
}

func TestAskMode_BlocksAllTools(t *testing.T) {
	state := modes.NewState()
	if _, err := state.SetMode("ask"); err != nil {
		t.Fatalf("SetMode(ask) failed: %v", err)
	}
	e := newTestEnforcer(t, func(cfg *EnforcerConfig) {
		cfg.Modes = state
	})

	t.Run("Read blocked", func(t *testing.T) {
		checkDecision(t, e, "Read", map[string]any{"file_path": "/tmp/x.txt"}, types.PermissionDeny)
	})
	t.Run("Bash blocked", func(t *testing.T) {
		checkDecision(t, e, "Bash", map[string]any{"command": "ls"}, types.PermissionDeny)
	})
}

func TestAutoMode_AutoApprovesLowRisk(t *testing.T) {
	state := modes.NewState()
	if _, err := state.SetMode("auto"); err != nil {
		t.Fatalf("SetMode(auto) failed: %v", err)
	}
	projectDir := t.TempDir()
	e := newTestEnforcer(t, func(cfg *EnforcerConfig) {
		cfg.Modes = state
		cfg.ProjectDir = projectDir
	})

	t.Run("low-risk edit", func(t *testing.T) {
		dec := checkDecision(t, e, "Edit",
			map[string]any{"file_path": filepath.Join(projectDir, "main.go")},
			types.PermissionAllow,
		)
		if dec.DecisionReason == nil {
			t.Fatal("expected DecisionReason to be populated")
		}
	})
}

// ---------------------------------------------------------------------------
// Approval flow
// ---------------------------------------------------------------------------

func TestApprovalFlow_Approved(t *testing.T) {
	approver := &mockApprover{response: ApprovalResponse{Allowed: true}}
	e := newTestEnforcer(t, func(cfg *EnforcerConfig) { cfg.AskFn = approver.AskApproval })

	checkDecision(t, e, "some_random_tool", map[string]any{}, types.PermissionAllow)
	if approver.Calls() != 1 {
		t.Errorf("expected 1 approval call, got %d", approver.Calls())
	}
	if approver.lastReq.ToolName != "some_random_tool" {
		t.Errorf("expected request ToolName=some_random_tool, got %q", approver.lastReq.ToolName)
	}
}

func TestApprovalFlow_Denied(t *testing.T) {
	approver := &mockApprover{response: ApprovalResponse{Allowed: false, Feedback: "nope"}}
	e := newTestEnforcer(t, func(cfg *EnforcerConfig) { cfg.AskFn = approver.AskApproval })

	dec := checkDecision(t, e, "some_random_tool", map[string]any{}, types.PermissionDeny)
	if dec.Message != "permission denied: nope" {
		t.Errorf("expected feedback in message, got %q", dec.Message)
	}
}

func TestApprovalFlow_AlwaysAllowStoresRule(t *testing.T) {
	approver := &mockApprover{response: ApprovalResponse{Allowed: true, Permanent: true}}
	e := newTestEnforcer(t, func(cfg *EnforcerConfig) { cfg.AskFn = approver.AskApproval })

	// First call prompts the user and stores an allow rule.
	checkDecision(t, e, "some_random_tool", map[string]any{}, types.PermissionAllow)
	if approver.Calls() != 1 {
		t.Fatalf("expected 1 approval call after first request, got %d", approver.Calls())
	}

	// Second call should be allowed by the session rule — no additional prompt.
	checkDecision(t, e, "some_random_tool", map[string]any{}, types.PermissionAllow)
	if approver.Calls() != 1 {
		t.Errorf("expected approver NOT to be called again after AlwaysAllow; calls=%d", approver.Calls())
	}

	// Verify the stored rule appears in stats.
	stats := e.Stats()
	if stats["session_allows"].(int) == 0 {
		t.Error("expected session_allows counter to be at least 1 after AlwaysAllow")
	}
}

func TestApprovalFlow_UserAborts(t *testing.T) {
	approver := &mockApprover{response: ApprovalResponse{Abort: true}}
	e := newTestEnforcer(t, func(cfg *EnforcerConfig) { cfg.AskFn = approver.AskApproval })

	dec := checkDecision(t, e, "some_random_tool", map[string]any{}, types.PermissionDeny)
	if dec.Message == "" {
		t.Error("expected non-empty message for abort")
	}
}

func TestApprovalFlow_HandlerError(t *testing.T) {
	// Simulates an approval handler that times out / returns an error.
	approver := &mockApprover{err: errors.New("approval timed out")}
	e := newTestEnforcer(t, func(cfg *EnforcerConfig) { cfg.AskFn = approver.AskApproval })

	dec := checkDecision(t, e, "some_random_tool", map[string]any{}, types.PermissionDeny)
	if dec.Message == "" {
		t.Error("expected non-empty error message")
	}
}

func TestApprovalFlow_NoHandler(t *testing.T) {
	// With no approval handler, medium+ risk operations must deny.
	e := newTestEnforcer(t, nil)
	dec := checkDecision(t, e, "some_random_tool", map[string]any{}, types.PermissionDeny)
	if dec.DecisionReason == nil {
		t.Fatal("expected DecisionReason to be set")
	}
}

// ---------------------------------------------------------------------------
// ClassifyToolRisk
// ---------------------------------------------------------------------------

func TestClassifyToolRisk(t *testing.T) {
	cases := []struct {
		name       string
		tool       string
		args       map[string]any
		wantLevel  ToolRiskLevel
		wantIsWrt  bool
		wantIsDest bool
	}{
		{
			name:      "read_file is safe",
			tool:      "read_file",
			args:      map[string]any{"path": "/tmp/x"},
			wantLevel: ToolRiskSafe,
		},
		{
			name:      "Glob is safe",
			tool:      "Glob",
			args:      map[string]any{"pattern": "*.go"},
			wantLevel: ToolRiskSafe,
		},
		{
			name:      "Edit in-project is low",
			tool:      "Edit",
			args:      map[string]any{"file_path": "./my_file.go"},
			wantLevel: ToolRiskLow,
			wantIsWrt: true,
		},
		{
			name:      "Write in-project is low",
			tool:      "Write",
			args:      map[string]any{"file_path": "./new_file.go"},
			wantLevel: ToolRiskLow,
			wantIsWrt: true,
		},
		{
			name:      "WebFetch is low",
			tool:      "WebFetch",
			args:      map[string]any{"url": "https://example.com"},
			wantLevel: ToolRiskLow,
		},
		{
			name:       "Bash rm -rf is critical",
			tool:       "Bash",
			args:       map[string]any{"command": "rm -rf /"},
			wantLevel:  ToolRiskCritical,
			wantIsWrt:  true,
			wantIsDest: true,
		},
		{
			name:      "Bash ls is safe",
			tool:      "Bash",
			args:      map[string]any{"command": "ls"},
			wantLevel: ToolRiskSafe,
		},
		{
			name:      "Bash pwd is safe",
			tool:      "Bash",
			args:      map[string]any{"command": "pwd"},
			wantLevel: ToolRiskSafe,
		},
		{
			name:      "run_command echo is safe",
			tool:      "run_command",
			args:      map[string]any{"command": "echo hello"},
			wantLevel: ToolRiskSafe,
		},
		{
			name:       "delete_file is high",
			tool:       "delete_file",
			args:       map[string]any{"path": "/tmp/x"},
			wantLevel:  ToolRiskHigh,
			wantIsWrt:  true,
			wantIsDest: true,
		},
		{
			name:      "unknown tool is medium",
			tool:      "totally_unknown_tool_xyz",
			args:      map[string]any{},
			wantLevel: ToolRiskMedium,
			wantIsWrt: true,
		},
		{
			name:      "mcp tool is medium",
			tool:      "mcp__github__list_issues",
			args:      map[string]any{},
			wantLevel: ToolRiskMedium,
			wantIsWrt: true,
		},
		{
			name:      "git_status is safe",
			tool:      "git_status",
			args:      map[string]any{},
			wantLevel: ToolRiskSafe,
		},
		{
			name:      "git_push is high",
			tool:      "git_push",
			args:      map[string]any{},
			wantLevel: ToolRiskHigh,
			wantIsWrt: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyToolRisk(tc.tool, tc.args)
			if got.Level != tc.wantLevel {
				t.Errorf("ClassifyToolRisk(%q).Level = %s, want %s (reason=%q)", tc.tool, got.Level, tc.wantLevel, got.Reason)
			}
			if got.IsWrite != tc.wantIsWrt {
				t.Errorf("ClassifyToolRisk(%q).IsWrite = %v, want %v", tc.tool, got.IsWrite, tc.wantIsWrt)
			}
			if got.IsDestructive != tc.wantIsDest {
				t.Errorf("ClassifyToolRisk(%q).IsDestructive = %v, want %v", tc.tool, got.IsDestructive, tc.wantIsDest)
			}
		})
	}
}

func TestClassifyToolRisk_LevelStringer(t *testing.T) {
	cases := map[ToolRiskLevel]string{
		ToolRiskSafe:     "safe",
		ToolRiskLow:      "low",
		ToolRiskMedium:   "medium",
		ToolRiskHigh:     "high",
		ToolRiskCritical: "critical",
	}
	for level, want := range cases {
		if got := level.String(); got != want {
			t.Errorf("ToolRiskLevel(%d).String() = %q, want %q", level, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// Stats & miscellaneous
// ---------------------------------------------------------------------------

func TestEnforcerStats_TrackCounters(t *testing.T) {
	approver := &mockApprover{response: ApprovalResponse{Allowed: true}}
	e := newTestEnforcer(t, func(cfg *EnforcerConfig) { cfg.AskFn = approver.AskApproval })

	// Allow path: a safe tool.
	checkDecision(t, e, "Read", map[string]any{"file_path": "/tmp/x"}, types.PermissionAllow)
	// Deny path: a session-denied tool.
	e.AddSessionDeny("Read", "")
	checkDecision(t, e, "Read", map[string]any{"file_path": "/tmp/x"}, types.PermissionDeny)
	// Ask path: an unknown tool approved via handler.
	checkDecision(t, e, "unknown_tool", map[string]any{}, types.PermissionAllow)

	stats := e.Stats()
	if stats["total_checks"].(int64) != 3 {
		t.Errorf("total_checks = %v, want 3", stats["total_checks"])
	}
	if stats["total_allowed"].(int64) < 2 {
		t.Errorf("total_allowed = %v, want at least 2", stats["total_allowed"])
	}
	if stats["total_denied"].(int64) < 1 {
		t.Errorf("total_denied = %v, want at least 1", stats["total_denied"])
	}
	if stats["total_asked"].(int64) < 1 {
		t.Errorf("total_asked = %v, want at least 1", stats["total_asked"])
	}
}

func TestCheckToolCall_DangerousFileEdit(t *testing.T) {
	// Editing a known dangerous file (e.g. ~/.bashrc) should be blocked
	// by path validation before any approval prompt is shown.
	approver := &mockApprover{response: ApprovalResponse{Allowed: true}}
	e := newTestEnforcer(t, func(cfg *EnforcerConfig) { cfg.AskFn = approver.AskApproval })

	dangerous := filepath.Join(t.TempDir(), ".bashrc")
	dec := checkDecision(t, e, "Edit", map[string]any{"file_path": dangerous}, types.PermissionDeny)
	if dec.DecisionReason == nil || dec.DecisionReason.Type != "safetyCheck" {
		t.Errorf("expected safetyCheck reason, got %+v", dec.DecisionReason)
	}
	if approver.Calls() != 0 {
		t.Errorf("approver should not be called when path validation blocks; calls=%d", approver.Calls())
	}
}

func TestAddSessionAllow_Concurrency(t *testing.T) {
	// Quick smoke test that AddSessionAllow/Deny are safe under concurrent
	// use with CheckToolCall — protects against regressions in the enforcer's
	// RW mutex usage.
	e := newTestEnforcer(t, nil)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			e.AddSessionAllow("X", "")
		}
		close(done)
	}()
	for i := 0; i < 50; i++ {
		_, _ = e.CheckToolCall(context.Background(), "Read", map[string]any{"file_path": "/tmp/x"})
	}
	<-done
	stats := e.Stats()
	if stats["session_allows"].(int) != 50 {
		t.Errorf("expected 50 session allows, got %v", stats["session_allows"])
	}
}
