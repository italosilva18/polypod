package provider

import (
	"testing"
)

func TestRegistryBasic(t *testing.T) {
	reg := NewRegistry()
	if len(reg.List()) != 0 {
		t.Fatalf("new registry should be empty, got %d", len(reg.List()))
	}

	p := NewOpenAICompat("test", "key", "http://localhost")
	reg.Register(p)

	if len(reg.List()) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(reg.List()))
	}

	got, ok := reg.Get("test")
	if !ok {
		t.Fatal("should find 'test' provider")
	}
	if got.Name() != "test" {
		t.Errorf("name = %q, want %q", got.Name(), "test")
	}
}

func TestOpenAICompatCapabilities(t *testing.T) {
	p := NewOpenAICompat("test", "key", "http://localhost")
	if p.Name() != "test" {
		t.Errorf("Name() = %q", p.Name())
	}
	if !p.SupportsVision() {
		t.Error("should support vision")
	}
	if !p.SupportsTools() {
		t.Error("should support tools")
	}
}

func TestOllamaCapabilities(t *testing.T) {
	p := NewOllama("http://localhost:11434")
	if p.Name() != "ollama" {
		t.Errorf("Name() = %q", p.Name())
	}
	if !p.SupportsVision() {
		t.Error("should support vision")
	}
}

func TestAnthropicCapabilities(t *testing.T) {
	p := NewAnthropic("test-key", "")
	if p.Name() != "anthropic" {
		t.Errorf("Name() = %q", p.Name())
	}
	if !p.SupportsVision() {
		t.Error("should support vision")
	}
	if !p.SupportsTools() {
		t.Error("should support tools")
	}
	if !p.SupportsStructuredOutput() {
		t.Error("should support structured output")
	}
}

func TestGoogleCapabilities(t *testing.T) {
	p := NewGoogle("test-key", "")
	if p.Name() != "google" {
		t.Errorf("Name() = %q", p.Name())
	}
}

func TestProviderFactory(t *testing.T) {
	tests := []struct {
		name     string
		wantName string
	}{
		{"groq", "groq"},
		{"mistral", "mistral"},
		{"ollama", "ollama"},
		{"anthropic", "anthropic"},
		{"google", "google"},
		{"openrouter", "openrouter"},
		{"custom", "custom"},
	}

	for _, tt := range tests {
		p := ProviderFactory(tt.name, "key", "http://test")
		if p.Name() != tt.wantName {
			t.Errorf("ProviderFactory(%q).Name() = %q, want %q", tt.name, p.Name(), tt.wantName)
		}
	}
}

func TestSupportedProviders(t *testing.T) {
	providers := SupportedProviders()
	if len(providers) < 15 {
		t.Errorf("expected at least 15 providers, got %d", len(providers))
	}

	// Check key providers exist
	has := make(map[string]bool)
	for _, p := range providers {
		has[p] = true
	}
	for _, need := range []string{"deepseek", "openai", "ollama", "anthropic", "google", "groq"} {
		if !has[need] {
			t.Errorf("missing provider: %s", need)
		}
	}
}

func TestConvertMessages(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi!"},
	}

	result := convertMessages(msgs)
	if len(result) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result))
	}
	if result[0].Role != "system" {
		t.Errorf("msg[0].Role = %q", result[0].Role)
	}
}

func TestConvertMessagesMultimodal(t *testing.T) {
	msgs := []Message{
		{
			Role: "user",
			Parts: []ContentPart{
				{Type: "text", Text: "What is this?"},
				{Type: "image_base64", ImageB64: "abc123", MimeType: "image/png"},
			},
		},
	}

	result := convertMessages(msgs)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if len(result[0].MultiContent) != 2 {
		t.Errorf("expected 2 multimodal parts, got %d", len(result[0].MultiContent))
	}
}
