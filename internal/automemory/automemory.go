package automemory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Fact is an automatically extracted piece of knowledge.
type Fact struct {
	Content   string    `json:"content"`
	Category  string    `json:"category"` // "decision", "preference", "pattern", "context"
	Source    string    `json:"source"`    // session ID or description
	CreatedAt time.Time `json:"created_at"`
	Relevance float64   `json:"relevance"` // 0-1
}

// Store manages auto-extracted memories.
type Store struct {
	dataDir string
	facts   []Fact
	logger  *slog.Logger
	maxFacts int
}

// NewStore creates an auto-memory store.
func NewStore(dataDir string, logger *slog.Logger) *Store {
	dir := filepath.Join(dataDir, "auto-memory")
	os.MkdirAll(dir, 0755)
	s := &Store{dataDir: dir, logger: logger, maxFacts: 100}
	s.Load()
	return s
}

// Load reads facts from disk.
func (s *Store) Load() {
	path := filepath.Join(s.dataDir, "facts.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	json.Unmarshal(data, &s.facts)
}

// Save persists facts to disk.
func (s *Store) Save() error {
	data, err := json.MarshalIndent(s.facts, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dataDir, "facts.json"), data, 0644)
}

// Add stores a new auto-extracted fact.
func (s *Store) Add(fact Fact) {
	if fact.CreatedAt.IsZero() {
		fact.CreatedAt = time.Now()
	}

	// Deduplicate: skip if very similar to existing
	for _, existing := range s.facts {
		if similarity(existing.Content, fact.Content) > 0.8 {
			return
		}
	}

	s.facts = append(s.facts, fact)

	// Trim oldest if over limit
	if len(s.facts) > s.maxFacts {
		s.facts = s.facts[len(s.facts)-s.maxFacts:]
	}

	s.Save()
}

// GetRelevant returns facts relevant to a query.
func (s *Store) GetRelevant(query string, maxResults int) []Fact {
	if maxResults == 0 {
		maxResults = 5
	}

	type scored struct {
		fact  Fact
		score float64
	}

	queryWords := strings.Fields(strings.ToLower(query))
	var results []scored

	for _, f := range s.facts {
		contentLower := strings.ToLower(f.Content)
		score := 0.0
		for _, w := range queryWords {
			if strings.Contains(contentLower, w) {
				score += 1.0
			}
		}
		// Recency boost
		age := time.Since(f.CreatedAt).Hours()
		if age < 24 {
			score *= 1.5
		} else if age < 168 { // 1 week
			score *= 1.2
		}

		if score > 0 {
			results = append(results, scored{f, score})
		}
	}

	// Sort by score descending
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].score > results[i].score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	var out []Fact
	for i, r := range results {
		if i >= maxResults {
			break
		}
		out = append(out, r.fact)
	}
	return out
}

// InjectContext builds a context string from relevant auto-memories.
func (s *Store) InjectContext(query string) string {
	facts := s.GetRelevant(query, 5)
	if len(facts) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[Auto-memoria]\n")
	for _, f := range facts {
		fmt.Fprintf(&sb, "- [%s] %s\n", f.Category, f.Content)
	}
	return sb.String()
}

// ExtractFromConversation analyzes messages and extracts facts.
// This is the "auto" part — called at session end.
func ExtractFromConversation(_ context.Context, messages []Message) []Fact {
	var facts []Fact

	for _, m := range messages {
		if m.Role != "assistant" {
			continue
		}
		content := m.Content

		// Extract decisions (patterns like "vou usar X", "optei por Y", "decidimos Z")
		decisionPatterns := []string{
			"vou usar", "optei por", "decidimos", "escolhi", "a melhor opcao",
			"recomendo", "devemos usar", "implementei com",
		}
		for _, p := range decisionPatterns {
			if idx := strings.Index(strings.ToLower(content), p); idx >= 0 {
				sentence := extractSentence(content, idx)
				if len(sentence) > 20 && len(sentence) < 200 {
					facts = append(facts, Fact{
						Content:  sentence,
						Category: "decision",
					})
				}
			}
		}

		// Extract technical patterns
		techPatterns := []string{
			"o padrao e", "a convencao e", "sempre usar", "nunca usar",
			"configurado para", "funciona com",
		}
		for _, p := range techPatterns {
			if idx := strings.Index(strings.ToLower(content), p); idx >= 0 {
				sentence := extractSentence(content, idx)
				if len(sentence) > 15 && len(sentence) < 200 {
					facts = append(facts, Fact{
						Content:  sentence,
						Category: "pattern",
					})
				}
			}
		}
	}

	// Limit to 3 facts per session
	if len(facts) > 3 {
		facts = facts[:3]
	}

	return facts
}

// Message mirrors conversation message.
type Message struct {
	Role    string
	Content string
}

func extractSentence(text string, startIdx int) string {
	// Find sentence boundaries
	start := startIdx
	for start > 0 && text[start-1] != '.' && text[start-1] != '\n' {
		start--
		if startIdx-start > 150 {
			break
		}
	}

	end := startIdx
	for end < len(text) && text[end] != '.' && text[end] != '\n' {
		end++
		if end-startIdx > 150 {
			break
		}
	}
	if end < len(text) {
		end++ // include the period
	}

	return strings.TrimSpace(text[start:end])
}

func similarity(a, b string) float64 {
	a = strings.ToLower(a)
	b = strings.ToLower(b)
	if a == b {
		return 1.0
	}

	wordsA := strings.Fields(a)
	wordsB := strings.Fields(b)
	if len(wordsA) == 0 || len(wordsB) == 0 {
		return 0
	}

	common := 0
	bSet := make(map[string]bool)
	for _, w := range wordsB {
		bSet[w] = true
	}
	for _, w := range wordsA {
		if bSet[w] {
			common++
		}
	}

	total := len(wordsA) + len(wordsB)
	return float64(2*common) / float64(total)
}

// Count returns number of stored facts.
func (s *Store) Count() int {
	return len(s.facts)
}

// All returns all facts.
func (s *Store) All() []Fact {
	return s.facts
}
