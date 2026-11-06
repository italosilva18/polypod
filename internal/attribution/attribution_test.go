package attribution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestComputeStats_EmptyInput verifies that an empty slice yields zero stats.
func TestComputeStats_EmptyInput(t *testing.T) {
	stats := ComputeStats(nil)
	if stats.NShotCount != 0 {
		t.Errorf("NShotCount = %d, want 0", stats.NShotCount)
	}
	if stats.ToolCallCount != 0 {
		t.Errorf("ToolCallCount = %d, want 0", stats.ToolCallCount)
	}
	if stats.MemoriesRecalled != 0 {
		t.Errorf("MemoriesRecalled = %d, want 0", stats.MemoriesRecalled)
	}
}

// TestComputeStats_CountsUserPrompts verifies user messages with visible text
// are counted as N-shots.
func TestComputeStats_CountsUserPrompts(t *testing.T) {
	msgs := []map[string]any{
		{
			"type": "user",
			"message": map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "please fix the bug"},
				},
			},
		},
		{
			"type": "user",
			"message": map[string]any{
				"role":    "user",
				"content": "another prompt as string",
			},
		},
	}
	stats := ComputeStats(msgs)
	if stats.NShotCount != 2 {
		t.Errorf("NShotCount = %d, want 2", stats.NShotCount)
	}
}

// TestComputeStats_IgnoresSidechain verifies that sidechain user entries are
// excluded from the prompt count.
func TestComputeStats_IgnoresSidechain(t *testing.T) {
	msgs := []map[string]any{
		{
			"type":        "user",
			"isSidechain": true,
			"message": map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "subagent request"},
				},
			},
		},
		{
			"type": "user",
			"message": map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "main request"},
				},
			},
		},
	}
	stats := ComputeStats(msgs)
	if stats.NShotCount != 1 {
		t.Errorf("NShotCount = %d, want 1 (sidechain must be excluded)", stats.NShotCount)
	}
}

// TestComputeStats_IgnoresTerminalOutput verifies that user messages that
// contain only tool output tags are NOT counted as prompts.
func TestComputeStats_IgnoresTerminalOutput(t *testing.T) {
	msgs := []map[string]any{
		{
			"type": "user",
			"message": map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "<bash_output>some output</bash_output>"},
				},
			},
		},
		{
			"type": "user",
			"message": map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "<tool_result>result</tool_result>"},
				},
			},
		},
	}
	stats := ComputeStats(msgs)
	if stats.NShotCount != 0 {
		t.Errorf("NShotCount = %d, want 0 (terminal output must be excluded)", stats.NShotCount)
	}
}

// TestComputeStats_CountsToolCallsAndMemories verifies tool_use counting and
// the memory-file heuristic.
func TestComputeStats_CountsToolCallsAndMemories(t *testing.T) {
	msgs := []map[string]any{
		{
			"type": "assistant",
			"message": map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{
						"type":  "tool_use",
						"name":  "Read",
						"input": map[string]any{"file_path": "/project/CLAUDE.md"},
					},
					map[string]any{
						"type":  "tool_use",
						"name":  "Read",
						"input": map[string]any{"file_path": "/project/main.go"},
					},
					map[string]any{
						"type":  "tool_use",
						"name":  "Grep",
						"input": map[string]any{"pattern": "memory.md"},
					},
					map[string]any{"type": "text", "text": "thinking..."},
				},
			},
		},
	}
	stats := ComputeStats(msgs)
	if stats.ToolCallCount != 3 {
		t.Errorf("ToolCallCount = %d, want 3", stats.ToolCallCount)
	}
	if stats.MemoriesRecalled != 2 {
		t.Errorf("MemoriesRecalled = %d, want 2 (CLAUDE.md + memory.md pattern)", stats.MemoriesRecalled)
	}
}

// TestFormatAttributionBadge verifies both the zero-pct and populated-pct
// branches of FormatAttributionBadge.
func TestFormatAttributionBadge(t *testing.T) {
	// Zero case falls back to the generic Claude Code badge.
	got := FormatAttributionBadge(AttributionStats{})
	if !strings.Contains(got, "Claude Code") || !strings.Contains(got, ProductURL) {
		t.Errorf("empty badge = %q, want Claude Code URL", got)
	}

	got = FormatAttributionBadge(AttributionStats{ClaudeContributionPct: 87})
	want := "Claude contributed 87% of this change"
	if got != want {
		t.Errorf("badge = %q, want %q", got, want)
	}
}

