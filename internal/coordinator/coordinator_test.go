package coordinator

import (
	"os"
	"strings"
	"testing"
	"time"
)

// --- Env helpers ---

// withEnv sets an env var for the duration of a test and restores the original value.
func withEnv(t *testing.T, key, val string) {
	t.Helper()
	orig, had := os.LookupEnv(key)
	if val == "" {
		os.Unsetenv(key)
	} else {
		os.Setenv(key, val)
	}
	t.Cleanup(func() {
		if had {
			os.Setenv(key, orig)
		} else {
			os.Unsetenv(key)
		}
	})
}

// --- Mode detection ---

func TestIsCoordinatorMode(t *testing.T) {
	cases := []struct {
		val  string
		want bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"no", false},
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"Yes", true},
		{"on", true},
		{" 1 ", true},
		{"bogus", false},
	}
	for _, tc := range cases {
		withEnv(t, "POLYPOD_COORDINATOR_MODE", tc.val)
		got := IsCoordinatorMode()
		if got != tc.want {
			t.Errorf("IsCoordinatorMode()=%v for val=%q; want %v", got, tc.val, tc.want)
		}
	}
}

func TestMatchSessionMode(t *testing.T) {
	// Empty session mode -> no-op
	withEnv(t, "POLYPOD_COORDINATOR_MODE", "")
	if msg := MatchSessionMode(""); msg != "" {
		t.Errorf("empty session mode should return empty string, got %q", msg)
	}

	// Session is coordinator but current is not -> enter coordinator
	withEnv(t, "POLYPOD_COORDINATOR_MODE", "")
	msg := MatchSessionMode("coordinator")
	if !strings.Contains(msg, "Entered") {
		t.Errorf("expected 'Entered' message, got %q", msg)
	}
	if !IsCoordinatorMode() {
		t.Error("expected coordinator mode to be enabled after MatchSessionMode")
	}

	// Same mode -> no switch
	withEnv(t, "POLYPOD_COORDINATOR_MODE", "1")
	if msg := MatchSessionMode("coordinator"); msg != "" {
		t.Errorf("expected no switch when modes match, got %q", msg)
	}

	// Session is not coordinator but current is -> exit
	withEnv(t, "POLYPOD_COORDINATOR_MODE", "1")
	msg = MatchSessionMode("normal")
	if !strings.Contains(msg, "Exited") {
		t.Errorf("expected 'Exited' message, got %q", msg)
	}
	if IsCoordinatorMode() {
		t.Error("expected coordinator mode to be disabled after exit")
	}
}

// --- Constructors ---

func TestNewTeamState(t *testing.T) {
	ts := NewTeamState()
	if ts == nil {
		t.Fatal("NewTeamState returned nil")
	}
	if ts.workers == nil {
		t.Error("workers map not initialized")
	}
	if len(ts.AllWorkers()) != 0 {
		t.Error("new team state should have no workers")
	}
	if ts.ScratchpadDir() != "" {
		t.Error("new team state should have empty scratchpad dir")
	}
}

func TestNewCoordinatorMode(t *testing.T) {
	cm := NewCoordinatorMode()
	if cm == nil {
		t.Fatal("NewCoordinatorMode returned nil")
	}
	if cm.Team() == nil {
		t.Error("Team() is nil on fresh coordinator")
	}
	if cm.agentToolName != "agent_run" {
		t.Errorf("unexpected agentToolName: %q", cm.agentToolName)
	}
	if cm.sendMsgToolName != "send_message" {
		t.Errorf("unexpected sendMsgToolName: %q", cm.sendMsgToolName)
	}
	if cm.taskStopToolName != "task_stop" {
		t.Errorf("unexpected taskStopToolName: %q", cm.taskStopToolName)
	}
}

// --- Activation / deactivation (state machine) ---

func TestCoordinatorModeActivateDeactivate(t *testing.T) {
	withEnv(t, "POLYPOD_COORDINATOR_MODE", "")
	cm := NewCoordinatorMode()

	if cm.IsActive() {
		t.Error("new coordinator should not be active")
	}

	cm.Activate()
	if !cm.IsActive() {
		t.Error("coordinator should be active after Activate")
	}
	if os.Getenv("POLYPOD_COORDINATOR_MODE") != "1" {
		t.Error("Activate should set POLYPOD_COORDINATOR_MODE=1")
	}

	cm.Deactivate()
	if cm.IsActive() {
		t.Error("coordinator should not be active after Deactivate")
	}
	if os.Getenv("POLYPOD_COORDINATOR_MODE") != "" {
		t.Error("Deactivate should unset POLYPOD_COORDINATOR_MODE")
	}

	// IsActive should also return true when only env var is set.
	withEnv(t, "POLYPOD_COORDINATOR_MODE", "1")
	cm2 := NewCoordinatorMode()
	if !cm2.IsActive() {
		t.Error("IsActive should read env var when active flag is false")
	}
}

