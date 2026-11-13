package codesearch

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/costa/polypod/internal/tokens"
)

// ContextOptions bounds on-demand repository context. Semantic ranking is
// optional and never required for lexical results.
type ContextOptions struct {
	Limit       int
	TokenBudget int
	Model       string
	Semantic    bool
	Ranking     string // hybrid, lexical, map, or semantic; empty uses hybrid
}

type ContextMatch struct {
	Match
	Reasons         []string
	EstimatedTokens int
}

type rankedChunk struct {
	item         chunk
	lexical      float64
	mapScore     float64
	semantic     float64
	symbolMatched bool
	modified     bool
	lexRank      int
	mapRank      int
	semanticRank int
	combined     float64
}

// SearchContext fuses path, symbol, content, recent edits, and optional
// embedding evidence. The same token budget applies regardless of ranking
// variant, making offline ablations directly comparable.
func (e *Engine) SearchContext(ctx context.Context, root, query string, opts ContextOptions) ([]ContextMatch, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("consulta vazia")
	}
	if opts.Limit <= 0 || opts.Limit > 20 {
		opts.Limit = 5
	}
	if opts.TokenBudget <= 0 || opts.TokenBudget > 16000 {
		opts.TokenBudget = 2000
	}
	if opts.Model == "" {
		opts.Model = "gpt-4"
	}
	if opts.Ranking == "" {
		opts.Ranking = "hybrid"
	}
	switch opts.Ranking {
	case "lexical", "map", "semantic", "hybrid":
	default:
		return nil, fmt.Errorf("modo de busca inválido: %q", opts.Ranking)
	}
	if opts.Ranking == "semantic" && (e == nil || e.embedder == nil) {
		return nil, errors.New("modo semântico requer embeddings configurados")
	}
	cfg, err := LoadConfig(root)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, errors.New("índice de código desativado; use /index on")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	items, err := collectChunks(ctx, root, cfg)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	terms := queryTerms(query)
	idf := corpusTermIDF(items, terms)
	fileSymbols := collectFileSymbols(items)
	modified := modifiedPaths(ctx, root)
	ranked := make([]rankedChunk, len(items))
	var maxLexical, maxMap, maxSemantic float64
	for i, item := range items {
		score := lexicalScore(item, terms)
		mapScore, symHit := contextMapScore(item, fileSymbols[item.match.Path], terms, idf)
		changed := modified[item.match.Path]
		if changed && mapScore > 0 {
			mapScore += .5
		}
		if score > maxLexical {
			maxLexical = score
		}
		if mapScore > maxMap {
			maxMap = mapScore
		}
		ranked[i] = rankedChunk{item: item, lexical: score, mapScore: mapScore, symbolMatched: symHit, modified: changed}
	}
	lexOrder := make([]int, len(ranked))
	for i := range ranked {
		lexOrder[i] = i
	}
	sort.SliceStable(lexOrder, func(i, j int) bool {
		a, b := ranked[lexOrder[i]], ranked[lexOrder[j]]
		if a.lexical != b.lexical {
			return a.lexical > b.lexical
		}
		return chunkLess(a.item, b.item)
	})
	for rank, i := range lexOrder {
		if ranked[i].lexical > 0 {
			ranked[i].lexRank = rank + 1
		}
	}
	mapOrder := append([]int(nil), lexOrder...)
	sort.SliceStable(mapOrder, func(i, j int) bool {
		a, b := ranked[mapOrder[i]], ranked[mapOrder[j]]
		if a.mapScore != b.mapScore {
			return a.mapScore > b.mapScore
		}
		return chunkLess(a.item, b.item)
	})
	for rank, i := range mapOrder {
		if ranked[i].mapScore > 0 {
			ranked[i].mapRank = rank + 1
		}
	}
	if (opts.Ranking == "semantic" || opts.Ranking == "hybrid" && opts.Semantic) && e != nil && e.embedder != nil {
		vectors, vectorErr := e.ensureVectors(ctx, items)
		if vectorErr == nil {
			queryVector, queryErr := e.embedder.Embed(ctx, []string{query})
			if queryErr == nil && len(queryVector) == 1 {
				for i, item := range items {
					ranked[i].semantic = cosine(queryVector[0], vectors[item.hash])
					if ranked[i].semantic > maxSemantic {
						maxSemantic = ranked[i].semantic
					}
				}
				semOrder := append([]int(nil), lexOrder...)
				sort.SliceStable(semOrder, func(i, j int) bool {
					a, b := ranked[semOrder[i]], ranked[semOrder[j]]
					if a.semantic != b.semantic {
						return a.semantic > b.semantic
					}
					return chunkLess(a.item, b.item)
				})
				for rank, i := range semOrder {
					if ranked[i].semantic > 0 {
						ranked[i].semanticRank = rank + 1
					}
				}
			} else if opts.Ranking == "semantic" {
				if queryErr == nil {
					queryErr = errors.New("resposta de embedding incompleta")
				}
				return nil, fmt.Errorf("embedding da consulta: %w", queryErr)
			} else if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		} else if opts.Ranking == "semantic" {
			return nil, fmt.Errorf("embeddings do código: %w", vectorErr)
		} else if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	semCutoffRank := max(12, len(ranked)/2)
	for i := range ranked {
		switch opts.Ranking {
		case "lexical":
			if ranked[i].lexRank > 0 {
				ranked[i].combined = 1 / float64(60+ranked[i].lexRank)
			}
		case "map":
			if ranked[i].mapRank > 0 {
				ranked[i].combined = 1 / float64(60+ranked[i].mapRank)
			}
		case "semantic":
			if ranked[i].semanticRank > 0 {
				ranked[i].combined = 1 / float64(60+ranked[i].semanticRank)
			}
		case "hybrid":
			if ranked[i].lexRank > 0 {
				ranked[i].combined += 1.0 / float64(20+ranked[i].lexRank)
				if maxLexical > 0 {
					ranked[i].combined += .15 * (ranked[i].lexical / maxLexical) / 21
				}
			}
			if ranked[i].mapRank > 0 {
				strength := 1.0
				if maxMap > 0 {
					strength = ranked[i].mapScore / maxMap
				}
				ranked[i].combined += .65 * strength / float64(20+ranked[i].mapRank)
			}
			if ranked[i].semanticRank > 0 && (ranked[i].semanticRank <= semCutoffRank || ranked[i].lexRank == 0 && ranked[i].mapRank == 0) {
				ranked[i].combined += .90 / float64(20+ranked[i].semanticRank)
				if maxSemantic > 0 {
					ranked[i].combined += .15 * (ranked[i].semantic / maxSemantic) / 21
				}
			}
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if math.Abs(ranked[i].combined-ranked[j].combined) > 1e-12 {
			return ranked[i].combined > ranked[j].combined
		}
		return chunkLess(ranked[i].item, ranked[j].item)
	})
	remaining := opts.TokenBudget
	perSlotCap := remaining
	if opts.Limit > 1 {
		perSlotCap = max(opts.TokenBudget/opts.Limit, 120)
	}
	selected := make([]ContextMatch, 0, opts.Limit)
	selectedPaths := make(map[string]bool)
	selectedChunks := make(map[int]bool)
	// First cover distinct files within a fair per-slot budget so early 80-line
	// chunks do not starve cross-file recall@k.
	for pass := 0; pass < 2 && len(selected) < opts.Limit && remaining >= 12; pass++ {
		for index, candidate := range ranked {
			if len(selected) == opts.Limit || remaining < 12 || candidate.combined == 0 {
				break
			}
			if selectedChunks[index] || pass == 0 && selectedPaths[candidate.item.match.Path] {
				continue
			}
			slotBudget := remaining
			if pass == 0 && perSlotCap < slotBudget {
				slotBudget = perSlotCap
			}
			match, ok := fitContext(candidate.item.match, terms, slotBudget, opts.Model)
			if !ok && slotBudget < remaining {
				match, ok = fitContext(candidate.item.match, terms, remaining, opts.Model)
			}
			if !ok {
				continue
			}
			match.Score = candidate.combined
			reasons := make([]string, 0, 4)
			if candidate.lexRank > 0 && (opts.Ranking == "lexical" || opts.Ranking == "hybrid") {
				reasons = append(reasons, "lexical")
			}
			if candidate.mapRank > 0 && (opts.Ranking == "map" || opts.Ranking == "hybrid") {
				reasons = append(reasons, "mapa")
			}
			if candidate.mapRank > 0 && (opts.Ranking == "map" || opts.Ranking == "hybrid") && candidate.symbolMatched {
				reasons = append(reasons, "símbolo")
			}
			if candidate.modified && (opts.Ranking == "map" || opts.Ranking == "hybrid") {
				reasons = append(reasons, "arquivo alterado")
			}
			if candidate.semanticRank > 0 && (opts.Ranking == "semantic" || opts.Ranking == "hybrid") {
				reasons = append(reasons, "semântico")
			}
			used := tokens.EstimateTokens(opts.Model, match.Path+"\n"+match.Snippet) + 32
			if used > remaining {
				continue
			}
			selected = append(selected, ContextMatch{Match: match, Reasons: reasons, EstimatedTokens: used})
			selectedPaths[match.Path] = true
			selectedChunks[index] = true
			remaining -= used
		}
	}
	return selected, nil
}

