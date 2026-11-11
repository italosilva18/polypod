package conversation

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestScopedJournalRecallsSavedCompactionSummaryByID(t *testing.T) {
	store := NewStore(nil, nil, t.TempDir())
	ctx := WithJournalScope(context.Background(), "cli", "local", "cli:local", "default")
	sess := NewSession("cli:local", "cli", "local")
	sess.Messages = []Message{{ID: "summary-id", Role: RoleUser, Origin: OriginCompaction,
		Content: "This session is being continued from a previous conversation. A very long summary."}}
	if err := store.Save(ctx, sess); err != nil {
		t.Fatal(err)
	}
	scope, _ := JournalScopeFromContext(ctx)
	entry, found, err := store.ReadJournalScoped(ctx, scope, "summary-id")
	if err != nil || !found || entry.Message.Content != sess.Messages[0].Content {
		t.Fatalf("saved summary cannot be recalled: found=%v entry=%+v err=%v", found, entry, err)
	}
	accessible, err := store.CanReadJournalScoped(ctx, scope, "summary-id")
	if err != nil || !accessible {
		t.Fatalf("scope check rejected saved summary: %v, %v", accessible, err)
	}
	page, err := store.SearchJournal(ctx, scope, "very long summary", 0, 10)
	if err != nil || len(page.Entries) != 0 {
		t.Fatalf("synthetic summary appeared in original-turn search: %+v, %v", page, err)
	}
	other := JournalScope{SessionID: scope.SessionID, Channel: scope.Channel, BranchID: scope.BranchID, AgentName: "other"}
	if _, found, err := store.ReadJournalScoped(ctx, other, "summary-id"); err != nil || found {
		t.Fatalf("cross-agent summary recall escaped scope: found=%v err=%v", found, err)
	}
	if accessible, err := store.CanReadJournalScoped(ctx, other, "summary-id"); err != nil || accessible {
		t.Fatalf("cross-agent reference check escaped scope: accessible=%v err=%v", accessible, err)
	}
}

func TestSQLiteCanReadScopedJournalReference(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE sessions (
		id TEXT PRIMARY KEY, channel TEXT, user_id TEXT, messages TEXT,
		created_at TIMESTAMP, updated_at TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	store := NewStore(nil, db, "")
	ctx := WithJournalScope(context.Background(), "cli", "local", "cli:local", "default")
	sess := NewSession("cli:local", "cli", "local")
	sess.Messages = []Message{{ID: "sqlite-long-id", Role: RoleUser, Content: "long saved text"}}
	ids := []string{"sqlite-long-id"}
	for i := 0; i < 305; i++ {
		id := fmt.Sprintf("sqlite-page-%03d", i)
		sess.Messages = append(sess.Messages, Message{ID: id, Role: RoleUser, Content: "saved text"})
		ids = append(ids, id)
	}
	if err := store.Save(ctx, sess); err != nil {
		t.Fatal(err)
	}
	scope, _ := JournalScopeFromContext(ctx)
	if ok, err := store.CanReadJournalScoped(ctx, scope, "sqlite-long-id"); err != nil || !ok {
		t.Fatalf("scoped SQLite reference missing: %v, %v", ok, err)
	}
	batch, err := store.CanReadJournalScopedBatch(ctx, scope, ids)
	if err != nil || len(batch) != len(ids) {
		t.Fatalf("batched SQLite scope check lost IDs across query pages: %d/%d, %v", len(batch), len(ids), err)
	}
	scope.AgentName = "another-agent"
	if ok, err := store.CanReadJournalScoped(ctx, scope, "sqlite-long-id"); err != nil || ok {
		t.Fatalf("SQLite scope check leaked another agent's message: %v, %v", ok, err)
	}
}