// TestBuildCommitAttribution verifies the commit trailer format.
func TestBuildCommitAttribution(t *testing.T) {
	got := buildCommitAttribution(AttributionStats{}, DefaultModelName)
	if !strings.Contains(got, "Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>") {
		t.Errorf("commit trailer missing Co-Authored-By line: %q", got)
	}
	if !strings.Contains(got, "Generated with [Claude Code]("+ProductURL+")") {
		t.Errorf("commit trailer missing Claude Code link: %q", got)
	}
	// Must be multi-line.
	if !strings.Contains(got, "\n") {
		t.Errorf("commit trailer must be multiline, got %q", got)
	}
}

// TestBuildPRAttribution_Empty verifies the fallback when nothing is reportable.
func TestBuildPRAttribution_Empty(t *testing.T) {
	got := buildPRAttribution(AttributionStats{}, DefaultModelName)
	want := "🤖 Generated with [Claude Code](" + ProductURL + ")"
	if got != want {
		t.Errorf("empty PR attribution = %q, want %q", got, want)
	}
}

// TestBuildPRAttribution_WithStats verifies the enhanced PR footer shape.
func TestBuildPRAttribution_WithStats(t *testing.T) {
	stats := AttributionStats{
		ClaudeContributionPct: 93,
		NShotCount:            3,
		MemoriesRecalled:      2,
	}
	got := buildPRAttribution(stats, DefaultModelName)

	// Must contain the base badge.
	if !strings.Contains(got, "Generated with [Claude Code]") {
		t.Errorf("PR attribution missing badge: %q", got)
	}
	// Must contain the percentage.
	if !strings.Contains(got, "93%") {
		t.Errorf("PR attribution missing percentage: %q", got)
	}
	// Must contain the N-shot phrase.
	if !strings.Contains(got, "3-shotted by Claude Opus 4.6") {
		t.Errorf("PR attribution missing N-shot phrase: %q", got)
	}
	// Must use plural 'memories'.
	if !strings.Contains(got, "2 memories recalled") {
		t.Errorf("PR attribution missing memories plural: %q", got)
	}
}

// TestBuildPRAttribution_SingularMemory verifies singular noun formatting.
func TestBuildPRAttribution_SingularMemory(t *testing.T) {
	stats := AttributionStats{MemoriesRecalled: 1}
	got := buildPRAttribution(stats, DefaultModelName)
	if !strings.Contains(got, "1 memory recalled") {
		t.Errorf("expected singular 'memory', got %q", got)
	}
	if strings.Contains(got, "memories") {
		t.Errorf("should not use plural for count=1, got %q", got)
	}
}

// TestIsMemoryFileAccess verifies the path heuristics.
func TestIsMemoryFileAccess(t *testing.T) {
	cases := []struct {
		name string
		tool string
		in   map[string]any
		want bool
	}{
		{"CLAUDE.md path", "Read", map[string]any{"file_path": "/repo/CLAUDE.md"}, true},
		{"polypod dir", "Read", map[string]any{"path": "/home/.polypod/sessions"}, true},
		{"memory dir", "Glob", map[string]any{"pattern": "/memory/foo.md"}, true},
		{"memory.md file", "Grep", map[string]any{"pattern": "memory.md"}, true},
		{"regular file", "Read", map[string]any{"file_path": "/repo/main.go"}, false},
		{"nil input", "Read", nil, false},
		{"empty candidates", "Read", map[string]any{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMemoryFileAccess(tc.tool, tc.in); got != tc.want {
				t.Errorf("isMemoryFileAccess(%q, %v) = %v, want %v", tc.tool, tc.in, got, tc.want)
			}
		})
	}
}

// TestIsTerminalOutput verifies the terminal-output tag detection helper.
func TestIsTerminalOutput(t *testing.T) {
	cases := map[string]bool{
		"<bash_input>ls</bash_input>":      true,
		"<bash_output>hello</bash_output>": true,
		"<tool_result>ok</tool_result>":    true,
		"<function_results>...":            true,
		"hello world":                      false,
		"<other>not terminal</other>":      false,
		"":                                 false,
	}
	for input, want := range cases {
		if got := isTerminalOutput(input); got != want {
			t.Errorf("isTerminalOutput(%q) = %v, want %v", input, got, want)
		}
	}
}