// --- Worker (agent) registration ---

func TestTeamStateAddAndGetWorker(t *testing.T) {
	ts := NewTeamState()
	ts.AddWorker("w1", "research task")

	w := ts.GetWorker("w1")
	if w == nil {
		t.Fatal("GetWorker returned nil")
	}
	if w.ID != "w1" {
		t.Errorf("unexpected ID %q", w.ID)
	}
	if w.Description != "research task" {
		t.Errorf("unexpected Description %q", w.Description)
	}
	if w.Status != WorkerStatusRunning {
		t.Errorf("new worker should have status running, got %q", w.Status)
	}
	if w.StartedAt.IsZero() {
		t.Error("StartedAt should be set")
	}
	if w.FinishedAt != nil {
		t.Error("FinishedAt should be nil for running worker")
	}

	// Missing worker
	if ts.GetWorker("nope") != nil {
		t.Error("GetWorker for missing ID should return nil")
	}

	// GetWorker returns a copy — mutations should not affect the stored worker.
	w.Description = "tampered"
	again := ts.GetWorker("w1")
	if again.Description != "research task" {
		t.Errorf("GetWorker should return a copy; got mutated description %q", again.Description)
	}
}

func TestTeamStateUpdateWorkerStatus(t *testing.T) {
	ts := NewTeamState()
	ts.AddWorker("w1", "task")

	// Sleep briefly to ensure a measurable duration.
	time.Sleep(2 * time.Millisecond)
	ts.UpdateWorkerStatus("w1", WorkerStatusCompleted, "done", "")

	w := ts.GetWorker("w1")
	if w.Status != WorkerStatusCompleted {
		t.Errorf("expected status completed, got %q", w.Status)
	}
	if w.Result != "done" {
		t.Errorf("expected result 'done', got %q", w.Result)
	}
	if w.FinishedAt == nil {
		t.Error("FinishedAt should be set after update")
	}
	if w.DurationMs < 0 {
		t.Errorf("DurationMs should be >= 0, got %d", w.DurationMs)
	}

	// Failed update
	ts.AddWorker("w2", "task2")
	ts.UpdateWorkerStatus("w2", WorkerStatusFailed, "", "boom")
	w2 := ts.GetWorker("w2")
	if w2.Status != WorkerStatusFailed || w2.Error != "boom" {
		t.Errorf("failed update wrong: %+v", w2)
	}

	// Update on missing worker should not panic.
	ts.UpdateWorkerStatus("ghost", WorkerStatusFailed, "", "nope")
}

func TestTeamStateUpdateUsageAndRemove(t *testing.T) {
	ts := NewTeamState()
	ts.AddWorker("w1", "task")

	ts.UpdateWorkerUsage("w1", 1234, 5)
	w := ts.GetWorker("w1")
	if w.TotalTokens != 1234 || w.ToolUses != 5 {
		t.Errorf("usage update failed: %+v", w)
	}

	// No-op on missing worker
	ts.UpdateWorkerUsage("missing", 1, 1)

	ts.RemoveWorker("w1")
	if ts.GetWorker("w1") != nil {
		t.Error("worker should be gone after RemoveWorker")
	}
}

// --- Listing active coordinators / workers ---

func TestTeamStateListWorkers(t *testing.T) {
	ts := NewTeamState()
	ts.AddWorker("w1", "a")
	ts.AddWorker("w2", "b")
	ts.AddWorker("w3", "c")

	// All three start as running.
	if got := len(ts.RunningWorkers()); got != 3 {
		t.Errorf("expected 3 running, got %d", got)
	}
	if got := len(ts.AllWorkers()); got != 3 {
		t.Errorf("expected 3 total, got %d", got)
	}

	// Complete one, fail one.
	ts.UpdateWorkerStatus("w1", WorkerStatusCompleted, "ok", "")
	ts.UpdateWorkerStatus("w2", WorkerStatusFailed, "", "err")

	if got := len(ts.RunningWorkers()); got != 1 {
		t.Errorf("expected 1 running, got %d", got)
	}
	if got := len(ts.AllWorkers()); got != 3 {
		t.Errorf("expected 3 total still, got %d", got)
	}

	// RunningWorkers should only contain w3.
	running := ts.RunningWorkers()
	if len(running) == 1 && running[0].ID != "w3" {
		t.Errorf("expected running worker w3, got %s", running[0].ID)
	}
}

