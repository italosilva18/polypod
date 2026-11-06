package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FileStore implements Store using a JSON file on disk.
type FileStore struct {
	path     string
	mu       sync.Mutex
	memories map[string]*Memory
}

// NewFileStore creates a new file-backed memory store.
func NewFileStore(dataDir string) *FileStore {
	path := filepath.Join(dataDir, "memories.json")
	s := &FileStore{
		path:     path,
		memories: make(map[string]*Memory),
	}
	s.load()
	return s
}

func (s *FileStore) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var list []Memory
	if err := json.Unmarshal(data, &list); err != nil {
		return
	}
	for i := range list {
		s.memories[list[i].Topic] = &list[i]
	}
}

func (s *FileStore) persist() error {
	list := make([]Memory, 0, len(s.memories))
	for _, m := range s.memories {
		list = append(list, *m)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}

func (s *FileStore) Save(_ context.Context, topic, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if m, ok := s.memories[topic]; ok {
		m.Content = content
		m.UpdatedAt = now
	} else {
		s.memories[topic] = &Memory{
			ID:        int64(len(s.memories) + 1),
			Topic:     topic,
			Content:   content,
			CreatedAt: now,
			UpdatedAt: now,
		}
	}
	return s.persist()
}

func (s *FileStore) SaveWithMeta(_ context.Context, mem Memory) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	mem.UpdatedAt = now
	if mem.CreatedAt.IsZero() {
		mem.CreatedAt = now
	}
	if existing, ok := s.memories[mem.Topic]; ok {
		mem.ID = existing.ID
		mem.CreatedAt = existing.CreatedAt
	} else {
		mem.ID = int64(len(s.memories) + 1)
	}
	s.memories[mem.Topic] = &mem
	return s.persist()
}

func (s *FileStore) SearchRelevant(_ context.Context, query string, maxResults int, minScore float64) ([]Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	type scored struct {
		mem   Memory
		score float64
	}
	var results []scored
	for _, m := range s.memories {
		relevant, score := m.IsRelevant(query)
		if relevant && score >= minScore {
			results = append(results, scored{*m, score})
		}
	}
	// Sort by score descending
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].score > results[i].score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
	if maxResults > 0 && len(results) > maxResults {
		results = results[:maxResults]
	}
	memories := make([]Memory, len(results))
	for i, r := range results {
		memories[i] = r.mem
	}
	return memories, nil
}

func (s *FileStore) Get(_ context.Context, topic string) (*Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	m, ok := s.memories[topic]
	if !ok {
		return nil, fmt.Errorf("memoria '%s' nao encontrada", topic)
	}
	return m, nil
}

func (s *FileStore) Search(_ context.Context, query string) ([]Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	q := strings.ToLower(query)
	var results []Memory
	for _, m := range s.memories {
		if strings.Contains(strings.ToLower(m.Topic), q) || strings.Contains(strings.ToLower(m.Content), q) {
			results = append(results, *m)
		}
	}
	return results, nil
}

func (s *FileStore) List(_ context.Context) ([]Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := make([]Memory, 0, len(s.memories))
	for _, m := range s.memories {
		list = append(list, *m)
	}
	return list, nil
}

func (s *FileStore) Delete(_ context.Context, topic string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.memories[topic]; !ok {
		return fmt.Errorf("memoria '%s' nao encontrada", topic)
	}
	delete(s.memories, topic)
	return s.persist()
}
