// Package extractmem extracts memorable facts and learnings from conversation
// history for long-term storage. It scans messages for extractable patterns
// (user preferences, project facts, decisions, learnings) using pattern matching,
// then deduplicates against existing memories and formats them for storage.
//
// Ported from src/services/extractMemories/ (TypeScript).
package extractmem

import (
	"fmt"
	"regexp"
	"strings"
)

// Categories for extracted memories.
const (
	CategoryUserPref    = "user_pref"
	CategoryProjectFact = "project_fact"
	CategoryDecision    = "decision"
	CategoryLearning    = "learning"
)

// AllCategories is the default set of extractable categories.
var AllCategories = []string{
	CategoryUserPref,
	CategoryProjectFact,
	CategoryDecision,
	CategoryLearning,
}

// Message represents a single conversation message.
type Message struct {
	Role    string // "user", "assistant", "system"
	Content string
}

// Memory represents an extracted piece of knowledge from conversation.
type Memory struct {
	Key        string  `json:"key"`
	Value      string  `json:"value"`
	Source     string  `json:"source"`     // which message produced it
	Confidence float64 `json:"confidence"` // 0.0–1.0
	Category   string  `json:"category"`   // user_pref, project_fact, decision, learning
}

// ExtractionConfig controls how memories are extracted.
type ExtractionConfig struct {
	MinConfidence float64  // minimum confidence to keep (default 0.7)
	MaxMemories   int      // max memories per extraction (default 10)
	Categories    []string // which categories to extract (default: all)
}

// DefaultConfig returns an ExtractionConfig with sensible defaults.
func DefaultConfig() ExtractionConfig {
	return ExtractionConfig{
		MinConfidence: 0.7,
		MaxMemories:   10,
		Categories:    AllCategories,
	}
}

// Extractor scans conversation messages and produces Memory values.
type Extractor struct {
	cfg      ExtractionConfig
	patterns []pattern
}

// pattern associates a compiled regex with a category and confidence.
type pattern struct {
	re         *regexp.Regexp
	category   string
	confidence float64
}

// NewExtractor creates an Extractor. Zero-value fields in cfg are replaced
// with defaults.
func NewExtractor(cfg ExtractionConfig) *Extractor {
	if cfg.MinConfidence <= 0 {
		cfg.MinConfidence = 0.7
	}
	if cfg.MaxMemories <= 0 {
		cfg.MaxMemories = 10
	}
	if len(cfg.Categories) == 0 {
		cfg.Categories = AllCategories
	}

	catSet := make(map[string]bool, len(cfg.Categories))
	for _, c := range cfg.Categories {
		catSet[c] = true
	}

	// Build patterns — only include those whose category is enabled.
	allPatterns := buildPatterns()
	var active []pattern
	for _, p := range allPatterns {
		if catSet[p.category] {
			active = append(active, p)
		}
	}

	return &Extractor{cfg: cfg, patterns: active}
}

