// Package commands — batch4_security.go
// Slash commands for security/config introspection:
//
//	/hooks, /permissions, /mcp, /memory
//
// These commands intentionally operate in read-only mode against on-disk
// config (no live Manager reference is plumbed through CommandContext yet),
// so full wiring will happen in main.go once the runtime managers are passed
// in through the context.  Until then, the commands scan:
//
//   - ~/.claude.json         (global config with mcpServers)
//   - ~/.polypod/settings.json
//   - .polypod/settings.json (project overrides)
//   - CLAUDE.md / .polypod.md / AGENTS.md (via memory.DetectMemoryFiles)
//
// The intent is that the output is accurate even when managers are absent.
package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/costa/polypod/internal/command"
	"github.com/costa/polypod/internal/memory"
)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch4Security registers /hooks, /permissions, /mcp and /memory.
func RegisterBatch4Security(reg *command.Registry) {
	reg.Register(cmdHooks())
	reg.Register(cmdPermissions())
	reg.Register(cmdMCP())
	reg.Register(cmdMemory())
}

// ---------------------------------------------------------------------------
// Shared: on-disk config readers
// ---------------------------------------------------------------------------

// globalClaudeConfigPath returns the path to ~/.claude.json (Claude Code
// global config), which is where MCP servers are conventionally defined.
func globalClaudeConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude.json")
}

// loadJSONFile reads and parses a JSON file into a generic map, returning an
// empty map if the file is missing.
func loadJSONFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// readHooksFromSettings scans a settings map for a "hooks" key following the
// Claude Code HooksSettings shape: { event: [ { matcher, hooks: [...] } ] }.
// Returns a map of event -> total hook count.
func readHooksFromSettings(m map[string]any) map[string]int {
	out := make(map[string]int)
	hooks, _ := m["hooks"].(map[string]any)
	for event, raw := range hooks {
		matchers, ok := raw.([]any)
		if !ok {
			continue
		}
		total := 0
		for _, entry := range matchers {
			em, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			hh, ok := em["hooks"].([]any)
			if !ok {
				continue
			}
			total += len(hh)
		}
		out[event] = total
	}
	return out
}

// collectHooks returns a merged map of event -> count across user, project and
// global config sources.
func collectHooks(workingDir string) (map[string]int, []string) {
	merged := make(map[string]int)
	var sources []string

	// User
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".polypod", "settings.json")
		if data, err := loadJSONFile(p); err == nil && len(data) > 0 {
			for k, v := range readHooksFromSettings(data) {
				merged[k] += v
			}
			if _, ok := data["hooks"]; ok {
				sources = append(sources, p)
			}
		}
	}
	// Project
	pp := filepath.Join(workingDir, ".polypod", "settings.json")
	if data, err := loadJSONFile(pp); err == nil && len(data) > 0 {
		for k, v := range readHooksFromSettings(data) {
			merged[k] += v
		}
		if _, ok := data["hooks"]; ok {
			sources = append(sources, pp)
		}
	}
	// Global claude config
	if data, err := loadJSONFile(globalClaudeConfigPath()); err == nil && len(data) > 0 {
		for k, v := range readHooksFromSettings(data) {
			merged[k] += v
		}
		if _, ok := data["hooks"]; ok {
			sources = append(sources, globalClaudeConfigPath())
		}
	}
	return merged, sources
}

// ---------------------------------------------------------------------------
// 1. /hooks — list configured hooks grouped by event
// ---------------------------------------------------------------------------

