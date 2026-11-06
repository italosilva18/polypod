package diagnostics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Severity / SeveritySymbol ---

func TestSeveritySymbol(t *testing.T) {
	cases := []struct {
		sev  Severity
		want string
	}{
		{SeverityError, "✗"},
		{SeverityWarning, "⚠"},
		{SeverityInfo, "ℹ"},
		{SeverityHint, "★"},
		{Severity("unknown"), "•"},
		{Severity(""), "•"},
	}
	for _, c := range cases {
		if got := SeveritySymbol(c.sev); got != c.want {
			t.Errorf("SeveritySymbol(%q) = %q, want %q", c.sev, got, c.want)
		}
	}
}

func TestSeverityConstants(t *testing.T) {
	if SeverityError != "Error" {
		t.Errorf("SeverityError = %q, want %q", SeverityError, "Error")
	}
	if SeverityWarning != "Warning" {
		t.Errorf("SeverityWarning = %q, want %q", SeverityWarning, "Warning")
	}
	if SeverityInfo != "Info" {
		t.Errorf("SeverityInfo = %q, want %q", SeverityInfo, "Info")
	}
	if SeverityHint != "Hint" {
		t.Errorf("SeverityHint = %q, want %q", SeverityHint, "Hint")
	}
}

// --- DiagnosticBuffer ---

func TestNewDiagnosticBuffer_Defaults(t *testing.T) {
	b := NewDiagnosticBuffer(0)
	if b == nil {
		t.Fatal("NewDiagnosticBuffer returned nil")
	}
	if b.maxSize != 10000 {
		t.Errorf("default maxSize = %d, want 10000", b.maxSize)
	}
	if b.Len() != 0 {
		t.Errorf("new buffer Len = %d, want 0", b.Len())
	}

	b2 := NewDiagnosticBuffer(-5)
	if b2.maxSize != 10000 {
		t.Errorf("negative maxSize default = %d, want 10000", b2.maxSize)
	}

	b3 := NewDiagnosticBuffer(50)
	if b3.maxSize != 50 {
		t.Errorf("maxSize = %d, want 50", b3.maxSize)
	}
}

func TestDiagnosticBuffer_AddEventsClearLen(t *testing.T) {
	b := NewDiagnosticBuffer(5)
	ev := DiagnosticEvent{
		Timestamp: time.Now(),
		Category:  CategoryAPI,
		Severity:  SeverityInfo,
		Message:   "hello",
		Metadata:  map[string]any{"k": "v"},
	}
	b.Add(ev)
	if b.Len() != 1 {
		t.Errorf("Len after 1 Add = %d, want 1", b.Len())
	}

	evs := b.Events()
	if len(evs) != 1 {
		t.Fatalf("Events len = %d, want 1", len(evs))
	}
	if evs[0].Message != "hello" || evs[0].Category != CategoryAPI {
		t.Errorf("event mismatch: %+v", evs[0])
	}

	// mutation of returned slice must not affect the buffer
	evs[0].Message = "mutated"
	evs2 := b.Events()
	if evs2[0].Message != "hello" {
		t.Errorf("Events returned non-copy: got %q", evs2[0].Message)
	}

	b.Clear()
	if b.Len() != 0 {
		t.Errorf("Len after Clear = %d, want 0", b.Len())
	}
	if got := len(b.Events()); got != 0 {
		t.Errorf("Events len after Clear = %d, want 0", got)
	}
}

func TestDiagnosticBuffer_Eviction(t *testing.T) {
	const cap = 10
	b := NewDiagnosticBuffer(cap)

	// Overfill by 5: 15 events, first eviction drops floor(10/10)=1 at idx=10.
	for i := 0; i < 15; i++ {
		b.Add(DiagnosticEvent{Message: string(rune('a' + i))})
	}
	evs := b.Events()
	if len(evs) > cap {
		t.Errorf("buffer len %d exceeds cap %d", len(evs), cap)
	}
	// Oldest event ("a") should have been evicted.
	for _, e := range evs {
		if e.Message == "a" {
			t.Errorf("oldest event not evicted; buffer = %+v", evs)
			break
		}
	}
}

