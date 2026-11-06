package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHTTPEmbedderOrdersVectorsAndKeepsKeyOnEndpoint(t *testing.T) {
	const key = "synthetic-embedding-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" || r.Header.Get("Authorization") != "Bearer "+key {
			t.Errorf("request path/auth = %q/%q", r.URL.Path, r.Header.Get("Authorization"))
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		var body struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body.Model != "embed-test" || len(body.Input) != 2 {
			t.Errorf("request body = %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"index":1,"embedding":[0,1]},{"index":0,"embedding":[1,0]}]}`))
	}))
	defer server.Close()
	embedder, err := NewHTTPEmbedder(HTTPEmbeddingConfig{BaseURL: server.URL + "/v1", APIKey: key, Model: "embed-test"})
	if err != nil {
		t.Fatal(err)
	}
	vectors, err := embedder.Embed(context.Background(), []string{"primeiro", "segundo"})
	if err != nil || len(vectors) != 2 || vectors[0][0] != 1 || vectors[1][1] != 1 {
		t.Fatalf("ordered vectors = %v, %v", vectors, err)
	}
	if embedder.Name() == "" || strings.Contains(embedder.Name(), key) {
		t.Fatalf("unsafe model identity = %q", embedder.Name())
	}
}

func TestHTTPEmbedderRejectsIncompleteAndUnsafeConfig(t *testing.T) {
	if _, err := NewHTTPEmbedder(HTTPEmbeddingConfig{BaseURL: "https://example.test/v1", APIKey: "key"}); err == nil {
		t.Fatal("missing model accepted")
	}
	if _, err := NewHTTPEmbedder(HTTPEmbeddingConfig{BaseURL: "http://remote.test/v1", APIKey: "key", Model: "model"}); err == nil {
		t.Fatal("plain HTTP with API key accepted")
	}
	if _, err := NewHTTPEmbedder(HTTPEmbeddingConfig{BaseURL: "http://remote.test/v1", Model: "model"}); err == nil {
		t.Fatal("plain HTTP with memory text accepted")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer server.Close()
	embedder, err := NewHTTPEmbedder(HTTPEmbeddingConfig{BaseURL: server.URL, Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := embedder.Embed(context.Background(), []string{"a", "b"}); err == nil {
		t.Fatal("incomplete response accepted")
	}
}

func TestHTTPEmbedderDoesNotFollowCredentialRedirect(t *testing.T) {
	var redirected atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/embeddings", http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	embedder, err := NewHTTPEmbedder(HTTPEmbeddingConfig{BaseURL: source.URL, APIKey: "synthetic-key", Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := embedder.Embed(context.Background(), []string{"texto"}); err == nil {
		t.Fatal("redirect response accepted")
	}
	if redirected.Load() {
		t.Fatal("request followed redirect")
	}
}
