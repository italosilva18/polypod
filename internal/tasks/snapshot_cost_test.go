package tasks

import (
	"log/slog"
	"sync"
	"testing"
)

func TestTaskRegistrySnapshotCostIsolation(t *testing.T) {
	reg := NewTaskRegistry()
	state := RegisterLocalAgent(reg, "task-cost-1", "Executar agente", "prompt", "coder", "", slog.Default())
	t.Cleanup(func() { removeAgentState("task-cost-1") })

	initial := 0.0125
	UpdateAgentProgress(reg, state.ID, AgentProgress{
		TokenCount:   100,
		ToolUseCount: 2,
		CostUSD:      &initial,
	})

	snap, ok := reg.Get(state.ID)
	if !ok || snap.CostUSD == nil || snap.Progress == nil || snap.Progress.CostUSD == nil {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	// Mutating returned snapshot must not affect stored task state.
	*snap.CostUSD = 99.0
	*snap.Progress.CostUSD = 99.0

	fresh, _ := reg.Get(state.ID)
	if *fresh.CostUSD != 0.0125 || *fresh.Progress.CostUSD != 0.0125 {
		t.Fatalf("snapshot aliased internal CostUSD pointer: got %v / %v", *fresh.CostUSD, *fresh.Progress.CostUSD)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 1; i <= 100; i++ {
			c := float64(i) * 0.001
			UpdateAgentProgress(reg, state.ID, AgentProgress{
				TokenCount:   i * 10,
				ToolUseCount: i,
				CostUSD:      &c,
			})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if st, ok := GetAgentState(state.ID); ok && st.Progress != nil && st.Progress.CostUSD != nil {
				_ = *st.Progress.CostUSD
			}
			for _, item := range reg.List() {
				if item.CostUSD != nil {
					_ = *item.CostUSD
				}
			}
		}
	}()
	wg.Wait()
}
