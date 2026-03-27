package imagine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// RegisterSkills registers image generation skills.
func RegisterSkills(reg *skill.Registry, apiKey, provider string) {
	reg.Register(&skill.Skill{
		Name:        "imagine",
		Description: "Gerar imagem a partir de descricao textual (DALL-E, LocalAI, Stable Diffusion)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"prompt":   {Type: jsonschema.String, Description: "Descricao da imagem a gerar"},
				"size":     {Type: jsonschema.String, Description: "Tamanho: 256x256, 512x512, 1024x1024 (default)"},
				"output":   {Type: jsonschema.String, Description: "Caminho de saida (default: polypod-images/<timestamp>.png)"},
				"provider": {Type: jsonschema.String, Description: "Provider: openai (default), localai"},
			},
			Required: []string{"prompt"},
		},
		Execute: func(args map[string]string) (string, error) {
			prompt := args["prompt"]
			size := args["size"]
			if size == "" {
				size = "1024x1024"
			}
			output := args["output"]
			if output == "" {
				os.MkdirAll("polypod-images", 0755)
				output = filepath.Join("polypod-images", fmt.Sprintf("%d.png", time.Now().Unix()))
			}
			prov := args["provider"]
			if prov == "" {
				prov = provider
			}

			var imgURL string
			var err error

			switch prov {
			case "openai":
				imgURL, err = generateOpenAI(apiKey, prompt, size)
			case "localai":
				imgURL, err = generateLocalAI(prompt, size)
			default:
				imgURL, err = generateOpenAI(apiKey, prompt, size)
			}

			if err != nil {
				return "", err
			}

			// Download image
			if err := downloadImage(imgURL, output); err != nil {
				return "", fmt.Errorf("baixando imagem: %w", err)
			}

			return fmt.Sprintf("Imagem gerada: %s\nPrompt: %s\nTamanho: %s", output, prompt, size), nil
		},
	})
}

func generateOpenAI(apiKey, prompt, size string) (string, error) {
	if apiKey == "" {
		return "", fmt.Errorf("API key do OpenAI necessaria para geracao de imagem")
	}

	body, _ := json.Marshal(map[string]any{
		"model":  "dall-e-3",
		"prompt": prompt,
		"size":   size,
		"n":      1,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("openai images: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("openai HTTP %d: %s", resp.StatusCode, string(b))
	}

	var result struct {
		Data []struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result.Data) == 0 {
		return "", fmt.Errorf("nenhuma imagem gerada")
	}
	return result.Data[0].URL, nil
}

func generateLocalAI(prompt, size string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"prompt": prompt,
		"size":   size,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "POST", "http://localhost:8080/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("localai: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Data []struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result.Data) == 0 {
		return "", fmt.Errorf("nenhuma imagem gerada (localai)")
	}
	return result.Data[0].URL, nil
}

func downloadImage(url, output string) error {
	if strings.HasPrefix(url, "data:") {
		// Base64 data URL — not a download
		return fmt.Errorf("base64 output nao suportado, use URL")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	os.MkdirAll(filepath.Dir(output), 0755)
	f, err := os.Create(output)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}
