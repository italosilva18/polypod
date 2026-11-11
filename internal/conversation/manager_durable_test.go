package conversation

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestAddUserMessageDurableRestoresSessionWhenSaveFails(t *testing.T) {
	store := NewStore(nil, nil, t.TempDir())
	manager := NewManager(store, nil)
	sess, err := manager.GetSession(context.Background(), "cli", "local")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.AddUserMessageDurable(ctx, sess, "não persistida"); err == nil {
		t.Fatal("save failure was hidden")
	}
	if len(sess.Messages) != 0 || len(sess.pending) != 0 {
		t.Fatalf("failed turn remained in active session: %+v", sess)
	}
	message, err := manager.AddUserMessageDurable(context.Background(), sess, "persistida")
	if err != nil || message.ID == "" || len(sess.Messages) != 1 {
		t.Fatalf("next durable turn failed: message=%+v err=%v", message, err)
	}
}

func TestAddUserMessageDurableRollsBackJournalWhenSnapshotFailsAfterInsert(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "conversation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, channel TEXT, user_id TEXT, messages TEXT, created_at DATETIME, updated_at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	store := NewStore(nil, db, "")
	manager := NewManager(store, nil)
	ctx := context.Background()
	sess, err := manager.GetSession(ctx, "cli", "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_session_insert BEFORE INSERT ON sessions BEGIN SELECT RAISE(FAIL, 'snapshot unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	// The durable method inserts the journal row inside a transaction, then
	// attempts the snapshot. The trigger forces the second write to fail after
	// the first statement has run.
	if _, err := manager.AddUserMessageDurable(ctx, sess, "must roll back"); err == nil {
		t.Fatal("snapshot trigger should fail")
	}
	if len(sess.Messages) != 0 {
		t.Fatalf("failed turn remained in memory: %+v", sess.Messages)
	}
	entries, err := store.JournalAfter(ctx, 0, 10)
	if err != nil || len(entries) != 0 {
		t.Fatalf("orphan journal row survived rollback: %+v, %v", entries, err)
	}
	if _, err := db.Exec(`DROP TRIGGER reject_session_insert`); err != nil {
		t.Fatal(err)
	}
	msg, err := manager.AddUserMessageDurable(ctx, sess, "committed")
	if err != nil {
		t.Fatal(err)
	}
	entries, err = store.JournalAfter(ctx, 0, 10)
	if err != nil || len(entries) != 1 || entries[0].Message.ID != msg.ID {
		t.Fatalf("committed message missing from journal: %+v, %v", entries, err)
	}
	loaded, err := store.loadFromSQLite(ctx, sess.ID, sess.Channel, sess.UserID)
	if err != nil || len(loaded.Messages) != 1 || loaded.Messages[0].ID != msg.ID {
		t.Fatalf("committed message missing from snapshot: %+v, %v", loaded, err)
	}
}

func TestAddUserMessageDurableYAMLFailureDoesNotPublishJournalOnlyTurn(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(nil, nil, dir)
	manager := NewManager(store, nil)
	ctx := context.Background()
	sess, err := manager.GetSession(ctx, "cli", "local")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(store.sessionFilePath(sess), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddUserMessageDurable(ctx, sess, "must not journal"); err == nil {
		t.Fatal("snapshot path is a directory; expected failure")
	}
	entries, err := store.JournalAfter(ctx, 0, 10)
	if err != nil || len(entries) != 0 {
		t.Fatalf("YAML snapshot failure left journal-only turn: %+v, %v", entries, err)
	}
}

func TestAddUserMessageDurableYAMLJournalFailureKeepsPublishedSnapshot(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(nil, nil, dir)
	manager := NewManager(store, nil)
	ctx := context.Background()
	sess, err := manager.GetSession(ctx, "cli", "local")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(store.journalPath(), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddUserMessageDurable(ctx, sess, "snapshot published"); err == nil {
		t.Fatal("journal failure should stop AI turn")
	}
	if len(sess.Messages) != 1 {
		t.Fatalf("published snapshot was rolled back in memory: %+v", sess.Messages)
	}
	if _, err := os.Stat(store.sessionFilePath(sess)); err != nil {
		t.Fatalf("snapshot was not published before journal attempt: %v", err)
	}
	if err := os.Remove(store.journalPath()); err != nil {
		t.Fatal(err)
	}
	restarted := NewStore(nil, nil, dir)
	loaded, err := restarted.Get(ctx, "cli", "local")
	if err != nil || len(loaded.Messages) != 1 || loaded.Messages[0].Content != "snapshot published" {
		t.Fatalf("restart lost published user message: %+v, %v", loaded, err)
	}
}
