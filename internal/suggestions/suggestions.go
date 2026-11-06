// Package suggestions provides prompt suggestion and ranking based on
// conversation history, file context, and available skills.
//
// Migrated from TypeScript: services/PromptSuggestion/{promptSuggestion,speculation}.ts
// and hooks/usePromptSuggestion.ts
package suggestions

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// Suggestion represents a single prompt suggestion with metadata.
type Suggestion struct {
	Text                string
	Source              SuggestionSource
	Score               float64
	GenerationRequestID string
	CreatedAt           time.Time
}

// SuggestionSource identifies where a suggestion came from.
type SuggestionSource int

const (
	SourceHistory SuggestionSource = iota
	SourceContext
	SourceSkill
	SourceCustom
)

// SuggestionState tracks the lifecycle of the current prompt suggestion.
type SuggestionState struct {
	Text                string
	PromptID            string
	ShownAt             time.Time
	AcceptedAt          time.Time
	GenerationRequestID string
}

// IsValid reports whether a suggestion has been displayed to the user.
func (s *SuggestionState) IsValid() bool {
	return s.Text != "" && !s.ShownAt.IsZero()
}

// ---------------------------------------------------------------------------
// Prompt suggestion service
// ---------------------------------------------------------------------------

// PromptSuggestionService generates and manages prompt suggestions.
type PromptSuggestionService struct {
	mu      sync.Mutex
	enabled bool
	current *SuggestionState

	// MinAssistantTurns is the minimum number of assistant responses
	// before suggestions are generated.
	MinAssistantTurns int

	// History of past prompts used for history-based suggestions.
	history []string

	// MaxSuggestionLen is the maximum length in runes for a suggestion.
	MaxSuggestionLen int

	// MaxSuggestionWords caps the word count.
	MaxSuggestionWords int
}

// NewPromptSuggestionService creates a new service with default settings.
func NewPromptSuggestionService() *PromptSuggestionService {
	return &PromptSuggestionService{
		enabled:            true,
		MinAssistantTurns:  2,
		MaxSuggestionLen:   100,
		MaxSuggestionWords: 12,
	}
}

// SetEnabled enables or disables suggestion generation.
func (p *PromptSuggestionService) SetEnabled(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.enabled = v
}

// IsEnabled reports whether suggestions are enabled.
func (p *PromptSuggestionService) IsEnabled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.enabled
}

// RecordPrompt adds a user prompt to the history for future suggestions.
func (p *PromptSuggestionService) RecordPrompt(prompt string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.history = append(p.history, prompt)
	// Keep history bounded.
	if len(p.history) > 200 {
		p.history = p.history[len(p.history)-200:]
	}
}

// Current returns the active suggestion state (may be nil).
func (p *PromptSuggestionService) Current() *SuggestionState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.current
}

// SetCurrent replaces the active suggestion state.
func (p *PromptSuggestionService) SetCurrent(s *SuggestionState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current = s
}

// ClearCurrent resets the active suggestion.
func (p *PromptSuggestionService) ClearCurrent() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current = nil
}

// ---------------------------------------------------------------------------
// Suggestion generation from history
// ---------------------------------------------------------------------------

// SuggestFromHistory generates suggestions based on past user prompts.
// It returns up to maxResults suggestions ordered by recency.
func (p *PromptSuggestionService) SuggestFromHistory(maxResults int) []Suggestion {
	p.mu.Lock()
	hist := make([]string, len(p.history))
	copy(hist, p.history)
	p.mu.Unlock()

	if len(hist) == 0 || maxResults <= 0 {
		return nil
	}

	// Deduplicate, preserving order (most recent first after reverse).
	seen := make(map[string]struct{})
	var unique []string
	for i := len(hist) - 1; i >= 0; i-- {
		norm := strings.TrimSpace(hist[i])
		if norm == "" {
			continue
		}
		if _, ok := seen[norm]; ok {
			continue
		}
		seen[norm] = struct{}{}
		unique = append(unique, norm)
		if len(unique) >= maxResults {
			break
		}
	}

	suggestions := make([]Suggestion, len(unique))
	for i, text := range unique {
		suggestions[i] = Suggestion{
			Text:      text,
			Source:    SourceHistory,
			Score:     1.0 - float64(i)*0.1,
			CreatedAt: time.Now(),
		}
	}
	return suggestions
}

