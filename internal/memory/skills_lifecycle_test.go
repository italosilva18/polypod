package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/skill"
)

func TestReviewAndForgetSkillsUseManagedLifecycle(t *testing.T) {
	ctx := conversation.WithJournalScope(context.Background(), "cli", "local", "branch-1", "default")
	store := NewStoreFromDB(nil, t.TempDir())
	if err := store.SaveWithMeta(ctx, Memory{Topic: "cidade", Content: "Recife", SessionID: "cli-branch:branch-1", Source: "dream", Tags: []string{"journal:turn-42"}}); err != nil {
		t.Fatal(err)
	}
	reg := skill.NewRegistry()
	RegisterSkills(reg, store)
	review, ok := reg.Get("review_memories")
	if !ok {
		t.Fatal("review skill not registered")
	}
	result, err := review.ExecuteResult(ctx, map[string]any{"topic": "cidade"})
	if err != nil || result.IsError || !strings.Contains(result.Content, "journal:turn-42") {
		t.Fatalf("review omitted evidence: %+v %v", result, err)
	}
	forget, ok := reg.Get("delete_memory")
	if !ok {
		t.Fatal("forget skill missing")
	}
	result, err = forget.ExecuteResult(ctx, map[string]any{"topic": "cidade"})
	if err != nil || result.IsError {
		t.Fatalf("forget failed: %+v %v", result, err)
	}
	result, err = review.ExecuteResult(ctx, map[string]any{"topic": "cidade"})
	if err != nil || result.IsError || !strings.Contains(result.Content, "esquecida") || strings.Contains(result.Content, "Recife") {
		t.Fatalf("forget failed: %+v %v", result, err)
	}
}
