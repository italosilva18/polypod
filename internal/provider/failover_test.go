package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

type failoverStub struct {
	name          string
	vision, tools bool
	call          func(ChatRequest, StreamCallback) (*ChatResponse, error)
}

func (p *failoverStub) Name() string                   { return p.name }
func (p *failoverStub) SupportsVision() bool           { return p.vision }
func (p *failoverStub) SupportsTools() bool            { return p.tools }
func (p *failoverStub) SupportsStructuredOutput() bool { return true }
func (p *failoverStub) Chat(_ context.Context, r ChatRequest) (*ChatResponse, error) {
	return p.call(r, nil)
}
func (p *failoverStub) ChatStream(_ context.Context, r ChatRequest, c StreamCallback) (*ChatResponse, error) {
	return p.call(r, c)
}

func TestFailoverHTTPQuotaPreservesRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"private-secret quota exhausted","type":"insufficient_quota","code":"insufficient_quota"}}`)
	}))
	defer server.Close()
	req := ChatRequest{Model: "primary-model", MaxTokens: 100, Tools: []Tool{{Name: "read_file"}}, ResponseFormat: &ResponseFormat{Type: "json_object"}, Stop: []string{"END"}, Messages: []Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "already-executed", Name: "read_file", Arguments: `{"path":"a"}`}}},
		{Role: "tool", ToolCallID: "already-executed", Content: "result"},
		{Role: "user", Parts: []ContentPart{{Type: "image_url", ImageURL: "data:image/png;base64,AAAA"}}},
	}}
	var got ChatRequest
	secondary := &failoverStub{name: "configured", vision: true, tools: true, call: func(r ChatRequest, cb StreamCallback) (*ChatResponse, error) {
		got = r
		if cb != nil {
			cb(StreamDelta{Content: "ok"})
		}
		return &ChatResponse{Content: "ok"}, nil
	}}
	var logs bytes.Buffer
	router := NewFailover(NewOpenAICompat("primary", "secret", server.URL), secondary, "secondary-model", slog.New(slog.NewTextHandler(&logs, nil)))
	resp, err := router.ChatStream(context.Background(), req, func(StreamDelta) {})
	if err != nil || resp.Content != "ok" {
		t.Fatalf("response=%v err=%v", resp, err)
	}
	want := req
	want.Model = "secondary-model"
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("request changed: got %#v want %#v", got, want)
	}
	if req.Model != "primary-model" {
		t.Fatal("mutated original model")
	}
	if !strings.Contains(logs.String(), "ai provider failover") || strings.Contains(logs.String(), "secret") {
		t.Fatalf("unsafe/missing telemetry: %s", logs.String())
	}
}

func TestFailoverGuards(t *testing.T) {
	for _, tt := range []struct {
		name   string
		err    error
		delta  StreamDelta
		req    ChatRequest
		cancel bool
	}{
		{name: "invalid request", err: &openai.APIError{HTTPStatusCode: 400}},
		{name: "safety rejection", err: &openai.APIError{HTTPStatusCode: 403}},
		{name: "unknown error", err: errors.New("429 in user text")},
		{name: "partial text", err: io.ErrUnexpectedEOF, delta: StreamDelta{Content: "partial"}},
		{name: "partial tool", err: io.ErrUnexpectedEOF, delta: StreamDelta{ToolCalls: []ToolCall{{ID: "partial"}}}},
		{name: "canceled", err: ErrProviderUnavailable, cancel: true},
		{name: "vision unsupported", err: ErrProviderUnavailable, req: ChatRequest{Messages: []Message{{Parts: []ContentPart{{Type: "image_url", ImageURL: "image"}}}}}},
		{name: "tools unsupported", err: ErrProviderUnavailable, req: ChatRequest{Tools: []Tool{{Name: "write_file"}}}},
		{name: "history tools unsupported", err: ErrProviderUnavailable, req: ChatRequest{Messages: []Message{{Role: "tool", ToolCallID: "done"}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			primary := &failoverStub{name: "primary", call: func(_ ChatRequest, cb StreamCallback) (*ChatResponse, error) { cb(tt.delta); return nil, tt.err }}
			fallback := &failoverStub{name: "secondary", call: func(ChatRequest, StreamCallback) (*ChatResponse, error) {
				t.Fatal("unexpected fallback")
				return nil, nil
			}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.cancel {
				cancel()
			}
			_, err := NewFailover(primary, fallback, "fallback", nil).ChatStream(ctx, tt.req, nil)
			if err != tt.err {
				t.Fatalf("original error lost: %v", err)
			}
		})
	}
}
