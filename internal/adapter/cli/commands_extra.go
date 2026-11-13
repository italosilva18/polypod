package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// ── /doctor ─────────────────────────────────────────────────────────────────

// doctorCheck represents a single diagnostic check result.
type doctorCheck struct {
	status string // "ok", "warn", "fail", "info"
	label  string
	detail string
}

func handleDoctor(deps commandDeps) commandResult {
	var b strings.Builder

	title := fmt.Sprintf("Polypod Doctor v%s", version)
	b.WriteString(cmdTitleStyle.Render(title))
	b.WriteString("\n")
	b.WriteString(separatorStyle.Render(strings.Repeat("─", 40)))
	b.WriteString("\n\n")

	okCount, warnCount, failCount := 0, 0, 0

	writeGroup := func(name string, checks []doctorCheck) {
		b.WriteString(cmdBadgeStyle.Render(name + ":"))
		b.WriteString("\n")
		for _, c := range checks {
			b.WriteString("  ")
			switch c.status {
			case "ok":
				b.WriteString(notifySuccessStyle.Render("✓"))
				okCount++
			case "warn":
				b.WriteString(notifyWarnStyle.Render("⚠"))
				warnCount++
			case "fail":
				b.WriteString(notifyErrorStyle.Render("✗"))
				failCount++
			case "info":
				b.WriteString(cmdLabelStyle.Render("ℹ"))
			}
			b.WriteString(" ")
			b.WriteString(cmdValueStyle.Render(c.label))
			if c.detail != "" {
				b.WriteString(cmdLabelStyle.Render(" " + c.detail))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// SISTEMA
	sysChecks := []doctorCheck{
		{status: "ok", label: "Go " + strings.TrimPrefix(runtime.Version(), "go")},
		{status: "ok", label: fmt.Sprintf("SO: %s/%s", runtime.GOOS, runtime.GOARCH)},
	}
	if mem := readMemFreeGB(); mem != "" {
		sysChecks = append(sysChecks, doctorCheck{status: "ok", label: "Memoria: " + mem + " livre"})
	}
	writeGroup("SISTEMA", sysChecks)

	// CONFIG
	var cfgChecks []doctorCheck
	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, ".polypod")
	if info, err := os.Stat(dataDir); err == nil && info.IsDir() {
		if isWritable(dataDir) {
			cfgChecks = append(cfgChecks, doctorCheck{status: "ok", label: "Data dir", detail: dataDir + " (writable)"})
		} else {
			cfgChecks = append(cfgChecks, doctorCheck{status: "warn", label: "Data dir", detail: dataDir + " (nao gravavel)"})
		}
	} else {
		cfgChecks = append(cfgChecks, doctorCheck{status: "warn", label: "Data dir", detail: dataDir + " (nao existe)"})
	}

	// API keys via env vars (provider-agnostic)
	providerVars := []struct {
		name   string
		envKey string
	}{
		{"DeepSeek", "DEEPSEEK_API_KEY"},
		{"OpenAI", "OPENAI_API_KEY"},
		{"Anthropic", "ANTHROPIC_API_KEY"},
		{"Google", "GEMINI_API_KEY"},
		{"Groq", "GROQ_API_KEY"},
		{"Mistral", "MISTRAL_API_KEY"},
	}
	foundAnyKey := false
	for _, p := range providerVars {
		if v := os.Getenv(p.envKey); v != "" {
			cfgChecks = append(cfgChecks, doctorCheck{status: "ok", label: p.name, detail: "configurada"})
			foundAnyKey = true
		}
	}
	if !foundAnyKey {
		cfgChecks = append(cfgChecks, doctorCheck{status: "warn", label: "API keys", detail: "nenhuma env var de provider encontrada"})
	}

	// SQLite check
	sqlitePath := filepath.Join(dataDir, "polypod.db")
	if _, err := os.Stat(sqlitePath); err == nil {
		cfgChecks = append(cfgChecks, doctorCheck{status: "ok", label: "SQLite", detail: sqlitePath})
	} else {
		cfgChecks = append(cfgChecks, doctorCheck{status: "info", label: "SQLite", detail: "nao inicializado"})
	}

	// Disk space
	if avail := diskAvailGB(dataDir); avail != "" {
		cfgChecks = append(cfgChecks, doctorCheck{status: "ok", label: "Disco", detail: avail + " disponivel"})
	}

	writeGroup("CONFIG", cfgChecks)

	// PROJETO
	var projChecks []doctorCheck
	wd, _ := os.Getwd()
	if _, err := os.Stat(filepath.Join(wd, ".git")); err == nil {
		branch, _ := runShellCmd("git rev-parse --abbrev-ref HEAD")
		projChecks = append(projChecks, doctorCheck{status: "ok", label: "Git repo", detail: branch + " branch"})
		if status, err := runShellCmd("git status --porcelain"); err == nil && status != "" {
			nChanges := len(strings.Split(strings.TrimSpace(status), "\n"))
			projChecks = append(projChecks, doctorCheck{status: "warn", label: fmt.Sprintf("%d arquivos nao commitados", nChanges)})
		} else {
			projChecks = append(projChecks, doctorCheck{status: "ok", label: "working tree limpa"})
		}
	} else {
		projChecks = append(projChecks, doctorCheck{status: "info", label: "Git repo", detail: "nao e um repositorio git"})
	}
	if _, err := os.Stat(filepath.Join(wd, ".polypod")); err == nil {
		projChecks = append(projChecks, doctorCheck{status: "ok", label: ".polypod/ existe"})
	} else {
		projChecks = append(projChecks, doctorCheck{status: "info", label: ".polypod/ nao existe"})
	}
	if _, err := os.Stat(filepath.Join(wd, ".polypod.md")); err == nil {
		projChecks = append(projChecks, doctorCheck{status: "ok", label: ".polypod.md existe"})
	}
	writeGroup("PROJETO", projChecks)

	// PERMISSOES
	var permChecks []doctorCheck
	permPath := filepath.Join(wd, ".polypod", "settings.json")
	if data, err := os.ReadFile(permPath); err == nil {
		if isValidJSON(data) {
			allow, ask, deny := countPermissionRules(data)
			permChecks = append(permChecks, doctorCheck{status: "ok", label: "settings.json valido"})
			if allow > 0 {
				permChecks = append(permChecks, doctorCheck{status: "ok", label: fmt.Sprintf("%d allow rules", allow)})
			}
			if ask > 0 {
				permChecks = append(permChecks, doctorCheck{status: "ok", label: fmt.Sprintf("%d ask rules", ask)})
			}
			if deny > 0 {
				permChecks = append(permChecks, doctorCheck{status: "ok", label: fmt.Sprintf("%d deny rules", deny)})
			}
		} else {
			permChecks = append(permChecks, doctorCheck{status: "fail", label: "settings.json invalido", detail: "JSON malformado"})
		}
	} else {
		permChecks = append(permChecks, doctorCheck{status: "info", label: "settings.json nao existe"})
	}
	writeGroup("PERMISSOES", permChecks)

	// MCP
	var mcpChecks []doctorCheck
	if deps.listMCPServers != nil {
		servers := deps.listMCPServers()
		if len(servers) == 0 {
			mcpChecks = append(mcpChecks, doctorCheck{status: "info", label: "Nenhum servidor configurado"})
		} else {
			for _, s := range servers {
				status := "ok"
				detail := fmt.Sprintf("%s - %d tools", s.transport, s.toolCount)
				if !s.connected {
					status = "fail"
					detail = s.transport + " - desconectado"
				}
				mcpChecks = append(mcpChecks, doctorCheck{status: status, label: s.name, detail: detail})
			}
		}
	} else {
		mcpChecks = append(mcpChecks, doctorCheck{status: "info", label: "MCP manager nao disponivel"})
	}
	writeGroup("MCP", mcpChecks)

	// HOOKS
	if deps.listHooks != nil {
		var hookChecks []doctorCheck
		hks := deps.listHooks()
		if len(hks) == 0 {
			hookChecks = append(hookChecks, doctorCheck{status: "info", label: "Nenhum hook registrado"})
		} else {
			enabled := 0
			for _, h := range hks {
				if h.enabled {
					enabled++
				}
			}
			hookChecks = append(hookChecks, doctorCheck{status: "ok", label: fmt.Sprintf("%d hooks registrados", len(hks)), detail: fmt.Sprintf("(%d ativados)", enabled)})
		}
		writeGroup("HOOKS", hookChecks)
	}

	// Status geral
	b.WriteString(cmdLabelStyle.Render("Status geral: "))
	if failCount > 0 {
		b.WriteString(notifyErrorStyle.Render(fmt.Sprintf("FALHA (%d erros, %d warnings)", failCount, warnCount)))
	} else if warnCount > 0 {
		b.WriteString(notifyWarnStyle.Render(fmt.Sprintf("OK (%d warnings)", warnCount)))
	} else {
		b.WriteString(notifySuccessStyle.Render(fmt.Sprintf("OK (%d checks)", okCount)))
	}

	return commandResult{output: b.String(), handled: true}
}

func readMemFreeGB() string {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "MemAvailable:") {
			var kb float64
			fmt.Sscanf(line, "MemAvailable: %f kB", &kb)
			return fmt.Sprintf("%.1f GB", kb/1024/1024)
		}
	}
	return ""
}