func TestDiagnosticBuffer_EvictionSmallCap(t *testing.T) {
	// maxSize < 10, so maxSize/10 = 0 and drop is forced to 1.
	b := NewDiagnosticBuffer(3)
	for i := 0; i < 5; i++ {
		b.Add(DiagnosticEvent{Message: string(rune('a' + i))})
	}
	if l := b.Len(); l == 0 || l > 3 {
		t.Errorf("Len = %d, want between 1 and 3", l)
	}
}

func TestDiagnosticBuffer_Concurrency(t *testing.T) {
	b := NewDiagnosticBuffer(10000)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				b.Add(DiagnosticEvent{Message: "x"})
				_ = b.Len()
				_ = b.Events()
			}
		}()
	}
	wg.Wait()
	if b.Len() != 20*50 {
		t.Errorf("concurrent Len = %d, want %d", b.Len(), 20*50)
	}
}

// --- DiagnosticTracker ---

func TestNewDiagnosticTracker(t *testing.T) {
	tr := NewDiagnosticTracker(nil)
	if tr == nil {
		t.Fatal("NewDiagnosticTracker returned nil")
	}
	if tr.buffer == nil {
		t.Error("tracker.buffer is nil")
	}
	if tr.baseline == nil {
		t.Error("tracker.baseline is nil")
	}
}

func TestDiagnosticTracker_Record(t *testing.T) {
	tr := NewDiagnosticTracker(nil)
	tr.Record(CategoryTool, SeverityWarning, "tool failed", map[string]any{"tool": "bash"})
	tr.Record(CategoryAPI, SeverityError, "api 500", nil)

	evs := tr.buffer.Events()
	if len(evs) != 2 {
		t.Fatalf("got %d events, want 2", len(evs))
	}
	if evs[0].Category != CategoryTool || evs[0].Severity != SeverityWarning {
		t.Errorf("first event = %+v", evs[0])
	}
	if evs[0].Message != "tool failed" {
		t.Errorf("first event message = %q", evs[0].Message)
	}
	if evs[0].Metadata["tool"] != "bash" {
		t.Errorf("first event metadata = %+v", evs[0].Metadata)
	}
	if evs[0].Timestamp.IsZero() {
		t.Error("first event has zero timestamp")
	}
}

func TestDiagnosticTracker_BaselineAndNewDiagnostics(t *testing.T) {
	tr := NewDiagnosticTracker(nil)

	baseDiag := Diagnostic{
		Message:  "unused import",
		Severity: SeverityWarning,
		Range:    DiagnosticRange{StartLine: 1, StartChar: 0, EndLine: 1, EndChar: 10},
		Source:   "gopls",
		Code:     "UNUSED",
	}
	newDiag := Diagnostic{
		Message:  "undefined: foo",
		Severity: SeverityError,
		Range:    DiagnosticRange{StartLine: 5, StartChar: 2, EndLine: 5, EndChar: 5},
		Source:   "gopls",
		Code:     "UNDEF",
	}

	file := "/tmp/test/file.go"

	// No baseline yet → all current considered new.
	out := tr.GetNewDiagnostics(file, []Diagnostic{baseDiag})
	if len(out) != 1 {
		t.Errorf("no baseline: got %d new, want 1", len(out))
	}

	tr.SetBaseline(file, []Diagnostic{baseDiag})

	// Only newDiag should be reported as new.
	out = tr.GetNewDiagnostics(file, []Diagnostic{baseDiag, newDiag})
	if len(out) != 1 {
		t.Fatalf("after baseline: got %d new, want 1", len(out))
	}
	if out[0].Message != "undefined: foo" {
		t.Errorf("unexpected new diagnostic: %+v", out[0])
	}

	// Nothing new when current matches baseline.
	out = tr.GetNewDiagnostics(file, []Diagnostic{baseDiag})
	if len(out) != 0 {
		t.Errorf("expected no new diags, got %+v", out)
	}
}

