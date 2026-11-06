package models

import (
	"fmt"
	"sort"
	"strings"
)

// CostBreakdown describes the dollar cost of a single usage on a specific model
// broken down by token category (input, output, cache read, cache write).
type CostBreakdown struct {
	Model            string
	Provider         string
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int // Anthropic/OpenAI cache reads
	CacheWriteTokens int // Anthropic cache writes

	InputCost      float64
	OutputCost     float64
	CacheReadCost  float64
	CacheWriteCost float64
	TotalCost      float64
}

// UsageData represents a hypothetical token usage for cost comparisons.
type UsageData struct {
	InputTokens  int
	OutputTokens int
}

// cacheWriteMultiplier captures the markup applied to cache writes by
// providers that charge for cache creation. Anthropic charges 1.25x the input
// price for cache writes (5m TTL); we apply that for any caching-capable model
// for simplicity.
const cacheWriteMultiplier = 1.25

// ComputeCost calculates a CostBreakdown for the given token counts on the
// specified model. If the model is unknown, all costs are zero and Provider
// is left empty.
func ComputeCost(model string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int) CostBreakdown {
	cb := CostBreakdown{
		Model:            model,
		InputTokens:      inputTokens,
		OutputTokens:     outputTokens,
		CacheReadTokens:  cacheReadTokens,
		CacheWriteTokens: cacheWriteTokens,
	}
	cap := GetCapabilities(model)
	if cap == nil {
		return cb
	}
	cb.Provider = cap.Provider

	cb.InputCost = float64(inputTokens) / 1_000_000 * cap.InputPricePer1M
	cb.OutputCost = float64(outputTokens) / 1_000_000 * cap.OutputPricePer1M
	if cap.SupportsCaching && cap.CachePricePer1M > 0 {
		cb.CacheReadCost = float64(cacheReadTokens) / 1_000_000 * cap.CachePricePer1M
		cb.CacheWriteCost = float64(cacheWriteTokens) / 1_000_000 * cap.InputPricePer1M * cacheWriteMultiplier
	}
	cb.TotalCost = cb.InputCost + cb.OutputCost + cb.CacheReadCost + cb.CacheWriteCost
	return cb
}

// CostComparison returns a slice of cost breakdowns, one per non-deprecated
// model in the database, sorted by total cost ascending.
func CostComparison(usage UsageData) []CostBreakdown {
	all := ListAllModels()
	out := make([]CostBreakdown, 0, len(all))
	for _, m := range all {
		if m.IsDeprecated {
			continue
		}
		cb := ComputeCost(m.ID, usage.InputTokens, usage.OutputTokens, 0, 0)
		out = append(out, cb)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].TotalCost < out[j].TotalCost
	})
	return out
}

// CheapestModel returns the cheapest non-deprecated model for the given usage,
// or nil if no models exist.
func CheapestModel(usage UsageData) *ModelCapabilities {
	cmp := CostComparison(usage)
	if len(cmp) == 0 {
		return nil
	}
	return GetCapabilities(cmp[0].Model)
}

// MostExpensive returns the most expensive non-deprecated model for the given
// usage, or nil if no models exist.
func MostExpensive(usage UsageData) *ModelCapabilities {
	cmp := CostComparison(usage)
	if len(cmp) == 0 {
		return nil
	}
	return GetCapabilities(cmp[len(cmp)-1].Model)
}

// ProjectedCost projects the total cost of N similar requests with the given
// average input and output token counts on the specified model.
func ProjectedCost(model string, avgInput, avgOutput, requests int) float64 {
	if requests <= 0 {
		return 0
	}
	cb := ComputeCost(model, avgInput, avgOutput, 0, 0)
	return cb.TotalCost * float64(requests)
}

// FormatCostBreakdown formats a single CostBreakdown as a multi-line string.
func FormatCostBreakdown(b CostBreakdown) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Modelo:    %s (%s)\n", b.Model, b.Provider)
	fmt.Fprintf(&sb, "Input:     %d tokens = $%.4f\n", b.InputTokens, b.InputCost)
	fmt.Fprintf(&sb, "Output:    %d tokens = $%.4f\n", b.OutputTokens, b.OutputCost)
	if b.CacheReadTokens > 0 {
		fmt.Fprintf(&sb, "Cache rd:  %d tokens = $%.4f\n", b.CacheReadTokens, b.CacheReadCost)
	}
	if b.CacheWriteTokens > 0 {
		fmt.Fprintf(&sb, "Cache wr:  %d tokens = $%.4f\n", b.CacheWriteTokens, b.CacheWriteCost)
	}
	fmt.Fprintf(&sb, "TOTAL:     $%.4f\n", b.TotalCost)
	return sb.String()
}