func diskAvailGB(path string) string {
	out, err := exec.Command("df", "-BG", "--output=avail", path).Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return ""
	}
	return strings.TrimSpace(lines[1])
}

func isWritable(path string) bool {
	test := filepath.Join(path, ".polypod_write_test")
	f, err := os.Create(test)
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(test)
	return true
}

func isValidJSON(data []byte) bool {
	// Lightweight balance check ignoring string contents.
	depth := 0
	inStr := false
	esc := false
	for _, c := range data {
		if esc {
			esc = false
			continue
		}
		if c == '\\' {
			esc = true
			continue
		}
		if c == '"' {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		switch c {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0 && !inStr
}

func countPermissionRules(data []byte) (allow, ask, deny int) {
	s := string(data)
	allow = strings.Count(s, `"allow"`)
	ask = strings.Count(s, `"ask"`)
	deny = strings.Count(s, `"deny"`)
	return
}

// ── /mcp ────────────────────────────────────────────────────────────────────

// mcpServerInfo is the display representation of an MCP server.
type mcpServerInfo struct {
	name      string
	transport string
	connected bool
	toolCount int
	tools     []string
}

func handleMCP(parts []string, deps commandDeps) commandResult {
	if deps.listMCPServers == nil {
		return commandResult{output: cmdLabelStyle.Render("MCP manager nao disponivel"), handled: true}
	}

	sub := ""
	if len(parts) >= 2 {
		sub = strings.ToLower(parts[1])
	}

	switch sub {
	case "", "list":
		return handleMCPList(deps)
	case "status":
		if len(parts) < 3 {
			return commandResult{output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/mcp status <server>"), handled: true}
		}
		return handleMCPStatus(deps, parts[2])
	case "tools":
		server := ""
		if len(parts) >= 3 {
			server = parts[2]
		}
		return handleMCPTools(deps, server)
	case "reconnect":
		if len(parts) < 3 {
			return commandResult{output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/mcp reconnect <server>"), handled: true}
		}
		return handleMCPReconnect(deps, parts[2])
	default:
		return commandResult{
			output: cmdLabelStyle.Render("uso: ") +
				cmdValueStyle.Render("/mcp [list|status <s>|tools [s]|reconnect <s>]"),
			handled: true,
		}
	}
}

func handleMCPList(deps commandDeps) commandResult {
	servers := deps.listMCPServers()

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("MCP Servers"))
	b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" (%d)", len(servers))))
	b.WriteString("\n\n")

	if len(servers) == 0 {
		b.WriteString("  ")
		b.WriteString(cmdLabelStyle.Render("nenhum servidor MCP configurado"))
		return commandResult{output: b.String(), handled: true}
	}

	sort.Slice(servers, func(i, j int) bool { return servers[i].name < servers[j].name })
	for _, s := range servers {
		b.WriteString("  ")
		if s.connected {
			b.WriteString(cmdActiveStyle.Render("●"))
		} else {
			b.WriteString(cmdInactiveStyle.Render("○"))
		}
		b.WriteString(" ")
		b.WriteString(cmdValueStyle.Render(s.name))
		b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" (%s)", s.transport)))
		if s.connected {
			b.WriteString(cmdLabelStyle.Render(" - "))
			b.WriteString(notifySuccessStyle.Render("conectado"))
			b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" - %d tools", s.toolCount)))
		} else {
			b.WriteString(cmdLabelStyle.Render(" - "))
			b.WriteString(notifyErrorStyle.Render("desconectado"))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(cmdLabelStyle.Render("  detalhes: "))
	b.WriteString(cmdValueStyle.Render("/mcp tools [server]"))

	return commandResult{output: b.String(), handled: true}
}

