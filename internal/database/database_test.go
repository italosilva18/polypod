package database

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
)

// newTestDB creates an isolated SQLite database under t.TempDir(). It returns
// the opened handle and registers a cleanup to close it.
func newTestDB(t *testing.T) *SQLiteDB {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	db, err := NewSQLite(path, logger)
	if err != nil {
		t.Fatalf("NewSQLite(%q) returned error: %v", path, err)
	}
	t.Cleanup(func() {
		db.Close()
	})
	return db
}

func TestNewSQLite_OpensConnection(t *testing.T) {
	db := newTestDB(t)

	if db == nil {
		t.Fatal("expected non-nil *SQLiteDB")
	}
	if db.DB == nil {
		t.Fatal("expected non-nil underlying *sql.DB")
	}
	if err := db.DB.Ping(); err != nil {
		t.Fatalf("Ping() returned error: %v", err)
	}
}

func TestNewSQLite_CreatesParentDirectories(t *testing.T) {
	root := t.TempDir()
	// Nested path that does not yet exist — NewSQLite should mkdir -p it.
	nested := filepath.Join(root, "a", "b", "c", "nested.db")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	db, err := NewSQLite(nested, logger)
	if err != nil {
		t.Fatalf("NewSQLite(%q) returned error: %v", nested, err)
	}
	defer db.Close()

	if err := db.DB.Ping(); err != nil {
		t.Fatalf("Ping() returned error: %v", err)
	}
}

func TestNewSQLite_ForeignKeysPragma(t *testing.T) {
	db := newTestDB(t)

	var fk int
	if err := db.DB.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("reading foreign_keys pragma: %v", err)
	}
	if fk != 1 {
		t.Fatalf("expected foreign_keys=1, got %d", fk)
	}
}