// FormatCostTable renders a table comparing costs across all known
// non-deprecated models for the same usage. Used by the /usage command to
// preview what a query would cost on different models.
func FormatCostTable(usage UsageData) string {
	cmp := CostComparison(usage)
	if len(cmp) == 0 {
		return "<no models available>\n"
	}

	var sb strings.Builder
	sb.WriteString("=== COMPARACAO DE CUSTOS ===\n")
	fmt.Fprintf(&sb, "Para %d input + %d output tokens:\n\n", usage.InputTokens, usage.OutputTokens)

	fmt.Fprintf(&sb, "%-12s %-34s %-10s %-10s %-12s\n",
		"PROVIDER", "MODEL", "INPUT", "OUTPUT", "TOTAL")
	sb.WriteString(strings.Repeat("-", 80))
	sb.WriteByte('\n')

	cheapest := cmp[0]
	mostExp := cmp[len(cmp)-1]

	// Re-sort by provider/model so the table is grouped, but keep the
	// cheapest/most-expensive markers from the cost-sorted comparison.
	display := make([]CostBreakdown, len(cmp))
	copy(display, cmp)
	sort.Slice(display, func(i, j int) bool {
		if display[i].Provider != display[j].Provider {
			return display[i].Provider < display[j].Provider
		}
		return display[i].Model < display[j].Model
	})

	for _, b := range display {
		marker := ""
		if b.Model == cheapest.Model {
			marker = " <- mais barato"
		}
		fmt.Fprintf(&sb, "%-12s %-34s $%-9.4f $%-9.4f $%-9.4f%s\n",
			b.Provider, truncate(b.Model, 34),
			b.InputCost, b.OutputCost, b.TotalCost, marker)
	}

	sb.WriteByte('\n')
	fmt.Fprintf(&sb, "MAIS BARATO: %s ($%.4f)\n", cheapest.Model, cheapest.TotalCost)
	if mostExp.TotalCost > 0 && cheapest.TotalCost > 0 && mostExp.Model != cheapest.Model {
		ratio := mostExp.TotalCost / cheapest.TotalCost
		fmt.Fprintf(&sb, "MAIS CARO:   %s ($%.4f) - %.0fx mais caro\n",
			mostExp.Model, mostExp.TotalCost, ratio)
	}
	return sb.String()
}

// FormatProviderSummary aggregates a slice of breakdowns by provider and
// renders the totals as a table.
func FormatProviderSummary(breakdowns []CostBreakdown) string {
	if len(breakdowns) == 0 {
		return "<no breakdowns>\n"
	}
	type agg struct {
		input, output, total float64
		requests             int
	}
	totals := make(map[string]*agg)
	for _, b := range breakdowns {
		a, ok := totals[b.Provider]
		if !ok {
			a = &agg{}
			totals[b.Provider] = a
		}
		a.input += b.InputCost
		a.output += b.OutputCost
		a.total += b.TotalCost
		a.requests++
	}

	providers := make([]string, 0, len(totals))
	for p := range totals {
		providers = append(providers, p)
	}
	sort.Slice(providers, func(i, j int) bool {
		return totals[providers[i]].total > totals[providers[j]].total
	})

	var sb strings.Builder
	sb.WriteString("=== TOTAL POR PROVIDER ===\n")
	fmt.Fprintf(&sb, "%-12s %-10s %-10s %-12s %s\n",
		"PROVIDER", "INPUT", "OUTPUT", "TOTAL", "REQS")
	sb.WriteString(strings.Repeat("-", 60))
	sb.WriteByte('\n')
	var grand float64
	for _, p := range providers {
		a := totals[p]
		fmt.Fprintf(&sb, "%-12s $%-9.4f $%-9.4f $%-11.4f %d\n",
			p, a.input, a.output, a.total, a.requests)
		grand += a.total
	}
	sb.WriteString(strings.Repeat("-", 60))
	sb.WriteByte('\n')
	fmt.Fprintf(&sb, "%-12s %-10s %-10s $%-11.4f\n", "GRAND TOTAL", "", "", grand)
	return sb.String()
}

