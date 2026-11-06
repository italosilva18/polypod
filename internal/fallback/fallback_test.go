package fallback

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/costa/polypod/internal/provider"
)

// mockProvider is a configurable stub implementing provider.Provider.
type mockProvider struct {
	name         string
	err          error
	streamErr    error
	response     *provider.ChatResponse
	callCount    int32
	lastModel    atomic.Value // string
	lastReq      atomic.Value // provider.ChatRequest
	streamChunks []string
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	atomic.AddInt32(&m.callCount, 1)
	m.lastModel.Store(req.Model)
	m.lastReq.Store(req)
	if m.err != nil {
		return nil, m.err
	}
	if m.response != nil {
		return m.response, nil
	}
	return &provider.ChatResponse{Content: "ok from " + m.name}, nil
}

func (m *mockProvider) ChatStream(ctx context.Context, req provider.ChatRequest, cb provider.StreamCallback) (*provider.ChatResponse, error) {
	atomic.AddInt32(&m.callCount, 1)
	m.lastModel.Store(req.Model)
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	for _, c := range m.streamChunks {
		if cb != nil {
			cb(provider.StreamDelta{Content: c})
		}
	}
	if cb != nil {
		cb(provider.StreamDelta{Done: true})
	}
	if m.response != nil {
		return m.response, nil
	}
	return &provider.ChatResponse{Content: "stream ok from " + m.name}, nil
}

func (m *mockProvider) SupportsVision() bool           { return false }
func (m *mockProvider) SupportsTools() bool            { return false }
func (m *mockProvider) SupportsStructuredOutput() bool { return false }