// buildPatterns returns the full set of extraction patterns.
// Each regex uses (?i) for case-insensitive matching.
// The captured group (if present) becomes the memory value;
// otherwise the entire matched sentence is used.
func buildPatterns() []pattern {
	return []pattern{
		// --- user preferences ---
		{
			re:         regexp.MustCompile(`(?i)\balways\s+(.+?)(?:\.|$)`),
			category:   CategoryUserPref,
			confidence: 0.85,
		},
		{
			re:         regexp.MustCompile(`(?i)\bnever\s+(.+?)(?:\.|$)`),
			category:   CategoryUserPref,
			confidence: 0.85,
		},
		{
			re:         regexp.MustCompile(`(?i)\bI\s+prefer\s+(.+?)(?:\.|$)`),
			category:   CategoryUserPref,
			confidence: 0.80,
		},
		{
			re:         regexp.MustCompile(`(?i)\bI\s+(?:like|want|need)\s+(.+?)(?:\.|$)`),
			category:   CategoryUserPref,
			confidence: 0.75,
		},
		// Portuguese preference patterns (existing codebase uses pt-BR)
		{
			re:         regexp.MustCompile(`(?i)\bsempre\s+(?:usar|faça|use)\s+(.+?)(?:\.|$)`),
			category:   CategoryUserPref,
			confidence: 0.85,
		},
		{
			re:         regexp.MustCompile(`(?i)\bnunca\s+(?:usar?|faça|use)\s+(.+?)(?:\.|$)`),
			category:   CategoryUserPref,
			confidence: 0.85,
		},

		// --- project facts ---
		{
			re:         regexp.MustCompile(`(?i)\bthe\s+project\s+uses?\s+(.+?)(?:\.|$)`),
			category:   CategoryProjectFact,
			confidence: 0.90,
		},
		{
			re:         regexp.MustCompile(`(?i)\bwe\s+use\s+(.+?)(?:\.|$)`),
			category:   CategoryProjectFact,
			confidence: 0.80,
		},
		{
			re:         regexp.MustCompile(`(?i)\b(?:our|this)\s+(?:stack|codebase|repo)\s+(?:uses?|has|includes)\s+(.+?)(?:\.|$)`),
			category:   CategoryProjectFact,
			confidence: 0.85,
		},
		{
			re:         regexp.MustCompile(`(?i)\bconfigurado\s+(?:para|com)\s+(.+?)(?:\.|$)`),
			category:   CategoryProjectFact,
			confidence: 0.80,
		},
		{
			re:         regexp.MustCompile(`(?i)\bo\s+projeto\s+usa\s+(.+?)(?:\.|$)`),
			category:   CategoryProjectFact,
			confidence: 0.85,
		},

		// --- decisions ---
		{
			re:         regexp.MustCompile(`(?i)\bwe\s+decided\s+to\s+(.+?)(?:\.|$)`),
			category:   CategoryDecision,
			confidence: 0.90,
		},
		{
			re:         regexp.MustCompile(`(?i)\blet'?s\s+go\s+with\s+(.+?)(?:\.|$)`),
			category:   CategoryDecision,
			confidence: 0.80,
		},
		{
			re:         regexp.MustCompile(`(?i)\bI(?:'ve)?\s+(?:decided|chosen|picked)\s+(.+?)(?:\.|$)`),
			category:   CategoryDecision,
			confidence: 0.85,
		},
		{
			re:         regexp.MustCompile(`(?i)\b(?:decidimos|optei\s+por|escolhi)\s+(.+?)(?:\.|$)`),
			category:   CategoryDecision,
			confidence: 0.85,
		},

		// --- learnings ---
		{
			re:         regexp.MustCompile(`(?i)\bI\s+learned\s+that\s+(.+?)(?:\.|$)`),
			category:   CategoryLearning,
			confidence: 0.85,
		},
		{
			re:         regexp.MustCompile(`(?i)\b(?:TIL|turns?\s+out)\s+(.+?)(?:\.|$)`),
			category:   CategoryLearning,
			confidence: 0.80,
		},
		{
			re:         regexp.MustCompile(`(?i)\b(?:it\s+seems|apparently)\s+(.+?)(?:\.|$)`),
			category:   CategoryLearning,
			confidence: 0.70,
		},
		{
			re:         regexp.MustCompile(`(?i)\baprendi\s+que\s+(.+?)(?:\.|$)`),
			category:   CategoryLearning,
			confidence: 0.85,
		},
	}
}