// ---------------------------------------------------------------------------
// Suggestion generation from context
// ---------------------------------------------------------------------------

// ContextHints provides information about the current working state.
type ContextHints struct {
	OpenFiles    []string
	GitStatus    string // e.g. "modified", "staged", "clean"
	RecentErrors []string
	CurrentTask  string
}

// SuggestFromContext generates suggestions based on the current project context.
func SuggestFromContext(hints ContextHints) []Suggestion {
	var suggestions []Suggestion

	// Suggest based on git status.
	switch hints.GitStatus {
	case "modified":
		suggestions = append(suggestions, Suggestion{
			Text:      "commit these changes",
			Source:    SourceContext,
			Score:     0.8,
			CreatedAt: time.Now(),
		})
	case "staged":
		suggestions = append(suggestions, Suggestion{
			Text:      "push the staged changes",
			Source:    SourceContext,
			Score:     0.9,
			CreatedAt: time.Now(),
		})
	}

	// Suggest based on recent errors.
	if len(hints.RecentErrors) > 0 {
		suggestions = append(suggestions, Suggestion{
			Text:      "fix the failing tests",
			Source:    SourceContext,
			Score:     0.85,
			CreatedAt: time.Now(),
		})
	}

	// Suggest based on open files.
	for _, f := range hints.OpenFiles {
		if strings.HasSuffix(f, "_test.go") || strings.HasSuffix(f, ".test.ts") {
			suggestions = append(suggestions, Suggestion{
				Text:      "run the tests",
				Source:    SourceContext,
				Score:     0.7,
				CreatedAt: time.Now(),
			})
			break
		}
	}

	return suggestions
}

// ---------------------------------------------------------------------------
// Skill suggestion
// ---------------------------------------------------------------------------

// SkillInfo describes an available skill for suggestion matching.
type SkillInfo struct {
	Name        string
	Description string
	Keywords    []string
}

// SuggestSkill returns the best matching skill for a given prompt, or nil.
func SuggestSkill(prompt string, skills []SkillInfo) *SkillInfo {
	lower := strings.ToLower(prompt)
	var best *SkillInfo
	bestScore := 0

	for i := range skills {
		score := 0
		for _, kw := range skills[i].Keywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				score++
			}
		}
		if score > bestScore {
			bestScore = score
			best = &skills[i]
		}
	}
	return best
}

// ---------------------------------------------------------------------------
// Ranking
// ---------------------------------------------------------------------------

// RankSuggestions sorts suggestions by score descending and returns
// at most maxResults entries.
func RankSuggestions(suggestions []Suggestion, maxResults int) []Suggestion {
	if len(suggestions) == 0 {
		return nil
	}
	sort.SliceStable(suggestions, func(i, j int) bool {
		return suggestions[i].Score > suggestions[j].Score
	})
	if maxResults > 0 && len(suggestions) > maxResults {
		suggestions = suggestions[:maxResults]
	}
	return suggestions
}

// ---------------------------------------------------------------------------
// Suggestion filtering (ported from shouldFilterSuggestion)
// ---------------------------------------------------------------------------

