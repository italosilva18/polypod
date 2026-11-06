package compact

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/costa/polypod/internal/conversation"
)

// --- helpers -----------------------------------------------------------------

// fixedSummarizer returns a constant summary and records how many times it was
// invoked. It's the simplest possible SummaryFunc injection seam.
func fixedSummarizer(summary string) (SummaryFunc, *int32) {
	var calls int32
	fn := func(ctx context.Context, _ string, _ []conversation.Message, _ string) (string, error) {
		atomic.AddInt32(&calls, 1)
		return summary, nil
	}
	return fn, &calls
}

// failingSummarizer always returns err.
func failingSummarizer(err error) SummaryFunc {
	return func(ctx context.Context, _ string, _ []conversation.Message, _ string) (string, error) {
		return "", err
	}
}

// buildConvo returns a conversation with at least two API rounds so that
// GroupMessagesByAPIRound yields >= 2 groups (which is what CompactService.Compact
// requires to proceed past ErrNotEnoughMessages).
func buildConvo() []conversation.Message {
	now := time.Now()
	return []conversation.Message{
		{Role: conversation.RoleUser, Content: "hello, who are you?", Timestamp: now},
		{Role: conversation.RoleAssistant, Content: "I am an assistant.", Timestamp: now},
		{Role: conversation.RoleUser, Content: "can you help me with Go?", Timestamp: now},
		{Role: conversation.RoleAssistant, Content: "Of course, Go is great.", Timestamp: now},
		{Role: conversation.RoleUser, Content: "write me a function.", Timestamp: now},
		{Role: conversation.RoleAssistant, Content: "func Add(a,b int) int { return a+b }", Timestamp: now},
	}
}

func testConfig() CompactConfig {
	cfg := DefaultConfig()
	// Small cooldown so back-to-back tests in the same process don't
	// accidentally interact via the (per-service) compacting flag.
	cfg.CooldownDuration = time.Millisecond
	return cfg
}

// --- CompactService.Compact --------------------------------------------------

func TestCompact_EmptyMessagesReturnsError(t *testing.T) {
	fn, _ := fixedSummarizer("unused")
	svc := NewCompactService(testConfig(), fn, nil)

	_, err := svc.Compact(context.Background(), nil, "", false)
	if !errors.Is(err, ErrNotEnoughMessages) {
		t.Fatalf("expected ErrNotEnoughMessages for empty messages, got %v", err)
	}
}

func TestCompact_SingleGroupReturnsError(t *testing.T) {
	// One user + one assistant == a single API-round group, which is
	// below the minimum required by Compact.
	fn, calls := fixedSummarizer("unused")
	svc := NewCompactService(testConfig(), fn, nil)

	msgs := []conversation.Message{
		{Role: conversation.RoleUser, Content: "hi", Timestamp: time.Now()},
		{Role: conversation.RoleAssistant, Content: "hello", Timestamp: time.Now()},
	}
	_, err := svc.Compact(context.Background(), msgs, "", false)
	if !errors.Is(err, ErrNotEnoughMessages) {
		t.Fatalf("expected ErrNotEnoughMessages for single group, got %v", err)
	}
	if atomic.LoadInt32(calls) != 0 {
		t.Errorf("summarizer should not have been called, got %d calls", *calls)
	}
}

func TestCompact_MultiGroupSucceeds(t *testing.T) {
	fn, calls := fixedSummarizer("<summary>conversation about Go</summary>")
	svc := NewCompactService(testConfig(), fn, nil)

	res, err := svc.Compact(context.Background(), buildConvo(), "", false)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if res == nil {
		t.Fatal("result is nil")
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Errorf("expected summarizer to be called once, got %d", *calls)
	}
	if res.Summary == "" {
		t.Error("Summary should not be empty")
	}
	// FormatCompactSummary should have replaced <summary>...</summary> with a
	// "Summary:" header.
	if !strings.Contains(res.Summary, "Summary:") {
		t.Errorf("expected formatted summary to contain 'Summary:', got %q", res.Summary)
	}
	if res.MessagesRemoved == 0 {
		t.Error("expected MessagesRemoved > 0")
	}
	if res.PreCompactTokens <= 0 {
		t.Error("expected PreCompactTokens > 0")
	}
	if res.IsAutoCompact {
		t.Error("IsAutoCompact should be false for manual compaction")
	}
	if len(res.SummaryMessages) != 2 {
		t.Errorf("expected 2 summary messages (boundary + summary), got %d", len(res.SummaryMessages))
	}
	if res.CompactedAt.IsZero() {
		t.Error("CompactedAt should be set")
	}
}

