package memory

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// SQLiteStore implements Store using a SQLite database.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore creates a new SQLite-backed memory store.
func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

func (s *SQLiteStore) Save(ctx context.Context, topic, content string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memories (topic, content, created_at, updated_at)
		 VALUES (?, ?, datetime('now'), datetime('now'))
		 ON CONFLICT(topic) DO UPDATE SET content = excluded.content, updated_at = datetime('now')`,
		topic, content,
	)
	if err != nil {
		return fmt.Errorf("saving memory: %w", err)
	}
	return nil
}

func (s *SQLiteStore) Get(ctx context.Context, topic string) (*Memory, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, topic, content, created_at, updated_at FROM memories WHERE topic = ?`, topic)
	return scanMemory(row)
}

func (s *SQLiteStore) Search(ctx context.Context, query string) ([]Memory, error) {
	like := "%" + query + "%"
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, topic, content, created_at, updated_at FROM memories
		 WHERE topic LIKE ? OR content LIKE ? ORDER BY updated_at DESC`,
		like, like,
	)
	if err != nil {
		return nil, fmt.Errorf("searching memories: %w", err)
	}
	defer rows.Close()
	return scanMemories(rows)
}

func (s *SQLiteStore) List(ctx context.Context) ([]Memory, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, topic, content, created_at, updated_at FROM memories ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing memories: %w", err)
	}
	defer rows.Close()
	return scanMemories(rows)
}

func (s *SQLiteStore) Delete(ctx context.Context, topic string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM memories WHERE topic = ?`, topic)
	if err != nil {
		return fmt.Errorf("deleting memory: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("memoria '%s' nao encontrada", topic)
	}
	return nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanMemory(row scannable) (*Memory, error) {
	var m Memory
	var createdAt, updatedAt string
	if err := row.Scan(&m.ID, &m.Topic, &m.Content, &createdAt, &updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("memoria nao encontrada")
		}
		return nil, fmt.Errorf("scanning memory: %w", err)
	}
	m.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	m.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
	return &m, nil
}

func scanMemories(rows *sql.Rows) ([]Memory, error) {
	var memories []Memory
	for rows.Next() {
		var m Memory
		var createdAt, updatedAt string
		if err := rows.Scan(&m.ID, &m.Topic, &m.Content, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scanning memory row: %w", err)
		}
		m.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		m.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
		memories = append(memories, m)
	}
	return memories, rows.Err()
}
