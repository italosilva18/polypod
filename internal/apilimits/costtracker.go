package apilimits

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// ModelCosts holds per-million-token pricing for a model.
type ModelCosts struct {
	InputTokens            float64 `json:"input_tokens"`              // $ per 1M input tokens
	OutputTokens           float64 `json:"output_tokens"`             // $ per 1M output tokens
	PromptCacheWriteTokens float64 `json:"prompt_cache_write_tokens"` // $ per 1M cache-write tokens
	PromptCacheReadTokens  float64 `json:"prompt_cache_read_tokens"`  // $ per 1M cache-read tokens
	WebSearchRequests      float64 `json:"web_search_requests"`       // $ per request
}

// Standard pricing tiers ($ per 1M tokens unless noted)
var (
	CostTier3_15 = ModelCosts{
		InputTokens: 3, OutputTokens: 15,
		PromptCacheWriteTokens: 3.75, PromptCacheReadTokens: 0.3,
		WebSearchRequests: 0.01,
	}
	CostTier15_75 = ModelCosts{
		InputTokens: 15, OutputTokens: 75,
		PromptCacheWriteTokens: 18.75, PromptCacheReadTokens: 1.5,
		WebSearchRequests: 0.01,
	}
	CostTier5_25 = ModelCosts{
		InputTokens: 5, OutputTokens: 25,
		PromptCacheWriteTokens: 6.25, PromptCacheReadTokens: 0.5,
		WebSearchRequests: 0.01,
	}
	CostTier30_150 = ModelCosts{
		InputTokens: 30, OutputTokens: 150,
		PromptCacheWriteTokens: 37.5, PromptCacheReadTokens: 3,
		WebSearchRequests: 0.01,
	}
	CostHaiku35 = ModelCosts{
		InputTokens: 0.8, OutputTokens: 4,
		PromptCacheWriteTokens: 1, PromptCacheReadTokens: 0.08,
		WebSearchRequests: 0.01,
	}
	CostHaiku45 = ModelCosts{
		InputTokens: 1, OutputTokens: 5,
		PromptCacheWriteTokens: 1.25, PromptCacheReadTokens: 0.1,
		WebSearchRequests: 0.01,
	}
)

// ModelCostsTable maps canonical model short names to their pricing.
var ModelCostsTable = map[string]ModelCosts{
	"claude-3-5-haiku":  CostHaiku35,
	"claude-haiku-4-5":  CostHaiku45,
	"claude-3-5-sonnet": CostTier3_15,
	"claude-3-7-sonnet": CostTier3_15,
	"claude-sonnet-4":   CostTier3_15,
	"claude-sonnet-4-5": CostTier3_15,
	"claude-sonnet-4-6": CostTier3_15,
	"claude-opus-4":     CostTier15_75,
	"claude-opus-4-1":   CostTier15_75,
	"claude-opus-4-5":   CostTier5_25,
	"claude-opus-4-6":   CostTier5_25,
}

// defaultUnknownModelCost is used when a model is not in the table.
var defaultUnknownModelCost = CostTier5_25

// GetModelCosts looks up costs for a model, falling back to default.
func GetModelCosts(model string) ModelCosts {
	m := strings.ToLower(model)
	// Direct lookup
	if costs, ok := ModelCostsTable[m]; ok {
		return costs
	}
	// Prefix match
	for name, costs := range ModelCostsTable {
		if strings.Contains(m, name) {
			return costs
		}
	}
	return defaultUnknownModelCost
}

// --- TokenUsage ---

// TokenUsage holds the token counts from a single API call.
type TokenUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	WebSearchRequests        int `json:"web_search_requests"`
}

// CalculateUSDCost computes the dollar cost for a single API call.
func CalculateUSDCost(model string, usage TokenUsage) float64 {
	costs := GetModelCosts(model)
	return tokensToUSDCost(costs, usage)
}

func tokensToUSDCost(costs ModelCosts, usage TokenUsage) float64 {
	return (float64(usage.InputTokens)/1_000_000)*costs.InputTokens +
		(float64(usage.OutputTokens)/1_000_000)*costs.OutputTokens +
		(float64(usage.CacheReadInputTokens)/1_000_000)*costs.PromptCacheReadTokens +
		(float64(usage.CacheCreationInputTokens)/1_000_000)*costs.PromptCacheWriteTokens +
		float64(usage.WebSearchRequests)*costs.WebSearchRequests
}

// FormatModelPricing returns e.g. "$3/$15 per Mtok".
func FormatModelPricing(costs ModelCosts) string {
	return fmt.Sprintf("%s/%s per Mtok", formatPrice(costs.InputTokens), formatPrice(costs.OutputTokens))
}

