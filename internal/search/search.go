package search

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SessionSearchResult is a match found in session history.
type SessionSearchResult struct {
	SessionID string
	Title     string
	Line      int
	Role      string
	Content   string // snippet with match context
	Timestamp time.Time
}

// SearchSessions searches through all saved sessions for a query.
func SearchSessions(dataDir, query string, maxResults int) []SessionSearchResult {
	if maxResults == 0 {
		maxResults = 20
	}

	sessionsDir := filepath.Join(dataDir, "sessions")
	queryLower := strings.ToLower(query)
	var results []SessionSearchResult

	// Search JSON session files
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dataDir, e.Name())
		matches := searchFile(path, queryLower, e.Name())
		results = append(results, matches...)

		if len(results) >= maxResults {
			break
		}
	}

	// Also search sessions dir
	if entries2, err := os.ReadDir(sessionsDir); err == nil {
		for _, e := range entries2 {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || e.Name() == "index.json" {
				continue
			}
			path := filepath.Join(sessionsDir, e.Name())
			matches := searchFile(path, queryLower, e.Name())
			results = append(results, matches...)

			if len(results) >= maxResults {
				break
			}
		}
	}

	if len(results) > maxResults {
		results = results[:maxResults]
	}
	return results
}

func searchFile(path, queryLower, filename string) []SessionSearchResult {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	sessionID := strings.TrimSuffix(filename, ".json")
	var results []SessionSearchResult

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if strings.Contains(strings.ToLower(line), queryLower) {
			// Try to extract role/content from JSON
			var msg struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			}
			json.Unmarshal([]byte(line), &msg)

			snippet := extractSnippet(line, queryLower, 100)

			results = append(results, SessionSearchResult{
				SessionID: sessionID,
				Line:      lineNum,
				Role:      msg.Role,
				Content:   snippet,
			})

			if len(results) >= 5 { // max 5 per file
				break
			}
		}
	}

	return results
}

func extractSnippet(text, query string, contextLen int) string {
	lower := strings.ToLower(text)
	idx := strings.Index(lower, query)
	if idx < 0 {
		if len(text) > contextLen*2 {
			return text[:contextLen*2] + "..."
		}
		return text
	}

	start := idx - contextLen
	if start < 0 {
		start = 0
	}
	end := idx + len(query) + contextLen
	if end > len(text) {
		end = len(text)
	}

	snippet := text[start:end]
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(text) {
		snippet = snippet + "..."
	}
	return snippet
}

// FormatResults formats search results for display.
func FormatResults(results []SessionSearchResult, query string) string {
	if len(results) == 0 {
		return fmt.Sprintf("Nenhum resultado encontrado para '%s'.", query)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Busca: '%s' (%d resultados)\n\n", query, len(results))

	for _, r := range results {
		role := r.Role
		if role == "" {
			role = "?"
		}
		fmt.Fprintf(&sb, "- **%s** (linha %d) [%s]\n  %s\n\n",
			r.SessionID, r.Line, role, r.Content)
	}
	return sb.String()
}

// SearchCurrent searches within the current session messages.
func SearchCurrent(messages []struct{ Role, Content string }, query string) []int {
	queryLower := strings.ToLower(query)
	var indices []int
	for i, m := range messages {
		if strings.Contains(strings.ToLower(m.Content), queryLower) {
			indices = append(indices, i)
		}
	}
	return indices
}

// RegisterSkills registers transcript search skills.
func RegisterSkills(reg interface{ Register(s interface{}) }, dataDir string, logger *slog.Logger) {
	// This is intentionally loosely typed to avoid circular imports.
	// The actual registration happens in main.go using the concrete skill.Registry.
	_ = dataDir
	_ = logger
}