// Precompiled filter patterns.
var (
	evaluativeRe  = regexp.MustCompile(`(?i)thanks|thank you|looks good|sounds good|that works|that worked|that's all|nice|great|perfect|makes sense|awesome|excellent`)
	claudeVoiceRe = regexp.MustCompile(`(?i)^(let me|i'll|i've|i'm|i can|i would|i think|i notice|here's|here is|here are|that's|this is|this will|you can|you should|you could|sure,|of course|certainly)`)
	metaTextRe    = regexp.MustCompile(`(?i)^(nothing found\.?|nothing to suggest|no suggestion)`)
	silentRe      = regexp.MustCompile(`(?i)\bsilence is\b|\bstay(s|ing)? silent\b`)
	bareSilenceRe = regexp.MustCompile(`(?i)^\W*silence\W*$`)
	wrappedRe     = regexp.MustCompile(`^\(.*\)$|^\[.*\]$`)
	multiSentRe   = regexp.MustCompile(`[.!?]\s+[A-Z]`)
	formattingRe  = regexp.MustCompile(`[\n*]|\*\*`)
	prefixLabelRe = regexp.MustCompile(`^\w+:\s`)
)

var allowedSingleWords = map[string]bool{
	"yes": true, "yeah": true, "yep": true, "yea": true, "yup": true,
	"sure": true, "ok": true, "okay": true,
	"push": true, "commit": true, "deploy": true, "stop": true,
	"continue": true, "check": true, "exit": true, "quit": true,
	"no": true,
}

// FilterReason returns the reason a suggestion should be filtered, or ""
// if it passes all checks.
func FilterReason(suggestion string) string {
	if suggestion == "" {
		return "empty"
	}

	lower := strings.ToLower(suggestion)
	words := strings.Fields(suggestion)
	wordCount := len(words)
	runeCount := utf8.RuneCountInString(suggestion)

	if lower == "done" {
		return "done"
	}
	if metaTextRe.MatchString(suggestion) || silentRe.MatchString(lower) || bareSilenceRe.MatchString(suggestion) {
		return "meta_text"
	}
	if wrappedRe.MatchString(suggestion) {
		return "meta_wrapped"
	}
	if strings.HasPrefix(lower, "api error:") || strings.HasPrefix(lower, "prompt is too long") ||
		strings.HasPrefix(lower, "request timed out") || strings.HasPrefix(lower, "invalid api key") {
		return "error_message"
	}
	if prefixLabelRe.MatchString(suggestion) {
		return "prefixed_label"
	}
	if wordCount < 2 {
		if strings.HasPrefix(suggestion, "/") {
			// slash commands are okay
		} else if !allowedSingleWords[lower] {
			return "too_few_words"
		}
	}
	if wordCount > 12 {
		return "too_many_words"
	}
	if runeCount >= 100 {
		return "too_long"
	}
	if multiSentRe.MatchString(suggestion) {
		return "multiple_sentences"
	}
	if formattingRe.MatchString(suggestion) {
		return "has_formatting"
	}
	if evaluativeRe.MatchString(lower) {
		return "evaluative"
	}
	if claudeVoiceRe.MatchString(suggestion) {
		return "claude_voice"
	}
	return ""
}

// ShouldFilter reports whether the suggestion should be discarded.
func ShouldFilter(suggestion string) bool {
	return FilterReason(suggestion) != ""
}

// ---------------------------------------------------------------------------
// Outcome tracking
// ---------------------------------------------------------------------------

// SuggestionOutcome describes what happened to a shown suggestion.
type SuggestionOutcome struct {
	Accepted   bool
	AcceptedAt time.Time
	ShownAt    time.Time
	Suggestion string
	UserInput  string
	Similarity float64
}

// ComputeOutcome determines if the user accepted or ignored the suggestion.
func ComputeOutcome(suggestion, userInput string, shownAt time.Time) SuggestionOutcome {
	accepted := userInput == suggestion
	similarity := 0.0
	if len(suggestion) > 0 {
		similarity = math.Round(float64(len(userInput))/float64(len(suggestion))*100) / 100
	}
	return SuggestionOutcome{
		Accepted:   accepted,
		AcceptedAt: time.Now(),
		ShownAt:    shownAt,
		Suggestion: suggestion,
		UserInput:  userInput,
		Similarity: similarity,
	}
}
