package memory

import (
	"context"
	"math"
	"sort"
	"strings"
)

// Embedder is optional and must be explicitly configured by the host. All
// vectors in a call must use the same model. No network provider is assumed.
type Embedder interface {
	Embed(context.Context, []string) ([][]float64, error)
}

// HybridSearch combines lexical and semantic evidence. Provider failures,
// malformed vectors, or an absent provider preserve lexical-only behavior.
func HybridSearch(ctx context.Context, store Store, embedder Embedder, query string, max int, min float64) ([]Memory, error) {
	lexical, err := store.SearchRelevant(ctx, query, 0, min)
	if err != nil {
		return nil, err
	}
	lexical = visibleOnly(ctx, lexical)
	if embedder == nil || strings.TrimSpace(query) == "" {
		return capResults(lexical, max), nil
	}
	all, err := store.List(ctx)
	if err != nil {
		return nil, err
	}
	all = visibleOnly(ctx, activeOnly(all))
	texts := make([]string, 1, len(all)+1)
	texts[0] = query
	for _, m := range all {
		texts = append(texts, m.Topic+"\n"+m.Content)
	}
	vectors, err := embedder.Embed(ctx, texts)
	if err != nil || len(vectors) != len(texts) {
		return capResults(lexical, max), nil
	}
	type scored struct {
		memory Memory
		score  float64
	}
	scores := make(map[string]scored)
	for _, m := range lexical {
		_, score := m.IsRelevant(query)
		scores[m.Topic] = scored{m, score}
	}
	for i, m := range all {
		similarity, valid := cosine(vectors[0], vectors[i+1])
		if !valid || similarity < 0.80 {
			continue
		}
		// A semantic-only candidate needs strong evidence independent of priority.
		score := similarity * 0.8
		if old, ok := scores[m.Topic]; ok {
			score = old.score + 0.2*similarity
		}
		if score >= min {
			scores[m.Topic] = scored{m, score}
		}
	}
	ranked := make([]scored, 0, len(scores))
	for _, s := range scores {
		ranked = append(ranked, s)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].memory.Topic < ranked[j].memory.Topic
		}
		return ranked[i].score > ranked[j].score
	})
	results := make([]Memory, len(ranked))
	for i, s := range ranked {
		results[i] = s.memory
	}
	return capResults(results, max), nil
}
func capResults(results []Memory, max int) []Memory {
	if max > 0 && len(results) > max {
		return results[:max]
	}
	return results
}
func cosine(a, b []float64) (float64, bool) {
	if len(a) == 0 || len(a) != len(b) {
		return 0, false
	}
	var dot, aa, bb float64
	for i, x := range a {
		y := b[i]
		if math.IsNaN(x) || math.IsInf(x, 0) || math.IsNaN(y) || math.IsInf(y, 0) {
			return 0, false
		}
		dot += x * y
		aa += x * x
		bb += y * y
	}
	if aa == 0 || bb == 0 {
		return 0, false
	}
	return dot / math.Sqrt(aa*bb), true
}
