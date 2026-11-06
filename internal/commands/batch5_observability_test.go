// Package commands — batch5_observability_test.go
// Tests for /cost, /insights and /usage covering:
//  1. RegisterBatch5Observability wires all three command names.
//  2. /cost with no tracker returns a Portuguese placeholder.
//  3. /cost with a live BudgetTracker returns its BudgetReport().
//  4. /insights and /usage round-trips (smoke).
package commands

import (
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
	"github.com/costa/polypod/internal/tokenbudget"
)

// resetObservabilityState wipes the package-level tracker slot and counters
// so tests don't leak state into each other.
func resetObservabilityState(t *testing.T) {
	t.Helper()
	observabilityState.mu.Lock()
	observabilityState.tracker = nil
	observabilityState.activeAgent = ""
	observabilityState.turns = 0
	observabilityState.toolCalls = 0
	observabilityState.errorCount = 0
	observabilityState.mu.Unlock()
	t.Cleanup(func() {
		observabilityState.mu.Lock()
		observabilityState.tracker = nil
		observabilityState.activeAgent = ""
		observabilityState.turns = 0
		observabilityState.toolCalls = 0
		observabilityState.errorCount = 0
		observabilityState.mu.Unlock()
	})
}

// ---------------------------------------------------------------------------
// 1. Registration
// ---------------------------------------------------------------------------

func TestRegisterBatch5Observability_RegistersAll(t *testing.T) {
	resetObservabilityState(t)
	reg := command.NewRegistry()
	RegisterBatch5Observability(reg)

	for _, name := range []string{"cost", "insights", "usage"} {
		cmd, ok := reg.Get(name)
		if !ok {
			t.Errorf("command %q not registered", name)
			continue
		}
		if cmd.Execute == nil {
			t.Errorf("command %q has nil Execute", name)
		}
		if cmd.Description == "" {
			t.Errorf("command %q has empty description", name)
		}
	}

	// /usage should also resolve via its alias.
	if _, ok := reg.Get("breakdown"); !ok {
		t.Error("alias 'breakdown' not registered for /usage")
	}
}

// ---------------------------------------------------------------------------
// 2. /cost with no tracker → Portuguese placeholder
// ---------------------------------------------------------------------------

