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

const (
	semanticFloor          = 0.80
	semanticRelativeWindow = 0.08
	maxSemanticCandidates  = 128
	minSemanticCandidates  = 32
	maxLexicalCandidates   = 128
)

// HybridSearch combines lexical and semantic evidence. Provider failures,
// malformed vectors, or an absent provider preserve lexical-only behavior.
// ManagedStore uses hybridSearchIndexed for named models so the full memory
// collection is embedded only on index refresh rather than every request.
func HybridSearch(ctx context.Context, store Store, embedder Embedder, query string, max int, min float64) ([]Memory, error) {
	lexical, err := lexicalCandidates(ctx, store, query, max, min)
	if err != nil {
		return nil, err
	}
	if embedder == nil || strings.TrimSpace(query) == "" {
		return rankLexical(query, lexical, max, min), nil
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
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return rankLexical(query, lexical, max, min), nil
	}
	return rankHybrid(ctx, query, lexical, all, vectors[0], vectors[1:], max, min)
}

func hybridSearchIndexed(ctx context.Context, store Store, embedder Embedder, index *semanticIndex,
	query string, max int, min float64) ([]Memory, error) {
	lexical, err := lexicalCandidates(ctx, store, query, max, min)
	if err != nil {
		return nil, err
	}
	if embedder == nil || strings.TrimSpace(query) == "" {
		return rankLexical(query, lexical, max, min), nil
	}
	snapshot, err := index.load(ctx, store, embedder)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return rankLexical(query, lexical, max, min), nil
	}
	queryVectors, err := embedder.Embed(ctx, []string{query})
	if err != nil || len(queryVectors) != 1 {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return rankLexical(query, lexical, max, min), nil
	}
	return rankHybrid(ctx, query, lexical, snapshot.memories, queryVectors[0], snapshot.vectors, max, min)
}

func lexicalCandidates(ctx context.Context, store Store, query string, max int, min float64) ([]Memory, error) {
	limit := 0
	if max > 0 {
		limit = max
		if max <= maxLexicalCandidates/4 {
			limit = max * 4
		} else if max < maxLexicalCandidates {
			limit = maxLexicalCandidates
		}
	}
	lexical, err := store.SearchRelevant(ctx, query, limit, min)
	if err != nil {
		return nil, err
	}
	return visibleOnly(ctx, activeOnly(lexical)), nil
}

type hybridScore struct {
	memory Memory
	score  float64
}

type semanticCandidate struct {
	index      int
	similarity float64
}

func rankHybrid(ctx context.Context, query string, lexical, all []Memory, queryVector []float64,
	vectors [][]float64, max int, min float64) ([]Memory, error) {
	if len(vectors) != len(all) {
		return rankLexical(query, lexical, max, min), nil
	}
	scores := make(map[string]hybridScore, len(lexical))
	for _, m := range lexical {
		_, score := m.IsRelevant(query)
		score *= evidenceWeight(m)
		if score >= min {
			scores[m.Topic] = hybridScore{memory: m, score: score}
		}
	}
	candidates := make([]semanticCandidate, 0, len(all))
	best := 0.0
	for i := range all {
		if i%128 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		similarity, valid := cosine(queryVector, vectors[i])
		if !valid || similarity < semanticFloor {
			continue
		}
		if similarity > best {
			best = similarity
		}
		candidates = append(candidates, semanticCandidate{index: i, similarity: similarity})
	}
	// Keep the best semantic neighbors, including paraphrases with no lexical
	// overlap. A relative window avoids filling the prompt with weaker matches
	// when one or more much closer memories exist.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].similarity == candidates[j].similarity {
			return all[candidates[i].index].Topic < all[candidates[j].index].Topic
		}
		return candidates[i].similarity > candidates[j].similarity
	})
	limit := len(candidates)
	if max > 0 {
		limit = max
		if max <= maxSemanticCandidates/4 {
			limit = max * 4
			if limit < minSemanticCandidates {
				limit = minSemanticCandidates
			}
		} else if max < maxSemanticCandidates {
			limit = maxSemanticCandidates
		}
	}
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	threshold := math.Max(semanticFloor, best-semanticRelativeWindow)
	for _, candidate := range candidates {
		if candidate.similarity < threshold {
			break
		}
		m := all[candidate.index]
		weight := evidenceWeight(m)
		score := candidate.similarity * 0.8
		if old, ok := scores[m.Topic]; ok {
			// A fresh lexical lookup can see an update made by another process
			// before this scope's cached vectors refresh.
			if old.memory.Content != m.Content || old.memory.UpdatedAt.After(m.UpdatedAt) {
				continue
			}
			score = old.score/weight + 0.2*candidate.similarity
		}
		score *= weight
		if score >= min {
			scores[m.Topic] = hybridScore{memory: m, score: score}
		}
	}
	ranked := make([]hybridScore, 0, len(scores))
	for _, score := range scores {
		ranked = append(ranked, score)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].memory.Topic < ranked[j].memory.Topic
		}
		return ranked[i].score > ranked[j].score
	})
	results := make([]Memory, len(ranked))
	for i, score := range ranked {
		results[i] = score.memory
	}
	return capResults(results, max), nil
}

func evidenceWeight(m Memory) float64 {
	if m.Source != "dream" && m.Source != "dream_episode" && m.Source != "auto_extracted" {
		return 1
	}
	confidence := m.Confidence
	if math.IsNaN(confidence) || math.IsInf(confidence, 0) {
		confidence = 0
	}
	confidence = math.Max(0, math.Min(1, confidence))
	return 0.7 + 0.3*confidence
}

func rankLexical(query string, lexical []Memory, max int, min float64) []Memory {
	ranked := make([]hybridScore, 0, len(lexical))
	for _, m := range lexical {
		_, score := m.IsRelevant(query)
		score *= evidenceWeight(m)
		if score >= min {
			ranked = append(ranked, hybridScore{memory: m, score: score})
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].memory.Topic < ranked[j].memory.Topic
		}
		return ranked[i].score > ranked[j].score
	})
	results := make([]Memory, len(ranked))
	for i, score := range ranked {
		results[i] = score.memory
	}
	return capResults(results, max)
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
	similarity := dot / math.Sqrt(aa*bb)
	return similarity, !math.IsNaN(similarity) && !math.IsInf(similarity, 0)
}
