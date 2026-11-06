package tips

import (
	"math"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// TipHistory
// ---------------------------------------------------------------------------

func TestTipHistory_RecordAndSessionsSince(t *testing.T) {
	h := NewTipHistory()

	// Never shown → MaxInt32 sentinel.
	if got := h.SessionsSinceLastShown("missing", 5); got != math.MaxInt32 {
		t.Errorf("never-shown: got %d, want %d", got, math.MaxInt32)
	}

	h.RecordTipShown("foo", 10)
	if got := h.SessionsSinceLastShown("foo", 13); got != 3 {
		t.Errorf("after record: got %d, want 3", got)
	}
	if got := h.SessionsSinceLastShown("foo", 10); got != 0 {
		t.Errorf("same startup: got %d, want 0", got)
	}
}

func TestTipHistory_DismissTip(t *testing.T) {
	h := NewTipHistory()
	h.DismissTip("forever-hidden")

	// Even at very large startup counts the tip is still "in cooldown".
	if got := h.SessionsSinceLastShown("forever-hidden", 1_000_000); got >= 0 {
		t.Errorf("dismissed tip should yield non-positive sessions, got %d", got)
	}
}

func TestTipHistory_LoadAndSnapshot(t *testing.T) {
	h := NewTipHistory()
	h.Load(map[string]int{"a": 1, "b": 2})

	snap := h.Snapshot()
	if len(snap) != 2 || snap["a"] != 1 || snap["b"] != 2 {
		t.Errorf("snapshot mismatch: %#v", snap)
	}

	// Mutating the snapshot must not affect the history.
	snap["a"] = 999
	again := h.Snapshot()
	if again["a"] != 1 {
		t.Errorf("snapshot leaked mutation: got %d, want 1", again["a"])
	}
}

// ---------------------------------------------------------------------------
// TipRegistry
// ---------------------------------------------------------------------------

func TestTipRegistry_RegisterAndAll(t *testing.T) {
	r := NewTipRegistry()
	if got := r.All(); len(got) != 0 {
		t.Errorf("new registry should be empty, got %d", len(got))
	}

	tip := Tip{
		ID:               "first",
		Content:          func(_ *TipContext) string { return "hello" },
		CooldownSessions: 1,
		IsRelevant:       func(_ *TipContext) bool { return true },
	}
	r.Register(tip)

	got := r.All()
	if len(got) != 1 {
		t.Fatalf("expected 1 tip, got %d", len(got))
	}
	if got[0].ID != "first" {
		t.Errorf("ID mismatch: got %q, want %q", got[0].ID, "first")
	}
}

func TestTipRegistry_RegisterMany(t *testing.T) {
	r := NewTipRegistry()
	r.RegisterMany([]Tip{
		{ID: "a", Content: func(_ *TipContext) string { return "a" }, IsRelevant: func(_ *TipContext) bool { return true }},
		{ID: "b", Content: func(_ *TipContext) string { return "b" }, IsRelevant: func(_ *TipContext) bool { return true }},
		{ID: "c", Content: func(_ *TipContext) string { return "c" }, IsRelevant: func(_ *TipContext) bool { return true }},
	})

	if got := len(r.All()); got != 3 {
		t.Errorf("expected 3 tips, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// BuiltinTips
// ---------------------------------------------------------------------------

func TestBuiltinTips_NonEmpty(t *testing.T) {
	tips := BuiltinTips()
	if len(tips) == 0 {
		t.Fatal("BuiltinTips() returned no tips")
	}

	seen := make(map[string]bool, len(tips))
	for _, tip := range tips {
		if tip.ID == "" {
			t.Errorf("tip with empty ID: %#v", tip)
		}
		if seen[tip.ID] {
			t.Errorf("duplicate tip ID: %q", tip.ID)
		}
		seen[tip.ID] = true

		if tip.Content == nil {
			t.Errorf("tip %q has nil Content", tip.ID)
			continue
		}
		text := tip.Content(&TipContext{})
		if strings.TrimSpace(text) == "" {
			t.Errorf("tip %q produced empty content", tip.ID)
		}

		if tip.IsRelevant == nil {
			t.Errorf("tip %q has nil IsRelevant", tip.ID)
		}
	}
}

// ---------------------------------------------------------------------------
// TipScheduler
// ---------------------------------------------------------------------------

func newTestScheduler(t *testing.T, tips ...Tip) *TipScheduler {
	t.Helper()
	r := NewTipRegistry()
	r.RegisterMany(tips)
	return NewTipScheduler(r, NewTipHistory())
}

func TestTipScheduler_ShouldShowTip_RespectsCooldown(t *testing.T) {
	tip := Tip{
		ID:               "cool",
		Content:          func(_ *TipContext) string { return "cool" },
		CooldownSessions: 5,
		IsRelevant:       func(_ *TipContext) bool { return true },
	}
	s := newTestScheduler(t, tip)
	ctx := &TipContext{NumStartups: 10}

	// Never shown → should display.
	if !s.ShouldShowTip(&tip, ctx) {
		t.Error("expected tip to be shown when never recorded")
	}

	// Record at startup 10, ask again at 12 (only 2 sessions later).
	s.History.RecordTipShown(tip.ID, 10)
	ctx.NumStartups = 12
	if s.ShouldShowTip(&tip, ctx) {
		t.Error("expected cooldown to suppress tip (2 < 5)")
	}

	// Move forward past the cooldown.
	ctx.NumStartups = 16
	if !s.ShouldShowTip(&tip, ctx) {
		t.Error("expected tip to reappear after cooldown")
	}
}

func TestTipScheduler_ShouldShowTip_RespectsRelevance(t *testing.T) {
	tip := Tip{
		ID:               "newbie",
		Content:          func(_ *TipContext) string { return "welcome" },
		CooldownSessions: 0,
		IsRelevant: func(ctx *TipContext) bool {
			return ctx.NumStartups < 5
		},
	}
	s := newTestScheduler(t, tip)

	if !s.ShouldShowTip(&tip, &TipContext{NumStartups: 2}) {
		t.Error("relevant tip should show")
	}
	if s.ShouldShowTip(&tip, &TipContext{NumStartups: 10}) {
		t.Error("irrelevant tip should not show")
	}
}

func TestTipScheduler_DisabledReturnsNothing(t *testing.T) {
	tip := Tip{
		ID:         "any",
		Content:    func(_ *TipContext) string { return "any" },
		IsRelevant: func(_ *TipContext) bool { return true },
	}
	s := newTestScheduler(t, tip)
	s.Enabled = false

	if s.ShouldShowTip(&tip, &TipContext{}) {
		t.Error("disabled scheduler should not show tips")
	}
	if got := s.GetRelevantTips(&TipContext{}); got != nil {
		t.Errorf("disabled scheduler should return nil tips, got %#v", got)
	}
	if got := s.SelectTip(&TipContext{}); got != nil {
		t.Errorf("disabled scheduler should select nothing, got %#v", got)
	}
}

func TestTipScheduler_GetRelevantTips_FiltersAndAppendsCustom(t *testing.T) {
	yes := Tip{
		ID:         "always",
		Content:    func(_ *TipContext) string { return "always" },
		IsRelevant: func(_ *TipContext) bool { return true },
	}
	no := Tip{
		ID:         "never",
		Content:    func(_ *TipContext) string { return "never" },
		IsRelevant: func(_ *TipContext) bool { return false },
	}
	s := newTestScheduler(t, yes, no)

	ctx := &TipContext{
		NumStartups: 1,
		CustomOverride: &SpinnerTipsOverride{
			Tips: []string{"my custom tip"},
		},
	}
	got := s.GetRelevantTips(ctx)

	// Expect "always" plus one custom tip; "never" must be filtered.
	if len(got) != 2 {
		t.Fatalf("expected 2 tips (always + custom), got %d: %#v", len(got), got)
	}

	ids := []string{got[0].ID, got[1].ID}
	if ids[0] != "always" {
		t.Errorf("first tip should be 'always', got %q", ids[0])
	}
	if !strings.HasPrefix(ids[1], "custom-tip-") {
		t.Errorf("second tip should be a custom tip, got %q", ids[1])
	}
	if text := got[1].Content(ctx); text != "my custom tip" {
		t.Errorf("custom tip content = %q, want %q", text, "my custom tip")
	}
}

func TestTipScheduler_GetRelevantTips_ExcludeDefault(t *testing.T) {
	yes := Tip{
		ID:         "builtin",
		Content:    func(_ *TipContext) string { return "builtin" },
		IsRelevant: func(_ *TipContext) bool { return true },
	}
	s := newTestScheduler(t, yes)

	ctx := &TipContext{
		CustomOverride: &SpinnerTipsOverride{
			Tips:           []string{"only me"},
			ExcludeDefault: true,
		},
	}
	got := s.GetRelevantTips(ctx)
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 custom tip, got %d", len(got))
	}
	if text := got[0].Content(ctx); text != "only me" {
		t.Errorf("custom tip content = %q, want %q", text, "only me")
	}
}

func TestTipScheduler_SelectTip_PrefersStalest(t *testing.T) {
	tipA := Tip{
		ID:         "a",
		Content:    func(_ *TipContext) string { return "a" },
		IsRelevant: func(_ *TipContext) bool { return true },
	}
	tipB := Tip{
		ID:         "b",
		Content:    func(_ *TipContext) string { return "b" },
		IsRelevant: func(_ *TipContext) bool { return true },
	}
	s := newTestScheduler(t, tipA, tipB)

	// Record A as recently shown; B has never been shown so it's "older".
	s.History.RecordTipShown("a", 9)
	ctx := &TipContext{NumStartups: 10}

	got := s.SelectTip(ctx)
	if got == nil {
		t.Fatal("SelectTip returned nil")
	}
	if got.ID != "b" {
		t.Errorf("expected stalest tip 'b', got %q", got.ID)
	}
}

func TestTipScheduler_RecordShown(t *testing.T) {
	tip := Tip{
		ID:         "rec",
		Content:    func(_ *TipContext) string { return "rec" },
		IsRelevant: func(_ *TipContext) bool { return true },
	}
	s := newTestScheduler(t, tip)
	s.RecordShown(&tip, 42)

	if got := s.History.SessionsSinceLastShown("rec", 42); got != 0 {
		t.Errorf("RecordShown did not register tip: got %d, want 0", got)
	}
	if got := s.History.SessionsSinceLastShown("rec", 50); got != 8 {
		t.Errorf("after RecordShown: got %d, want 8", got)
	}
}

func TestTipScheduler_PlanModeRelevance(t *testing.T) {
	// Sanity check on a built-in tip's relevance behaviour for an
	// time-driven condition. plan-mode-for-complex-tasks is relevant when
	// PlanModeLastUse is zero or older than 7 days.
	var planTip *Tip
	for _, tip := range BuiltinTips() {
		tip := tip
		if tip.ID == "plan-mode-for-complex-tasks" {
			planTip = &tip
			break
		}
	}
	if planTip == nil {
		t.Skip("plan-mode-for-complex-tasks tip not present in builtin set")
	}

	if !planTip.IsRelevant(&TipContext{}) {
		t.Error("plan-mode tip should be relevant when PlanModeLastUse is zero")
	}
	recent := &TipContext{PlanModeLastUse: time.Now().Add(-1 * time.Hour)}
	if planTip.IsRelevant(recent) {
		t.Error("plan-mode tip should NOT be relevant if used 1h ago")
	}
	old := &TipContext{PlanModeLastUse: time.Now().Add(-30 * 24 * time.Hour)}
	if !planTip.IsRelevant(old) {
		t.Error("plan-mode tip should be relevant if last used 30d ago")
	}
}
