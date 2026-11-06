package queryctx

import (
	"strings"
	"testing"
)

func TestAsSystemPromptCopiesParts(t *testing.T) {
	parts := []string{"section one", "section two", "section three"}
	sp := AsSystemPrompt(parts)

	if len(sp) != 3 {
		t.Fatalf("expected 3 sections, got %d", len(sp))
	}
	if sp[0] != "section one" || sp[1] != "section two" || sp[2] != "section three" {
		t.Errorf("sections not copied correctly: %#v", sp)
	}

	// Mutating original must not affect the SystemPrompt (defensive copy).
	parts[0] = "mutated"
	if sp[0] == "mutated" {
		t.Errorf("AsSystemPrompt did not copy the underlying slice")
	}
}

func TestSystemPromptJoinAndIsEmpty(t *testing.T) {
	sp := AsSystemPrompt([]string{"alpha", "beta", "gamma"})
	got := sp.Join("\n\n")
	want := "alpha\n\nbeta\n\ngamma"
	if got != want {
		t.Errorf("Join mismatch: got %q, want %q", got, want)
	}
	if sp.IsEmpty() {
		t.Errorf("SystemPrompt with content should not be empty")
	}

	empty := AsSystemPrompt(nil)
	if !empty.IsEmpty() {
		t.Errorf("nil SystemPrompt should be empty")
	}

	whitespaceOnly := AsSystemPrompt([]string{"  ", "\t\n", ""})
	if !whitespaceOnly.IsEmpty() {
		t.Errorf("whitespace-only SystemPrompt should be empty")
	}

	partial := AsSystemPrompt([]string{"  ", "real", ""})
	if partial.IsEmpty() {
		t.Errorf("SystemPrompt with some real content should not be empty")
	}
}

func TestNewQueryTrackingHasFreshChainID(t *testing.T) {
	qt1 := NewQueryTracking("repl_main_thread")
	qt2 := NewQueryTracking("repl_main_thread")

	if qt1.ChainID == "" || qt2.ChainID == "" {
		t.Fatalf("expected non-empty chain IDs")
	}
	if qt1.ChainID == qt2.ChainID {
		t.Errorf("chain IDs should be unique, got duplicate %q", qt1.ChainID)
	}
	if len(qt1.ChainID) != 32 {
		t.Errorf("expected 32-char hex chain ID, got %d chars: %q", len(qt1.ChainID), qt1.ChainID)
	}
	if qt1.Depth != 0 {
		t.Errorf("expected depth 0 for new tracking, got %d", qt1.Depth)
	}
	if qt1.Source != "repl_main_thread" {
		t.Errorf("expected source propagated, got %q", qt1.Source)
	}
	if qt1.ParentChainID != "" {
		t.Errorf("top-level tracking should have no parent, got %q", qt1.ParentChainID)
	}
}

func TestNewChildTrackingPropagatesParent(t *testing.T) {
	parent := NewQueryTracking("orchestrator")
	child := parent.NewChildTracking()

	if child.ParentChainID != parent.ChainID {
		t.Errorf("child ParentChainID = %q, want %q", child.ParentChainID, parent.ChainID)
	}
	if child.ChainID == parent.ChainID {
		t.Errorf("child ChainID should differ from parent")
	}
	if child.Depth != parent.Depth+1 {
		t.Errorf("child depth = %d, want %d", child.Depth, parent.Depth+1)
	}
	if child.Source != parent.Source {
		t.Errorf("child source = %q, want %q", child.Source, parent.Source)
	}

	// Grandchild should continue the chain.
	grandchild := child.NewChildTracking()
	if grandchild.ParentChainID != child.ChainID {
		t.Errorf("grandchild parent = %q, want %q", grandchild.ParentChainID, child.ChainID)
	}
	if grandchild.Depth != 2 {
		t.Errorf("grandchild depth = %d, want 2", grandchild.Depth)
	}
}

