package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

const (
	semanticIndexTTL       = time.Minute
	semanticIndexMaxScopes = 4
)

// semanticIndex retains vectors within one ManagedStore. The persistent
// CachedEmbedder still owns model-versioned disk caching; this index avoids
// reading every vector from SQLite for every agent turn. A scope-specific
// snapshot never embeds another session's episodic memories for this query.
type semanticIndex struct {
	gate       chan struct{}
	generation atomic.Uint64
	snapshots  map[string]semanticSnapshot
}

type semanticSnapshot struct {
	memories   []Memory
	vectors    [][]float64
	builtAt    time.Time
	generation uint64
}

func newSemanticIndex() *semanticIndex {
	return &semanticIndex{gate: make(chan struct{}, 1), snapshots: make(map[string]semanticSnapshot)}
}

func (idx *semanticIndex) acquire(ctx context.Context) error {
	select {
	case idx.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (idx *semanticIndex) release() { <-idx.gate }

func (idx *semanticIndex) invalidate() { idx.generation.Add(1) }

func (idx *semanticIndex) load(ctx context.Context, store Store, embedder Embedder) (semanticSnapshot, error) {
	named, ok := embedder.(interface{ Name() string })
	if !ok || strings.TrimSpace(named.Name()) == "" {
		return semanticSnapshot{}, errors.New("identidade do modelo de embeddings ausente")
	}
	if err := idx.acquire(ctx); err != nil {
		return semanticSnapshot{}, err
	}
	defer idx.release()

	key := named.Name() + "\x00" + sessionScope(ctx)
	generation := idx.generation.Load()
	if snapshot, ok := idx.snapshots[key]; ok && snapshot.generation == generation &&
		time.Since(snapshot.builtAt) < semanticIndexTTL {
		return snapshot, nil
	}
	all, err := store.List(ctx)
	if err != nil {
		return semanticSnapshot{}, err
	}
	all = visibleOnly(ctx, activeOnly(all))
	texts := make([]string, 0, len(all))
	for _, m := range all {
		texts = append(texts, m.Topic+"\n"+m.Content)
	}
	var vectors [][]float64
	if len(texts) > 0 {
		vectors, err = embedder.Embed(ctx, texts)
		if err != nil {
			return semanticSnapshot{}, fmt.Errorf("gerar indice semantico: %w", err)
		}
		if len(vectors) != len(texts) {
			return semanticSnapshot{}, errors.New("indice semantico incompleto")
		}
	}
	if idx.generation.Load() != generation {
		return semanticSnapshot{}, errors.New("indice semantico alterado durante a leitura")
	}
	snapshot := semanticSnapshot{memories: all, vectors: vectors, builtAt: time.Now(), generation: generation}
	if len(idx.snapshots) >= semanticIndexMaxScopes {
		oldestKey := ""
		var oldestTime time.Time
		for candidate, existing := range idx.snapshots {
			if oldestKey == "" || existing.builtAt.Before(oldestTime) {
				oldestKey, oldestTime = candidate, existing.builtAt
			}
		}
		delete(idx.snapshots, oldestKey)
	}
	idx.snapshots[key] = snapshot
	return snapshot, nil
}
