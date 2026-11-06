package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/skill"
)

func TestReviewAndForgetSkillsUseManagedLifecycle(t *testing.T) {
	store := NewStoreFromDB(nil, t.TempDir())
	if err := store.SaveWithMeta(context.Background(), Memory{Topic: "cidade", Content: "Recife", Source: "dream", Tags: []string{"journal:turn-42"}}); err != nil {
		t.Fatal(err)
	}
	reg := skill.NewRegistry()
	RegisterSkills(reg, store)
	review, ok := reg.Get("review_memories")
	if !ok {
		t.Fatal("review skill not registered")
	}
	text, err := review.Execute(map[string]string{"topic": "cidade"})
	if err != nil || !strings.Contains(text, "journal:turn-42") {
		t.Fatalf("review omitted evidence: %s %v", text, err)
	}
	forget, ok := reg.Get("delete_memory")
	if !ok {
		t.Fatal("forget skill missing")
	}
	if _, err := forget.Execute(map[string]string{"topic": "cidade"}); err != nil {
		t.Fatal(err)
	}
	text, err = review.Execute(map[string]string{"topic": "cidade"})
	if err != nil || !strings.Contains(text, "esquecida") || strings.Contains(text, "Recife") {
		t.Fatalf("forget failed: %s %v", text, err)
	}
}
