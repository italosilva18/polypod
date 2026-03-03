package web

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// SearchResult represents a single search result.
type SearchResult struct {
	Title   string
	URL     string
	Snippet string
}

// WebSearch performs a search using DuckDuckGo HTML and returns top results.
func WebSearch(query string) ([]SearchResult, error) {
	searchURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))

	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating search request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("performing search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("reading search results: %w", err)
	}

	return parseSearchResults(string(body)), nil
}

// parseSearchResults extracts results from DuckDuckGo HTML response.
func parseSearchResults(htmlContent string) []SearchResult {
	tokenizer := html.NewTokenizer(strings.NewReader(htmlContent))
	var results []SearchResult
	var current SearchResult
	inResult := false
	inTitle := false
	inSnippet := false

	for {
		tt := tokenizer.Next()
		if tt == html.ErrorToken {
			break
		}

		switch tt {
		case html.StartTagToken:
			token := tokenizer.Token()
			if token.Data == "div" {
				for _, attr := range token.Attr {
					if attr.Key == "class" && strings.Contains(attr.Val, "result__body") {
						inResult = true
						current = SearchResult{}
					}
				}
			}
			if inResult && token.Data == "a" {
				for _, attr := range token.Attr {
					if attr.Key == "class" && strings.Contains(attr.Val, "result__a") {
						inTitle = true
						for _, a := range token.Attr {
							if a.Key == "href" {
								current.URL = extractDDGURL(a.Val)
							}
						}
					}
				}
			}
			if inResult && token.Data == "a" {
				for _, attr := range token.Attr {
					if attr.Key == "class" && strings.Contains(attr.Val, "result__snippet") {
						inSnippet = true
					}
				}
			}

		case html.TextToken:
			if inTitle {
				current.Title += tokenizer.Token().Data
			}
			if inSnippet {
				current.Snippet += tokenizer.Token().Data
			}

		case html.EndTagToken:
			token := tokenizer.Token()
			if token.Data == "a" {
				if inTitle {
					inTitle = false
				}
				if inSnippet {
					inSnippet = false
					current.Snippet = strings.TrimSpace(current.Snippet)
					if current.Title != "" {
						results = append(results, current)
					}
					if len(results) >= 5 {
						return results
					}
					inResult = false
				}
			}
		}
	}

	return results
}

// extractDDGURL extracts the actual URL from DuckDuckGo redirect links.
func extractDDGURL(ddgURL string) string {
	if strings.Contains(ddgURL, "uddg=") {
		parts := strings.SplitN(ddgURL, "uddg=", 2)
		if len(parts) == 2 {
			decoded, err := url.QueryUnescape(parts[1])
			if err == nil {
				// Remove any trailing parameters
				if idx := strings.Index(decoded, "&"); idx > 0 {
					decoded = decoded[:idx]
				}
				return decoded
			}
		}
	}
	return ddgURL
}
