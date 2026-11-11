package cli

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/ai"
	"github.com/costa/polypod/internal/compact"
	"github.com/costa/polypod/internal/conversation"
)

func TestManualCompactLargeTurnUsesScopedJournal(t *testing.T) {
	store := conversation.NewStore(nil, nil, t.TempDir())
	manager := conversation.NewManager(store, slog.Default())
	ctx := conversation.WithJournalScope(context.Background(), "cli", "local", "cli:local", "default")
	sess, err := manager.GetSession(ctx, "cli", "local")
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.AddUserMessageDurable(ctx, sess, strings.Repeat("decisão extensa ação 🚀 ", 10_000))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AddAssistantMessage(ctx, sess, "Entendido"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddUserMessageDurable(ctx, sess, "Continue"); err != nil {
		t.Fatal(err)
	}
	if err := manager.AddAssistantMessage(ctx, sess, "Vou continuar"); err != nil {
		t.Fatal(err)
	}
	cfg := compact.DefaultConfig()
	cfg.ContextWindowSize = 80_000
	cfg.MaxOutputTokens = 8_000
	cfg.ReservedOutputTokens = 8_000
	seenReference := false
	compactor := compact.NewCompactService(cfg, func(_ context.Context, _ string, batch []conversation.Message, _ string) (string, error) {
		for _, message := range batch {
			if strings.Contains(message.Content, "message_id="+first.ID) {
				seenReference = true
			}
		}
		return "Decisão extensa preservada e tarefa pendente.", nil
	}, slog.Default())
	compactor.SetJournalStore(store)
	service := ai.NewService(nil, nil, nil, "default", nil, slog.Default())
	service.SetAutoCompact(compact.NewAutoCompactService(compact.AutoCompactConfig{CompactService: compactor, Enabled: true}))
	adapter := &Adapter{aiSvc: service, convMgr: manager, activeSessionID: "cli:local", logger: slog.Default()}
	deps := adapter.buildCommandDeps(context.Background())
	if deps.compactHistory == nil {
		t.Fatal("manual compact command is unavailable")
	}
	entries, _, _, err := deps.compactHistory()
	if err != nil {
		t.Fatal(err)
	}
	if !seenReference || len(entries) < 2 {
		t.Fatalf("manual compact dropped long-turn reference: seen=%v entries=%+v", seenReference, entries)
	}
	scope, _ := conversation.JournalScopeFromContext(ctx)
	if ok, err := store.CanReadJournalScoped(ctx, scope, sess.Messages[1].ID); err != nil || !ok {
		t.Fatalf("compaction summary was not saved under the active branch: %v, %v", ok, err)
	}
}