func (m *mockProvider) calls() int32 {
	return atomic.LoadInt32(&m.callCount)
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newReq(model string) provider.ChatRequest {
	return provider.ChatRequest{
		Model: model,
		Messages: []provider.Message{
			{Role: "user", Content: "hello"},
		},
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestChain_FirstProviderSucceeds(t *testing.T) {
	p1 := &mockProvider{name: "openai", response: &provider.ChatResponse{Content: "from p1"}}
	p2 := &mockProvider{name: "anthropic"}

	chain := NewChain([]provider.Provider{p1, p2}, silentLogger())

	resp, err := chain.Chat(context.Background(), newReq("gpt-4o"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "from p1" {
		t.Errorf("expected response from p1, got %q", resp.Content)
	}
	if p1.calls() != 1 {
		t.Errorf("expected p1 called once, got %d", p1.calls())
	}
	if p2.calls() != 0 {
		t.Errorf("expected p2 not to be called, got %d", p2.calls())
	}
}

func TestChain_FallbackOnRetriableError(t *testing.T) {
	p1 := &mockProvider{name: "openai", err: errors.New("429 Too Many Requests")}
	p2 := &mockProvider{name: "anthropic", response: &provider.ChatResponse{Content: "from p2"}}

	chain := NewChain([]provider.Provider{p1, p2}, silentLogger())

	resp, err := chain.Chat(context.Background(), newReq("gpt-4o"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "from p2" {
		t.Errorf("expected content from fallback p2, got %q", resp.Content)
	}
	if p1.calls() != 1 || p2.calls() != 1 {
		t.Errorf("expected both providers called once, got p1=%d p2=%d", p1.calls(), p2.calls())
	}
}

func TestChain_NonRetriableErrorStopsChain(t *testing.T) {
	p1 := &mockProvider{name: "openai", err: errors.New("401 unauthorized: invalid api key")}
	p2 := &mockProvider{name: "anthropic", response: &provider.ChatResponse{Content: "should not run"}}

	chain := NewChain([]provider.Provider{p1, p2}, silentLogger())

	resp, err := chain.Chat(context.Background(), newReq("gpt-4o"))
	if err == nil {
		t.Fatalf("expected error, got nil (resp=%+v)", resp)
	}
	if p2.calls() != 0 {
		t.Errorf("p2 should not be called on non-retriable error, got %d calls", p2.calls())
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected original error to propagate, got %v", err)
	}
}

func TestChain_AllProvidersFail(t *testing.T) {
	p1 := &mockProvider{name: "openai", err: errors.New("503 service unavailable")}
	p2 := &mockProvider{name: "anthropic", err: errors.New("502 bad gateway")}
	p3 := &mockProvider{name: "google", err: errors.New("timeout exceeded")}

	chain := NewChain([]provider.Provider{p1, p2, p3}, silentLogger())

	resp, err := chain.Chat(context.Background(), newReq("gpt-4o"))
	if err == nil {
		t.Fatalf("expected error when all providers fail, got nil resp=%+v", resp)
	}
	if !strings.Contains(err.Error(), "todos os providers falharam") {
		t.Errorf("expected combined error message, got %v", err)
	}
	// Last error should be wrapped
	if !errors.Is(err, p3.err) {
		t.Errorf("expected wrapped last error, got %v", err)
	}
	if p1.calls() != 1 || p2.calls() != 1 || p3.calls() != 1 {
		t.Errorf("expected each provider called once, got p1=%d p2=%d p3=%d",
			p1.calls(), p2.calls(), p3.calls())
	}
}

func TestChain_ModelMappingAcrossProviders(t *testing.T) {
	// p1 fails on retriable error, p2 (ollama) must receive the mapped model.
	p1 := &mockProvider{name: "openai", err: errors.New("503 service unavailable")}
	p2 := &mockProvider{name: "ollama", response: &provider.ChatResponse{Content: "local ok"}}

	chain := NewChain([]provider.Provider{p1, p2}, silentLogger())

	_, err := chain.Chat(context.Background(), newReq("gpt-4o-mini"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := p2.lastModel.Load()
	if got == nil {
		t.Fatalf("p2 never received a model")
	}
	if got.(string) != "llama3.1:8b" {
		t.Errorf("expected model mapped to llama3.1:8b, got %q", got)
	}

	// p1 receives the original model since there's no mapping for "openai"->"gpt-4o-mini".
	gotP1 := p1.lastModel.Load()
	if gotP1 == nil || gotP1.(string) != "gpt-4o-mini" {
		t.Errorf("expected p1 to get original model gpt-4o-mini, got %v", gotP1)
	}
}

func TestChain_UnmappedModelPassthrough(t *testing.T) {
	p1 := &mockProvider{name: "openai", err: errors.New("500 internal server error")}
	p2 := &mockProvider{name: "ollama", response: &provider.ChatResponse{Content: "ok"}}

	chain := NewChain([]provider.Provider{p1, p2}, silentLogger())

	_, err := chain.Chat(context.Background(), newReq("some-unknown-model"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := p2.lastModel.Load()
	if got == nil || got.(string) != "some-unknown-model" {
		t.Errorf("expected unmapped model to passthrough, got %v", got)
	}
}

func TestChain_SetModelMapCustom(t *testing.T) {
	p1 := &mockProvider{name: "openai", err: errors.New("429 rate limit")}
	p2 := &mockProvider{name: "customprov", response: &provider.ChatResponse{Content: "ok"}}

	chain := NewChain([]provider.Provider{p1, p2}, silentLogger())
	chain.SetModelMap("customprov", "gpt-4o", "my-custom-model")

	_, err := chain.Chat(context.Background(), newReq("gpt-4o"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := p2.lastModel.Load()
	if got == nil || got.(string) != "my-custom-model" {
		t.Errorf("expected custom mapped model, got %v", got)
	}
}

func TestChain_SetModelMapOverridesDefault(t *testing.T) {
	p1 := &mockProvider{name: "openai", err: errors.New("502 bad gateway")}
	p2 := &mockProvider{name: "ollama", response: &provider.ChatResponse{Content: "ok"}}

	chain := NewChain([]provider.Provider{p1, p2}, silentLogger())
	// Default maps gpt-4o-mini -> llama3.1:8b, override it.
	chain.SetModelMap("ollama", "gpt-4o-mini", "phi3:mini")

	_, err := chain.Chat(context.Background(), newReq("gpt-4o-mini"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := p2.lastModel.Load()
	if got == nil || got.(string) != "phi3:mini" {
		t.Errorf("expected overridden mapping phi3:mini, got %v", got)
	}
}

func TestChain_EmptyProviderList(t *testing.T) {
	chain := NewChain([]provider.Provider{}, silentLogger())

	resp, err := chain.Chat(context.Background(), newReq("gpt-4o"))
	if err == nil {
		t.Fatalf("expected error with empty chain, got resp=%+v", resp)
	}
	if !strings.Contains(err.Error(), "todos os providers falharam") {
		t.Errorf("expected 'todos os providers falharam' message, got %v", err)
	}
}

func TestChain_ChatStreamFallback(t *testing.T) {
	p1 := &mockProvider{name: "openai", streamErr: errors.New("503 service unavailable")}
	p2 := &mockProvider{
		name:         "anthropic",
		streamChunks: []string{"hel", "lo"},
		response:     &provider.ChatResponse{Content: "hello"},
	}

	chain := NewChain([]provider.Provider{p1, p2}, silentLogger())

	var received strings.Builder
	cb := func(d provider.StreamDelta) {
		received.WriteString(d.Content)
	}

	resp, err := chain.ChatStream(context.Background(), newReq("gpt-4o"), cb)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "hello" {
		t.Errorf("expected hello, got %q", resp.Content)
	}
	if received.String() != "hello" {
		t.Errorf("expected streamed 'hello', got %q", received.String())
	}
	if p1.calls() != 1 || p2.calls() != 1 {
		t.Errorf("expected p1=1,p2=1 calls, got p1=%d p2=%d", p1.calls(), p2.calls())
	}
}

func TestChain_ChatStreamNonRetriableStops(t *testing.T) {
	p1 := &mockProvider{name: "openai", streamErr: errors.New("401 unauthorized")}
	p2 := &mockProvider{name: "anthropic", streamChunks: []string{"nope"}}

	chain := NewChain([]provider.Provider{p1, p2}, silentLogger())

	_, err := chain.ChatStream(context.Background(), newReq("gpt-4o"), nil)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if p2.calls() != 0 {
		t.Errorf("p2 should not be called after non-retriable stream error, got %d", p2.calls())
	}
}

func TestChain_ChatStreamAllFail(t *testing.T) {
	p1 := &mockProvider{name: "openai", streamErr: errors.New("503 service unavailable")}
	p2 := &mockProvider{name: "anthropic", streamErr: errors.New("connection refused")}

	chain := NewChain([]provider.Provider{p1, p2}, silentLogger())

	_, err := chain.ChatStream(context.Background(), newReq("gpt-4o"), nil)
	if err == nil {
		t.Fatalf("expected error when all stream providers fail")
	}
	if !strings.Contains(err.Error(), "todos os providers falharam (stream)") {
		t.Errorf("expected stream failure message, got %v", err)
	}
}

func TestChain_RetriableErrorDetection(t *testing.T) {
	cases := []struct {
		name      string
		errMsg    string
		retriable bool
	}{
		{"429 code", "got 429 response", true},
		{"rate limit text", "rate limit exceeded", true},
		{"too many requests", "too many requests received", true},
		{"500", "500 internal server error", true},
		{"502", "502 bad gateway", true},
		{"503", "503 service unavailable", true},
		{"504", "504 gateway timeout", true},
		{"connection refused", "dial tcp: connection refused", true},
		{"connection reset", "read: connection reset by peer", true},
		{"timeout", "request timeout", true},
		{"deadline exceeded", "context deadline exceeded", true},
		{"uppercase", "RATE LIMIT", true},
		{"401", "401 unauthorized", false},
		{"403", "403 forbidden", false},
		{"400", "400 bad request", false},
		{"parse error", "failed to parse response", false},
		{"empty", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isRetriableError(errors.New(tc.errMsg))
			if got != tc.retriable {
				t.Errorf("isRetriableError(%q) = %v, want %v", tc.errMsg, got, tc.retriable)
			}
		})
	}
}

func TestChain_OriginalModelPreservedAcrossAttempts(t *testing.T) {
	// Verify that req.Model is re-mapped from the original on each attempt,
	// not carried over from the previous (already mapped) provider.
	p1 := &mockProvider{name: "ollama", err: errors.New("503 service unavailable")} // maps gpt-4o-mini -> llama3.1:8b
	p2 := &mockProvider{name: "anthropic", response: &provider.ChatResponse{Content: "ok"}}

	chain := NewChain([]provider.Provider{p1, p2}, silentLogger())

	_, err := chain.Chat(context.Background(), newReq("gpt-4o-mini"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// p1 should have received the ollama-mapped model.
	gotP1 := p1.lastModel.Load()
	if gotP1 == nil || gotP1.(string) != "llama3.1:8b" {
		t.Errorf("p1 (ollama) expected llama3.1:8b, got %v", gotP1)
	}
	// p2 should have received the anthropic-mapped model (claude haiku).
	gotP2 := p2.lastModel.Load()
	if gotP2 == nil || gotP2.(string) != "claude-3-haiku-20240307" {
		t.Errorf("p2 (anthropic) expected claude-3-haiku-20240307, got %v", gotP2)
	}
}

func TestChain_SingleProviderSuccess(t *testing.T) {
	p1 := &mockProvider{name: "openai", response: &provider.ChatResponse{Content: "solo"}}

	chain := NewChain([]provider.Provider{p1}, silentLogger())

	resp, err := chain.Chat(context.Background(), newReq("gpt-4o"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "solo" {
		t.Errorf("expected solo, got %q", resp.Content)
	}
}

func TestChain_SingleProviderFailure(t *testing.T) {
	p1 := &mockProvider{name: "openai", err: errors.New("503 service unavailable")}

	chain := NewChain([]provider.Provider{p1}, silentLogger())

	_, err := chain.Chat(context.Background(), newReq("gpt-4o"))
	if err == nil {
		t.Fatalf("expected error from single failing provider")
	}
	if !errors.Is(err, p1.err) {
		t.Errorf("expected wrapped original error, got %v", err)
	}
}
