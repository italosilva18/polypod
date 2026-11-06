package models

import (
	"strings"
	"testing"
)

func TestGetCapabilities(t *testing.T) {
	known := []string{
		"claude-sonnet-4-20250514",
		"gpt-4o",
		"gemini-2.5-pro",
		"deepseek-chat",
	}
	for _, id := range known {
		t.Run(id, func(t *testing.T) {
			cap := GetCapabilities(id)
			if cap == nil {
				t.Errorf("nil capabilities for %s", id)
				return
			}
			if cap.Provider == "" {
				t.Errorf("empty Provider for %s", id)
			}
			if cap.DisplayName == "" {
				t.Errorf("empty DisplayName for %s", id)
			}
			if cap.MaxContextTokens <= 0 {
				t.Errorf("bad MaxContextTokens for %s: %d", id, cap.MaxContextTokens)
			}
		})
	}
}

func TestGetCapabilities_Unknown(t *testing.T) {
	cap := GetCapabilities("nonexistent-model")
	if cap != nil {
		t.Errorf("unknown model returned %v; want nil", cap)
	}
}

func TestGetCapabilities_CaseInsensitive(t *testing.T) {
	a := GetCapabilities("GPT-4O")
	b := GetCapabilities("gpt-4o")
	if a == nil || b == nil {
		t.Fatal("expected both lookups to succeed")
	}
	if a.ID != b.ID {
		t.Errorf("case-insensitive lookup mismatch: %s vs %s", a.ID, b.ID)
	}
}

func TestListModels_Anthropic(t *testing.T) {
	list := ListModels("anthropic")
	if len(list) == 0 {
		t.Fatal("expected at least one anthropic model")
	}
	for _, m := range list {
		if m.Provider != "anthropic" {
			t.Errorf("non-anthropic model in list: %s (%s)", m.ID, m.Provider)
		}
	}
}

func TestListModels_EmptyProviderReturnsAll(t *testing.T) {
	all := ListAllModels()
	list := ListModels("")
	if len(list) != len(all) {
		t.Errorf("ListModels(\"\") returned %d; want %d", len(list), len(all))
	}
}

func TestListModels_UnknownProvider(t *testing.T) {
	list := ListModels("doesnotexist")
	if len(list) != 0 {
		t.Errorf("unknown provider returned %d models; want 0", len(list))
	}
}

func TestListAllModels_Count(t *testing.T) {
	all := ListAllModels()
	if len(all) < 15 {
		t.Errorf("ListAllModels returned %d; want >= 15", len(all))
	}
}

func TestRecommendModel_Coding(t *testing.T) {
	m := RecommendModel("coding")
	if m == nil {
		t.Fatal("coding recommendation returned nil")
	}
	if m.ID == "" {
		t.Error("coding recommendation has empty ID")
	}
}

func TestRecommendModel_FastTasks(t *testing.T) {
	m := RecommendModel("fast-tasks")
	if m == nil {
		t.Fatal("fast-tasks recommendation returned nil")
	}
	if m.Speed != "fast" {
		t.Errorf("fast-tasks recommended %s with Speed=%q; want fast", m.ID, m.Speed)
	}
}

func TestRecommendModel_UnknownFallback(t *testing.T) {
	m := RecommendModel("totally-unknown-category")
	if m == nil {
		t.Fatal("unknown category returned nil; expected fallback")
	}
	if m.ID != "claude-sonnet-4-20250514" {
		t.Errorf("fallback returned %s; want claude-sonnet-4-20250514", m.ID)
	}
}

func TestFormatModelInfo(t *testing.T) {
	cap := GetCapabilities("claude-sonnet-4-20250514")
	if cap == nil {
		t.Fatal("setup: missing claude-sonnet-4-20250514")
	}
	info := FormatModelInfo(cap)
	if info == "" {
		t.Error("FormatModelInfo returned empty string")
	}
	if !strings.Contains(info, cap.DisplayName) {
		t.Errorf("FormatModelInfo missing DisplayName %q", cap.DisplayName)
	}
	if !strings.Contains(info, cap.Provider) {
		t.Errorf("FormatModelInfo missing provider %q", cap.Provider)
	}
}

