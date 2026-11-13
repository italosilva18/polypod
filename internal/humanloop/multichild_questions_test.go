package humanloop

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMultiChildQuestionQueueOutOfOrderAndDraftIsolation(t *testing.T) {
	q := NewMultiChildQuestionQueue()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type result struct {
		ans string
		err error
	}
	res := make([]result, 3)
	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		ans, err := q.Ask(ctx, "worker-db", "DB Migrator", "Usar PostgreSQL ou SQLite?")
		res[0] = result{ans, err}
	}()
	go func() {
		defer wg.Done()
		ans, err := q.Ask(ctx, "worker-ui", "Frontend Builder", "Tema escuro ou claro?")
		res[1] = result{ans, err}
	}()
	go func() {
		defer wg.Done()
		ans, err := q.Ask(ctx, "worker-ci", "CI Runner", "Rodar lint estrito?")
		res[2] = result{ans, err}
	}()

	// Wait until all 3 questions are registered.
	deadline := time.Now().Add(2 * time.Second)
	var pending []ChildQuestion
	for time.Now().Before(deadline) {
		pending = q.ListPending()
		if len(pending) == 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(pending) != 3 {
		t.Fatalf("expected 3 pending child questions, got %d", len(pending))
	}

	// Map childID -> question ID because goroutines may arrive in any scheduling order.
	idByChild := make(map[string]string, 3)
	for _, item := range pending {
		idByChild[item.ChildID] = item.ID
	}

	// Save a draft on worker-ui and ensure FormatPending displays it without resolving.
	if err := q.SaveDraft(idByChild["worker-ui"], "rascunho: escuro"); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	formatted := q.FormatPending()
	if !strings.Contains(formatted, "rascunho: escuro") {
		t.Fatalf("expected draft in FormatPending, got:\n%s", formatted)
	}

	// Answer OUT OF ORDER: worker-ci -> worker-db -> worker-ui!
	if err := q.Answer(idByChild["worker-ci"], "Sim, golangci-lint"); err != nil {
		t.Fatalf("Answer worker-ci: %v", err)
	}
	if err := q.Answer(idByChild["worker-db"], "PostgreSQL 17"); err != nil {
		t.Fatalf("Answer worker-db: %v", err)
	}
	if err := q.Answer(idByChild["worker-ui"], "Tema escuro OLED"); err != nil {
		t.Fatalf("Answer worker-ui: %v", err)
	}

	wg.Wait()

	if res[0].err != nil || res[0].ans != "PostgreSQL 17" {
		t.Fatalf("worker-db mismatch: %+v", res[0])
	}
	if res[1].err != nil || res[1].ans != "Tema escuro OLED" {
		t.Fatalf("worker-ui mismatch: %+v", res[1])
	}
	if res[2].err != nil || res[2].ans != "Sim, golangci-lint" {
		t.Fatalf("worker-ci mismatch: %+v", res[2])
	}
}