func chunkLess(a, b chunk) bool {
	if a.match.Path != b.match.Path {
		return a.match.Path < b.match.Path
	}
	return a.match.StartLine < b.match.StartLine
}

func queryTerms(query string) []string {
	words := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	stop := map[string]bool{"the": true, "and": true, "for": true, "with": true, "who": true, "what": true,
		"where": true, "how": true, "may": true, "are": true, "was": true, "that": true,
		"que": true, "uma": true, "para": true, "com": true, "sem": true, "como": true,
		"onde": true, "qual": true, "por": true, "dos": true, "das": true}
	seen := make(map[string]bool)
	var terms []string
	for _, word := range words {
		if len([]rune(word)) < 3 || seen[word] || stop[word] {
			continue
		}
		seen[word] = true
		terms = append(terms, word)
	}
	return terms
}

func splitIdentifierTokens(raw string) []string {
	var tokens []string
	var buf []rune
	flush := func() {
		if len(buf) >= 2 {
			tokens = append(tokens, strings.ToLower(string(buf)))
		}
		buf = buf[:0]
	}
	runes := []rune(raw)
	for i, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if i > 0 && unicode.IsUpper(r) && len(buf) > 0 {
			prev := runes[i-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush()
			}
		}
		buf = append(buf, r)
	}
	flush()
	return tokens
}

