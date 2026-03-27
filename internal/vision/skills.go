package vision

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// RegisterSkills registers vision/image analysis skills.
func RegisterSkills(reg *skill.Registry) {
	reg.Register(&skill.Skill{
		Name:        "analyze_image",
		Description: "Analisar uma imagem local (descrever conteudo, extrair texto, etc)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path":   {Type: jsonschema.String, Description: "Caminho da imagem"},
				"prompt": {Type: jsonschema.String, Description: "O que analisar na imagem (default: descreva a imagem)"},
			},
			Required: []string{"path"},
		},
		Execute: func(args map[string]string) (string, error) {
			path := args["path"]
			if _, err := os.Stat(path); err != nil {
				return "", fmt.Errorf("imagem nao encontrada: %s", path)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			b64 := base64.StdEncoding.EncodeToString(data)
			mime := detectMime(path)
			prompt := args["prompt"]
			if prompt == "" {
				prompt = "Descreva esta imagem em detalhes."
			}

			return fmt.Sprintf("[VISION_REQUEST]\nmime: %s\nbase64_length: %d\nprompt: %s\n\nIMPORTANTE: Esta skill preparou a imagem para analise. Para completar, o sistema de IA precisa processar a imagem via provider com suporte a vision (GPT-4o, Claude, Gemini, LLaVA).", mime, len(b64), prompt), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "screenshot",
		Description: "Capturar screenshot da tela",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"output": {Type: jsonschema.String, Description: "Caminho do arquivo (default: /tmp/polypod_screenshot.png)"},
				"region": {Type: jsonschema.String, Description: "Regiao: 'full' (default), 'window', ou WxH+X+Y"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			output := args["output"]
			if output == "" {
				output = "/tmp/polypod_screenshot.png"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			// Try various screenshot tools
			tools := []struct {
				name string
				args []string
			}{
				{"scrot", []string{output}},
				{"import", []string{"-window", "root", output}},
				{"gnome-screenshot", []string{"-f", output}},
				{"maim", []string{output}},
			}

			for _, t := range tools {
				if _, err := exec.LookPath(t.name); err == nil {
					cmd := exec.CommandContext(ctx, t.name, t.args...)
					if err := cmd.Run(); err == nil {
						return fmt.Sprintf("Screenshot salvo: %s", output), nil
					}
				}
			}
			return "", fmt.Errorf("nenhuma ferramenta de screenshot disponivel (instale scrot, maim ou imagemagick)")
		},
	})

	reg.Register(&skill.Skill{
		Name:        "image_info",
		Description: "Mostrar metadados de uma imagem (tamanho, formato, dimensoes)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path": {Type: jsonschema.String, Description: "Caminho da imagem"},
			},
			Required: []string{"path"},
		},
		Execute: func(args map[string]string) (string, error) {
			path := args["path"]
			info, err := os.Stat(path)
			if err != nil {
				return "", fmt.Errorf("arquivo nao encontrado: %s", path)
			}

			result := fmt.Sprintf("Arquivo: %s\nTamanho: %d bytes\nFormato: %s\n",
				path, info.Size(), strings.TrimPrefix(filepath.Ext(path), "."))

			// Try to get dimensions with ImageMagick identify
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := exec.LookPath("identify"); err == nil {
				cmd := exec.CommandContext(ctx, "identify", "-format", "%wx%h", path)
				out, err := cmd.Output()
				if err == nil {
					result += fmt.Sprintf("Dimensoes: %s\n", strings.TrimSpace(string(out)))
				}
			}

			return result, nil
		},
	})
}

func detectMime(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	mimes := map[string]string{
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".gif":  "image/gif",
		".webp": "image/webp",
		".bmp":  "image/bmp",
		".svg":  "image/svg+xml",
	}
	if m, ok := mimes[ext]; ok {
		return m
	}
	// Try to detect from content
	f, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	return http.DetectContentType(buf[:n])
}
