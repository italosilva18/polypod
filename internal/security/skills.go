package security

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|secret|password|token|credential)\s*[:=]\s*['"][^'"]{8,}['"]`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),                                    // AWS Access Key
	regexp.MustCompile(`-----BEGIN (RSA |EC |DSA )?PRIVATE KEY-----`),          // Private keys
	regexp.MustCompile(`ghp_[0-9a-zA-Z]{36}`),                                  // GitHub PAT
	regexp.MustCompile(`sk-[0-9a-zA-Z]{32,}`),                                  // OpenAI/Stripe
	regexp.MustCompile(`(?i)postgres://[^:]+:[^@]+@`),                           // DB connection strings
	regexp.MustCompile(`(?i)mongodb\+srv://[^:]+:[^@]+@`),
}

// RegisterSkills registers security scanning skills.
func RegisterSkills(reg *skill.Registry) {
	reg.Register(&skill.Skill{
		Name:        "security_scan",
		Description: "Executar varredura de seguranca no codigo",
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
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			// semgrep
			if _, err := exec.LookPath("semgrep"); err == nil {
				cmd := exec.CommandContext(ctx, "semgrep", "--config=auto", path, "--quiet")
				if out, _ := cmd.CombinedOutput(); len(out) > 0 {
					results = append(results, "## Semgrep\n"+string(out))
				}
			}

			// Go: gosec
			if fileExists(filepath.Join(path, "go.mod")) {
				if _, err := exec.LookPath("gosec"); err == nil {
					cmd := exec.CommandContext(ctx, "gosec", "-quiet", "./...")
					if out, _ := cmd.CombinedOutput(); len(out) > 0 {
						results = append(results, "## gosec\n"+string(out))
					}
				} else {
					cmd := exec.CommandContext(ctx, "go", "vet", "./...")
					if out, _ := cmd.CombinedOutput(); len(out) > 0 {
						results = append(results, "## go vet\n"+string(out))
					}
				}
			}

			// Node: npm audit
			if fileExists(filepath.Join(path, "package-lock.json")) {
				cmd := exec.CommandContext(ctx, "npm", "audit", "--production")
				if out, _ := cmd.CombinedOutput(); len(out) > 0 {
					results = append(results, "## npm audit\n"+string(out))
				}
			}

			// Python: bandit
			if _, err := exec.LookPath("bandit"); err == nil {
				if fileExists(filepath.Join(path, "requirements.txt")) || fileExists(filepath.Join(path, "pyproject.toml")) {
					cmd := exec.CommandContext(ctx, "bandit", "-r", path, "-q")
					if out, _ := cmd.CombinedOutput(); len(out) > 0 {
						results = append(results, "## bandit\n"+string(out))
					}
				}
			}

			if len(results) == 0 {
				return "Nenhum problema de seguranca encontrado (ou nenhum scanner disponivel).", nil
			}
			return strings.Join(results, "\n\n"), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "security_secrets",
		Description: "Buscar segredos hardcoded no codigo (API keys, senhas, tokens)",
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
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			// Try dedicated tools first
			if _, err := exec.LookPath("gitleaks"); err == nil {
				cmd := exec.CommandContext(ctx, "gitleaks", "detect", "--source", path, "--no-git", "-v")
				if out, _ := cmd.CombinedOutput(); len(out) > 0 {
					return "## Gitleaks\n" + string(out), nil
				}
			}

			// Fallback: regex scan
			var findings []string
			filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return nil
				}
				if info.Size() > 512*1024 { // skip files > 512KB
					return nil
				}
				ext := filepath.Ext(p)
				skipExts := map[string]bool{".png": true, ".jpg": true, ".gif": true, ".woff": true, ".ico": true, ".exe": true, ".bin": true}
				if skipExts[ext] {
					return nil
				}
				data, err := os.ReadFile(p)
				if err != nil {
					return nil
				}
				content := string(data)
				for _, re := range secretPatterns {
					matches := re.FindAllString(content, 3)
					for _, m := range matches {
						relPath, _ := filepath.Rel(path, p)
						findings = append(findings, fmt.Sprintf("- **%s**: `%s`", relPath, truncate(m, 80)))
					}
				}
				return nil
			})

			if len(findings) == 0 {
				return "Nenhum segredo hardcoded encontrado.", nil
			}
			return fmt.Sprintf("## Possiveis segredos encontrados\n\n%s\n\n**Acao recomendada**: Mova esses valores para variaveis de ambiente.", strings.Join(findings, "\n")), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "security_deps",
		Description: "Verificar dependencias com vulnerabilidades conhecidas",
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
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			var results []string

			// Go
			if fileExists(filepath.Join(path, "go.mod")) {
				if _, err := exec.LookPath("govulncheck"); err == nil {
					cmd := exec.CommandContext(ctx, "govulncheck", "./...")
					out, _ := cmd.CombinedOutput()
					results = append(results, "## Go (govulncheck)\n"+string(out))
				}
			}

			// Node
			if fileExists(filepath.Join(path, "package-lock.json")) {
				cmd := exec.CommandContext(ctx, "npm", "audit")
				out, _ := cmd.CombinedOutput()
				results = append(results, "## Node (npm audit)\n"+string(out))
			}

			// Python
			if _, err := exec.LookPath("pip-audit"); err == nil {
				cmd := exec.CommandContext(ctx, "pip-audit")
				out, _ := cmd.CombinedOutput()
				results = append(results, "## Python (pip-audit)\n"+string(out))
			}

			if len(results) == 0 {
				return "Nenhuma ferramenta de auditoria de dependencias disponivel.", nil
			}
			return strings.Join(results, "\n\n"), nil
		},
	})
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