func TestCmdCost_NoTracker_ReturnsPlaceholder(t *testing.T) {
	resetObservabilityState(t)
	cmd := cmdCost()
	if cmd.Execute == nil {
		t.Fatal("cmdCost has nil Execute")
	}
	res, err := cmd.Execute(nil, command.CommandContext{WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatalf("cmdCost Execute error: %v", err)
	}
	if res == nil || res.Display == "" {
		t.Fatal("cmdCost returned empty result")
	}
	if !strings.Contains(res.Display, "Nenhum rastreamento") {
		t.Errorf("expected Portuguese placeholder, got %q", res.Display)
	}
}

// ---------------------------------------------------------------------------
// 3. /cost with fake tracker → BudgetReport
// ---------------------------------------------------------------------------

func TestCmdCost_WithTracker_ReturnsReport(t *testing.T) {
	resetObservabilityState(t)

	tr := tokenbudget.NewTracker(tokenbudget.TrackerConfig{Limit: 100_000})
	tr.RecordTurn(tokenbudget.TurnStats{
		Prompt:     1_000,
		Completion: 500,
		ToolResult: 250,
		CacheRead:  2_000,
	})
	SetObservabilityTracker(tr)

	res, err := cmdCost().Execute(nil, command.CommandContext{WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatalf("cmdCost Execute error: %v", err)
	}
	if res == nil || res.Display == "" {
		t.Fatal("cmdCost returned empty result")
	}

	// Output should be the tracker's BudgetReport, not the placeholder.
	if strings.Contains(res.Display, "Nenhum rastreamento") {
		t.Errorf("unexpected placeholder with tracker wired: %q", res.Display)
	}
	for _, want := range []string{
		"Token Budget Report",
		"Turns recorded",
		"prompt",
		"completion",
		"cache_read",
	} {
		if !strings.Contains(res.Display, want) {
			t.Errorf("missing %q in cost output:\n%s", want, res.Display)
		}
	}
}

// ---------------------------------------------------------------------------
// 4. /insights smoke
// ---------------------------------------------------------------------------

func TestCmdInsights_NoTracker(t *testing.T) {
	resetObservabilityState(t)
	SetObservabilityAgent("default")
	RecordObservabilityTurn()
	RecordObservabilityToolCall()
	RecordObservabilityToolCall()
	RecordObservabilityError()

	res, err := cmdInsightsObservability().Execute(nil, command.CommandContext{WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatalf("cmdInsights error: %v", err)
	}
	if res == nil || res.Display == "" {
		t.Fatal("empty result")
	}
	for _, want := range []string{
		"Resumo da sessao",
		"Agente ativo",
		"default",
		"Turnos",
		"Chamadas tool",
		"rastreamento nao ativo",
	} {
		if !strings.Contains(res.Display, want) {
			t.Errorf("missing %q in insights output:\n%s", want, res.Display)
		}
	}
}

func TestCmdInsights_WithTracker(t *testing.T) {
	resetObservabilityState(t)
	tr := tokenbudget.NewTracker(tokenbudget.TrackerConfig{Limit: 50_000})
	tr.RecordTurn(tokenbudget.TurnStats{Prompt: 2_500, Completion: 1_500})
	SetObservabilityTracker(tr)
	SetObservabilityAgent("reviewer")

	res, err := cmdInsightsObservability().Execute(nil, command.CommandContext{WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatalf("cmdInsights error: %v", err)
	}
	if !strings.Contains(res.Display, "reviewer") {
		t.Errorf("missing agent name: %s", res.Display)
	}
	if !strings.Contains(res.Display, "Tokens totais") {
		t.Errorf("missing token line: %s", res.Display)
	}
	if !strings.Contains(res.Display, "Limite") {
		t.Errorf("missing limit line: %s", res.Display)
	}
}

// ---------------------------------------------------------------------------
// 5. /usage smoke
// ---------------------------------------------------------------------------

func TestCmdUsage_NoTracker(t *testing.T) {
	resetObservabilityState(t)
	res, err := cmdUsageObservability().Execute(nil, command.CommandContext{WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatalf("cmdUsage error: %v", err)
	}
	if !strings.Contains(res.Display, "Nenhum rastreamento") {
		t.Errorf("expected placeholder: %s", res.Display)
	}
}

func TestCmdUsage_WithTracker(t *testing.T) {
	resetObservabilityState(t)
	tr := tokenbudget.NewTracker(tokenbudget.TrackerConfig{Limit: 200_000})
	tr.RecordTurn(tokenbudget.TurnStats{
		Prompt:     4_000,
		Completion: 1_000,
		ToolResult: 750,
		Attachment: 500,
		CacheRead:  10_000,
		CacheWrite: 2_000,
		Extras:     map[tokenbudget.Category]int{"thinking": 800},
	})
	SetObservabilityTracker(tr)

	res, err := cmdUsageObservability().Execute(nil, command.CommandContext{WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatalf("cmdUsage error: %v", err)
	}
	if res == nil || res.Display == "" {
		t.Fatal("empty result")
	}
	// Plain-text header must survive lipgloss wrapping.
	for _, want := range []string{
		"Detalhamento de uso",
		"Categoria",
		"Tokens",
		"prompt",
		"completion",
		"cache_read",
		"thinking",
	} {
		if !strings.Contains(res.Display, want) {
			t.Errorf("missing %q in usage output:\n%s", want, res.Display)
		}
	}
}

// ---------------------------------------------------------------------------
// 6. formatObsInt sanity
// ---------------------------------------------------------------------------

func TestFormatObsInt(t *testing.T) {
	cases := map[int]string{
		0:          "0",
		42:         "42",
		1_000:      "1_000",
		1_234_567:  "1_234_567",
		-1_000_000: "-1_000_000",
	}
	for in, want := range cases {
		if got := formatObsInt(in); got != want {
			t.Errorf("formatObsInt(%d) = %q, want %q", in, got, want)
		}
	}
}