func TestCompact_PropagatesSummarizerError(t *testing.T) {
	boom := errors.New("LLM unavailable")
	svc := NewCompactService(testConfig(), failingSummarizer(boom), nil)

	_, err := svc.Compact(context.Background(), buildConvo(), "", false)
	if err == nil {
		t.Fatal("expected error from failing summarizer")
	}
	if !errors.Is(err, boom) {
		t.Errorf("expected wrapped boom error, got %v", err)
	}
}

func TestCompact_EmptySummaryReturnsError(t *testing.T) {
	empty := func(ctx context.Context, _ string, _ []conversation.Message, _ string) (string, error) {
		return "", nil
	}
	svc := NewCompactService(testConfig(), empty, nil)

	_, err := svc.Compact(context.Background(), buildConvo(), "", false)
	if !errors.Is(err, ErrNoSummaryGenerated) {
		t.Fatalf("expected ErrNoSummaryGenerated, got %v", err)
	}
}

func TestCompact_DisabledReturnsError(t *testing.T) {
	cfg := testConfig()
	cfg.Enabled = false
	fn, _ := fixedSummarizer("unused")
	svc := NewCompactService(cfg, fn, nil)

	_, err := svc.Compact(context.Background(), buildConvo(), "", false)
	if err == nil {
		t.Fatal("expected error when compaction disabled")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("expected 'disabled' in error, got %v", err)
	}
}

