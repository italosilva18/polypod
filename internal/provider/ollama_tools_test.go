package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOllamaStreamCarriesNativeToolCallsAndHistory(t *testing.T) {
	var request ollamaChatReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","tool_calls":[{"function":{"name":"read_file","arguments":{"file_path":"a.go"}}}]},"done":false}` + "\n"))
		_, _ = w.Write([]byte("{\"message\":{},\"done\":true,\"prompt_eval_count\":20,\"eval_count\":4}\n"))
	}))
	defer server.Close()
	provider := NewOllama(server.URL)
	response, err := provider.ChatStream(context.Background(), ChatRequest{Model: "local", Messages: []Message{
		{Role: "assistant", ToolCalls: []ToolCall{{Name: "read_file", Arguments: `{"file_path":"before.go"}`}}},
		{Role: "tool", Content: "contents"},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "read_file" || response.ToolCalls[0].ID == "" || response.TotalTokens != 24 {
		t.Fatalf("native call lost: %+v", response)
	}
	if len(request.Messages) != 2 || len(request.Messages[0].ToolCalls) != 1 || request.Messages[0].ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("tool history lost: %+v", request.Messages)
	}
}

func TestOllamaExactTextToolCallRequiresOfferedName(t *testing.T) {
	content := `{"name":"write_file","arguments":{"file_path":"x.go","content":"ok"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"role": "assistant", "content": content}, "done": true})
	}))
	defer server.Close()
	provider := NewOllama(server.URL)
	response, err := provider.Chat(context.Background(), ChatRequest{Model: "local", Tools: []Tool{{Name: "write_file"}}})
	if err != nil || len(response.ToolCalls) != 1 || response.Content != "" {
		t.Fatalf("exact JSON tool not recognized: %+v, %v", response, err)
	}
	response, err = provider.Chat(context.Background(), ChatRequest{Model: "local", Tools: []Tool{{Name: "read_file"}}})
	if err != nil || len(response.ToolCalls) != 0 || !strings.Contains(response.Content, "write_file") {
		t.Fatalf("unadvertised tool was accepted: %+v, %v", response, err)
	}
}
