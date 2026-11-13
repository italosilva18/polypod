package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelCatalogFiltersProvidersAndMergesWithLocalFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"openai":{"models":{"new-model":{"id":"new-model","name":"Novo modelo","tool_call":true,"limit":{"context":123456},"cost":{"input":1.5,"output":3}},"old-model":{"status":"deprecated"}}},"other":{"models":{"hidden":{}}}}`))
		if err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	models, err := fetchModelCatalog(context.Background(), server.URL, []ProviderOption{{Name: "openai"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "new-model" || models[0].MaxContext != 123456 || !models[0].SupportsTools {
		t.Fatalf("catálogo filtrado incorretamente: %+v", models)
	}
	picker := &modelPickerModel{models: []ModelInfo{{ID: "local", Provider: "openai"}}}
	picker.mergeCatalog(models)
	if len(picker.models) != 2 {
		t.Fatalf("fallback local não preservado: %+v", picker.models)
	}
}

func TestOllamaModelDiscovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("caminho inesperado: %s", r.URL.Path)
		}
		_, err := w.Write([]byte(`{"models":[{"name":"qwen3:8b","model":"qwen3:8b"}]}`))
		if err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	models, err := fetchOllamaModels(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Provider != "ollama" || models[0].ID != "qwen3:8b" {
		t.Fatalf("modelos locais incorretos: %+v", models)
	}
}
