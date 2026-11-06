package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/costa/polypod/internal/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/provider"
	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/types"
)

type visionTestProvider struct {
	provider.Provider
	vision   bool
	requests []provider.ChatRequest
}

func (p *visionTestProvider) SupportsVision() bool { return p.vision }
func (p *visionTestProvider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	p.requests = append(p.requests, req)
	if len(p.requests) == 1 {
		return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "screen", Name: "screenshot", Arguments: "{}"}, {ID: "other", Name: "screenshot", Arguments: "{}"}}}, nil
	}
	return &provider.ChatResponse{Content: "done"}, nil
}

func TestToolImagesReachNextModelRequest(t *testing.T) {
	for _, streamingManager := range []bool{false, true} {
		for _, vision := range []bool{false, true} {
			t.Run(strings.Join([]string{map[bool]string{true: "manager", false: "adapter"}[streamingManager], map[bool]string{true: "vision", false: "text"}[vision]}, "/"), func(t *testing.T) {
				reg := tool.NewRegistry()
				reg.RegisterDef(types.ToolDef{NameFn: func() string { return "screenshot" }, DescriptionFn: func() string { return "screen" }, InputSchemaFn: func() map[string]any { return map[string]any{"type": "object"} }, ExecuteFn: func(types.ToolContext, map[string]any) (types.ToolResult, error) {
					return types.ToolResult{Content: "OCR map", Images: []types.ToolImage{{MIMEType: "image/png", Base64: "cG5n"}}}, nil
				}})
				p := &visionTestProvider{vision: vision}
				c := &Client{responseProvider: p, skills: NewToolRegistryAdapter(reg, nil), toolsOn: true}
				if streamingManager {
					c.streamingToolMgr = NewStreamingToolManager(reg, nil, silentLogger())
				}
				_, err := c.completeWithResponseTools(context.Background(), []ChatMessage{{Role: "user", Content: "see"}}, nil, silentLogger())
				if err != nil {
					t.Fatal(err)
				}
				msgs := p.requests[1].Messages
				if msgs[2].Role != "tool" || msgs[3].Role != "tool" {
					t.Fatal("tool response ordering broken")
				}
				if vision {
					if len(msgs) != 6 || msgs[4].Parts[1].ImageB64 != "cG5n" {
						t.Fatal("image missing from model request")
					}
					wire, err := json.Marshal(openAIImageObservations([]ToolCallResult{{ID: "screen", Name: "screenshot", Images: []types.ToolImage{{MIMEType: "image/png", Base64: "cG5n"}}}}))
					if err != nil || !strings.Contains(string(wire), "data:image/png;base64,cG5n") {
						t.Fatalf("invalid Chat Completions payload: %s %v", wire, err)
					}
				} else if len(msgs) != 4 || msgs[2].Content != "OCR map" {
					t.Fatal("text provider fallback lost")
				}
			})
		}
	}
}

func TestChatCompletionImageWireAfterToolBatch(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, manager := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%v/manager=%v", stream, manager), func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var req struct {
						Messages []json.RawMessage `json:"messages"`
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					calls++
					if calls == 2 {
						if len(req.Messages) != 4 || !strings.Contains(string(req.Messages[2]), `"role":"tool"`) || !strings.Contains(string(req.Messages[3]), "data:image/png;base64,cG5n") {
							t.Errorf("missing image or wrong ordering: %s", req.Messages)
						}
					}
					w.Header().Set("Content-Type", "application/json")
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						if calls == 1 {
							fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"screen","type":"function","function":{"name":"screenshot","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
						} else {
							fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
						}
					} else if calls == 1 {
						fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"screen","type":"function","function":{"name":"screenshot","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
					} else {
						fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
					}
				}))
				defer server.Close()
				reg := tool.NewRegistry()
				reg.RegisterDef(types.ToolDef{NameFn: func() string { return "screenshot" }, DescriptionFn: func() string { return "screen" }, InputSchemaFn: func() map[string]any { return map[string]any{"type": "object"} }, ExecuteFn: func(types.ToolContext, map[string]any) (types.ToolResult, error) {
					return types.ToolResult{Content: "OCR map", Images: []types.ToolImage{{MIMEType: "image/png", Base64: "cG5n"}}}, nil
				}})
				c := NewClient(config.AIConfig{BaseURL: server.URL, Model: "vision-test", Tools: true}, NewToolRegistryAdapter(reg, nil))
				if manager {
					c.SetStreamingToolManager(NewStreamingToolManager(reg, nil, silentLogger()))
				}
				msgs := []ChatMessage{{Role: "user", Content: "see"}}
				var err error
				if stream {
					_, err = c.CompleteWithToolsStream(context.Background(), msgs, func(string) {}, silentLogger())
				} else {
					_, err = c.CompleteWithTools(context.Background(), msgs, silentLogger())
				}
				if err != nil {
					t.Fatal(err)
				}
				if calls != 2 {
					t.Fatalf("requests=%d", calls)
				}
			})
		}
	}
}

func TestChatMessageMixedContentMarshals(t *testing.T) {
	msg := openAIChatMessage(ChatMessage{Role: "user", Content: "Inspect screenshot", Parts: []provider.ContentPart{{Type: "image_base64", MimeType: "image/png", ImageB64: "cG5n"}}})
	wire, err := json.Marshal(msg)
	if err != nil || !strings.Contains(string(wire), "Inspect screenshot") || !strings.Contains(string(wire), "data:image/png;base64,cG5n") {
		t.Fatalf("mixed content lost: %s %v", wire, err)
	}
}
