package models

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultModelsDevURL is the public models.dev capability specification feed.
	DefaultModelsDevURL = "https://models.dev/api.json"
	modelsDevCacheFile  = "models-dev-cache.json"
)

var (
	catalogMu     sync.RWMutex
	dynamicModels = make(map[string]*ModelCapabilities)
)

// modelsDevProvider represents one provider entry in the models.dev JSON feed.
type modelsDevProvider struct {
	ID     string                    `json:"id"`
	Name   string                    `json:"name"`
	Models map[string]modelsDevEntry `json:"models"`
}

type modelsDevEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Attachment  bool   `json:"attachment"`
	Reasoning   bool   `json:"reasoning"`
	ToolCall    bool   `json:"tool_call"`
	Temperature bool   `json:"temperature"`
	Modalities  struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"modalities"`
	Cost struct {
		Input     float64 `json:"input"`
		Output    float64 `json:"output"`
		CacheRead float64 `json:"cache_read"`
	} `json:"cost"`
	Limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
}

// LoadModelsDevJSON parses a models.dev-compatible JSON payload and overlays
// its model capability profiles onto the runtime catalog in a thread-safe way.
func LoadModelsDevJSON(raw []byte) (int, error) {
	var feed map[string]modelsDevProvider
	if err := json.Unmarshal(raw, &feed); err != nil {
		return 0, fmt.Errorf("decodificando catálogo models.dev: %w", err)
	}

	loaded := 0
	catalogMu.Lock()
	defer catalogMu.Unlock()

	for provKey, prov := range feed {
		provName := strings.ToLower(strings.TrimSpace(prov.ID))
		if provName == "" {
			provName = strings.ToLower(strings.TrimSpace(provKey))
		}
		for modelKey, m := range prov.Models {
			id := strings.TrimSpace(m.ID)
			if id == "" {
				id = strings.TrimSpace(modelKey)
			}
			if id == "" || m.Limit.Context <= 0 {
				continue
			}
			vision := m.Attachment
			for _, mod := range m.Modalities.Input {
				modLower := strings.ToLower(strings.TrimSpace(mod))
				if modLower == "image" || modLower == "video" || modLower == "pdf" {
					vision = true
					break
				}
			}
			maxOut := m.Limit.Output
			if maxOut <= 0 {
				maxOut = 8192
			}
			display := strings.TrimSpace(m.Name)
			if display == "" {
				display = id
			}
			capEntry := &ModelCapabilities{
				ID:                id,
				Provider:          provName,
				DisplayName:       display,
				MaxContextTokens:  m.Limit.Context,
				MaxOutputTokens:   maxOut,
				SupportsVision:    vision,
				SupportsTools:     m.ToolCall,
				SupportsStreaming: true,
				SupportsThinking:  m.Reasoning,
				SupportsCaching:   m.Cost.CacheRead > 0,
				SupportsJSON:      true,
				InputPricePer1M:   m.Cost.Input,
				OutputPricePer1M:  m.Cost.Output,
				CachePricePer1M:   m.Cost.CacheRead,
				Speed:             "medium",
				Quality:           "high",
				RecommendedFor:    []string{"coding"},
			}
			dynamicModels[strings.ToLower(id)] = capEntry
			if !strings.Contains(id, "/") && provName != "" {
				dynamicModels[provName+"/"+strings.ToLower(id)] = capEntry
			}
			loaded++
		}
	}
	return loaded, nil
}

// RefreshFromModelsDev loads cached models.dev capabilities from cacheDir first,
// and optionally refreshes from url unless POLYPOD_MODELS_DEV_REFRESH=off.
func RefreshFromModelsDev(ctx context.Context, cacheDir, url string) (int, error) {
	if strings.TrimSpace(cacheDir) != "" {
		cachePath := filepath.Join(cacheDir, modelsDevCacheFile)
		if cached, err := os.ReadFile(cachePath); err == nil && len(cached) > 0 {
			_, _ = LoadModelsDevJSON(cached)
		}
	}

	mode := strings.ToLower(strings.TrimSpace(os.Getenv("POLYPOD_MODELS_DEV_REFRESH")))
	if mode == "off" || mode == "false" || mode == "0" {
		return DynamicModelCount(), nil
	}

	if strings.TrimSpace(url) == "" {
		url = strings.TrimSpace(os.Getenv("POLYPOD_MODELS_DEV_URL"))
	}
	if url == "" {
		url = DefaultModelsDevURL
	}

	if ctx == nil {
		ctx = context.Background()
	}
	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("models.dev HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return 0, err
	}
	count, err := LoadModelsDevJSON(body)
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(cacheDir) != "" {
		_ = os.MkdirAll(cacheDir, 0700)
		_ = os.WriteFile(filepath.Join(cacheDir, modelsDevCacheFile), body, 0600)
	}
	return count, nil
}

// DynamicModelCount returns the number of dynamically loaded model entries.
func DynamicModelCount() int {
	catalogMu.RLock()
	defer catalogMu.RUnlock()
	return len(dynamicModels)
}

// ResetDynamicModelsForTest clears dynamically overlaid models in unit tests.
func ResetDynamicModelsForTest() {
	catalogMu.Lock()
	clear(dynamicModels)
	catalogMu.Unlock()
}

// BestVisionModelForProvider returns the recommended multimodal vision model
// on the same provider when the user's active coding model is text-only.
func BestVisionModelForProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "qwen", "dashscope":
		return "qwen3-vl-plus"
	case "openai":
		return "gpt-4.1"
	case "chatgpt":
		return "gpt-6.1-sol"
	case "google", "gemini":
		return "gemini-2.5-pro"
	case "anthropic", "claude":
		return "claude-sonnet-4-20250514"
	case "nvidia":
		return "nvidia/nemotron-3.5-super-vl-120b-a12b"
	case "openrouter":
		return "google/gemini-2.5-flash"
	case "github":
		return "gpt-4o"
	case "mistral":
		return "mistral-large-latest"
	case "ollama":
		return "qwen2.5vl:7b"
	default:
		// Check dynamic catalog for any vision-capable model of this provider.
		p := strings.ToLower(strings.TrimSpace(provider))
		catalogMu.RLock()
		defer catalogMu.RUnlock()
		for _, m := range dynamicModels {
			if m.Provider == p && m.SupportsVision && !m.IsDeprecated {
				return m.ID
			}
		}
		return ""
	}
}
