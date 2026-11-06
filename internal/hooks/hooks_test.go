package hooks

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/costa/polypod/internal/types"
)

// testLogger returns a logger that discards output to avoid noisy test runs.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --- Manager lifecycle ---

func TestNewManager(t *testing.T) {
	m := NewManager(testLogger())
	if m == nil {
		t.Fatal("NewManager returned nil")
	}
	if m.SessionRegistry == nil {
		t.Error("SessionRegistry not initialized")
	}
	if m.AsyncRegistry == nil {
		t.Error("AsyncRegistry not initialized")
	}
	if m.EventEmitter == nil {
		t.Error("EventEmitter not initialized")
	}
	if m.ConfigSnapshot == nil {
		t.Error("ConfigSnapshot not initialized")
	}
	if m.PostSamplingHooks == nil {
		t.Error("PostSamplingHooks not initialized")
	}
	if m.StopHooks == nil {
		t.Error("StopHooks not initialized")
	}
	if m.ToolUseSummaryHooks == nil {
		t.Error("ToolUseSummaryHooks not initialized")
	}
	if m.MessageTransforms == nil {
		t.Error("MessageTransforms not initialized")
	}
	if m.ResponseFilters == nil {
		t.Error("ResponseFilters not initialized")
	}
	if len(m.List()) != 0 {
		t.Errorf("new manager should have no hooks; got %d", len(m.List()))
	}
}

func TestManager_RegisterAndList(t *testing.T) {
	m := NewManager(testLogger())
	m.Register(Hook{
		Name:    "hook1",
		Event:   EventPreToolUse,
		Type:    HandlerShell,
		Command: "echo ok",
	})
	m.Register(Hook{
		Name:    "hook2",
		Event:   EventPostToolUse,
		Type:    HandlerShell,
		Command: "echo done",
	})

	hooks := m.List()
	if len(hooks) != 2 {
		t.Fatalf("List returned %d hooks; want 2", len(hooks))
	}
	if hooks[0].Name != "hook1" || hooks[1].Name != "hook2" {
		t.Errorf("unexpected hook order: %v", hooks)
	}
	// Default timeout should be applied
	if hooks[0].Timeout != 10 {
		t.Errorf("default timeout = %d; want 10", hooks[0].Timeout)
	}
	// Hook should be auto-enabled
	if !hooks[0].Enabled {
		t.Error("registered hook should be enabled by default")
	}
}

func TestManager_ListIsCopy(t *testing.T) {
	m := NewManager(testLogger())
	m.Register(Hook{Name: "h1", Event: EventPreToolUse, Type: HandlerShell, Command: "echo"})
	list := m.List()
	list[0].Name = "mutated"
	if m.List()[0].Name != "h1" {
		t.Error("List should return a defensive copy")
	}
}

func TestManager_Remove(t *testing.T) {
	m := NewManager(testLogger())
	m.Register(Hook{Name: "a", Event: EventPreToolUse, Type: HandlerShell, Command: "echo"})
	m.Register(Hook{Name: "b", Event: EventPreToolUse, Type: HandlerShell, Command: "echo"})

	if !m.Remove("a") {
		t.Error("Remove returned false for existing hook")
	}
	if len(m.List()) != 1 {
		t.Errorf("after remove, List has %d hooks; want 1", len(m.List()))
	}
	if m.List()[0].Name != "b" {
		t.Errorf("wrong hook remained: %s", m.List()[0].Name)
	}
	if m.Remove("nonexistent") {
		t.Error("Remove returned true for nonexistent hook")
	}
}

func TestManager_ClearAll(t *testing.T) {
	m := NewManager(testLogger())
	m.Register(Hook{Name: "a", Event: EventPreToolUse, Type: HandlerShell, Command: "echo"})
	m.Register(Hook{Name: "b", Event: EventPostToolUse, Type: HandlerShell, Command: "echo"})

	m.ClearAll()

	if len(m.List()) != 0 {
		t.Errorf("after ClearAll, List has %d hooks; want 0", len(m.List()))
	}
}

func TestManager_LoadHooks(t *testing.T) {
	m := NewManager(testLogger())
	m.LoadHooks([]Hook{
		{Name: "x", Event: EventPreToolUse, Type: HandlerShell, Command: "echo"},
		{Name: "y", Event: EventPostToolUse, Type: HandlerShell, Command: "echo"},
		{Name: "z", Event: EventSessionStart, Type: HandlerShell, Command: "echo"},
	})
	if len(m.List()) != 3 {
		t.Errorf("LoadHooks: got %d; want 3", len(m.List()))
	}
}