func cmdHooks() *command.Command {
	return &command.Command{
		Name:        "hooks",
		Description: "Listar hooks configurados agrupados por evento",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			sub := ""
			if len(args) > 0 {
				sub = strings.ToLower(args[0])
			}

			// Canonical event order (subset shown; others appended alphabetically).
			canonical := []string{
				"PreToolUse", "PostToolUse", "UserPromptSubmit",
				"SessionStart", "SessionEnd", "Stop",
				"SubagentStart", "SubagentStop",
				"PreCompact", "PostCompact",
				"Notification",
			}

			merged, sources := collectHooks(ctx.WorkingDir)

			if len(merged) == 0 {
				var b strings.Builder
				b.WriteString("○ Nenhum hook configurado.\n\n")
				b.WriteString("Configure hooks em ~/.polypod/settings.json ou\n")
				b.WriteString(".polypod/settings.json sob a chave \"hooks\".\n")
				return okResult(b.String()), nil
			}

			// Merge canonical + any extras seen.
			seen := make(map[string]bool)
			order := make([]string, 0, len(merged))
			for _, e := range canonical {
				if _, ok := merged[e]; ok {
					order = append(order, e)
					seen[e] = true
				}
			}
			var extras []string
			for e := range merged {
				if !seen[e] {
					extras = append(extras, e)
				}
			}
			sort.Strings(extras)
			order = append(order, extras...)

			var b strings.Builder
			b.WriteString("● Hooks configurados\n\n")

			if sub == "list" || sub == "ls" {
				// Full detail view.
				for _, e := range order {
					count := merged[e]
					marker := "○"
					if count > 0 {
						marker = "●"
					}
					b.WriteString(fmt.Sprintf("  %s %-18s %d hook(s)\n", marker, e, count))
				}
				if len(sources) > 0 {
					b.WriteString("\nFontes:\n")
					for _, s := range sources {
						b.WriteString("  • " + s + "\n")
					}
				}
				b.WriteString("\nNota: execucao de hooks requer o Manager injetado\n")
				b.WriteString("pelo main.go. Este comando exibe apenas a config em disco.\n")
			} else {
				// Compact summary: one line per event present.
				parts := make([]string, 0, len(order))
				for _, e := range order {
					parts = append(parts, fmt.Sprintf("%s: %d", e, merged[e]))
				}
				b.WriteString("  " + strings.Join(parts, " · ") + "\n")
				b.WriteString("\nDica: /hooks list para ver detalhes.\n")
			}

			return okResult(b.String()), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 2. /permissions — show current mode + active rules
// ---------------------------------------------------------------------------

// permissionRulesFromSettings extracts rules from a .polypod/settings.json
// file shape: { allowed_tools: [...], denied_tools: [...], ask_tools: [...],
// mode: "..." }.
func permissionRulesFromSettings(m map[string]any) (allow, deny, ask []string, mode string) {
	if s, ok := m["mode"].(string); ok {
		mode = s
	}
	extract := func(key string) []string {
		raw, _ := m[key].([]any)
		out := make([]string, 0, len(raw))
		for _, r := range raw {
			switch v := r.(type) {
			case string:
				out = append(out, v)
			case map[string]any:
				if p, ok := v["pattern"].(string); ok {
					out = append(out, p)
				}
			}
		}
		return out
	}
	return extract("allowed_tools"), extract("denied_tools"), extract("ask_tools"), mode
}

func cmdPermissions() *command.Command {
	return &command.Command{
		Name:        "permissions",
		Aliases:     []string{"allowed-tools", "perm"},
		Description: "Mostrar modo e regras de permissao ativas",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			sub := ""
			if len(args) > 0 {
				sub = strings.ToLower(args[0])
			}

			type ruleEntry struct {
				source, pattern, decision string
			}
			var entries []ruleEntry
			mode := "default"
			counts := map[string]int{"user": 0, "project": 0, "session": 0}

			add := func(source string, allow, deny, ask []string) {
				for _, p := range deny {
					entries = append(entries, ruleEntry{source, p, "deny"})
					counts[source]++
				}
				for _, p := range ask {
					entries = append(entries, ruleEntry{source, p, "ask"})
					counts[source]++
				}
				for _, p := range allow {
					entries = append(entries, ruleEntry{source, p, "allow"})
					counts[source]++
				}
			}

			// User
			if home, err := os.UserHomeDir(); err == nil {
				p := filepath.Join(home, ".polypod", "settings.json")
				if data, err := loadJSONFile(p); err == nil && len(data) > 0 {
					a, d, k, m := permissionRulesFromSettings(data)
					if m != "" {
						mode = m
					}
					add("user", a, d, k)
				}
			}
			// Project
			pp := filepath.Join(ctx.WorkingDir, ".polypod", "settings.json")
			if data, err := loadJSONFile(pp); err == nil && len(data) > 0 {
				a, d, k, m := permissionRulesFromSettings(data)
				if m != "" {
					mode = m
				}
				add("project", a, d, k)
			}
			// Session (~/.polypod/sessions/<id>.json)
			if ctx.SessionID != "" {
				if sess, err := readSessionJSON(ctx.SessionID); err == nil {
					a, d, k, m := permissionRulesFromSettings(sess)
					if m != "" {
						mode = m
					}
					add("session", a, d, k)
				}
			}

			var b strings.Builder
			b.WriteString("● Permissoes\n\n")
			b.WriteString(fmt.Sprintf("  Modo atual: %s\n\n", mode))
			b.WriteString("  Regras por origem:\n")
			b.WriteString(fmt.Sprintf("    • user:    %d\n", counts["user"]))
			b.WriteString(fmt.Sprintf("    • project: %d\n", counts["project"]))
			b.WriteString(fmt.Sprintf("    • session: %d\n", counts["session"]))

			if sub == "list" || sub == "ls" {
				b.WriteString("\n  Regras ativas:\n")
				if len(entries) == 0 {
					b.WriteString("    (nenhuma)\n")
				} else {
					for _, e := range entries {
						marker := "✓"
						switch e.decision {
						case "deny":
							marker = "✗"
						case "ask":
							marker = "○"
						}
						b.WriteString(fmt.Sprintf("    %s [%-7s] %-20s → %s\n",
							marker, e.source, e.pattern, e.decision))
					}
				}
			} else {
				b.WriteString("\nDica: /permissions list para ver todas as regras.\n")
			}
			return okResult(b.String()), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 3. /mcp — list MCP servers + connection status
// ---------------------------------------------------------------------------

// mcpServersFromGlobalConfig reads ~/.claude.json and extracts the mcpServers
// map. Each server is returned with its name and transport type.
type mcpServerInfo struct {
	Name      string
	Transport string
	Command   string
	URL       string
}

func mcpServersFromGlobalConfig() ([]mcpServerInfo, error) {
	data, err := loadJSONFile(globalClaudeConfigPath())
	if err != nil {
		return nil, err
	}
	raw, _ := data["mcpServers"].(map[string]any)
	out := make([]mcpServerInfo, 0, len(raw))
	for name, cfg := range raw {
		entry := mcpServerInfo{Name: name, Transport: "stdio"}
		if m, ok := cfg.(map[string]any); ok {
			if t, ok := m["type"].(string); ok {
				entry.Transport = t
			}
			if c, ok := m["command"].(string); ok {
				entry.Command = c
			}
			if u, ok := m["url"].(string); ok {
				entry.URL = u
			}
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func cmdMCP() *command.Command {
	return &command.Command{
		Name:        "mcp",
		Description: "Gerenciar servidores MCP",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			sub := ""
			if len(args) > 0 {
				sub = strings.ToLower(args[0])
			}

			switch sub {
			case "reconnect":
				if len(args) < 2 {
					return errResult("uso: /mcp reconnect <nome>"), nil
				}
				name := args[1]
				// Without a plumbed mcp.Manager we can't actually reconnect
				// here; the wiring is expected to be injected in main.go.
				msg := fmt.Sprintf("○ Reconexao de %q solicitada.\n\n", name) +
					"Nota: reconexao ao vivo requer o mcp.Manager injetado\n" +
					"pelo main.go. Marcado como pendente de wiring.\n"
				return okResult(msg), nil
			}

			servers, err := mcpServersFromGlobalConfig()
			if err != nil {
				return errResult(fmt.Sprintf("falha ao ler config global: %v", err)), nil
			}

			var b strings.Builder
			b.WriteString("● Servidores MCP\n\n")

			if len(servers) == 0 {
				b.WriteString("  ○ Nenhum servidor MCP configurado.\n\n")
				b.WriteString("  Adicione em ~/.claude.json sob \"mcpServers\" ou\n")
				b.WriteString("  via /mcp add (pendente de wiring).\n")
				return okResult(b.String()), nil
			}

			for _, s := range servers {
				// Without a live manager we report as "desconhecido"
				// (unknown state). Live status will come from mcp.Manager
				// once injected.
				marker := "○"
				status := "desconhecido"
				detail := s.Command
				if s.Transport == "sse" {
					detail = s.URL
				}
				b.WriteString(fmt.Sprintf("  %s %-20s [%s] %s — %s\n",
					marker, s.Name, s.Transport, status, detail))
			}
			b.WriteString("\nNota: contagem de ferramentas e status real exigem\n")
			b.WriteString("o mcp.Manager injetado pelo main.go.\n")
			b.WriteString("Comandos: /mcp reconnect <nome>\n")
			return okResult(b.String()), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 4. /memory — list memory files tracked by the session
// ---------------------------------------------------------------------------

func cmdMemory() *command.Command {
	return &command.Command{
		Name:        "memory",
		Description: "Listar e recarregar arquivos de memoria (CLAUDE.md, .polypod.md, AGENTS.md)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			sub := ""
			if len(args) > 0 {
				sub = strings.ToLower(args[0])
			}

			wd := ctx.WorkingDir
			if wd == "" {
				var err error
				wd, err = os.Getwd()
				if err != nil {
					return errResult(fmt.Sprintf("cannot determine working directory: %v", err)), nil
				}
			}

			files, err := memory.DetectMemoryFiles(wd)
			if err != nil {
				return errResult(fmt.Sprintf("falha ao detectar arquivos de memoria: %v", err)), nil
			}

			var b strings.Builder

			switch sub {
			case "reload", "refresh":
				// Re-read each detected file so the content is fresh on disk.
				reloaded := 0
				for i := range files {
					mf, err := memory.LoadMemoryFile(files[i].Path)
					if err != nil {
						continue
					}
					files[i] = *mf
					reloaded++
				}
				b.WriteString(fmt.Sprintf("● Memoria recarregada: %d arquivo(s)\n\n", reloaded))
			default:
				b.WriteString("● Arquivos de memoria\n\n")
			}

			if len(files) == 0 {
				b.WriteString("  ○ Nenhum arquivo de memoria encontrado.\n\n")
				b.WriteString("  Crie um dos seguintes no diretorio do projeto:\n")
				for _, n := range memory.MemoryFileNames() {
					b.WriteString("    • " + n + "\n")
				}
				return okResult(b.String()), nil
			}

			for _, f := range files {
				info, err := os.Stat(f.Path)
				modTime := "?"
				size := f.Size
				if err == nil {
					modTime = info.ModTime().Format(time.RFC3339)
					size = info.Size()
				}
				b.WriteString(fmt.Sprintf("  ● %s\n", f.Path))
				b.WriteString(fmt.Sprintf("     tipo=%s origem=%s tamanho=%dB modificado=%s\n",
					f.Type, f.Source, size, modTime))
				if len(f.Imports) > 0 {
					b.WriteString(fmt.Sprintf("     imports: %s\n", strings.Join(f.Imports, ", ")))
				}
			}

			b.WriteString(fmt.Sprintf("\n%d arquivo(s) rastreado(s).\n", len(files)))
			if sub != "reload" && sub != "refresh" {
				b.WriteString("Dica: /memory reload para re-ler do disco.\n")
			}
			return okResult(b.String()), nil
		},
	}
}