func TestDiagnosticTracker_BaselineNormalisesURIAndCase(t *testing.T) {
	tr := NewDiagnosticTracker(nil)
	d := Diagnostic{Message: "m", Severity: SeverityError}

	tr.SetBaseline("file:///Tmp/Project/MAIN.go", []Diagnostic{d})

	// Equivalent path with different casing and no scheme prefix → same baseline.
	got := tr.GetNewDiagnostics("/tmp/project/main.go", []Diagnostic{d})
	if len(got) != 0 {
		t.Errorf("path normalisation failed; got %d new, want 0", len(got))
	}

	// claude_fs prefix also stripped.
	got = tr.GetNewDiagnostics("_claude_fs_right:/tmp/project/main.go", []Diagnostic{d})
	if len(got) != 0 {
		t.Errorf("fs_right prefix not stripped; got %d new, want 0", len(got))
	}
}

func TestDiagnosticTracker_SetBaselineCopies(t *testing.T) {
	tr := NewDiagnosticTracker(nil)
	input := []Diagnostic{{Message: "orig", Severity: SeverityError}}
	tr.SetBaseline("/a.go", input)

	// Mutate caller slice; baseline should be unaffected.
	input[0].Message = "mutated"

	// Current contains only the mutated one → baseline still has "orig",
	// so "mutated" should be reported as new.
	out := tr.GetNewDiagnostics("/a.go", []Diagnostic{{Message: "mutated", Severity: SeverityError}})
	if len(out) != 1 {
		t.Errorf("baseline not copied on SetBaseline; got %+v", out)
	}
}

func TestDiagnosticTracker_Reset(t *testing.T) {
	tr := NewDiagnosticTracker(nil)
	tr.Record(CategoryAPI, SeverityInfo, "m", nil)
	tr.SetBaseline("/x.go", []Diagnostic{{Message: "m", Severity: SeverityError}})

	tr.Reset()

	if tr.buffer.Len() != 0 {
		t.Errorf("buffer not cleared after Reset: Len=%d", tr.buffer.Len())
	}
	d := Diagnostic{Message: "m", Severity: SeverityError}
	// After reset, baseline is gone → current returned as new.
	out := tr.GetNewDiagnostics("/x.go", []Diagnostic{d})
	if len(out) != 1 {
		t.Errorf("baseline not cleared after Reset; got %+v", out)
	}
}

func TestDiagnosticTracker_ExportDiagnostics(t *testing.T) {
	tr := NewDiagnosticTracker(nil)
	tr.Record(CategoryAPI, SeverityError, "boom", map[string]any{"status": 500})

	data, err := tr.ExportDiagnostics()
	if err != nil {
		t.Fatalf("ExportDiagnostics error: %v", err)
	}

	var decoded []DiagnosticEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal export: %v", err)
	}
	if len(decoded) != 1 {
		t.Fatalf("decoded len = %d, want 1", len(decoded))
	}
	if decoded[0].Message != "boom" || decoded[0].Category != CategoryAPI {
		t.Errorf("decoded event = %+v", decoded[0])
	}

	// Empty tracker exports an empty JSON array.
	tr2 := NewDiagnosticTracker(nil)
	data2, err := tr2.ExportDiagnostics()
	if err != nil {
		t.Fatalf("empty export error: %v", err)
	}
	var decoded2 []DiagnosticEvent
	if err := json.Unmarshal(data2, &decoded2); err != nil {
		t.Fatalf("unmarshal empty export: %v", err)
	}
	if len(decoded2) != 0 {
		t.Errorf("empty export decoded len = %d, want 0", len(decoded2))
	}
}

// --- FormatDiagnosticsSummary ---

func TestFormatDiagnosticsSummary_Empty(t *testing.T) {
	if got := FormatDiagnosticsSummary(nil); got != "" {
		t.Errorf("nil files: got %q, want empty", got)
	}
	if got := FormatDiagnosticsSummary([]DiagnosticFile{}); got != "" {
		t.Errorf("empty files: got %q, want empty", got)
	}
}

