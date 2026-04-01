package arena

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/costa/polypod/internal/provider"
)

// mockProvider for testing
type mockProvider struct {
	name     string
	response string
	delay    time.Duration
}

func (m *mockProvider) Name() string                  { return m.name }
func (m *mockProvider) SupportsVision() bool           { return false }
func (m *mockProvider) SupportsTools() bool            { return false }
func (m *mockProvider) SupportsStructuredOutput() bool { return false }

func (m *mockProvider) Chat(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	time.Sleep(m.delay)
	return &provider.ChatResponse{
		Content:          m.response,
		PromptTokens:     10,
		CompletionTokens: 20,
	}, nil
}

func (m *mockProvider) ChatStream(_ context.Context, req provider.ChatRequest, cb provider.StreamCallback) (*provider.ChatResponse, error) {
	return m.Chat(context.Background(), req)
}

func TestArenaRun(t *testing.T) {
	contenders := []Contender{
		{Provider: &mockProvider{name: "a", response: "answer A", delay: 10 * time.Millisecond}, Model: "model-a", Name: "Provider A"},
		{Provider: &mockProvider{name: "b", response: "answer B", delay: 20 * time.Millisecond}, Model: "model-b", Name: "Provider B"},
	}

	a := New(contenders, slog.Default())
	result := a.Run(context.Background(), "test prompt", 100, 0.3)

	if len(result.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result.Results))
	}

	for _, r := range result.Results {
		if r.Error != nil {
			t.Errorf("%s error: %v", r.Name, r.Error)
		}
		if r.Content == "" {
			t.Errorf("%s empty content", r.Name)
		}
		if r.Duration == 0 {
			t.Errorf("%s zero duration", r.Name)
		}
	}
}

func TestArenaParallel(t *testing.T) {
	contenders := []Contender{
		{Provider: &mockProvider{name: "slow", response: "slow", delay: 50 * time.Millisecond}, Model: "m1", Name: "Slow"},
		{Provider: &mockProvider{name: "fast", response: "fast", delay: 50 * time.Millisecond}, Model: "m2", Name: "Fast"},
	}

	a := New(contenders, slog.Default())
	start := time.Now()
	a.Run(context.Background(), "test", 100, 0.3)
	elapsed := time.Since(start)

	// Should run in parallel (~50ms, not 100ms)
	if elapsed > 90*time.Millisecond {
		t.Errorf("expected parallel execution (~50ms), got %s", elapsed)
	}
}

func TestFormatResults(t *testing.T) {
	result := &ArenaResult{
		Prompt: "test prompt",
		Results: []ContenderResult{
			{Name: "A", Model: "m-a", Content: "answer", PromptTokens: 10, CompletionTokens: 20, Duration: 100 * time.Millisecond},
		},
	}

	output := FormatResults(result)
	if output == "" {
		t.Fatal("format should not be empty")
	}
}