func TestQueryContextFieldAssignment(t *testing.T) {
	tracking := NewQueryTracking("test")
	qc := QueryContext{
		SystemPrompt:       AsSystemPrompt([]string{"you are a helpful assistant"}),
		UserContext:        map[string]string{"CLAUDE.md": "# Project guide"},
		SystemContext:      map[string]string{"git_status": "clean"},
		Tracking:           tracking,
		Model:              "claude-opus-4-6",
		MaxOutputTokens:    8192,
		CustomSystemPrompt: "custom override",
		AppendSystemPrompt: "appended bit",
		QuerySource:        "repl_main_thread",
	}

	if qc.SystemPrompt.Join(" ") != "you are a helpful assistant" {
		t.Errorf("SystemPrompt not stored correctly")
	}
	if qc.UserContext["CLAUDE.md"] != "# Project guide" {
		t.Errorf("UserContext not stored correctly")
	}
	if qc.SystemContext["git_status"] != "clean" {
		t.Errorf("SystemContext not stored correctly")
	}
	if qc.Tracking.ChainID != tracking.ChainID {
		t.Errorf("Tracking ChainID not stored: got %q, want %q", qc.Tracking.ChainID, tracking.ChainID)
	}
	if qc.Model != "claude-opus-4-6" {
		t.Errorf("Model = %q, want claude-opus-4-6", qc.Model)
	}
	if qc.MaxOutputTokens != 8192 {
		t.Errorf("MaxOutputTokens = %d, want 8192", qc.MaxOutputTokens)
	}
	if qc.CustomSystemPrompt != "custom override" {
		t.Errorf("CustomSystemPrompt not stored")
	}
	if qc.AppendSystemPrompt != "appended bit" {
		t.Errorf("AppendSystemPrompt not stored")
	}
	if qc.QuerySource != "repl_main_thread" {
		t.Errorf("QuerySource not stored")
	}
}

func TestSystemPromptBuilderDefaultOnly(t *testing.T) {
	b := &SystemPromptBuilder{
		DefaultParts: []string{"base part 1", "base part 2"},
	}
	result := b.Build()
	if len(result) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(result))
	}
	if result[0] != "base part 1" || result[1] != "base part 2" {
		t.Errorf("default parts not returned: %#v", result)
	}
}

func TestSystemPromptBuilderPriority(t *testing.T) {
	// Override wins over everything.
	b := &SystemPromptBuilder{
		DefaultParts:   []string{"default"},
		CustomPrompt:   "custom",
		AgentPrompt:    "agent",
		AppendPrompt:   "appended",
		OverridePrompt: "override wins",
	}
	result := b.Build()
	if len(result) != 1 || result[0] != "override wins" {
		t.Errorf("override should replace everything, got %#v", result)
	}

	// Agent wins over custom and default.
	b2 := &SystemPromptBuilder{
		DefaultParts: []string{"default"},
		CustomPrompt: "custom",
		AgentPrompt:  "agent instructions",
		AppendPrompt: "tail",
	}
	result2 := b2.Build()
	if len(result2) != 2 {
		t.Fatalf("expected 2 parts (agent + append), got %d: %#v", len(result2), result2)
	}
	if result2[0] != "agent instructions" {
		t.Errorf("agent prompt should take precedence, got %q", result2[0])
	}
	if result2[1] != "tail" {
		t.Errorf("append prompt should be last, got %q", result2[1])
	}

	// Custom wins over default when agent is absent.
	b3 := &SystemPromptBuilder{
		DefaultParts: []string{"default a", "default b"},
		CustomPrompt: "custom prompt",
	}
	result3 := b3.Build()
	if len(result3) != 1 || result3[0] != "custom prompt" {
		t.Errorf("custom should replace default, got %#v", result3)
	}

	// Default + append.
	b4 := &SystemPromptBuilder{
		DefaultParts: []string{"d1", "d2"},
		AppendPrompt: "app",
	}
	result4 := b4.Build()
	if len(result4) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(result4))
	}
	if result4[2] != "app" {
		t.Errorf("append should be last, got %q", result4[2])
	}
}

