// Package billing tracks API usage costs per provider/model across sessions.
// It accumulates token counts and dollar costs in a thread-safe tracker,
// allowing formatted reports and per-model breakdowns.
package billing

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ModelPrice holds the per-1K-token prices for a single model.
type ModelPrice struct {
	InputPer1K       float64 // USD per 1K input tokens
	OutputPer1K      float64 // USD per 1K output tokens
	CachedInputPer1K float64 // USD per 1K cached input tokens
}

// PriceTable maps model identifiers to their token prices.
type PriceTable map[string]ModelPrice

// DefaultPrices returns a PriceTable pre-populated with current prices for
// popular models across Anthropic, OpenAI, and Google providers.
func DefaultPrices() PriceTable {
	return PriceTable{
		// ── Anthropic ──────────────────────────────────────────────────
		"claude-sonnet-4-6": {
			InputPer1K:       0.003,  // $3.00 / 1M
			OutputPer1K:      0.015,  // $15.00 / 1M
			CachedInputPer1K: 0.0003, // $0.30 / 1M
		},
		"claude-opus-4-6": {
			InputPer1K:       0.015, // $15.00 / 1M
			OutputPer1K:      0.075, // $75.00 / 1M
			CachedInputPer1K: 0.0015,
		},
		"claude-haiku-4-5": {
			InputPer1K:       0.0008,  // $0.80 / 1M
			OutputPer1K:      0.004,   // $4.00 / 1M
			CachedInputPer1K: 0.00008, // $0.08 / 1M
		},

		// ── OpenAI ─────────────────────────────────────────────────────
		"gpt-4o": {
			InputPer1K:       0.0025, // $2.50 / 1M
			OutputPer1K:      0.010,  // $10.00 / 1M
			CachedInputPer1K: 0,
		},
		"gpt-4o-mini": {
			InputPer1K:       0.00015, // $0.15 / 1M
			OutputPer1K:      0.0006,  // $0.60 / 1M
			CachedInputPer1K: 0,
		},
		"gpt-4.1": {
			InputPer1K:       0.002, // $2.00 / 1M
			OutputPer1K:      0.008, // $8.00 / 1M
			CachedInputPer1K: 0,
		},
		"gpt-4.1-mini": {
			InputPer1K:       0.0004, // $0.40 / 1M
			OutputPer1K:      0.0016, // $1.60 / 1M
			CachedInputPer1K: 0,
		},
		"o3": {
			InputPer1K:       0.010, // $10.00 / 1M
			OutputPer1K:      0.040, // $40.00 / 1M
			CachedInputPer1K: 0,
		},
		"o3-mini": {
			InputPer1K:       0.0011, // $1.10 / 1M
			OutputPer1K:      0.0044, // $4.40 / 1M
			CachedInputPer1K: 0,
		},
		"o4-mini": {
			InputPer1K:       0.0011, // $1.10 / 1M
			OutputPer1K:      0.0044, // $4.40 / 1M
			CachedInputPer1K: 0,
		},

		// ── Google ─────────────────────────────────────────────────────
		"gemini-2.5-pro": {
			InputPer1K:       0.00125,  // $1.25 / 1M
			OutputPer1K:      0.010,    // $10.00 / 1M
			CachedInputPer1K: 0.000315, // $0.315 / 1M
		},
		"gemini-2.5-flash": {
			InputPer1K:       0.00015,   // $0.15 / 1M
			OutputPer1K:      0.0006,    // $0.60 / 1M
			CachedInputPer1K: 0.0000375, // $0.0375 / 1M
		},
		"gemini-2.0-flash": {
			InputPer1K:       0.0001,   // $0.10 / 1M
			OutputPer1K:      0.0004,   // $0.40 / 1M
			CachedInputPer1K: 0.000025, // $0.025 / 1M
		},

		// ── DeepSeek ───────────────────────────────────────────────────
		"deepseek-chat": {
			InputPer1K:       0.00014,  // $0.14 / 1M
			OutputPer1K:      0.00028,  // $0.28 / 1M
			CachedInputPer1K: 0.000014, // $0.014 / 1M
		},
		"deepseek-reasoner": {
			InputPer1K:       0.00055, // $0.55 / 1M
			OutputPer1K:      0.00219, // $2.19 / 1M
			CachedInputPer1K: 0.00014, // $0.14 / 1M
		},
	}
}

// UsageRecord captures a single API call's token usage and computed cost.
type UsageRecord struct {
	Model        string
	InputTokens  int
	OutputTokens int
	CachedTokens int
	Cost         float64
	Timestamp    time.Time
}

// Tracker accumulates API usage records in a thread-safe manner and provides
// cost summaries and reports.
type Tracker struct {
	mu      sync.Mutex
	prices  PriceTable
	records []UsageRecord
}