func TestFormatDiagnosticsSummary_Basic(t *testing.T) {
	files := []DiagnosticFile{
		{
			URI: "/home/user/project/main.go",
			Diagnostics: []Diagnostic{
				{
					Message:  "syntax error",
					Severity: SeverityError,
					Range:    DiagnosticRange{StartLine: 9, StartChar: 4, EndLine: 9, EndChar: 10},
					Source:   "gopls",
					Code:     "E001",
				},
				{
					Message:  "unused var",
					Severity: SeverityWarning,
					Range:    DiagnosticRange{StartLine: 3, StartChar: 0},
				},
			},
		},
		{
			URI: "/home/user/project/util.go",
			Diagnostics: []Diagnostic{
				{
					Message:  "deprecated",
					Severity: SeverityInfo,
					Range:    DiagnosticRange{StartLine: 0, StartChar: 0},
				},
			},
		},
	}

	out := FormatDiagnosticsSummary(files)

	// Uses basename, not full path.
	if !strings.Contains(out, "main.go:") {
		t.Errorf("missing main.go header:\n%s", out)
	}
	if !strings.Contains(out, "util.go:") {
		t.Errorf("missing util.go header:\n%s", out)
	}
	if strings.Contains(out, "/home/user") {
		t.Errorf("output should use basename only:\n%s", out)
	}

	// 1-based line and column conversion.
	if !strings.Contains(out, "[Line 10:5]") {
		t.Errorf("missing 1-based line/col:\n%s", out)
	}
	if !strings.Contains(out, "[Line 4:1]") {
		t.Errorf("missing warning line/col:\n%s", out)
	}

	// Severity symbols.
	if !strings.Contains(out, SeveritySymbol(SeverityError)) {
		t.Error("missing error symbol")
	}
	if !strings.Contains(out, SeveritySymbol(SeverityWarning)) {
		t.Error("missing warning symbol")
	}

	// Code and source annotations on first diagnostic only.
	if !strings.Contains(out, "[E001]") {
		t.Errorf("missing code tag:\n%s", out)
	}
	if !strings.Contains(out, "(gopls)") {
		t.Errorf("missing source tag:\n%s", out)
	}

	// Messages present.
	if !strings.Contains(out, "syntax error") || !strings.Contains(out, "unused var") || !strings.Contains(out, "deprecated") {
		t.Errorf("missing messages:\n%s", out)
	}
}

func TestFormatDiagnosticsSummary_Truncation(t *testing.T) {
	longMsg := strings.Repeat("A", 500)
	var diags []Diagnostic
	for i := 0; i < 50; i++ {
		diags = append(diags, Diagnostic{
			Message:  longMsg,
			Severity: SeverityError,
			Range:    DiagnosticRange{StartLine: i, StartChar: 0},
		})
	}
	out := FormatDiagnosticsSummary([]DiagnosticFile{{URI: "big.go", Diagnostics: diags}})

	if len(out) > 4000 {
		t.Errorf("output len = %d, exceeds 4000", len(out))
	}
	if !strings.HasSuffix(out, "…[truncated]") {
		t.Errorf("expected truncation marker, got suffix %q", out[max(0, len(out)-40):])
	}
}

// --- LogForDiagnosticsNoPII ---

func TestLogForDiagnosticsNoPII_NoEnv(t *testing.T) {
	t.Setenv("POLYPOD_DIAGNOSTICS_FILE", "")
	// Must be a no-op and not panic.
	LogForDiagnosticsNoPII(DiagLogInfo, "evt", map[string]any{"a": 1})
}

func TestLogForDiagnosticsNoPII_WritesFile(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "sub", "diag.log")
	t.Setenv("POLYPOD_DIAGNOSTICS_FILE", logFile)

	LogForDiagnosticsNoPII(DiagLogInfo, "first_event", map[string]any{"n": 1})
	LogForDiagnosticsNoPII(DiagLogWarn, "second_event", nil)
	LogForDiagnosticsNoPII(DiagLogError, "third_event", map[string]any{"err": "oops"})
	LogForDiagnosticsNoPII(DiagLogDebug, "fourth_event", map[string]any{})

	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}

	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4:\n%s", len(lines), content)
	}

	var first diagLogEntry
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("unmarshal first line: %v", err)
	}
	if first.Event != "first_event" {
		t.Errorf("first.Event = %q, want first_event", first.Event)
	}
	if first.Level != DiagLogInfo {
		t.Errorf("first.Level = %q, want info", first.Level)
	}
	if first.Data["n"] == nil {
		t.Errorf("first.Data missing 'n': %+v", first.Data)
	}
	if _, err := time.Parse(time.RFC3339Nano, first.Timestamp); err != nil {
		t.Errorf("first.Timestamp %q not RFC3339Nano: %v", first.Timestamp, err)
	}

	// nil data becomes {} and still round-trips.
	var second diagLogEntry
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("unmarshal second line: %v", err)
	}
	if second.Event != "second_event" || second.Level != DiagLogWarn {
		t.Errorf("second entry wrong: %+v", second)
	}
	if second.Data == nil {
		t.Error("nil data should be serialised as {} not null")
	}
}