func TestSystemPromptBuilderDoesNotMutateDefaults(t *testing.T) {
	defaults := []string{"one", "two"}
	b := &SystemPromptBuilder{
		DefaultParts: defaults,
		AppendPrompt: "three",
	}
	result := b.Build()
	if len(result) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(result))
	}
	// DefaultParts slice should not have been mutated.
	if len(defaults) != 2 {
		t.Errorf("DefaultParts was mutated: %#v", defaults)
	}
}

func TestCacheSafeParamsFieldsStored(t *testing.T) {
	params := CacheSafeParams{
		SystemPrompt:  AsSystemPrompt([]string{"sp"}),
		UserContext:   map[string]string{"u": "1"},
		SystemContext: map[string]string{"s": "2"},
		Model:         "claude-opus-4-6",
	}
	if params.Model != "claude-opus-4-6" {
		t.Errorf("Model = %q", params.Model)
	}
	if params.UserContext["u"] != "1" {
		t.Errorf("UserContext not stored")
	}
	if params.SystemContext["s"] != "2" {
		t.Errorf("SystemContext not stored")
	}
	if params.SystemPrompt.Join("") != "sp" {
		t.Errorf("SystemPrompt not stored")
	}
}

func TestQueryHelpersIsResultSuccessful(t *testing.T) {
	var h QueryHelpers

	cases := []struct {
		name     string
		msgType  string
		hasText  bool
		stop     string
		expected bool
	}{
		{"assistant with text", "assistant", true, "", true},
		{"assistant without text", "assistant", false, "", false},
		{"user is always ok", "user", false, "", true},
		{"empty with end_turn", "", false, "end_turn", true},
		{"empty without end_turn", "", false, "max_tokens", false},
		{"system with end_turn", "system", false, "end_turn", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := h.IsResultSuccessful(tc.msgType, tc.hasText, tc.stop)
			if got != tc.expected {
				t.Errorf("IsResultSuccessful(%q, %v, %q) = %v, want %v",
					tc.msgType, tc.hasText, tc.stop, got, tc.expected)
			}
		})
	}
}

