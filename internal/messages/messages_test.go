package messages

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/costa/polypod/internal/adapter"
)

// ─────────────────────────────────────────────────────────────────────────────
// formatter.go — header / result / usage
// ─────────────────────────────────────────────────────────────────────────────

func TestFormatToolCallHeader_NoArgs(t *testing.T) {
	got := FormatToolCallHeader("Bash", nil)
	if got != "Bash()" {
		t.Errorf("FormatToolCallHeader(Bash, nil) = %q; want %q", got, "Bash()")
	}
	got = FormatToolCallHeader("Read", map[string]any{})
	if got != "Read()" {
		t.Errorf("FormatToolCallHeader(Read, empty) = %q; want %q", got, "Read()")
	}
}

func TestFormatToolCallHeader_WithArgs(t *testing.T) {
	out := FormatToolCallHeader("Bash", map[string]any{
		"command": "ls -la",
	})
	if !strings.HasPrefix(out, "Bash(") || !strings.HasSuffix(out, ")") {
		t.Errorf("bad envelope: %q", out)
	}
	if !strings.Contains(out, "command:") {
		t.Errorf("missing key name: %q", out)
	}
	if !strings.Contains(out, `"ls -la"`) {
		t.Errorf("missing quoted string: %q", out)
	}
}

func TestFormatToolCallHeader_LongArgsTruncated(t *testing.T) {
	// Each per-value is truncated to ~60 chars; to blow past the 120-char
	// outer cap we need several args so the joined string gets truncated.
	long := strings.Repeat("x", 80)
	out := FormatToolCallHeader("Tool", map[string]any{
		"a": long,
		"b": long,
		"c": long,
	})
	// Output is toolName + "(" + joined + ")". joined is capped to 120 runes.
	inner := strings.TrimSuffix(strings.TrimPrefix(out, "Tool("), ")")
	if len(inner) > 120 {
		t.Errorf("inner length %d > 120: %q", len(inner), inner)
	}
	if !strings.HasSuffix(inner, "...") {
		t.Errorf("expected trailing ellipsis, got %q", inner)
	}
}

func TestFormatToolCallHeader_PerValueTruncation(t *testing.T) {
	// A single long string value should be per-value truncated to 60 chars
	// (quoted), producing `"...57 chars...\"..."` inside.
	long := strings.Repeat("y", 200)
	out := FormatToolCallHeader("Tool", map[string]any{"data": long})
	if !strings.Contains(out, `..."`) {
		t.Errorf("expected per-value ellipsis, got %q", out)
	}
}

func TestFormatToolCallHeader_MixedTypes(t *testing.T) {
	out := FormatToolCallHeader("F", map[string]any{
		"flag":  true,
		"count": 42,
		"name":  "abc",
		"none":  nil,
	})
	// Can't rely on map order, just check each rendered value is present.
	for _, want := range []string{"true", "42", `"abc"`, "null"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in %q", want, out)
		}
	}
}

func TestFormatToolResult_SingleLine(t *testing.T) {
	out := FormatToolResult("Read", "hello world", false)
	if out != "Read: hello world" {
		t.Errorf("single line: got %q", out)
	}
}

func TestFormatToolResult_Error(t *testing.T) {
	out := FormatToolResult("Bash", "oops", true)
	if !strings.Contains(out, "[ERROR]") {
		t.Errorf("missing [ERROR] marker: %q", out)
	}
	if !strings.Contains(out, "oops") {
		t.Errorf("missing payload: %q", out)
	}
}

func TestFormatToolResult_MultiLineIndented(t *testing.T) {
	out := FormatToolResult("Grep", "line1\nline2\nline3", false)
	// Multiline format: "Grep:\n  line1\n  line2\n  line3"
	if !strings.HasPrefix(out, "Grep:\n") {
		t.Errorf("missing header line: %q", out)
	}
	if !strings.Contains(out, "  line1") {
		t.Errorf("line1 not indented: %q", out)
	}
	if !strings.Contains(out, "  line3") {
		t.Errorf("line3 not indented: %q", out)
	}
}

func TestFormatTokenUsage(t *testing.T) {
	out := FormatTokenUsage(1200, 856, 0, 0.0234)
	if !strings.Contains(out, "prompt") || !strings.Contains(out, "completion") || !strings.Contains(out, "tokens") {
		t.Errorf("missing labels: %q", out)
	}
	// Cost is < 0.5, so 4-digit format: $0.0234
	if !strings.Contains(out, "$0.0234") {
		t.Errorf("missing cost: %q", out)
	}
}

