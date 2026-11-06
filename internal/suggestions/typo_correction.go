package suggestions

import (
	"sort"
	"strings"

	"github.com/costa/polypod/internal/wordsutil"
)

// TypoSuggestion is a ranked correction for a misspelled input.
//
// Note: this type is named TypoSuggestion (not Suggestion) to avoid colliding
// with the prompt-suggestion Suggestion type defined in suggestions.go.
type TypoSuggestion struct {
	Value      string
	Similarity float64 // 0-1, higher is better
	Distance   int     // Levenshtein edit distance
}

// Options controls typo correction behavior.
type Options struct {
	// MinSimilarity is the floor similarity (0-1) for suggestions (default 0.6).
	MinSimilarity float64
	// MaxDistance is the max edit distance (default 3).
	MaxDistance int
	// MaxResults is the max number of suggestions returned (default 5).
	MaxResults int
	// CaseSensitive matches case-sensitively (default false).
	CaseSensitive bool
}

// DefaultOptions returns sane defaults.
func DefaultOptions() Options {
	return Options{
		MinSimilarity: 0.6,
		MaxDistance:   3,
		MaxResults:    5,
	}
}

// Suggest returns sorted suggestions from a candidate list for a misspelled input.
func Suggest(input string, candidates []string, opts Options) []TypoSuggestion {
	if opts.MinSimilarity <= 0 {
		opts.MinSimilarity = 0.6
	}
	if opts.MaxDistance <= 0 {
		opts.MaxDistance = 3
	}
	if opts.MaxResults <= 0 {
		opts.MaxResults = 5
	}

	if input == "" || len(candidates) == 0 {
		return nil
	}

	query := input
	if !opts.CaseSensitive {
		query = strings.ToLower(query)
	}

	var results []TypoSuggestion
	for _, cand := range candidates {
		if cand == "" {
			continue
		}

		compareCand := cand
		if !opts.CaseSensitive {
			compareCand = strings.ToLower(compareCand)
		}

		// Exact match shortcut
		if compareCand == query {
			results = append(results, TypoSuggestion{
				Value:      cand,
				Similarity: 1.0,
				Distance:   0,
			})
			continue
		}

		dist := wordsutil.Levenshtein(query, compareCand)
		if dist > opts.MaxDistance {
			continue
		}

		similarity := wordsutil.Similarity(query, compareCand)
		if similarity < opts.MinSimilarity {
			continue
		}

		results = append(results, TypoSuggestion{
			Value:      cand,
			Similarity: similarity,
			Distance:   dist,
		})
	}

	// Sort: highest similarity first, then lowest distance
	sort.Slice(results, func(i, j int) bool {
		if results[i].Similarity != results[j].Similarity {
			return results[i].Similarity > results[j].Similarity
		}
		return results[i].Distance < results[j].Distance
	})

	if len(results) > opts.MaxResults {
		results = results[:opts.MaxResults]
	}

	return results
}

// SuggestTop returns just the top suggestion, or empty string if none qualify.
func SuggestTop(input string, candidates []string) string {
	suggestions := Suggest(input, candidates, DefaultOptions())
	if len(suggestions) == 0 {
		return ""
	}
	return suggestions[0].Value
}

// DidYouMean formats a "did you mean X?" message.
// Returns empty string if no good suggestion exists.
func DidYouMean(input string, candidates []string) string {
	top := SuggestTop(input, candidates)
	if top == "" {
		return ""
	}
	return "Did you mean " + top + "?"
}

// DidYouMeanMulti formats a message with multiple suggestions.
func DidYouMeanMulti(input string, candidates []string, max int) string {
	opts := DefaultOptions()
	opts.MaxResults = max
	suggestions := Suggest(input, candidates, opts)

	if len(suggestions) == 0 {
		return ""
	}

	if len(suggestions) == 1 {
		return "Did you mean " + suggestions[0].Value + "?"
	}

	values := make([]string, len(suggestions))
	for i, s := range suggestions {
		values[i] = s.Value
	}

	return "Did you mean one of: " + strings.Join(values, ", ") + "?"
}

// CorrectCase normalizes an input to match a candidate's case.
// Returns the candidate if it's a case-insensitive match for input.
func CorrectCase(input string, candidates []string) string {
	lower := strings.ToLower(input)
	for _, cand := range candidates {
		if strings.ToLower(cand) == lower {
			return cand
		}
	}
	return input
}

// NormalizeToCamel converts any case format to camelCase.
func NormalizeToCamel(s string) string {
	return wordsutil.ToCamel(s)
}

// NormalizeToSnake converts any case format to snake_case.
func NormalizeToSnake(s string) string {
	return wordsutil.ToSnake(s)
}

// NormalizeToKebab converts any case format to kebab-case.
func NormalizeToKebab(s string) string {
	return wordsutil.ToKebab(s)
}
