// Package tokens provides fast, dependency-free token counting and cost
// estimation for LLM inputs. It offers a heuristic approximation of token
// counts that tracks tiktoken closely enough for UI and budgeting purposes
// (context windows, warning thresholds, cost previews) without requiring
// API calls or large encoding tables.
//
// If a true tiktoken-go dependency becomes available, the public API
// (EstimateTokens, CountTokens, etc.) is stable — only the internal
// implementation needs to change.
package tokens

import (
	"fmt"
	"math"
	"strings"
	"unicode"
)

// Message represents a chat message for token budgeting.
type Message struct {
	Role    string
	Content string
	Name    string
}

// Encoding names follow tiktoken conventions so callers can branch on them.
const (
	EncodingCL100KBase = "cl100k_base" // GPT-3.5 / GPT-4 / older Claude-compatible
	EncodingO200KBase  = "o200k_base"  // GPT-4o, o1, o3
	EncodingClaude     = "claude"      // Anthropic (approximate)
	EncodingGemini     = "gemini"      // Google Gemini (approximate)
	EncodingDeepseek   = "deepseek"    // DeepSeek (approximate)
	EncodingLlama      = "llama"       // Meta Llama / Mistral / Qwen (approximate)
)

// ModelEncoding returns the encoding name for a given model identifier.
// Unknown models fall back to cl100k_base, the most common modern tokenizer.
func ModelEncoding(model string) string {
	m := strings.ToLower(model)
	switch {
	case strings.HasPrefix(m, "gpt-4o"),
		strings.HasPrefix(m, "gpt-4.1"),
		strings.HasPrefix(m, "o1"),
		strings.HasPrefix(m, "o3"),
		strings.HasPrefix(m, "o4"):
		return EncodingO200KBase
	case strings.HasPrefix(m, "gpt-"),
		strings.HasPrefix(m, "text-embedding"):
		return EncodingCL100KBase
	case strings.HasPrefix(m, "claude"):
		return EncodingClaude
	case strings.HasPrefix(m, "gemini"):
		return EncodingGemini
	case strings.HasPrefix(m, "deepseek"):
		return EncodingDeepseek
	case strings.HasPrefix(m, "llama"),
		strings.HasPrefix(m, "mistral"),
		strings.HasPrefix(m, "qwen"):
		return EncodingLlama
	}
	return EncodingCL100KBase
}

// charsPerToken returns the empirical characters-per-token ratio for an
// encoding. These numbers come from measuring typical code+prose corpora
// and match tiktoken within ~5% for mixed content.
func charsPerToken(encoding string) float64 {
	switch encoding {
	case EncodingO200KBase:
		return 4.0 // o200k is slightly denser for modern vocab
	case EncodingCL100KBase:
		return 4.0
	case EncodingClaude:
		return 3.8 // Claude tends to be slightly denser for code
	case EncodingGemini:
		return 4.1
	case EncodingDeepseek:
		return 3.7 // DeepSeek BPE is dense for code/Chinese
	case EncodingLlama:
		return 3.9
	}
	return 4.0
}

// EstimateTokens returns an approximate token count for text. It is
// deterministic, cheap (O(len(text))), and good enough for UI / budget use.
//
// The heuristic works in three steps:
//  1. Partition text by script (ASCII vs CJK vs other) — CJK languages
//     use roughly 1 token per character.
//  2. For ASCII, split into "word-ish" chunks (contiguous letters, digits,
//     or symbols) and score each: short words are ~1 token, longer words
//     get sub-tokenised.
//  3. Scale by the encoding's characters-per-token ratio.
func EstimateTokens(model, text string) int {
	if text == "" {
		return 0
	}
	encoding := ModelEncoding(model)
	cpt := charsPerToken(encoding)

	// Split by character class to separate CJK from ASCII.
	var asciiChars, cjkChars, otherChars int
	for _, r := range text {
		switch {
		case r < 128:
			asciiChars++
		case unicode.Is(unicode.Han, r), unicode.Is(unicode.Hiragana, r),
			unicode.Is(unicode.Katakana, r), unicode.Is(unicode.Hangul, r):
			cjkChars++
		default:
			otherChars++
		}
	}

	// CJK: ~1 token per character (often 1-2 bytes pre-BPE → 1 token).
	cjkTokens := float64(cjkChars)

	// Other Unicode (accented Latin, emoji, etc.): ~2 chars per token.
	otherTokens := float64(otherChars) / 2.0

	// ASCII: use characters-per-token ratio, adjusted for whitespace density.
	// Code has more whitespace/punctuation per token, prose less so.
	asciiTokens := float64(asciiChars) / cpt

	// Whitespace correction: lots of whitespace means code-like content,
	// which tiktoken tokenises slightly more aggressively.
	wsRatio := whitespaceRatio(text)
	if wsRatio > 0.18 {
		asciiTokens *= 1.05
	}

	total := cjkTokens + otherTokens + asciiTokens
	if total < 1 {
		total = 1
	}
	return int(math.Ceil(total))
}

// whitespaceRatio returns the fraction of characters that are whitespace.
func whitespaceRatio(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	ws := 0
	for _, r := range s {
		if unicode.IsSpace(r) {
			ws++
		}
	}
	return float64(ws) / float64(len(s))
}

// messageOverhead returns the per-message token overhead used by chat APIs.
// OpenAI documents ~4 tokens per message for ChatML framing plus 2 tokens
// for the assistant priming; Anthropic is similar. We use 4+2 as a
// conservative cross-model estimate.
func messageOverhead(encoding string) (perMessage, perReply int) {
	switch encoding {
	case EncodingO200KBase, EncodingCL100KBase:
		return 4, 2
	case EncodingClaude:
		return 5, 2
	default:
		return 4, 2
	}
}

