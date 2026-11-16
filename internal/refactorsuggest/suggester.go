// Package suggester — Refactor Suggestions
package refactorsuggest

type RefactorSuggester struct{}

type RefactorSuggestion struct {
	Type        string
	Description string
	Priority    string
	CodeBefore  string
	CodeAfter   string
}

func NewRefactorSuggester() *RefactorSuggester {
	return &RefactorSuggester{}
}

func (s *RefactorSuggester) Analyze(code string) []RefactorSuggestion {
	return []RefactorSuggestion{
		{
			Type:        "extract_method",
			Description: "Extract this block to a separate method",
			Priority:    "high",
		},
		{
			Type:        "rename_variable",
			Description: "Rename variable for clarity",
			Priority:    "medium",
		},
	}
}

func (s *RefactorSuggester) FormatSuggestions(suggestions []RefactorSuggestion) string {
	result := "💡 **Refactor Suggestions**:\n\n"
	for i, sug := range suggestions {
		result += string(rune(i+1)) + ". " + sug.Type + " - " + sug.Description + " (priority: " + sug.Priority + ")\n"
	}
	return result
}
