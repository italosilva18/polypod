package mergedtools

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// ---------------------------------------------------------------------------
// Tool fixtures
// ---------------------------------------------------------------------------

func stubTool(name string, src Priority) Tool {
	return Tool{
		Name:        name,
		Description: name + " description (" + sourceLabel(src) + ")",
		Source:      src,
		Schema:      map[string]any{"type": "object"},
		Execute: func(input map[string]any) (any, error) {
			return name, nil
		},
	}
}

func sourceLabel(p Priority) string {
	switch p {
	case PriorityBuiltin:
		return "builtin"
	case PriorityPlugin:
		return "plugin"
	case PriorityMCP:
		return "mcp"
	}
	return "unknown"
}

func toolNames(tools []Tool) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}

func findTool(tools []Tool, name string) (Tool, bool) {
	for _, t := range tools {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

// ---------------------------------------------------------------------------
// MergeTools
// ---------------------------------------------------------------------------

func TestMergeTools_EmptyInputs(t *testing.T) {
	got := MergeTools()
	if len(got) != 0 {
		t.Errorf("expected empty result for no sources, got %d tools", len(got))
	}

	got = MergeTools(nil, nil)
	if len(got) != 0 {
		t.Errorf("expected empty result for nil sources, got %d tools", len(got))
	}

	got = MergeTools([]Tool{}, []Tool{})
	if len(got) != 0 {
		t.Errorf("expected empty result for empty slices, got %d tools", len(got))
	}
}

func TestMergeTools_CombinesDistinctNames(t *testing.T) {
	builtin := []Tool{stubTool("read", PriorityBuiltin), stubTool("write", PriorityBuiltin)}
	plugin := []Tool{stubTool("lint", PriorityPlugin)}
	mcp := []Tool{stubTool("search", PriorityMCP)}

	merged := MergeTools(builtin, plugin, mcp)

	if len(merged) != 4 {
		t.Fatalf("expected 4 tools, got %d: %v", len(merged), toolNames(merged))
	}

	// Result should be sorted by source, then name.
	expectedOrder := []string{"read", "write", "lint", "search"}
	for i, name := range expectedOrder {
		if merged[i].Name != name {
			t.Errorf("at index %d: expected %q, got %q (full order: %v)",
				i, name, merged[i].Name, toolNames(merged))
		}
	}
}

func TestMergeTools_ConflictResolution_BuiltinWins(t *testing.T) {
	builtin := []Tool{stubTool("bash", PriorityBuiltin)}
	plugin := []Tool{stubTool("bash", PriorityPlugin)}
	mcp := []Tool{stubTool("bash", PriorityMCP)}

	// Pass in reverse priority order to verify priority (not iteration order)
	// is the tiebreaker.
	merged := MergeTools(mcp, plugin, builtin)

	if len(merged) != 1 {
		t.Fatalf("expected 1 deduplicated tool, got %d", len(merged))
	}

	winner := merged[0]
	if winner.Source != PriorityBuiltin {
		t.Errorf("expected builtin (%d) to win, got source %d", PriorityBuiltin, winner.Source)
	}
	if winner.Description != "bash description (builtin)" {
		t.Errorf("expected builtin description, got %q", winner.Description)
	}
}

func TestMergeTools_ConflictResolution_PluginBeatsMCP(t *testing.T) {
	plugin := []Tool{stubTool("grep", PriorityPlugin)}
	mcp := []Tool{stubTool("grep", PriorityMCP)}

	merged := MergeTools(mcp, plugin)

	if len(merged) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(merged))
	}
	if merged[0].Source != PriorityPlugin {
		t.Errorf("expected plugin to win over mcp, got source %d", merged[0].Source)
	}
}

