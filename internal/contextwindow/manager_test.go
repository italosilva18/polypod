package contextwindow

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// testLogger returns a discard-based slog.Logger suitable for tests.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ── WellKnownLimits ─────────────────────────────────────────────────────────

func TestWellKnownLimits_ClaudeSonnet4(t *testing.T) {
	l := WellKnownLimits("claude-sonnet-4-20250514")
	if l.MaxContextTokens == 0 {
		t.Error("claude-sonnet-4 MaxContextTokens should be non-zero")
	}
	if l.MaxOutputTokens == 0 {
		t.Error("claude-sonnet-4 MaxOutputTokens should be non-zero")
	}
	if l.ReservedForOutput == 0 {
		t.Error("claude-sonnet-4 ReservedForOutput should be non-zero")
	}
	if l.MaxContextTokens != 200000 {
		t.Errorf("claude-sonnet-4 MaxContextTokens = %d; want 200000", l.MaxContextTokens)
	}
}

func TestWellKnownLimits_GPT4o(t *testing.T) {
	l := WellKnownLimits("gpt-4o")
	if l.MaxContextTokens != 128000 {
		t.Errorf("gpt-4o MaxContextTokens = %d; want 128000", l.MaxContextTokens)
	}
	if l.MaxOutputTokens == 0 {
		t.Error("gpt-4o MaxOutputTokens should be non-zero")
	}
	if l.ReservedForOutput == 0 {
		t.Error("gpt-4o ReservedForOutput should be non-zero")
	}
}

func TestWellKnownLimits_Gemini25Pro(t *testing.T) {
	l := WellKnownLimits("gemini-2.5-pro")
	if l.MaxContextTokens == 0 {
		t.Error("gemini-2.5-pro MaxContextTokens should be non-zero")
	}
	if l.MaxOutputTokens == 0 {
		t.Error("gemini-2.5-pro MaxOutputTokens should be non-zero")
	}
	if l.ReservedForOutput == 0 {
		t.Error("gemini-2.5-pro ReservedForOutput should be non-zero")
	}
}

func TestWellKnownLimits_UnknownDefaults(t *testing.T) {
	l := WellKnownLimits("totally-made-up-model-v999")
	if l.MaxContextTokens != 128000 {
		t.Errorf("unknown MaxContextTokens = %d; want default 128000", l.MaxContextTokens)
	}
	if l.MaxOutputTokens != 8192 {
		t.Errorf("unknown MaxOutputTokens = %d; want default 8192", l.MaxOutputTokens)
	}
	if l.ReservedForOutput != 8192 {
		t.Errorf("unknown ReservedForOutput = %d; want default 8192", l.ReservedForOutput)
	}
}

func TestWellKnownLimits_AllModelsHavePositiveFields(t *testing.T) {
	models := []string{
		"claude-sonnet-4-20250514",
		"claude-3-7-sonnet-20250219",
		"claude-3-5-sonnet-20241022",
		"claude-3-5-haiku-20241022",
		"gpt-4o",
		"gpt-4o-mini",
		"gpt-4.1",
		"gpt-4.1-mini",
		"o3",
		"o3-mini",
		"o4-mini",
		"gemini-2.5-pro",
		"gemini-2.5-flash",
		"gemini-2.0-flash",
		"deepseek-chat",
		"deepseek-reasoner",
		"llama-3.3-70b-versatile",
		"mistral-large-latest",
	}
	for _, model := range models {
		l := WellKnownLimits(model)
		if l.MaxContextTokens <= 0 {
			t.Errorf("%s: MaxContextTokens = %d; want > 0", model, l.MaxContextTokens)
		}
		if l.MaxOutputTokens <= 0 {
			t.Errorf("%s: MaxOutputTokens = %d; want > 0", model, l.MaxOutputTokens)
		}
		if l.ReservedForOutput <= 0 {
			t.Errorf("%s: ReservedForOutput = %d; want > 0", model, l.ReservedForOutput)
		}
	}
}

// ── Manager lifecycle ───────────────────────────────────────────────────────

func TestNewManager_KnownModel(t *testing.T) {
	m := NewManager("claude-sonnet-4-20250514", testLogger())
	limits := m.Limits()
	if limits.MaxContextTokens != 200000 {
		t.Errorf("MaxContextTokens = %d; want 200000", limits.MaxContextTokens)
	}
	if limits.MaxOutputTokens != 16384 {
		t.Errorf("MaxOutputTokens = %d; want 16384", limits.MaxOutputTokens)
	}
}

