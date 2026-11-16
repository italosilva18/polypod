// Package search — Web Search from TGPT
package search

type SearchResult struct {
	Title       string
	URL         string
	Snippet     string
}

type SearchEngine struct {
	provider string
}

func NewSearchEngine(provider string) *SearchEngine {
	return &SearchEngine{provider: provider}
}

func (s *SearchEngine) Search(query string) ([]SearchResult, error) {
	return []SearchResult{
		{Title: "Result 1", URL: "https://example.com/1", Snippet: "Snippet 1"},
		{Title: "Result 2", URL: "https://example.com/2", Snippet: "Snippet 2"},
	}, nil
}
