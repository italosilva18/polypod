package accountauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/costa/polypod/internal/chatgptauth"
	"github.com/costa/polypod/internal/provider"
)

func TestRotateActiveOnRateLimit_MultiProviderAndLive429Retry(t *testing.T) {
	dir := t.TempDir()
	oauthPath := filepath.Join(dir, "oauth-accounts.enc")
	chatPath := filepath.Join(dir, "chatgpt-oauth.enc")

	store := NewStoreWithChatGPT(oauthPath, chatPath)
	mgr := NewManager(store)

	// 1. Save two Google OAuth accounts and verify round-robin rotation.
	if err := store.SaveAccount("google", "team-alpha", &Credential{
		Provider:    "google",
		AccountID:   "team-alpha",
		AccessToken: "ya29.token-alpha",
		ExpiresAt:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAccount("google", "team-beta", &Credential{
		Provider:    "google",
		AccountID:   "team-beta",
		AccessToken: "ya29.token-beta",
		ExpiresAt:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	// Make team-alpha active initially.
	if err := store.SetActiveAccount("google", "team-alpha"); err != nil {
		t.Fatal(err)
	}

	// 2. Save two ChatGPT OAuth accounts and verify ChatGPT rotation via Manager too.
	cgStore := chatgptauth.NewStore(chatPath)
	if err := cgStore.SaveAccount("plus-1", &chatgptauth.Credential{
		AccountID:   "plus-1",
		AccessToken: "cg-token-1",
		ExpiresAt:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := cgStore.SaveAccount("plus-2", &chatgptauth.Credential{
		AccountID:   "plus-2",
		AccessToken: "cg-token-2",
		ExpiresAt:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := cgStore.SetActiveAccount("plus-1"); err != nil {
		t.Fatal(err)
	}
	nextCG, ok := mgr.RotateActiveOnRateLimit("chatgpt")
	if !ok || nextCG != "plus-2" {
		t.Fatalf("expected chatgpt rotation to plus-2, got (%q, %v)", nextCG, ok)
	}
	nextCG2, ok := mgr.RotateActiveOnRateLimit("chatgpt")
	if !ok || nextCG2 != "plus-1" {
		t.Fatalf("expected chatgpt wrap-around rotation to plus-1, got (%q, %v)", nextCG2, ok)
	}

	// 3. End-to-end provider HTTP 429 -> automatic OAuth account rotation -> HTTP 200 retry!
	provider.SetAccountRotator(mgr.RotateActiveOnRateLimit)
	t.Cleanup(func() { provider.SetAccountRotator(nil) })

	var hits atomic.Int32
	var seenAuth []string
	var seenBudget int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callNum := hits.Add(1)
		auth := r.Header.Get("Authorization")
		seenAuth = append(seenAuth, auth)

		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if gc, ok := payload["generationConfig"].(map[string]any); ok {
			if tc, ok := gc["thinkingConfig"].(map[string]any); ok {
				if b, ok := tc["thinkingBudget"].(float64); ok {
					seenBudget = int(b)
				}
			}
		}

		if callNum == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":429,"message":"Resource has been exhausted (quota)"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"resposta com segunda conta"}]}}],"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":6}}`))
	}))
	defer srv.Close()

	g := provider.NewGoogleWithTokenSource(mgr.TokenSource("google"), srv.URL)
	resp, err := g.Chat(context.Background(), provider.ChatRequest{
		Model:          "gemini-2.5-pro",
		Messages:       []provider.Message{{Role: "user", Content: "olá"}},
		ThinkingBudget: 8192,
	})
	if err != nil {
		t.Fatalf("expected transparent 429 account rotation recovery, got err: %v", err)
	}
	if resp.Content != "resposta com segunda conta" {
		t.Fatalf("unexpected response content: %q", resp.Content)
	}
	if hits.Load() != 2 {
		t.Fatalf("expected 2 HTTP calls (429 then 200), got %d", hits.Load())
	}
	if len(seenAuth) != 2 || !strings.Contains(seenAuth[0], "ya29.token-alpha") || !strings.Contains(seenAuth[1], "ya29.token-beta") {
		t.Fatalf("expected bearer rotation alpha -> beta, got %v", seenAuth)
	}
	if seenBudget != 8192 {
		t.Fatalf("expected Gemini 2.5 thinkingBudget=8192 forwarded on wire, got %d", seenBudget)
	}
}