// --- Firing events ---

func TestManager_FirePreToolUse_Allow(t *testing.T) {
	m := NewManager(testLogger())
	m.Register(Hook{
		Name:    "allower",
		Event:   EventPreToolUse,
		Type:    HandlerShell,
		Command: "echo",
	})

	result, err := m.Fire(context.Background(), LegacyPayload{
		Event:    EventPreToolUse,
		ToolName: "bash",
	})
	if err != nil {
		t.Fatalf("Fire error: %v", err)
	}
	if result == nil {
		t.Fatal("Fire returned nil result")
	}
	if result.Decision != DecisionAllow {
		t.Errorf("decision = %s; want %s", result.Decision, DecisionAllow)
	}
}

func TestManager_FirePreToolUse_Deny(t *testing.T) {
	m := NewManager(testLogger())
	// Shell hook outputs JSON with a deny decision
	m.Register(Hook{
		Name:    "denier",
		Event:   EventPreToolUse,
		Type:    HandlerShell,
		Command: `echo '{"decision":"deny","message":"blocked"}'`,
	})

	result, err := m.Fire(context.Background(), LegacyPayload{
		Event:    EventPreToolUse,
		ToolName: "rm",
	})
	if err != nil {
		t.Fatalf("Fire error: %v", err)
	}
	if result.Decision != DecisionDeny {
		t.Errorf("decision = %s; want %s", result.Decision, DecisionDeny)
	}
	if result.Message != "blocked" {
		t.Errorf("message = %q; want 'blocked'", result.Message)
	}
}

func TestManager_FirePreToolUse_Ask(t *testing.T) {
	m := NewManager(testLogger())
	m.Register(Hook{
		Name:    "asker",
		Event:   EventPreToolUse,
		Type:    HandlerShell,
		Command: `echo '{"decision":"ask","message":"confirm?"}'`,
	})

	result, err := m.Fire(context.Background(), LegacyPayload{
		Event:    EventPreToolUse,
		ToolName: "write",
	})
	if err != nil {
		t.Fatalf("Fire error: %v", err)
	}
	if result.Decision != DecisionAsk {
		t.Errorf("decision = %s; want %s", result.Decision, DecisionAsk)
	}
}

func TestManager_FirePostToolUse(t *testing.T) {
	m := NewManager(testLogger())
	m.Register(Hook{
		Name:    "post",
		Event:   EventPostToolUse,
		Type:    HandlerShell,
		Command: "echo done",
	})

	result, err := m.Fire(context.Background(), LegacyPayload{
		Event:    EventPostToolUse,
		ToolName: "bash",
	})
	if err != nil {
		t.Fatalf("Fire error: %v", err)
	}
	if result == nil {
		t.Fatal("Fire returned nil result")
	}
	// PostToolUse should always return allow
	if result.Decision != DecisionAllow {
		t.Errorf("decision = %s; want %s", result.Decision, DecisionAllow)
	}
}

func TestManager_FireSessionStart(t *testing.T) {
	m := NewManager(testLogger())
	m.Register(Hook{
		Name:    "session",
		Event:   EventSessionStart,
		Type:    HandlerShell,
		Command: "echo started",
	})

	result, err := m.Fire(context.Background(), LegacyPayload{
		Event: EventSessionStart,
	})
	if err != nil {
		t.Fatalf("Fire error: %v", err)
	}
	if result == nil {
		t.Fatal("Fire returned nil result")
	}
}

func TestManager_FireNoHandlers(t *testing.T) {
	m := NewManager(testLogger())
	// No hooks registered at all
	result, err := m.Fire(context.Background(), LegacyPayload{
		Event:    EventPreToolUse,
		ToolName: "bash",
	})
	if err != nil {
		t.Fatalf("Fire error with no handlers: %v", err)
	}
	if result == nil || result.Decision != DecisionAllow {
		t.Errorf("no-handler Fire should default to allow; got %+v", result)
	}
}

func TestManager_FireEventMismatch(t *testing.T) {
	m := NewManager(testLogger())
	// Register a PostToolUse hook but fire PreToolUse
	m.Register(Hook{
		Name:    "post-only",
		Event:   EventPostToolUse,
		Type:    HandlerShell,
		Command: `echo '{"decision":"deny"}'`,
	})
	result, err := m.Fire(context.Background(), LegacyPayload{
		Event:    EventPreToolUse,
		ToolName: "bash",
	})
	if err != nil {
		t.Fatalf("Fire error: %v", err)
	}
	if result.Decision != DecisionAllow {
		t.Errorf("mismatched event should not invoke hook; got decision %s", result.Decision)
	}
}

