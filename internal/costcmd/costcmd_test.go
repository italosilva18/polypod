package costcmd

import (
	"strings"
	"testing"
	"time"
)

func TestFormatDurationSeconds(t *testing.T) {
	got := formatDuration(45 * time.Second)
	want := "45s"
	if got != want {
		t.Errorf("formatDuration(45s) = %q, want %q", got, want)
	}
}

func TestFormatDurationMinutes(t *testing.T) {
	got := formatDuration(2*time.Minute + 30*time.Second)
	want := "2m30s"
	if got != want {
		t.Errorf("formatDuration(2m30s) = %q, want %q", got, want)
	}
}

func TestFormatDurationHours(t *testing.T) {
	got := formatDuration(3*time.Hour + 15*time.Minute)
	want := "3h15m"
	if got != want {
		t.Errorf("formatDuration(3h15m) = %q, want %q", got, want)
	}
}

func TestFormatDurationZero(t *testing.T) {
	got := formatDuration(0)
	want := "0s"
	if got != want {
		t.Errorf("formatDuration(0) = %q, want %q", got, want)
	}
}

func TestFormatDurationSubSecond(t *testing.T) {
	// Less than a minute, even sub-second, should render as Ns.
	got := formatDuration(500 * time.Millisecond)
	want := "0s"
	if got != want {
		t.Errorf("formatDuration(500ms) = %q, want %q", got, want)
	}
}

func TestMakeBarHalfFull(t *testing.T) {
	got := makeBar(50, 100, 10)
	if len(got) == 0 {
		t.Fatalf("makeBar returned empty string")
	}
	filled := strings.Count(got, "█")
	empty := strings.Count(got, "░")
	if filled != 5 {
		t.Errorf("filled count = %d, want 5 (got %q)", filled, got)
	}
	if empty != 5 {
		t.Errorf("empty count = %d, want 5 (got %q)", empty, got)
	}
}

func TestMakeBarFull(t *testing.T) {
	got := makeBar(100, 100, 10)
	filled := strings.Count(got, "█")
	empty := strings.Count(got, "░")
	if filled != 10 {
		t.Errorf("filled count = %d, want 10", filled)
	}
	if empty != 0 {
		t.Errorf("empty count = %d, want 0", empty)
	}
}

func TestMakeBarEmpty(t *testing.T) {
	got := makeBar(0, 100, 10)
	filled := strings.Count(got, "█")
	empty := strings.Count(got, "░")
	if filled != 0 {
		t.Errorf("filled count = %d, want 0", filled)
	}
	if empty != 10 {
		t.Errorf("empty count = %d, want 10", empty)
	}
}

func TestMakeBarOverflowClamped(t *testing.T) {
	// value > maxValue should clamp to full width.
	got := makeBar(500, 100, 8)
	filled := strings.Count(got, "█")
	empty := strings.Count(got, "░")
	if filled != 8 {
		t.Errorf("filled count = %d, want 8", filled)
	}
	if empty != 0 {
		t.Errorf("empty count = %d, want 0", empty)
	}
}

func TestMakeBarZeroMaxValue(t *testing.T) {
	got := makeBar(10, 0, 6)
	if strings.Count(got, "░") != 6 {
		t.Errorf("expected 6 empty blocks, got %q", got)
	}
	if strings.Count(got, "█") != 0 {
		t.Errorf("expected 0 filled blocks, got %q", got)
	}
}

func TestFormatCostBasic(t *testing.T) {
	s := SessionStats{
		StartedAt:        time.Now().Add(-30 * time.Second),
		PromptTokens:     100,
		CompletionTokens: 200,
		TotalTokens:      300,
		TotalCost:        0.1234,
		Requests:         2,
		Model:            "claude-opus-4",
	}
	out := FormatCost(s)

	checks := []string{
		"Custo da sessao",
		"claude-opus-4",
		"100 prompt + 200 completion = **300 total**",
		"$0.1234 USD",
		"Requisicoes**: 2",
		"Media",
		"$0.0617/req",
		"150 tokens/req",
	}
	for _, c := range checks {
		if !strings.Contains(out, c) {
			t.Errorf("FormatCost output missing %q\nGot:\n%s", c, out)
		}
	}
}

func TestFormatCostNoRequestsNoAverage(t *testing.T) {
	s := SessionStats{
		StartedAt: time.Now(),
		Model:     "test",
	}
	out := FormatCost(s)
	if strings.Contains(out, "Media") {
		t.Errorf("FormatCost with 0 requests should not contain 'Media', got:\n%s", out)
	}
}