func TestCompact_ContextCancellation(t *testing.T) {
	// Summarizer that respects ctx cancellation.
	fn := func(ctx context.Context, _ string, _ []conversation.Message, _ string) (string, error) {
		return "", ctx.Err()
	}
	svc := NewCompactService(testConfig(), fn, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := svc.Compact(ctx, buildConvo(), "", false)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// --- Thresholds --------------------------------------------------------------

func TestCompactService_EffectiveContextWindow(t *testing.T) {
	fn, _ := fixedSummarizer("unused")

	// Default: 200000 ctx - min(16384 maxOut, 20000 reserved) = 200000 - 16384.
	svc := NewCompactService(DefaultConfig(), fn, nil)
	if got, want := svc.EffectiveContextWindow(), 200_000-16_384; got != want {
		t.Errorf("EffectiveContextWindow default = %d, want %d", got, want)
	}

	// When MaxOutputTokens > ReservedOutputTokens, the reserved value wins.
	cfg := CompactConfig{
		ContextWindowSize:    100_000,
		MaxOutputTokens:      50_000,
		ReservedOutputTokens: 10_000,
		Enabled:              true,
	}
	svc2 := NewCompactService(cfg, fn, nil)
	if got, want := svc2.EffectiveContextWindow(), 90_000; got != want {
		t.Errorf("EffectiveContextWindow custom = %d, want %d", got, want)
	}
}

func TestCompactService_AutoCompactThreshold(t *testing.T) {
	fn, _ := fixedSummarizer("unused")
	svc := NewCompactService(DefaultConfig(), fn, nil)

	// Default: effective - 13000 buffer.
	want := (200_000 - 16_384) - 13_000
	if got := svc.AutoCompactThreshold(); got != want {
		t.Errorf("AutoCompactThreshold = %d, want %d", got, want)
	}
}

func TestNewCompactService_AppliesDefaults(t *testing.T) {
	fn, _ := fixedSummarizer("unused")
	// Explicitly pass a zero-value config — the constructor should fill in
	// its own defaults for the key fields.
	cfg := CompactConfig{ContextWindowSize: 100_000, Enabled: true}
	svc := NewCompactService(cfg, fn, nil)

	if svc.config.ReservedOutputTokens != 20_000 {
		t.Errorf("ReservedOutputTokens default = %d, want 20000", svc.config.ReservedOutputTokens)
	}
	if svc.config.AutoCompactBufferTokens != 13_000 {
		t.Errorf("AutoCompactBufferTokens default = %d, want 13000", svc.config.AutoCompactBufferTokens)
	}
	if svc.config.MaxConsecutiveFailures != 3 {
		t.Errorf("MaxConsecutiveFailures default = %d, want 3", svc.config.MaxConsecutiveFailures)
	}
	if svc.config.CooldownDuration != 10*time.Second {
		t.Errorf("CooldownDuration default = %v, want 10s", svc.config.CooldownDuration)
	}
}

// --- EstimateTokens & helpers ------------------------------------------------

func TestEstimateTokens_EmptyIsZero(t *testing.T) {
	if got := EstimateTokens(nil); got != 0 {
		t.Errorf("EstimateTokens(nil) = %d, want 0", got)
	}
}

func TestEstimateTokens_GrowsWithContent(t *testing.T) {
	short := []conversation.Message{
		{Role: conversation.RoleUser, Content: "hi"},
	}
	long := []conversation.Message{
		{Role: conversation.RoleUser, Content: strings.Repeat("word ", 500)},
	}
	if EstimateTokens(long) <= EstimateTokens(short) {
		t.Error("longer content should yield a higher token estimate")
	}
}

func TestStripImagesFromMessages(t *testing.T) {
	msgs := []conversation.Message{
		{Role: conversation.RoleUser, Content: "look at this data:image/png;base64,AAAABBBBCCCCDDDDEEEE end"},
		{Role: conversation.RoleUser, Content: "no image here"},
		{Role: conversation.RoleUser, Content: "here is a pdf data:application/pdf;base64,XYZ123ABCDEF end"},
	}
	out := StripImagesFromMessages(msgs)

	if strings.Contains(out[0].Content, "data:image/") {
		t.Errorf("image data not stripped: %q", out[0].Content)
	}
	if !strings.Contains(out[0].Content, "[image]") {
		t.Errorf("expected [image] placeholder, got %q", out[0].Content)
	}
	if out[1].Content != "no image here" {
		t.Errorf("non-image message was altered: %q", out[1].Content)
	}
	if strings.Contains(out[2].Content, "data:application/pdf") {
		t.Errorf("pdf data not stripped: %q", out[2].Content)
	}
	if !strings.Contains(out[2].Content, "[document]") {
		t.Errorf("expected [document] placeholder, got %q", out[2].Content)
	}
	// Ensure the original slice is not mutated (StripImages copies).
	if !strings.Contains(msgs[0].Content, "data:image/") {
		t.Error("StripImagesFromMessages mutated its input")
	}
}

func TestFormatCompactSummary_StripsAnalysisKeepsSummary(t *testing.T) {
	raw := "<analysis>scratchpad thinking</analysis>\n<summary>the important part</summary>"
	got := FormatCompactSummary(raw)

	if strings.Contains(got, "scratchpad") {
		t.Errorf("analysis block not stripped: %q", got)
	}
	if !strings.Contains(got, "the important part") {
		t.Errorf("summary content lost: %q", got)
	}
	if !strings.Contains(got, "Summary:") {
		t.Errorf("expected 'Summary:' header, got %q", got)
	}
}

func TestFormatCompactSummary_PlainPassthrough(t *testing.T) {
	raw := "just some plain text"
	got := FormatCompactSummary(raw)
	if got != "just some plain text" {
		t.Errorf("plain text mangled: got %q", got)
	}
}

// --- Grouping ----------------------------------------------------------------

func TestGroupMessagesByAPIRound_EmptyIsNil(t *testing.T) {
	if g := GroupMessagesByAPIRound(nil); g != nil {
		t.Errorf("expected nil for empty input, got %v", g)
	}
}

func TestGroupMessagesByAPIRound_SplitsAtAssistantBoundaries(t *testing.T) {
	msgs := buildConvo() // user,asst,user,asst,user,asst
	groups := GroupMessagesByAPIRound(msgs)
	// Group 0 gets [user, assistant]; then each subsequent assistant starts a
	// new group with any user messages between getting prepended to it — per
	// the implementation, the user message goes into the current group, so
	// each assistant after the first opens a new group starting with that
	// assistant. The trailing user message of the prior group is attached to
	// the prior group.
	if len(groups) < 2 {
		t.Fatalf("expected >= 2 groups, got %d", len(groups))
	}
	// Total messages preserved.
	total := 0
	for _, g := range groups {
		total += len(g)
	}
	if total != len(msgs) {
		t.Errorf("grouping lost messages: total=%d, want %d", total, len(msgs))
	}
}

// --- BuildResultMessages -----------------------------------------------------

func TestBuildResultMessages_ConcatenatesSummaryAndPreserved(t *testing.T) {
	res := &CompactResult{
		SummaryMessages: []conversation.Message{
			{Role: conversation.RoleSystem, Content: "boundary"},
			{Role: conversation.RoleUser, Content: "summary"},
		},
		PreservedMessages: []conversation.Message{
			{Role: conversation.RoleAssistant, Content: "kept"},
		},
	}
	out := BuildResultMessages(res)
	if len(out) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(out))
	}
	if out[0].Content != "boundary" || out[2].Content != "kept" {
		t.Errorf("unexpected ordering: %+v", out)
	}
}

