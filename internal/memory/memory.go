package memory

import (
	"context"
	"time"
)

// Memory represents a persistent memory entry.
type Memory struct {
	ID        int64
	Topic     string
	Content   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Store is the interface for memory persistence.
type Store interface {
	Save(ctx context.Context, topic, content string) error
	Get(ctx context.Context, topic string) (*Memory, error)
	Search(ctx context.Context, query string) ([]Memory, error)
	List(ctx context.Context) ([]Memory, error)
	Delete(ctx context.Context, topic string) error
}
