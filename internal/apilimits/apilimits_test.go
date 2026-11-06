package apilimits

import (
	"strings"
	"testing"
)

func TestLimitsForModel_Known(t *testing.T) {
	knownModels := []string{
		"claude-opus-4-20250514",
		"claude-sonnet-4-20250514",
		"claude-3-5-haiku-20241022",
		"claude-3-opus",
		"claude-3-haiku",
		"claude-3-5-sonnet",
	}

	for _, id := range knownModels {
		t.Run(id, func(t *testing.T) {
			limits := LimitsForModel(id)
			if limits.RequestsPerMinute <= 0 {
				t.Errorf("expected positive RequestsPerMinute for %s, got %d", id, limits.RequestsPerMinute)
			}
			if limits.TokensPerMinute <= 0 {
				t.Errorf("expected positive TokensPerMinute for %s, got %d", id, limits.TokensPerMinute)
			}
			if limits.TokensPerDay <= 0 {
				t.Errorf("expected positive TokensPerDay for %s, got %d", id, limits.TokensPerDay)
			}
		})
	}
}

func TestLimitsForModel_Variants(t *testing.T) {
	opus := LimitsForModel("claude-3-opus")
	haiku := LimitsForModel("claude-3-haiku")
	sonnet := LimitsForModel("claude-3-5-sonnet")

	// Opus should be the most conservative on RPM (50)
	if opus.RequestsPerMinute != 50 {
		t.Errorf("expected opus RPM=50, got %d", opus.RequestsPerMinute)
	}
	// Haiku should be the most permissive on RPM (100)
	if haiku.RequestsPerMinute != 100 {
		t.Errorf("expected haiku RPM=100, got %d", haiku.RequestsPerMinute)
	}
	// Sonnet gets the default branch (60)
	if sonnet.RequestsPerMinute != 60 {
		t.Errorf("expected sonnet RPM=60, got %d", sonnet.RequestsPerMinute)
	}

	// Haiku should allow more daily tokens than opus
	if haiku.TokensPerDay <= opus.TokensPerDay {
		t.Errorf("expected haiku TokensPerDay > opus TokensPerDay, got haiku=%d opus=%d",
			haiku.TokensPerDay, opus.TokensPerDay)
	}
}

func TestLimitsForModel_CaseInsensitive(t *testing.T) {
	lower := LimitsForModel("claude-3-opus")
	upper := LimitsForModel("CLAUDE-3-OPUS")
	mixed := LimitsForModel("Claude-3-Opus")

	if lower != upper || lower != mixed {
		t.Errorf("expected case-insensitive matching: lower=%+v upper=%+v mixed=%+v",
			lower, upper, mixed)
	}
}

func TestLimitsForModel_Unknown(t *testing.T) {
	limits := LimitsForModel("nonexistent-model-xyz")
	// Should fall through to the default (sonnet-like) branch.
	if limits.RequestsPerMinute <= 0 || limits.TokensPerMinute <= 0 || limits.TokensPerDay <= 0 {
		t.Errorf("expected non-zero default limits, got %+v", limits)
	}
	if limits.RequestsPerMinute != 60 {
		t.Errorf("expected default RPM=60 for unknown model, got %d", limits.RequestsPerMinute)
	}
}

func TestLimitsForModel_Empty(t *testing.T) {
	// Should not panic and should return the default (sonnet) limits.
	limits := LimitsForModel("")
	if limits.RequestsPerMinute != 60 {
		t.Errorf("expected default RPM=60 for empty model, got %d", limits.RequestsPerMinute)
	}
}

func TestLimitsForTier_AllTiers(t *testing.T) {
	tiers := []Tier{TierFree, TierPro, TierMax, TierTeam, TierEnterprise}
	for _, tier := range tiers {
		t.Run(string(tier), func(t *testing.T) {
			limits := LimitsForTier(tier)
			if limits.RequestsPerMinute <= 0 {
				t.Errorf("tier %s: expected positive RPM, got %d", tier, limits.RequestsPerMinute)
			}
			if limits.TokensPerMinute <= 0 {
				t.Errorf("tier %s: expected positive TPM, got %d", tier, limits.TokensPerMinute)
			}
			if limits.TokensPerDay <= 0 {
				t.Errorf("tier %s: expected positive daily tokens, got %d", tier, limits.TokensPerDay)
			}
		})
	}
}

