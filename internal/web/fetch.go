package web

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	maxResponseBytes = 1024 * 1024 // 1MB
	httpTimeout      = 15 * time.Second
	userAgent        = "Polypod/0.2"
)

var httpClient = &http.Client{
	Timeout: httpTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	},
}

// FetchURL fetches a URL and extracts visible text content.
func FetchURL(url string) (string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/html") {
		return extractText(string(body)), nil
	}

	return string(body), nil
}

// extractText extracts visible text from HTML, skipping script/style/noscript tags.
func extractText(htmlContent string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(htmlContent))

	var sb strings.Builder
	skipDepth := 0

	for {
		tt := tokenizer.Next()
		switch tt {
		case html.ErrorToken:
			return strings.TrimSpace(sb.String())

		case html.StartTagToken:
			tn, _ := tokenizer.TagName()
			tag := string(tn)
			if tag == "script" || tag == "style" || tag == "noscript" {
				skipDepth++
			}

		case html.EndTagToken:
			tn, _ := tokenizer.TagName()
			tag := string(tn)
			if tag == "script" || tag == "style" || tag == "noscript" {
				if skipDepth > 0 {
					skipDepth--
				}
			}

		case html.TextToken:
			if skipDepth == 0 {
				text := strings.TrimSpace(tokenizer.Token().Data)
				if text != "" {
					sb.WriteString(text)
					sb.WriteString("\n")
				}
			}
		}
	}
}