func handleMCPStatus(deps commandDeps, name string) commandResult {
	servers := deps.listMCPServers()
	var target *mcpServerInfo
	for i := range servers {
		if servers[i].name == name {
			target = &servers[i]
			break
		}
	}
	if target == nil {
		return commandResult{
			output:  errorStyle.Render("servidor nao encontrado: ") + cmdValueStyle.Render(name),
			handled: true,
		}
	}

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("MCP: " + target.name))
	b.WriteString("\n\n")

	rows := []struct{ label, value string }{
		{"nome", target.name},
		{"transport", target.transport},
	}
	if target.connected {
		rows = append(rows, struct{ label, value string }{"status", "conectado"})
	} else {
		rows = append(rows, struct{ label, value string }{"status", "desconectado"})
	}
	rows = append(rows, struct{ label, value string }{"tools", fmt.Sprintf("%d", target.toolCount)})

	maxLabel := 0
	for _, r := range rows {
		if len(r.label) > maxLabel {
			maxLabel = len(r.label)
		}
	}
	for _, r := range rows {
		padding := strings.Repeat(" ", maxLabel-len(r.label))
		b.WriteString("  ")
		b.WriteString(cmdLabelStyle.Render(r.label + padding + "  "))
		b.WriteString(cmdValueStyle.Render(r.value))
		b.WriteString("\n")
	}

	if len(target.tools) > 0 {
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(cmdBadgeStyle.Render("tools:"))
		b.WriteString("\n")
		for _, t := range target.tools {
			b.WriteString("    ")
			b.WriteString(cmdBadgeStyle.Render("▸ "))
			b.WriteString(cmdValueStyle.Render(t))
			b.WriteString("\n")
		}
	}

	return commandResult{output: b.String(), handled: true}
}

