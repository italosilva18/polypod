package modelpicker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ModelInfo represents an available model.
type ModelInfo struct {
	Name     string
	Provider string
	Size     string // "7B", "70B", etc.
	Active   bool   // currently selected
}

// ListOllamaModels fetches available models from Ollama.
func ListOllamaModels(baseURL string) ([]ModelInfo, error) {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", baseURL+"/api/tags", nil)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama nao disponivel: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Models []struct {
			Name    string `json:"name"`
			Size    int64  `json:"size"`
			Details struct {
				ParameterSize string `json:"parameter_size"`
			} `json:"details"`
		} `json:"models"`
	}

	data, _ := io.ReadAll(resp.Body)
	json.Unmarshal(data, &result)

	var models []ModelInfo
	for _, m := range result.Models {
		models = append(models, ModelInfo{
			Name:     m.Name,
			Provider: "ollama",
			Size:     m.Details.ParameterSize,
		})
	}
	return models, nil
}

// ListConfiguredModels returns models from the config.
func ListConfiguredModels(currentModel, currentProvider string) []ModelInfo {
	// Known models per provider
	knownModels := map[string][]string{
		"deepseek":  {"deepseek-chat", "deepseek-coder", "deepseek-reasoner"},
		"openai":    {"gpt-4o", "gpt-4o-mini", "gpt-4-turbo", "gpt-3.5-turbo"},
		"anthropic": {"claude-3-5-sonnet-latest", "claude-3-haiku-20240307", "claude-3-opus-latest"},
		"google":    {"gemini-1.5-pro", "gemini-1.5-flash", "gemini-1.5-flash-8b"},
	}

	var models []ModelInfo
	for provider, names := range knownModels {
		for _, name := range names {
			models = append(models, ModelInfo{
				Name:     name,
				Provider: provider,
				Active:   name == currentModel && provider == currentProvider,
			})
		}
	}

	sort.Slice(models, func(i, j int) bool {
		if models[i].Provider != models[j].Provider {
			return models[i].Provider < models[j].Provider
		}
		return models[i].Name < models[j].Name
	})

	return models
}

// FormatModelList formats models for display.
func FormatModelList(models []ModelInfo, currentModel string) string {
	var sb strings.Builder
	sb.WriteString("## Modelos disponiveis\n\n")

	currentProvider := ""
	for _, m := range models {
		if m.Provider != currentProvider {
			if currentProvider != "" {
				sb.WriteByte('\n')
			}
			fmt.Fprintf(&sb, "### %s\n", m.Provider)
			currentProvider = m.Provider
		}

		marker := "  "
		if m.Active || m.Name == currentModel {
			marker = "> "
		}

		size := ""
		if m.Size != "" {
			size = fmt.Sprintf(" (%s)", m.Size)
		}

		fmt.Fprintf(&sb, "%s%s%s\n", marker, m.Name, size)
	}

	return sb.String()
}
