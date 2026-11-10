package session

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

// TextEmbedder is the small interface shared with the configured memory
// embedder. Search remains usable when no embedder has been configured.
type TextEmbedder interface {
	Embed(context.Context, []string) ([][]float64, error)
}

type rankedSession struct {
	summary SessionSummary
	text    string
	exact   bool
	score   float64
}

// A slow local model must not freeze the synchronous CLI slash command. One
// in-flight embedding attempt per process also bounds goroutines if the
// embedder is waiting on work started elsewhere.
var semanticSearchSlot = make(chan struct{}, 1)

// SearchRelevant combines full-text matches with recent-session candidates.
// When an embedder is available it reranks the bounded candidate set by
// meaning as well as words. A failed embedding returns lexical results and
// the error so callers can log the degraded mode without hiding matches.
func SearchRelevant(ctx context.Context, storage SessionStorage, embedder TextEmbedder, query string, maxResults int) ([]SessionSummary, error) {
	if storage == nil {
		return nil, fmt.Errorf("armazenamento de sessões indisponível")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if maxResults <= 0 {
		maxResults = 20
	}
	if maxResults > 100 {
		maxResults = 100
	}
	exact, err := storage.Search(query)
	if err != nil {
		return nil, err
	}
	recent, err := storage.List(SessionFilter{MaxCount: 80})
	if err != nil {
		return nil, err
	}
	candidates := make([]rankedSession, 0, 130)
	seen := make(map[string]int, 130)
	add := func(summary SessionSummary, isExact bool) {
		if index, exists := seen[summary.ID]; exists {
			candidates[index].exact = candidates[index].exact || isExact
			return
		}
		seen[summary.ID] = len(candidates)
		candidates = append(candidates, rankedSession{summary: summary, exact: isExact})
	}
	for _, summary := range exact[:min(len(exact), 50)] {
		add(summary, true)
	}
	for _, summary := range recent {
		add(summary, false)
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	terms := searchTerms(query)
	texts := make([]string, 0, len(candidates)+1)
	texts = append(texts, query)
	for i := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidates[i].text = searchCandidateText(storage, candidates[i].summary)
		candidates[i].score = lexicalSessionScore(candidates[i], query, terms)
		texts = append(texts, candidates[i].text)
	}
	var semanticErr error
	if embedder != nil {
		vectors, err := embedSessionsWithDeadline(ctx, embedder, texts)
		if err != nil {
			semanticErr = fmt.Errorf("busca semântica indisponível: %w", err)
		} else if len(vectors) != len(texts) {
			semanticErr = fmt.Errorf("busca semântica retornou %d vetores para %d textos", len(vectors), len(texts))
		} else {
			for i := range candidates {
				similarity := cosineSession(vectors[0], vectors[i+1])
				if similarity >= 0.35 {
					candidates[i].score += similarity * 10
				}
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].summary.UpdatedAt.After(candidates[j].summary.UpdatedAt)
	})
	results := make([]SessionSummary, 0, min(maxResults, len(candidates)))
	for _, candidate := range candidates {
		if candidate.score <= 0 {
			continue
		}
		results = append(results, candidate.summary)
		if len(results) >= maxResults {
			break
		}
	}
	return results, semanticErr
}

func embedSessionsWithDeadline(parent context.Context, embedder TextEmbedder, texts []string) ([][]float64, error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	select {
	case semanticSearchSlot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	type result struct {
		vectors [][]float64
		err     error
	}
	done := make(chan result, 1)
	go func() {
		defer func() { <-semanticSearchSlot }()
		vectors, err := embedder.Embed(ctx, texts)
		done <- result{vectors: vectors, err: err}
	}()
	select {
	case value := <-done:
		return value.vectors, value.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func searchCandidateText(storage SessionStorage, summary SessionSummary) string {
	var builder strings.Builder
	builder.WriteString(summary.Title)
	builder.WriteByte(' ')
	builder.WriteString(summary.Tag)
	builder.WriteByte(' ')
	builder.WriteString(summary.GitBranch)
	builder.WriteByte(' ')
	builder.WriteString(summary.Preview)
	if paged, ok := storage.(interface {
		LoadPage(string, int, int) (*SessionPage, error)
	}); ok {
		page, err := paged.LoadPage(summary.ID, 0, 6)
		if err == nil && page != nil {
			for _, msg := range page.Messages {
				if msg.Role != "user" && msg.Role != "assistant" {
					continue
				}
				builder.WriteByte(' ')
				builder.WriteString(truncateSessionText(msg.Content, 350))
			}
		}
	}
	text := strings.TrimSpace(truncateSessionText(builder.String(), 1800))
	if text == "" {
		return summary.ID
	}
	return text
}

func truncateSessionText(value string, maxRunes int) string {
	runes := []rune(value)
	if len(runes) > maxRunes {
		runes = runes[:maxRunes]
	}
	return string(runes)
}

func lexicalSessionScore(candidate rankedSession, query string, terms []string) float64 {
	title := strings.ToLower(candidate.summary.Title)
	preview := strings.ToLower(candidate.summary.Preview)
	text := strings.ToLower(candidate.text)
	query = strings.ToLower(query)
	var score float64
	if candidate.exact {
		score += 4
	}
	if strings.Contains(title, query) {
		score += 12
	}
	if strings.Contains(preview, query) {
		score += 6
	}
	for _, term := range terms {
		if strings.Contains(title, term) {
			score += 2
		} else if strings.Contains(text, term) {
			score += 0.6
		}
	}
	if score > 0 {
		age := time.Since(candidate.summary.UpdatedAt)
		if age <= 7*24*time.Hour {
			score += 0.6
		} else if age <= 30*24*time.Hour {
			score += 0.2
		}
	}
	return score
}

func searchTerms(query string) []string {
	stopwords := map[string]struct{}{
		"como": {}, "com": {}, "essa": {}, "esse": {}, "esta": {}, "este": {},
		"minha": {}, "minhas": {}, "meu": {}, "meus": {}, "para": {}, "pela": {},
		"pelo": {}, "por": {}, "qual": {}, "quais": {}, "que": {}, "sobre": {},
		"the": {}, "and": {}, "for": {}, "with": {},
	}
	fields := strings.FieldsFunc(strings.ToLower(query), func(ch rune) bool {
		return !unicode.IsLetter(ch) && !unicode.IsDigit(ch)
	})
	seen := make(map[string]struct{}, len(fields))
	terms := make([]string, 0, len(fields))
	for _, field := range fields {
		if len([]rune(field)) < 3 {
			continue
		}
		if _, ignored := stopwords[field]; ignored {
			continue
		}
		if _, exists := seen[field]; exists {
			continue
		}
		seen[field] = struct{}{}
		terms = append(terms, field)
	}
	return terms
}

func cosineSession(left, right []float64) float64 {
	if len(left) == 0 || len(left) != len(right) {
		return 0
	}
	var dot, leftNorm, rightNorm float64
	for i := range left {
		dot += left[i] * right[i]
		leftNorm += left[i] * left[i]
		rightNorm += right[i] * right[i]
	}
	if leftNorm == 0 || rightNorm == 0 {
		return 0
	}
	value := dot / math.Sqrt(leftNorm*rightNorm)
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}
