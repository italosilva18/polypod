package depsgraph

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

// RegisterSkills registers dependency graph skills.
func RegisterSkills(reg *skill.Registry) {
	reg.Register(&skill.Skill{
		Name:        "deps_tree",
		Description: "Gerar arvore de dependencias do projeto (Go, Node, Python)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path":   {Type: jsonschema.String, Description: "Diretorio do projeto (default: .)"},
				"depth":  {Type: jsonschema.String, Description: "Profundidade maxima (default: 3)"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			path := args["path"]
			if path == "" {
				path = "."
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Go
			if fileExists(filepath.Join(path, "go.mod")) {
				cmd := exec.CommandContext(ctx, "go", "mod", "graph")
				out, err := cmd.CombinedOutput()
				if err != nil {
					return string(out), nil
				}
				return formatGoGraph(string(out)), nil
			}

			// Node
			if fileExists(filepath.Join(path, "package.json")) {
				cmd := exec.CommandContext(ctx, "npm", "ls", "--all", "--depth=3")
				out, _ := cmd.CombinedOutput()
				return "## Dependencias Node\n\n```\n" + string(out) + "\n```", nil
			}

			// Python
			if fileExists(filepath.Join(path, "requirements.txt")) || fileExists(filepath.Join(path, "pyproject.toml")) {
				cmd := exec.CommandContext(ctx, "pip", "list", "--format=columns")
				out, _ := cmd.CombinedOutput()
				return "## Dependencias Python\n\n```\n" + string(out) + "\n```", nil
			}

			return "Tipo de projeto nao reconhecido (suportados: Go, Node, Python).", nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "deps_circular",
		Description: "Detectar dependencias circulares no projeto",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path": {Type: jsonschema.String, Description: "Diretorio (default: .)"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			path := args["path"]
			if path == "" {
				path = "."
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Go: use go vet for import cycles
			if fileExists(filepath.Join(path, "go.mod")) {
				cmd := exec.CommandContext(ctx, "go", "build", "./...")
				out, err := cmd.CombinedOutput()
				output := string(out)
				if err != nil && strings.Contains(output, "import cycle") {
					return "## Dependencias circulares detectadas!\n\n```\n" + output + "\n```", nil
				}
				return "Nenhuma dependencia circular encontrada.", nil
			}

			// Node: use madge if available
			if _, err := exec.LookPath("npx"); err == nil {
				cmd := exec.CommandContext(ctx, "npx", "madge", "--circular", path)
				out, _ := cmd.CombinedOutput()
				output := strings.TrimSpace(string(out))
				if output == "" {
					return "Nenhuma dependencia circular encontrada.", nil
				}
				return "## Dependencias circulares\n\n```\n" + output + "\n```", nil
			}

			return "Ferramenta de deteccao nao disponivel.", nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "deps_graph_image",
		Description: "Gerar imagem do grafo de dependencias (requer Graphviz)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"output": {Type: jsonschema.String, Description: "Caminho da imagem (default: deps.svg)"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			output := args["output"]
			if output == "" {
				output = "deps.svg"
			}

			if _, err := exec.LookPath("dot"); err != nil {
				return "", fmt.Errorf("graphviz (dot) nao encontrado. Instale: apt install graphviz")
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Generate DOT from go mod graph
			cmd := exec.CommandContext(ctx, "go", "mod", "graph")
			out, err := cmd.CombinedOutput()
			if err != nil {
				return "", fmt.Errorf("go mod graph: %w", err)
			}

			dot := goGraphToDot(string(out))
			dotFile := output + ".dot"
			os.WriteFile(dotFile, []byte(dot), 0644)

			// Render
			ext := filepath.Ext(output)
			format := "svg"
			if ext == ".png" {
				format = "png"
			}

			cmd = exec.CommandContext(ctx, "dot", "-T"+format, "-o", output, dotFile)
			_, err = cmd.CombinedOutput()
			os.Remove(dotFile)

			if err != nil {
				return "", fmt.Errorf("dot: %w", err)
			}

			return fmt.Sprintf("Grafo de dependencias gerado: %s", output), nil
		},
	})
}

func formatGoGraph(graph string) string {
	var sb strings.Builder
	sb.WriteString("## Dependencias Go\n\n")

	deps := make(map[string][]string)
	for _, line := range strings.Split(strings.TrimSpace(graph), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 {
			from := simplifyModule(parts[0])
			to := simplifyModule(parts[1])
			deps[from] = append(deps[from], to)
		}
	}

	for mod, children := range deps {
		fmt.Fprintf(&sb, "- **%s**\n", mod)
		for _, c := range children {
			if len(children) > 10 {
				fmt.Fprintf(&sb, "  - %s ... (%d total)\n", c, len(children))
				break
			}
			fmt.Fprintf(&sb, "  - %s\n", c)
		}
	}
	return sb.String()
}

func goGraphToDot(graph string) string {
	var sb strings.Builder
	sb.WriteString("digraph deps {\n  rankdir=LR;\n  node [shape=box, fontsize=10];\n")

	for _, line := range strings.Split(strings.TrimSpace(graph), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 {
			from := quote(simplifyModule(parts[0]))
			to := quote(simplifyModule(parts[1]))
			fmt.Fprintf(&sb, "  %s -> %s;\n", from, to)
		}
	}
	sb.WriteString("}\n")
	return sb.String()
}

func simplifyModule(mod string) string {
	// Remove version
	if idx := strings.Index(mod, "@"); idx > 0 {
		mod = mod[:idx]
	}
	return mod
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