func handleMCPTools(deps commandDeps, server string) commandResult {
	servers := deps.listMCPServers()
	if len(servers) == 0 {
		return commandResult{output: cmdLabelStyle.Render("nenhum servidor MCP configurado"), handled: true}
	}

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("MCP Tools"))
	b.WriteString("\n\n")

	total := 0
	for _, s := range servers {
		if server != "" && s.name != server {
			continue
		}
		b.WriteString("  ")
		b.WriteString(cmdBadgeStyle.Render(s.name))
		b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" (%d)", len(s.tools))))
		b.WriteString("\n")
		for _, t := range s.tools {
			b.WriteString("    ")
			b.WriteString(cmdBadgeStyle.Render("▸ "))
			b.WriteString(cmdValueStyle.Render(t))
			b.WriteString("\n")
			total++
		}
		b.WriteString("\n")
	}

	if total == 0 {
		if server != "" {
			return commandResult{
				output:  cmdLabelStyle.Render("nenhuma tool em: ") + cmdValueStyle.Render(server),
				handled: true,
			}
		}
		return commandResult{output: cmdLabelStyle.Render("nenhuma tool disponivel"), handled: true}
	}

	return commandResult{output: strings.TrimRight(b.String(), "\n"), handled: true}
}

func handleMCPReconnect(deps commandDeps, name string) commandResult {
	if deps.reconnectMCP == nil {
		return commandResult{output: cmdLabelStyle.Render("reconnect nao disponivel"), handled: true}
	}
	if err := deps.reconnectMCP(name); err != nil {
		return commandResult{
			output:  errorStyle.Render("erro ao reconectar: ") + cmdLabelStyle.Render(err.Error()),
			handled: true,
		}
	}
	return commandResult{
		output:  notifySuccessStyle.Render("servidor reconectado: ") + cmdValueStyle.Render(name),
		handled: true,
	}
}

