package tracking

import (
	"log/slog"
	"os"
	"strings"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}

func TestNewCostEngine_NilLogger(t *testing.T) {
	e := NewCostEngine(nil)
	if e == nil {
		t.Fatal("NewCostEngine(nil) returned nil")
	}
	if e.SessionCost() != 0 {
		t.Errorf("fresh SessionCost = %v; want 0", e.SessionCost())
	}
	if e.SessionRequests() != 0 {
		t.Errorf("fresh SessionRequests = %d; want 0", e.SessionRequests())
	}
	// Recording with nil logger must not panic.
	e.RecordCompletion("gpt-4o", 100, 50, 0, 0)
	if e.SessionCost() <= 0 {
		t.Error("expected cost > 0 after recording with nil logger")
	}
}

func TestNewCostEngine_InitialState(t *testing.T) {
	e := NewCostEngine(testLogger())

	if e.SessionCost() != 0 {
		t.Errorf("empty SessionCost = %v; want 0", e.SessionCost())
	}

	prompt, completion, total := e.SessionTokens()
	if prompt != 0 || completion != 0 || total != 0 {
		t.Errorf("tokens = %d/%d/%d; want 0/0/0", prompt, completion, total)
	}

	if e.SessionRequests() != 0 {
		t.Errorf("requests = %d; want 0", e.SessionRequests())
	}

	if len(e.ModelBreakdown()) != 0 {
		t.Errorf("ModelBreakdown len = %d; want 0", len(e.ModelBreakdown()))
	}
	if len(e.ToolBreakdown()) != 0 {
		t.Errorf("ToolBreakdown len = %d; want 0", len(e.ToolBreakdown()))
	}
}

func TestCostEngine_RecordCompletion_Single(t *testing.T) {
	e := NewCostEngine(testLogger())

	e.RecordCompletion("claude-sonnet-4-20250514", 1000, 500, 0, 0)

	prompt, completion, total := e.SessionTokens()
	if prompt != 1000 {
		t.Errorf("prompt = %d; want 1000", prompt)
	}
	if completion != 500 {
		t.Errorf("completion = %d; want 500", completion)
	}
	if total != 1500 {
		t.Errorf("total = %d; want 1500", total)
	}

	cost := e.SessionCost()
	if cost <= 0 {
		t.Errorf("cost = %v; want > 0", cost)
	}

	if e.SessionRequests() != 1 {
		t.Errorf("requests = %d; want 1", e.SessionRequests())
	}
}

func TestCostEngine_RecordCompletion_Multiple(t *testing.T) {
	e := NewCostEngine(testLogger())

	e.RecordCompletion("gpt-4o-mini", 100, 50, 0, 0)
	e.RecordCompletion("gpt-4o-mini", 200, 100, 0, 0)
	e.RecordCompletion("gpt-4o-mini", 300, 150, 0, 0)

	prompt, completion, total := e.SessionTokens()
	if prompt != 600 {
		t.Errorf("prompt = %d; want 600", prompt)
	}
	if completion != 300 {
		t.Errorf("completion = %d; want 300", completion)
	}
	if total != 900 {
		t.Errorf("total = %d; want 900", total)
	}

	if e.SessionRequests() != 3 {
		t.Errorf("requests = %d; want 3", e.SessionRequests())
	}
}

func TestCostEngine_RecordCompletion_ZeroTokens(t *testing.T) {
	e := NewCostEngine(testLogger())

	// Zero tokens should be a "no-op" in the sense of adding nothing to totals.
	e.RecordCompletion("claude-sonnet-4-20250514", 0, 0, 0, 0)

	prompt, completion, _ := e.SessionTokens()
	if prompt != 0 || completion != 0 {
		t.Errorf("tokens after zero-record = %d/%d; want 0/0", prompt, completion)
	}
	if e.SessionCost() != 0 {
		t.Errorf("cost after zero-record = %v; want 0", e.SessionCost())
	}
}