func TestTeamStateScratchpadDir(t *testing.T) {
	ts := NewTeamState()
	ts.SetScratchpadDir("/tmp/scratch")
	if got := ts.ScratchpadDir(); got != "/tmp/scratch" {
		t.Errorf("expected /tmp/scratch, got %q", got)
	}
}

// --- Task delegation (using fake agents / stub calls) ---

func TestCoordinatorDelegateTask(t *testing.T) {
	cm := NewCoordinatorMode()

	id := cm.DelegateTask("fake agent: search code", "prompt body")
	if id == "" {
		t.Fatal("DelegateTask returned empty id")
	}
	if !strings.HasPrefix(id, "worker-") {
		t.Errorf("expected worker- prefix, got %q", id)
	}

	w := cm.Team().GetWorker(id)
	if w == nil {
		t.Fatalf("delegated worker %q not found in team state", id)
	}
	if w.Description != "fake agent: search code" {
		t.Errorf("unexpected description %q", w.Description)
	}
	if w.Status != WorkerStatusRunning {
		t.Errorf("delegated worker should start as running, got %q", w.Status)
	}

	// Delegate a second task — should also be tracked.
	// (We don't assert uniqueness of IDs because the generator uses a
	// time-based nonce which could theoretically collide.)
	id2 := cm.DelegateTask("fake agent: write tests", "another prompt")
	if id2 == "" {
		t.Fatal("second DelegateTask returned empty id")
	}
	if cm.Team().GetWorker(id2) == nil {
		t.Error("second delegated worker not tracked")
	}
}

func TestCoordinatorCollectResults(t *testing.T) {
	cm := NewCoordinatorMode()

	id1 := cm.DelegateTask("agent one", "p1")
	id2 := cm.DelegateTask("agent two", "p2")
	id3 := cm.DelegateTask("agent three", "p3")

	// Mark one completed, one failed, leave one running.
	cm.Team().UpdateWorkerUsage(id1, 100, 3)
	cm.Team().UpdateWorkerStatus(id1, WorkerStatusCompleted, "yay", "")
	cm.Team().UpdateWorkerStatus(id2, WorkerStatusFailed, "", "nope")
	_ = id3 // stays running

	results := cm.CollectResults()
	if len(results) != 2 {
		t.Fatalf("expected 2 collected results, got %d", len(results))
	}

	var gotDone, gotFail bool
	for _, r := range results {
		switch r.Status {
		case WorkerStatusCompleted:
			gotDone = true
			if r.Result != "yay" || r.TotalTokens != 100 || r.ToolUses != 3 {
				t.Errorf("unexpected completed result: %+v", r)
			}
		case WorkerStatusFailed:
			gotFail = true
			if r.Error != "nope" {
				t.Errorf("unexpected failed result: %+v", r)
			}
		default:
			t.Errorf("unexpected status in results: %q", r.Status)
		}
	}
	if !gotDone || !gotFail {
		t.Errorf("missing expected statuses; gotDone=%v gotFail=%v", gotDone, gotFail)
	}
}

// --- Tool filtering & prompts ---

func TestCoordinatorFilterTools(t *testing.T) {
	cm := NewCoordinatorMode()
	all := []string{"read", "write", "agent_run", "bash", "send_message", "task_stop", "grep"}
	got := cm.FilterTools(all)

	want := map[string]bool{
		"agent_run":    true,
		"send_message": true,
		"task_stop":    true,
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d coordinator tools, got %d (%v)", len(want), len(got), got)
	}
	for _, name := range got {
		if !want[name] {
			t.Errorf("unexpected tool %q in filtered set", name)
		}
	}

	// Disallowed tools must not leak through.
	for _, name := range got {
		if name == "read" || name == "write" || name == "bash" || name == "grep" {
			t.Errorf("forbidden tool %q leaked through FilterTools", name)
		}
	}
}

