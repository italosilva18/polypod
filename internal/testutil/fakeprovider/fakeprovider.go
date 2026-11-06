// Package fakeprovider provides a fake AI provider for testing.
package fakeprovider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync"
)

// Message is a single message in a conversation.
type Message struct {
	Role    string
	Content string
	Name    string
}

// ToolCall represents a tool invocation request from the model.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string // JSON string
}

// Request is a request to the provider.
type Request struct {
	Messages    []Message
	Model       string
	MaxTokens   int
	Temperature float32
	Tools       []ToolDefinition
	Stream      bool
}

// ToolDefinition describes a tool available to the model.
type ToolDefinition struct {
	Name        string
	Description string
	Schema      map[string]any
}

// Response is a model response.
type Response struct {
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
	InputTokens  int
	OutputTokens int
}

// Script defines a canned response triggered by a regex match on the last user message.
type Script struct {
	Match     *regexp.Regexp
	Reply     string
	ToolCalls []ToolCall
	Tokens    int
	Error     error
	Delay     int // milliseconds
}

// Call records a single Send/Stream invocation for assertions.
type Call struct {
	Request  Request
	Response Response
	Error    error
}

// FakeProvider is a thread-safe fake AI provider.
type FakeProvider struct {
	mu      sync.Mutex
	Scripts []Script
	Default string
	Calls   []Call

	// ErrorOnNthCall returns an error on the Nth call (1-indexed).
	// Set to 0 to disable.
	ErrorOnNthCall int
	NthError       error

	// CallCount counts total calls (including errors).
	callCount int
}

// New creates a new fake provider.
func New() *FakeProvider {
	return &FakeProvider{
		Default: "I am a fake AI. No script matched your message.",
	}
}

// AddScript adds a script entry.
func (p *FakeProvider) AddScript(script Script) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Scripts = append(p.Scripts, script)
}

// AddSimpleScript adds a script with just a regex pattern and reply.
func (p *FakeProvider) AddSimpleScript(pattern, reply string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}
	p.AddScript(Script{Match: re, Reply: reply})
	return nil
}

// SetDefault sets the default response.
func (p *FakeProvider) SetDefault(reply string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Default = reply
}

// Send executes a request and returns the response.
func (p *FakeProvider) Send(ctx context.Context, req Request) (Response, error) {
	p.mu.Lock()
	p.callCount++
	n := p.callCount

	// Check error injection
	if p.ErrorOnNthCall > 0 && n == p.ErrorOnNthCall {
		err := p.NthError
		if err == nil {
			err = errors.New("fakeprovider: injected error")
		}
		call := Call{Request: req, Error: err}
		p.Calls = append(p.Calls, call)
		p.mu.Unlock()
		return Response{}, err
	}
	p.mu.Unlock()

	if ctx != nil && ctx.Err() != nil {
		return Response{}, ctx.Err()
	}

	// Find matching script
	lastUserMsg := ""
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			lastUserMsg = req.Messages[i].Content
			break
		}
	}

	var resp Response
	var matchErr error

	p.mu.Lock()
	var matched *Script
	for i := range p.Scripts {
		s := &p.Scripts[i]
		if s.Match != nil && s.Match.MatchString(lastUserMsg) {
			matched = s
			break
		}
	}

	if matched != nil {
		if matched.Error != nil {
			matchErr = matched.Error
		} else {
			resp = Response{
				Content:      matched.Reply,
				ToolCalls:    matched.ToolCalls,
				FinishReason: "stop",
				InputTokens:  estimateTokens(lastUserMsg),
				OutputTokens: matched.Tokens,
			}
			if resp.OutputTokens == 0 {
				resp.OutputTokens = estimateTokens(matched.Reply)
			}
			if len(matched.ToolCalls) > 0 {
				resp.FinishReason = "tool_calls"
			}
		}
	} else {
		resp = Response{
			Content:      p.Default,
			FinishReason: "stop",
			InputTokens:  estimateTokens(lastUserMsg),
			OutputTokens: estimateTokens(p.Default),
		}
	}

	p.Calls = append(p.Calls, Call{Request: req, Response: resp, Error: matchErr})
	p.mu.Unlock()

	return resp, matchErr
}

// Stream simulates a streaming response by chunking content.
func (p *FakeProvider) Stream(ctx context.Context, req Request, callback func(delta string) error) (Response, error) {
	resp, err := p.Send(ctx, req)
	if err != nil {
		return resp, err
	}

	// Stream content in chunks of 10 characters
	content := resp.Content
	for i := 0; i < len(content); i += 10 {
		end := i + 10
		if end > len(content) {
			end = len(content)
		}
		if err := callback(content[i:end]); err != nil {
			return resp, err
		}
		if ctx != nil && ctx.Err() != nil {
			return resp, ctx.Err()
		}
	}

	return resp, nil
}

// Models returns a list of fake model names.
func (p *FakeProvider) Models() []string {
	return []string{"fake-small", "fake-medium", "fake-large"}
}

// Name returns the provider name.
func (p *FakeProvider) Name() string {
	return "fake"
}

// CallCount returns the number of calls made.
func (p *FakeProvider) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.callCount
}

// LastCall returns the most recent call.
func (p *FakeProvider) LastCall() (Call, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.Calls) == 0 {
		return Call{}, false
	}
	return p.Calls[len(p.Calls)-1], true
}

// LastRequest is a convenience for LastCall().Request.
func (p *FakeProvider) LastRequest() Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.Calls) == 0 {
		return Request{}
	}
	return p.Calls[len(p.Calls)-1].Request
}

// AllCalls returns a copy of all recorded calls.
func (p *FakeProvider) AllCalls() []Call {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]Call, len(p.Calls))
	copy(result, p.Calls)
	return result
}

// Reset clears recorded calls and resets the call counter.
func (p *FakeProvider) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Calls = nil
	p.callCount = 0
}

// ClearScripts removes all scripted responses.
func (p *FakeProvider) ClearScripts() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Scripts = nil
}

// AssertCallCount panics if the call count doesn't match (for tests).
func (p *FakeProvider) AssertCallCount(expected int) error {
	actual := p.CallCount()
	if actual != expected {
		return fmt.Errorf("expected %d calls, got %d", expected, actual)
	}
	return nil
}

// AssertContainsMessage returns nil if any call contains a message matching the pattern.
func (p *FakeProvider) AssertContainsMessage(pattern string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, call := range p.Calls {
		for _, msg := range call.Request.Messages {
			if re.MatchString(msg.Content) {
				return nil
			}
		}
	}
	return fmt.Errorf("no call contained a message matching %q", pattern)
}

// estimateTokens returns a rough token estimate (chars / 4).
func estimateTokens(s string) int {
	return len(s) / 4
}

// ScriptedToolCall is a helper for creating tool call scripts.
func ScriptedToolCall(name, args string) ToolCall {
	return ToolCall{
		ID:        fmt.Sprintf("call_%s", name),
		Name:      name,
		Arguments: args,
	}
}

// MustCompileScript creates a script with a compiled regex (panics on error).
func MustCompileScript(pattern, reply string) Script {
	return Script{
		Match: regexp.MustCompile(pattern),
		Reply: reply,
	}
}