func TestLogForDiagnosticsNoPII_AppendsExisting(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "diag.log")
	t.Setenv("POLYPOD_DIAGNOSTICS_FILE", logFile)

	LogForDiagnosticsNoPII(DiagLogInfo, "one", nil)
	LogForDiagnosticsNoPII(DiagLogInfo, "two", nil)
	LogForDiagnosticsNoPII(DiagLogInfo, "three", nil)

	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if n := strings.Count(string(content), "\n"); n != 3 {
		t.Errorf("line count = %d, want 3", n)
	}
	if !strings.Contains(string(content), `"one"`) ||
		!strings.Contains(string(content), `"two"`) ||
		!strings.Contains(string(content), `"three"`) {
		t.Errorf("missing events in log:\n%s", content)
	}
}

// --- WithDiagnosticsTiming ---

func TestWithDiagnosticsTiming_Success(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "timing.log")
	t.Setenv("POLYPOD_DIAGNOSTICS_FILE", logFile)

	result, err := WithDiagnosticsTiming("my_op", func() (int, error) {
		time.Sleep(2 * time.Millisecond)
		return 42, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 42 {
		t.Errorf("result = %d, want 42", result)
	}

	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	s := string(content)
	if !strings.Contains(s, "my_op_started") {
		t.Errorf("missing started event:\n%s", s)
	}
	if !strings.Contains(s, "my_op_completed") {
		t.Errorf("missing completed event:\n%s", s)
	}
	if strings.Contains(s, "my_op_failed") {
		t.Errorf("unexpected failed event:\n%s", s)
	}
	if !strings.Contains(s, "duration_ms") {
		t.Errorf("missing duration_ms:\n%s", s)
	}
}

func TestWithDiagnosticsTiming_Error(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "timing_err.log")
	t.Setenv("POLYPOD_DIAGNOSTICS_FILE", logFile)

	sentinel := &stringErr{"boom"}
	result, err := WithDiagnosticsTiming("bad_op", func() (string, error) {
		return "partial", sentinel
	})
	if err != sentinel {
		t.Errorf("err = %v, want sentinel", err)
	}
	if result != "partial" {
		t.Errorf("result = %q, want partial", result)
	}

	content, _ := os.ReadFile(logFile)
	s := string(content)
	if !strings.Contains(s, "bad_op_started") {
		t.Errorf("missing started:\n%s", s)
	}
	if !strings.Contains(s, "bad_op_failed") {
		t.Errorf("missing failed:\n%s", s)
	}
	if strings.Contains(s, "bad_op_completed") {
		t.Errorf("unexpected completed:\n%s", s)
	}
}

type stringErr struct{ s string }

func (e *stringErr) Error() string { return e.s }

// --- DebugFilter / ParseDebugFilter ---

func TestParseDebugFilter(t *testing.T) {
	if ParseDebugFilter("") != nil {
		t.Error("empty filter should be nil")
	}
	if ParseDebugFilter("   ") != nil {
		t.Error("whitespace filter should be nil")
	}
	if ParseDebugFilter(",,,") != nil {
		t.Error("comma-only filter should be nil")
	}

	f := ParseDebugFilter("api,tool,HOOK")
	if f == nil {
		t.Fatal("include filter returned nil")
	}
	if f.IsExclusive {
		t.Error("include filter marked exclusive")
	}
	wantIncl := []string{"api", "tool", "hook"}
	if len(f.Include) != len(wantIncl) {
		t.Fatalf("Include = %v, want %v", f.Include, wantIncl)
	}
	for i, v := range wantIncl {
		if f.Include[i] != v {
			t.Errorf("Include[%d] = %q, want %q", i, f.Include[i], v)
		}
	}

	f = ParseDebugFilter("!mcp,!file")
	if f == nil {
		t.Fatal("exclude filter returned nil")
	}
	if !f.IsExclusive {
		t.Error("exclude filter not marked exclusive")
	}
	if len(f.Exclude) != 2 || f.Exclude[0] != "mcp" || f.Exclude[1] != "file" {
		t.Errorf("Exclude = %v", f.Exclude)
	}

	// Mixed → nil (show all).
	if ParseDebugFilter("api,!mcp") != nil {
		t.Error("mixed filter should be nil")
	}
}