// GetModelPricingString returns a formatted pricing string for a model, or "" if not found.
func GetModelPricingString(model string) string {
	m := strings.ToLower(model)
	if costs, ok := ModelCostsTable[m]; ok {
		return FormatModelPricing(costs)
	}
	for name, costs := range ModelCostsTable {
		if strings.Contains(m, name) {
			return FormatModelPricing(costs)
		}
	}
	return ""
}

func formatPrice(price float64) string {
	if price == math.Trunc(price) {
		return fmt.Sprintf("$%d", int(price))
	}
	return fmt.Sprintf("$%.2f", price)
}

// --- CostTracker ---

// ModelUsageAccum tracks accumulated usage for a single model within a session.
type ModelUsageAccum struct {
	InputTokens              int     `json:"input_tokens"`
	OutputTokens             int     `json:"output_tokens"`
	CacheReadInputTokens     int     `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int     `json:"cache_creation_input_tokens"`
	WebSearchRequests        int     `json:"web_search_requests"`
	CostUSD                  float64 `json:"cost_usd"`
}

// CostTracker accumulates API costs across a session and provides budget enforcement.
type CostTracker struct {
	mu sync.Mutex

	sessionCost float64
	dailyCost   float64
	monthlyCost float64
	dayKey      string // "2006-01-02"
	monthKey    string // "2006-01"

	totalInputTokens              int
	totalOutputTokens             int
	totalCacheReadInputTokens     int
	totalCacheCreationInputTokens int
	totalWebSearchRequests        int

	modelUsage map[string]*ModelUsageAccum

	// Budget enforcement
	dailyBudget   float64 // 0 = unlimited
	monthlyBudget float64 // 0 = unlimited
	warningPct    float64 // threshold (0-1) to fire budget warning (default 0.8)
	onBudgetAlert func(pct float64, message string)

	hasUnknownModelCost bool
}

// NewCostTracker creates a cost tracker with optional budget limits.
func NewCostTracker(dailyBudget, monthlyBudget float64) *CostTracker {
	now := time.Now()
	return &CostTracker{
		dayKey:        now.Format("2006-01-02"),
		monthKey:      now.Format("2006-01"),
		modelUsage:    make(map[string]*ModelUsageAccum),
		dailyBudget:   dailyBudget,
		monthlyBudget: monthlyBudget,
		warningPct:    0.8,
	}
}

// SetBudgetAlert sets the callback fired when approaching a budget limit.
func (ct *CostTracker) SetBudgetAlert(fn func(pct float64, message string)) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.onBudgetAlert = fn
}

// SetWarningPct sets the threshold for budget warnings (default 0.8).
func (ct *CostTracker) SetWarningPct(pct float64) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.warningPct = pct
}

// TrackAPICall records the cost and token usage from a single API response.
func (ct *CostTracker) TrackAPICall(model string, usage TokenUsage) float64 {
	cost := CalculateUSDCost(model, usage)

	ct.mu.Lock()
	defer ct.mu.Unlock()

	// Roll over day/month if needed
	now := time.Now()
	if now.Format("2006-01-02") != ct.dayKey {
		ct.dailyCost = 0
		ct.dayKey = now.Format("2006-01-02")
	}
	if now.Format("2006-01") != ct.monthKey {
		ct.monthlyCost = 0
		ct.monthKey = now.Format("2006-01")
	}

	ct.sessionCost += cost
	ct.dailyCost += cost
	ct.monthlyCost += cost

	ct.totalInputTokens += usage.InputTokens
	ct.totalOutputTokens += usage.OutputTokens
	ct.totalCacheReadInputTokens += usage.CacheReadInputTokens
	ct.totalCacheCreationInputTokens += usage.CacheCreationInputTokens
	ct.totalWebSearchRequests += usage.WebSearchRequests

	// Model-level accumulation
	ma, ok := ct.modelUsage[model]
	if !ok {
		ma = &ModelUsageAccum{}
		ct.modelUsage[model] = ma
	}
	ma.InputTokens += usage.InputTokens
	ma.OutputTokens += usage.OutputTokens
	ma.CacheReadInputTokens += usage.CacheReadInputTokens
	ma.CacheCreationInputTokens += usage.CacheCreationInputTokens
	ma.WebSearchRequests += usage.WebSearchRequests
	ma.CostUSD += cost

	// Check budgets
	ct.checkBudgetLocked()

	return cost
}

// GetSessionCost returns the total cost for the current session.
func (ct *CostTracker) GetSessionCost() float64 {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return ct.sessionCost
}

// GetDailyCost returns the total cost for today.
func (ct *CostTracker) GetDailyCost() float64 {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return ct.dailyCost
}

// GetMonthlyCost returns the total cost for this month.
func (ct *CostTracker) GetMonthlyCost() float64 {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return ct.monthlyCost
}

// GetModelUsage returns accumulated usage by model.
func (ct *CostTracker) GetModelUsage() map[string]ModelUsageAccum {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	out := make(map[string]ModelUsageAccum, len(ct.modelUsage))
	for k, v := range ct.modelUsage {
		out[k] = *v
	}
	return out
}

// HasUnknownModelCost returns true if any tracked call used an unknown model.
func (ct *CostTracker) HasUnknownModelCost() bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return ct.hasUnknownModelCost
}

// SetHasUnknownModelCost flags that an unknown model was used.
func (ct *CostTracker) SetHasUnknownModelCost() {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.hasUnknownModelCost = true
}

// Reset clears all tracked state.
func (ct *CostTracker) Reset() {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.sessionCost = 0
	ct.dailyCost = 0
	ct.monthlyCost = 0
	ct.totalInputTokens = 0
	ct.totalOutputTokens = 0
	ct.totalCacheReadInputTokens = 0
	ct.totalCacheCreationInputTokens = 0
	ct.totalWebSearchRequests = 0
	ct.modelUsage = make(map[string]*ModelUsageAccum)
	ct.hasUnknownModelCost = false
	now := time.Now()
	ct.dayKey = now.Format("2006-01-02")
	ct.monthKey = now.Format("2006-01")
}

// FormatCostSummary returns a human-readable cost report for the session.
func (ct *CostTracker) FormatCostSummary() string {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	var sb strings.Builder
	costStr := formatCostValue(ct.sessionCost)
	if ct.hasUnknownModelCost {
		costStr += " (costs may be inaccurate due to usage of unknown models)"
	}
	fmt.Fprintf(&sb, "Total cost:            %s\n", costStr)

	if len(ct.modelUsage) == 0 {
		fmt.Fprintf(&sb, "Usage:                 %s input, %s output, %s cache read, %s cache write\n",
			formatNumber(ct.totalInputTokens), formatNumber(ct.totalOutputTokens),
			formatNumber(ct.totalCacheReadInputTokens), formatNumber(ct.totalCacheCreationInputTokens))
	} else {
		sb.WriteString("Usage by model:\n")
		for model, mu := range ct.modelUsage {
			line := fmt.Sprintf("  %s input, %s output, %s cache read, %s cache write",
				formatNumber(mu.InputTokens), formatNumber(mu.OutputTokens),
				formatNumber(mu.CacheReadInputTokens), formatNumber(mu.CacheCreationInputTokens))
			if mu.WebSearchRequests > 0 {
				line += fmt.Sprintf(", %s web search", formatNumber(mu.WebSearchRequests))
			}
			line += fmt.Sprintf(" (%s)", formatCostValue(mu.CostUSD))
			fmt.Fprintf(&sb, "%21s:%s\n", model, line)
		}
	}

	return sb.String()
}

func (ct *CostTracker) checkBudgetLocked() {
	if ct.onBudgetAlert == nil {
		return
	}
	if ct.dailyBudget > 0 {
		pct := ct.dailyCost / ct.dailyBudget
		if pct >= 1.0 {
			ct.onBudgetAlert(pct, fmt.Sprintf("Daily budget exceeded ($%.4f/$%.4f)", ct.dailyCost, ct.dailyBudget))
		} else if pct >= ct.warningPct {
			ct.onBudgetAlert(pct, fmt.Sprintf("Approaching daily budget (%.0f%% of $%.4f)", pct*100, ct.dailyBudget))
		}
	}
	if ct.monthlyBudget > 0 {
		pct := ct.monthlyCost / ct.monthlyBudget
		if pct >= 1.0 {
			ct.onBudgetAlert(pct, fmt.Sprintf("Monthly budget exceeded ($%.4f/$%.4f)", ct.monthlyCost, ct.monthlyBudget))
		} else if pct >= ct.warningPct {
			ct.onBudgetAlert(pct, fmt.Sprintf("Approaching monthly budget (%.0f%% of $%.4f)", pct*100, ct.monthlyBudget))
		}
	}
}

// FormatCost formats a USD cost value for display.
func FormatCost(cost float64) string {
	return formatCostValue(cost)
}

func formatCostValue(cost float64) string {
	if cost > 0.5 {
		rounded := math.Round(cost*100) / 100
		return fmt.Sprintf("$%.2f", rounded)
	}
	return fmt.Sprintf("$%.4f", cost)
}

func formatNumber(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}