// NewTracker creates a Tracker that uses the supplied PriceTable for cost
// calculations. Pass DefaultPrices() for standard pricing.
func NewTracker(prices PriceTable) *Tracker {
	return &Tracker{
		prices:  prices,
		records: make([]UsageRecord, 0, 64),
	}
}

// Record calculates the cost for a single API call and stores it.
// If the model is not in the price table, cost is recorded as zero.
func (t *Tracker) Record(model string, input, output, cached int) {
	price, ok := t.prices[model]
	var cost float64
	if ok {
		// Regular (non-cached) input tokens: total input minus cached.
		regularInput := input - cached
		if regularInput < 0 {
			regularInput = 0
		}
		cost = float64(regularInput)/1000*price.InputPer1K +
			float64(output)/1000*price.OutputPer1K +
			float64(cached)/1000*price.CachedInputPer1K
	}

	rec := UsageRecord{
		Model:        model,
		InputTokens:  input,
		OutputTokens: output,
		CachedTokens: cached,
		Cost:         cost,
		Timestamp:    time.Now(),
	}

	t.mu.Lock()
	t.records = append(t.records, rec)
	t.mu.Unlock()
}

// TotalCost returns the cumulative cost across all recorded usage.
func (t *Tracker) TotalCost() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()

	var total float64
	for _, r := range t.records {
		total += r.Cost
	}
	return total
}

// TotalTokens returns the aggregate input and output token counts.
func (t *Tracker) TotalTokens() (input, output int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, r := range t.records {
		input += r.InputTokens
		output += r.OutputTokens
	}
	return input, output
}

// ByModel returns a map of model identifier to total cost for that model.
func (t *Tracker) ByModel() map[string]float64 {
	t.mu.Lock()
	defer t.mu.Unlock()

	m := make(map[string]float64, len(t.prices))
	for _, r := range t.records {
		m[r.Model] += r.Cost
	}
	return m
}

// Report returns a formatted cost report in Portuguese, summarising total
// usage and per-model breakdown sorted by cost descending.
func (t *Tracker) Report() string {
	t.mu.Lock()
	records := make([]UsageRecord, len(t.records))
	copy(records, t.records)
	t.mu.Unlock()

	if len(records) == 0 {
		return "Nenhum uso registrado.\n"
	}

	// Aggregate per model.
	type modelAgg struct {
		input, output, cached int
		cost                  float64
		calls                 int
	}
	agg := make(map[string]*modelAgg)
	var totalCost float64
	var totalInput, totalOutput int

	for _, r := range records {
		a, ok := agg[r.Model]
		if !ok {
			a = &modelAgg{}
			agg[r.Model] = a
		}
		a.input += r.InputTokens
		a.output += r.OutputTokens
		a.cached += r.CachedTokens
		a.cost += r.Cost
		a.calls++
		totalCost += r.Cost
		totalInput += r.InputTokens
		totalOutput += r.OutputTokens
	}

	// Sort models by cost descending.
	models := make([]string, 0, len(agg))
	for m := range agg {
		models = append(models, m)
	}
	sort.Slice(models, func(i, j int) bool {
		return agg[models[i]].cost > agg[models[j]].cost
	})

	var sb strings.Builder
	sb.WriteString("=== RELATORIO DE CUSTOS ===\n\n")

	fmt.Fprintf(&sb, "Total de chamadas: %d\n", len(records))
	fmt.Fprintf(&sb, "Tokens de entrada: %d\n", totalInput)
	fmt.Fprintf(&sb, "Tokens de saida:   %d\n", totalOutput)
	fmt.Fprintf(&sb, "Custo total:       $%.4f\n\n", totalCost)

	fmt.Fprintf(&sb, "%-30s %8s %8s %8s %6s %10s\n",
		"MODELO", "INPUT", "OUTPUT", "CACHE", "REQS", "CUSTO")
	sb.WriteString(strings.Repeat("-", 78))
	sb.WriteByte('\n')

	for _, m := range models {
		a := agg[m]
		fmt.Fprintf(&sb, "%-30s %8d %8d %8d %6d $%9.4f\n",
			truncate(m, 30), a.input, a.output, a.cached, a.calls, a.cost)
	}

	sb.WriteString(strings.Repeat("-", 78))
	sb.WriteByte('\n')
	fmt.Fprintf(&sb, "%-30s %8d %8d %8s %6d $%9.4f\n",
		"TOTAL", totalInput, totalOutput, "", len(records), totalCost)

	return sb.String()
}

// Reset discards all recorded usage data.
func (t *Tracker) Reset() {
	t.mu.Lock()
	t.records = t.records[:0]
	t.mu.Unlock()
}

// truncate shortens s to maxLen, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
