package tokens

import (
	"strings"
	"testing"
)

func TestModelEncoding(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		{"gpt-4o", EncodingO200KBase},
		{"gpt-4o-mini", EncodingO200KBase},
		{"gpt-4.1", EncodingO200KBase},
		{"o1-preview", EncodingO200KBase},
		{"o3-mini", EncodingO200KBase},
		{"gpt-4", EncodingCL100KBase},
		{"gpt-3.5-turbo", EncodingCL100KBase},
		{"claude-3-5-sonnet", EncodingClaude},
		{"gemini-1.5-pro", EncodingGemini},
		{"deepseek-chat", EncodingDeepseek},
		{"llama-3.3-70b", EncodingLlama},
		{"mistral-large", EncodingLlama},
		{"qwen2.5", EncodingLlama},
		{"unknown-model-xyz", EncodingCL100KBase}, // fallback
	}
	for _, tt := range tests {
		if got := ModelEncoding(tt.model); got != tt.want {
			t.Errorf("ModelEncoding(%q) = %q; want %q", tt.model, got, tt.want)
		}
	}
}

func TestEstimateTokens_Empty(t *testing.T) {
	if got := EstimateTokens("gpt-4", ""); got != 0 {
		t.Errorf("empty = %d; want 0", got)
	}
}

func TestEstimateTokens_NonEmpty(t *testing.T) {
	if got := EstimateTokens("gpt-4", "hello world"); got <= 0 {
		t.Errorf("non-empty = %d; want > 0", got)
	}
}

func TestEstimateTokens_Monotonic(t *testing.T) {
	short := EstimateTokens("gpt-4", "hi")
	long := EstimateTokens("gpt-4", "this is a much longer piece of text with many more words and characters included")
	if long <= short {
		t.Errorf("long (%d) should be > short (%d)", long, short)
	}
}

func TestEstimateTokens_DifferentModels(t *testing.T) {
	text := "The quick brown fox jumps over the lazy dog"
	models := []string{"gpt-4", "gpt-4o", "claude-3-5-sonnet", "gemini-1.5-pro", "deepseek-chat", "llama-3.3-70b"}
	for _, m := range models {
		if got := EstimateTokens(m, text); got <= 0 {
			t.Errorf("EstimateTokens(%q, ...) = %d; want > 0", m, got)
		}
	}
}

func TestEstimateTokens_CJK(t *testing.T) {
	// CJK text should yield roughly one token per character.
	text := "你好世界你好世界"
	got := EstimateTokens("gpt-4", text)
	if got <= 0 {
		t.Errorf("CJK tokens = %d; want > 0", got)
	}
}

func TestEstimateMessagesTokens_Empty(t *testing.T) {
	if got := EstimateMessagesTokens("gpt-4", nil); got != 0 {
		t.Errorf("nil messages = %d; want 0", got)
	}
	if got := EstimateMessagesTokens("gpt-4", []Message{}); got != 0 {
		t.Errorf("empty messages = %d; want 0", got)
	}
}

func TestEstimateMessagesTokens_Overhead(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hello"},
	}
	msgTokens := EstimateMessagesTokens("gpt-4", msgs)
	contentOnly := EstimateTokens("gpt-4", "hello")
	// message tokens should include role + framing overhead, so strictly greater.
	if msgTokens <= contentOnly {
		t.Errorf("msgTokens (%d) should be > contentOnly (%d) due to overhead", msgTokens, contentOnly)
	}
}

func TestEstimateMessagesTokens_WithName(t *testing.T) {
	withoutName := EstimateMessagesTokens("gpt-4", []Message{
		{Role: "user", Content: "hello"},
	})
	withName := EstimateMessagesTokens("gpt-4", []Message{
		{Role: "user", Content: "hello", Name: "alice"},
	})
	if withName <= withoutName {
		t.Errorf("withName (%d) should be > withoutName (%d)", withName, withoutName)
	}
}

func TestEstimateMessagesTokens_MultipleMessages(t *testing.T) {
	one := EstimateMessagesTokens("gpt-4", []Message{
		{Role: "user", Content: "hello"},
	})
	two := EstimateMessagesTokens("gpt-4", []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	})
	if two <= one {
		t.Errorf("two messages (%d) should be > one message (%d)", two, one)
	}
}

func TestCountTokens_MatchesEstimate(t *testing.T) {
	text := "Count these tokens please."
	count := CountTokens("gpt-4", text)
	est := EstimateTokens("gpt-4", text)
	if count != est {
		t.Errorf("CountTokens (%d) should match EstimateTokens (%d) in heuristic build", count, est)
	}
}

func TestCountTokens_Empty(t *testing.T) {
	if got := CountTokens("gpt-4", ""); got != 0 {
		t.Errorf("CountTokens empty = %d; want 0", got)
	}
}

func TestCost_KnownModel(t *testing.T) {
	cost := Cost("gpt-4o", 1000, 500)
	if cost <= 0 {
		t.Errorf("cost = %v; want > 0", cost)
	}
}