func TestManager_MultipleHandlersInOrder(t *testing.T) {
	m := NewManager(testLogger())
	// Two allow hooks + a deny hook, PreToolUse => first deny wins
	m.Register(Hook{
		Name:    "first-allow",
		Event:   EventPreToolUse,
		Type:    HandlerShell,
		Command: `echo '{"decision":"allow","message":"first"}'`,
	})
	m.Register(Hook{
		Name:    "second-deny",
		Event:   EventPreToolUse,
		Type:    HandlerShell,
		Command: `echo '{"decision":"deny","message":"second"}'`,
	})
	m.Register(Hook{
		Name:    "third-never",
		Event:   EventPreToolUse,
		Type:    HandlerShell,
		Command: `echo '{"decision":"deny","message":"third"}'`,
	})

	result, err := m.Fire(context.Background(), LegacyPayload{
		Event:    EventPreToolUse,
		ToolName: "bash",
	})
	if err != nil {
		t.Fatalf("Fire error: %v", err)
	}
	if result.Decision != DecisionDeny {
		t.Errorf("decision = %s; want deny", result.Decision)
	}
	if result.Message != "second" {
		t.Errorf("first deny should win; got message = %q", result.Message)
	}
}

func TestManager_HandlerErrorDoesNotStopOthers(t *testing.T) {
	m := NewManager(testLogger())
	// First hook errors (nonexistent command), second should still run
	m.Register(Hook{
		Name:    "broken",
		Event:   EventPostToolUse,
		Type:    HandlerShell,
		Command: "exit 1",
	})
	m.Register(Hook{
		Name:    "ok",
		Event:   EventPostToolUse,
		Type:    HandlerShell,
		Command: "echo ok",
	})

	result, err := m.Fire(context.Background(), LegacyPayload{
		Event:    EventPostToolUse,
		ToolName: "bash",
	})
	if err != nil {
		t.Fatalf("Fire should swallow hook errors; got: %v", err)
	}
	if result == nil {
		t.Fatal("Fire returned nil result")
	}
}

func TestManager_UnknownHandlerType(t *testing.T) {
	m := NewManager(testLogger())
	m.Register(Hook{
		Name:    "bogus",
		Event:   EventPostToolUse,
		Type:    HandlerType("bogus"),
		Command: "noop",
	})
	result, err := m.Fire(context.Background(), LegacyPayload{
		Event: EventPostToolUse,
	})
	if err != nil {
		t.Fatalf("Fire error: %v", err)
	}
	if result == nil || result.Decision != DecisionAllow {
		t.Errorf("unknown handler should be skipped, default allow; got %+v", result)
	}
}

// --- Enable/Disable ---

func TestManager_SetEnabledDisablesHook(t *testing.T) {
	m := NewManager(testLogger())
	m.Register(Hook{
		Name:    "dis",
		Event:   EventPreToolUse,
		Type:    HandlerShell,
		Command: `echo '{"decision":"deny","message":"blocked"}'`,
	})

	// Disable it
	if !m.SetEnabled("dis", false) {
		t.Fatal("SetEnabled returned false")
	}
	result, err := m.Fire(context.Background(), LegacyPayload{
		Event:    EventPreToolUse,
		ToolName: "bash",
	})
	if err != nil {
		t.Fatalf("Fire error: %v", err)
	}
	if result.Decision != DecisionAllow {
		t.Errorf("disabled hook fired; decision = %s", result.Decision)
	}

	// Re-enable it
	if !m.SetEnabled("dis", true) {
		t.Fatal("SetEnabled(true) returned false")
	}
	result, err = m.Fire(context.Background(), LegacyPayload{
		Event:    EventPreToolUse,
		ToolName: "bash",
	})
	if err != nil {
		t.Fatalf("Fire error: %v", err)
	}
	if result.Decision != DecisionDeny {
		t.Errorf("re-enabled hook didn't fire; decision = %s", result.Decision)
	}

	// SetEnabled on nonexistent hook returns false
	if m.SetEnabled("nonexistent", true) {
		t.Error("SetEnabled on nonexistent hook returned true")
	}
}

// --- Hook matching ---

