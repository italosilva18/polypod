package conversation

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestCheckCurrentSnapshotRejectsStaleSecondSQLiteProcess(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "conversation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, channel TEXT, user_id TEXT, messages TEXT, created_at DATETIME, updated_at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	first := NewManager(NewStore(nil, db, ""), nil)
	second := NewManager(NewStore(nil, db, ""), nil)
	ctx := context.Background()
	active, err := first.GetSession(ctx, "cli", "local")
	if err != nil {
		t.Fatal(err)
	}
	stale, err := second.GetSession(ctx, "cli", "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.AddUserMessageDurable(ctx, active, "one completed turn"); err != nil {
		t.Fatal(err)
	}
	if err := second.CheckCurrentSnapshot(ctx, stale); !errors.Is(err, ErrSessionStale) {
		t.Fatalf("stale SQLite view passed pre-tool check: %v", err)
	}
	if err := first.CheckCurrentSnapshot(ctx, active); err != nil {
		t.Fatalf("current SQLite view was rejected: %v", err)
	}
}

func TestCheckCurrentSnapshotRejectsStaleSecondYAMLProcess(t *testing.T) {
	dir := t.TempDir()
	first := NewManager(NewStore(nil, nil, dir), nil)
	second := NewManager(NewStore(nil, nil, dir), nil)
	ctx := context.Background()
	active, err := first.GetSession(ctx, "cli", "local")
	if err != nil {
		t.Fatal(err)
	}
	stale, err := second.GetSession(ctx, "cli", "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.AddUserMessageDurable(ctx, active, "one completed turn"); err != nil {
		t.Fatal(err)
	}
	if err := second.CheckCurrentSnapshot(ctx, stale); !errors.Is(err, ErrSessionStale) {
		t.Fatalf("stale YAML view passed pre-tool check: %v", err)
	}
	if err := first.CheckCurrentSnapshot(ctx, active); err != nil {
		t.Fatalf("current YAML view was rejected: %v", err)
	}
}
