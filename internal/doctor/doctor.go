package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/costa/polypod/internal/config"
)

// Check represents a single diagnostic check.
type Check struct {
	Name    string
	Status  string // "ok", "warn", "fail"
	Message string
}

// RunDiagnostics performs all health checks and returns results.
func RunDiagnostics(cfg *config.Config) []Check {
	var checks []Check

	// 1. Go version
	checks = append(checks, checkCommand("Go", "go", "version"))

	// 2. Git
	checks = append(checks, checkCommand("Git", "git", "--version"))

	// 3. AI provider connectivity
	checks = append(checks, checkAPIKey("AI Provider", cfg.AI.APIKey, cfg.AI.BaseURL))

	// 4. Database
	if cfg.Database.Enabled {
		switch cfg.Database.Driver {
		case "sqlite":
			checks = append(checks, checkFile("SQLite DB", cfg.Database.Path))
		default:
			checks = append(checks, checkCommand("PostgreSQL", "psql", "--version"))
		}
	} else {
		checks = append(checks, Check{Name: "Banco de dados", Status: "ok", Message: "desativado (usando JSON)"})
	}

	// 5. Docker (for sandbox)
	checks = append(checks, checkCommand("Docker", "docker", "info"))

	// 6. Voice tools
	checks = append(checks, checkOptionalTool("Whisper (STT)", "whisper"))
	checks = append(checks, checkOptionalTool("espeak-ng (TTS)", "espeak-ng"))

	// 7. Data directories
	checks = append(checks, checkDir("Data dir", cfg.Data.Dir))
	checks = append(checks, checkDir("Agents dir", cfg.Data.AgentsDir))

	// 8. Templates
	checks = append(checks, checkDir("Templates", cfg.Templates.Dir))

	// 9. Plugins
	checks = append(checks, checkDir("Plugins", cfg.Plugins.Dir))

	// 10. MCP servers
	for _, mcp := range cfg.MCP {
		if mcp.Transport == "stdio" {
			checks = append(checks, checkCommand("MCP: "+mcp.Name, mcp.Command, "--version"))
		}
	}

	// 11. Linters
	checks = append(checks, checkOptionalTool("golangci-lint", "golangci-lint"))
	checks = append(checks, checkOptionalTool("semgrep", "semgrep"))

	// 12. Config file integrity
	checks = append(checks, Check{Name: "Config", Status: "ok", Message: fmt.Sprintf("provider=%s model=%s", cfg.AI.Provider, cfg.AI.Model)})

	return checks
}

// FormatReport formats check results for display.
func FormatReport(checks []Check) string {
	var sb strings.Builder
	sb.WriteString("## Polypod Doctor\n\n")

	okCount, warnCount, failCount := 0, 0, 0
	for _, c := range checks {
		var icon string
		switch c.Status {
		case "ok":
			icon = "[OK]"
			okCount++
		case "warn":
			icon = "[!!]"
			warnCount++
		case "fail":
			icon = "[XX]"
			failCount++
		}
		fmt.Fprintf(&sb, "  %s %-25s %s\n", icon, c.Name, c.Message)
	}

	sb.WriteString(fmt.Sprintf("\nResultado: %d ok, %d avisos, %d falhas\n", okCount, warnCount, failCount))
	if failCount == 0 {
		sb.WriteString("Polypod esta funcionando corretamente.\n")
	}
	return sb.String()
}

func checkCommand(name, cmd string, args ...string) Check {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, cmd, args...)
	out, err := c.CombinedOutput()
	if err != nil {
		return Check{Name: name, Status: "fail", Message: fmt.Sprintf("nao encontrado (%v)", err)}
	}
	firstLine := strings.Split(strings.TrimSpace(string(out)), "\n")[0]
	if len(firstLine) > 60 {
		firstLine = firstLine[:60]
	}
	return Check{Name: name, Status: "ok", Message: firstLine}
}

func checkOptionalTool(name, cmd string) Check {
	if _, err := exec.LookPath(cmd); err != nil {
		return Check{Name: name, Status: "warn", Message: "nao instalado (opcional)"}
	}
	return Check{Name: name, Status: "ok", Message: "disponivel"}
}

func checkAPIKey(name, key, baseURL string) Check {
	if key == "" || key == "${DEEPSEEK_API_KEY}" || strings.HasPrefix(key, "sk-your") {
		return Check{Name: name, Status: "fail", Message: "API key nao configurada"}
	}
	return Check{Name: name, Status: "ok", Message: fmt.Sprintf("configurada (%s...)", key[:8])}
}

func checkFile(name, path string) Check {
	if _, err := os.Stat(path); err != nil {
		return Check{Name: name, Status: "warn", Message: "arquivo nao existe (sera criado no primeiro uso)"}
	}
	return Check{Name: name, Status: "ok", Message: path}
}

func checkDir(name, path string) Check {
	if info, err := os.Stat(path); err != nil {
		return Check{Name: name, Status: "warn", Message: fmt.Sprintf("%s nao existe", path)}
	} else if !info.IsDir() {
		return Check{Name: name, Status: "fail", Message: fmt.Sprintf("%s nao e diretorio", path)}
	}
	return Check{Name: name, Status: "ok", Message: path}
}
