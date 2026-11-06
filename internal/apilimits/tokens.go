package apilimits

import (
	"encoding/json"
	"strings"
)

// TokenCounter is the interface for token estimation strategies.
type TokenCounter interface {
	// EstimateTokens returns a rough token count for a text string.
	EstimateTokens(text string) int
	// Name returns the estimator name for logging.
	Name() string
}

// --- SimpleTokenCounter ---

// SimpleTokenCounter uses a chars-per-token heuristic (default: 4 bytes per token).
type SimpleTokenCounter struct {
	BytesPerToken int
}

// NewSimpleTokenCounter creates a counter with the default 4 bytes/token ratio.
func NewSimpleTokenCounter() *SimpleTokenCounter {
	return &SimpleTokenCounter{BytesPerToken: 4}
}

// EstimateTokens returns len(text)/BytesPerToken, minimum 1 for non-empty text.
func (c *SimpleTokenCounter) EstimateTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	n := len(text) / c.BytesPerToken
	if n < 1 {
		n = 1
	}
	return n
}

// Name returns "simple".
func (c *SimpleTokenCounter) Name() string { return "simple" }

// --- Standalone estimation functions ---

// EstimateTokens returns a rough token count using the default 4 bytes/token heuristic.
func EstimateTokens(text string) int {
	return RoughTokenCountEstimation(text, 4)
}

// RoughTokenCountEstimation estimates tokens from text using the given bytes-per-token ratio.
func RoughTokenCountEstimation(content string, bytesPerToken int) int {
	if len(content) == 0 {
		return 0
	}
	if bytesPerToken <= 0 {
		bytesPerToken = 4
	}
	n := len(content) / bytesPerToken
	if n < 1 && len(content) > 0 {
		n = 1
	}
	return n
}

// BytesPerTokenForFileType returns a more accurate bytes-per-token ratio
// for known file types. JSON has many single-character tokens, so the
// ratio is closer to 2 rather than the default 4.
func BytesPerTokenForFileType(ext string) int {
	ext = strings.TrimPrefix(strings.ToLower(ext), ".")
	switch ext {
	case "json", "jsonl", "jsonc":
		return 2
	default:
		return 4
	}
}

// RoughTokenCountEstimationForFileType combines file-type-aware ratio selection
// with the rough estimation.
func RoughTokenCountEstimationForFileType(content, fileExtension string) int {
	return RoughTokenCountEstimation(content, BytesPerTokenForFileType(fileExtension))
}

// ContentBlock is a simplified representation of a message content block
// for token estimation purposes.
type ContentBlock struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	Name  string `json:"name,omitempty"`  // tool_use name
	Input any    `json:"input,omitempty"` // tool_use input
	Data  string `json:"data,omitempty"`  // redacted_thinking, image, document
}

// EstimateMessageTokens estimates the token count for a message whose
// content is either a plain string or a slice of ContentBlocks.
func EstimateMessageTokens(content any) int {
	switch v := content.(type) {
	case string:
		return EstimateTokens(v)
	case []ContentBlock:
		return estimateBlockSlice(v)
	case []any:
		// Try to marshal/unmarshal through JSON for dynamic content
		data, err := json.Marshal(v)
		if err != nil {
			return 0
		}
		var blocks []ContentBlock
		if err := json.Unmarshal(data, &blocks); err != nil {
			// Fall back to estimating the raw JSON
			return EstimateTokens(string(data))
		}
		return estimateBlockSlice(blocks)
	default:
		return 0
	}
}

func estimateBlockSlice(blocks []ContentBlock) int {
	total := 0
	for _, b := range blocks {
		total += estimateBlockTokens(b)
	}
	return total
}

func estimateBlockTokens(b ContentBlock) int {
	switch b.Type {
	case "text":
		return EstimateTokens(b.Text)
	case "image", "document":
		// Images/PDFs: use conservative fixed estimate (matches microCompact)
		return 2000
	case "tool_use":
		inputJSON, _ := json.Marshal(b.Input)
		return EstimateTokens(b.Name + string(inputJSON))
	case "tool_result":
		// tool_result content is nested; estimate its text
		return EstimateTokens(b.Text)
	case "thinking":
		return EstimateTokens(b.Text)
	case "redacted_thinking":
		return EstimateTokens(b.Data)
	default:
		// Generic: marshal and estimate
		data, _ := json.Marshal(b)
		return EstimateTokens(string(data))
	}
}

// EstimateToolResultTokens estimates tokens for a tool result (typically a string output).
func EstimateToolResultTokens(output string) int {
	return EstimateTokens(output)
}
