package insights

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"seconds", 45 * time.Second, "45s"},
		{"zero", 0, "0s"},
		{"just under minute", 59 * time.Second, "59s"},
		{"minutes", 5 * time.Minute, "5m"},
		{"just under hour", 59 * time.Minute, "59m"},
		{"hours and minutes", 2*time.Hour + 30*time.Minute, "2h30m"},
		{"exactly one hour", time.Hour, "1h0m"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatDuration(tc.d)
			if got != tc.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tc.d, got, tc.want)
			}
		})
	}
}

func TestGenerateInsightsPromptBasicFields(t *testing.T) {
	data := SessionData{
		Duration:   10 * time.Minute,
		Requests:   50,
		Tokens:     12345,
		Cost:       1.2345,
		ErrorCount: 3,
	}
	got := GenerateInsightsPrompt(data)

	expectedSubstrings := []string{
		"Analise os dados da sessao",
		"## Dados da sessao",
		"Duracao: 10m",
		"Requisicoes: 50",
		"Tokens: 12345",
		"Custo: $1.2345",
		"Erros: 3",
		"## Gere insights sobre:",
		"1. Padroes de uso",
		"2. Eficiencia",
		"3. Pontos de friccao",
		"4. Sugestoes de otimizacao",
		"5. Resumo executivo",
	}
	for _, s := range expectedSubstrings {
		if !strings.Contains(got, s) {
			t.Errorf("expected output to contain %q, got:\n%s", s, got)
		}
	}
}

func TestGenerateInsightsPromptOmitsEmptyMaps(t *testing.T) {
	data := SessionData{
		Duration: 5 * time.Second,
		Requests: 1,
	}
	got := GenerateInsightsPrompt(data)

	if strings.Contains(got, "Tools mais usadas") {
		t.Errorf("should not include tools section when ToolCalls empty, got:\n%s", got)
	}
	if strings.Contains(got, "Arquivos mais editados") {
		t.Errorf("should not include files section when TopFiles empty, got:\n%s", got)
	}
	if !strings.Contains(got, "Duracao: 5s") {
		t.Errorf("expected duration 5s in output, got:\n%s", got)
	}
}

func TestGenerateInsightsPromptToolCallsSortedDesc(t *testing.T) {
	data := SessionData{
		Duration: time.Minute,
		Requests: 10,
		ToolCalls: map[string]int{
			"read_file": 5,
			"edit_file": 20,
			"bash":      12,
		},
	}
	got := GenerateInsightsPrompt(data)

	if !strings.Contains(got, "### Tools mais usadas") {
		t.Fatalf("expected tools section, got:\n%s", got)
	}

	// Verify descending order by checking positions.
	editIdx := strings.Index(got, "edit_file: 20")
	bashIdx := strings.Index(got, "bash: 12")
	readIdx := strings.Index(got, "read_file: 5")

	if editIdx < 0 || bashIdx < 0 || readIdx < 0 {
		t.Fatalf("missing expected tool entries in output:\n%s", got)
	}
	if !(editIdx < bashIdx && bashIdx < readIdx) {
		t.Errorf("tools not sorted by count desc. positions: edit=%d bash=%d read=%d\noutput:\n%s",
			editIdx, bashIdx, readIdx, got)
	}
}

func TestGenerateInsightsPromptTopFilesLimitedTo10(t *testing.T) {
	files := make(map[string]int)
	for i := 0; i < 15; i++ {
		// give each a unique count so sort is deterministic
		files["file"+strconv.Itoa(i)+".go"] = 100 - i
	}
	data := SessionData{
		Duration: time.Minute,
		Requests: 1,
		TopFiles: files,
	}
	got := GenerateInsightsPrompt(data)

	if !strings.Contains(got, "### Arquivos mais editados") {
		t.Fatalf("expected files section, got:\n%s", got)
	}

	// Top 10 should be present (files 0..9 with counts 100..91)
	for i := 0; i < 10; i++ {
		marker := fmt.Sprintf("file%d.go: %d edicoes", i, 100-i)
		if !strings.Contains(got, marker) {
			t.Errorf("expected top file entry %q in output", marker)
		}
	}
	// Files 10..14 should be excluded.
	for i := 10; i < 15; i++ {
		marker := fmt.Sprintf("file%d.go", i)
		if strings.Contains(got, marker) {
			t.Errorf("file %q should have been excluded (beyond top 10)", marker)
		}
	}
}

func TestFormatQuickInsightsEfficiencyAndRatio(t *testing.T) {
	data := SessionData{
		Duration: 10 * time.Minute,
		Requests: 10,
		Tokens:   1000,
		Cost:     0.5,
		ToolCalls: map[string]int{
			"read_file":      4,
			"list_directory": 1,
			"edit_file":      2,
			"create_file":    1,
		},
	}
	got := FormatQuickInsights(data)

	if !strings.Contains(got, "## Insights da sessao") {
		t.Errorf("missing header, got:\n%s", got)
	}
	// avg 100 tokens/req, $0.0500/req
	if !strings.Contains(got, "100 tokens/req") {
		t.Errorf("expected '100 tokens/req' in output, got:\n%s", got)
	}
	if !strings.Contains(got, "$0.0500/req") {
		t.Errorf("expected '$0.0500/req' in output, got:\n%s", got)
	}
	// read ratio: 4 + 1 = 5, write: 2 + 1 = 3
	if !strings.Contains(got, "5 leituras / 3 escritas") {
		t.Errorf("expected read/write ratio '5 leituras / 3 escritas', got:\n%s", got)
	}
}

func TestFormatQuickInsightsHighErrorRate(t *testing.T) {
	high := SessionData{
		Duration:   time.Minute,
		Requests:   10,
		ErrorCount: 3, // 30%
	}
	got := FormatQuickInsights(high)
	if !strings.Contains(got, "taxa de erro alta") {
		t.Errorf("expected high error rate warning, got:\n%s", got)
	}

	low := SessionData{
		Duration:   time.Minute,
		Requests:   100,
		ErrorCount: 5, // 5%
	}
	got = FormatQuickInsights(low)
	if strings.Contains(got, "taxa de erro alta") {
		t.Errorf("should NOT warn for low error rate, got:\n%s", got)
	}
}

func TestFormatQuickInsightsLongSessionTip(t *testing.T) {
	long := SessionData{
		Duration: 45 * time.Minute,
		Requests: 5,
	}
	got := FormatQuickInsights(long)
	if !strings.Contains(got, "/compact") {
		t.Errorf("expected /compact tip for long session, got:\n%s", got)
	}

	short := SessionData{
		Duration: 10 * time.Minute,
		Requests: 5,
	}
	got = FormatQuickInsights(short)
	if strings.Contains(got, "/compact") {
		t.Errorf("should NOT show /compact tip for short session, got:\n%s", got)
	}
}

func TestFormatQuickInsightsZeroRequests(t *testing.T) {
	// With zero requests, efficiency and error-rate branches are skipped,
	// avoiding division by zero.
	data := SessionData{
		Duration: 5 * time.Second,
		Requests: 0,
	}
	got := FormatQuickInsights(data)
	if !strings.Contains(got, "## Insights da sessao") {
		t.Errorf("expected header even with zero requests, got:\n%s", got)
	}
	if strings.Contains(got, "tokens/req") {
		t.Errorf("should not emit efficiency line with zero requests, got:\n%s", got)
	}
	if strings.Contains(got, "taxa de erro alta") {
		t.Errorf("should not emit error-rate line with zero requests, got:\n%s", got)
	}
}