func TestMergeTools_SamePriorityFirstWins(t *testing.T) {
	first := Tool{Name: "dup", Description: "first", Source: PriorityPlugin}
	second := Tool{Name: "dup", Description: "second", Source: PriorityPlugin}

	// Within the same priority, MergeTools keeps the first encountered
	// (the condition is strict: t.Source < existing.Source).
	merged := MergeTools([]Tool{first}, []Tool{second})

	if len(merged) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(merged))
	}
	if merged[0].Description != "first" {
		t.Errorf("expected first occurrence to win, got %q", merged[0].Description)
	}
}

func TestMergeTools_SortedBySourceThenName(t *testing.T) {
	merged := MergeTools(
		[]Tool{stubTool("zebra", PriorityMCP), stubTool("alpha", PriorityMCP)},
		[]Tool{stubTool("yankee", PriorityPlugin), stubTool("bravo", PriorityPlugin)},
		[]Tool{stubTool("xray", PriorityBuiltin), stubTool("charlie", PriorityBuiltin)},
	)

	expected := []string{"charlie", "xray", "bravo", "yankee", "alpha", "zebra"}
	got := toolNames(merged)

	if len(got) != len(expected) {
		t.Fatalf("expected %d tools, got %d: %v", len(expected), len(got), got)
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Errorf("order mismatch at %d: expected %q, got %q (full: %v)",
				i, expected[i], got[i], got)
		}
	}
}

func TestMergeTools_ExecutePreservedAfterMerge(t *testing.T) {
	builtin := []Tool{{
		Name:   "echo",
		Source: PriorityBuiltin,
		Execute: func(input map[string]any) (any, error) {
			return input["msg"], nil
		},
	}}

	merged := MergeTools(builtin)
	if len(merged) != 1 || merged[0].Execute == nil {
		t.Fatal("expected single tool with Execute preserved")
	}

	result, err := merged[0].Execute(map[string]any{"msg": "hello"})
	if err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
	if result != "hello" {
		t.Errorf("expected 'hello', got %v", result)
	}
}

// ---------------------------------------------------------------------------
// DeduplicateToolsByName
// ---------------------------------------------------------------------------

func TestDeduplicateToolsByName(t *testing.T) {
	tools := []Tool{
		{Name: "a", Description: "first-a"},
		{Name: "b", Description: "first-b"},
		{Name: "a", Description: "second-a"},
		{Name: "c", Description: "first-c"},
		{Name: "b", Description: "second-b"},
	}

	out := DeduplicateToolsByName(tools)

	if len(out) != 3 {
		t.Fatalf("expected 3 unique tools, got %d", len(out))
	}

	// Order preserved (first occurrence wins).
	expected := []string{"a", "b", "c"}
	for i, name := range expected {
		if out[i].Name != name {
			t.Errorf("at %d: expected %q, got %q", i, name, out[i].Name)
		}
	}

	// First descriptions kept.
	if tool, _ := findTool(out, "a"); tool.Description != "first-a" {
		t.Errorf("expected first-a, got %q", tool.Description)
	}
	if tool, _ := findTool(out, "b"); tool.Description != "first-b" {
		t.Errorf("expected first-b, got %q", tool.Description)
	}
}

func TestDeduplicateToolsByName_Empty(t *testing.T) {
	if out := DeduplicateToolsByName(nil); len(out) != 0 {
		t.Errorf("expected empty, got %d", len(out))
	}
	if out := DeduplicateToolsByName([]Tool{}); len(out) != 0 {
		t.Errorf("expected empty, got %d", len(out))
	}
}

// ---------------------------------------------------------------------------
// FilterByMode
// ---------------------------------------------------------------------------

func TestFilterByMode_EmptyDenySet(t *testing.T) {
	tools := []Tool{stubTool("read", PriorityBuiltin), stubTool("write", PriorityBuiltin)}

	out := FilterByMode(tools, nil)
	if len(out) != 2 {
		t.Errorf("expected 2 tools when denySet is nil, got %d", len(out))
	}

	out = FilterByMode(tools, map[string]bool{})
	if len(out) != 2 {
		t.Errorf("expected 2 tools when denySet is empty, got %d", len(out))
	}
}

