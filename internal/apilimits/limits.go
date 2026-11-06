// Package apilimits provides API rate limit tracking, cost tracking,
// and token estimation, migrated from the TypeScript claudeAiLimits.ts,
// rateLimitMessages.ts, and policyLimits.ts modules.
package apilimits

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// QuotaStatus represents the current rate limit status from the API.
type QuotaStatus string

const (
	QuotaAllowed        QuotaStatus = "allowed"
	QuotaAllowedWarning QuotaStatus = "allowed_warning"
	QuotaRejected       QuotaStatus = "rejected"
)

// RateLimitType identifies which quota window is applicable.
type RateLimitType string

const (
	RateLimitFiveHour       RateLimitType = "five_hour"
	RateLimitSevenDay       RateLimitType = "seven_day"
	RateLimitSevenDayOpus   RateLimitType = "seven_day_opus"
	RateLimitSevenDaySonnet RateLimitType = "seven_day_sonnet"
	RateLimitOverage        RateLimitType = "overage"
)

// RateLimitDisplayName returns a user-facing name for the rate limit type.
func RateLimitDisplayName(t RateLimitType) string {
	switch t {
	case RateLimitFiveHour:
		return "session limit"
	case RateLimitSevenDay:
		return "weekly limit"
	case RateLimitSevenDayOpus:
		return "Opus limit"
	case RateLimitSevenDaySonnet:
		return "Sonnet limit"
	case RateLimitOverage:
		return "extra usage limit"
	default:
		return string(t)
	}
}

// Tier represents a user subscription tier.
type Tier string

const (
	TierFree       Tier = "free"
	TierPro        Tier = "pro"
	TierMax        Tier = "max"
	TierTeam       Tier = "team"
	TierEnterprise Tier = "enterprise"
)

// APILimits holds the rate limits for a given model+tier combination.
type APILimits struct {
	RequestsPerMinute int `json:"requests_per_minute"`
	TokensPerMinute   int `json:"tokens_per_minute"`
	TokensPerDay      int `json:"tokens_per_day"`
}

// LimitsForModel returns default API limits for a model.
// These are approximate values; actual limits come from the API response headers.
func LimitsForModel(model string) APILimits {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "opus"):
		return APILimits{
			RequestsPerMinute: 50,
			TokensPerMinute:   40000,
			TokensPerDay:      1000000,
		}
	case strings.Contains(m, "haiku"):
		return APILimits{
			RequestsPerMinute: 100,
			TokensPerMinute:   100000,
			TokensPerDay:      5000000,
		}
	default: // sonnet and others
		return APILimits{
			RequestsPerMinute: 60,
			TokensPerMinute:   80000,
			TokensPerDay:      2000000,
		}
	}
}

// LimitsForTier returns default API limits for a subscription tier.
func LimitsForTier(tier Tier) APILimits {
	switch tier {
	case TierFree:
		return APILimits{
			RequestsPerMinute: 5,
			TokensPerMinute:   20000,
			TokensPerDay:      300000,
		}
	case TierPro:
		return APILimits{
			RequestsPerMinute: 50,
			TokensPerMinute:   80000,
			TokensPerDay:      2000000,
		}
	case TierMax, TierTeam:
		return APILimits{
			RequestsPerMinute: 100,
			TokensPerMinute:   160000,
			TokensPerDay:      5000000,
		}
	case TierEnterprise:
		return APILimits{
			RequestsPerMinute: 200,
			TokensPerMinute:   320000,
			TokensPerDay:      10000000,
		}
	default:
		return LimitsForTier(TierFree)
	}
}

// --- ClaudeAILimits (from the API response headers) ---

// OverageDisabledReason explains why overage is not available.
type OverageDisabledReason string