// ── /hooks ──────────────────────────────────────────────────────────────────

// hookInfo is the display representation of a hook.
type hookInfo struct {
	name    string
	event   string
	hType   string
	command string
	matcher string
	enabled bool
}

func handleHooks(parts []string, deps commandDeps) commandResult {
	if deps.listHooks == nil {
		return commandResult{output: cmdLabelStyle.Render("hooks registry nao disponivel"), handled: true}
	}

	sub := ""
	if len(parts) >= 2 {
		sub = strings.ToLower(parts[1])
	}

	switch sub {
	case "", "list":
		return handleHooksList(deps)
	case "enable":
		if len(parts) < 3 {
			return commandResult{output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/hooks enable <name>"), handled: true}
		}
		return handleHookToggle(deps, parts[2], true)
	case "disable":
		if len(parts) < 3 {
			return commandResult{output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/hooks disable <name>"), handled: true}
		}
		return handleHookToggle(deps, parts[2], false)
	case "show":
		if len(parts) < 3 {
			return commandResult{output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/hooks show <name>"), handled: true}
		}
		return handleHookShow(deps, parts[2])
	default:
		return commandResult{
			output: cmdLabelStyle.Render("uso: ") +
				cmdValueStyle.Render("/hooks [list|enable <n>|disable <n>|show <n>]"),
			handled: true,
		}
	}
}

func handleHooksList(deps commandDeps) commandResult {
	hks := deps.listHooks()

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Hooks registrados"))
	b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" (%d)", len(hks))))
	b.WriteString("\n\n")

	if len(hks) == 0 {
		b.WriteString("  ")
		b.WriteString(cmdLabelStyle.Render("nenhum hook registrado"))
		return commandResult{output: b.String(), handled: true}
	}

	// Group by event
	byEvent := make(map[string][]hookInfo)
	var events []string
	for _, h := range hks {
		if _, ok := byEvent[h.event]; !ok {
			events = append(events, h.event)
		}
		byEvent[h.event] = append(byEvent[h.event], h)
	}
	sort.Strings(events)

	for _, ev := range events {
		b.WriteString("  ")
		b.WriteString(cmdBadgeStyle.Render(ev + ":"))
		b.WriteString("\n")
		for _, h := range byEvent[ev] {
			b.WriteString("    ")
			if h.enabled {
				b.WriteString(notifySuccessStyle.Render("✓"))
			} else {
				b.WriteString(notifyErrorStyle.Render("✗"))
			}
			b.WriteString(" ")
			b.WriteString(cmdValueStyle.Render(h.name))
			if h.enabled {
				b.WriteString(cmdLabelStyle.Render(" (ativado)"))
			} else {
				b.WriteString(cmdLabelStyle.Render(" (desativado)"))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(cmdLabelStyle.Render("  controle: "))
	b.WriteString(cmdValueStyle.Render("/hooks enable|disable|show <name>"))

	return commandResult{output: b.String(), handled: true}
}

func handleHookToggle(deps commandDeps, name string, enable bool) commandResult {
	if deps.setHookEnabled == nil {
		return commandResult{output: cmdLabelStyle.Render("operacao nao disponivel"), handled: true}
	}
	if ok := deps.setHookEnabled(name, enable); !ok {
		return commandResult{
			output:  errorStyle.Render("hook nao encontrado: ") + cmdValueStyle.Render(name),
			handled: true,
		}
	}
	state := "desativado"
	if enable {
		state = "ativado"
	}
	return commandResult{
		output:  notifySuccessStyle.Render("hook "+state+": ") + cmdValueStyle.Render(name),
		handled: true,
	}
}

func handleHookShow(deps commandDeps, name string) commandResult {
	hks := deps.listHooks()
	var target *hookInfo
	for i := range hks {
		if hks[i].name == name {
			target = &hks[i]
			break
		}
	}
	if target == nil {
		return commandResult{
			output:  errorStyle.Render("hook nao encontrado: ") + cmdValueStyle.Render(name),
			handled: true,
		}
	}

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Hook: " + target.name))
	b.WriteString("\n\n")

	rows := []struct{ label, value string }{
		{"nome", target.name},
		{"evento", target.event},
		{"tipo", target.hType},
	}
	if target.matcher != "" {
		rows = append(rows, struct{ label, value string }{"matcher", target.matcher})
	}
	if target.command != "" {
		rows = append(rows, struct{ label, value string }{"comando", target.command})
	}
	if target.enabled {
		rows = append(rows, struct{ label, value string }{"status", "ativado"})
	} else {
		rows = append(rows, struct{ label, value string }{"status", "desativado"})
	}

	maxLabel := 0
	for _, r := range rows {
		if len(r.label) > maxLabel {
			maxLabel = len(r.label)
		}
	}
	for _, r := range rows {
		padding := strings.Repeat(" ", maxLabel-len(r.label))
		b.WriteString("  ")
		b.WriteString(cmdLabelStyle.Render(r.label + padding + "  "))
		b.WriteString(cmdValueStyle.Render(r.value))
		b.WriteString("\n")
	}

	return commandResult{output: b.String(), handled: true}
}

// ── /files ──────────────────────────────────────────────────────────────────

// fileEntry tracks a file referenced in the session.
type fileEntry struct {
	path      string
	readAt    time.Time
	edited    bool
	editCount int
}

func handleFiles(deps commandDeps) commandResult {
	if deps.sessionState == nil {
		return commandResult{output: cmdLabelStyle.Render("state de sessao nao disponivel"), handled: true}
	}
	files := deps.sessionState.snapshotFiles()
	if len(files) == 0 {
		return commandResult{output: cmdLabelStyle.Render("nenhum arquivo no contexto ainda"), handled: true}
	}

	var read, edited []fileEntry
	for _, f := range files {
		if f.edited {
			edited = append(edited, f)
		} else {
			read = append(read, f)
		}
	}
	sort.Slice(read, func(i, j int) bool { return read[i].readAt.After(read[j].readAt) })
	sort.Slice(edited, func(i, j int) bool { return edited[i].readAt.After(edited[j].readAt) })

	// Sum up sizes (rough token estimate)
	totalBytes := int64(0)
	for _, f := range files {
		if info, err := os.Stat(resolveFilePath(f.path)); err == nil {
			totalBytes += info.Size()
		}
	}
	approxTokens := totalBytes / 4

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Arquivos no contexto"))
	b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" (%d total, ~%s tokens)", len(files), formatTokens(int(approxTokens)))))
	b.WriteString("\n\n")

	writeFile := func(f fileEntry) {
		info, err := os.Stat(resolveFilePath(f.path))
		var size int64
		if err == nil {
			size = info.Size()
		}
		tokens := size / 4

		b.WriteString("  ")
		b.WriteString(cmdValueStyle.Render(truncate(f.path, 50)))
		if size > 0 {
			b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" %s tokens", formatTokens(int(tokens)))))
		}
		if f.edited && f.editCount > 0 {
			b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" (%d edicoes)", f.editCount)))
		}
		b.WriteString("\n")
	}

	if len(read) > 0 {
		b.WriteString(cmdBadgeStyle.Render("  LIDOS:"))
		b.WriteString("\n")
		for _, f := range read {
			writeFile(f)
		}
		b.WriteString("\n")
	}
	if len(edited) > 0 {
		b.WriteString(cmdBadgeStyle.Render("  EDITADOS:"))
		b.WriteString("\n")
		for _, f := range edited {
			writeFile(f)
		}
	}

	return commandResult{output: strings.TrimRight(b.String(), "\n"), handled: true}
}

