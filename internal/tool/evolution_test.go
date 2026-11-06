package tool

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/costa/polypod/internal/evolution"
	"github.com/costa/polypod/internal/types"
)

func TestEvolutionToolReviewedApplyAndActivationRetry(t *testing.T) {
	dir := t.TempDir()
	store, err := evolution.Open(filepath.Join(dir, "state", "evolution.db"), filepath.Join(dir, ".polypod", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	activations := 0
	registry := NewRegistry()
	RegisterEvolutionTool(registry, store, func(name string) error {
		if name == "" {
			t.Fatal("activation received empty name")
		}
		activations++
		return nil
	})
	tool, ok := registry.Get("skill_evolution")
	if !ok || !tool.IsDestructive(map[string]any{"action": "apply"}) ||
		!tool.IsReadOnly(map[string]any{"action": "patterns"}) {
		t.Fatal("evolution tool classification is incorrect")
	}
	ctx := types.ToolContext{Context: context.Background()}
	for _, id := range []string{"run-1", "run-2", "run-3"} {
		result, err := registry.Execute("skill_evolution", map[string]any{
			"action": "record", "source_kind": "task", "source_id": id,
			"pattern_key": "browse report", "success": true,
			"verified_by": "user-1", "evidence_ref": "task:" + id,
			"tool_names": []any{"browser_observe", "browser_act"},
		}, ctx)
		if err != nil || result.IsError {
			t.Fatalf("record %s = %+v, %v", id, result, err)
		}
	}
	result, err := registry.Execute("skill_evolution", map[string]any{"action": "propose", "pattern_key": "browse report"}, ctx)
	if err != nil || result.IsError {
		t.Fatalf("propose = %+v, %v", result, err)
	}
	var draft evolution.Draft
	if err := json.Unmarshal([]byte(result.Content), &draft); err != nil {
		t.Fatal(err)
	}
	result, err = registry.Execute("skill_evolution", map[string]any{"action": "apply", "draft_id": draft.ID}, ctx)
	if err != nil || !result.IsError || activations != 0 {
		t.Fatalf("apply without confirmation = %+v, %v, activations %d", result, err, activations)
	}
	result, err = registry.Execute("skill_evolution", map[string]any{
		"action": "review", "draft_id": draft.ID, "decision": "approve", "reviewer": "user-1",
	}, ctx)
	if err != nil || result.IsError {
		t.Fatalf("review = %+v, %v", result, err)
	}
	args := map[string]any{"action": "apply", "draft_id": draft.ID, "confirm": true}
	for range 2 {
		result, err = registry.Execute("skill_evolution", args, ctx)
		if err != nil || result.IsError {
			t.Fatalf("apply = %+v, %v", result, err)
		}
	}
	if activations != 2 {
		t.Fatalf("activation callback count = %d, want 2 for retry", activations)
	}
}