// --- AutoCompactor (autocompact.go) -----------------------------------------

func TestAutoCompactor_ShouldAutoCompact_BelowThreshold(t *testing.T) {
	fn, _ := fixedSummarizer("unused")
	svc := NewCompactService(testConfig(), fn, nil)
	ac := NewAutoCompactor(svc, nil)

	// Just a few messages — nowhere near the 170k token threshold.
	if ac.ShouldAutoCompact(buildConvo()) {
		t.Error("should not auto-compact small conversation")
	}
}

func TestAutoCompactor_ShouldAutoCompact_AboveThreshold(t *testing.T) {
	// Use a tiny context window so the threshold is trivially exceeded.
	cfg := CompactConfig{
		ContextWindowSize:       2_000,
		MaxOutputTokens:         500,
		ReservedOutputTokens:    500,
		AutoCompactBufferTokens: 100,
		Enabled:                 true,
		AutoCompactEnabled:      true,
		MaxConsecutiveFailures:  3,
		CooldownDuration:        time.Millisecond,
	}
	fn, _ := fixedSummarizer("ok")
	svc := NewCompactService(cfg, fn, nil)
	ac := NewAutoCompactor(svc, nil)

	// Build a large conversation that blows past the small threshold.
	big := make([]conversation.Message, 0, 20)
	for i := 0; i < 20; i++ {
		big = append(big, conversation.Message{
			Role:    conversation.RoleUser,
			Content: strings.Repeat("lorem ipsum ", 100),
		})
	}
	if !ac.ShouldAutoCompact(big) {
		t.Errorf("should auto-compact big conversation (tokens=%d, threshold=%d)",
			EstimateTokens(big), svc.AutoCompactThreshold())
	}
}

func TestAutoCompactor_ShouldAutoCompact_DisabledReturnsFalse(t *testing.T) {
	cfg := testConfig()
	cfg.AutoCompactEnabled = false
	fn, _ := fixedSummarizer("unused")
	svc := NewCompactService(cfg, fn, nil)
	ac := NewAutoCompactor(svc, nil)

	if ac.ShouldAutoCompact(buildConvo()) {
		t.Error("disabled auto-compact should not trigger")
	}
}

func TestAutoCompactor_CircuitBreaker(t *testing.T) {
	fn, _ := fixedSummarizer("unused")
	svc := NewCompactService(testConfig(), fn, nil)
	ac := NewAutoCompactor(svc, nil)

	// Trip the circuit breaker manually.
	for i := 0; i < svc.config.MaxConsecutiveFailures; i++ {
		ac.Tracker().RecordFailure()
	}
	if !ac.Tracker().IsCircuitBroken(svc.config.MaxConsecutiveFailures) {
		t.Fatal("tracker should report circuit broken")
	}

	// Even a huge conversation shouldn't trigger once the breaker tripped.
	big := make([]conversation.Message, 0, 50)
	for i := 0; i < 50; i++ {
		big = append(big, conversation.Message{
			Role:    conversation.RoleUser,
			Content: strings.Repeat("x", 10_000),
		})
	}
	if ac.ShouldAutoCompact(big) {
		t.Error("circuit breaker should block auto-compact")
	}
}