func TestNewManager_UnknownModel(t *testing.T) {
	m := NewManager("nonexistent-model", testLogger())
	limits := m.Limits()
	if limits.MaxContextTokens != 128000 {
		t.Errorf("unknown MaxContextTokens = %d; want default 128000", limits.MaxContextTokens)
	}
}

func TestNewManagerWithLimits_Custom(t *testing.T) {
	custom := ModelLimits{
		MaxContextTokens:  50000,
		MaxOutputTokens:   4000,
		ReservedForOutput: 4000,
	}
	m := NewManagerWithLimits(custom, testLogger())
	limits := m.Limits()
	if limits.MaxContextTokens != 50000 {
		t.Errorf("custom MaxContextTokens = %d; want 50000", limits.MaxContextTokens)
	}
	if limits.MaxOutputTokens != 4000 {
		t.Errorf("custom MaxOutputTokens = %d; want 4000", limits.MaxOutputTokens)
	}
	if limits.ReservedForOutput != 4000 {
		t.Errorf("custom ReservedForOutput = %d; want 4000", limits.ReservedForOutput)
	}
}

// ── Token tracking ──────────────────────────────────────────────────────────

func TestManager_Update_SumsToTotal(t *testing.T) {
	m := NewManager("gpt-4o", testLogger())
	m.Update(1000, 2000, 500)

	stats := m.Stats()
	if stats["total_tokens"] != 3500 {
		t.Errorf("total_tokens = %v; want 3500", stats["total_tokens"])
	}
	if stats["system_tokens"] != 1000 {
		t.Errorf("system_tokens = %v; want 1000", stats["system_tokens"])
	}
	if stats["history_tokens"] != 2000 {
		t.Errorf("history_tokens = %v; want 2000", stats["history_tokens"])
	}
	if stats["tool_tokens"] != 500 {
		t.Errorf("tool_tokens = %v; want 500", stats["tool_tokens"])
	}
}

func TestManager_AddTokens_Increments(t *testing.T) {
	m := NewManager("gpt-4o", testLogger())
	m.Update(100, 200, 50) // total 350
	m.AddTokens(150)

	stats := m.Stats()
	if stats["total_tokens"] != 500 {
		t.Errorf("after AddTokens total = %v; want 500", stats["total_tokens"])
	}
}

func TestManager_Reset_ClearsCounts(t *testing.T) {
	m := NewManager("gpt-4o", testLogger())
	m.Update(1000, 2000, 500)
	m.Reset()

	stats := m.Stats()
	if stats["total_tokens"] != 0 {
		t.Errorf("after Reset total_tokens = %v; want 0", stats["total_tokens"])
	}
	if stats["system_tokens"] != 0 {
		t.Errorf("after Reset system_tokens = %v; want 0", stats["system_tokens"])
	}
	if stats["history_tokens"] != 0 {
		t.Errorf("after Reset history_tokens = %v; want 0", stats["history_tokens"])
	}
	if stats["tool_tokens"] != 0 {
		t.Errorf("after Reset tool_tokens = %v; want 0", stats["tool_tokens"])
	}
}

func TestManager_Stats_HasExpectedKeys(t *testing.T) {
	m := NewManager("gpt-4o", testLogger())
	m.Update(1000, 2000, 500)

	stats := m.Stats()
	expectedKeys := []string{
		"total_tokens",
		"system_tokens",
		"history_tokens",
		"tool_tokens",
		"max_context",
		"max_output",
		"available_for_input",
		"usage_percent",
		"usage_level",
		"needs_compaction",
	}
	for _, k := range expectedKeys {
		if _, ok := stats[k]; !ok {
			t.Errorf("Stats missing key %q", k)
		}
	}
}

func TestManager_AvailableForInput(t *testing.T) {
	m := NewManager("gpt-4o", testLogger()) // 128k ctx, 16k reserved
	m.Update(0, 0, 0)

	avail := m.AvailableForInput()
	want := 128000 - 16384
	if avail != want {
		t.Errorf("AvailableForInput empty = %d; want %d", avail, want)
	}

	m.Update(1000, 2000, 500) // 3500 used
	avail = m.AvailableForInput()
	want = 128000 - 16384 - 3500
	if avail != want {
		t.Errorf("AvailableForInput = %d; want %d", avail, want)
	}
}

func TestManager_AvailableForInput_NeverNegative(t *testing.T) {
	m := NewManager("gpt-4o", testLogger())
	m.Update(200000, 0, 0) // exceeds everything
	if a := m.AvailableForInput(); a != 0 {
		t.Errorf("AvailableForInput when over = %d; want 0", a)
	}
}

