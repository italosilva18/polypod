package debug

import (
	"strings"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	s := New()
	if s == nil {
		t.Fatal("New() returned nil")
	}
	if s.Enabled {
		t.Error("expected Enabled to be false by default")
	}
	if s.Categories == nil {
		t.Error("expected Categories map to be initialized")
	}
	if s.MaxBuffer != 200 {
		t.Errorf("expected MaxBuffer 200, got %d", s.MaxBuffer)
	}
	if len(s.LogBuffer) != 0 {
		t.Errorf("expected empty LogBuffer, got %d entries", len(s.LogBuffer))
	}
}

func TestEnableAllCategories(t *testing.T) {
	s := New()
	msg := s.Enable("")
	if !s.Enabled {
		t.Error("expected Enabled to be true after Enable(\"\")")
	}
	if s.Categories != nil {
		t.Error("expected Categories to be nil when enabling all")
	}
	if !strings.Contains(msg, "todas as categorias") {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestEnableWithIncludeCategories(t *testing.T) {
	s := New()
	msg := s.Enable("api,tools")
	if !s.Enabled {
		t.Error("expected Enabled to be true")
	}
	if s.Categories == nil {
		t.Fatal("expected Categories to be set")
	}
	if v, ok := s.Categories["api"]; !ok || !v {
		t.Error("expected 'api' category to be included")
	}
	if v, ok := s.Categories["tools"]; !ok || !v {
		t.Error("expected 'tools' category to be included")
	}
	if !strings.Contains(msg, "api,tools") {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestEnableWithExcludeCategories(t *testing.T) {
	s := New()
	s.Enable("!statsig,!telemetry")
	if v, ok := s.Categories["statsig"]; !ok || v {
		t.Error("expected 'statsig' to be excluded (false)")
	}
	if v, ok := s.Categories["telemetry"]; !ok || v {
		t.Error("expected 'telemetry' to be excluded (false)")
	}
}

func TestEnableWithWhitespace(t *testing.T) {
	s := New()
	s.Enable(" api , tools ")
	if v, ok := s.Categories["api"]; !ok || !v {
		t.Error("expected 'api' category to be included after trimming")
	}
	if v, ok := s.Categories["tools"]; !ok || !v {
		t.Error("expected 'tools' category to be included after trimming")
	}
}

func TestDisable(t *testing.T) {
	s := New()
	s.Enable("")
	if !s.Enabled {
		t.Fatal("expected Enabled true after Enable")
	}
	s.Disable()
	if s.Enabled {
		t.Error("expected Enabled to be false after Disable")
	}
}

func TestLogWhenDisabled(t *testing.T) {
	s := New()
	s.Log("api", "test message", nil)
	if len(s.LogBuffer) != 0 {
		t.Errorf("expected no logs when disabled, got %d", len(s.LogBuffer))
	}
}

func TestLogWhenEnabled(t *testing.T) {
	s := New()
	s.Enable("")
	s.Log("api", "hello", map[string]int{"x": 1})
	if len(s.LogBuffer) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(s.LogBuffer))
	}
	entry := s.LogBuffer[0]
	if entry.Category != "api" {
		t.Errorf("expected category 'api', got %q", entry.Category)
	}
	if entry.Message != "hello" {
		t.Errorf("expected message 'hello', got %q", entry.Message)
	}
	if entry.Data == nil {
		t.Error("expected data to be set")
	}
	if entry.Timestamp.IsZero() {
		t.Error("expected timestamp to be set")
	}
}

func TestLogCategoryFilterInclude(t *testing.T) {
	s := New()
	s.Enable("api")
	s.Log("api", "visible", nil)
	s.Log("tools", "hidden", nil)
	if len(s.LogBuffer) != 1 {
		t.Fatalf("expected 1 log, got %d", len(s.LogBuffer))
	}
	if s.LogBuffer[0].Category != "api" {
		t.Errorf("expected 'api' category, got %q", s.LogBuffer[0].Category)
	}
}

func TestLogCategoryFilterExclude(t *testing.T) {
	s := New()
	s.Enable("!statsig")
	s.Log("api", "visible", nil)
	s.Log("statsig", "hidden", nil)
	s.Log("tools", "visible", nil)
	if len(s.LogBuffer) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(s.LogBuffer))
	}
	for _, e := range s.LogBuffer {
		if e.Category == "statsig" {
			t.Error("statsig category should have been excluded")
		}
	}
}

func TestLogBufferMaxSize(t *testing.T) {
	s := New()
	s.MaxBuffer = 3
	s.Enable("")
	s.Log("api", "msg1", nil)
	s.Log("api", "msg2", nil)
	s.Log("api", "msg3", nil)
	s.Log("api", "msg4", nil)
	if len(s.LogBuffer) != 3 {
		t.Fatalf("expected buffer capped at 3, got %d", len(s.LogBuffer))
	}
	// Oldest (msg1) should be dropped
	if s.LogBuffer[0].Message != "msg2" {
		t.Errorf("expected oldest to be 'msg2', got %q", s.LogBuffer[0].Message)
	}
	if s.LogBuffer[2].Message != "msg4" {
		t.Errorf("expected newest to be 'msg4', got %q", s.LogBuffer[2].Message)
	}
}