func TestFormatCostOptionalFields(t *testing.T) {
	s := SessionStats{
		StartedAt:    time.Now(),
		Requests:     1,
		TotalTokens:  10,
		TotalCost:    0.01,
		Model:        "m",
		ToolCalls:    5,
		LinesAdded:   42,
		LinesRemoved: 7,
	}
	out := FormatCost(s)
	if !strings.Contains(out, "Tool calls**: 5") {
		t.Errorf("expected tool calls line, got:\n%s", out)
	}
	if !strings.Contains(out, "Linhas**: +42 -7") {
		t.Errorf("expected lines line, got:\n%s", out)
	}

	// With no tool calls or lines, those fields should be absent.
	s2 := SessionStats{StartedAt: time.Now(), Model: "m"}
	out2 := FormatCost(s2)
	if strings.Contains(out2, "Tool calls") {
		t.Errorf("did not expect tool calls line, got:\n%s", out2)
	}
	if strings.Contains(out2, "Linhas") {
		t.Errorf("did not expect linhas line, got:\n%s", out2)
	}
}

func TestFormatStatsEmpty(t *testing.T) {
	out := FormatStats(map[string]int{}, map[string]float64{}, 0)
	if !strings.Contains(out, "Estatisticas de uso") {
		t.Errorf("missing header, got:\n%s", out)
	}
	if !strings.Contains(out, "Total de sessoes**: 0") {
		t.Errorf("missing sessions count, got:\n%s", out)
	}
	// With empty daily data, there should be no table.
	if strings.Contains(out, "Ultimos 7 dias") {
		t.Errorf("did not expect 7-day table for empty data, got:\n%s", out)
	}
}

func TestFormatStatsWithData(t *testing.T) {
	today := time.Now().Format("2006-01-02")
	daily := map[string]int{today: 12345}
	cost := map[string]float64{today: 0.5}
	out := FormatStats(daily, cost, 3)

	if !strings.Contains(out, "Total de sessoes**: 3") {
		t.Errorf("missing session count, got:\n%s", out)
	}
	if !strings.Contains(out, "Ultimos 7 dias") {
		t.Errorf("missing 7-day header, got:\n%s", out)
	}
	if !strings.Contains(out, today) {
		t.Errorf("missing today's date %q, got:\n%s", today, out)
	}
	if !strings.Contains(out, "12345") {
		t.Errorf("missing token count, got:\n%s", out)
	}
	if !strings.Contains(out, "$0.5000") {
		t.Errorf("missing cost value, got:\n%s", out)
	}
}

func TestFormatContextLow(t *testing.T) {
	breakdown := map[string]int{
		"system_prompt": 1000,
		"tools":         500,
		"history":       1500,
		"free":          97000,
	}
	out := FormatContext(breakdown, 100000)

	if !strings.Contains(out, "Context Window") {
		t.Errorf("missing header, got:\n%s", out)
	}
	// Total used = 100000, but history+tools+system+free = 100000.
	if !strings.Contains(out, "100000 / 100000") {
		t.Errorf("missing usage line, got:\n%s", out)
	}
	if !strings.Contains(out, "System prompt") {
		t.Errorf("missing 'System prompt' label, got:\n%s", out)
	}
	if !strings.Contains(out, "Historico de mensagens") {
		t.Errorf("missing 'Historico de mensagens' label, got:\n%s", out)
	}
	if !strings.Contains(out, "Livre") {
		t.Errorf("missing 'Livre' label, got:\n%s", out)
	}
}

func TestFormatContextHighTriggersSuggestions(t *testing.T) {
	// Force >80% usage so suggestions block is emitted.
	breakdown := map[string]int{
		"history":   75000, // > maxTokens/2 => compact suggestion
		"mcp_tools": 6000,  // > 5000 => disconnect suggestion
		"memory":    4000,  // > 3000 => clean memories suggestion
	}
	// Total = 85000, which is >80% of 100000.
	out := FormatContext(breakdown, 100000)

	if !strings.Contains(out, "Sugestoes") {
		t.Errorf("expected suggestions section, got:\n%s", out)
	}
	if !strings.Contains(out, "/compact") {
		t.Errorf("expected /compact suggestion, got:\n%s", out)
	}
	if !strings.Contains(out, "MCP servers") {
		t.Errorf("expected MCP disconnect suggestion, got:\n%s", out)
	}
	if !strings.Contains(out, "memorias antigas") {
		t.Errorf("expected delete_memory suggestion, got:\n%s", out)
	}
}

func TestFormatContextNoSuggestionsWhenLowUsage(t *testing.T) {
	breakdown := map[string]int{
		"history": 1000,
	}
	out := FormatContext(breakdown, 100000)
	if strings.Contains(out, "Sugestoes") {
		t.Errorf("did not expect suggestions section for low usage, got:\n%s", out)
	}
}