// TestSlicePostBoundary verifies that entries before the last compact_boundary
// are dropped.
func TestSlicePostBoundary(t *testing.T) {
	entries := []transcriptEntry{
		{Type: "user"},
		{Type: "assistant"},
		{Type: "system", Subtype: "compact_boundary"},
		{Type: "user"},
		{Type: "assistant"},
	}
	got := slicePostBoundary(entries)
	if len(got) != 2 {
		t.Errorf("post-boundary len = %d, want 2", len(got))
	}
	if len(got) > 0 && got[0].Type != "user" {
		t.Errorf("first post-boundary entry type = %q, want 'user'", got[0].Type)
	}

	// Without any boundary the full slice is returned.
	noBoundary := []transcriptEntry{{Type: "user"}, {Type: "assistant"}}
	if got := slicePostBoundary(noBoundary); len(got) != 2 {
		t.Errorf("no-boundary len = %d, want 2", len(got))
	}
}

// TestEstimateClaudeContributionPct verifies the heuristic produces a value
// in [0,99] and scales correctly.
func TestEstimateClaudeContributionPct(t *testing.T) {
	// Both zero → 0.
	if got := EstimateClaudeContributionPct(AttributionStats{}); got != 0 {
		t.Errorf("zero stats pct = %d, want 0", got)
	}

	// Equal tool calls and prompts → 50%.
	if got := EstimateClaudeContributionPct(AttributionStats{ToolCallCount: 1, NShotCount: 1}); got != 50 {
		t.Errorf("1:1 ratio pct = %d, want 50", got)
	}

	// Heavy tool use → > 50%.
	big := EstimateClaudeContributionPct(AttributionStats{ToolCallCount: 100, NShotCount: 1})
	if big <= 50 {
		t.Errorf("heavy tool-use pct = %d, want > 50", big)
	}
	if big > 99 {
		t.Errorf("pct must be clamped to <= 99, got %d", big)
	}
}

// TestGetEnhancedAttribution verifies that a non-empty sessionURL takes
// precedence and that the empty case returns a PR-style footer.
func TestGetEnhancedAttribution(t *testing.T) {
	url := "https://example.com/session/abc"
	if got := GetEnhancedAttribution(AttributionStats{}, url); got != url {
		t.Errorf("sessionURL precedence = %q, want %q", got, url)
	}

	stats := AttributionStats{NShotCount: 2}
	got := GetEnhancedAttribution(stats, "")
	if !strings.Contains(got, "2-shotted") {
		t.Errorf("enhanced attribution missing shot count: %q", got)
	}
}

// TestLoadTranscriptStats_MissingFile verifies the graceful-degradation path.
func TestLoadTranscriptStats_MissingFile(t *testing.T) {
	stats := loadTranscriptStats("/definitely/does/not/exist.jsonl")
	if stats.NShotCount != 0 || stats.ToolCallCount != 0 {
		t.Errorf("missing file should yield zero stats, got %+v", stats)
	}

	// Empty path also returns zero.
	if got := loadTranscriptStats(""); got.NShotCount != 0 {
		t.Errorf("empty path should yield zero stats, got %+v", got)
	}
}

// TestLoadTranscriptStats_RealFile writes a small JSONL transcript and verifies
// the end-to-end parse + compute pipeline.
func TestLoadTranscriptStats_RealFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")

	lines := []string{
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":"/x/CLAUDE.md"}}]}}`,
		`{"type":"system","subtype":"compact_boundary"}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"post boundary"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Grep","input":{"pattern":"foo"}}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	stats := loadTranscriptStats(path)
	// Only post-boundary entries should be counted.
	if stats.NShotCount != 1 {
		t.Errorf("NShotCount = %d, want 1 (post-boundary)", stats.NShotCount)
	}
	if stats.ToolCallCount != 1 {
		t.Errorf("ToolCallCount = %d, want 1 (post-boundary)", stats.ToolCallCount)
	}
	if stats.MemoriesRecalled != 0 {
		t.Errorf("MemoriesRecalled = %d, want 0 (CLAUDE.md was pre-boundary)", stats.MemoriesRecalled)
	}
}

// TestGenerateCommitAttribution_EmptyPath verifies the public generator returns
// a valid trailer even with no transcript.
func TestGenerateCommitAttribution_EmptyPath(t *testing.T) {
	got := GenerateCommitAttribution("")
	if !strings.Contains(got, "Co-Authored-By:") {
		t.Errorf("commit attribution missing Co-Authored-By: %q", got)
	}
}

// TestGeneratePRAttribution_EmptyPath verifies the public PR generator returns
// the fallback badge with no transcript.
func TestGeneratePRAttribution_EmptyPath(t *testing.T) {
	got := GeneratePRAttribution("")
	if !strings.Contains(got, ProductURL) {
		t.Errorf("PR attribution missing product URL: %q", got)
	}
}