func TestQueryHelpersExtractCliName(t *testing.T) {
	var h QueryHelpers

	cases := []struct {
		in   string
		want string
	}{
		{"vercel deploy", "vercel"},
		{"FOO=bar vercel deploy", "vercel"},
		{"FOO=bar BAZ=qux vercel deploy", "vercel"},
		{"sudo apt install", "apt"},
		{"sudo FOO=1 npm install", "npm"},
		{"", ""},
		{"   ", ""},
		{"=notvalid cmd", "=notvalid"}, // =notvalid has empty prefix -> invalid env name -> not skipped
		{"1FOO=bar cmd", "1FOO=bar"},   // 1FOO invalid env name -> not skipped
	}
	for _, tc := range cases {
		got := h.ExtractCliName(tc.in)
		if got != tc.want {
			t.Errorf("ExtractCliName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNewTokenStatsAndIncrement(t *testing.T) {
	ts := NewTokenStats()
	if ts == nil {
		t.Fatal("NewTokenStats returned nil")
	}
	if ts.ToolRequests == nil || ts.ToolResults == nil ||
		ts.Attachments == nil || ts.DuplicateFileReads == nil {
		t.Error("NewTokenStats should initialize maps")
	}
	if ts.Total != 0 {
		t.Errorf("fresh TokenStats Total = %d, want 0", ts.Total)
	}

	ts.IncrementToolRequest("Read", 100)
	ts.IncrementToolRequest("Read", 50)
	ts.IncrementToolRequest("Bash", 25)
	ts.IncrementToolResult("Read", 200)

	if ts.ToolRequests["Read"] != 150 {
		t.Errorf("ToolRequests[Read] = %d, want 150", ts.ToolRequests["Read"])
	}
	if ts.ToolRequests["Bash"] != 25 {
		t.Errorf("ToolRequests[Bash] = %d, want 25", ts.ToolRequests["Bash"])
	}
	if ts.ToolResults["Read"] != 200 {
		t.Errorf("ToolResults[Read] = %d, want 200", ts.ToolResults["Read"])
	}
	if ts.Total != 375 {
		t.Errorf("Total = %d, want 375", ts.Total)
	}
}

func TestTokenStatsToMetrics(t *testing.T) {
	ts := NewTokenStats()
	ts.HumanMessages = 100
	ts.AssistantMessages = 300
	ts.LocalCommandOutputs = 50
	ts.Other = 50
	ts.Total = 500
	ts.IncrementToolRequest("Read", 0) // Total stays at 500 after zero-increment actually, it adds 0
	ts.Attachments["image"] = 2
	ts.DuplicateFileReads["/a.go"] = DuplicateRead{Count: 3, Tokens: 30}
	ts.DuplicateFileReads["/b.go"] = DuplicateRead{Count: 2, Tokens: 20}

	metrics := ts.ToMetrics()

	if metrics["total_tokens"] != 500 {
		t.Errorf("total_tokens = %d, want 500", metrics["total_tokens"])
	}
	if metrics["human_message_tokens"] != 100 {
		t.Errorf("human_message_tokens = %d, want 100", metrics["human_message_tokens"])
	}
	if metrics["assistant_message_tokens"] != 300 {
		t.Errorf("assistant_message_tokens = %d, want 300", metrics["assistant_message_tokens"])
	}
	if metrics["attachment_image_count"] != 2 {
		t.Errorf("attachment_image_count = %d, want 2", metrics["attachment_image_count"])
	}
	if metrics["duplicate_read_tokens"] != 50 {
		t.Errorf("duplicate_read_tokens = %d, want 50", metrics["duplicate_read_tokens"])
	}
	if metrics["duplicate_read_file_count"] != 2 {
		t.Errorf("duplicate_read_file_count = %d, want 2", metrics["duplicate_read_file_count"])
	}
	// Percentages: 100/500 = 20, 300/500 = 60, 50/500 = 10
	if metrics["human_message_percent"] != 20 {
		t.Errorf("human_message_percent = %d, want 20", metrics["human_message_percent"])
	}
	if metrics["assistant_message_percent"] != 60 {
		t.Errorf("assistant_message_percent = %d, want 60", metrics["assistant_message_percent"])
	}
	if metrics["duplicate_read_percent"] != 10 {
		t.Errorf("duplicate_read_percent = %d, want 10", metrics["duplicate_read_percent"])
	}

	// Check tool request key was emitted.
	foundToolKey := false
	for k := range metrics {
		if strings.HasPrefix(k, "tool_request_Read") {
			foundToolKey = true
			break
		}
	}
	if !foundToolKey {
		t.Errorf("expected tool_request_Read_tokens key in metrics, got keys: %v", metrics)
	}
}

func TestTokenStatsToMetricsZeroTotal(t *testing.T) {
	// When Total is 0, percentage keys should not be emitted (avoids div-by-zero).
	ts := NewTokenStats()
	metrics := ts.ToMetrics()
	if _, ok := metrics["human_message_percent"]; ok {
		t.Errorf("human_message_percent should not be set when total is 0")
	}
	if _, ok := metrics["assistant_message_percent"]; ok {
		t.Errorf("assistant_message_percent should not be set when total is 0")
	}
	if metrics["total_tokens"] != 0 {
		t.Errorf("total_tokens = %d, want 0", metrics["total_tokens"])
	}
	// Non-percent keys should still exist.
	if _, ok := metrics["duplicate_read_tokens"]; !ok {
		t.Errorf("duplicate_read_tokens should be present")
	}
}