func TestCostEngine_Pricing_KnownModel(t *testing.T) {
	e := NewCostEngine(testLogger())

	// claude-sonnet-4 prefix matches "claude-sonnet-4" → $3/M input, $15/M output
	// 1000 in + 500 out → 0.003 + 0.0075 = 0.0105
	e.RecordCompletion("claude-sonnet-4-20250514", 1000, 500, 0, 0)

	cost := e.SessionCost()
	if cost <= 0 {
		t.Errorf("known model cost = %v; want > 0", cost)
	}

	expected := 0.0105
	epsilon := 1e-9
	if cost < expected-epsilon || cost > expected+epsilon {
		t.Errorf("cost = %v; want ~%v", cost, expected)
	}
}

func TestCostEngine_Pricing_UnknownModel(t *testing.T) {
	e := NewCostEngine(testLogger())

	e.RecordCompletion("totally-made-up-model-xyz", 1000, 500, 0, 0)

	// Unknown model falls back to zero cost.
	if e.SessionCost() != 0 {
		t.Errorf("unknown model cost = %v; want 0", e.SessionCost())
	}

	// But tokens and request count should still be recorded.
	prompt, completion, _ := e.SessionTokens()
	if prompt != 1000 || completion != 500 {
		t.Errorf("tokens = %d/%d; want 1000/500", prompt, completion)
	}
	if e.SessionRequests() != 1 {
		t.Errorf("requests = %d; want 1", e.SessionRequests())
	}
}

func TestCostEngine_ModelBreakdown(t *testing.T) {
	e := NewCostEngine(testLogger())

	e.RecordCompletion("gpt-4o", 1000, 500, 0, 0)
	e.RecordCompletion("claude-sonnet-4-20250514", 2000, 1000, 0, 0)

	breakdown := e.ModelBreakdown()
	if len(breakdown) != 2 {
		t.Fatalf("ModelBreakdown len = %d; want 2", len(breakdown))
	}

	gpt, ok := breakdown["gpt-4o"]
	if !ok {
		t.Fatal("gpt-4o missing from breakdown")
	}
	if gpt.PromptTokens != 1000 {
		t.Errorf("gpt-4o prompt = %d; want 1000", gpt.PromptTokens)
	}
	if gpt.CompletionTokens != 500 {
		t.Errorf("gpt-4o completion = %d; want 500", gpt.CompletionTokens)
	}
	if gpt.TotalTokens != 1500 {
		t.Errorf("gpt-4o total = %d; want 1500", gpt.TotalTokens)
	}
	if gpt.Requests != 1 {
		t.Errorf("gpt-4o requests = %d; want 1", gpt.Requests)
	}
	if gpt.TotalCostUSD <= 0 {
		t.Errorf("gpt-4o cost = %v; want > 0", gpt.TotalCostUSD)
	}

	claude, ok := breakdown["claude-sonnet-4-20250514"]
	if !ok {
		t.Fatal("claude-sonnet-4-20250514 missing from breakdown")
	}
	if claude.PromptTokens != 2000 {
		t.Errorf("claude prompt = %d; want 2000", claude.PromptTokens)
	}
	if claude.CompletionTokens != 1000 {
		t.Errorf("claude completion = %d; want 1000", claude.CompletionTokens)
	}
	if claude.TotalCostUSD <= 0 {
		t.Errorf("claude cost = %v; want > 0", claude.TotalCostUSD)
	}
}

func TestCostEngine_ModelBreakdown_IsCopy(t *testing.T) {
	e := NewCostEngine(testLogger())
	e.RecordCompletion("gpt-4o", 100, 50, 0, 0)

	b1 := e.ModelBreakdown()
	mc := b1["gpt-4o"]
	mc.PromptTokens = 99999
	b1["gpt-4o"] = mc

	b2 := e.ModelBreakdown()
	if b2["gpt-4o"].PromptTokens != 100 {
		t.Errorf("internal state mutated via breakdown: got %d; want 100",
			b2["gpt-4o"].PromptTokens)
	}
}

