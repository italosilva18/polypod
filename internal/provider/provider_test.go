package provider

import (
	"strings"
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

func TestRegistryUnknown(t *testing.T) {
	reg := NewRegistry()
	if _, ok := reg.Get("nope"); ok {
		t.Error("expected nope to be missing")
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

func TestProviderFactoryImplementsInterface(t *testing.T) {
	// Compile-time assertion that factory output satisfies Provider.
	var _ Provider = ProviderFactory("groq", "key", "")
	var _ Provider = ProviderFactory("anthropic", "key", "")
	var _ Provider = ProviderFactory("google", "key", "")
	var _ Provider = ProviderFactory("ollama", "key", "http://localhost")
	var _ Provider = ProviderFactory("custom", "key", "http://x")
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

// --- Bedrock provider tests ---

func TestBedrockProvider_Name(t *testing.T) {
	p := NewBedrockProvider(BedrockConfig{
		AccessKeyID: "AKIA123",
		SecretKey:   "secret",
	})
	if p.Name() != "bedrock" {
		t.Errorf("Name = %q, want bedrock", p.Name())
	}
}

func TestBedrockProvider_Defaults(t *testing.T) {
	p := NewBedrockProvider(BedrockConfig{})
	if p.config.Region != "us-east-1" {
		t.Errorf("default region = %q, want us-east-1", p.config.Region)
	}
	if p.config.Model != "anthropic.claude-3-5-sonnet-20241022-v2:0" {
		t.Errorf("default model = %q", p.config.Model)
	}
	if p.client == nil {
		t.Error("client should be non-nil")
	}
}

func TestBedrockProvider_IsAvailable(t *testing.T) {
	p1 := NewBedrockProvider(BedrockConfig{})
	if p1.IsAvailable() {
		t.Error("empty config should be unavailable")
	}

	p2 := NewBedrockProvider(BedrockConfig{
		AccessKeyID: "AKIA123",
		SecretKey:   "secret",
	})
	if !p2.IsAvailable() {
		t.Error("full config should be available")
	}

	// Only one credential is not enough.
	p3 := NewBedrockProvider(BedrockConfig{AccessKeyID: "only-key"})
	if p3.IsAvailable() {
		t.Error("access key alone should be unavailable")
	}
	p4 := NewBedrockProvider(BedrockConfig{SecretKey: "only-secret"})
	if p4.IsAvailable() {
		t.Error("secret key alone should be unavailable")
	}
}

func TestBedrockProvider_Models(t *testing.T) {
	p := NewBedrockProvider(BedrockConfig{})
	models := p.Models()
	if len(models) == 0 {
		t.Fatal("Models should not be empty")
	}
	// Check that a few known model IDs are present.
	found := make(map[string]bool)
	for _, m := range models {
		found[m] = true
	}
	expected := []string{
		"anthropic.claude-3-5-sonnet-20241022-v2:0",
		"anthropic.claude-3-5-haiku-20241022-v1:0",
	}
	for _, want := range expected {
		if !found[want] {
			t.Errorf("Models missing %q", want)
		}
	}
}

func TestBedrockModelInfo(t *testing.T) {
	info := BedrockModelInfo("anthropic.claude-3-5-sonnet-20241022-v2:0")
	if len(info) == 0 {
		t.Fatal("info should not be empty for known model")
	}
	// Verify a few expected keys.
	if _, ok := info["context_tokens"]; !ok {
		t.Error("missing context_tokens key")
	}
	if _, ok := info["supports_tools"]; !ok {
		t.Error("missing supports_tools key")
	}
}

func TestBedrockModelInfo_Unknown(t *testing.T) {
	unknown := BedrockModelInfo("nonexistent")
	if len(unknown) != 0 {
		t.Errorf("info for unknown model should be empty; got %v", unknown)
	}
}

// --- Azure provider tests ---

func TestAzureProvider_Name(t *testing.T) {
	p := NewAzureProvider(AzureConfig{})
	if p.Name() != "azure" {
		t.Errorf("Name = %q, want azure", p.Name())
	}
}

func TestAzureProvider_Defaults(t *testing.T) {
	p := NewAzureProvider(AzureConfig{})
	if p.config.APIVersion != "2024-08-01-preview" {
		t.Errorf("default APIVersion = %q", p.config.APIVersion)
	}
	if p.client == nil {
		t.Error("client should be non-nil")
	}
}

func TestAzureProvider_IsAvailable(t *testing.T) {
	if NewAzureProvider(AzureConfig{}).IsAvailable() {
		t.Error("empty config should be unavailable")
	}
	full := NewAzureProvider(AzureConfig{
		Endpoint:   "https://example.openai.azure.com",
		APIKey:     "key",
		Deployment: "gpt-4o",
	})
	if !full.IsAvailable() {
		t.Error("full config should be available")
	}
	// Missing deployment.
	part := NewAzureProvider(AzureConfig{
		Endpoint: "https://example.openai.azure.com",
		APIKey:   "key",
	})
	if part.IsAvailable() {
		t.Error("missing deployment should be unavailable")
	}
}

func TestAzureProvider_BuildURL(t *testing.T) {
	p := NewAzureProvider(AzureConfig{
		Endpoint:   "https://example.openai.azure.com",
		Deployment: "my-gpt4",
		APIKey:     "k",
		APIVersion: "2024-08-01-preview",
	})
	url := p.BuildURL("/chat/completions")
	want := "https://example.openai.azure.com/openai/deployments/my-gpt4/chat/completions?api-version=2024-08-01-preview"
	if url != want {
		t.Errorf("BuildURL = %q, want %q", url, want)
	}
	if !strings.Contains(url, "my-gpt4") {
		t.Error("URL should include deployment name")
	}
}

func TestAzureProvider_Models(t *testing.T) {
	p := NewAzureProvider(AzureConfig{})
	models := p.Models()
	if len(models) == 0 {
		t.Fatal("Models should not be empty")
	}
	found := make(map[string]bool)
	for _, m := range models {
		found[m] = true
	}
	if !found["gpt-4o"] {
		t.Error("expected gpt-4o in models")
	}
	if !found["gpt-4o-mini"] {
		t.Error("expected gpt-4o-mini in models")
	}
}

func TestAzureModelInfo(t *testing.T) {
	info := AzureModelInfo("gpt-4o")
	if len(info) == 0 {
		t.Fatal("info should not be empty for gpt-4o")
	}
	if _, ok := info["context_tokens"]; !ok {
		t.Error("missing context_tokens key")
	}
	if _, ok := info["input_cost"]; !ok {
		t.Error("missing input_cost key")
	}
}

func TestAzureModelInfo_Unknown(t *testing.T) {
	info := AzureModelInfo("no-such-model")
	if len(info) != 0 {
		t.Errorf("expected empty info for unknown model; got %v", info)
	}
}

// --- Mistral (standalone) provider tests ---

func TestMistralProvider_Name(t *testing.T) {
	p := NewMistralProvider(MistralConfig{APIKey: "k"})
	if p.Name() != "mistral" {
		t.Errorf("Name = %q, want mistral", p.Name())
	}
}

func TestMistralProvider_Defaults(t *testing.T) {
	p := NewMistralProvider(MistralConfig{})
	if p.config.BaseURL != "https://api.mistral.ai/v1" {
		t.Errorf("default BaseURL = %q", p.config.BaseURL)
	}
	if p.client == nil {
		t.Error("client should be non-nil")
	}
}

func TestMistralProvider_CustomBaseURL(t *testing.T) {
	p := NewMistralProvider(MistralConfig{BaseURL: "http://my-proxy/v1"})
	if p.config.BaseURL != "http://my-proxy/v1" {
		t.Errorf("BaseURL should be preserved; got %q", p.config.BaseURL)
	}
}

func TestMistralProvider_IsAvailable(t *testing.T) {
	if NewMistralProvider(MistralConfig{}).IsAvailable() {
		t.Error("empty config should be unavailable")
	}
	if !NewMistralProvider(MistralConfig{APIKey: "k"}).IsAvailable() {
		t.Error("config with APIKey should be available")
	}
}

func TestMistralProvider_Models(t *testing.T) {
	p := NewMistralProvider(MistralConfig{})
	models := p.Models()
	if len(models) == 0 {
		t.Fatal("Models should not be empty")
	}
	found := make(map[string]bool)
	for _, m := range models {
		found[m] = true
	}
	for _, want := range []string{"mistral-large-latest", "mistral-small-latest", "codestral-latest"} {
		if !found[want] {
			t.Errorf("Models missing %q", want)
		}
	}
}

func TestMistralModelInfo(t *testing.T) {
	info := MistralModelInfo("mistral-large-latest")
	if len(info) == 0 {
		t.Fatal("info should not be empty for mistral-large-latest")
	}
	if _, ok := info["context_tokens"]; !ok {
		t.Error("missing context_tokens key")
	}
	if _, ok := info["supports_tools"]; !ok {
		t.Error("missing supports_tools key")
	}
}

func TestMistralModelInfo_Unknown(t *testing.T) {
	info := MistralModelInfo("nope")
	if len(info) != 0 {
		t.Errorf("expected empty info for unknown model; got %v", info)
	}
}