func resolveFilePath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	wd, _ := os.Getwd()
	return filepath.Join(wd, p)
}

func formatTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// ── /add-dir ────────────────────────────────────────────────────────────────

// dirEntry represents an additional working directory.
type dirEntry struct {
	path    string
	addedAt time.Time
}

func handleAddDir(parts []string, deps commandDeps) commandResult {
	if deps.sessionState == nil {
		return commandResult{output: cmdLabelStyle.Render("state de sessao nao disponivel"), handled: true}
	}

	sub := ""
	if len(parts) >= 2 {
		sub = strings.ToLower(parts[1])
	}

	// /add-dir list (also shown when /add-dir has no args)
	if sub == "list" || len(parts) == 1 {
		return handleAddDirList(deps)
	}

	// /add-dir remove <path>
	if sub == "remove" {
		if len(parts) < 3 {
			return commandResult{output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/add-dir remove <path>"), handled: true}
		}
		arg := strings.Join(parts[2:], " ")
		resolved, err := resolveAddDirPath(arg)
		if err != nil {
			return commandResult{output: errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()), handled: true}
		}
		if ok := deps.sessionState.removeDir(resolved); !ok {
			return commandResult{
				output:  errorStyle.Render("diretorio nao estava na lista: ") + cmdValueStyle.Render(resolved),
				handled: true,
			}
		}
		return commandResult{
			output:  notifySuccessStyle.Render("diretorio removido: ") + cmdValueStyle.Render(resolved),
			handled: true,
		}
	}

	// /add-dir <path>
	path := strings.TrimSpace(strings.Join(parts[1:], " "))
	if path == "" {
		return commandResult{
			output: cmdLabelStyle.Render("uso: ") +
				cmdValueStyle.Render("/add-dir <path>") +
				cmdLabelStyle.Render(" | ") +
				cmdValueStyle.Render("/add-dir list") +
				cmdLabelStyle.Render(" | ") +
				cmdValueStyle.Render("/add-dir remove <path>"),
			handled: true,
		}
	}

	resolved, err := resolveAddDirPath(path)
	if err != nil {
		return commandResult{output: errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()), handled: true}
	}
	deps.sessionState.addDir(resolved)

	return commandResult{
		output:  notifySuccessStyle.Render("diretorio adicionado: ") + cmdValueStyle.Render(resolved),
		handled: true,
	}
}