func TestCost_KnownModels(t *testing.T) {
	models := []string{
		"claude-3-5-sonnet",
		"claude-3-opus",
		"gpt-4o",
		"gpt-4o-mini",
		"gemini-1.5-pro",
		"deepseek-chat",
	}
	for _, m := range models {
		if cost := Cost(m, 1000, 500); cost <= 0 {
			t.Errorf("Cost(%q) = %v; want > 0", m, cost)
		}
	}
}

func TestCost_UnknownModel(t *testing.T) {
	cost := Cost("nonexistent-xyz", 1000, 500)
	if cost != 0 {
		t.Errorf("cost for unknown = %v; want 0", cost)
	}
}

func TestCost_PrefixMatch(t *testing.T) {
	// "gpt-4o-2024-08-06" should match the "gpt-4o" prefix in the table.
	cost := Cost("gpt-4o-2024-08-06", 1000, 500)
	if cost <= 0 {
		t.Errorf("prefix-matched cost = %v; want > 0", cost)
	}
}

func TestCost_OutputMoreExpensive(t *testing.T) {
	// For gpt-4o, output pricing is higher than input.
	inputCost := Cost("gpt-4o", 1000, 0)
	outputCost := Cost("gpt-4o", 0, 1000)
	if outputCost <= inputCost {
		t.Errorf("outputCost (%v) should be > inputCost (%v) for gpt-4o", outputCost, inputCost)
	}
}

func TestFormatTokenCount(t *testing.T) {
	tests := []struct {
		in       int
		contains string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "K"},
		{1500, "K"},
		{2500000, "M"},
		{1_500_000_000, "B"},
	}
	for _, tt := range tests {
		got := FormatTokenCount(tt.in)
		if got == "" {
			t.Errorf("FormatTokenCount(%d) empty", tt.in)
		}
		if !strings.Contains(got, tt.contains) {
			t.Errorf("FormatTokenCount(%d) = %q; want contains %q", tt.in, got, tt.contains)
		}
	}
}

func TestFormatTokenCount_SpecificValues(t *testing.T) {
	if got := FormatTokenCount(0); got != "0" {
		t.Errorf("FormatTokenCount(0) = %q; want %q", got, "0")
	}
	if got := FormatTokenCount(999); got != "999" {
		t.Errorf("FormatTokenCount(999) = %q; want %q", got, "999")
	}
	if got := FormatTokenCount(1500); !strings.Contains(got, "1.5") || !strings.Contains(got, "K") {
		t.Errorf("FormatTokenCount(1500) = %q; want contains 1.5 and K", got)
	}
	if got := FormatTokenCount(2_500_000); !strings.Contains(got, "2.5") || !strings.Contains(got, "M") {
		t.Errorf("FormatTokenCount(2500000) = %q; want contains 2.5 and M", got)
	}
}

func TestFormatTokenCount_Negative(t *testing.T) {
	got := FormatTokenCount(-1500)
	if !strings.HasPrefix(got, "-") {
		t.Errorf("FormatTokenCount(-1500) = %q; want leading -", got)
	}
}

func TestTokenWarningLevel(t *testing.T) {
	tests := []struct {
		used, max int
		want      string
	}{
		{0, 100, LevelOK},
		{50, 100, LevelOK},
		{69, 100, LevelOK},
		{70, 100, LevelWarning},
		{75, 100, LevelWarning},
		{89, 100, LevelWarning},
		{90, 100, LevelCritical},
		{95, 100, LevelCritical},
		{100, 100, LevelCritical},
		{150, 100, LevelCritical},
	}
	for _, tt := range tests {
		got := TokenWarningLevel(tt.used, tt.max)
		if got != tt.want {
			t.Errorf("TokenWarningLevel(%d,%d) = %q; want %q", tt.used, tt.max, got, tt.want)
		}
	}
}

func TestTokenWarningLevel_ZeroMax(t *testing.T) {
	if got := TokenWarningLevel(100, 0); got != LevelOK {
		t.Errorf("TokenWarningLevel(100,0) = %q; want %q", got, LevelOK)
	}
	if got := TokenWarningLevel(100, -1); got != LevelOK {
		t.Errorf("TokenWarningLevel(100,-1) = %q; want %q", got, LevelOK)
	}
}

func TestPercentUsed(t *testing.T) {
	tests := []struct {
		used, max, want int
	}{
		{0, 100, 0},
		{50, 100, 50},
		{100, 100, 100},
		{25, 100, 25},
		{1, 4, 25},
		{150, 100, 150},
	}
	for _, tt := range tests {
		if got := PercentUsed(tt.used, tt.max); got != tt.want {
			t.Errorf("PercentUsed(%d,%d) = %d; want %d", tt.used, tt.max, got, tt.want)
		}
	}
}

func TestPercentUsed_ZeroMax(t *testing.T) {
	if got := PercentUsed(100, 0); got != 0 {
		t.Errorf("PercentUsed(100,0) = %d; want 0", got)
	}
	if got := PercentUsed(100, -1); got != 0 {
		t.Errorf("PercentUsed(100,-1) = %d; want 0", got)
	}
}