// ── Usage levels ────────────────────────────────────────────────────────────

func TestManager_UsageLevel_Low(t *testing.T) {
	m := NewManager("gpt-4o", testLogger())
	m.Update(100, 100, 100) // ~300 / 128000 -> very low
	if lvl := m.UsageLevel(); lvl != UsageLow {
		t.Errorf("UsageLevel = %v; want UsageLow", lvl)
	}
}

func TestManager_UsageLevel_Medium(t *testing.T) {
	m := NewManager("gpt-4o", testLogger())
	// 50% of 128k = 64000
	m.Update(64000, 0, 0)
	if lvl := m.UsageLevel(); lvl != UsageMedium {
		t.Errorf("UsageLevel at 50%% = %v; want UsageMedium", lvl)
	}
}

func TestManager_UsageLevel_High(t *testing.T) {
	m := NewManager("gpt-4o", testLogger())
	// 80% of 128k = 102400
	m.Update(102400, 0, 0)
	if lvl := m.UsageLevel(); lvl != UsageHigh {
		t.Errorf("UsageLevel at 80%% = %v; want UsageHigh", lvl)
	}
}

func TestManager_UsageLevel_Critical(t *testing.T) {
	m := NewManager("gpt-4o", testLogger())
	// 95% of 128k = 121600
	m.Update(121600, 0, 0)
	if lvl := m.UsageLevel(); lvl != UsageCritical {
		t.Errorf("UsageLevel at 95%% = %v; want UsageCritical", lvl)
	}
}

func TestManager_UsagePercent(t *testing.T) {
	m := NewManager("gpt-4o", testLogger()) // 128k
	m.Update(64000, 0, 0)
	p := m.UsagePercent()
	if p < 49.9 || p > 50.1 {
		t.Errorf("UsagePercent at half = %f; want ~50", p)
	}
}

func TestManager_UsagePercent_Empty(t *testing.T) {
	m := NewManager("gpt-4o", testLogger())
	if p := m.UsagePercent(); p != 0 {
		t.Errorf("UsagePercent empty = %f; want 0", p)
	}
}

// ── Callbacks ───────────────────────────────────────────────────────────────