func TestFilterByMode_RemovesDeniedTools(t *testing.T) {
	tools := []Tool{
		stubTool("read", PriorityBuiltin),
		stubTool("write", PriorityBuiltin),
		stubTool("bash", PriorityBuiltin),
		stubTool("edit", PriorityBuiltin),
	}

	denySet := map[string]bool{"write": true, "bash": true}
	out := FilterByMode(tools, denySet)

	if len(out) != 2 {
		t.Fatalf("expected 2 tools remaining, got %d: %v", len(out), toolNames(out))
	}

	for _, tool := range out {
		if denySet[tool.Name] {
			t.Errorf("denied tool %q was not filtered out", tool.Name)
		}
	}

	if _, ok := findTool(out, "read"); !ok {
		t.Error("expected 'read' to remain")
	}
	if _, ok := findTool(out, "edit"); !ok {
		t.Error("expected 'edit' to remain")
	}
}

func TestFilterByMode_AllDenied(t *testing.T) {
	tools := []Tool{stubTool("a", PriorityBuiltin), stubTool("b", PriorityBuiltin)}
	out := FilterByMode(tools, map[string]bool{"a": true, "b": true})
	if len(out) != 0 {
		t.Errorf("expected all tools filtered out, got %d", len(out))
	}
}

// ---------------------------------------------------------------------------
// MergeCommands
// ---------------------------------------------------------------------------

func TestMergeCommands_DeduplicatesByPriority(t *testing.T) {
	builtin := []Command{
		{Name: "help", Description: "builtin help", Source: PriorityBuiltin},
		{Name: "quit", Description: "builtin quit", Source: PriorityBuiltin},
	}
	plugin := []Command{
		{Name: "help", Description: "plugin help", Source: PriorityPlugin},
		{Name: "deploy", Description: "plugin deploy", Source: PriorityPlugin},
	}

	merged := MergeCommands(plugin, builtin)

	if len(merged) != 3 {
		t.Fatalf("expected 3 commands, got %d", len(merged))
	}

	// Commands sorted by name.
	expectedNames := []string{"deploy", "help", "quit"}
	for i, name := range expectedNames {
		if merged[i].Name != name {
			t.Errorf("at %d: expected %q, got %q", i, name, merged[i].Name)
		}
	}

	// Builtin 'help' wins over plugin 'help'.
	for _, c := range merged {
		if c.Name == "help" && c.Description != "builtin help" {
			t.Errorf("expected builtin help to win, got %q", c.Description)
		}
	}
}

func TestDeduplicateCommandsByName(t *testing.T) {
	cmds := []Command{
		{Name: "x", Description: "first"},
		{Name: "y", Description: "first"},
		{Name: "x", Description: "second"},
	}
	out := DeduplicateCommandsByName(cmds)
	if len(out) != 2 {
		t.Fatalf("expected 2, got %d", len(out))
	}
	if out[0].Description != "first" {
		t.Errorf("expected first occurrence to win, got %q", out[0].Description)
	}
}

// ---------------------------------------------------------------------------
// MergeClients
// ---------------------------------------------------------------------------

func TestMergeClients_NoDynamicReturnsInitial(t *testing.T) {
	initial := []MCPClient{{Name: "a", Status: "connected"}}
	out := MergeClients(initial, nil)
	if len(out) != 1 || out[0].Name != "a" {
		t.Errorf("expected initial unchanged, got %v", out)
	}
}

func TestMergeClients_DeduplicatesByName(t *testing.T) {
	initial := []MCPClient{
		{Name: "fs", Status: "connected"},
		{Name: "github", Status: "connected"},
	}
	dynamic := []MCPClient{
		{Name: "fs", Status: "error"}, // duplicate -- should be ignored
		{Name: "slack", Status: "connected"},
	}

	out := MergeClients(initial, dynamic)

	if len(out) != 3 {
		t.Fatalf("expected 3 clients, got %d", len(out))
	}

	// Order: initial first, then dynamic. First occurrence wins.
	expected := []string{"fs", "github", "slack"}
	for i, name := range expected {
		if out[i].Name != name {
			t.Errorf("at %d: expected %q, got %q", i, name, out[i].Name)
		}
	}

	// 'fs' should retain 'connected' status from initial.
	if out[0].Status != "connected" {
		t.Errorf("expected 'fs' to keep connected status, got %q", out[0].Status)
	}
}