func TestCoordinatorTransformSystemPrompt(t *testing.T) {
	cm := NewCoordinatorMode()

	// Short tool list (<=3) => explicit tools mentioned.
	p := cm.TransformSystemPrompt([]string{"read", "write"}, nil, "")
	if !strings.Contains(p, "You are Polypod") {
		t.Error("prompt missing header")
	}
	if !strings.Contains(p, "agent_run") || !strings.Contains(p, "send_message") || !strings.Contains(p, "task_stop") {
		t.Error("prompt should mention coordinator tools")
	}
	if !strings.Contains(p, "read, write") {
		t.Errorf("prompt should list explicit worker tools for short list; got:\n%s", p)
	}

	// Long list (>3) => generic skill phrasing.
	p2 := cm.TransformSystemPrompt(
		[]string{"read", "write", "bash", "grep"},
		[]string{"server1", "server2"},
		"/tmp/scratch",
	)
	if !strings.Contains(p2, "standard tools") {
		t.Error("expected generic worker capability phrasing for long tool list")
	}
	if !strings.Contains(p2, "server1") || !strings.Contains(p2, "server2") {
		t.Error("prompt should mention MCP servers")
	}
	if !strings.Contains(p2, "/tmp/scratch") {
		t.Error("prompt should mention scratchpad dir")
	}
}

func TestCoordinatorAugmentWithTeamStatus(t *testing.T) {
	cm := NewCoordinatorMode()

	// Empty team — empty status.
	if s := cm.AugmentWithTeamStatus(); s != "" {
		t.Errorf("expected empty status for empty team, got %q", s)
	}

	id1 := cm.DelegateTask("alpha", "p")
	id2 := cm.DelegateTask("beta", "p")
	cm.Team().UpdateWorkerStatus(id1, WorkerStatusCompleted, "done", "")
	_ = id2 // still running

	status := cm.AugmentWithTeamStatus()
	if !strings.Contains(status, "Current Team Status") {
		t.Error("missing status header")
	}
	if !strings.Contains(status, "1 running") || !strings.Contains(status, "1 completed") {
		t.Errorf("wrong counts in status:\n%s", status)
	}
	if !strings.Contains(status, "alpha") || !strings.Contains(status, "beta") {
		t.Error("status should list worker descriptions")
	}
}

// --- Formatting ---

func TestFormatWorkerResult(t *testing.T) {
	r := WorkerResult{
		WorkerID:    "worker-42",
		Description: "do stuff",
		Status:      WorkerStatusCompleted,
		Result:      "all good",
		TotalTokens: 500,
		ToolUses:    7,
		DurationMs:  1234,
	}
	out := FormatWorkerResult(r)
	wantStrs := []string{
		"<task-notification>",
		"<task-id>worker-42</task-id>",
		"<status>completed</status>",
		"all good",
		"<total_tokens>500</total_tokens>",
		"<tool_uses>7</tool_uses>",
		"<duration_ms>1234</duration_ms>",
		"</task-notification>",
	}
	for _, s := range wantStrs {
		if !strings.Contains(out, s) {
			t.Errorf("FormatWorkerResult output missing %q; full output:\n%s", s, out)
		}
	}

	// Failed result should mention the error.
	rf := WorkerResult{
		WorkerID:    "worker-1",
		Description: "bad",
		Status:      WorkerStatusFailed,
		Error:       "kaboom",
	}
	outF := FormatWorkerResult(rf)
	if !strings.Contains(outF, "kaboom") {
		t.Errorf("failed result should mention error; got:\n%s", outF)
	}
}

// --- User context ---

func TestGetCoordinatorUserContext(t *testing.T) {
	withEnv(t, "POLYPOD_COORDINATOR_MODE", "")
	if ctx := GetCoordinatorUserContext([]string{"read"}, nil, ""); ctx != nil {
		t.Errorf("expected nil context when not in coordinator mode, got %v", ctx)
	}

	withEnv(t, "POLYPOD_COORDINATOR_MODE", "1")
	tools := []string{"read", "write", "send_message", "team_create", "bash"}
	ctx := GetCoordinatorUserContext(tools, []string{"mcp1"}, "/tmp/sp")
	if ctx == nil {
		t.Fatal("expected non-nil context in coordinator mode")
	}
	text, ok := ctx["workerToolsContext"]
	if !ok {
		t.Fatal("missing workerToolsContext key")
	}
	// Internal tools should be filtered out.
	if strings.Contains(text, "send_message") {
		t.Error("send_message should be filtered from visible tools")
	}
	if strings.Contains(text, "team_create") {
		t.Error("team_create should be filtered from visible tools")
	}
	// Regular worker tools should appear.
	if !strings.Contains(text, "read") || !strings.Contains(text, "write") || !strings.Contains(text, "bash") {
		t.Errorf("expected normal tools listed; got:\n%s", text)
	}
	if !strings.Contains(text, "mcp1") {
		t.Error("expected MCP server to be mentioned")
	}
	if !strings.Contains(text, "/tmp/sp") {
		t.Error("expected scratchpad dir to be mentioned")
	}
}