func TestLimitsForTier_Ordering(t *testing.T) {
	free := LimitsForTier(TierFree)
	pro := LimitsForTier(TierPro)
	max := LimitsForTier(TierMax)
	enterprise := LimitsForTier(TierEnterprise)

	// Free < Pro < Max <= Enterprise (in RPM)
	if free.RequestsPerMinute >= pro.RequestsPerMinute {
		t.Errorf("expected free RPM < pro RPM, got free=%d pro=%d",
			free.RequestsPerMinute, pro.RequestsPerMinute)
	}
	if pro.RequestsPerMinute >= max.RequestsPerMinute {
		t.Errorf("expected pro RPM < max RPM, got pro=%d max=%d",
			pro.RequestsPerMinute, max.RequestsPerMinute)
	}
	if max.RequestsPerMinute > enterprise.RequestsPerMinute {
		t.Errorf("expected max RPM <= enterprise RPM, got max=%d enterprise=%d",
			max.RequestsPerMinute, enterprise.RequestsPerMinute)
	}
}

func TestLimitsForTier_Unknown(t *testing.T) {
	unknown := LimitsForTier(Tier("bogus"))
	free := LimitsForTier(TierFree)
	if unknown != free {
		t.Errorf("expected unknown tier to fall back to free tier, got %+v vs %+v", unknown, free)
	}
}

func TestLimitsForTier_TeamEqualsMax(t *testing.T) {
	team := LimitsForTier(TierTeam)
	max := LimitsForTier(TierMax)
	if team != max {
		t.Errorf("expected team and max tiers to share limits, got team=%+v max=%+v", team, max)
	}
}

func TestRateLimitDisplayName(t *testing.T) {
	cases := map[RateLimitType]string{
		RateLimitFiveHour:       "session limit",
		RateLimitSevenDay:       "weekly limit",
		RateLimitSevenDayOpus:   "Opus limit",
		RateLimitSevenDaySonnet: "Sonnet limit",
		RateLimitOverage:        "extra usage limit",
	}
	for rt, expected := range cases {
		if got := RateLimitDisplayName(rt); got != expected {
			t.Errorf("RateLimitDisplayName(%q) = %q, want %q", rt, got, expected)
		}
	}

	// Unknown type returns the raw string.
	if got := RateLimitDisplayName(RateLimitType("custom_limit")); got != "custom_limit" {
		t.Errorf("expected pass-through for unknown RateLimitType, got %q", got)
	}
}

func TestDefaultClaudeAILimits(t *testing.T) {
	d := DefaultClaudeAILimits()
	if d.Status != QuotaAllowed {
		t.Errorf("expected default Status=allowed, got %q", d.Status)
	}
	if d.UnifiedRateLimitFallbackAvailable {
		t.Errorf("expected UnifiedRateLimitFallbackAvailable=false")
	}
	if d.IsUsingOverage {
		t.Errorf("expected IsUsingOverage=false")
	}
}

func TestIsRateLimitErrorMessage(t *testing.T) {
	matches := []string{
		"You've hit your weekly limit",
		"You've used 80% of your session limit",
		"You're now using extra usage",
		"You're close to your limit",
		"You're out of extra usage",
	}
	for _, m := range matches {
		if !IsRateLimitErrorMessage(m) {
			t.Errorf("expected true for %q", m)
		}
	}

	nonMatches := []string{
		"",
		"random error",
		"Internal server error",
		"you've hit your weekly limit", // lower-case start
	}
	for _, m := range nonMatches {
		if IsRateLimitErrorMessage(m) {
			t.Errorf("expected false for %q", m)
		}
	}
}

func TestGetRateLimitMessage_Rejected(t *testing.T) {
	limits := ClaudeAILimits{
		Status:        QuotaRejected,
		RateLimitType: RateLimitSevenDay,
	}
	msg := GetRateLimitMessage(limits)
	if msg == nil {
		t.Fatal("expected a message for rejected status")
	}
	if msg.Severity != SeverityMsgError {
		t.Errorf("expected error severity, got %q", msg.Severity)
	}
	if !strings.Contains(msg.Message, "weekly limit") {
		t.Errorf("expected message to mention 'weekly limit', got %q", msg.Message)
	}
}