func stemTerm(term string) string {
	for _, suffix := range []string{"tion", "ing", "able", "ible", "ed", "es", "s"} {
		if !strings.HasSuffix(term, suffix) {
			continue
		}
		base := term[:len(term)-len(suffix)]
		if (suffix == "ing" || suffix == "ed") && len(base) >= 4 && base[len(base)-1] == base[len(base)-2] {
			switch base[len(base)-1] {
			case 'b', 'd', 'g', 'm', 'n', 'p', 'r', 't':
				return base[:len(base)-1]
			}
		}
		if len(base) >= 4 || (suffix == "s" && len(base) >= 3) {
			return base
		}
	}
	return term
}

func corpusTermIDF(items []chunk, terms []string) map[string]float64 {
	idf := make(map[string]float64, len(terms))
	if len(terms) == 0 {
		return idf
	}
	df := make(map[string]int, len(terms))
	for _, item := range items {
		text := strings.ToLower(item.text)
		for _, term := range terms {
			stem := stemTerm(term)
			if strings.Contains(text, term) || len(stem) >= 4 && strings.Contains(text, stem) {
				df[term]++
			}
		}
	}
	total := float64(len(items))
	for _, term := range terms {
		idf[term] = math.Log(1 + (total+1)/float64(df[term]+1))
	}
	return idf
}

func isDeclarationLine(trimmedLower string) bool {
	for _, prefix := range []string{"func ", "type ", "const ", "var ", "def ", "class ", "function ", "export function ", "export class ", "interface "} {
		if strings.HasPrefix(trimmedLower, prefix) {
			return true
		}
	}
	return false
}

func collectFileSymbols(items []chunk) map[string][]string {
	seen := make(map[string]map[string]bool)
	result := make(map[string][]string)
	for _, item := range items {
		for _, line := range strings.Split(item.match.Snippet, "\n") {
			trimmed := strings.TrimSpace(line)
			if !isDeclarationLine(strings.ToLower(trimmed)) {
				continue
			}
			if seen[item.match.Path] == nil {
				seen[item.match.Path] = make(map[string]bool)
			}
			if !seen[item.match.Path][trimmed] {
				seen[item.match.Path][trimmed] = true
				result[item.match.Path] = append(result[item.match.Path], trimmed)
			}
		}
	}
	return result
}

func termMatchesToken(term, stem, tok string) float64 {
	if tok == term {
		return 1.0
	}
	tokStem := stemTerm(tok)
	if len(stem) >= 3 && tokStem == stem {
		return 0.85
	}
	if len(term) >= 4 && (strings.HasPrefix(tok, term) || strings.HasPrefix(term, tok) && len(tok) >= 4) {
		return 0.75
	}
	if len(term) >= 4 && strings.Contains(tok, term) {
		return 0.6
	}
	return 0
}

func lexicalScore(item chunk, terms []string) float64 {
	body := strings.ToLower(item.match.Snippet)
	var score float64
	for _, term := range terms {
		count := strings.Count(body, term)
		if count > 0 {
			score += 1 + math.Log1p(float64(min(count, 8)))
		}
	}
	return score
}

