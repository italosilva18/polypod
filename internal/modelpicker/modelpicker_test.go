package modelpicker

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

func TestListConfiguredModels_ReturnsAllKnownProviders(t *testing.T) {
	models := ListConfiguredModels("", "")

	if len(models) == 0 {
		t.Fatal("expected models, got none")
	}

	providers := make(map[string]bool)
	for _, m := range models {
		providers[m.Provider] = true
	}

	expected := []string{"deepseek", "openai", "anthropic", "google", "nvidia"}
	for _, p := range expected {
		if !providers[p] {
			t.Errorf("expected provider %q to be present", p)
		}
	}
}

func TestListConfiguredModels_TotalCount(t *testing.T) {
	models := ListConfiguredModels("", "")

	// 3 deepseek + 4 openai + 3 anthropic + 3 google + 1 nvidia = 14
	expected := 14
	if len(models) != expected {
		t.Errorf("expected %d models, got %d", expected, len(models))
	}
}

func TestListConfiguredModels_SortedByProviderThenName(t *testing.T) {
	models := ListConfiguredModels("", "")

	if !sort.SliceIsSorted(models, func(i, j int) bool {
		if models[i].Provider != models[j].Provider {
			return models[i].Provider < models[j].Provider
		}
		return models[i].Name < models[j].Name
	}) {
		t.Error("models are not sorted by provider then name")
	}
}

func TestListConfiguredModels_MarksActiveModel(t *testing.T) {
	models := ListConfiguredModels("gpt-4o", "openai")

	activeCount := 0
	var active ModelInfo
	for _, m := range models {
		if m.Active {
			activeCount++
			active = m
		}
	}

	if activeCount != 1 {
		t.Fatalf("expected exactly 1 active model, got %d", activeCount)
	}
	if active.Name != "gpt-4o" || active.Provider != "openai" {
		t.Errorf("expected active to be gpt-4o/openai, got %s/%s", active.Name, active.Provider)
	}
}

func TestListConfiguredModels_NoMatchMeansNoneActive(t *testing.T) {
	models := ListConfiguredModels("nonexistent-model", "nonexistent-provider")

	for _, m := range models {
		if m.Active {
			t.Errorf("did not expect any model to be active, but %s/%s was", m.Name, m.Provider)
		}
	}
}

func TestListConfiguredModels_NameMatchButProviderMismatch(t *testing.T) {
	// gpt-4o exists only in openai, so matching it with wrong provider means none active
	models := ListConfiguredModels("gpt-4o", "anthropic")

	for _, m := range models {
		if m.Active {
			t.Errorf("expected no active model (provider mismatch), got %s/%s", m.Name, m.Provider)
		}
	}
}

func TestFormatModelList_IncludesHeaderAndProviders(t *testing.T) {
	models := ListConfiguredModels("", "")
	out := FormatModelList(models, "")

	if !strings.Contains(out, "## Modelos disponiveis") {
		t.Error("expected header in output")
	}
	for _, p := range []string{"deepseek", "openai", "anthropic", "google", "nvidia"} {
		if !strings.Contains(out, "### "+p) {
			t.Errorf("expected provider section %q in output", p)
		}
	}
}

func TestFormatModelList_MarksCurrentModel(t *testing.T) {
	models := ListConfiguredModels("", "")
	out := FormatModelList(models, "gpt-4o")

	if !strings.Contains(out, "> gpt-4o") {
		t.Error("expected current model gpt-4o to be marked with '> '")
	}

	// Non-current models should have "  " marker, not "> "
	lines := strings.Split(out, "\n")
	markedCount := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "> ") {
			markedCount++
		}
	}
	if markedCount != 1 {
		t.Errorf("expected exactly 1 marked line, got %d", markedCount)
	}
}

func TestFormatModelList_IncludesSizeWhenPresent(t *testing.T) {
	models := []ModelInfo{
		{Name: "llama3", Provider: "ollama", Size: "8B"},
		{Name: "gpt-4o", Provider: "openai", Size: ""},
	}
	out := FormatModelList(models, "")

	if !strings.Contains(out, "llama3 (8B)") {
		t.Error("expected llama3 to include size (8B)")
	}
	if strings.Contains(out, "gpt-4o (") {
		t.Error("did not expect gpt-4o to show size parentheses when empty")
	}
}

func TestFormatModelList_EmptyInput(t *testing.T) {
	out := FormatModelList(nil, "")

	if !strings.Contains(out, "## Modelos disponiveis") {
		t.Error("expected header even with empty input")
	}
	if strings.Contains(out, "### ") {
		t.Error("did not expect any provider sections with empty input")
	}
}

func TestListOllamaModels_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("expected path /api/tags, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"models": [
				{"name": "llama3:8b", "size": 4000000000, "details": {"parameter_size": "8B"}},
				{"name": "mistral:7b", "size": 3000000000, "details": {"parameter_size": "7B"}}
			]
		}`))
	}))
	defer server.Close()

	models, err := ListOllamaModels(server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].Name != "llama3:8b" || models[0].Provider != "ollama" || models[0].Size != "8B" {
		t.Errorf("unexpected first model: %+v", models[0])
	}
	if models[1].Name != "mistral:7b" || models[1].Size != "7B" {
		t.Errorf("unexpected second model: %+v", models[1])
	}
}

func TestListOllamaModels_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"models": []}`))
	}))
	defer server.Close()

	models, err := ListOllamaModels(server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 0 {
		t.Errorf("expected 0 models, got %d", len(models))
	}
}

func TestListOllamaModels_UnreachableServer(t *testing.T) {
	// Use an address that should immediately refuse connections
	_, err := ListOllamaModels("http://127.0.0.1:1")
	if err == nil {
		t.Error("expected error for unreachable server, got nil")
	}
	if !strings.Contains(err.Error(), "ollama nao disponivel") {
		t.Errorf("expected 'ollama nao disponivel' in error, got: %v", err)
	}
}