// qualityScore maps Quality strings to numeric scores used by ModelEfficiency.
var qualityScore = map[string]float64{
	"low":      1.0,
	"medium":   2.0,
	"high":     3.0,
	"frontier": 4.0,
}

// ModelEfficiency returns a cost-per-quality ratio for a model. Lower is
// better: it expresses how much you pay (per Mtok of mixed input+output) for
// each unit of model quality. Returns 0 for unknown or zero-priced models.
func ModelEfficiency(modelID string) float64 {
	cap := GetCapabilities(modelID)
	if cap == nil {
		return 0
	}
	q := qualityScore[strings.ToLower(cap.Quality)]
	if q == 0 {
		q = 1.0
	}
	// Use a 1:1 input/output blend for the efficiency metric.
	avgPrice := (cap.InputPricePer1M + cap.OutputPricePer1M) / 2.0
	if avgPrice <= 0 {
		return 0
	}
	return avgPrice / q
}

// RecommendForBudget returns all non-deprecated models whose cost for a
// reference 1K input + 500 output token request fits within budgetPerRequest
// USD. Results are sorted by quality descending so the highest-quality models
// the user can afford come first.
func RecommendForBudget(budgetPerRequest float64) []*ModelCapabilities {
	if budgetPerRequest <= 0 {
		return nil
	}
	usage := UsageData{InputTokens: 1000, OutputTokens: 500}
	var out []*ModelCapabilities
	for _, m := range ListAllModels() {
		if m.IsDeprecated {
			continue
		}
		cb := ComputeCost(m.ID, usage.InputTokens, usage.OutputTokens, 0, 0)
		if cb.TotalCost <= budgetPerRequest && cb.TotalCost > 0 {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		qi := qualityScore[strings.ToLower(out[i].Quality)]
		qj := qualityScore[strings.ToLower(out[j].Quality)]
		if qi != qj {
			return qi > qj
		}
		return out[i].InputPricePer1M < out[j].InputPricePer1M
	})
	return out
}

// FormatPriceComparison formats a side-by-side price comparison of two models
// for the given usage.
func FormatPriceComparison(modelA, modelB string, usage UsageData) string {
	a := ComputeCost(modelA, usage.InputTokens, usage.OutputTokens, 0, 0)
	b := ComputeCost(modelB, usage.InputTokens, usage.OutputTokens, 0, 0)

	var sb strings.Builder
	fmt.Fprintf(&sb, "=== %s vs %s ===\n", modelA, modelB)
	fmt.Fprintf(&sb, "Para %d input + %d output tokens:\n\n",
		usage.InputTokens, usage.OutputTokens)

	fmt.Fprintf(&sb, "%-15s %-20s %-20s\n", "", modelA, modelB)
	sb.WriteString(strings.Repeat("-", 60))
	sb.WriteByte('\n')
	fmt.Fprintf(&sb, "%-15s %-20s %-20s\n", "Provider", a.Provider, b.Provider)
	fmt.Fprintf(&sb, "%-15s $%-19.4f $%-19.4f\n", "Input", a.InputCost, b.InputCost)
	fmt.Fprintf(&sb, "%-15s $%-19.4f $%-19.4f\n", "Output", a.OutputCost, b.OutputCost)
	fmt.Fprintf(&sb, "%-15s $%-19.4f $%-19.4f\n", "TOTAL", a.TotalCost, b.TotalCost)

	if a.TotalCost > 0 && b.TotalCost > 0 {
		sb.WriteByte('\n')
		if a.TotalCost < b.TotalCost {
			fmt.Fprintf(&sb, "%s e %.2fx mais barato que %s\n",
				modelA, b.TotalCost/a.TotalCost, modelB)
		} else if b.TotalCost < a.TotalCost {
			fmt.Fprintf(&sb, "%s e %.2fx mais barato que %s\n",
				modelB, a.TotalCost/b.TotalCost, modelA)
		} else {
			sb.WriteString("Custos iguais.\n")
		}
	}
	return sb.String()
}