const (
	OverageNotProvisioned      OverageDisabledReason = "overage_not_provisioned"
	OverageOrgDisabled         OverageDisabledReason = "org_level_disabled"
	OverageOrgDisabledUntil    OverageDisabledReason = "org_level_disabled_until"
	OverageOutOfCredits        OverageDisabledReason = "out_of_credits"
	OverageSeatTierDisabled    OverageDisabledReason = "seat_tier_level_disabled"
	OverageMemberDisabled      OverageDisabledReason = "member_level_disabled"
	OverageSeatTierZeroCredits OverageDisabledReason = "seat_tier_zero_credit_limit"
	OverageGroupZeroCredits    OverageDisabledReason = "group_zero_credit_limit"
	OverageMemberZeroCredits   OverageDisabledReason = "member_zero_credit_limit"
	OverageOrgServiceDisabled  OverageDisabledReason = "org_service_level_disabled"
	OverageOrgServiceZero      OverageDisabledReason = "org_service_zero_credit_limit"
	OverageNoLimitsConfigured  OverageDisabledReason = "no_limits_configured"
	OverageUnknown             OverageDisabledReason = "unknown"
)

// ClaudeAILimits holds the parsed rate limit state from API response headers.
type ClaudeAILimits struct {
	Status                            QuotaStatus           `json:"status"`
	UnifiedRateLimitFallbackAvailable bool                  `json:"unified_rate_limit_fallback_available"`
	ResetsAt                          *float64              `json:"resets_at,omitempty"` // unix epoch seconds
	RateLimitType                     RateLimitType         `json:"rate_limit_type,omitempty"`
	Utilization                       *float64              `json:"utilization,omitempty"` // 0-1
	OverageStatus                     QuotaStatus           `json:"overage_status,omitempty"`
	OverageResetsAt                   *float64              `json:"overage_resets_at,omitempty"`
	OverageDisabledReason             OverageDisabledReason `json:"overage_disabled_reason,omitempty"`
	IsUsingOverage                    bool                  `json:"is_using_overage"`
	SurpassedThreshold                *float64              `json:"surpassed_threshold,omitempty"`
}

// DefaultClaudeAILimits returns the initial/default limits state.
func DefaultClaudeAILimits() ClaudeAILimits {
	return ClaudeAILimits{
		Status:                            QuotaAllowed,
		UnifiedRateLimitFallbackAvailable: false,
		IsUsingOverage:                    false,
	}
}

// --- RateLimitTracker ---

// RateLimitTracker tracks usage against API limits in real-time.
type RateLimitTracker struct {
	mu             sync.Mutex
	limits         APILimits
	minuteRequests []time.Time
	minuteTokens   []tokenEntry
	dayTokens      int
	dayStart       time.Time
	currentLimits  ClaudeAILimits
	listeners      []func(ClaudeAILimits)
}

type tokenEntry struct {
	ts     time.Time
	tokens int
}

// NewRateLimitTracker creates a tracker with the given limits.
func NewRateLimitTracker(limits APILimits) *RateLimitTracker {
	return &RateLimitTracker{
		limits:        limits,
		dayStart:      startOfDay(time.Now()),
		currentLimits: DefaultClaudeAILimits(),
	}
}

// OnStatusChange registers a callback for limit state changes.
func (rt *RateLimitTracker) OnStatusChange(fn func(ClaudeAILimits)) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.listeners = append(rt.listeners, fn)
}

// RecordRequest records a request with the given token count.
func (rt *RateLimitTracker) RecordRequest(tokens int) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	now := time.Now()

	// Reset daily counter if needed
	dayStart := startOfDay(now)
	if dayStart.After(rt.dayStart) {
		rt.dayTokens = 0
		rt.dayStart = dayStart
	}

	rt.minuteRequests = append(rt.minuteRequests, now)
	rt.minuteTokens = append(rt.minuteTokens, tokenEntry{ts: now, tokens: tokens})
	rt.dayTokens += tokens
	rt.pruneOld(now)
}

// UpdateLimits replaces the current API limits (e.g., from header-based info).
func (rt *RateLimitTracker) UpdateLimits(limits APILimits) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.limits = limits
}

// UpdateClaudeAILimits sets the parsed quota state from API headers and notifies listeners.
func (rt *RateLimitTracker) UpdateClaudeAILimits(newLimits ClaudeAILimits) {
	rt.mu.Lock()
	old := rt.currentLimits
	rt.currentLimits = newLimits
	listeners := make([]func(ClaudeAILimits), len(rt.listeners))
	copy(listeners, rt.listeners)
	rt.mu.Unlock()

	if old != newLimits {
		for _, fn := range listeners {
			fn(newLimits)
		}
	}
}