func TestFormatSystemPrompt(t *testing.T) {
	out := FormatSystemPrompt("You are helpful.", 5, 2, 1500)
	if !strings.Contains(out, "System Prompt Debug") {
		t.Error("expected header in output")
	}
	if !strings.Contains(out, "**Tools**: 5") {
		t.Error("expected tool count in output")
	}
	if !strings.Contains(out, "**Memorias**: 2") {
		t.Error("expected memory count in output")
	}
	if !strings.Contains(out, "1500 tokens") {
		t.Error("expected context tokens in output")
	}
	if !strings.Contains(out, "You are helpful.") {
		t.Error("expected prompt content in output")
	}
}

func TestFormatSystemPromptTruncation(t *testing.T) {
	longPrompt := strings.Repeat("a", 5000)
	out := FormatSystemPrompt(longPrompt, 0, 0, 0)
	if !strings.Contains(out, "[truncado") {
		t.Error("expected truncation marker for long prompt")
	}
	if !strings.Contains(out, "5000 chars total") {
		t.Error("expected original length in truncation marker")
	}
}

func TestFormatAPIRequest(t *testing.T) {
	out := FormatAPIRequest("gpt-4", 10, 5, 0.7)
	expected := "[API] model=gpt-4 messages=10 tools=5 temp=0.70"
	if out != expected {
		t.Errorf("expected %q, got %q", expected, out)
	}
}

func TestFormatAPIResponseNotCached(t *testing.T) {
	out := FormatAPIResponse(100, 50, 250*time.Millisecond, false)
	if !strings.Contains(out, "tokens=100+50") {
		t.Errorf("expected token counts, got %q", out)
	}
	if !strings.Contains(out, "duration=250ms") {
		t.Errorf("expected duration, got %q", out)
	}
	if strings.Contains(out, "cached") {
		t.Errorf("did not expect 'cached' marker, got %q", out)
	}
}

func TestFormatAPIResponseCached(t *testing.T) {
	out := FormatAPIResponse(100, 50, 250*time.Millisecond, true)
	if !strings.Contains(out, "(cached)") {
		t.Errorf("expected '(cached)' marker, got %q", out)
	}
}

func TestDumpLogsEmpty(t *testing.T) {
	s := New()
	out := s.DumpLogs()
	if out != "Nenhum log de debug." {
		t.Errorf("unexpected empty dump output: %q", out)
	}
}

func TestDumpLogsWithEntries(t *testing.T) {
	s := New()
	s.Enable("")
	s.Log("api", "request sent", map[string]string{"endpoint": "/chat"})
	s.Log("tools", "tool invoked", nil)

	out := s.DumpLogs()
	if !strings.Contains(out, "Debug Logs (2 entradas)") {
		t.Errorf("expected header with count, got: %s", out)
	}
	if !strings.Contains(out, "request sent") {
		t.Error("expected first log message in dump")
	}
	if !strings.Contains(out, "tool invoked") {
		t.Error("expected second log message in dump")
	}
	if !strings.Contains(out, "**api**") {
		t.Error("expected api category label")
	}
	if !strings.Contains(out, "**tools**") {
		t.Error("expected tools category label")
	}
	if !strings.Contains(out, "/chat") {
		t.Error("expected serialized data in dump")
	}
}

func TestDumpLogsDataTruncation(t *testing.T) {
	s := New()
	s.Enable("")
	bigData := strings.Repeat("x", 500)
	s.Log("api", "big", bigData)
	out := s.DumpLogs()
	if !strings.Contains(out, "...") {
		t.Error("expected truncation marker for large data")
	}
}

func TestShouldLogNoFilter(t *testing.T) {
	s := New()
	s.Categories = nil
	if !s.shouldLog("anything") {
		t.Error("expected shouldLog to return true when Categories is nil")
	}
}

func TestShouldLogOnlyIncludesDefaultExclude(t *testing.T) {
	s := New()
	s.Enable("api")
	// api is explicitly included
	if !s.shouldLog("api") {
		t.Error("expected 'api' to be logged")
	}
	// unknown category should default to excluded when only includes exist
	if s.shouldLog("unknown") {
		t.Error("expected 'unknown' to NOT be logged when only include rules exist")
	}
}

func TestShouldLogOnlyExcludesDefaultInclude(t *testing.T) {
	s := New()
	s.Enable("!statsig")
	// statsig explicitly excluded
	if s.shouldLog("statsig") {
		t.Error("expected 'statsig' to NOT be logged")
	}
	// unknown category should default to included when only excludes exist
	if !s.shouldLog("unknown") {
		t.Error("expected 'unknown' to be logged when only exclude rules exist")
	}
}