func TestGetRateLimitMessage_Allowed(t *testing.T) {
	limits := ClaudeAILimits{Status: QuotaAllowed}
	if msg := GetRateLimitMessage(limits); msg != nil {
		t.Errorf("expected nil message for allowed status, got %+v", msg)
	}
}

func TestGetRateLimitMessage_WarningBelowThreshold(t *testing.T) {
	util := 0.5
	limits := ClaudeAILimits{
		Status:        QuotaAllowedWarning,
		RateLimitType: RateLimitSevenDay,
		Utilization:   &util,
	}
	if msg := GetRateLimitMessage(limits); msg != nil {
		t.Errorf("expected nil message when utilization < warning threshold, got %+v", msg)
	}
}

func TestGetRateLimitMessage_WarningAboveThreshold(t *testing.T) {
	util := 0.85
	limits := ClaudeAILimits{
		Status:        QuotaAllowedWarning,
		RateLimitType: RateLimitSevenDay,
		Utilization:   &util,
	}
	msg := GetRateLimitMessage(limits)
	if msg == nil {
		t.Fatal("expected a warning message above threshold")
	}
	if msg.Severity != SeverityMsgWarning {
		t.Errorf("expected warning severity, got %q", msg.Severity)
	}
	if !strings.Contains(msg.Message, "85%") {
		t.Errorf("expected message to include '85%%', got %q", msg.Message)
	}
}

func TestGetRateLimitErrorMessage(t *testing.T) {
	limits := ClaudeAILimits{
		Status:        QuotaRejected,
		RateLimitType: RateLimitFiveHour,
	}
	errMsg := GetRateLimitErrorMessage(limits)
	if errMsg == "" {
		t.Fatal("expected non-empty error message")
	}
	if !strings.Contains(errMsg, "session limit") {
		t.Errorf("expected error message to mention 'session limit', got %q", errMsg)
	}

	// Allowed -> empty string.
	if got := GetRateLimitErrorMessage(ClaudeAILimits{Status: QuotaAllowed}); got != "" {
		t.Errorf("expected empty for allowed, got %q", got)
	}
}

func TestRateLimitTracker_RecordAndApproach(t *testing.T) {
	limits := APILimits{
		RequestsPerMinute: 10,
		TokensPerMinute:   1000,
		TokensPerDay:      100000,
	}
	tracker := NewRateLimitTracker(limits)

	if tracker.IsAtLimit() {
		t.Error("fresh tracker should not be at limit")
	}
	if tracker.IsApproachingLimit(0.5) {
		t.Error("fresh tracker should not be approaching 50% limit")
	}

	// Record enough requests to cross 50% of the RPM window.
	for i := 0; i < 6; i++ {
		tracker.RecordRequest(50)
	}
	if !tracker.IsApproachingLimit(0.5) {
		t.Error("expected to be approaching 50% limit after 6/10 requests")
	}
	if tracker.IsAtLimit() {
		t.Error("should not be at limit after 6/10 requests")
	}

	// Push over the full RPM limit.
	for i := 0; i < 5; i++ {
		tracker.RecordRequest(50)
	}
	if !tracker.IsAtLimit() {
		t.Error("expected to be at limit after 11/10 requests")
	}
}

func TestRateLimitTracker_UpdateClaudeAILimits_Notifies(t *testing.T) {
	tracker := NewRateLimitTracker(APILimits{RequestsPerMinute: 10, TokensPerMinute: 1000, TokensPerDay: 10000})

	var called int
	tracker.OnStatusChange(func(l ClaudeAILimits) {
		called++
	})

	newLimits := ClaudeAILimits{Status: QuotaAllowedWarning}
	tracker.UpdateClaudeAILimits(newLimits)
	if called != 1 {
		t.Errorf("expected listener to be called once, got %d", called)
	}

	// Same value again - should not trigger.
	tracker.UpdateClaudeAILimits(newLimits)
	if called != 1 {
		t.Errorf("expected listener to NOT be called for identical state, got %d", called)
	}

	// Different value - should trigger.
	tracker.UpdateClaudeAILimits(ClaudeAILimits{Status: QuotaRejected})
	if called != 2 {
		t.Errorf("expected listener to be called again for changed state, got %d", called)
	}

	current := tracker.CurrentLimits()
	if current.Status != QuotaRejected {
		t.Errorf("expected CurrentLimits Status=rejected, got %q", current.Status)
	}
}