func TestCostEngine_ToolBreakdown(t *testing.T) {
	e := NewCostEngine(testLogger())

	// Seed session totals so tool cost estimation has data to work with.
	e.RecordCompletion("gpt-4o", 1000, 500, 0, 0)

	e.RecordToolUse("read_file", 200, 100)
	e.RecordToolUse("read_file", 300, 150)
	e.RecordToolUse("write_file", 100, 50)

	tools := e.ToolBreakdown()
	if len(tools) != 2 {
		t.Fatalf("ToolBreakdown len = %d; want 2", len(tools))
	}

	rf, ok := tools["read_file"]
	if !ok {
		t.Fatal("read_file missing")
	}
	if rf.Invocations != 2 {
		t.Errorf("read_file invocations = %d; want 2", rf.Invocations)
	}
	if rf.PromptTokens != 500 {
		t.Errorf("read_file prompt = %d; want 500", rf.PromptTokens)
	}
	if rf.CompletionTokens != 250 {
		t.Errorf("read_file completion = %d; want 250", rf.CompletionTokens)
	}
	if rf.TotalTokens != 750 {
		t.Errorf("read_file total = %d; want 750", rf.TotalTokens)
	}

	wf, ok := tools["write_file"]
	if !ok {
		t.Fatal("write_file missing")
	}
	if wf.Invocations != 1 {
		t.Errorf("write_file invocations = %d; want 1", wf.Invocations)
	}
}

func TestCostEngine_SessionStats(t *testing.T) {
	e := NewCostEngine(testLogger())

	e.RecordCompletion("gpt-4o", 1000, 500, 0, 0)
	e.RecordCompletion("gpt-4o-mini", 2000, 1000, 0, 0)

	// SessionCost
	if e.SessionCost() <= 0 {
		t.Errorf("SessionCost = %v; want > 0", e.SessionCost())
	}

	// SessionTokens: prompt, completion, total
	prompt, completion, total := e.SessionTokens()
	if prompt != 3000 {
		t.Errorf("prompt = %d; want 3000", prompt)
	}
	if completion != 1500 {
		t.Errorf("completion = %d; want 1500", completion)
	}
	if total != 4500 {
		t.Errorf("total = %d; want 4500", total)
	}

	// SessionRequests
	if e.SessionRequests() != 2 {
		t.Errorf("requests = %d; want 2", e.SessionRequests())
	}
}

func TestCostEngine_FormatCostSummary(t *testing.T) {
	e := NewCostEngine(testLogger())

	// Empty summary should still be non-empty string.
	s := e.FormatCostSummary()
	if s == "" {
		t.Error("FormatCostSummary empty string for zero state")
	}

	e.RecordCompletion("gpt-4o", 1000, 500, 0, 0)
	s = e.FormatCostSummary()
	if s == "" {
		t.Error("FormatCostSummary returned empty")
	}
	// Should mention USD and tokens.
	if !strings.Contains(s, "USD") {
		t.Errorf("summary missing 'USD': %q", s)
	}
	if !strings.Contains(s, "tokens") {
		t.Errorf("summary missing 'tokens': %q", s)
	}
}

func TestCostEngine_FormatDetailedReport(t *testing.T) {
	e := NewCostEngine(testLogger())

	e.RecordCompletion("gpt-4o", 1000, 500, 0, 0)
	e.RecordCompletion("claude-sonnet-4-20250514", 2000, 1000, 100, 50)
	e.RecordToolUse("read_file", 200, 100)

	report := e.FormatDetailedReport()
	if report == "" {
		t.Fatal("FormatDetailedReport empty")
	}

	// Should be multi-line.
	lines := strings.Split(report, "\n")
	if len(lines) < 5 {
		t.Errorf("report has %d lines; want >= 5", len(lines))
	}

	// Should contain section headers and model names.
	mustContain := []string{"Custo", "gpt-4o", "claude-sonnet-4-20250514", "Por modelo", "Por ferramenta", "read_file"}
	for _, s := range mustContain {
		if !strings.Contains(report, s) {
			t.Errorf("report missing %q", s)
		}
	}
}

