package compact

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/conversation"
)

func TestCompactLargeHistoryUsesBoundedSummaryBatches(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ContextWindowSize = 40_000
	cfg.MaxOutputTokens = 8_000
	cfg.ReservedOutputTokens = 8_000
	limit := min(24_000, (cfg.ContextWindowSize-cfg.ReservedOutputTokens)/4)
	calls := 0
	svc := NewCompactService(cfg, func(_ context.Context, _ string, batch []conversation.Message, _ string) (string, error) {
		calls++
		if got := EstimateTokens(batch); got > limit {
			t.Fatalf("summarizer received %d tokens, limit %d", got, limit)
		}
		return fmt.Sprintf("Resumo do lote %d: decisões e tarefas preservadas.", calls), nil
	}, nil)
	var history []conversation.Message
	for i := 0; i < 24; i++ {
		role := conversation.RoleUser
		if i%2 == 1 {
			role = conversation.RoleAssistant
		}
		history = append(history, conversation.Message{Role: role, ID: fmt.Sprintf("message-%d", i), Content: strings.Repeat("contexto detalhado ", 700)})
	}
	result, err := svc.Compact(context.Background(), history, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if calls <= 1 || result.Summary == "" || result.MessagesRemoved == 0 {
		t.Fatalf("large history was not summarized in bounded batches: calls=%d result=%+v", calls, result)
	}
}

func TestBoundedSummaryNamesExactLargeTurnAndRejectsUnrecoverableText(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ContextWindowSize = 16_000
	cfg.MaxOutputTokens = 4_000
	cfg.ReservedOutputTokens = 4_000
	svc := NewCompactService(cfg, func(_ context.Context, _ string, batch []conversation.Message, _ string) (string, error) {
		if EstimateTokens(batch) > 3000 || !strings.Contains(batch[0].Content, "message_id=long-id") {
			t.Fatalf("large message had no bounded reference: %+v", batch)
		}
		return "Tarefa longa em andamento", nil
	}, nil)
	message := conversation.Message{ID: "long-id", Role: conversation.RoleUser, Content: strings.Repeat("ação 🚀 ", 10_000)}
	ctx := conversation.WithJournalScope(context.Background(), "cli", "local", "cli:local", "default")
	store := conversation.NewStore(nil, nil, t.TempDir())
	sess := conversation.NewSession("cli:local", "cli", "local")
	sess.Messages = []conversation.Message{message}
	if err := store.Save(ctx, sess); err != nil {
		t.Fatal(err)
	}
	svc.SetJournalStore(store)
	summary, err := svc.summarizeBounded(ctx, "system", []conversation.Message{message}, "resuma")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "long-id") || !strings.Contains(summary, "history_recall") {
		t.Fatalf("recoverable reference missing from final summary: %s", summary)
	}
	if _, err := svc.summarizeBounded(context.Background(), "system", []conversation.Message{message}, "resuma"); err == nil {
		t.Fatal("unsaved giant turn was silently shortened")
	}
	otherCtx := conversation.WithJournalScope(context.Background(), "cli", "local", "cli:local", "other-agent")
	if _, err := svc.summarizeBounded(otherCtx, "system", []conversation.Message{message}, "resuma"); err == nil {
		t.Fatal("compaction promised another agent's journal reference")
	}
}

func TestAppendSummaryReferencesKeepsMoreThan64IDs(t *testing.T) {
	refs := make([]string, 65)
	for i := range refs {
		refs[i] = fmt.Sprintf("message-%02d", i)
	}
	summary := appendSummaryReferences("Resumo", refs)
	if !strings.Contains(summary, "65 mensagens") || !strings.Contains(summary, "message-00") || !strings.Contains(summary, "message-64") {
		t.Fatalf("summary dropped recoverable references: %s", summary)
	}
}
