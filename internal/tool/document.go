package tool

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/costa/polypod/internal/document"
	"github.com/costa/polypod/internal/types"
)

const (
	defaultDocumentChars = 50_000
	maxDocumentChars     = 100_000
)

// RegisterDocumentTool exposes bounded extraction from local text, PDF, Office
// and image files. Longer output is paginated by Unicode character offset.
func RegisterDocumentTool(reg *Registry) {
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "document_extract" },
		DescriptionFn: func() string {
			return "Extrair texto de documento local (PDF, Office, imagem com OCR ou texto). " +
				"Use start/max_chars para ler documentos longos em partes; OCR aceita idioma via POLYPOD_OCR_LANG."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":  map[string]any{"type": "string", "description": "Caminho do arquivo local"},
					"ocr":   map[string]any{"type": "boolean", "description": "Forçar OCR em PDF"},
					"start": map[string]any{"type": "integer", "minimum": 0, "description": "Índice do primeiro caractere Unicode"},
					"max_chars": map[string]any{"type": "integer", "minimum": 1, "maximum": maxDocumentChars,
						"description": "Máximo de caracteres nesta resposta (padrão 50000)"},
				},
				"required": []string{"path"},
			}
		},
		ValidateFn: func(args map[string]any) types.ValidationResult {
			_, _, _, _, err := documentArgs(args)
			if err != nil {
				return types.ValidationResult{Message: err.Error()}
			}
			return types.ValidationResult{Valid: true}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			path, ocr, start, maxChars, err := documentArgs(args)
			if err != nil {
				return types.ToolResult{IsError: true, Content: err.Error()}, nil
			}
			if !filepath.IsAbs(path) {
				path = filepath.Join(tc.WorkingDir, path)
			}
			path, err = filepath.Abs(path)
			if err != nil {
				return types.ToolResult{IsError: true, Content: "Caminho inválido: " + err.Error()}, nil
			}
			ctx := tc.Context
			if ctx == nil {
				ctx = context.Background()
			}
			result, err := document.Extract(ctx, path, document.Options{OCR: ocr, OCRLanguage: os.Getenv("POLYPOD_OCR_LANG")})
			if err != nil {
				return types.ToolResult{IsError: true, Content: "Não foi possível extrair o documento: " + err.Error()}, nil
			}
			runes := []rune(result.Text)
			if start > len(runes) {
				return types.ToolResult{IsError: true,
					Content: fmt.Sprintf("start=%d excede o total de %d caracteres", start, len(runes))}, nil
			}
			end := min(start+maxChars, len(runes))
			content := fmt.Sprintf("Arquivo: %s\nFormato: %s\nCaracteres: %d; trecho: %d..%d\n",
				path, result.Format, len(runes), start, end)
			if result.Pages > 0 {
				content += fmt.Sprintf("Páginas: %d; processadas: %d; OCR: %t\n",
					result.Pages, result.PageLimit, result.UsedOCR)
				if result.TruncatedPDF {
					content += "[Limite de páginas atingido; páginas seguintes não foram processadas.]\n"
				}
			}
			content += "\n" + string(runes[start:end])
			if end < len(runes) {
				content += fmt.Sprintf("\n[Continue com start=%d.]", end)
			}
			return types.ToolResult{Content: content}, nil
		},
	})
}

func documentArgs(args map[string]any) (path string, ocr bool, start, maxChars int, err error) {
	path, _ = args["path"].(string)
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false, 0, 0, fmt.Errorf("informe o caminho do documento")
	}
	ocr, _ = args["ocr"].(bool)
	start, err = integerArg(args, "start", 0, math.MaxInt32)
	if err != nil {
		return "", false, 0, 0, err
	}
	maxChars, err = integerArg(args, "max_chars", defaultDocumentChars, maxDocumentChars)
	return path, ocr, start, maxChars, err
}

func integerArg(args map[string]any, name string, defaultValue, maximum int) (int, error) {
	raw, exists := args[name]
	if !exists {
		return defaultValue, nil
	}
	var value float64
	switch v := raw.(type) {
	case float64:
		value = v
	case int:
		value = float64(v)
	default:
		return 0, fmt.Errorf("%s deve ser um inteiro", name)
	}
	minimum := 0
	if name == "max_chars" {
		minimum = 1
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || value != math.Trunc(value) || value < float64(minimum) || value > float64(maximum) {
		return 0, fmt.Errorf("%s deve estar entre %d e %d", name, minimum, maximum)
	}
	return int(value), nil
}
