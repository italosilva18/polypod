package cli

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// commandResult holds the output of a local slash command.
type commandResult struct {
	output       string
	handled      bool
	quit         bool
	clearDisplay bool // limpa m.messages quando true
}

// commandDeps holds function callbacks for slash commands.
type commandDeps struct {
	activeAgent    func() string
	setAgent       func(name string)
	listAgents     func() []string
	listSkills     func() []string
	listMemories   func() ([]memoryEntry, error)
	searchMemories func(query string) ([]memoryEntry, error)
	clearSession   func() error
	sessionInfo    func() (string, int, error) // id, count, err
}

// memoryEntry is a simplified memory representation for display.
type memoryEntry struct {
	topic   string
	content string
}

// processCommand checks if input is a slash command and executes it locally.
func processCommand(input string, deps commandDeps, messages []chatEntry) commandResult {
	input = strings.TrimSpace(input)

	// ! prefix as alias for /run
	if strings.HasPrefix(input, "!") {
		cmd := strings.TrimSpace(strings.TrimPrefix(input, "!"))
		if cmd == "" {
			return commandResult{output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("!<comando>"), handled: true}
		}
		return handleRun(cmd)
	}

	if !strings.HasPrefix(input, "/") {
		return commandResult{handled: false}
	}

	parts := strings.Fields(input)
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "/help":
		return commandResult{output: helpText(), handled: true}

	case "/quit", "/exit", "/sair":
		return commandResult{quit: true, handled: true}

	case "/clear":
		return handleClear(deps)

	case "/agents":
		return handleAgents(deps)

	case "/agent":
		if len(parts) >= 3 && strings.ToLower(parts[1]) == "switch" {
			return handleAgentSwitch(deps, parts[2])
		}
		return commandResult{output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/agent switch <nome>"), handled: true}

	case "/skills":
		return handleSkills(deps)

	case "/memory":
		return handleMemory(parts, deps)

	case "/model":
		return handleModel(deps)

	case "/session":
		return handleSession(deps)

	case "/copy":
		return handleCopy(messages)

	case "/run":
		if len(parts) < 2 {
			return commandResult{output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/run <comando>"), handled: true}
		}
		shellCmd := strings.TrimSpace(strings.TrimPrefix(input, parts[0]))
		return handleRun(shellCmd)

	case "/file":
		if len(parts) < 2 {
			return commandResult{output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/file <caminho>"), handled: true}
		}
		return handleFile(parts[1])

	case "/search":
		if len(parts) < 2 {
			return commandResult{output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/search <pattern>"), handled: true}
		}
		pattern := strings.Join(parts[1:], " ")
		return handleSearch(pattern)

	case "/git":
		sub := ""
		if len(parts) >= 2 {
			sub = strings.ToLower(parts[1])
		}
		return handleGit(sub)

	case "/project":
		return handleProject()

	case "/export":
		path := "chat_export.md"
		if len(parts) >= 2 {
			path = parts[1]
		}
		return handleExport(messages, path)

	case "/context":
		return handleContext(deps, messages)

	default:
		return commandResult{
			output: errorStyle.Render("comando desconhecido: ") +
				cmdValueStyle.Render(cmd) + "\n" +
				cmdLabelStyle.Render("digite /help para ver comandos"),
			handled: true,
		}
	}
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// runShellCmd executes a shell command with a 15s timeout and returns its output.
func runShellCmd(command string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	out, err := cmd.CombinedOutput()

	output := string(out)
	if len(output) > 8000 {
		output = output[:8000] + "\n... (truncado em 8000 chars)"
	}

	return strings.TrimRight(output, "\n"), err
}
