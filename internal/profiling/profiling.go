package profiling

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// RegisterSkills registers performance profiling skills.
func RegisterSkills(reg *skill.Registry) {
	reg.Register(&skill.Skill{
		Name:        "profile_go",
		Description: "Executar profiling de CPU/memoria em aplicacao Go",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"binary":   {Type: jsonschema.String, Description: "Caminho do binario ou 'go test' para perfil de testes"},
				"type":     {Type: jsonschema.String, Description: "Tipo: cpu (default), mem, goroutine, block"},
				"duration": {Type: jsonschema.String, Description: "Duracao em segundos (default: 30)"},
			},
			Required: []string{"binary"},
		},
		Execute: func(args map[string]string) (string, error) {
			profType := args["type"]
			if profType == "" {
				profType = "cpu"
			}
			duration := args["duration"]
			if duration == "" {
				duration = "30"
			}
			binary := args["binary"]

			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			profFile := fmt.Sprintf("/tmp/polypod_%s.prof", profType)

			if binary == "go test" {
				// Profile tests
				cmd := exec.CommandContext(ctx, "go", "test", "-"+profType+"profile="+profFile, "-bench=.", "-benchtime="+duration+"s", "./...")
				out, err := cmd.CombinedOutput()
				if err != nil {
					return string(out), nil
				}
			} else {
				return "", fmt.Errorf("para binarios em execucao, use: go tool pprof http://localhost:6060/debug/pprof/%s", profType)
			}

			// Analyze profile
			cmd := exec.CommandContext(ctx, "go", "tool", "pprof", "-top", "-nodecount=20", profFile)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return "", fmt.Errorf("pprof: %w", err)
			}

			return fmt.Sprintf("## Profiling %s\n\nArquivo: %s\n\n```\n%s\n```", profType, profFile, string(out)), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "profile_analyze",
		Description: "Analisar um arquivo .prof existente e identificar hotspots",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path": {Type: jsonschema.String, Description: "Caminho do arquivo .prof"},
				"top":  {Type: jsonschema.String, Description: "Numero de hotspots (default: 15)"},
			},
			Required: []string{"path"},
		},
		Execute: func(args map[string]string) (string, error) {
			path := args["path"]
			top := args["top"]
			if top == "" {
				top = "15"
			}

			if _, err := os.Stat(path); err != nil {
				return "", fmt.Errorf("arquivo nao encontrado: %s", path)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Top functions
			cmd := exec.CommandContext(ctx, "go", "tool", "pprof", "-top", "-nodecount="+top, path)
			topOut, _ := cmd.CombinedOutput()

			// Cumulative
			cmd = exec.CommandContext(ctx, "go", "tool", "pprof", "-cum", "-top", "-nodecount="+top, path)
			cumOut, _ := cmd.CombinedOutput()

			return fmt.Sprintf("## Analise de Profile: %s\n\n### Top %s (flat)\n```\n%s\n```\n\n### Top %s (cumulative)\n```\n%s\n```\n\nPara flame graph: `go tool pprof -http=:8080 %s`",
				path, top, string(topOut), top, string(cumOut), path), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "profile_flame",
		Description: "Gerar flame graph SVG de um arquivo .prof",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path":   {Type: jsonschema.String, Description: "Caminho do .prof"},
				"output": {Type: jsonschema.String, Description: "Caminho do SVG (default: flame.svg)"},
			},
			Required: []string{"path"},
		},
		Execute: func(args map[string]string) (string, error) {
			path := args["path"]
			output := args["output"]
			if output == "" {
				output = "flame.svg"
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Try go tool pprof -svg
			cmd := exec.CommandContext(ctx, "go", "tool", "pprof", "-svg", "-output="+output, path)
			out, err := cmd.CombinedOutput()
			if err != nil {
				// Check if graphviz is needed
				if strings.Contains(string(out), "graphviz") || strings.Contains(string(out), "dot") {
					return "", fmt.Errorf("graphviz necessario: instale com 'apt install graphviz' ou 'brew install graphviz'")
				}
				return string(out), nil
			}

			return fmt.Sprintf("Flame graph gerado: %s\nAbra no browser: file://%s", output, output), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "profile_python",
		Description: "Executar profiling de script Python com cProfile",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"script": {Type: jsonschema.String, Description: "Caminho do script Python"},
				"top":    {Type: jsonschema.String, Description: "Numero de funcoes (default: 20)"},
			},
			Required: []string{"script"},
		},
		Execute: func(args map[string]string) (string, error) {
			script := args["script"]
			top := args["top"]
			if top == "" {
				top = "20"
			}

			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, "python3", "-m", "cProfile", "-s", "cumulative", script)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return string(out), nil
			}

			// Truncate to top N lines
			lines := strings.Split(string(out), "\n")
			if len(lines) > 25 {
				lines = lines[:25]
			}

			return fmt.Sprintf("## Python Profile: %s\n\n```\n%s\n```", script, strings.Join(lines, "\n")), nil
		},
	})
}