func TestMigrate_CreatesAllTables(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}

	want := []string{"sessions", "documents", "usage_log", "memories"}
	for _, table := range want {
		var name string
		err := db.DB.QueryRowContext(
			ctx,
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`,
			table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing after Migrate: %v", table, err)
			continue
		}
		if name != table {
			t.Errorf("expected table %q, got %q", table, name)
		}
	}
}

func TestMigrate_CreatesIndexes(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}

	want := []string{
		"idx_sessions_channel_user",
		"idx_documents_source",
		"idx_usage_log_user",
	}
	for _, idx := range want {
		var name string
		err := db.DB.QueryRowContext(
			ctx,
			`SELECT name FROM sqlite_master WHERE type='index' AND name=?`,
			idx,
		).Scan(&name)
		if err != nil {
			t.Errorf("index %q missing after Migrate: %v", idx, err)
		}
	}
}

func TestMigrate_IsIdempotent(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := db.Migrate(ctx); err != nil {
			t.Fatalf("Migrate run %d returned error: %v", i+1, err)
		}
	}

	// Verify sessions table still works after multiple Migrate calls.
	_, err := db.DB.ExecContext(
		ctx,
		`INSERT INTO sessions (id, channel, user_id) VALUES (?, ?, ?)`,
		"s1", "#general", "alice",
	)
	if err != nil {
		t.Fatalf("insert after repeated Migrate: %v", err)
	}
}

func TestSessionsCRUD(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Create
	_, err := db.DB.ExecContext(
		ctx,
		`INSERT INTO sessions (id, channel, user_id, messages) VALUES (?, ?, ?, ?)`,
		"sess-1", "#dev", "bob", `[{"role":"user","content":"hi"}]`,
	)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	// Read
	var channel, userID, messages string
	err = db.DB.QueryRowContext(
		ctx,
		`SELECT channel, user_id, messages FROM sessions WHERE id = ?`,
		"sess-1",
	).Scan(&channel, &userID, &messages)
	if err != nil {
		t.Fatalf("select session: %v", err)
	}
	if channel != "#dev" || userID != "bob" {
		t.Errorf("unexpected row: channel=%q user=%q", channel, userID)
	}
	if messages != `[{"role":"user","content":"hi"}]` {
		t.Errorf("unexpected messages: %q", messages)
	}

	// Update
	_, err = db.DB.ExecContext(
		ctx,
		`UPDATE sessions SET messages = ? WHERE id = ?`,
		`[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"}]`,
		"sess-1",
	)
	if err != nil {
		t.Fatalf("update session: %v", err)
	}

	var updated string
	if err := db.DB.QueryRowContext(ctx, `SELECT messages FROM sessions WHERE id = ?`, "sess-1").Scan(&updated); err != nil {
		t.Fatalf("select after update: %v", err)
	}
	if updated == messages {
		t.Errorf("expected messages to change after update")
	}

	// Delete
	res, err := db.DB.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, "sess-1")
	if err != nil {
		t.Fatalf("delete session: %v", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		t.Fatalf("rows affected: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 row deleted, got %d", n)
	}
}

func TestDocumentsInsertAndSelect(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	embedding := []byte{0x01, 0x02, 0x03, 0x04}
	res, err := db.DB.ExecContext(
		ctx,
		`INSERT INTO documents (source, title, chunk_index, content, embedding, metadata)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"doc.pdf", "Doc", 0, "chunk text", embedding, `{"lang":"en"}`,
	)
	if err != nil {
		t.Fatalf("insert document: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	if id <= 0 {
		t.Errorf("expected positive autoincrement id, got %d", id)
	}

	var source, title, content, metadata string
	var chunkIndex int
	var gotEmbedding []byte
	err = db.DB.QueryRowContext(
		ctx,
		`SELECT source, title, chunk_index, content, embedding, metadata FROM documents WHERE id = ?`,
		id,
	).Scan(&source, &title, &chunkIndex, &content, &gotEmbedding, &metadata)
	if err != nil {
		t.Fatalf("select document: %v", err)
	}
	if source != "doc.pdf" || title != "Doc" || chunkIndex != 0 || content != "chunk text" {
		t.Errorf("unexpected row: %q %q %d %q", source, title, chunkIndex, content)
	}
	if string(gotEmbedding) != string(embedding) {
		t.Errorf("embedding mismatch: got %v want %v", gotEmbedding, embedding)
	}
}

func TestUsageLogDefaults(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	_, err := db.DB.ExecContext(
		ctx,
		`INSERT INTO usage_log (channel, user_id, model) VALUES (?, ?, ?)`,
		"#ops", "carol", "gpt-4",
	)
	if err != nil {
		t.Fatalf("insert usage_log: %v", err)
	}

	var prompt, completion, total int
	var createdAt string
	err = db.DB.QueryRowContext(
		ctx,
		`SELECT prompt_tokens, completion_tokens, total_tokens, created_at FROM usage_log WHERE user_id = ?`,
		"carol",
	).Scan(&prompt, &completion, &total, &createdAt)
	if err != nil {
		t.Fatalf("select usage_log: %v", err)
	}
	if prompt != 0 || completion != 0 || total != 0 {
		t.Errorf("expected zero token defaults, got %d/%d/%d", prompt, completion, total)
	}
	if createdAt == "" {
		t.Error("expected created_at default to be populated")
	}
}

func TestMemoriesUniqueTopicConstraint(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	_, err := db.DB.ExecContext(
		ctx,
		`INSERT INTO memories (topic, content) VALUES (?, ?)`,
		"favorite-color", "blue",
	)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}

	// Inserting a row with the same topic must fail the UNIQUE constraint.
	_, err = db.DB.ExecContext(
		ctx,
		`INSERT INTO memories (topic, content) VALUES (?, ?)`,
		"favorite-color", "red",
	)
	if err == nil {
		t.Fatal("expected UNIQUE constraint violation, got nil")
	}
}

func TestClose_ReleasesResources(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "close.db")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	db, err := NewSQLite(path, logger)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	db.Close()

	// After Close, the connection pool is drained and any query must fail.
	if err := db.DB.Ping(); err == nil {
		t.Error("expected Ping to fail after Close, got nil")
	}
	if _, err := db.DB.Exec(`SELECT 1`); err == nil {
		t.Error("expected Exec to fail after Close, got nil")
	}
}

func TestMigrate_CanReopenExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "persist.db")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// First session: migrate and insert.
	db1, err := NewSQLite(path, logger)
	if err != nil {
		t.Fatalf("NewSQLite (1): %v", err)
	}
	if err := db1.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate (1): %v", err)
	}
	_, err = db1.DB.Exec(
		`INSERT INTO memories (topic, content) VALUES (?, ?)`,
		"persisted", "value",
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	db1.Close()

	// Second session: reopen the same file, re-run Migrate, read back.
	db2, err := NewSQLite(path, logger)
	if err != nil {
		t.Fatalf("NewSQLite (2): %v", err)
	}
	defer db2.Close()

	if err := db2.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate (2): %v", err)
	}

	var content string
	err = db2.DB.QueryRow(
		`SELECT content FROM memories WHERE topic = ?`,
		"persisted",
	).Scan(&content)
	if err != nil {
		t.Fatalf("select persisted memory: %v", err)
	}
	if content != "value" {
		t.Errorf("expected content=%q, got %q", "value", content)
	}
}