func TestFormatModelInfo_Nil(t *testing.T) {
	info := FormatModelInfo(nil)
	if info == "" {
		t.Error("FormatModelInfo(nil) returned empty; want placeholder")
	}
}

func TestComputeCost_Known(t *testing.T) {
	cb := ComputeCost("gpt-4o", 1000, 500, 0, 0)
	if cb.TotalCost <= 0 {
		t.Errorf("expected positive total cost; got %f", cb.TotalCost)
	}
	if cb.Model != "gpt-4o" {
		t.Errorf("Model = %q; want gpt-4o", cb.Model)
	}
	if cb.Provider != "openai" {
		t.Errorf("Provider = %q; want openai", cb.Provider)
	}
	if cb.InputTokens != 1000 {
		t.Errorf("InputTokens = %d; want 1000", cb.InputTokens)
	}
	if cb.OutputTokens != 500 {
		t.Errorf("OutputTokens = %d; want 500", cb.OutputTokens)
	}
	if cb.InputCost <= 0 || cb.OutputCost <= 0 {
		t.Errorf("expected positive InputCost/OutputCost; got %f/%f", cb.InputCost, cb.OutputCost)
	}
	// 1000/1M * 2.50 + 500/1M * 10.00 = 0.0025 + 0.005 = 0.0075
	const want = 0.0075
	if diff := cb.TotalCost - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("TotalCost = %f; want %f", cb.TotalCost, want)
	}
}

func TestComputeCost_Unknown(t *testing.T) {
	cb := ComputeCost("unknown-model-xyz", 1000, 500, 0, 0)
	if cb.TotalCost != 0 {
		t.Errorf("unknown model TotalCost = %f; want 0", cb.TotalCost)
	}
	if cb.Provider != "" {
		t.Errorf("unknown model Provider = %q; want empty", cb.Provider)
	}
}

func TestComputeCost_ZeroTokens(t *testing.T) {
	cb := ComputeCost("gpt-4o", 0, 0, 0, 0)
	if cb.TotalCost != 0 {
		t.Errorf("zero tokens TotalCost = %f; want 0", cb.TotalCost)
	}
}

func TestCostComparison_SortedAndComplete(t *testing.T) {
	usage := UsageData{InputTokens: 1000, OutputTokens: 500}
	cmp := CostComparison(usage)
	if len(cmp) == 0 {
		t.Fatal("empty comparison")
	}
	// Count non-deprecated models.
	expected := 0
	for _, m := range ListAllModels() {
		if !m.IsDeprecated {
			expected++
		}
	}
	if len(cmp) != expected {
		t.Errorf("CostComparison length %d; want %d", len(cmp), expected)
	}
	// Verify ascending sort by total cost.
	for i := 1; i < len(cmp); i++ {
		if cmp[i].TotalCost < cmp[i-1].TotalCost {
			t.Errorf("not sorted: %s ($%f) after %s ($%f)",
				cmp[i].Model, cmp[i].TotalCost,
				cmp[i-1].Model, cmp[i-1].TotalCost)
		}
	}
}

func TestCheapestVsMostExpensive(t *testing.T) {
	usage := UsageData{InputTokens: 10000, OutputTokens: 5000}
	cheap := CheapestModel(usage)
	exp := MostExpensive(usage)
	if cheap == nil || exp == nil {
		t.Fatal("cheapest/most-expensive returned nil")
	}
	cheapCost := ComputeCost(cheap.ID, usage.InputTokens, usage.OutputTokens, 0, 0).TotalCost
	expCost := ComputeCost(exp.ID, usage.InputTokens, usage.OutputTokens, 0, 0).TotalCost
	if cheapCost > expCost {
		t.Errorf("cheapest (%s $%f) > mostExpensive (%s $%f)",
			cheap.ID, cheapCost, exp.ID, expCost)
	}
	if cheapCost <= 0 {
		t.Errorf("cheapest cost = %f; want > 0", cheapCost)
	}
}

