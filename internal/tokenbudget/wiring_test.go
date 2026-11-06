package tokenbudget

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBudgetTracker_Accumulation(t *testing.T) {
	tr := NewTracker(TrackerConfig{Limit: 10_000})

	tr.RecordTurn(TurnStats{
		Prompt:     100,
		Completion: 200,
		ToolResult: 50,
	})
	tr.RecordTurn(TurnStats{
		Prompt:     150,
		Completion: 250,
		CacheRead:  500,
		CacheWrite: 100,
		Attachment: 25,
		Extras:     map[Category]int{"thinking": 75},
	})

	if got, want := tr.Turns(), 2; got != want {
		t.Errorf("Turns = %d; want %d", got, want)
	}

	// Totals per category
	if got := tr.UsedIn(CategoryPrompt); got != 250 {
		t.Errorf("prompt total = %d; want 250", got)
	}
	if got := tr.UsedIn(CategoryCompletion); got != 450 {
		t.Errorf("completion total = %d; want 450", got)
	}
	if got := tr.UsedIn(CategoryToolResult); got != 50 {
		t.Errorf("tool_result total = %d; want 50", got)
	}
	if got := tr.UsedIn(CategoryAttachment); got != 25 {
		t.Errorf("attachment total = %d; want 25", got)
	}
	if got := tr.UsedIn(CategoryCacheRead); got != 500 {
		t.Errorf("cache_read total = %d; want 500", got)
	}
	if got := tr.UsedIn(CategoryCacheWrite); got != 100 {
		t.Errorf("cache_write total = %d; want 100", got)
	}
	if got := tr.UsedIn("thinking"); got != 75 {
		t.Errorf("extras[thinking] total = %d; want 75", got)
	}

	wantTotal := 250 + 450 + 50 + 25 + 500 + 100 + 75
	if got := tr.TotalUsed(); got != wantTotal {
		t.Errorf("TotalUsed = %d; want %d", got, wantTotal)
	}

	// Remaining = 10000 - 1450 = 8550
	rem, err := tr.RemainingBudget(context.Background())
	if err != nil {
		t.Fatalf("RemainingBudget err: %v", err)
	}
	if rem != 10_000-wantTotal {
		t.Errorf("RemainingBudget = %d; want %d", rem, 10_000-wantTotal)
	}

	// Reset should clear everything but keep limit.
	tr.Reset()
	if tr.TotalUsed() != 0 || tr.Turns() != 0 {
		t.Errorf("after Reset: TotalUsed=%d Turns=%d; want 0/0", tr.TotalUsed(), tr.Turns())
	}
	if tr.Limit() != 10_000 {
		t.Errorf("after Reset: Limit=%d; want 10000", tr.Limit())
	}
}

func TestBudgetTracker_Report(t *testing.T) {
	tr := NewTracker(TrackerConfig{Limit: 1_000, CompactThreshold: 0.5})

	tr.RecordTurn(TurnStats{
		Prompt:     300,
		Completion: 100,
		ToolResult: 50,
	})

	report := tr.BudgetReport()

	// Sanity: the report should be multi-line, contain the header, the
	// limit, and at least one per-category row.
	if !strings.Contains(report, "Token Budget Report") {
		t.Errorf("report missing header: %q", report)
	}
	if !strings.Contains(report, "prompt") {
		t.Errorf("report missing prompt row: %q", report)
	}
	if !strings.Contains(report, "completion") {
		t.Errorf("report missing completion row: %q", report)
	}
	if !strings.Contains(report, "1_000") && !strings.Contains(report, "1000") {
		t.Errorf("report missing limit: %q", report)
	}
	// 450/1000 = 45% — below the 50% threshold.
	if !strings.Contains(report, "below threshold") {
		t.Errorf("report should indicate below threshold, got: %q", report)
	}

	// Count at least 6 per-category lines (one per default category).
	for _, c := range DefaultCategories() {
		if !strings.Contains(report, string(c)) {
			t.Errorf("report missing category %s: %q", c, report)
		}
	}
}

func TestBudgetTracker_CompactThreshold(t *testing.T) {
	tr := NewTracker(TrackerConfig{Limit: 1_000, CompactThreshold: 0.70})

	// 50% usage — below 70% threshold.
	tr.RecordTurn(TurnStats{Prompt: 500})
	if tr.ShouldTriggerCompact() {
		t.Errorf("ShouldTriggerCompact at 50%% = true; want false")
	}
	if tr.IsExceeded() {
		t.Errorf("IsExceeded at 50%% = true; want false")
	}

	// Push to 75% — above 70% threshold.
	tr.RecordTurn(TurnStats{Completion: 250})
	if !tr.ShouldTriggerCompact() {
		t.Errorf("ShouldTriggerCompact at 75%% = false; want true")
	}

	// Push over the limit.
	tr.RecordTurn(TurnStats{ToolResult: 400})
	if !tr.IsExceeded() {
		t.Errorf("IsExceeded after over-limit = false; want true")
	}
	if _, err := tr.RemainingBudget(context.Background()); !errors.Is(err, ErrBudgetExceeded) {
		t.Errorf("RemainingBudget err = %v; want ErrBudgetExceeded", err)
	}
}

func TestBudgetTracker_NoLimit(t *testing.T) {
	tr := NewTracker(TrackerConfig{}) // no limit

	tr.RecordTurn(TurnStats{Prompt: 1_000_000})

	// No limit: ShouldTriggerCompact should always be false.
	if tr.ShouldTriggerCompact() {
		t.Errorf("ShouldTriggerCompact with no limit = true; want false")
	}
	if tr.IsExceeded() {
		t.Errorf("IsExceeded with no limit = true; want false")
	}

	// RemainingBudget should return a sentinel "effectively unlimited" value
	// without error.
	rem, err := tr.RemainingBudget(context.Background())
	if err != nil {
		t.Fatalf("RemainingBudget err: %v", err)
	}
	if rem <= 1_000_000 {
		t.Errorf("RemainingBudget with no limit = %d; want very large", rem)
	}

	// Report should still render.
	report := tr.BudgetReport()
	if !strings.Contains(report, "unlimited") {
		t.Errorf("report missing unlimited marker: %q", report)
	}
}

func TestBudgetTracker_ContextCancellation(t *testing.T) {
	tr := NewTracker(TrackerConfig{Limit: 100})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := tr.RemainingBudget(ctx); err == nil {
		t.Error("RemainingBudget with cancelled ctx should return error")
	}
}
