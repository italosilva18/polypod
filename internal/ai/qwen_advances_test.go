package ai

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/costa/polypod/internal/config"
	providerapi "github.com/costa/polypod/internal/provider"
)

type mockVisionCaptureProvider struct {
	mu           sync.Mutex
	seenModels   []string
	visionCap    bool
}

func (m *mockVisionCaptureProvider) Name() string { return "qwen" }
func (m *mockVisionCaptureProvider) Chat(ctx context.Context, req providerapi.ChatRequest) (*providerapi.ChatResponse, error) {
	m.mu.Lock()
	m.seenModels = append(m.seenModels, req.Model)
	m.mu.Unlock()
	return &providerapi.ChatResponse{
		Content:          "ok:" + req.Model,
		Model:            req.Model,
		PromptTokens:     120,
		CompletionTokens: 40,
		TotalTokens:      160,
	}, nil
}
func (m *mockVisionCaptureProvider) ChatStream(ctx context.Context, req providerapi.ChatRequest, onDelta providerapi.StreamCallback) (*providerapi.ChatResponse, error) {
	return m.Chat(ctx, req)
}
func (m *mockVisionCaptureProvider) Models() []string     { return []string{"qwen3-coder-plus", "qwen3-vl-plus"} }
func (m *mockVisionCaptureProvider) SupportsTools() bool            { return true }
func (m *mockVisionCaptureProvider) SupportsVision() bool           { return m.visionCap }
func (m *mockVisionCaptureProvider) SupportsStructuredOutput() bool { return false }

func TestAutoVisionModelSwitchAndRestoreOnTextTurns(t *testing.T) {
	prov := &mockVisionCaptureProvider{visionCap: true}
	c := NewClient(config.AIConfig{
		Provider: "qwen",
		Model:    "qwen3-coder-plus", // Text-only 1M coding model!
		MaxToks:  4096,
		Tools:    true,
	}, nil)
	c.responseProvider = prov

	ctx := context.Background()

	// Turn 1: Pure text coding request -> must stay on qwen3-coder-plus!
	resp1, err := c.CompleteWithTools(ctx, []ChatMessage{
		{Role: "user", Content: "Refatore a função parseArgs em Go"},
	}, slog.Default())
	if err != nil {
		t.Fatalf("Turn 1 failed: %v", err)
	}
	if resp1.Content != "ok:qwen3-coder-plus" {
		t.Fatalf("expected Turn 1 to use qwen3-coder-plus, got %q", resp1.Content)
	}

	// Turn 2: Multimodal turn with screenshot -> must auto-switch to a Qwen VL model!
	resp2, err := c.CompleteWithTools(ctx, []ChatMessage{
		{
			Role:    "user",
			Content: "Analise o erro visual neste screenshot",
			Parts: []providerapi.ContentPart{
				{Type: "text", Text: "Analise o erro visual neste screenshot"},
				{Type: "image_url", ImageURL: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="},
			},
		},
	}, slog.Default())
	if err != nil {
		t.Fatalf("Turn 2 failed: %v", err)
	}
	if resp2.Content == "ok:qwen3-coder-plus" || !strings.HasPrefix(resp2.Content, "ok:") {
		t.Fatalf("expected Turn 2 with image to auto-switch away from text-only qwen3-coder-plus, got %q", resp2.Content)
	}

	// Turn 3: Next text-only turn -> must seamlessly return to qwen3-coder-plus!
	resp3, err := c.CompleteWithTools(ctx, []ChatMessage{
		{Role: "user", Content: "Agora rode os testes unitários"},
	}, slog.Default())
	if err != nil {
		t.Fatalf("Turn 3 failed: %v", err)
	}
	if resp3.Content != "ok:qwen3-coder-plus" {
		t.Fatalf("expected Turn 3 to restore qwen3-coder-plus, got %q", resp3.Content)
	}

	// Turn 4: Explicit `/model --vision` override -> must honor explicit vision model on image turns!
	if err := c.SetVisionModel("qwen", "qwen3-vl-custom", "", ""); err != nil {
		t.Fatalf("SetVisionModel: %v", err)
	}
	resp4, err := c.Complete(ctx, []ChatMessage{
		{
			Role: "user",
			Parts: []providerapi.ContentPart{
				{Type: "image_url", ImageURL: "data:image/png;base64,AAAA"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Turn 4 failed: %v", err)
	}
	if resp4.Content != "ok:qwen3-vl-custom" {
		t.Fatalf("expected explicit vision model qwen3-vl-custom, got %q", resp4.Content)
	}
}

func TestHistoryWindowScalesFor1MModelsAndWaterfallFormatting(t *testing.T) {
	if got := historyWindowBudget("deepseek-chat"); got != 24_000 {
		t.Fatalf("expected 128K model historyWindowBudget=24000, got %d", got)
	}
	if got := historyWindowBudget("qwen3-coder-plus"); got != 120_000 {
		t.Fatalf("expected 1M model historyWindowBudget=120000, got %d", got)
	}

	rec := NewWaterfallRecorder(5)
	rec.BeginTurn("Inspecione os pacotes ai e watcher", "qwen3-coder-plus")
	t0 := time.Now()
	rec.RecordModelSpan("qwen3-coder-plus", t0, 45*time.Millisecond, 1200, 85, false)

	// Two overlapping tool calls -> must be marked Parallel!
	rec.ObserveToolEvent(ToolEvent{Type: ToolEventStart, ToolName: "read_file", ToolID: "call_1"})
	rec.ObserveToolEvent(ToolEvent{Type: ToolEventStart, ToolName: "grep_search", ToolID: "call_2"})
	rec.ObserveToolEvent(ToolEvent{Type: ToolEventComplete, ToolName: "read_file", ToolID: "call_1", Elapsed: 18 * time.Millisecond, ResultBytes: 2048})
	rec.ObserveToolEvent(ToolEvent{Type: ToolEventComplete, ToolName: "grep_search", ToolID: "call_2", Elapsed: 22 * time.Millisecond, ResultBytes: 512})
	rec.EndTurn()

	wf, ok := rec.Latest()
	if !ok {
		t.Fatal("expected Latest() waterfall to exist")
	}
	rendered := FormatWaterfall(wf)
	if !strings.Contains(rendered, "Trajectory Waterfall") ||
		!strings.Contains(rendered, "qwen3-coder-plus") ||
		!strings.Contains(rendered, "[paralelo]") {
		t.Fatalf("unexpected waterfall output:\n%s", rendered)
	}
}
