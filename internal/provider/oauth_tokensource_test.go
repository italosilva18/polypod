package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGoogleWithTokenSourceUsesBearerWithoutAPIKeyQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "" {
			t.Errorf("OAuth request nao deve enviar ?key= na query string: %s", r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer ya29.oauth-gemini-token" {
			t.Errorf("Authorization = %q, esperado Bearer ya29.oauth-gemini-token", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{"parts": []map[string]string{{"text": "ok gemini oauth"}}}, "finishReason": "STOP"},
			},
		})
	}))
	defer srv.Close()

	g := NewGoogleWithTokenSource(func(context.Context) (string, error) {
		return "ya29.oauth-gemini-token", nil
	}, srv.URL)
	g.httpClient = srv.Client()

	resp, err := g.Chat(context.Background(), ChatRequest{
		Model:    "gemini-2.5-pro",
		Messages: []Message{{Role: "user", Content: "ping"}},
	})
	if err != nil {
		t.Fatalf("Google.Chat com TokenSource: %v", err)
	}
	if resp.Content != "ok gemini oauth" {
		t.Fatalf("resp.Content = %q", resp.Content)
	}
}

func TestOpenAICompatWithTokenSourceRotatesPerTurn(t *testing.T) {
	calls := 0
	var headers []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = append(headers, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-1",
			"object":  "chat.completion",
			"model":   "gpt-4o",
			"choices": []map[string]any{{"index": 0, "message": map[string]string{"role": "assistant", "content": "pong"}, "finish_reason": "stop"}},
		})
	}))
	defer srv.Close()

	p := NewOpenAICompatWithTokenSource("openrouter", func(context.Context) (string, error) {
		calls++
		if calls == 1 {
			return "tok-turn-1", nil
		}
		return "tok-turn-2", nil
	}, srv.URL)

	for i := 0; i < 2; i++ {
		if _, err := p.Chat(context.Background(), ChatRequest{
			Model:    "gpt-4o",
			Messages: []Message{{Role: "user", Content: "oi"}},
		}); err != nil {
			t.Fatalf("turn %d erro: %v", i+1, err)
		}
	}
	if len(headers) != 2 || headers[0] != "Bearer tok-turn-1" || headers[1] != "Bearer tok-turn-2" {
		t.Fatalf("headers rotacionados inesperados: %+v", headers)
	}
}