func TestFormatTokenUsage_ComputesTotal(t *testing.T) {
	// When total == 0, function sums prompt + completion.
	out := FormatTokenUsage(100, 200, 0, 1.5)
	// With high cost, format is $1.50
	if !strings.Contains(out, "$1.50") {
		t.Errorf("expected rounded cost $1.50 in %q", out)
	}
	// Total = 300, FormatNumber(300) = "300"
	if !strings.Contains(out, "300") {
		t.Errorf("expected total 300 in %q", out)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// formatter.go — FormatDurationHuman (time.Duration version)
// NOTE: FormatDuration in this package takes int64 milliseconds.
// ─────────────────────────────────────────────────────────────────────────────

func TestFormatDurationHuman(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"negative", -1 * time.Second, "0s"},
		{"millis", 250 * time.Millisecond, "250ms"},
		{"whole seconds", 2 * time.Second, "2s"},
		{"fractional seconds", 4200 * time.Millisecond, "4.2s"},
		{"exact minute", 1 * time.Minute, "1m"},
		{"mins and secs", 90 * time.Second, "1m 30s"},
		{"exact hour", 1 * time.Hour, "1h"},
		{"hours and mins", 2*time.Hour + 5*time.Minute, "2h 5m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatDurationHuman(tt.d)
			if got != tt.want {
				t.Errorf("FormatDurationHuman(%v) = %q; want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestFormatDurationHuman_Days(t *testing.T) {
	got := FormatDurationHuman(49 * time.Hour) // 2d 1h 0m
	if !strings.HasPrefix(got, "2d ") {
		t.Errorf("expected days prefix, got %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// format.go — FormatDuration (int64 ms version)
// ─────────────────────────────────────────────────────────────────────────────

func TestFormatDuration_Milliseconds(t *testing.T) {
	tests := []struct {
		ms   int64
		want string
	}{
		{0, "0s"},
		{1500, "1s"},
		{59000, "59s"},
		{60000, "1m 0s"},
		{3600000, "1h 0m 0s"},
	}
	for _, tt := range tests {
		got := FormatDuration(tt.ms)
		if got != tt.want {
			t.Errorf("FormatDuration(%d) = %q; want %q", tt.ms, got, tt.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// formatter.go — FormatFileSize
// ─────────────────────────────────────────────────────────────────────────────

func TestFormatFileSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
		{-100, "0 B"},
	}
	for _, tt := range tests {
		got := FormatFileSize(tt.bytes)
		if got != tt.want {
			t.Errorf("FormatFileSize(%d) = %q; want %q", tt.bytes, got, tt.want)
		}
	}
}

func TestFormatFileSize_Terabytes(t *testing.T) {
	const tb = int64(1024) * 1024 * 1024 * 1024
	got := FormatFileSize(2 * tb)
	if !strings.HasSuffix(got, "TB") {
		t.Errorf("expected TB suffix, got %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// formatter.go — TruncateWithEllipsis
// ─────────────────────────────────────────────────────────────────────────────

func TestTruncateWithEllipsis(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"short passthrough", "hi", 10, "hi"},
		{"empty", "", 5, ""},
		{"exact length", "hello", 5, "hello"},
		{"truncated", "hello world", 8, "hello..."},
		{"zero maxLen", "abc", 0, "..."},
		{"tiny maxLen", "abcdef", 2, "ab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateWithEllipsis(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("TruncateWithEllipsis(%q, %d) = %q; want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// formatter.go — FormatErrorWithContext
// ─────────────────────────────────────────────────────────────────────────────

func TestFormatErrorWithContext(t *testing.T) {
	err := errors.New("boom")

	out := FormatErrorWithContext(err, "while reading file")
	if out != "[while reading file] boom" {
		t.Errorf("with context: got %q", out)
	}

	out = FormatErrorWithContext(err, "")
	if out != "boom" {
		t.Errorf("empty context: got %q", out)
	}

	out = FormatErrorWithContext(nil, "anything")
	if out != "" {
		t.Errorf("nil err: got %q, want empty", out)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// formatter.go — FormatMarkdownTable
// ─────────────────────────────────────────────────────────────────────────────

func TestFormatMarkdownTable(t *testing.T) {
	headers := []string{"Name", "Value"}
	rows := [][]string{
		{"foo", "1"},
		{"bar", "22"},
	}
	out := FormatMarkdownTable(headers, rows)
	if out == "" {
		t.Fatal("unexpected empty output")
	}
	// Every non-empty row starts with '|'.
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if !strings.HasPrefix(line, "|") {
			t.Errorf("row missing leading pipe: %q", line)
		}
	}
	if !strings.Contains(out, "Name") || !strings.Contains(out, "Value") {
		t.Errorf("missing headers: %q", out)
	}
	if !strings.Contains(out, "foo") || !strings.Contains(out, "bar") {
		t.Errorf("missing data: %q", out)
	}
	// Separator row uses dashes.
	if !strings.Contains(out, "---") {
		t.Errorf("missing separator: %q", out)
	}
}

func TestFormatMarkdownTable_EmptyHeaders(t *testing.T) {
	out := FormatMarkdownTable(nil, [][]string{{"x"}})
	if out != "" {
		t.Errorf("expected empty string for no headers, got %q", out)
	}
}

func TestFormatMarkdownTable_ShortRow(t *testing.T) {
	// Row has fewer cells than header count: should still render.
	out := FormatMarkdownTable([]string{"A", "B", "C"}, [][]string{{"x"}})
	if out == "" {
		t.Fatal("empty output for short row")
	}
	// Still has 3 columns (3 pipes per content row + leading pipe = 4 pipes).
	firstDataLine := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "x") {
			firstDataLine = line
			break
		}
	}
	if strings.Count(firstDataLine, "|") < 4 {
		t.Errorf("expected 4 pipes in data row, got %q", firstDataLine)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// toolgroup.go — GroupToolEvents / ShouldCollapse / FormatGroup / FormatToolBadge
// ─────────────────────────────────────────────────────────────────────────────

func makeEvent(name, id string, args map[string]any, elapsed time.Duration) adapter.ToolEventInfo {
	return adapter.ToolEventInfo{
		Type:     adapter.ToolEventComplete,
		ToolName: name,
		ToolID:   id,
		Args:     args,
		Elapsed:  elapsed,
	}
}

func TestGroupToolEvents_Empty(t *testing.T) {
	got := GroupToolEvents(nil)
	if got != nil {
		t.Errorf("expected nil for empty input, got %v", got)
	}
}

func TestGroupToolEvents_SingleTool(t *testing.T) {
	events := []adapter.ToolEventInfo{
		makeEvent("read_file", "1", map[string]any{"path": "main.go"}, 100*time.Millisecond),
	}
	groups := GroupToolEvents(events)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].Kind != GroupSingle {
		t.Errorf("expected GroupSingle, got %v", groups[0].Kind)
	}
}

func TestGroupToolEvents_ReadBatch(t *testing.T) {
	events := []adapter.ToolEventInfo{
		makeEvent("read_file", "1", map[string]any{"path": "a.go"}, 50*time.Millisecond),
		makeEvent("read_file", "2", map[string]any{"path": "b.go"}, 50*time.Millisecond),
		makeEvent("read_file", "3", map[string]any{"path": "c.go"}, 50*time.Millisecond),
	}
	groups := GroupToolEvents(events)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].Kind != GroupReadBatch {
		t.Errorf("expected GroupReadBatch, got %v", groups[0].Kind)
	}
	if len(groups[0].Tools) != 3 {
		t.Errorf("expected 3 tools, got %d", len(groups[0].Tools))
	}
	if groups[0].TotalTime != 150*time.Millisecond {
		t.Errorf("expected total 150ms, got %v", groups[0].TotalTime)
	}
	if groups[0].Summary == "" {
		t.Error("expected non-empty summary")
	}
}

func TestGroupToolEvents_BashBatch(t *testing.T) {
	events := []adapter.ToolEventInfo{
		makeEvent("bash", "1", map[string]any{"command": "ls"}, 10*time.Millisecond),
		makeEvent("bash", "2", map[string]any{"command": "pwd"}, 10*time.Millisecond),
	}
	groups := GroupToolEvents(events)
	if len(groups) != 1 || groups[0].Kind != GroupBashBatch {
		t.Fatalf("expected bash batch, got %+v", groups)
	}
}

func TestGroupToolEvents_DedupByToolID(t *testing.T) {
	// Same ToolID twice should be deduped (Start + Complete).
	events := []adapter.ToolEventInfo{
		{Type: adapter.ToolEventStart, ToolName: "read_file", ToolID: "x"},
		{Type: adapter.ToolEventComplete, ToolName: "read_file", ToolID: "x"},
		{Type: adapter.ToolEventComplete, ToolName: "read_file", ToolID: "x"}, // dup
	}
	groups := GroupToolEvents(events)
	total := 0
	for _, g := range groups {
		total += len(g.Tools)
	}
	if total != 1 {
		t.Errorf("expected 1 deduped tool, got %d", total)
	}
}

func TestGroupToolEvents_IgnoresStartOnlyEvents(t *testing.T) {
	events := []adapter.ToolEventInfo{
		{Type: adapter.ToolEventStart, ToolName: "bash", ToolID: "a"},
	}
	groups := GroupToolEvents(events)
	if groups != nil {
		t.Errorf("expected nil groups (no terminal events), got %v", groups)
	}
}

func TestShouldCollapse(t *testing.T) {
	// Fewer than 4 events → false.
	few := []adapter.ToolEventInfo{
		makeEvent("read_file", "1", nil, 0),
		makeEvent("read_file", "2", nil, 0),
	}
	if ShouldCollapse(few) {
		t.Error("ShouldCollapse(2 events) = true; want false")
	}

	// 4 consecutive read events → true.
	many := []adapter.ToolEventInfo{
		makeEvent("read_file", "1", nil, 0),
		makeEvent("read_file", "2", nil, 0),
		makeEvent("read_file", "3", nil, 0),
		makeEvent("read_file", "4", nil, 0),
	}
	if !ShouldCollapse(many) {
		t.Error("ShouldCollapse(4 reads) = false; want true")
	}
}

func TestFormatGroup_SingleExpanded(t *testing.T) {
	g := ToolGroup{
		Kind: GroupSingle,
		Tools: []adapter.ToolEventInfo{
			makeEvent("read_file", "1", map[string]any{"path": "main.go"}, 100*time.Millisecond),
		},
	}
	out := FormatGroup(g, false)
	if !strings.Contains(out, "main.go") {
		t.Errorf("expected filename in single-group output: %q", out)
	}
}

func TestFormatGroup_ReadBatchCompact(t *testing.T) {
	g := ToolGroup{
		Kind: GroupReadBatch,
		Tools: []adapter.ToolEventInfo{
			makeEvent("read_file", "1", map[string]any{"path": "a.go"}, 50*time.Millisecond),
			makeEvent("read_file", "2", map[string]any{"path": "b.go"}, 50*time.Millisecond),
		},
		TotalTime: 100 * time.Millisecond,
		Summary:   "Lido 2 arquivos: a.go, b.go (100ms)",
	}
	out := FormatGroup(g, false)
	if !strings.Contains(out, "▶") {
		t.Errorf("expected bullet marker: %q", out)
	}
	if !strings.Contains(out, "Lido 2 arquivos") {
		t.Errorf("expected summary: %q", out)
	}
}

func TestFormatGroup_BashBatchListsCommands(t *testing.T) {
	g := ToolGroup{
		Kind: GroupBashBatch,
		Tools: []adapter.ToolEventInfo{
			makeEvent("bash", "1", map[string]any{"command": "ls -la"}, 10*time.Millisecond),
			makeEvent("bash", "2", map[string]any{"command": "pwd"}, 10*time.Millisecond),
		},
		Summary: "Executado 2 comandos (20ms)",
	}
	out := FormatGroup(g, false)
	if !strings.Contains(out, "ls -la") {
		t.Errorf("expected 'ls -la' in output: %q", out)
	}
	if !strings.Contains(out, "pwd") {
		t.Errorf("expected 'pwd' in output: %q", out)
	}
}

func TestFormatGroup_VerboseExpands(t *testing.T) {
	g := ToolGroup{
		Kind: GroupReadBatch,
		Tools: []adapter.ToolEventInfo{
			makeEvent("read_file", "1", map[string]any{"path": "a.go"}, 50*time.Millisecond),
			makeEvent("read_file", "2", map[string]any{"path": "b.go"}, 50*time.Millisecond),
		},
		Summary: "Lido 2 arquivos",
	}
	out := FormatGroup(g, true)
	// Verbose: each tool gets its own FormatToolBadge line.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines, got %d: %q", len(lines), out)
	}
	if !strings.Contains(out, "a.go") || !strings.Contains(out, "b.go") {
		t.Errorf("missing filenames: %q", out)
	}
}

func TestFormatToolBadge_Complete(t *testing.T) {
	ev := makeEvent("read_file", "1", map[string]any{"path": "/tmp/main.go"}, 100*time.Millisecond)
	out := FormatToolBadge(ev)
	if !strings.Contains(out, "✓") {
		t.Errorf("expected checkmark: %q", out)
	}
	if !strings.Contains(out, "main.go") {
		t.Errorf("expected basename: %q", out)
	}
	if !strings.Contains(out, "100ms") {
		t.Errorf("expected elapsed: %q", out)
	}
}

func TestFormatToolBadge_Error(t *testing.T) {
	ev := adapter.ToolEventInfo{
		Type:     adapter.ToolEventError,
		ToolName: "bash",
		Args:     map[string]any{"command": "fail"},
		Elapsed:  500 * time.Millisecond,
	}
	out := FormatToolBadge(ev)
	if !strings.Contains(out, "✗") {
		t.Errorf("expected error marker: %q", out)
	}
}

func TestFormatToolBadge_Start(t *testing.T) {
	ev := adapter.ToolEventInfo{
		Type:     adapter.ToolEventStart,
		ToolName: "bash",
	}
	out := FormatToolBadge(ev)
	if !strings.Contains(out, "running") {
		t.Errorf("expected 'running' marker: %q", out)
	}
}

func TestFormatToolBadge_NoElapsed(t *testing.T) {
	ev := makeEvent("read_file", "1", map[string]any{"path": "x.go"}, 0)
	out := FormatToolBadge(ev)
	if strings.Contains(out, "(") {
		t.Errorf("expected no elapsed parentheses when Elapsed==0: %q", out)
	}
}
