package sessionmem

import (
	"sort"
	"strings"
	"unicode"
)

var queryStopwords = map[string]bool{
	"para": true, "como": true, "isso": true, "esse": true, "essa": true,
	"qual": true, "quais": true, "quando": true, "onde": true, "sobre": true,
	"pode": true, "fazer": true, "faça": true, "faca": true, "usar": true,
	"with": true, "what": true, "when": true, "where": true, "from": true,
	"have": true, "this": true, "that": true, "using": true,
}

func meaningfulTerms(text string) []string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	seen := make(map[string]bool)
	terms := make([]string, 0, len(words))
	for _, word := range words {
		if len([]rune(word)) < 4 || queryStopwords[word] || seen[word] {
			continue
		}
		seen[word] = true
		terms = append(terms, word)
	}
	return terms
}

// Relevant returns a short section for the current query. It does not dump the
// full file into the prompt and skips entries already visible verbatim in the
// retained conversation. New user instructions always take precedence.
func (s *FileStore) Relevant(scope, query, visible string, maxChars int) (string, error) {
	terms := meaningfulTerms(query)
	if len(terms) == 0 {
		return "", nil
	}
	if maxChars <= 0 || maxChars > 1600 {
		maxChars = 1000
	}
	entries, err := s.Load(scope)
	if err != nil {
		return "", err
	}
	type scored struct {
		entry Entry
		score int
	}
	visible = strings.ToLower(visible)
	candidates := make([]scored, 0, len(entries))
	for _, entry := range entries {
		entry, ok := cleanEntry(entry)
		if !ok || strings.Contains(visible, strings.ToLower(entry.Value)) {
			continue
		}
		value := strings.ToLower(entry.Value)
		score := 0
		for _, term := range terms {
			if strings.Contains(value, term) {
				score++
			}
		}
		if score > 0 {
			candidates = append(candidates, scored{entry: entry, score: score})
		}
	}
	if len(candidates) == 0 {
		return "", nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].entry.CreatedAt.After(candidates[j].entry.CreatedAt)
	})

	var output strings.Builder
	output.WriteString("Memória desta sessão (afirmações explícitas anteriores do usuário; a solicitação atual prevalece):\n")
	count := 0
	for _, candidate := range candidates {
		line := "- [" + candidate.entry.Category + "] " + candidate.entry.Value + "\n"
		if output.Len()+len(line) > maxChars {
			continue
		}
		output.WriteString(line)
		count++
		if count == 4 {
			break
		}
	}
	if count == 0 {
		return "", nil
	}
	return output.String(), nil
}
