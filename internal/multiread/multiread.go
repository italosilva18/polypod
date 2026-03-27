package multiread

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

const maxTotalBytes = 512 * 1024 // 512KB total limit

// RegisterSkills registers multi-file read skills.
func RegisterSkills(reg *skill.Registry) {
	reg.Register(&skill.Skill{
		Name:        "read_files",
		Description: "Ler multiplos arquivos de uma vez (lista ou glob pattern)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"paths":   {Type: jsonschema.String, Description: "Caminhos separados por virgula OU glob pattern (ex: *.go, internal/**/*.go)"},
				"max_lines": {Type: jsonschema.String, Description: "Maximo de linhas por arquivo (default: 200)"},
			},
			Required: []string{"paths"},
		},
		Execute: func(args map[string]string) (string, error) {
			input := args["paths"]
			maxLines := 200
			if ml := args["max_lines"]; ml != "" {
				fmt.Sscanf(ml, "%d", &maxLines)
			}

			var paths []string

			// Check if it's a glob pattern or comma-separated list
			if strings.ContainsAny(input, "*?[") {
				// Glob pattern
				matches, err := filepath.Glob(input)
				if err != nil {
					return "", fmt.Errorf("glob invalido: %w", err)
				}
				paths = matches
			} else {
				// Comma-separated
				for _, p := range strings.Split(input, ",") {
					p = strings.TrimSpace(p)
					if p != "" {
						paths = append(paths, p)
					}
				}
			}

			if len(paths) == 0 {
				return "Nenhum arquivo encontrado.", nil
			}

			var sb strings.Builder
			totalBytes := 0

			for _, path := range paths {
				info, err := os.Stat(path)
				if err != nil || info.IsDir() {
					continue
				}

				data, err := os.ReadFile(path)
				if err != nil {
					fmt.Fprintf(&sb, "\n=== %s (erro: %v) ===\n", path, err)
					continue
				}

				content := string(data)
				lines := strings.Split(content, "\n")
				if len(lines) > maxLines {
					lines = lines[:maxLines]
					content = strings.Join(lines, "\n") + fmt.Sprintf("\n... [truncado, %d linhas total]", len(strings.Split(string(data), "\n")))
				}

				if totalBytes+len(content) > maxTotalBytes {
					fmt.Fprintf(&sb, "\n=== %s (ignorado: limite de %dKB atingido) ===\n", path, maxTotalBytes/1024)
					continue
				}

				fmt.Fprintf(&sb, "\n=== %s (%d bytes, %d linhas) ===\n%s\n", path, info.Size(), len(lines), content)
				totalBytes += len(content)
			}

			fmt.Fprintf(&sb, "\n--- %d arquivo(s) lido(s), %d bytes total ---\n", len(paths), totalBytes)
			return sb.String(), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "read_dir",
		Description: "Ler todos os arquivos de um diretorio (nao-recursivo)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path":      {Type: jsonschema.String, Description: "Caminho do diretorio"},
				"extension": {Type: jsonschema.String, Description: "Filtrar por extensao (ex: .go, .py)"},
				"max_lines": {Type: jsonschema.String, Description: "Maximo de linhas por arquivo (default: 100)"},
			},
			Required: []string{"path"},
		},
		Execute: func(args map[string]string) (string, error) {
			dirPath := args["path"]
			extension := args["extension"]
			maxLines := 100
			if ml := args["max_lines"]; ml != "" {
				fmt.Sscanf(ml, "%d", &maxLines)
			}

			entries, err := os.ReadDir(dirPath)
			if err != nil {
				return "", fmt.Errorf("lendo diretorio: %w", err)
			}

			var sb strings.Builder
			totalBytes := 0
			fileCount := 0

			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				if extension != "" && !strings.HasSuffix(e.Name(), extension) {
					continue
				}

				path := filepath.Join(dirPath, e.Name())
				data, err := os.ReadFile(path)
				if err != nil {
					continue
				}

				content := string(data)
				lines := strings.Split(content, "\n")
				if len(lines) > maxLines {
					content = strings.Join(lines[:maxLines], "\n") + "\n... [truncado]"
				}

				if totalBytes+len(content) > maxTotalBytes {
					break
				}

				info, _ := e.Info()
				fmt.Fprintf(&sb, "\n=== %s (%d bytes) ===\n%s\n", path, info.Size(), content)
				totalBytes += len(content)
				fileCount++
			}

			fmt.Fprintf(&sb, "\n--- %d arquivo(s), %d bytes ---\n", fileCount, totalBytes)
			return sb.String(), nil
		},
	})
}
