package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// SearchResult represents a single search result.
type SearchResult struct {
	Title    string `json:"title"`
	URL      string `json:"url"`
	Snippet  string `json:"snippet"`
	Position int    `json:"position"`
}

// defaultMaxResults is the default number of search results to return.
const defaultMaxResults = 5

// ---------------------------------------------------------------------------
// SearchEngine interface
// ---------------------------------------------------------------------------

// SearchEngine abstracts a web search backend.
type SearchEngine interface {
	Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error)
}

// ---------------------------------------------------------------------------
// DuckDuckGoEngine — scrapes DuckDuckGo HTML (no API key needed)
// ---------------------------------------------------------------------------

// DuckDuckGoEngine performs searches via the DuckDuckGo HTML endpoint.
type DuckDuckGoEngine struct{}

// Search scrapes html.duckduckgo.com and returns up to maxResults results.
func (e *DuckDuckGoEngine) Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	if maxResults <= 0 {
		maxResults = defaultMaxResults
	}

	searchURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
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

	results := parseSearchResults(string(body), maxResults)
	return results, nil
}

// ---------------------------------------------------------------------------
// BraveEngine — Brave Search API (requires X-Subscription-Token)
// ---------------------------------------------------------------------------

// BraveEngine uses the Brave Search API.
type BraveEngine struct {
	APIKey string
}

// braveResponse is the top-level Brave Search API response.
type braveResponse struct {
	Web struct {
		Results []braveResult `json:"results"`
	} `json:"web"`
}

// braveResult is a single item in the Brave web results.
type braveResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

// Search queries the Brave Search API and returns up to maxResults results.
func (e *BraveEngine) Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	if maxResults <= 0 {
		maxResults = defaultMaxResults
	}
	if e.APIKey == "" {
		return nil, fmt.Errorf("brave search: API key is required")
	}

	endpoint := fmt.Sprintf(
		"https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
		url.QueryEscape(query), maxResults,
	)

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("brave search: creating request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", e.APIKey)
	req.Header.Set("User-Agent", userAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("brave search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("brave search HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("brave search: reading response: %w", err)
	}

	var br braveResponse
	if err := json.Unmarshal(body, &br); err != nil {
		return nil, fmt.Errorf("brave search: decoding JSON: %w", err)
	}

	results := make([]SearchResult, 0, len(br.Web.Results))
	for i, r := range br.Web.Results {
		if i >= maxResults {
			break
		}
		results = append(results, SearchResult{
			Title:    r.Title,
			URL:      r.URL,
			Snippet:  r.Description,
			Position: i + 1,
		})
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// GoogleEngine — Google Custom Search JSON API (requires API key + CX)
// ---------------------------------------------------------------------------

// GoogleEngine uses the Google Custom Search JSON API.
type GoogleEngine struct {
	APIKey string
	CX     string // Custom Search Engine ID
}

// googleResponse is the top-level Google Custom Search response.
type googleResponse struct {
	Items []googleItem `json:"items"`
}

// googleItem is a single search result in the Google response.
type googleItem struct {
	Title   string `json:"title"`
	Link    string `json:"link"`
	Snippet string `json:"snippet"`
}

// Search queries Google Custom Search and returns up to maxResults results.
func (e *GoogleEngine) Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	if maxResults <= 0 {
		maxResults = defaultMaxResults
	}
	if maxResults > 10 {
		maxResults = 10 // Google CSE caps at 10 per request
	}
	if e.APIKey == "" {
		return nil, fmt.Errorf("google search: API key is required")
	}
	if e.CX == "" {
		return nil, fmt.Errorf("google search: CX (search engine ID) is required")
	}

	endpoint := fmt.Sprintf(
		"https://www.googleapis.com/customsearch/v1?q=%s&num=%d&key=%s&cx=%s",
		url.QueryEscape(query), maxResults, url.QueryEscape(e.APIKey), url.QueryEscape(e.CX),
	)

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("google search: creating request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google search HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("google search: reading response: %w", err)
	}

	var gr googleResponse
	if err := json.Unmarshal(body, &gr); err != nil {
		return nil, fmt.Errorf("google search: decoding JSON: %w", err)
	}

	results := make([]SearchResult, 0, len(gr.Items))
	for i, item := range gr.Items {
		if i >= maxResults {
			break
		}
		results = append(results, SearchResult{
			Title:    item.Title,
			URL:      item.Link,
			Snippet:  item.Snippet,
			Position: i + 1,
		})
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

// NewSearchEngine creates a SearchEngine by name.
// Supported names: "duckduckgo" (default, no API key), "brave", "google".
// For "google", apiKey must be in the form "KEY:CX" where CX is the Custom
// Search Engine ID.
func NewSearchEngine(name string, apiKey string) SearchEngine {
	switch strings.ToLower(name) {
	case "brave":
		return &BraveEngine{APIKey: apiKey}
	case "google":
		key, cx, _ := strings.Cut(apiKey, ":")
		return &GoogleEngine{APIKey: key, CX: cx}
	default:
		return &DuckDuckGoEngine{}
	}
}

// ---------------------------------------------------------------------------
// WebSearchSkill — wraps a SearchEngine as a skill.Skill
// ---------------------------------------------------------------------------

// WebSearchSkill wraps a SearchEngine into a callable skill.
type WebSearchSkill struct {
	Engine SearchEngine
}

// ---------------------------------------------------------------------------
// Legacy package-level helpers (backward compatible)
// ---------------------------------------------------------------------------

// Search performs a web search using DuckDuckGo's HTML interface and returns
// up to maxResults results. If maxResults <= 0, defaults to 5.
// The context is used for request cancellation and timeouts.
func Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	engine := &DuckDuckGoEngine{}
	return engine.Search(ctx, query, maxResults)
}

// WebSearch performs a search using DuckDuckGo HTML and returns top 5 results.
// Deprecated: Use Search with context instead.
func WebSearch(query string) ([]SearchResult, error) {
	return Search(context.Background(), query, defaultMaxResults)
}

// parseSearchResults extracts results from DuckDuckGo HTML response.
func parseSearchResults(htmlContent string, maxResults int) []SearchResult {
	if maxResults <= 0 {
		maxResults = defaultMaxResults
	}

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
					current.Title = strings.TrimSpace(current.Title)
					current.Snippet = strings.TrimSpace(current.Snippet)
					if current.Title != "" {
						current.Position = len(results) + 1
						results = append(results, current)
					}
					if len(results) >= maxResults {
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