// Extract scans messages for extractable memories using pattern matching.
// Only messages from "user" and "assistant" roles are considered. Results
// are filtered by MinConfidence and capped at MaxMemories.
func (e *Extractor) Extract(messages []Message) []Memory {
	var memories []Memory

	for i, msg := range messages {
		if msg.Role == "system" {
			continue
		}

		source := fmt.Sprintf("message[%d]/%s", i, msg.Role)

		for _, p := range e.patterns {
			matches := p.re.FindAllStringSubmatch(msg.Content, -1)
			for _, match := range matches {
				value := match[0] // full match
				if len(match) > 1 && match[1] != "" {
					value = strings.TrimSpace(match[1])
				}

				// Skip trivially short extractions.
				if len(value) < 5 {
					continue
				}

				// Cap value length to avoid storing huge fragments.
				if len(value) > 200 {
					value = value[:200]
				}

				key := buildKey(p.category, value)

				memories = append(memories, Memory{
					Key:        key,
					Value:      value,
					Source:     source,
					Confidence: p.confidence,
					Category:   p.category,
				})
			}
		}
	}

	// Filter by confidence.
	filtered := memories[:0]
	for _, m := range memories {
		if m.Confidence >= e.cfg.MinConfidence {
			filtered = append(filtered, m)
		}
	}
	memories = filtered

	// Cap at MaxMemories — keep highest confidence first.
	if len(memories) > e.cfg.MaxMemories {
		sortByConfidenceDesc(memories)
		memories = memories[:e.cfg.MaxMemories]
	}

	return memories
}

// Deduplicate removes from newMems any memory whose Key is sufficiently
// similar to an existing memory's Key (word-overlap similarity > 0.7).
// Returns only the truly new memories.
func Deduplicate(existing, newMems []Memory) []Memory {
	if len(existing) == 0 {
		return newMems
	}

	var unique []Memory
	for _, nm := range newMems {
		isDup := false
		for _, em := range existing {
			if keySimilarity(em.Key, nm.Key) > 0.7 {
				isDup = true
				break
			}
		}
		if !isDup {
			unique = append(unique, nm)
		}
	}
	return unique
}

// FormatForStorage renders memories as a Markdown string suitable for
// persisting to a memory file. Each memory becomes a section with
// frontmatter-style metadata.
func FormatForStorage(memories []Memory) string {
	if len(memories) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("# Extracted Memories\n\n")

	for i, m := range memories {
		if i > 0 {
			sb.WriteString("\n---\n\n")
		}
		fmt.Fprintf(&sb, "## %s\n\n", m.Key)
		fmt.Fprintf(&sb, "- **Category:** %s\n", m.Category)
		fmt.Fprintf(&sb, "- **Confidence:** %.2f\n", m.Confidence)
		fmt.Fprintf(&sb, "- **Source:** %s\n\n", m.Source)
		sb.WriteString(m.Value)
		sb.WriteByte('\n')
	}

	return sb.String()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// buildKey produces a short, normalized key from a category and value.
func buildKey(category, value string) string {
	// Take up to the first 6 significant words from the value.
	words := strings.Fields(strings.ToLower(value))
	if len(words) > 6 {
		words = words[:6]
	}
	slug := strings.Join(words, "_")

	// Remove non-alphanumeric characters except underscores.
	clean := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return -1
	}, slug)

	return category + "/" + clean
}

// keySimilarity returns the word-overlap (Dice coefficient) between two keys.
func keySimilarity(a, b string) float64 {
	a = strings.ToLower(a)
	b = strings.ToLower(b)
	if a == b {
		return 1.0
	}

	wordsA := strings.FieldsFunc(a, func(r rune) bool { return r == '/' || r == '_' || r == ' ' })
	wordsB := strings.FieldsFunc(b, func(r rune) bool { return r == '/' || r == '_' || r == ' ' })
	if len(wordsA) == 0 || len(wordsB) == 0 {
		return 0
	}

	setB := make(map[string]bool, len(wordsB))
	for _, w := range wordsB {
		setB[w] = true
	}
	common := 0
	for _, w := range wordsA {
		if setB[w] {
			common++
		}
	}
	return float64(2*common) / float64(len(wordsA)+len(wordsB))
}

// sortByConfidenceDesc sorts memories in-place by Confidence descending.
func sortByConfidenceDesc(mems []Memory) {
	for i := 0; i < len(mems); i++ {
		for j := i + 1; j < len(mems); j++ {
			if mems[j].Confidence > mems[i].Confidence {
				mems[i], mems[j] = mems[j], mems[i]
			}
		}
	}
}
