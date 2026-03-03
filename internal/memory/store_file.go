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