func TestManager_MatcherExactToolName(t *testing.T) {
	m := NewManager(testLogger())
	m.Register(Hook{
		Name:    "bash-only",
		Event:   EventPreToolUse,
		Type:    HandlerShell,
		Matcher: "bash",
		Command: `echo '{"decision":"deny","message":"no bash"}'`,
	})

	// Firing for "bash" should deny
	result, _ := m.Fire(context.Background(), LegacyPayload{
		Event:    EventPreToolUse,
		ToolName: "bash",
	})
	if result.Decision != DecisionDeny {
		t.Errorf("matcher='bash' fired for 'bash' should deny; got %s", result.Decision)
	}

	// Firing for "write" should allow (no match)
	result, _ = m.Fire(context.Background(), LegacyPayload{
		Event:    EventPreToolUse,
		ToolName: "write",
	})
	if result.Decision != DecisionAllow {
		t.Errorf("matcher='bash' for 'write' should allow; got %s", result.Decision)
	}
}

func TestManager_MatcherWildcard(t *testing.T) {
	m := NewManager(testLogger())
	m.Register(Hook{
		Name:    "all-tools",
		Event:   EventPreToolUse,
		Type:    HandlerShell,
		Matcher: "*",
		Command: `echo '{"decision":"deny","message":"any"}'`,
	})

	for _, tool := range []string{"bash", "write", "read", "anything"} {
		result, _ := m.Fire(context.Background(), LegacyPayload{
			Event:    EventPreToolUse,
			ToolName: tool,
		})
		if result.Decision != DecisionDeny {
			t.Errorf("wildcard should match %s; got %s", tool, result.Decision)
		}
	}
}

func TestManager_MatcherPrefixGlob(t *testing.T) {
	m := NewManager(testLogger())
	m.Register(Hook{
		Name:    "git-tools",
		Event:   EventPreToolUse,
		Type:    HandlerShell,
		Matcher: "git_*",
		Command: `echo '{"decision":"deny"}'`,
	})

	// git_commit should match
	result, _ := m.Fire(context.Background(), LegacyPayload{
		Event:    EventPreToolUse,
		ToolName: "git_commit",
	})
	if result.Decision != DecisionDeny {
		t.Errorf("git_* should match git_commit; got %s", result.Decision)
	}

	// bash should not match
	result, _ = m.Fire(context.Background(), LegacyPayload{
		Event:    EventPreToolUse,
		ToolName: "bash",
	})
	if result.Decision != DecisionAllow {
		t.Errorf("git_* should not match bash; got %s", result.Decision)
	}
}

// --- matchTool unit tests ---

func TestMatchTool(t *testing.T) {
	cases := []struct {
		pattern  string
		toolName string
		want     bool
	}{
		{"*", "anything", true},
		{"*", "", true},
		{"bash", "bash", true},
		{"bash", "write", false},
		{"git_*", "git_commit", true},
		{"git_*", "git_push", true},
		{"git_*", "bash", false},
		{"git_*", "git_", true}, // prefix empty match
	}
	for _, c := range cases {
		got := matchTool(c.pattern, c.toolName)
		if got != c.want {
			t.Errorf("matchTool(%q, %q) = %v; want %v", c.pattern, c.toolName, got, c.want)
		}
	}
}

// --- Thread safety ---

func TestManager_ConcurrentRegister(t *testing.T) {
	m := NewManager(testLogger())
	var wg sync.WaitGroup
	const n = 50
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m.Register(Hook{
				Name:    "h",
				Event:   EventPreToolUse,
				Type:    HandlerShell,
				Command: "echo",
			})
		}(i)
	}
	wg.Wait()
	if len(m.List()) != n {
		t.Errorf("concurrent register: got %d hooks; want %d", len(m.List()), n)
	}
}

func TestManager_ConcurrentFire(t *testing.T) {
	m := NewManager(testLogger())
	m.Register(Hook{
		Name:    "h",
		Event:   EventPostToolUse,
		Type:    HandlerShell,
		Command: "echo",
	})

	var wg sync.WaitGroup
	var errCount int32
	const n = 20
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.Fire(context.Background(), LegacyPayload{
				Event:    EventPostToolUse,
				ToolName: "bash",
			})
			if err != nil {
				atomic.AddInt32(&errCount, 1)
			}
		}()
	}
	wg.Wait()
	if atomic.LoadInt32(&errCount) != 0 {
		t.Errorf("concurrent Fire errors: %d", errCount)
	}
}