func handleAddDirList(deps commandDeps) commandResult {
	dirs := deps.sessionState.snapshotDirs()

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Diretorios adicionais"))
	b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" (%d)", len(dirs))))
	b.WriteString("\n\n")

	if len(dirs) == 0 {
		b.WriteString("  ")
		b.WriteString(cmdLabelStyle.Render("nenhum diretorio adicional"))
		b.WriteString("\n\n")
		b.WriteString(cmdLabelStyle.Render("  adicionar: "))
		b.WriteString(cmdValueStyle.Render("/add-dir <path>"))
		return commandResult{output: b.String(), handled: true}
	}

	now := time.Now()
	for i, d := range dirs {
		b.WriteString("  ")
		b.WriteString(cmdBadgeStyle.Render(fmt.Sprintf("%d.", i+1)))
		b.WriteString(" ")
		b.WriteString(cmdValueStyle.Render(d.path))
		b.WriteString(cmdLabelStyle.Render(" (" + formatRelTime(now.Sub(d.addedAt)) + ")"))
		b.WriteString("\n")
	}

	return commandResult{output: strings.TrimRight(b.String(), "\n"), handled: true}
}

func resolveAddDirPath(p string) (string, error) {
	if !filepath.IsAbs(p) {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		p = filepath.Join(wd, p)
	}
	info, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("nao e um diretorio: %s", p)
	}
	return filepath.Clean(p), nil
}

func formatRelTime(d time.Duration) string {
	if d < time.Minute {
		return "adicionado agora"
	}
	if d < time.Hour {
		return fmt.Sprintf("ha %dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("ha %dh", int(d.Hours()))
	}
	return fmt.Sprintf("ha %dd", int(d.Hours()/24))
}