func TestAutoCompactor_AutoCompactIfNeeded_GuardsRecursion(t *testing.T) {
	fn, calls := fixedSummarizer("<summary>s</summary>")
	svc := NewCompactService(testConfig(), fn, nil)
	ac := NewAutoCompactor(svc, nil)

	for _, src := range []string{"compact", "session_memory"} {
		res, err := ac.AutoCompactIfNeeded(context.Background(), buildConvo(), src)
		if err != nil {
			t.Errorf("source=%s: unexpected err %v", src, err)
		}
		if res != nil {
			t.Errorf("source=%s: expected nil result (skipped), got %+v", src, res)
		}
	}
	if atomic.LoadInt32(calls) != 0 {
		t.Errorf("summarizer must not be called for guarded sources, got %d calls", *calls)
	}
}

func TestAutoCompactor_ResetTracker(t *testing.T) {
	fn, _ := fixedSummarizer("unused")
	svc := NewCompactService(testConfig(), fn, nil)
	ac := NewAutoCompactor(svc, nil)

	ac.Tracker().RecordFailure()
	ac.Tracker().IncrementTurn("t1")
	ac.Tracker().RecordSuccess() // sets Compacted=true, resets ConsecutiveFailures

	ac.ResetTracker()

	if ac.Tracker().Compacted {
		t.Error("Compacted should be false after ResetTracker")
	}
	if ac.Tracker().ConsecutiveFailures != 0 {
		t.Error("ConsecutiveFailures should be 0 after ResetTracker")
	}
	if ac.Tracker().TurnCounter != 0 {
		t.Error("TurnCounter should be 0 after ResetTracker")
	}
}

func TestAutoCompactor_CalculateTokenWarningState(t *testing.T) {
	fn, _ := fixedSummarizer("unused")
	svc := NewCompactService(DefaultConfig(), fn, nil)
	ac := NewAutoCompactor(svc, nil)

	// Zero usage: plenty of headroom.
	state := ac.CalculateTokenWarningState(0)
	if state.PercentLeft != 100 {
		t.Errorf("PercentLeft at 0 tokens = %d, want 100", state.PercentLeft)
	}
	if state.IsAboveAutoCompactThreshold {
		t.Error("should not be above auto-compact threshold at 0 tokens")
	}

	// Usage exactly at the threshold: should flip auto-compact bit on.
	state2 := ac.CalculateTokenWarningState(svc.AutoCompactThreshold())
	if !state2.IsAboveAutoCompactThreshold {
		t.Error("should be above auto-compact threshold at exactly the threshold")
	}
}

// --- AutoCompactService (integration.go) ------------------------------------

func TestAutoCompactService_Disabled_PassThrough(t *testing.T) {
	cfg := AutoCompactConfig{
		CompactFn: func(ctx context.Context, _ []string) (string, error) { return "sum", nil },
		Enabled:   false,
		Threshold: 0.5,
	}
	s := NewAutoCompactService(cfg)
	if s.IsEnabled() {
		t.Error("service should start disabled")
	}

	// Check returns false when disabled.
	if s.Check(1_000_000) {
		t.Error("disabled Check should return false")
	}

	// CompactHistory returns history unchanged when disabled.
	in := buildConvo()
	out, err := s.CompactHistory(context.Background(), in)
	if err != nil {
		t.Fatalf("CompactHistory (disabled): %v", err)
	}
	if len(out) != len(in) {
		t.Errorf("disabled CompactHistory should return input; got %d msgs, want %d",
			len(out), len(in))
	}
}

func TestAutoCompactService_Enabled_CompactsHistory(t *testing.T) {
	var callCount int32
	cfg := AutoCompactConfig{
		CompactFn: func(ctx context.Context, msgs []string) (string, error) {
			atomic.AddInt32(&callCount, 1)
			if len(msgs) == 0 {
				return "", errors.New("no messages")
			}
			return "<summary>done</summary>", nil
		},
		Enabled:   true,
		Threshold: 0.5,
	}
	s := NewAutoCompactService(cfg)
	if !s.IsEnabled() {
		t.Fatal("service should be enabled")
	}

	out, err := s.CompactHistory(context.Background(), buildConvo())
	if err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}
	if atomic.LoadInt32(&callCount) != 1 {
		t.Errorf("CompactFn called %d times, want 1", callCount)
	}
	if len(out) == 0 {
		t.Fatal("compacted history should not be empty")
	}
	// The output should start with a boundary system message.
	if out[0].Role != conversation.RoleSystem {
		t.Errorf("expected boundary system message first, got role=%s", out[0].Role)
	}
	if !strings.Contains(out[0].Content, "compact_boundary") {
		t.Errorf("expected compact_boundary marker, got %q", out[0].Content)
	}
}