// CurrentLimits returns the latest ClaudeAILimits state.
func (rt *RateLimitTracker) CurrentLimits() ClaudeAILimits {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.currentLimits
}

// IsApproachingLimit returns true if usage is at or above the given threshold (0-1).
func (rt *RateLimitTracker) IsApproachingLimit(threshold float64) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	now := time.Now()
	rt.pruneOld(now)

	if rt.limits.RequestsPerMinute > 0 {
		if float64(len(rt.minuteRequests))/float64(rt.limits.RequestsPerMinute) >= threshold {
			return true
		}
	}
	if rt.limits.TokensPerMinute > 0 {
		sum := 0
		for _, e := range rt.minuteTokens {
			sum += e.tokens
		}
		if float64(sum)/float64(rt.limits.TokensPerMinute) >= threshold {
			return true
		}
	}
	if rt.limits.TokensPerDay > 0 {
		if float64(rt.dayTokens)/float64(rt.limits.TokensPerDay) >= threshold {
			return true
		}
	}
	return false
}

// IsAtLimit returns true if any limit is fully exhausted.
func (rt *RateLimitTracker) IsAtLimit() bool {
	return rt.IsApproachingLimit(1.0)
}

func (rt *RateLimitTracker) pruneOld(now time.Time) {
	cutoff := now.Add(-time.Minute)
	i := 0
	for i < len(rt.minuteRequests) && rt.minuteRequests[i].Before(cutoff) {
		i++
	}
	rt.minuteRequests = rt.minuteRequests[i:]

	j := 0
	for j < len(rt.minuteTokens) && rt.minuteTokens[j].ts.Before(cutoff) {
		j++
	}
	rt.minuteTokens = rt.minuteTokens[j:]
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// --- Rate limit messages ---

// RateLimitMessageSeverity is the severity of a rate limit message.
type RateLimitMessageSeverity string

const (
	SeverityMsgError   RateLimitMessageSeverity = "error"
	SeverityMsgWarning RateLimitMessageSeverity = "warning"
)

// RateLimitMessage is a user-facing rate limit notification.
type RateLimitMessage struct {
	Message  string
	Severity RateLimitMessageSeverity
}

// RATE_LIMIT_ERROR_PREFIXES are the known prefixes of rate-limit error messages.
var RATE_LIMIT_ERROR_PREFIXES = []string{
	"You've hit your",
	"You've used",
	"You're now using extra usage",
	"You're close to",
	"You're out of extra usage",
}

// IsRateLimitErrorMessage checks whether text starts with a known rate limit prefix.
func IsRateLimitErrorMessage(text string) bool {
	for _, p := range RATE_LIMIT_ERROR_PREFIXES {
		if strings.HasPrefix(text, p) {
			return true
		}
	}
	return false
}

// GetRateLimitMessage determines the appropriate user-facing message.
// Returns nil when no message should be shown.
func GetRateLimitMessage(limits ClaudeAILimits) *RateLimitMessage {
	// Overage scenarios
	if limits.IsUsingOverage {
		if limits.OverageStatus == QuotaAllowedWarning {
			return &RateLimitMessage{
				Message:  "You're close to your extra usage spending limit",
				Severity: SeverityMsgWarning,
			}
		}
		return nil
	}

	// Hard rejection
	if limits.Status == QuotaRejected {
		return &RateLimitMessage{
			Message:  formatLimitReached(limits),
			Severity: SeverityMsgError,
		}
	}

	// Early warning
	if limits.Status == QuotaAllowedWarning {
		const warningThreshold = 0.7
		if limits.Utilization != nil && *limits.Utilization < warningThreshold {
			return nil
		}
		text := formatEarlyWarning(limits)
		if text != "" {
			return &RateLimitMessage{
				Message:  text,
				Severity: SeverityMsgWarning,
			}
		}
	}

	return nil
}

// GetRateLimitErrorMessage returns only error-severity rate limit messages, or "".
func GetRateLimitErrorMessage(limits ClaudeAILimits) string {
	msg := GetRateLimitMessage(limits)
	if msg != nil && msg.Severity == SeverityMsgError {
		return msg.Message
	}
	return ""
}

// GetRateLimitWarning returns only warning-severity rate limit messages, or "".
func GetRateLimitWarning(limits ClaudeAILimits) string {
	msg := GetRateLimitMessage(limits)
	if msg != nil && msg.Severity == SeverityMsgWarning {
		return msg.Message
	}
	return ""
}

// GetUsingOverageText returns the notification shown when entering overage mode.
func GetUsingOverageText(limits ClaudeAILimits) string {
	limitName := ""
	switch limits.RateLimitType {
	case RateLimitFiveHour:
		limitName = "session limit"
	case RateLimitSevenDay:
		limitName = "weekly limit"
	case RateLimitSevenDayOpus:
		limitName = "Opus limit"
	case RateLimitSevenDaySonnet:
		limitName = "Sonnet limit"
	}
	if limitName == "" {
		return "Now using extra usage"
	}
	resetMsg := ""
	if limits.ResetsAt != nil {
		resetMsg = fmt.Sprintf(" · Your %s resets %s", limitName, formatResetTime(*limits.ResetsAt))
	}
	return fmt.Sprintf("You're now using extra usage%s", resetMsg)
}

func formatLimitReached(limits ClaudeAILimits) string {
	resetMsg := ""
	if limits.ResetsAt != nil {
		resetMsg = fmt.Sprintf(" · resets %s", formatResetTime(*limits.ResetsAt))
	}

	if limits.OverageStatus == QuotaRejected {
		if limits.OverageDisabledReason == OverageOutOfCredits {
			return fmt.Sprintf("You're out of extra usage%s", resetMsg)
		}
		return fmt.Sprintf("You've hit your limit%s", resetMsg)
	}

	switch limits.RateLimitType {
	case RateLimitSevenDaySonnet:
		return fmt.Sprintf("You've hit your Sonnet limit%s", resetMsg)
	case RateLimitSevenDayOpus:
		return fmt.Sprintf("You've hit your Opus limit%s", resetMsg)
	case RateLimitSevenDay:
		return fmt.Sprintf("You've hit your weekly limit%s", resetMsg)
	case RateLimitFiveHour:
		return fmt.Sprintf("You've hit your session limit%s", resetMsg)
	default:
		return fmt.Sprintf("You've hit your usage limit%s", resetMsg)
	}
}

func formatEarlyWarning(limits ClaudeAILimits) string {
	limitName := ""
	switch limits.RateLimitType {
	case RateLimitSevenDay:
		limitName = "weekly limit"
	case RateLimitFiveHour:
		limitName = "session limit"
	case RateLimitSevenDayOpus:
		limitName = "Opus limit"
	case RateLimitSevenDaySonnet:
		limitName = "Sonnet limit"
	case RateLimitOverage:
		limitName = "extra usage"
	case "":
		return ""
	default:
		limitName = string(limits.RateLimitType)
	}

	if limits.Utilization != nil {
		used := int(math.Floor(*limits.Utilization * 100))
		if limits.ResetsAt != nil {
			return fmt.Sprintf("You've used %d%% of your %s · resets %s",
				used, limitName, formatResetTime(*limits.ResetsAt))
		}
		return fmt.Sprintf("You've used %d%% of your %s", used, limitName)
	}

	if limits.RateLimitType == RateLimitOverage {
		limitName += " limit"
	}
	if limits.ResetsAt != nil {
		return fmt.Sprintf("Approaching %s · resets %s", limitName, formatResetTime(*limits.ResetsAt))
	}
	return fmt.Sprintf("Approaching %s", limitName)
}

// formatResetTime formats a unix epoch timestamp (seconds) as a relative time description.
func formatResetTime(epochSeconds float64) string {
	resetTime := time.Unix(int64(epochSeconds), 0)
	d := time.Until(resetTime)
	if d < 0 {
		return "now"
	}
	if d < time.Minute {
		return "in <1 min"
	}
	if d < time.Hour {
		return fmt.Sprintf("in %d min", int(d.Minutes()))
	}
	hours := int(d.Hours())
	if hours < 24 {
		return fmt.Sprintf("in %dh", hours)
	}
	days := hours / 24
	return fmt.Sprintf("in %dd", days)
}