func TestProjectedCost_Linear(t *testing.T) {
	single := ComputeCost("gpt-4o", 1000, 500, 0, 0).TotalCost
	projected := ProjectedCost("gpt-4o", 1000, 500, 100)
	want := single * 100
	if diff := projected - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("ProjectedCost = %f; want %f", projected, want)
	}
}

func TestProjectedCost_ZeroRequests(t *testing.T) {
	if got := ProjectedCost("gpt-4o", 1000, 500, 0); got != 0 {
		t.Errorf("ProjectedCost(0 reqs) = %f; want 0", got)
	}
}

func TestModelEfficiency_PositiveForKnown(t *testing.T) {
	known := []string{"gpt-4o", "claude-sonnet-4-20250514", "deepseek-chat"}
	for _, id := range known {
		e := ModelEfficiency(id)
		if e <= 0 {
			t.Errorf("ModelEfficiency(%s) = %f; want > 0", id, e)
		}
	}
}

func TestModelEfficiency_Unknown(t *testing.T) {
	if e := ModelEfficiency("unknown-xyz"); e != 0 {
		t.Errorf("unknown model efficiency = %f; want 0", e)
	}
}

func TestModelEfficiency_CheaperIsBetter(t *testing.T) {
	// A cheap medium-quality model should have lower (better) cost-per-quality
	// than an expensive frontier model like Opus.
	cheap := ModelEfficiency("deepseek-chat")
	expensive := ModelEfficiency("claude-3-opus-20240229")
	if cheap <= 0 || expensive <= 0 {
		t.Fatalf("setup: got efficiencies cheap=%f expensive=%f", cheap, expensive)
	}
	if cheap >= expensive {
		t.Errorf("expected deepseek-chat (%f) < claude-3-opus (%f)", cheap, expensive)
	}
}

func TestFormatCostTable(t *testing.T) {
	usage := UsageData{InputTokens: 1000, OutputTokens: 500}
	s := FormatCostTable(usage)
	if s == "" {
		t.Fatal("empty FormatCostTable output")
	}
	// Should mention at least one known model.
	if !strings.Contains(s, "gpt-4o") && !strings.Contains(s, "claude") {
		t.Errorf("FormatCostTable missing known model ID; got:\n%s", s)
	}
}

func TestRecommendForBudget_Cheap(t *testing.T) {
	// Very tight budget: only the cheapest models should fit.
	got := RecommendForBudget(0.001)
	if len(got) == 0 {
		t.Error("expected at least one cheap model for $0.001 budget")
	}
	// Every returned model must fit the budget.
	for _, m := range got {
		cb := ComputeCost(m.ID, 1000, 500, 0, 0)
		if cb.TotalCost > 0.001 {
			t.Errorf("%s cost $%f exceeds $0.001 budget", m.ID, cb.TotalCost)
		}
	}
}

func TestRecommendForBudget_Generous(t *testing.T) {
	got := RecommendForBudget(10.00)
	// All non-deprecated priced models should fit.
	expected := 0
	for _, m := range ListAllModels() {
		if m.IsDeprecated {
			continue
		}
		cb := ComputeCost(m.ID, 1000, 500, 0, 0)
		if cb.TotalCost > 0 && cb.TotalCost <= 10.00 {
			expected++
		}
	}
	if len(got) != expected {
		t.Errorf("RecommendForBudget($10) returned %d; want %d", len(got), expected)
	}
}

func TestRecommendForBudget_Zero(t *testing.T) {
	if got := RecommendForBudget(0); got != nil {
		t.Errorf("zero budget returned %d models; want nil", len(got))
	}
}
