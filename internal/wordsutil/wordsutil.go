// Package wordsutil provides word manipulation and string similarity utilities.
package wordsutil

import (
	"strings"
	"unicode"
)

// Words splits a string into words, respecting camelCase, snake_case, and kebab-case.
func Words(s string) []string {
	if s == "" {
		return nil
	}

	var words []string
	var current strings.Builder

	runes := []rune(s)
	for i, r := range runes {
		switch {
		case r == ' ' || r == '_' || r == '-' || r == '.' || r == '/':
			if current.Len() > 0 {
				words = append(words, current.String())
				current.Reset()
			}
		case unicode.IsUpper(r):
			// Start a new word if previous was lowercase (camelCase boundary)
			if i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1])) {
				if current.Len() > 0 {
					words = append(words, current.String())
					current.Reset()
				}
			}
			// Or if we're at a acronym->word boundary: "HTTPServer" -> HTTP, Server
			if i > 0 && i+1 < len(runes) && unicode.IsUpper(runes[i-1]) && unicode.IsLower(runes[i+1]) {
				if current.Len() > 0 {
					words = append(words, current.String())
					current.Reset()
				}
			}
			current.WriteRune(r)
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		words = append(words, current.String())
	}
	return words
}

// ToLowerWords splits s and lowercases every word.
func ToLowerWords(s string) []string {
	words := Words(s)
	for i := range words {
		words[i] = strings.ToLower(words[i])
	}
	return words
}

// ToCamel converts a string to camelCase.
func ToCamel(s string) string {
	words := ToLowerWords(s)
	if len(words) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(words[0])
	for _, w := range words[1:] {
		sb.WriteString(Capitalize(w))
	}
	return sb.String()
}

// ToPascal converts a string to PascalCase.
func ToPascal(s string) string {
	words := ToLowerWords(s)
	var sb strings.Builder
	for _, w := range words {
		sb.WriteString(Capitalize(w))
	}
	return sb.String()
}

// ToSnake converts a string to snake_case.
func ToSnake(s string) string {
	words := ToLowerWords(s)
	return strings.Join(words, "_")
}

// ToScreamingSnake converts a string to SCREAMING_SNAKE_CASE.
func ToScreamingSnake(s string) string {
	return strings.ToUpper(ToSnake(s))
}

// ToKebab converts a string to kebab-case.
func ToKebab(s string) string {
	words := ToLowerWords(s)
	return strings.Join(words, "-")
}

// ToTitle converts a string to Title Case.
func ToTitle(s string) string {
	words := ToLowerWords(s)
	for i := range words {
		words[i] = Capitalize(words[i])
	}
	return strings.Join(words, " ")
}

