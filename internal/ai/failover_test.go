package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/costa/polypod/internal/config"
	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/types"
)

func TestConfiguredFailoverContinuesToolHistory(t *testing.T) {
	primaryCalls, fallbackCalls, toolCalls := 0, 0, 0
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalls++
		if primaryCalls > 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(503)
			fmt.Fprint(w, `{"error":{"message":"unavailable"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: "+`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"written","type":"function","function":{"name":"write_file","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
	}))
	defer primary.Close()
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackCalls++
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role       string `json:"role"`
				ToolCallID string `json:"tool_call_id"`
				Content    string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req.Model != "backup-model" || len(req.Messages) != 3 || req.Messages[2].ToolCallID != "written" || req.Messages[2].Content != "already written" {
			t.Errorf("lost tool history or fallback model: %+v", req)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: "+`{"choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
	}))
	defer secondary.Close()
	reg := tool.NewRegistry()
	reg.RegisterDef(types.ToolDef{NameFn: func() string { return "write_file" }, DescriptionFn: func() string { return "write" }, InputSchemaFn: func() map[string]any { return map[string]any{"type": "object"} }, ExecuteFn: func(types.ToolContext, map[string]any) (types.ToolResult, error) {
		toolCalls++
		return types.ToolResult{Content: "already written"}, nil
	}})
	client := NewClient(config.AIConfig{Provider: "primary", BaseURL: primary.URL, Model: "primary-model", Tools: true, FallbackProvider: "secondary", FallbackBaseURL: secondary.URL, FallbackModel: "backup-model"}, NewToolRegistryAdapter(reg, nil))
	resp, err := client.CompleteWithToolsStream(context.Background(), []ChatMessage{{Role: "user", Content: "write once"}}, func(string) {}, silentLogger())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "done" || toolCalls != 1 || fallbackCalls != 1 || primaryCalls != 2 {
		t.Fatalf("response=%+v tool=%d fallback=%d primary=%d", resp, toolCalls, fallbackCalls, primaryCalls)
	}
}
