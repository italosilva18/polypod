package memory

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"time"
)

const forgottenTag = "memory:forgotten"
const supersededTag = "memory:superseded"

// ManagedStore separates active facts, reviewable revisions, and suppression
// tombstones. It never touches the private conversation journal.
type ManagedStore struct {
	Store
	embedder Embedder
	mu       sync.Mutex // serializes fact transitions and forget operations
}

func NewManagedStore(base Store) *ManagedStore { return &ManagedStore{Store: base} }

func hasTag(m Memory, tag string) bool {
	for _, t := range m.Tags {
		if t == tag {
			return true
		}
	}
	return false
}
func active(m Memory) bool { return !hasTag(m, forgottenTag) && !hasTag(m, supersededTag) }

func (s *ManagedStore) Save(ctx context.Context, topic, content string) error {
	return s.SaveWithMeta(ctx, Memory{Topic: topic, Content: content, Source: "user", Confidence: 1})
}

func (s *ManagedStore) SaveWithMeta(ctx context.Context, m Memory) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.Store.List(ctx)
	if err != nil {
		return err
	}
	for _, old := range all {
		if old.Topic != m.Topic {
			continue
		}
		// Explicit forget blocks automatic re-learning, even after cursor resets.
		// A later explicit save is the only operation which revokes suppression.
		if hasTag(old, forgottenTag) && m.Source != "user" {
			return nil
		}
		if old.Content == m.Content && old.Source == m.Source {
			// Merge additional evidence without manufacturing a new content revision.
			changed := false
			old.Tags = append([]string(nil), old.Tags...)
			for _, tag := range m.Tags {
				if !hasTag(old, tag) {
					old.Tags = append(old.Tags, tag)
					changed = true
				}
			}
			if !changed {
				return nil
			}
			return s.Store.SaveWithMeta(ctx, old)
		}
		if active(old) && old.Content != "" {
			sum := sha256.Sum256([]byte(old.Topic + "\x00" + old.Content + "\x00" + strings.Join(old.Tags, "\x00")))
			revision := old
			revision.Topic = fmt.Sprintf("revision_%x", sum[:12])
			revision.Tags = append(append([]string(nil), old.Tags...), supersededTag, "memory:parent="+old.Topic, "memory:observed_at="+old.UpdatedAt.Format(time.RFC3339Nano))
			if err := s.Store.SaveWithMeta(ctx, revision); err != nil {
				return fmt.Errorf("archiving previous memory: %w", err)
			}
		}
		break
	}
	return s.Store.SaveWithMeta(ctx, m)
}

// Delete forgets derived content and revisions but retains a content-free
// suppression marker, preventing automatic consolidation from recreating it.
func (s *ManagedStore) Delete(ctx context.Context, topic string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.Store.List(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, m := range all {
		if m.Topic == topic {
			found = true
		}
		if hasTag(m, "memory:parent="+topic) {
			if err := s.Store.Delete(ctx, m.Topic); err != nil {
				return err
			}
		}
	}
	if !found {
		return fmt.Errorf("memoria '%s' nao encontrada", topic)
	}
	return s.Store.SaveWithMeta(ctx, Memory{Topic: topic, Source: "user", Tags: []string{forgottenTag}, Confidence: 1})
}

func (s *ManagedStore) Get(ctx context.Context, topic string) (*Memory, error) {
	m, err := s.Store.Get(ctx, topic)
	if err != nil {
		return nil, err
	}
	if m == nil || !active(*m) {
		return nil, fmt.Errorf("memoria '%s' nao esta ativa", topic)
	}
	return m, nil
}
func (s *ManagedStore) List(ctx context.Context) ([]Memory, error) {
	all, err := s.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	return activeOnly(all), nil
}
func (s *ManagedStore) Search(ctx context.Context, q string) ([]Memory, error) {
	all, err := s.Store.Search(ctx, q)
	if err != nil {
		return nil, err
	}
	return activeOnly(all), nil
}
func (s *ManagedStore) SearchRelevant(ctx context.Context, q string, max int, min float64) ([]Memory, error) {
	s.mu.Lock()
	embedder := s.embedder
	s.mu.Unlock()
	all, err := HybridSearch(ctx, s.Store, embedder, q, 0, min)
	if err != nil {
		return nil, err
	}
	all = activeOnly(all)
	if max > 0 && len(all) > max {
		all = all[:max]
	}
	return all, nil
}
func activeOnly(all []Memory) []Memory {
	out := make([]Memory, 0, len(all))
	for _, m := range all {
		if active(m) {
			out = append(out, m)
		}
	}
	return out
}

// Review includes inactive revisions and forget markers for inspection only.
func (s *ManagedStore) Review(ctx context.Context, topic string) ([]Memory, error) {
	all, err := s.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	if topic == "" {
		return all, nil
	}
	out := []Memory{}
	for _, m := range all {
		if m.Topic == topic || hasTag(m, "memory:parent="+topic) {
			out = append(out, m)
		}
	}
	return out, nil
}

// SetEmbedder enables semantic retrieval only when a real provider is available.
// Passing nil restores lexical-only retrieval.
func (s *ManagedStore) SetEmbedder(embedder Embedder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.embedder = embedder
}