// ---------------------------------------------------------------------------
// CommandQueue
// ---------------------------------------------------------------------------

func TestCommandQueue_EnqueueDequeueByPriority(t *testing.T) {
	q := NewCommandQueue()

	q.Enqueue(QueuedCommand{Text: "later", Priority: PriorityLater})
	q.Enqueue(QueuedCommand{Text: "now", Priority: PriorityNow})
	q.Enqueue(QueuedCommand{Text: "next", Priority: PriorityNext})

	if q.Len() != 3 {
		t.Fatalf("expected length 3, got %d", q.Len())
	}

	// Dequeue returns highest priority (PriorityNow == 0) first.
	got, ok := q.Dequeue()
	if !ok || got.Text != "now" {
		t.Errorf("expected 'now', got %+v (ok=%v)", got, ok)
	}

	got, ok = q.Dequeue()
	if !ok || got.Text != "next" {
		t.Errorf("expected 'next', got %+v (ok=%v)", got, ok)
	}

	got, ok = q.Dequeue()
	if !ok || got.Text != "later" {
		t.Errorf("expected 'later', got %+v (ok=%v)", got, ok)
	}

	if _, ok := q.Dequeue(); ok {
		t.Error("expected empty queue to return false")
	}
}

func TestCommandQueue_DequeueAllSortedByPriority(t *testing.T) {
	q := NewCommandQueue()
	q.Enqueue(QueuedCommand{Text: "a", Priority: PriorityLater})
	q.Enqueue(QueuedCommand{Text: "b", Priority: PriorityNow})
	q.Enqueue(QueuedCommand{Text: "c", Priority: PriorityNext})
	q.Enqueue(QueuedCommand{Text: "d", Priority: PriorityNow})

	all := q.DequeueAll()

	if len(all) != 4 {
		t.Fatalf("expected 4 items, got %d", len(all))
	}
	if q.Len() != 0 {
		t.Errorf("expected queue to be empty, got %d", q.Len())
	}

	// Priorities in non-decreasing order.
	for i := 1; i < len(all); i++ {
		if all[i].Priority < all[i-1].Priority {
			t.Errorf("not sorted: index %d priority %d < prior %d",
				i, all[i].Priority, all[i-1].Priority)
		}
	}

	// Stable sort: b comes before d within PriorityNow.
	if all[0].Text != "b" || all[1].Text != "d" {
		t.Errorf("expected stable order b,d within PriorityNow, got %q,%q",
			all[0].Text, all[1].Text)
	}
}

func TestCommandQueue_Snapshot(t *testing.T) {
	q := NewCommandQueue()
	q.Enqueue(QueuedCommand{Text: "x", Priority: PriorityNext})
	q.Enqueue(QueuedCommand{Text: "y", Priority: PriorityLater})

	snap := q.Snapshot()

	if len(snap) != 2 {
		t.Fatalf("expected 2 items, got %d", len(snap))
	}
	if q.Len() != 2 {
		t.Error("snapshot must not drain queue")
	}

	// Snapshot is a copy.
	snap[0].Text = "mutated"
	if got := q.Snapshot(); got[0].Text == "mutated" {
		t.Error("snapshot should be a copy, not a reference")
	}
}

func TestCommandQueue_Subscribe(t *testing.T) {
	q := NewCommandQueue()
	var count int32
	q.Subscribe(func() { atomic.AddInt32(&count, 1) })

	q.Enqueue(QueuedCommand{Text: "a", Priority: PriorityNow})
	q.Enqueue(QueuedCommand{Text: "b", Priority: PriorityNext})
	q.Enqueue(QueuedCommand{Text: "c", Priority: PriorityLater})

	if got := atomic.LoadInt32(&count); got != 3 {
		t.Errorf("expected 3 subscriber notifications, got %d", got)
	}
}