func TestManager_AutoCompactCallback(t *testing.T) {
	m := NewManager("gpt-4o", testLogger())

	var called bool
	var mu sync.Mutex
	m.SetAutoCompactCallback(func() {
		mu.Lock()
		defer mu.Unlock()
		called = true
	})

	// gpt-4o: 128k * 0.80 = 102400
	m.Update(50000, 55000, 0) // 105000 total ~82%

	// Callback runs in a goroutine.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		c := called
		mu.Unlock()
		if c {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if !called {
		t.Error("auto-compact callback was not fired at >80%")
	}
}

func TestManager_CriticalCallback(t *testing.T) {
	m := NewManager("gpt-4o", testLogger())

	var called bool
	var mu sync.Mutex
	m.SetCriticalCallback(func() {
		mu.Lock()
		defer mu.Unlock()
		called = true
	})

	// gpt-4o: 128k * 0.95 = 121600
	m.Update(125000, 0, 0) // ~97.6%

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		c := called
		mu.Unlock()
		if c {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if !called {
		t.Error("critical callback was not fired at >95%")
	}
}

func TestManager_SetThresholds_Custom(t *testing.T) {
	m := NewManager("gpt-4o", testLogger())
	// Make it trip auto-compact at 20% and critical at 30%.
	m.SetThresholds(0.20, 0.30)

	var autoCalled bool
	var mu sync.Mutex
	m.SetAutoCompactCallback(func() {
		mu.Lock()
		defer mu.Unlock()
		autoCalled = true
	})

	// 25% of 128k = 32000
	m.Update(32000, 0, 0)

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		c := autoCalled
		mu.Unlock()
		if c {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if !autoCalled {
		t.Error("auto-compact callback should fire at 25% with 20% threshold")
	}
}

// ── NeedsCompaction ─────────────────────────────────────────────────────────

func TestManager_NeedsCompaction_False(t *testing.T) {
	m := NewManager("gpt-4o", testLogger())
	m.Update(1000, 1000, 0) // way below
	if m.NeedsCompaction() {
		t.Error("NeedsCompaction at low usage = true; want false")
	}
}

func TestManager_NeedsCompaction_True(t *testing.T) {
	m := NewManager("gpt-4o", testLogger())
	m.Update(102400, 0, 0) // exactly 80% of 128k
	if !m.NeedsCompaction() {
		t.Error("NeedsCompaction at 80%% = false; want true")
	}
}

func TestManager_NeedsCompaction_AtCritical(t *testing.T) {
	m := NewManager("gpt-4o", testLogger())
	m.Update(125000, 0, 0)
	if !m.NeedsCompaction() {
		t.Error("NeedsCompaction at critical = false; want true")
	}
}

// ── AnalyzeContext ──────────────────────────────────────────────────────────

func TestManager_AnalyzeContext_SectionsAndTotal(t *testing.T) {
	m := NewManager("gpt-4o", testLogger())
	a := m.AnalyzeContext(1000, 2000, 500, 300, 200)
	if a == nil {
		t.Fatal("AnalyzeContext returned nil")
	}

	want := 1000 + 2000 + 500 + 300 + 200
	if a.TotalUsed != want {
		t.Errorf("TotalUsed = %d; want %d", a.TotalUsed, want)
	}
	if a.TotalMax != 128000 {
		t.Errorf("TotalMax = %d; want 128000", a.TotalMax)
	}
	if len(a.Sections) == 0 {
		t.Fatal("Sections is empty; want populated")
	}

	// Sum of sections should equal TotalUsed.
	sum := 0
	for _, s := range a.Sections {
		sum += s.Tokens
	}
	if sum != want {
		t.Errorf("sum of sections = %d; want %d", sum, want)
	}
}

func TestManager_AnalyzeContext_OmitsZeroSections(t *testing.T) {
	m := NewManager("gpt-4o", testLogger())
	a := m.AnalyzeContext(1000, 2000, 0, 0, 0)
	if a == nil {
		t.Fatal("nil analysis")
	}
	// Only System Prompt + History should appear.
	if len(a.Sections) != 2 {
		t.Errorf("len(Sections) = %d; want 2 (zero sections should be omitted)", len(a.Sections))
	}
	for _, s := range a.Sections {
		if s.Tokens <= 0 {
			t.Errorf("section %q has tokens = %d; want > 0", s.Name, s.Tokens)
		}
	}
}

func TestManager_AnalyzeContext_WarningsAtHighUsage(t *testing.T) {
	m := NewManager("gpt-4o", testLogger()) // 128k
	// Push usage above 80% with history.
	a := m.AnalyzeContext(0, 110000, 0, 0, 0) // ~85.9%
	if a == nil {
		t.Fatal("nil analysis")
	}
	if len(a.Warnings) == 0 {
		t.Error("Warnings should be populated at high usage")
	}
	if len(a.Recommendations) == 0 {
		t.Error("Recommendations should be populated at high usage")
	}
}

func TestManager_AnalyzeContext_WarningsAtCritical(t *testing.T) {
	m := NewManager("gpt-4o", testLogger())   // 128k
	a := m.AnalyzeContext(0, 125000, 0, 0, 0) // ~97.6%
	if a == nil {
		t.Fatal("nil analysis")
	}
	if len(a.Warnings) == 0 {
		t.Error("Warnings should be populated at critical usage")
	}
}

func TestManager_AnalyzeContext_EmptyContext(t *testing.T) {
	m := NewManager("gpt-4o", testLogger())
	a := m.AnalyzeContext(0, 0, 0, 0, 0)
	if a == nil {
		t.Fatal("nil analysis")
	}
	if a.TotalUsed != 0 {
		t.Errorf("TotalUsed empty = %d; want 0", a.TotalUsed)
	}
	if len(a.Sections) != 0 {
		t.Errorf("len(Sections) empty = %d; want 0", len(a.Sections))
	}
}

func TestManager_AnalyzeContext_UsagePercent(t *testing.T) {
	m := NewManager("gpt-4o", testLogger()) // 128k
	a := m.AnalyzeContext(0, 64000, 0, 0, 0)
	if a.UsagePercent < 49.9 || a.UsagePercent > 50.1 {
		t.Errorf("UsagePercent = %f; want ~50", a.UsagePercent)
	}
}

// ── FormatAnalysis ──────────────────────────────────────────────────────────

func TestFormatAnalysis_NilReturnsEmpty(t *testing.T) {
	if s := FormatAnalysis(nil); s != "" {
		t.Errorf("FormatAnalysis(nil) = %q; want empty", s)
	}
}

func TestFormatAnalysis_NonEmpty(t *testing.T) {
	m := NewManager("gpt-4o", testLogger())
	a := m.AnalyzeContext(1000, 2000, 500, 0, 0)
	out := FormatAnalysis(a)
	if out == "" {
		t.Error("FormatAnalysis returned empty string for non-nil analysis")
	}
}