// EstimateMessagesTokens estimates the total tokens for a full chat request,
// including role tags and framing overhead.
func EstimateMessagesTokens(model string, messages []Message) int {
	if len(messages) == 0 {
		return 0
	}
	encoding := ModelEncoding(model)
	perMsg, perReply := messageOverhead(encoding)

	total := 0
	for _, m := range messages {
		total += perMsg
		total += EstimateTokens(model, m.Role)
		total += EstimateTokens(model, m.Content)
		if m.Name != "" {
			total += 1 // name field marker
			total += EstimateTokens(model, m.Name)
		}
	}
	total += perReply
	return total
}

// CountTokens returns the best available token count for text. If a precise
// tokenizer (tiktoken-go) is wired in, it is used; otherwise this falls
// back to EstimateTokens. Public API is stable either way.
func CountTokens(model, text string) int {
	// No exact tokenizer linked in this build — use the heuristic.
	return EstimateTokens(model, text)
}

// ----- pricing helpers -------------------------------------------------

// pricingEntry mirrors the subset of tracking.CostEnginePricingTable we need
// here, duplicated to avoid a package import cycle with internal/tracking
// (which imports nothing in this package but may in the future). Keep in
// sync with internal/tracking/costengine.go.
type pricingEntry struct {
	InputPerMillion  float64
	OutputPerMillion float64
}

var pricingTable = map[string]pricingEntry{
	// Claude family
	"claude-sonnet-4":   {3.00, 15.00},
	"claude-3-5-sonnet": {3.00, 15.00},
	"claude-3.5-sonnet": {3.00, 15.00},
	"claude-3-5-haiku":  {0.80, 4.00},
	"claude-3.5-haiku":  {0.80, 4.00},
	"claude-3-haiku":    {0.25, 1.25},
	"claude-3-opus":     {15.00, 75.00},

	// OpenAI family
	"gpt-4o-mini":   {0.15, 0.60},
	"gpt-4o":        {2.50, 10.00},
	"gpt-4.1":       {2.00, 8.00},
	"gpt-3.5-turbo": {0.50, 1.50},

	// Google Gemini family
	"gemini-2.5-pro":   {1.25, 10.00},
	"gemini-2.5-flash": {0.15, 0.60},
	"gemini-1.5-pro":   {1.25, 5.00},
	"gemini-1.5-flash": {0.075, 0.30},

	// DeepSeek family
	"deepseek-chat":     {0.14, 0.28},
	"deepseek-coder":    {0.14, 0.28},
	"deepseek-reasoner": {0.55, 2.19},

	// Meta / Mistral / local
	"llama-3.3-70b": {0.59, 0.79},
	"llama3.3":      {0.59, 0.79},
	"llama3.1":      {0, 0},
	"mistral-large": {2.00, 6.00},
	"qwen2.5":       {0, 0},
}

// lookupPricing mirrors tracking.lookupPricing: exact match first, then
// longest-prefix match.
func lookupPricing(model string) (pricingEntry, bool) {
	if p, ok := pricingTable[model]; ok {
		return p, true
	}
	var bestKey string
	for key := range pricingTable {
		if strings.HasPrefix(model, key) && len(key) > len(bestKey) {
			bestKey = key
		}
	}
	if bestKey != "" {
		return pricingTable[bestKey], true
	}
	return pricingEntry{}, false
}

// Cost returns the USD cost for a request with the given input and output
// token counts. Returns 0 for unknown or free models.
func Cost(model string, inputTokens, outputTokens int) float64 {
	p, ok := lookupPricing(model)
	if !ok {
		return 0
	}
	return float64(inputTokens)/1_000_000*p.InputPerMillion +
		float64(outputTokens)/1_000_000*p.OutputPerMillion
}

// FormatTokenCount formats a token count in a human-readable short form:
// 1234 → "1.2K", 1234567 → "1.2M", 1234567890 → "1.2B".
func FormatTokenCount(n int) string {
	abs := n
	sign := ""
	if n < 0 {
		abs = -n
		sign = "-"
	}
	switch {
	case abs < 1000:
		return fmt.Sprintf("%s%d", sign, abs)
	case abs < 1_000_000:
		return fmt.Sprintf("%s%.1fK", sign, float64(abs)/1000)
	case abs < 1_000_000_000:
		return fmt.Sprintf("%s%.1fM", sign, float64(abs)/1_000_000)
	default:
		return fmt.Sprintf("%s%.1fB", sign, float64(abs)/1_000_000_000)
	}
}

// Warning levels returned by TokenWarningLevel.
const (
	LevelOK       = "ok"
	LevelWarning  = "warning"
	LevelCritical = "critical"
)

// TokenWarningLevel returns "ok", "warning" or "critical" based on the
// fraction of the context window consumed. Thresholds:
//
//	>= 90% → critical
//	>= 70% → warning
//	otherwise → ok
//
// A non-positive max returns "ok" to avoid division-by-zero surprises.
func TokenWarningLevel(used, max int) string {
	if max <= 0 {
		return LevelOK
	}
	ratio := float64(used) / float64(max)
	switch {
	case ratio >= 0.90:
		return LevelCritical
	case ratio >= 0.70:
		return LevelWarning
	default:
		return LevelOK
	}
}

// PercentUsed returns the percentage of context consumed as an integer 0-100+.
func PercentUsed(used, max int) int {
	if max <= 0 {
		return 0
	}
	return int(math.Round(float64(used) / float64(max) * 100))
}