// Capitalize uppercases the first character of a string.
func Capitalize(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// Uncapitalize lowercases the first character of a string.
func Uncapitalize(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

// Pluralize returns the plural form of a word based on count.
// Simple English rules only.
func Pluralize(word string, count int) string {
	if count == 1 {
		return word
	}

	lower := strings.ToLower(word)

	// Irregular plurals
	irregular := map[string]string{
		"man":    "men",
		"woman":  "women",
		"child":  "children",
		"foot":   "feet",
		"tooth":  "teeth",
		"mouse":  "mice",
		"person": "people",
		"ox":     "oxen",
	}
	if plural, ok := irregular[lower]; ok {
		return plural
	}

	// Rules
	switch {
	case strings.HasSuffix(lower, "y") && len(lower) > 1 && !isVowel(rune(lower[len(lower)-2])):
		return word[:len(word)-1] + "ies"
	case strings.HasSuffix(lower, "s") || strings.HasSuffix(lower, "sh") ||
		strings.HasSuffix(lower, "ch") || strings.HasSuffix(lower, "x") ||
		strings.HasSuffix(lower, "z"):
		return word + "es"
	case strings.HasSuffix(lower, "f"):
		return word[:len(word)-1] + "ves"
	case strings.HasSuffix(lower, "fe"):
		return word[:len(word)-2] + "ves"
	default:
		return word + "s"
	}
}

func isVowel(r rune) bool {
	r = unicode.ToLower(r)
	return r == 'a' || r == 'e' || r == 'i' || r == 'o' || r == 'u'
}

// Singularize returns the singular form of a plural word (best-effort).
func Singularize(word string) string {
	lower := strings.ToLower(word)

	// Irregular
	irregular := map[string]string{
		"men":      "man",
		"women":    "woman",
		"children": "child",
		"feet":     "foot",
		"teeth":    "tooth",
		"mice":     "mouse",
		"people":   "person",
		"oxen":     "ox",
	}
	if sing, ok := irregular[lower]; ok {
		return sing
	}

	switch {
	case strings.HasSuffix(lower, "ies"):
		return word[:len(word)-3] + "y"
	case strings.HasSuffix(lower, "ves"):
		return word[:len(word)-3] + "f"
	case strings.HasSuffix(lower, "es") && (strings.HasSuffix(lower, "ses") || strings.HasSuffix(lower, "shes") || strings.HasSuffix(lower, "ches") || strings.HasSuffix(lower, "xes")):
		return word[:len(word)-2]
	case strings.HasSuffix(lower, "s") && !strings.HasSuffix(lower, "ss"):
		return word[:len(word)-1]
	default:
		return word
	}
}

// Levenshtein returns the edit distance between two strings.
func Levenshtein(a, b string) int {
	if a == b {
		return 0
	}

	ra := []rune(a)
	rb := []rune(b)

	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}

	// Use two rows instead of full matrix
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)

	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = min3(
				prev[j]+1,      // deletion
				curr[j-1]+1,    // insertion
				prev[j-1]+cost, // substitution
			)
		}
		prev, curr = curr, prev
	}

	return prev[len(rb)]
}

// JaroWinkler returns the Jaro-Winkler similarity between two strings (0.0-1.0).
func JaroWinkler(a, b string) float64 {
	jaro := jaroDistance(a, b)
	if jaro == 0 {
		return 0
	}

	// Common prefix (up to 4 chars)
	prefixLen := 0
	ra := []rune(a)
	rb := []rune(b)
	for i := 0; i < 4 && i < len(ra) && i < len(rb); i++ {
		if ra[i] == rb[i] {
			prefixLen++
		} else {
			break
		}
	}

	return jaro + float64(prefixLen)*0.1*(1-jaro)
}

func jaroDistance(a, b string) float64 {
	ra := []rune(a)
	rb := []rune(b)

	if len(ra) == 0 && len(rb) == 0 {
		return 1
	}
	if len(ra) == 0 || len(rb) == 0 {
		return 0
	}

	matchDistance := max(len(ra), len(rb))/2 - 1
	if matchDistance < 0 {
		matchDistance = 0
	}

	aMatches := make([]bool, len(ra))
	bMatches := make([]bool, len(rb))
	matches := 0

	for i := 0; i < len(ra); i++ {
		start := i - matchDistance
		if start < 0 {
			start = 0
		}
		end := i + matchDistance + 1
		if end > len(rb) {
			end = len(rb)
		}
		for j := start; j < end; j++ {
			if bMatches[j] {
				continue
			}
			if ra[i] != rb[j] {
				continue
			}
			aMatches[i] = true
			bMatches[j] = true
			matches++
			break
		}
	}

	if matches == 0 {
		return 0
	}

	transpositions := 0
	k := 0
	for i := 0; i < len(ra); i++ {
		if !aMatches[i] {
			continue
		}
		for !bMatches[k] {
			k++
		}
		if ra[i] != rb[k] {
			transpositions++
		}
		k++
	}

	m := float64(matches)
	return (m/float64(len(ra)) + m/float64(len(rb)) + (m-float64(transpositions)/2)/m) / 3
}

// Similarity returns a 0-1 similarity score (1.0 = identical).
func Similarity(a, b string) float64 {
	if a == b {
		return 1
	}
	maxLen := max(len(a), len(b))
	if maxLen == 0 {
		return 1
	}
	dist := Levenshtein(a, b)
	return 1 - float64(dist)/float64(maxLen)
}

// Truncate cuts a string to maxLen runes, optionally appending an ellipsis.
func Truncate(s string, maxLen int, ellipsis string) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	ellipsisRunes := []rune(ellipsis)
	if len(ellipsisRunes) >= maxLen {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-len(ellipsisRunes)]) + ellipsis
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