// --- ExtractDebugCategories ---

func TestExtractDebugCategories(t *testing.T) {
	cats := ExtractDebugCategories(`MCP server "weather": fetching`)
	if !containsString(cats, "mcp") || !containsString(cats, "weather") {
		t.Errorf("mcp extraction = %v", cats)
	}

	cats = ExtractDebugCategories("api: request sent")
	if !containsString(cats, "api") {
		t.Errorf("prefix extraction = %v", cats)
	}

	cats = ExtractDebugCategories("[TOOL] running bash")
	if !containsString(cats, "tool") {
		t.Errorf("bracket extraction = %v", cats)
	}

	cats = ExtractDebugCategories("1P event: thing happened")
	if !containsString(cats, "1p") {
		t.Errorf("1p extraction = %v", cats)
	}

	// Plain message → no categories.
	cats = ExtractDebugCategories("just a message without markers")
	if len(cats) != 0 {
		t.Errorf("plain message categories = %v", cats)
	}
}

func containsString(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}

// --- ShouldShowDebugMessage / ShouldShowDebugCategories ---

func TestShouldShowDebugMessage(t *testing.T) {
	// nil filter → always true.
	if !ShouldShowDebugMessage("anything", nil) {
		t.Error("nil filter should allow all messages")
	}

	f := ParseDebugFilter("api")
	if !ShouldShowDebugMessage("api: boom", f) {
		t.Error("api message should pass api filter")
	}
	if ShouldShowDebugMessage("tool: running", f) {
		t.Error("tool message should not pass api-only filter")
	}
	// Message with no extractable categories on include filter → false.
	if ShouldShowDebugMessage("no markers here", f) {
		t.Error("uncategorised message should not pass include filter")
	}

	f = ParseDebugFilter("!mcp")
	if ShouldShowDebugMessage(`MCP server "x": up`, f) {
		t.Error("mcp message should be excluded")
	}
	if !ShouldShowDebugMessage("api: ok", f) {
		t.Error("api message should pass !mcp filter")
	}
}

func TestShouldShowDebugCategories(t *testing.T) {
	if !ShouldShowDebugCategories([]string{"api"}, nil) {
		t.Error("nil filter should allow all")
	}

	f := ParseDebugFilter("api,tool")
	if !ShouldShowDebugCategories([]string{"tool"}, f) {
		t.Error("tool should pass include [api,tool]")
	}
	if ShouldShowDebugCategories([]string{"mcp"}, f) {
		t.Error("mcp should not pass include [api,tool]")
	}
	if ShouldShowDebugCategories(nil, f) {
		t.Error("empty categories should not pass include filter")
	}

	f = ParseDebugFilter("!mcp")
	if !ShouldShowDebugCategories([]string{"api"}, f) {
		t.Error("api should pass !mcp filter")
	}
	if ShouldShowDebugCategories([]string{"api", "mcp"}, f) {
		t.Error("any category matching exclude should block entire message")
	}
}

// --- DebugLogger ---

func TestNewDebugLogger_Defaults(t *testing.T) {
	dl := NewDebugLogger(0, nil)
	if dl == nil {
		t.Fatal("NewDebugLogger returned nil")
	}
	if dl.maxEntries != 5000 {
		t.Errorf("default maxEntries = %d, want 5000", dl.maxEntries)
	}

	dl2 := NewDebugLogger(-3, nil)
	if dl2.maxEntries != 5000 {
		t.Errorf("negative maxEntries default = %d, want 5000", dl2.maxEntries)
	}

	dl3 := NewDebugLogger(100, nil)
	if dl3.maxEntries != 100 {
		t.Errorf("maxEntries = %d, want 100", dl3.maxEntries)
	}
}

