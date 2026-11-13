package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/costa/polypod/internal/chatgptauth"
)

const modelCatalogURL = "https://models.dev/api.json"

type modelCatalogMsg struct {
	picker *modelPickerModel
	models []ModelInfo
	err    error
}

type catalogModel struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	ToolCall bool   `json:"tool_call"`
	Limit    struct {
		Context int `json:"context"`
	} `json:"limit"`
	Cost struct {
		Input  float64 `json:"input"`
		Output float64 `json:"output"`
	} `json:"cost"`
}

type catalogProvider struct {
	Models map[string]catalogModel `json:"models"`
}

func fetchModelCatalog(ctx context.Context, url string, providers []ProviderOption) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 Polypod/0.6")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catálogo: HTTP %d", resp.StatusCode)
	}
	allowed := make(map[string]bool, len(providers))
	for _, p := range providers {
		allowed[p.Name] = true
	}
	var catalog map[string]catalogProvider
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&catalog); err != nil {
		return nil, err
	}
	var result []ModelInfo
	for providerName, provider := range catalog {
		if !allowed[providerName] {
			continue
		}
		for id, entry := range provider.Models {
			if entry.Status == "deprecated" || entry.Status == "unavailable" {
				continue
			}
			if entry.ID != "" {
				id = entry.ID
			}
			result = append(result, ModelInfo{ID: id, Provider: providerName, Display: entry.Name,
				MaxContext: entry.Limit.Context, InputCost: entry.Cost.Input, OutputCost: entry.Cost.Output,
				SupportsTools: entry.ToolCall})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Provider != result[j].Provider {
			return result[i].Provider < result[j].Provider
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func fetchOllamaModels(ctx context.Context, baseURL string) ([]ModelInfo, error) {
	if !strings.Contains(baseURL, "://") {
		baseURL = "http://" + baseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Ollama: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return nil, err
	}
	result := make([]ModelInfo, 0, len(body.Models))
	for _, model := range body.Models {
		id := model.Model
		if id == "" {
			id = model.Name
		}
		if id != "" {
			result = append(result, ModelInfo{ID: id, Display: id, Provider: "ollama", SupportsTools: true})
		}
	}
	return result, nil
}

func loadModelCatalog(picker *modelPickerModel) tea.Cmd {
	providers := append([]ProviderOption(nil), picker.providers...)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		models, err := fetchModelCatalog(ctx, modelCatalogURL, providers)
		cancel()
		for _, option := range providers {
			if option.Name != "chatgpt" || !option.Available {
				continue
			}
			manager := chatgptauth.NewManager(chatgptauth.DefaultCredentialPath())
			chatCtx, chatCancel := context.WithTimeout(context.Background(), 5*time.Second)
			chatModels, chatErr := manager.Models(chatCtx)
			chatCancel()
			if chatErr == nil {
				for _, model := range chatModels {
					models = append(models, ModelInfo{ID: model.Name(), Display: model.Name(),
						Provider: "chatgpt", SupportsTools: true})
				}
				if len(chatModels) > 0 {
					err = nil
				}
			}
			break
		}
		for _, option := range providers {
			if option.Name != "ollama" {
				continue
			}
			baseURL := strings.TrimSpace(option.BaseURL)
			if baseURL == "" {
				baseURL = strings.TrimSpace(os.Getenv("OLLAMA_HOST"))
			}
			if baseURL == "" {
				baseURL = "http://127.0.0.1:11434"
			}
			ollamaCtx, ollamaCancel := context.WithTimeout(context.Background(), time.Second)
			localModels, localErr := fetchOllamaModels(ollamaCtx, baseURL)
			ollamaCancel()
			if localErr == nil {
				models = append(models, localModels...)
				if len(localModels) > 0 {
					err = nil
				}
			}
			break
		}
		return modelCatalogMsg{picker: picker, models: models, err: err}
	}
}

func (m *modelPickerModel) mergeCatalog(models []ModelInfo) {
	index := make(map[string]int, len(m.models))
	for i, candidate := range m.models {
		index[candidate.Provider+"\x00"+candidate.ID] = i
	}
	for _, candidate := range models {
		key := candidate.Provider + "\x00" + candidate.ID
		if i, found := index[key]; found {
			if candidate.Quality == "" {
				candidate.Quality = m.models[i].Quality
			}
			if candidate.Speed == "" {
				candidate.Speed = m.models[i].Speed
			}
			m.models[i] = candidate
		} else {
			index[key] = len(m.models)
			m.models = append(m.models, candidate)
		}
	}
	m.matches = m.filter(m.query)
	if m.selected >= len(m.matches) {
		m.selected = max(0, len(m.matches)-1)
	}
}