func TestCommandQueue_ConcurrentAccess(t *testing.T) {
	q := NewCommandQueue()
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			q.Enqueue(QueuedCommand{Text: "x", Priority: PriorityNext})
		}(i)
	}
	wg.Wait()

	if q.Len() != 10 {
		t.Errorf("expected 10 items after concurrent enqueue, got %d", q.Len())
	}
}

// ---------------------------------------------------------------------------
// QueueProcessor
// ---------------------------------------------------------------------------

func TestQueueProcessor_SkipsWhenQueryActive(t *testing.T) {
	q := NewCommandQueue()
	q.Enqueue(QueuedCommand{Text: "hi", Priority: PriorityNext})

	executed := false
	p := &QueueProcessor{
		Queue:         q,
		IsQueryActive: func() bool { return true },
		HasActiveUI:   func() bool { return false },
		ExecuteInput: func(cmds []QueuedCommand) error {
			executed = true
			return nil
		},
	}

	if err := p.ProcessIfReady(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if executed {
		t.Error("should not execute when query is active")
	}
	if q.Len() != 1 {
		t.Error("queue should not be drained when query is active")
	}
}

func TestQueueProcessor_SkipsWhenUIActive(t *testing.T) {
	q := NewCommandQueue()
	q.Enqueue(QueuedCommand{Text: "hi", Priority: PriorityNext})

	executed := false
	p := &QueueProcessor{
		Queue:         q,
		IsQueryActive: func() bool { return false },
		HasActiveUI:   func() bool { return true },
		ExecuteInput: func(cmds []QueuedCommand) error {
			executed = true
			return nil
		},
	}

	if err := p.ProcessIfReady(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if executed {
		t.Error("should not execute when UI is active")
	}
}

func TestQueueProcessor_ExecutesWhenReady(t *testing.T) {
	q := NewCommandQueue()
	q.Enqueue(QueuedCommand{Text: "first", Priority: PriorityNext})
	q.Enqueue(QueuedCommand{Text: "urgent", Priority: PriorityNow})

	var executed []QueuedCommand
	p := &QueueProcessor{
		Queue:         q,
		IsQueryActive: func() bool { return false },
		HasActiveUI:   func() bool { return false },
		ExecuteInput: func(cmds []QueuedCommand) error {
			executed = cmds
			return nil
		},
	}

	if err := p.ProcessIfReady(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(executed) != 2 {
		t.Fatalf("expected 2 commands executed, got %d", len(executed))
	}
	if executed[0].Text != "urgent" {
		t.Errorf("expected priority order (urgent first), got %q", executed[0].Text)
	}
	if q.Len() != 0 {
		t.Errorf("expected queue drained, got %d items", q.Len())
	}
}

func TestQueueProcessor_NoOpWhenEmpty(t *testing.T) {
	q := NewCommandQueue()
	called := false
	p := &QueueProcessor{
		Queue:         q,
		IsQueryActive: func() bool { return false },
		HasActiveUI:   func() bool { return false },
		ExecuteInput: func(cmds []QueuedCommand) error {
			called = true
			return nil
		},
	}

	if err := p.ProcessIfReady(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("ExecuteInput should not be called for empty queue")
	}
}

func TestQueueProcessor_PropagatesExecuteError(t *testing.T) {
	q := NewCommandQueue()
	q.Enqueue(QueuedCommand{Text: "boom", Priority: PriorityNow})

	wantErr := errors.New("execute failed")
	p := &QueueProcessor{
		Queue:         q,
		IsQueryActive: func() bool { return false },
		HasActiveUI:   func() bool { return false },
		ExecuteInput: func(cmds []QueuedCommand) error {
			return wantErr
		},
	}

	err := p.ProcessIfReady()
	if !errors.Is(err, wantErr) {
		t.Errorf("expected wrapped error %v, got %v", wantErr, err)
	}
}
