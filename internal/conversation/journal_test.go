package conversation

import (
	"context"
	"database/sql"
	"fmt"
	_ "modernc.org/sqlite"
	"path/filepath"
	"testing"
)

func TestJournalSurvivesEvictionRestartAndRepeatedSave(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := NewStore(nil, nil, dir)
	sess, err := store.Get(ctx, "cli", "user")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 125; i++ {
		sess.AddMessage(RoleUser, fmt.Sprintf("turn %03d", i))
	}
	if len(sess.Messages) != MaxMessages {
		t.Fatal("context must remain bounded")
	}
	if err := store.Save(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, sess); err != nil {
		t.Fatal(err)
	}
	store = NewStore(nil, nil, dir)
	entries, err := store.JournalAfter(ctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 125 {
		t.Fatalf("lost/duplicated messages: %d", len(entries))
	}
	for i, e := range entries {
		if e.Message.Content != fmt.Sprintf("turn %03d", i) || e.Sequence != uint64(i+1) || e.Message.ID == "" {
			t.Fatalf("bad evidence at %d: %+v", i, e)
		}
	}
	sess, err = store.Get(ctx, "cli", "user")
	if err != nil {
		t.Fatal(err)
	}
	sess.AddMessage(RoleUser, "after restart")
	if err := store.Save(ctx, sess); err != nil {
		t.Fatal(err)
	}
	tail, err := store.JournalAfter(ctx, 125, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 1 || tail[0].Sequence != 126 {
		t.Fatalf("bad restart cursor: %+v", tail)
	}
}

func TestSQLiteJournalPersistsWithoutSessionTable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "journal.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(nil, db, "")
	sess := NewSession("cli:user", "cli", "user")
	for i := 0; i < 40; i++ {
		sess.AddMessage(RoleUser, fmt.Sprint(i))
	}
	// Journal commits first, even if the session snapshot backend is unavailable.
	if err := store.Save(ctx, sess); err == nil {
		t.Fatal("expected missing session table")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store = NewStore(nil, db, "")
	entries, err := store.JournalAfter(ctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 40 {
		t.Fatalf("journal lost messages after snapshot failure: %d", len(entries))
	}
}