func TestDebugLogger_LogAndLogf(t *testing.T) {
	dl := NewDebugLogger(10, nil)
	dl.Log(LogCategoryAPI, "first")
	dl.Logf(LogCategoryTool, "count=%d name=%s", 3, "bash")

	entries := dl.Entries()
	if len(entries) != 2 {
		t.Fatalf("entries len = %d, want 2", len(entries))
	}
	if entries[0].Category != LogCategoryAPI || entries[0].Message != "first" {
		t.Errorf("entry[0] = %+v", entries[0])
	}
	if entries[1].Category != LogCategoryTool || entries[1].Message != "count=3 name=bash" {
		t.Errorf("entry[1] = %+v", entries[1])
	}
	if entries[0].Timestamp.IsZero() {
		t.Error("entry[0] has zero timestamp")
	}

	// Ensure returned slice is a copy.
	entries[0].Message = "hacked"
	if dl.Entries()[0].Message == "hacked" {
		t.Error("Entries returned non-copy")
	}
}

func TestDebugLogger_Eviction(t *testing.T) {
	dl := NewDebugLogger(10, nil)
	for i := 0; i < 15; i++ {
		dl.Logf(LogCategoryAPI, "msg-%d", i)
	}
	entries := dl.Entries()
	if len(entries) > 10 {
		t.Errorf("entries len %d exceeds max 10", len(entries))
	}
	for _, e := range entries {
		if e.Message == "msg-0" {
			t.Error("oldest entry not evicted")
			break
		}
	}
}

func TestDebugLogger_ClearAndDebugDump(t *testing.T) {
	dl := NewDebugLogger(10, nil)
	dl.Log(LogCategoryAPI, "hello")
	dl.Log(LogCategoryTool, "world")

	data, err := dl.DebugDump()
	if err != nil {
		t.Fatalf("DebugDump error: %v", err)
	}
	var decoded []DebugLogEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal dump: %v", err)
	}
	if len(decoded) != 2 {
		t.Errorf("decoded len = %d, want 2", len(decoded))
	}

	dl.Clear()
	if len(dl.Entries()) != 0 {
		t.Errorf("entries after Clear = %d, want 0", len(dl.Entries()))
	}

	data, err = dl.DebugDump()
	if err != nil {
		t.Fatalf("DebugDump after Clear error: %v", err)
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal empty dump: %v", err)
	}
	if len(decoded) != 0 {
		t.Errorf("empty dump decoded len = %d, want 0", len(decoded))
	}
}

func TestDebugLogger_SetDebugFilterBuffersAll(t *testing.T) {
	dl := NewDebugLogger(10, nil)
	dl.SetDebugFilter("api")

	// All messages are buffered even when filter blocks slog output.
	dl.Log(LogCategoryAPI, "api: ok")
	dl.Log(LogCategoryMCP, "mcp: ping")
	dl.Log(LogCategoryTool, "plain message")

	if len(dl.Entries()) != 3 {
		t.Errorf("buffered entries = %d, want 3", len(dl.Entries()))
	}
}

func TestDebugLogger_Concurrency(t *testing.T) {
	dl := NewDebugLogger(10000, nil)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				dl.Logf(LogCategoryAPI, "g%d-%d", id, j)
				_ = dl.Entries()
			}
		}(i)
	}
	wg.Wait()
	if n := len(dl.Entries()); n != 1000 {
		t.Errorf("concurrent entries = %d, want 1000", n)
	}
}

// --- Diagnostic struct / JSON round-trips ---

func TestDiagnosticJSONRoundTrip(t *testing.T) {
	d := Diagnostic{
		Message:  "oops",
		Severity: SeverityError,
		Range:    DiagnosticRange{StartLine: 1, StartChar: 2, EndLine: 3, EndChar: 4},
		Source:   "gopls",
		Code:     "E001",
	}
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Diagnostic
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != d {
		t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", got, d)
	}
}

func TestDiagnosticFileJSONRoundTrip(t *testing.T) {
	df := DiagnosticFile{
		URI: "/x/y/z.go",
		Diagnostics: []Diagnostic{
			{Message: "a", Severity: SeverityWarning, Range: DiagnosticRange{StartLine: 0}},
			{Message: "b", Severity: SeverityInfo, Range: DiagnosticRange{StartLine: 5}},
		},
	}
	data, err := json.Marshal(df)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got DiagnosticFile
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.URI != df.URI || len(got.Diagnostics) != len(df.Diagnostics) {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
