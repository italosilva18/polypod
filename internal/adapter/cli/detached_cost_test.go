package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/adapter"
	"github.com/costa/polypod/internal/tasks"
)

func TestFinishDetachedTurnRecordsMeasuredAndUnmeasuredCost(t *testing.T) {
	dir := t.TempDir()
	reg := tasks.NewTaskRegistry()
	if err := reg.EnablePersistence(filepath.Join(dir, "tasks.json")); err != nil {
		t.Fatalf("EnablePersistence: %v", err)
	}

	state := tasks.NewTaskState(tasks.TaskTypeCLITurn, "Analisar módulo")
	state.Status = tasks.StatusRunning
	if err := reg.RegisterDurable(state); err != nil {
		t.Fatalf("RegisterDurable: %v", err)
	}

	cost := 0.0175
	m := &model{
		taskRegistry:     reg,
		detachedTaskID:   state.ID,
		parallelDetached: true,
	}
	m.finishDetachedTurn(tasks.StatusCompleted, "análise concluída", "", &adapter.Usage{
		Provider:     "openai",
		Model:        "gpt-4o",
		PromptTokens: 5000,
		TotalTokens:  5500,
		Steps:        3,
		CostUSD:      &cost,
	})

	saved, ok := reg.Get(state.ID)
	if !ok {
		t.Fatalf("task %s missing after finishDetachedTurn", state.ID)
	}
	if saved.CostUSD == nil || *saved.CostUSD != cost {
		t.Fatalf("saved.CostUSD = %v, want %v", saved.CostUSD, cost)
	}
	if saved.Progress == nil || saved.Progress.TokenCount != 5500 || saved.Progress.ToolUseCount != 3 {
		t.Fatalf("saved.Progress = %+v", saved.Progress)
	}
	formatted := formatDetachedTask(saved)
	if !strings.Contains(formatted, "Custo: US$0.0175") {
		t.Fatalf("formatDetachedTask missing measured cost: %s", formatted)
	}

	unmeasured := tasks.NewTaskState(tasks.TaskTypeCLITurn, "Protótipo sem tarifa")
	unmeasured.Status = tasks.StatusRunning
	if err := reg.RegisterDurable(unmeasured); err != nil {
		t.Fatalf("RegisterDurable unmeasured: %v", err)
	}
	m.detachedTaskID = unmeasured.ID
	m.parallelDetached = true
	m.finishDetachedTurn(tasks.StatusCompleted, "ok", "", &adapter.Usage{
		Provider:    "nvidia",
		Model:       "nvidia/nemotron-3.5-super-vl-120b-a12b",
		TotalTokens: 900,
		Steps:       1,
	})
	savedUnmeasured, ok := reg.Get(unmeasured.ID)
	if !ok {
		t.Fatalf("task %s missing", unmeasured.ID)
	}
	if savedUnmeasured.CostUSD != nil {
		t.Fatalf("expected nil CostUSD for unpriced model, got %v", *savedUnmeasured.CostUSD)
	}
	formattedUnmeasured := formatDetachedTask(savedUnmeasured)
	if !strings.Contains(formattedUnmeasured, "Custo: não medido") {
		t.Fatalf("formatDetachedTask missing unmeasured label: %s", formattedUnmeasured)
	}
}