func TestManager_RegisterDuringFire(t *testing.T) {
	m := NewManager(testLogger())
	m.Register(Hook{
		Name:    "base",
		Event:   EventPostToolUse,
		Type:    HandlerShell,
		Command: "echo",
	})

	var wg sync.WaitGroup
	// Spawn fire loops
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_, _ = m.Fire(context.Background(), LegacyPayload{
					Event:    EventPostToolUse,
					ToolName: "bash",
				})
			}
		}()
	}
	// Register concurrently
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				m.Register(Hook{
					Name:    "concurrent",
					Event:   EventPostToolUse,
					Type:    HandlerShell,
					Command: "echo",
				})
			}
		}()
	}
	wg.Wait()
	// No panic / race means pass; List should have grown
	if len(m.List()) < 1 {
		t.Error("expected hooks to remain after concurrent ops")
	}
}

// --- HTTP handler ---

func TestManager_FireHTTPHandler(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"decision":"deny","message":"http said no"}`))
	}))
	defer srv.Close()

	m := NewManager(testLogger())
	m.Register(Hook{
		Name:  "http",
		Event: EventPreToolUse,
		Type:  HandlerHTTP,
		URL:   srv.URL,
	})

	result, err := m.Fire(context.Background(), LegacyPayload{
		Event:    EventPreToolUse,
		ToolName: "bash",
	})
	if err != nil {
		t.Fatalf("Fire error: %v", err)
	}
	if result.Decision != DecisionDeny {
		t.Errorf("http hook decision = %s; want deny", result.Decision)
	}
	if result.Message != "http said no" {
		t.Errorf("http hook message = %q", result.Message)
	}
}

// --- CheckToolPermission ---

func TestManager_CheckToolPermission_Allow(t *testing.T) {
	m := NewManager(testLogger())
	decision := m.CheckToolPermission(context.Background(), "bash", map[string]any{"cmd": "ls"})
	// No hooks registered => default allow
	if string(decision.Behavior) != "allow" {
		t.Errorf("CheckToolPermission with no hooks = %s; want allow", decision.Behavior)
	}
}

func TestManager_CheckToolPermission_Deny(t *testing.T) {
	m := NewManager(testLogger())
	m.Register(Hook{
		Name:    "blocker",
		Event:   EventPreToolUse,
		Type:    HandlerShell,
		Command: `echo '{"decision":"deny","message":"no"}'`,
	})
	decision := m.CheckToolPermission(context.Background(), "bash", nil)
	if string(decision.Behavior) != "deny" {
		t.Errorf("CheckToolPermission after deny hook = %s; want deny", decision.Behavior)
	}
	if decision.DecisionReason == nil || decision.DecisionReason.Type != "hook" {
		t.Errorf("DecisionReason should be set to 'hook'; got %+v", decision.DecisionReason)
	}
}

func TestManager_CheckToolPermission_Ask(t *testing.T) {
	m := NewManager(testLogger())
	m.Register(Hook{
		Name:    "asker",
		Event:   EventPreToolUse,
		Type:    HandlerShell,
		Command: `echo '{"decision":"ask","message":"are you sure"}'`,
	})
	decision := m.CheckToolPermission(context.Background(), "bash", nil)
	if string(decision.Behavior) != "ask" {
		t.Errorf("CheckToolPermission ask = %s; want ask", decision.Behavior)
	}
}

// --- IsValidHookEvent ---

func TestIsValidHookEvent(t *testing.T) {
	if !IsValidHookEvent("PreToolUse") {
		t.Error("PreToolUse should be valid")
	}
	if !IsValidHookEvent("SessionStart") {
		t.Error("SessionStart should be valid")
	}
	if IsValidHookEvent("NotARealEvent") {
		t.Error("NotARealEvent should not be valid")
	}
	if IsValidHookEvent("") {
		t.Error("empty string should not be valid")
	}
}

// --- FireWithProgress ---

func TestManager_FireWithProgress(t *testing.T) {
	m := NewManager(testLogger())
	m.Register(Hook{
		Name:    "p1",
		Event:   EventPostToolUse,
		Type:    HandlerShell,
		Command: "echo one",
	})
	m.Register(Hook{
		Name:    "p2",
		Event:   EventPostToolUse,
		Type:    HandlerShell,
		Command: "echo two",
	})

	var progressCount int32
	result, err := m.FireWithProgress(context.Background(), LegacyPayload{
		Event:    EventPostToolUse,
		ToolName: "bash",
	}, func(p types.ToolProgress) {
		atomic.AddInt32(&progressCount, 1)
	})
	if err != nil {
		t.Fatalf("FireWithProgress error: %v", err)
	}
	if result == nil {
		t.Fatal("FireWithProgress returned nil result")
	}
	// At least one progress call should have been made (the final "completed" one)
	if atomic.LoadInt32(&progressCount) == 0 {
		t.Error("expected at least one progress callback")
	}
}
