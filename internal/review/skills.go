package review

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

// RegisterSkills registers code review and testing skills.
func RegisterSkills(reg *skill.Registry) {
	reg.Register(&skill.Skill{
		Name:        "code_review",
		Description: "Obter diff para revisao de codigo (staged, unstaged ou entre branches)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"target": {Type: jsonschema.String, Description: "Alvo: 'staged' (default), 'unstaged', 'branch:<nome>', ou caminho de arquivo"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			target := args["target"]
			if target == "" {
				target = "staged"
			}

			var diff string
			var err error

			switch {
			case target == "staged":
				diff, err = runCmd("git", "diff", "--cached")
			case target == "unstaged":
				diff, err = runCmd("git", "diff")
			case strings.HasPrefix(target, "branch:"):
				branch := strings.TrimPrefix(target, "branch:")
				diff, err = runCmd("git", "diff", branch+"...HEAD")
			default:
				diff, err = runCmd("git", "diff", "--", target)
			}

			if err != nil {
				return "", err
			}
			if diff == "" {
				return "Nenhuma alteracao encontrada para revisao.", nil
			}

			return fmt.Sprintf("## Diff para revisao (%s)\n\n```diff\n%s\n```\n\nAnalise este diff e identifique:\n1. Bugs potenciais\n2. Problemas de seguranca\n3. Melhorias de performance\n4. Questoes de legibilidade", target, diff), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "lint_check",
		Description: "Executar linters disponiveis no projeto",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path": {Type: jsonschema.String, Description: "Caminho (default: .)"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			path := args["path"]
			if path == "" {
				path = "."
			}

			var results []string

			// Go
			if fileExists(filepath.Join(path, "go.mod")) {
				if out, err := runCmd("go", "vet", "./..."); err == nil {
					results = append(results, "## go vet\n"+out)
				}
				if _, err := exec.LookPath("staticcheck"); err == nil {
					if out, _ := runCmd("staticcheck", "./..."); out != "" {
						results = append(results, "## staticcheck\n"+out)
					}
				}
			}

			// Node.js
			if fileExists(filepath.Join(path, "package.json")) {
				if _, err := exec.LookPath("npx"); err == nil {
					if out, _ := runCmd("npx", "eslint", ".", "--format", "compact"); out != "" {
						results = append(results, "## eslint\n"+out)
					}
				}
			}

			// Python
			if fileExists(filepath.Join(path, "requirements.txt")) || fileExists(filepath.Join(path, "pyproject.toml")) {
				if _, err := exec.LookPath("ruff"); err == nil {
					if out, _ := runCmd("ruff", "check", path); out != "" {
						results = append(results, "## ruff\n"+out)
					}
				} else if _, err := exec.LookPath("pylint"); err == nil {
					if out, _ := runCmd("pylint", path, "--score=no"); out != "" {
						results = append(results, "## pylint\n"+out)
					}
				}
			}

			if len(results) == 0 {
				return "Nenhum linter encontrado ou nenhum problema detectado.", nil
			}
			return strings.Join(results, "\n\n"), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "test_run",
		Description: "Executar testes do projeto (auto-detecta linguagem)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path":    {Type: jsonschema.String, Description: "Caminho (default: .)"},
				"verbose": {Type: jsonschema.String, Description: "'true' para output detalhado"},
				"pattern": {Type: jsonschema.String, Description: "Filtro de nome de teste"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			path := args["path"]
			if path == "" {
				path = "."
			}
			verbose := args["verbose"] == "true"
			pattern := args["pattern"]

			// Go
			if fileExists(filepath.Join(path, "go.mod")) {
				cmdArgs := []string{"test", "./..."}
				if verbose {
					cmdArgs = append(cmdArgs, "-v")
				}
				if pattern != "" {
					cmdArgs = append(cmdArgs, "-run", pattern)
				}
				return runCmd("go", cmdArgs...)
			}

			// Node.js
			if fileExists(filepath.Join(path, "package.json")) {
				cmdArgs := []string{"test"}
				if pattern != "" {
					cmdArgs = append(cmdArgs, "--", "--grep", pattern)
				}
				return runCmd("npm", cmdArgs...)
			}

			// Python
			if fileExists(filepath.Join(path, "pytest.ini")) || fileExists(filepath.Join(path, "pyproject.toml")) {
				cmdArgs := []string{"-m", "pytest"}
				if verbose {
					cmdArgs = append(cmdArgs, "-v")
				}
				if pattern != "" {
					cmdArgs = append(cmdArgs, "-k", pattern)
				}
				return runCmd("python3", cmdArgs...)
			}

			return "Tipo de projeto nao reconhecido. Suportados: Go (go.mod), Node (package.json), Python (pytest.ini/pyproject.toml).", nil
		},
	})
}

func runCmd(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(out))
	if ctx.Err() == context.DeadlineExceeded {
		return result + "\n[timeout: 60s]", nil
	}
	if err != nil {
		return result, nil // return output even on non-zero exit
	}
	return result, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
