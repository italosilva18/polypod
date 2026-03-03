package database

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// SQLiteDB wraps a standard *sql.DB connection to SQLite.
type SQLiteDB struct {
	DB     *sql.DB
	logger *slog.Logger
}

// NewSQLite opens a SQLite database at the given path with recommended pragmas.
func NewSQLite(path string, logger *slog.Logger) (*SQLiteDB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("creating directory for sqlite db: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite database: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging sqlite database: %w", err)
	}

	logger.Info("sqlite database connected", "path", path)
	return &SQLiteDB{DB: db, logger: logger}, nil
}

// Migrate creates the required tables if they don't exist.
func (s *SQLiteDB) Migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			channel TEXT NOT NULL,
			user_id TEXT NOT NULL,
			messages TEXT NOT NULL DEFAULT '[]',
			created_at DATETIME NOT NULL DEFAULT (datetime('now')),
			updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_channel_user ON sessions (channel, user_id)`,

		`CREATE TABLE IF NOT EXISTS documents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			chunk_index INTEGER NOT NULL DEFAULT 0,
			content TEXT NOT NULL,
			embedding BLOB,
			metadata TEXT NOT NULL DEFAULT '{}',
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_documents_source ON documents (source)`,

		`CREATE TABLE IF NOT EXISTS usage_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel TEXT NOT NULL,
			user_id TEXT NOT NULL,
			model TEXT NOT NULL,
			prompt_tokens INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_log_user ON usage_log (channel, user_id)`,
	}

	for _, stmt := range stmts {
		if _, err := s.DB.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("executing migration: %w", err)
		}
	}

	s.logger.Info("sqlite migrations applied")
	return nil
}

// Close closes the database connection.
func (s *SQLiteDB) Close() {
	s.DB.Close()
}
