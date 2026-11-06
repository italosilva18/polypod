package toolcache

import (
	"sort"
	"strings"
	"sync"
	"unicode"
)

// ToolSearchMode determines how deferrable tools are surfaced.
type ToolSearchMode string

const (
	// ToolSearchTST defers tools via ToolSearchTool (always enabled).
	ToolSearchTST ToolSearchMode = "tst"

	// ToolSearchAuto defers tools only when they exceed a threshold.
	ToolSearchAuto ToolSearchMode = "tst-auto"

	// ToolSearchStandard disables tool search; all tools exposed inline.
	ToolSearchStandard ToolSearchMode = "standard"
)

// ToolEntry represents an indexed tool for searching.
type ToolEntry struct {
	Name        string
	Description string
	IsMCP       bool
	IsDeferred  bool
	ServerName  string // MCP server name (extracted from tool name)

	// tokens is the pre-computed set of lowercase tokens for matching.
	tokens []string
}

// ToolIndex is an indexed collection of tool names and descriptions for fast search.
type ToolIndex struct {
	mu      sync.RWMutex
	entries []ToolEntry
}

// NewToolIndex creates a new empty tool index.
func NewToolIndex() *ToolIndex {
	return &ToolIndex{
		entries: make([]ToolEntry, 0),
	}
}

// Add registers a tool in the index.
func (idx *ToolIndex) Add(entry ToolEntry) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Pre-compute search tokens.
	entry.tokens = tokenize(entry.Name + " " + entry.Description)
	idx.entries = append(idx.entries, entry)
}

// Clear removes all entries from the index.
func (idx *ToolIndex) Clear() {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.entries = idx.entries[:0]
}

// Size returns the number of indexed tools.
func (idx *ToolIndex) Size() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.entries)
}

// SearchResult represents a tool search match with a relevance score.
type SearchResult struct {
	Entry ToolEntry
	Score float64
}

// SearchTools performs fuzzy search across available tools using the query string.
// Returns results sorted by relevance (highest score first).
func SearchTools(idx *ToolIndex, query string) []SearchResult {
	if query == "" {
		return nil
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	queryTokens := tokenize(query)
	if len(queryTokens) == 0 {
		return nil
	}

	var results []SearchResult

	for _, entry := range idx.entries {
		score := scoreMatch(queryTokens, entry)
		if score > 0 {
			results = append(results, SearchResult{
				Entry: entry,
				Score: score,
			})
		}
	}

	// Sort by score descending.
	RankResults(results)

	return results
}

// RankResults sorts search results by relevance score (highest first).
// Ties are broken alphabetically by tool name.
func RankResults(results []SearchResult) {
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Entry.Name < results[j].Entry.Name
	})
}

// SearchHint provides a search hint for tool discovery.
type SearchHint struct {
	// Query is the search string to use.
	Query string

	// ToolName is a specific tool name to find (exact match).
	ToolName string
}

// MatchHint checks if a tool entry matches a search hint.
// Returns true for exact name matches or fuzzy query matches.
func MatchHint(entry ToolEntry, hint SearchHint) bool {
	if hint.ToolName != "" {
		return strings.EqualFold(entry.Name, hint.ToolName)
	}
	if hint.Query != "" {
		tokens := tokenize(hint.Query)
		return scoreMatch(tokens, entry) > 0
	}
	return false
}

// scoreMatch computes a relevance score for a tool entry against query tokens.
// Higher scores indicate better matches.
func scoreMatch(queryTokens []string, entry ToolEntry) float64 {
	if len(queryTokens) == 0 {
		return 0
	}

	score := 0.0
	nameLower := strings.ToLower(entry.Name)

	for _, qt := range queryTokens {
		qtLower := strings.ToLower(qt)

		// Exact name match is highest priority.
		if nameLower == qtLower {
			score += 10.0
			continue
		}

		// Name prefix match.
		if strings.HasPrefix(nameLower, qtLower) {
			score += 5.0
			continue
		}

		// Name contains query token.
		if strings.Contains(nameLower, qtLower) {
			score += 3.0
			continue
		}

		// Token match in pre-computed tokens (description + name).
		tokenMatched := false
		for _, t := range entry.tokens {
			if strings.HasPrefix(t, qtLower) {
				score += 2.0
				tokenMatched = true
				break
			}
			if strings.Contains(t, qtLower) {
				score += 1.0
				tokenMatched = true
				break
			}
		}

		// Fuzzy match: check edit distance for short tokens.
		if !tokenMatched && len(qtLower) >= 3 {
			if fuzzyMatch(nameLower, qtLower) {
				score += 0.5
			}
		}
	}

	return score
}

// tokenize splits text into lowercase tokens for matching.
func tokenize(text string) []string {
	// Split on whitespace, underscores, hyphens, and camelCase boundaries.
	var tokens []string
	var current strings.Builder

	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, strings.ToLower(current.String()))
			current.Reset()
		}
	}

	runes := []rune(text)
	for i, r := range runes {
		if unicode.IsSpace(r) || r == '_' || r == '-' || r == '/' || r == ':' {
			flush()
			continue
		}

		// Split on camelCase boundary: lowercase followed by uppercase.
		if i > 0 && unicode.IsUpper(r) && unicode.IsLower(runes[i-1]) {
			flush()
		}

		current.WriteRune(r)
	}
	flush()

	return tokens
}

// fuzzyMatch performs a simple substring fuzzy match.
// Returns true if the query chars appear in order within the target.
func fuzzyMatch(target, query string) bool {
	ti := 0
	for qi := 0; qi < len(query) && ti < len(target); ti++ {
		if target[ti] == query[qi] {
			qi++
			if qi == len(query) {
				return true
			}
		}
	}
	return false
}

// ExtractServerName extracts the MCP server name from a tool name.
// MCP tool names follow the pattern "mcp__servername__toolname".
func ExtractServerName(toolName string) string {
	parts := strings.SplitN(toolName, "__", 3)
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}
