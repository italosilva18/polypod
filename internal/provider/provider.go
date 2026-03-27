package provider

import (
	"context"
	"sync"
)

// Provider is the abstraction every AI backend must implement.
type Provider interface {
	Name() string
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	ChatStream(ctx context.Context, req ChatRequest, callback StreamCallback) (*ChatResponse, error)
	SupportsVision() bool
	SupportsTools() bool
	SupportsStructuredOutput() bool
}

// ChatRequest holds everything needed for a completion call.
type ChatRequest struct {
	Model          string
	Messages       []Message
	Tools          []Tool
	MaxTokens      int
	Temperature    float32
	ResponseFormat *ResponseFormat
	Stop           []string
}

// Message represents a single chat message with optional multimodal parts.
type Message struct {
	Role       string
	Content    string
	Parts      []ContentPart
	ToolCalls  []ToolCall
	ToolCallID string
	Name       string
}

// ContentPart is a piece of multimodal content (text, image URL, base64 image).
type ContentPart struct {
	Type     string // "text", "image_url", "image_base64"
	Text     string
	ImageURL string
	ImageB64 string
	MimeType string
}

// Tool describes a callable tool for function calling.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]interface{}
}

// ToolCall represents a tool invocation returned by the model.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// ResponseFormat controls structured output.
type ResponseFormat struct {
	Type   string                 // "json_object", "json_schema"
	Schema map[string]interface{} // JSON Schema when Type == "json_schema"
}

// ChatResponse is the unified response from any provider.
type ChatResponse struct {
	Content          string
	ToolCalls        []ToolCall
	FinishReason     string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	InputCostPer1M   float64
	OutputCostPer1M  float64
}

// StreamCallback receives incremental deltas during streaming.
type StreamCallback func(delta StreamDelta)

// StreamDelta carries a chunk of streamed content.
type StreamDelta struct {
	Content   string
	ToolCalls []ToolCall
	Done      bool
}

// Registry stores available providers keyed by name.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry creates an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

// Register adds a provider.
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Name()] = p
}

// Get retrieves a provider by name.
func (r *Registry) Get(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	return p, ok
}

// List returns all registered provider names.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.providers))
	for n := range r.providers {
		names = append(names, n)
	}
	return names
}