func TestCostEngine_Reset(t *testing.T) {
	e := NewCostEngine(testLogger())

	e.RecordCompletion("gpt-4o", 1000, 500, 0, 0)
	e.RecordCompletion("claude-sonnet-4-20250514", 2000, 1000, 0, 0)
	e.RecordToolUse("read_file", 100, 50)

	if e.SessionCost() == 0 {
		t.Fatal("pre-reset cost is zero; test setup broken")
	}

	e.Reset()

	if e.SessionCost() != 0 {
		t.Errorf("SessionCost after Reset = %v; want 0", e.SessionCost())
	}
	prompt, completion, total := e.SessionTokens()
	if prompt != 0 || completion != 0 || total != 0 {
		t.Errorf("tokens after Reset = %d/%d/%d; want 0/0/0", prompt, completion, total)
	}
	if e.SessionRequests() != 0 {
		t.Errorf("requests after Reset = %d; want 0", e.SessionRequests())
	}
	if len(e.ModelBreakdown()) != 0 {
		t.Errorf("ModelBreakdown len after Reset = %d; want 0", len(e.ModelBreakdown()))
	}
	if len(e.ToolBreakdown()) != 0 {
		t.Errorf("ToolBreakdown len after Reset = %d; want 0", len(e.ToolBreakdown()))
	}

	// Engine should still be usable after reset.
	e.RecordCompletion("gpt-4o", 500, 250, 0, 0)
	if e.SessionRequests() != 1 {
		t.Errorf("post-reset recording broken: requests = %d; want 1", e.SessionRequests())
	}
}

func TestCalculateModelCost(t *testing.T) {
	// Known model: claude-sonnet-4-20250514 → prefix "claude-sonnet-4" → $3/M in, $15/M out.
	in, out := calculateModelCost("claude-sonnet-4-20250514", 1000, 500)
	if in <= 0 {
		t.Errorf("input cost = %v; want > 0", in)
	}
	if out <= 0 {
		t.Errorf("output cost = %v; want > 0", out)
	}

	// 1000/1e6 * 3 = 0.003, 500/1e6 * 15 = 0.0075
	expectedIn := 0.003
	expectedOut := 0.0075
	epsilon := 1e-9
	if in < expectedIn-epsilon || in > expectedIn+epsilon {
		t.Errorf("input cost = %v; want %v", in, expectedIn)
	}
	if out < expectedOut-epsilon || out > expectedOut+epsilon {
		t.Errorf("output cost = %v; want %v", out, expectedOut)
	}

	total := in + out
	if total <= 0 {
		t.Errorf("total = %v; want > 0", total)
	}
}

func TestCalculateModelCost_UnknownModel(t *testing.T) {
	in, out := calculateModelCost("some-unknown-model-xyz", 1000, 500)
	if in != 0 {
		t.Errorf("unknown input cost = %v; want 0", in)
	}
	if out != 0 {
		t.Errorf("unknown output cost = %v; want 0", out)
	}
}

func TestLookupPricing_ExactAndPrefix(t *testing.T) {
	// Exact
	p, ok := lookupPricing("gpt-4o")
	if !ok {
		t.Fatal("gpt-4o not found")
	}
	if p.InputPerMillion != 2.50 {
		t.Errorf("gpt-4o input price = %v; want 2.50", p.InputPerMillion)
	}

	// Prefix match
	p, ok = lookupPricing("claude-sonnet-4-20250514")
	if !ok {
		t.Fatal("claude-sonnet-4-20250514 not found via prefix")
	}
	if p.InputPerMillion != 3.00 {
		t.Errorf("claude-sonnet-4 input price = %v; want 3.00", p.InputPerMillion)
	}

	// Miss
	_, ok = lookupPricing("nonexistent-model-foo")
	if ok {
		t.Error("nonexistent model unexpectedly found")
	}
}
