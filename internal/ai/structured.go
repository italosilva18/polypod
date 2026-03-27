package ai

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var jsonBlockRe = regexp.MustCompile("(?s)```(?:json)?\\s*\\n(.*?)\\n```")

// ExtractJSON extracts JSON from a string, handling markdown code blocks.
func ExtractJSON(content string) string {
	content = strings.TrimSpace(content)

	// Try as-is first
	if json.Valid([]byte(content)) {
		return content
	}

	// Try extracting from markdown code blocks
	if matches := jsonBlockRe.FindStringSubmatch(content); len(matches) > 1 {
		candidate := strings.TrimSpace(matches[1])
		if json.Valid([]byte(candidate)) {
			return candidate
		}
	}

	// Try finding first { to last }
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end > start {
		candidate := content[start : end+1]
		if json.Valid([]byte(candidate)) {
			return candidate
		}
	}

	// Try finding first [ to last ]
	start = strings.Index(content, "[")
	end = strings.LastIndex(content, "]")
	if start >= 0 && end > start {
		candidate := content[start : end+1]
		if json.Valid([]byte(candidate)) {
			return candidate
		}
	}

	return content
}

// ValidateJSON checks if a string is valid JSON.
func ValidateJSON(content string) error {
	if !json.Valid([]byte(content)) {
		return fmt.Errorf("JSON invalido")
	}
	return nil
}

// ParseJSONResponse extracts and parses JSON from an AI response.
func ParseJSONResponse(content string) (map[string]any, error) {
	extracted := ExtractJSON(content)
	var result map[string]any
	if err := json.Unmarshal([]byte(extracted), &result); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w\nconteudo: %s", err, truncateContent(extracted))
	}
	return result, nil
}

// ParseJSONArrayResponse extracts and parses a JSON array from an AI response.
func ParseJSONArrayResponse(content string) ([]any, error) {
	extracted := ExtractJSON(content)
	var result []any
	if err := json.Unmarshal([]byte(extracted), &result); err != nil {
		return nil, fmt.Errorf("parsing JSON array: %w", err)
	}
	return result, nil
}

func truncateContent(s string) string {
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}
