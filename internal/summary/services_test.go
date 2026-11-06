package summary

import (
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// AgentSummaryService
// ---------------------------------------------------------------------------

func TestNewAgentSummaryService_DefaultMaxKeep(t *testing.T) {
	svc := NewAgentSummaryService(0)
	if svc == nil {
		t.Fatal("NewAgentSummaryService returned nil")
	}
	if svc.maxKeep != 50 {
		t.Errorf("maxKeep = %d; want 50 (default)", svc.maxKeep)
	}

	svc2 := NewAgentSummaryService(-5)
	if svc2.maxKeep != 50 {
		t.Errorf("negative maxKeep -> %d; want 50", svc2.maxKeep)
	}

	svc3 := NewAgentSummaryService(7)
	if svc3.maxKeep != 7 {
		t.Errorf("maxKeep = %d; want 7", svc3.maxKeep)
	}
}

func TestAgentSummaryService_RingBuffer(t *testing.T) {
	svc := NewAgentSummaryService(5)

	for i := 0; i < 10; i++ {
		svc.RecordRun(AgentRunSummary{
			AgentName: "test",
			Duration:  time.Second,
			ToolCalls: 3,
			Status:    "success",
		})
	}

	if svc.Count() != 5 {
		t.Errorf("Count = %d; want 5 (ring buffer cap)", svc.Count())
	}

	recent := svc.Recent(3)
	if len(recent) != 3 {
		t.Errorf("Recent(3) len = %d; want 3", len(recent))
	}
}

func TestAgentSummaryService_RecentAllWhenNIsZero(t *testing.T) {
	svc := NewAgentSummaryService(10)
	for i := 0; i < 4; i++ {
		svc.RecordRun(AgentRunSummary{AgentName: "a", Status: "success"})
	}

	all := svc.Recent(0)
	if len(all) != 4 {
		t.Errorf("Recent(0) len = %d; want 4 (all)", len(all))
	}

	// n larger than stored should also return everything.
	all2 := svc.Recent(100)
	if len(all2) != 4 {
		t.Errorf("Recent(100) len = %d; want 4 (all)", len(all2))
	}
}

func TestAgentSummaryService_RingBufferKeepsNewest(t *testing.T) {
	svc := NewAgentSummaryService(3)

	for i := 0; i < 5; i++ {
		svc.RecordRun(AgentRunSummary{
			AgentName: "agent",
			ToolCalls: i, // use ToolCalls as a sequence marker
			Status:    "success",
		})
	}

	all := svc.Recent(0)
	if len(all) != 3 {
		t.Fatalf("stored count = %d; want 3", len(all))
	}
	// Should contain the newest three entries: ToolCalls = 2, 3, 4.
	for i, want := range []int{2, 3, 4} {
		if all[i].ToolCalls != want {
			t.Errorf("entry[%d].ToolCalls = %d; want %d", i, all[i].ToolCalls, want)
		}
	}
}

func TestAgentSummaryService_EmptyRecent(t *testing.T) {
	svc := NewAgentSummaryService(5)
	if svc.Count() != 0 {
		t.Errorf("empty Count = %d; want 0", svc.Count())
	}
	if r := svc.Recent(3); len(r) != 0 {
		t.Errorf("empty Recent len = %d; want 0", len(r))
	}
}

// ---------------------------------------------------------------------------
// FormatAgentSummary / FormatAgentSummaryList
// ---------------------------------------------------------------------------

func TestFormatAgentSummary(t *testing.T) {
	s := AgentRunSummary{
		AgentName:    "coder",
		Duration:     2 * time.Minute,
		ToolCalls:    5,
		InputTokens:  1000,
		OutputTokens: 500,
		Cost:         0.01,
		Status:       "success",
		KeyActions:   []string{"read 3 files", "ran 1 command"},
	}

	out := FormatAgentSummary(s)
	if out == "" {
		t.Fatal("format output empty")
	}
	if !strings.Contains(out, "coder") {
		t.Errorf("output missing agent name; got %q", out)
	}
	if !strings.Contains(out, "success") {
		t.Errorf("output missing status; got %q", out)
	}
	if !strings.Contains(out, "5 tool calls") {
		t.Errorf("output missing tool-call count; got %q", out)
	}
}

func TestFormatAgentSummary_NoActionsUsesResult(t *testing.T) {
	s := AgentRunSummary{
		AgentName: "tester",
		Duration:  500 * time.Millisecond,
		Status:    "error",
		Result:    "something went wrong",
	}
	out := FormatAgentSummary(s)
	if !strings.Contains(out, "something went wrong") {
		t.Errorf("expected result used as action fallback; got %q", out)
	}
	if !strings.Contains(out, "[x]") {
		t.Errorf("expected error glyph [x]; got %q", out)
	}
}

func TestFormatAgentSummaryList(t *testing.T) {
	empty := FormatAgentSummaryList(nil)
	if empty == "" {
		t.Error("empty list should render a placeholder, not empty string")
	}
	if !strings.Contains(empty, "no agent runs") {
		t.Errorf("empty placeholder unexpected: %q", empty)
	}

	list := []AgentRunSummary{
		{AgentName: "a", Duration: time.Second, Status: "success"},
		{AgentName: "b", Duration: 2 * time.Second, Status: "error"},
	}
	out := FormatAgentSummaryList(list)
	if !strings.Contains(out, "a agent") || !strings.Contains(out, "b agent") {
		t.Errorf("output missing one of the agents: %q", out)
	}
	// Entries should be separated by a blank line.
	if !strings.Contains(out, "\n\n") {
		t.Errorf("expected blank-line separator between summaries: %q", out)
	}
}

// ---------------------------------------------------------------------------
// GenerateAgentSummary
// ---------------------------------------------------------------------------

func TestGenerateAgentSummary_Success(t *testing.T) {
	s := GenerateAgentSummary(
		"worker",
		3*time.Second,
		[]string{"read_file", "read_file", "bash"},
		1200, 300,
		0.0042,
		"All good, completed cleanly.",
	)

	if s.AgentName != "worker" {
		t.Errorf("AgentName = %q; want worker", s.AgentName)
	}
	if s.Status != "success" {
		t.Errorf("Status = %q; want success", s.Status)
	}
	if s.ToolCalls != 3 {
		t.Errorf("ToolCalls = %d; want 3", s.ToolCalls)
	}
	if len(s.KeyActions) == 0 {
		t.Error("KeyActions should be populated from tool calls")
	}
	if s.Duration != 3*time.Second {
		t.Errorf("Duration = %v; want 3s", s.Duration)
	}
	if s.Cost != 0.0042 {
		t.Errorf("Cost = %v; want 0.0042", s.Cost)
	}
}

func TestGenerateAgentSummary_ErrorStatus(t *testing.T) {
	s := GenerateAgentSummary(
		"worker",
		time.Second,
		nil,
		0, 0, 0,
		"Operation failed during validation",
	)
	if s.Status != "error" {
		t.Errorf("Status = %q; want error", s.Status)
	}
}

// ---------------------------------------------------------------------------
// SummarizeToolUses
// ---------------------------------------------------------------------------

func TestSummarizeToolUses_GroupsConsecutive(t *testing.T) {
	calls := []ToolCall{
		{Name: "read_file", Args: map[string]any{"path": "a.go"}, Success: true},
		{Name: "read_file", Args: map[string]any{"path": "b.go"}, Success: true},
		{Name: "bash", Args: map[string]any{"command": "ls"}, Success: true},
		{Name: "read_file", Args: map[string]any{"path": "c.go"}, Success: false},
	}

	summaries := SummarizeToolUses(calls)
	if len(summaries) != 3 {
		t.Fatalf("expected 3 groups (read,bash,read), got %d", len(summaries))
	}

	if summaries[0].ToolName != "read_file" || summaries[0].CallCount != 2 {
		t.Errorf("group[0] = %+v; want read_file x2", summaries[0])
	}
	if summaries[0].SuccessCount != 2 || summaries[0].ErrorCount != 0 {
		t.Errorf("group[0] counts = ok:%d err:%d; want ok:2 err:0",
			summaries[0].SuccessCount, summaries[0].ErrorCount)
	}
	if summaries[1].ToolName != "bash" || summaries[1].CallCount != 1 {
		t.Errorf("group[1] = %+v; want bash x1", summaries[1])
	}
	if summaries[2].ToolName != "read_file" || summaries[2].CallCount != 1 {
		t.Errorf("group[2] = %+v; want read_file x1", summaries[2])
	}
	if summaries[2].ErrorCount != 1 {
		t.Errorf("group[2].ErrorCount = %d; want 1", summaries[2].ErrorCount)
	}
}

func TestSummarizeToolUses_Empty(t *testing.T) {
	if s := SummarizeToolUses(nil); s != nil {
		t.Errorf("expected nil for empty input; got %v", s)
	}
}

func TestSummarizeToolUses_CollectsTargets(t *testing.T) {
	calls := []ToolCall{
		{Name: "read_file", Args: map[string]any{"path": "a.go"}, Success: true},
		{Name: "read_file", Args: map[string]any{"path": "b.go"}, Success: true},
		{Name: "read_file", Args: map[string]any{"path": "a.go"}, Success: true}, // dup
	}
	summaries := SummarizeToolUses(calls)
	if len(summaries) != 1 {
		t.Fatalf("want 1 group, got %d", len(summaries))
	}
	if len(summaries[0].Targets) != 2 {
		t.Errorf("Targets = %v; want 2 unique (a.go, b.go)", summaries[0].Targets)
	}
}

// ---------------------------------------------------------------------------
// DetectPatterns
// ---------------------------------------------------------------------------

func TestDetectPatterns_Empty(t *testing.T) {
	if p := DetectPatterns(nil); p != nil {
		t.Errorf("expected nil for empty calls; got %v", p)
	}
}

func TestDetectPatterns_ReadingFilesInDir(t *testing.T) {
	calls := []ToolCall{
		{Name: "read_file", Args: map[string]any{"path": "internal/auth/login.go"}, Success: true},
		{Name: "read_file", Args: map[string]any{"path": "internal/auth/logout.go"}, Success: true},
		{Name: "read_file", Args: map[string]any{"path": "internal/auth/session.go"}, Success: true},
	}
	patterns := DetectPatterns(calls)

	found := false
	for _, p := range patterns {
		if strings.Contains(p.Description, "Reading files in") &&
			strings.Contains(p.Description, "internal/auth") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'Reading files in internal/auth' pattern; got %+v", patterns)
	}
}

func TestDetectPatterns_GrepPlusRead(t *testing.T) {
	calls := []ToolCall{
		{Name: "grep", Args: map[string]any{"pattern": "TODO"}, Success: true},
		{Name: "read_file", Args: map[string]any{"path": "x.go"}, Success: true},
	}
	patterns := DetectPatterns(calls)

	found := false
	for _, p := range patterns {
		if strings.Contains(p.Description, "Searching") {
			found = true
			if !strings.Contains(p.Description, "TODO") {
				t.Errorf("expected needle TODO in description; got %q", p.Description)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected a 'Searching' pattern; got %+v", patterns)
	}
}

func TestDetectPatterns_RunningTests(t *testing.T) {
	calls := []ToolCall{
		{Name: "bash", Args: map[string]any{"command": "go test ./..."}, Success: true},
	}

	patterns := DetectPatterns(calls)
	found := false
	for _, p := range patterns {
		if strings.Contains(strings.ToLower(p.Description), "test") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a 'Running tests' pattern; got %+v", patterns)
	}
}

func TestDetectPatterns_Refactoring(t *testing.T) {
	calls := []ToolCall{
		{Name: "edit", Args: map[string]any{"path": "foo.go"}, Success: true},
		{Name: "bash", Args: map[string]any{"command": "go build"}, Success: true},
		{Name: "grep", Args: map[string]any{"pattern": "oldName"}, Success: true},
	}
	patterns := DetectPatterns(calls)

	found := false
	for _, p := range patterns {
		if strings.Contains(strings.ToLower(p.Description), "refactor") {
			found = true
			if p.Confidence < 0.8 {
				t.Errorf("refactor confidence = %v; want >=0.8", p.Confidence)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected a refactor pattern; got %+v", patterns)
	}
}

// ---------------------------------------------------------------------------
// FormatToolUseSummary / FormatPatterns
// ---------------------------------------------------------------------------

func TestFormatToolUseSummary(t *testing.T) {
	if out := FormatToolUseSummary(nil); out != "" {
		t.Errorf("empty input should return empty string; got %q", out)
	}

	summaries := []ToolUseSummary{
		{
			ToolName:     "read_file",
			CallCount:    3,
			SuccessCount: 3,
			Targets:      []string{"a.go", "b.go"},
		},
	}
	out := FormatToolUseSummary(summaries)
	if out == "" {
		t.Fatal("output empty")
	}
	if !strings.Contains(out, "read_file") {
		t.Errorf("output missing tool name; got %q", out)
	}
	if !strings.Contains(out, "x3") {
		t.Errorf("output missing x3 multiplier; got %q", out)
	}
}

func TestFormatPatterns_SortedByConfidenceDesc(t *testing.T) {
	patterns := []ToolUsePattern{
		{Description: "low-conf", Confidence: 0.3},
		{Description: "high-conf", Confidence: 0.95},
		{Description: "mid-conf", Confidence: 0.6},
	}
	out := FormatPatterns(patterns)
	if out == "" {
		t.Fatal("output empty")
	}

	hi := strings.Index(out, "high-conf")
	mid := strings.Index(out, "mid-conf")
	lo := strings.Index(out, "low-conf")
	if hi < 0 || mid < 0 || lo < 0 {
		t.Fatalf("missing entries in output: %q", out)
	}
	if !(hi < mid && mid < lo) {
		t.Errorf("expected high < mid < low ordering; got hi=%d mid=%d lo=%d (%q)",
			hi, mid, lo, out)
	}
}

func TestFormatPatterns_Empty(t *testing.T) {
	if out := FormatPatterns(nil); out != "" {
		t.Errorf("expected empty string; got %q", out)
	}
}

// ---------------------------------------------------------------------------
// ToolUseBatch (the renamed legacy struct from summary.go)
// ---------------------------------------------------------------------------

func TestToolUseBatch_BasicUsage(t *testing.T) {
	now := time.Now()
	batch := ToolUseBatch{
		Summary: "Ran Read x3",
		Tools: []ToolInfo{
			{Name: "Read", Input: map[string]any{"path": "a.go"}},
		},
		CreatedAt: now,
	}
	if batch.Summary == "" {
		t.Error("Summary empty")
	}
	if len(batch.Tools) != 1 {
		t.Errorf("Tools len = %d; want 1", len(batch.Tools))
	}
	if batch.CreatedAt != now {
		t.Error("CreatedAt not preserved")
	}
}