func TestAutoCompactService_EnableDisableToggles(t *testing.T) {
	s := NewAutoCompactService(AutoCompactConfig{
		CompactFn: func(ctx context.Context, _ []string) (string, error) { return "x", nil },
		Enabled:   false,
	})
	if s.IsEnabled() {
		t.Fatal("expected disabled initially")
	}
	s.Enable()
	if !s.IsEnabled() {
		t.Error("Enable did not activate service")
	}
	s.Disable()
	if s.IsEnabled() {
		t.Error("Disable did not deactivate service")
	}
}

func TestAutoCompactService_Stats_AccumulateAfterRun(t *testing.T) {
	s := NewAutoCompactService(AutoCompactConfig{
		CompactFn: func(ctx context.Context, _ []string) (string, error) {
			return "<summary>ok</summary>", nil
		},
		Enabled: true,
	})

	// Before any compaction: zero counters.
	before := s.Stats()
	if got, _ := before["compactions_run"].(int); got != 0 {
		t.Errorf("initial compactions_run = %v, want 0", before["compactions_run"])
	}
	if en, _ := before["enabled"].(bool); !en {
		t.Error("stats should reflect enabled=true")
	}

	// Run a compaction.
	if _, err := s.CompactHistory(context.Background(), buildConvo()); err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}

	after := s.Stats()
	if got, _ := after["compactions_run"].(int); got != 1 {
		t.Errorf("compactions_run after = %v, want 1", after["compactions_run"])
	}
	if _, ok := after["last_compact_time"]; !ok {
		t.Error("expected last_compact_time in stats after a run")
	}
	if _, ok := after["last_pre_tokens"]; !ok {
		t.Error("expected last_pre_tokens in stats after a run")
	}
	if got, _ := after["consecutive_failures"].(int); got != 0 {
		t.Errorf("consecutive_failures after success = %v, want 0", got)
	}
}

func TestAutoCompactService_Stats_RecordsFailure(t *testing.T) {
	boom := errors.New("nope")
	s := NewAutoCompactService(AutoCompactConfig{
		CompactFn: func(ctx context.Context, _ []string) (string, error) {
			return "", boom
		},
		Enabled: true,
	})

	if _, err := s.CompactHistory(context.Background(), buildConvo()); err == nil {
		t.Fatal("expected error from failing CompactFn")
	}
	stats := s.Stats()
	if got, _ := stats["consecutive_failures"].(int); got != 1 {
		t.Errorf("consecutive_failures after failure = %v, want 1", got)
	}
	if got, _ := stats["compactions_run"].(int); got != 0 {
		t.Errorf("compactions_run after failure = %v, want 0", got)
	}
}

func TestAutoCompactService_DefaultThreshold(t *testing.T) {
	// Threshold == 0 should fall back to the default 0.80.
	s := NewAutoCompactService(AutoCompactConfig{
		CompactFn: func(ctx context.Context, _ []string) (string, error) { return "", nil },
		Enabled:   true,
		Threshold: 0,
	})
	stats := s.Stats()
	if got, _ := stats["threshold"].(float64); got != 0.80 {
		t.Errorf("default threshold = %v, want 0.80", got)
	}
}

func TestAutoCompactService_CustomThresholdClamped(t *testing.T) {
	// Out-of-range threshold should be clamped to the default.
	s := NewAutoCompactService(AutoCompactConfig{
		CompactFn: func(ctx context.Context, _ []string) (string, error) { return "", nil },
		Enabled:   true,
		Threshold: 1.5, // invalid: > 1.0
	})
	stats := s.Stats()
	if got, _ := stats["threshold"].(float64); got != 0.80 {
		t.Errorf("invalid threshold should fall back to 0.80, got %v", got)
	}
}

func TestAutoCompactService_CompactServiceAccessor(t *testing.T) {
	s := NewAutoCompactService(AutoCompactConfig{
		CompactFn: func(ctx context.Context, _ []string) (string, error) { return "", nil },
		Enabled:   true,
	})
	if s.CompactService() == nil {
		t.Error("CompactService() should return non-nil service")
	}
}