func contextMapScore(item chunk, fileSymbols []string, terms []string, idf map[string]float64) (float64, bool) {
	ext := filepath.Ext(item.match.Path)
	pathNoExt := strings.TrimSuffix(item.match.Path, ext)
	pathTokens := splitIdentifierTokens(pathNoExt)
	pathLower := strings.ToLower(pathNoExt)
	var score float64
	for _, term := range terms {
		weight := idf[term]
		if weight <= 0 {
			weight = 1
		}
		stem := stemTerm(term)
		best := 0.0
		for _, tok := range pathTokens {
			if m := termMatchesToken(term, stem, tok); m > best {
				best = m
			}
		}
		if best == 0 && len(term) >= 4 && strings.Contains(pathLower, term) {
			best = 0.55
		}
		if best > 0 {
			score += 2.5 * weight * best
		}
	}
	chunkSym := declarationScore(strings.Split(item.match.Snippet, "\n"), terms, idf)
	fileSym := declarationScore(fileSymbols, terms, idf)
	symHit := chunkSym > 0 || fileSym > 0
	if chunkSym > 0 {
		score += chunkSym
	} else if fileSym > 0 {
		score += 0.5 * fileSym
	}
	return score, symHit
}

func declarationScore(lines []string, terms []string, idf map[string]float64) float64 {
	if len(lines) == 0 || len(terms) == 0 {
		return 0
	}
	var declTokens []string
	var declRaw strings.Builder
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if !isDeclarationLine(lower) {
			continue
		}
		declTokens = append(declTokens, splitIdentifierTokens(trimmed)...)
		declRaw.WriteString(lower)
		declRaw.WriteByte('\n')
	}
	if len(declTokens) == 0 {
		return 0
	}
	rawLower := declRaw.String()
	var score float64
	for _, term := range terms {
		weight := idf[term]
		if weight <= 0 {
			weight = 1
		}
		stem := stemTerm(term)
		best := 0.0
		for _, tok := range declTokens {
			if m := termMatchesToken(term, stem, tok); m > best {
				best = m
			}
		}
		if best == 0 && strings.Contains(rawLower, term) {
			best = 0.65
		}
		if best > 0 {
			score += 2.0 * weight * best
		}
	}
	return score
}

func modifiedPaths(ctx context.Context, root string) map[string]bool {
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain=v1", "-z", "--untracked-files=all")
	cmd.Dir = root
	raw, err := cmd.Output()
	if err != nil {
		return nil
	}
	result := make(map[string]bool)
	parts := strings.Split(string(raw), "\x00")
	for i := 0; i < len(parts); i++ {
		part := parts[i]
		if len(part) < 4 {
			continue
		}
		result[part[3:]] = true
		if (part[0] == 'R' || part[1] == 'R' || part[0] == 'C' || part[1] == 'C') && i+1 < len(parts) {
			i++
		}
	}
	return result
}

func fitContext(match Match, terms []string, budget int, model string) (Match, bool) {
	if tokens.EstimateTokens(model, match.Path+"\n"+match.Snippet)+32 <= budget {
		return match, true
	}
	lines := strings.Split(match.Snippet, "\n")
	center := 0
	bestLineScore := -1.0
	for i, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		lineScore := 0.0
		if isDeclarationLine(lower) {
			lineScore += 0.5
		}
		for _, term := range terms {
			stem := stemTerm(term)
			if strings.Contains(lower, term) {
				lineScore += 1.0
			} else if len(stem) >= 4 && strings.Contains(lower, stem) {
				lineScore += 0.7
			}
		}
		if lineScore > bestLineScore {
			bestLineScore = lineScore
			center = i
		}
	}
	start, end := center, center+1
	fits := func(a, b int) bool {
		return tokens.EstimateTokens(model, match.Path+"\n"+strings.Join(lines[a:b], "\n"))+32 <= budget
	}
	if !fits(start, end) {
		return Match{}, false
	}
	for start > 0 || end < len(lines) {
		changed := false
		if start > 0 && fits(start-1, end) {
			start--
			changed = true
		}
		if end < len(lines) && fits(start, end+1) {
			end++
			changed = true
		}
		if !changed {
			break
		}
	}
	match.StartLine += start
	match.EndLine = match.StartLine + end - start - 1
	match.Snippet = strings.Join(lines[start:end], "\n")
	return match, true
}

func FormatContext(matches []ContextMatch) string {
	if len(matches) == 0 {
		return "Nenhum trecho relevante encontrado."
	}
	var out strings.Builder
	for _, match := range matches {
		fmt.Fprintf(&out, "%s:%d-%d [%s; ~%d tokens]\n%s\n\n", match.Path, match.StartLine, match.EndLine,
			strings.Join(match.Reasons, ", "), match.EstimatedTokens, match.Snippet)
	}
	return strings.TrimSpace(out.String())
}
