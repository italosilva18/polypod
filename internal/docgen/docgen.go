package docgen

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// RegisterSkills registers documentation generation skills.
func RegisterSkills(reg *skill.Registry) {
	reg.Register(&skill.Skill{
		Name:        "docs_readme",
		Description: "Gerar README.md a partir da estrutura do projeto",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path": {Type: jsonschema.String, Description: "Diretorio do projeto (default: .)"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			path := args["path"]
			if path == "" {
				path = "."
			}
			info := collectProjectInfo(path)
			return fmt.Sprintf("[DOC_GENERATE type=readme]\n\n%s\n\nGere um README.md completo para este projeto.", info), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "docs_changelog",
		Description: "Gerar changelog a partir do historico git",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"since": {Type: jsonschema.String, Description: "Tag ou commit inicial (ex: v1.0.0)"},
				"format": {Type: jsonschema.String, Description: "Formato: keepachangelog (default), conventional"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			since := args["since"]
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			logArgs := []string{"log", "--oneline", "--format=%h %s (%an, %ad)", "--date=short"}
			if since != "" {
				logArgs = append(logArgs, since+"..HEAD")
			} else {
				logArgs = append(logArgs, "-50")
			}

			cmd := exec.CommandContext(ctx, "git", logArgs...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return "", fmt.Errorf("git log: %w", err)
			}

			return fmt.Sprintf("[DOC_GENERATE type=changelog since=%s]\n\nCommits:\n%s\n\nGere um CHANGELOG.md categorizado (Added, Changed, Fixed, Removed) seguindo Keep a Changelog.", since, string(out)), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "docs_api",
		Description: "Gerar documentacao de API a partir do codigo fonte",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path":   {Type: jsonschema.String, Description: "Arquivo ou diretorio"},
				"format": {Type: jsonschema.String, Description: "Formato: markdown (default), openapi"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			path := args["path"]
			if path == "" {
				path = "."
			}

			var content string
			info, err := os.Stat(path)
			if err != nil {
				return "", err
			}

			if info.IsDir() {
				// Read all Go/Python/JS files
				var files []string
				filepath.Walk(path, func(p string, i os.FileInfo, err error) error {
					if err != nil || i.IsDir() {
						return nil
					}
					ext := filepath.Ext(p)
					if ext == ".go" || ext == ".py" || ext == ".ts" || ext == ".js" {
						files = append(files, p)
					}
					return nil
				})
				for _, f := range files {
					data, _ := os.ReadFile(f)
					content += fmt.Sprintf("\n=== %s ===\n%s\n", f, string(data))
					if len(content) > 20000 {
						content += "\n... [truncado]"
						break
					}
				}
			} else {
				data, err := os.ReadFile(path)
				if err != nil {
					return "", err
				}
				content = string(data)
			}

			format := args["format"]
			if format == "" {
				format = "markdown"
			}

			return fmt.Sprintf("[DOC_GENERATE type=api format=%s]\n\nCodigo:\n%s\n\nGere documentacao de API detalhada com: funcoes publicas, parametros, retornos, exemplos de uso.", format, content), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "docs_godoc",
		Description: "Gerar comentarios godoc para funcoes/tipos sem documentacao",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path": {Type: jsonschema.String, Description: "Arquivo Go"},
			},
			Required: []string{"path"},
		},
		Execute: func(args map[string]string) (string, error) {
			data, err := os.ReadFile(args["path"])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("[DOC_GENERATE type=godoc]\n\nArquivo: %s\n\n```go\n%s\n```\n\nAdicione comentarios godoc (// FunctionName ...) para todas as funcoes e tipos exportados que nao tem comentario.", args["path"], string(data)), nil
		},
	})
}

func collectProjectInfo(path string) string {
	var sb strings.Builder

	// Check project files
	checks := []struct {
		file, desc string
	}{
		{"go.mod", "Go"}, {"package.json", "Node.js"}, {"Cargo.toml", "Rust"},
		{"pyproject.toml", "Python"}, {"Makefile", "Makefile"}, {"Dockerfile", "Docker"},
		{"docker-compose.yml", "Docker Compose"}, {".github", "GitHub Actions"},
	}

	sb.WriteString("## Projeto detectado\n\n")
	for _, c := range checks {
		if _, err := os.Stat(filepath.Join(path, c.file)); err == nil {
			fmt.Fprintf(&sb, "- %s: %s\n", c.desc, c.file)
		}
	}

	// List top-level structure
	entries, _ := os.ReadDir(path)
	sb.WriteString("\n## Estrutura\n\n")
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		t := "arquivo"
		if e.IsDir() {
			t = "dir"
		}
		fmt.Fprintf(&sb, "- %s (%s)\n", e.Name(), t)
	}

	return sb.String()
}
