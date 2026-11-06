// Package commands — batch3_core.go
// Core introspection / configuration slash commands:
//
//	/model    — list available models or switch the active model for the
//	            current session
//	/config   — human-readable dump of the loaded config (read-only)
//	/doctor   — health diagnostic summary (Go version, binary path, config
//	            origin, providers, MCP servers)
//
// These three commands intentionally avoid live network calls: /doctor will
// report provider reachability based on static config (API key presence,
// base URL, etc.) so it remains fast and safe in headless/test environments.
//
// Session-level state (for /model switching) is persisted via the existing
// per-session JSON helpers in batch1.go (readSessionJSON / writeSessionJSON)
// so that a chosen model survives the command invocation without needing a
// live runtime handle plumbed through CommandContext.
package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/costa/polypod/internal/command"
	"github.com/costa/polypod/internal/modelpicker"
)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch3Core registers /model, /config and /doctor on the given
// command registry. It is safe to call multiple times; later calls overwrite
// earlier registrations for these names.
func RegisterBatch3Core(reg *command.Registry) {
	reg.Register(cmdModel())
	reg.Register(cmdConfig())
	reg.Register(cmdDoctor())
}

// ---------------------------------------------------------------------------
// Shared helpers (batch3-local)
// ---------------------------------------------------------------------------

// loadPolypodConfigView returns a best-effort view of the polypod config
// merged from the well-known on-disk locations. Unlike internal/config.Load
// this does not validate or substitute env vars — /config and /doctor just
// need a read-only snapshot for display, so we stay tolerant of missing or
// partially-written files.
//
// Return value includes the path the config was resolved from ("" if none),
// the raw decoded map, and a derived summary of key fields used by both
// /config and /doctor.
type configView struct {
	Path     string
	Provider string
	Model    string
	DataDir  string
	Hooks    int // count of enabled hooks
	MCPCount int // count of configured MCP servers
	Raw      map[string]any
}

func loadPolypodConfigView() configView {
	var v configView

	// Candidate paths, in priority order.
	candidates := []string{
		filepath.Join(configHomeDir(), "config.yaml"),
		filepath.Join(configHomeDir(), "config.yml"),
		filepath.Join(configHomeDir(), "settings.json"),
	}
	// Also look for ~/.claude.json (legacy location for MCP servers).
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".claude.json"))
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			v.Path = p
			break
		}
	}

	// Load the primary settings.json if present (best effort).
	raw, _ := loadJSONFile(filepath.Join(configHomeDir(), "settings.json"))
	v.Raw = raw

	// Extract AI provider/model.
	if ai, ok := raw["ai"].(map[string]any); ok {
		if s, ok := ai["provider"].(string); ok {
			v.Provider = s
		}
		if s, ok := ai["model"].(string); ok {
			v.Model = s
		}
	}
	// Fallback to top-level active_provider / active_model keys.
	if v.Provider == "" {
		if s, ok := raw["active_provider"].(string); ok {
			v.Provider = s
		}
	}
	if v.Model == "" {
		if s, ok := raw["active_model"].(string); ok {
			v.Model = s
		}
	}

	// Data dir.
	if d, ok := raw["data_dir"].(string); ok {
		v.DataDir = d
	} else if data, ok := raw["data"].(map[string]any); ok {
		if s, ok := data["dir"].(string); ok {
			v.DataDir = s
		}
	}
	if v.DataDir == "" {
		v.DataDir = configHomeDir()
	}

	// Hooks count (enabled ones).
	if hs, ok := raw["hooks"].([]any); ok {
		for _, h := range hs {
			if m, ok := h.(map[string]any); ok {
				if enabled, ok := m["enabled"].(bool); !ok || enabled {
					v.Hooks++
				}
			}
		}
	}

	// MCP server count from settings.json.
	if mcps, ok := raw["mcp"].([]any); ok {
		v.MCPCount += len(mcps)
	}
	// Also merge in any ~/.claude.json mcpServers map.
	if home, err := os.UserHomeDir(); err == nil {
		if claude, err := loadJSONFile(filepath.Join(home, ".claude.json")); err == nil {
			if ms, ok := claude["mcpServers"].(map[string]any); ok {
				v.MCPCount += len(ms)
			}
		}
	}

	return v
}

// activeModelForSession returns the model currently selected for the given
// session, or the fallback model from global config if none has been pinned
// for this session yet.
func activeModelForSession(sessionID string, fallback string) string {
	if sessionID != "" {
		if m, _ := readSessionJSON(sessionID); m != nil {
			if s, ok := m["model"].(string); ok && s != "" {
				return s
			}
		}
	}
	return fallback
}

// ---------------------------------------------------------------------------
// /model — list available models or switch the active one
// ---------------------------------------------------------------------------

func cmdModel() *command.Command {
	return &command.Command{
		Name:        "model",
		Description: "Listar ou trocar o modelo de IA ativo",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			cfg := loadPolypodConfigView()
			current := activeModelForSession(ctx.SessionID, cfg.Model)

			// No args → list available models with a marker on the current one.
			if len(args) == 0 {
				models := modelpicker.ListConfiguredModels(current, cfg.Provider)
				if len(models) == 0 {
					msg := "Nenhum modelo configurado.\n"
					if current != "" {
						msg += fmt.Sprintf("Modelo ativo: %s\n", current)
					}
					return okResult(msg), nil
				}

				var b strings.Builder
				if current != "" {
					fmt.Fprintf(&b, "Modelo ativo: %s\n\n", current)
				} else {
					b.WriteString("Nenhum modelo ativo.\n\n")
				}
				b.WriteString("Modelos disponiveis:\n")
				lastProvider := ""
				for _, m := range models {
					if m.Provider != lastProvider {
						fmt.Fprintf(&b, "\n  [%s]\n", m.Provider)
						lastProvider = m.Provider
					}
					marker := "  "
					if m.Name == current {
						marker = "* "
					}
					fmt.Fprintf(&b, "  %s%s\n", marker, m.Name)
				}
				b.WriteString("\nUse: /model <nome> para trocar o modelo ativo.")
				return okResult(b.String()), nil
			}

			// With a name argument → switch.
			name := strings.TrimSpace(args[0])
			if name == "" {
				return errResult("nome do modelo vazio. Uso: /model <nome>"), nil
			}

			// Validate against the known list so typos fail loudly.
			known := modelpicker.ListConfiguredModels(current, cfg.Provider)
			var match *modelpicker.ModelInfo
			for i := range known {
				if known[i].Name == name {
					match = &known[i]
					break
				}
			}
			if match == nil {
				return errResult(fmt.Sprintf("modelo desconhecido: %q. Use /model sem argumentos para listar.", name)), nil
			}

			if ctx.SessionID == "" {
				return okResult(fmt.Sprintf("Modelo selecionado: %s (sem sessao ativa — troca nao persistida)", name)), nil
			}

			// Persist in the per-session JSON blob.
			m, _ := readSessionJSON(ctx.SessionID)
			if m == nil {
				m = map[string]any{}
			}
			m["model"] = name
			m["provider"] = match.Provider
			if err := writeSessionJSON(ctx.SessionID, m); err != nil {
				return errResult(fmt.Sprintf("falha ao salvar modelo: %v", err)), nil
			}

			return okResult(fmt.Sprintf("Modelo ativo alterado para %s (provider=%s).", name, match.Provider)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /config — read-only human-readable dump of key config values
// ---------------------------------------------------------------------------

func cmdConfig() *command.Command {
	return &command.Command{
		Name:        "config",
		Aliases:     []string{"settings"},
		Description: "Visualizar configuracao ativa (somente leitura)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			cfg := loadPolypodConfigView()
			current := activeModelForSession(ctx.SessionID, cfg.Model)

			var b strings.Builder
			b.WriteString("## Configuracao do Polypod\n\n")

			if cfg.Path != "" {
				fmt.Fprintf(&b, "Arquivo:           %s\n", cfg.Path)
			} else {
				fmt.Fprintf(&b, "Arquivo:           (nenhum config encontrado em %s)\n", configHomeDir())
			}
			fmt.Fprintf(&b, "Diretorio base:    %s\n", configHomeDir())
			fmt.Fprintf(&b, "Diretorio dados:   %s\n", cfg.DataDir)
			b.WriteString("\n")

			b.WriteString("### IA\n")
			provider := cfg.Provider
			if provider == "" {
				provider = "(nao definido)"
			}
			model := current
			if model == "" {
				model = "(nao definido)"
			}
			fmt.Fprintf(&b, "  Provider ativo:  %s\n", provider)
			fmt.Fprintf(&b, "  Modelo ativo:    %s\n", model)
			b.WriteString("\n")

			b.WriteString("### Integracoes\n")
			fmt.Fprintf(&b, "  Hooks ativos:    %d\n", cfg.Hooks)
			fmt.Fprintf(&b, "  Servidores MCP:  %d\n", cfg.MCPCount)
			b.WriteString("\n")

			b.WriteString("### Sessao\n")
			sid := ctx.SessionID
			if sid == "" {
				sid = "(nenhuma)"
			}
			fmt.Fprintf(&b, "  ID:              %s\n", sid)
			fmt.Fprintf(&b, "  Diretorio:       %s\n", ctx.WorkingDir)

			b.WriteString("\nUse /doctor para verificar o estado do sistema.")
			return okResult(b.String()), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /doctor — diagnostic summary with OK/FAIL markers
// ---------------------------------------------------------------------------

// doctorCheck is a minimal local struct so /doctor stays independent of
// internal/doctor's shape (which requires a fully-loaded *config.Config).
type doctorCheck struct {
	Name   string
	OK     bool
	Detail string
}

func cmdDoctor() *command.Command {
	return &command.Command{
		Name:        "doctor",
		Description: "Executar verificacao de saude do sistema",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			cfg := loadPolypodConfigView()
			var checks []doctorCheck

			// 1. Go runtime version.
			checks = append(checks, doctorCheck{
				Name:   "Go runtime",
				OK:     true,
				Detail: runtime.Version() + " (" + runtime.GOOS + "/" + runtime.GOARCH + ")",
			})

			// 2. Binary path.
			if exe, err := os.Executable(); err == nil {
				checks = append(checks, doctorCheck{
					Name:   "Binario",
					OK:     true,
					Detail: exe,
				})
			} else {
				checks = append(checks, doctorCheck{
					Name:   "Binario",
					OK:     false,
					Detail: fmt.Sprintf("os.Executable falhou: %v", err),
				})
			}

			// 3. Config file origin.
			if cfg.Path != "" {
				checks = append(checks, doctorCheck{
					Name:   "Config",
					OK:     true,
					Detail: "carregado de " + cfg.Path,
				})
			} else {
				checks = append(checks, doctorCheck{
					Name:   "Config",
					OK:     false,
					Detail: "nenhum arquivo de configuracao encontrado em " + configHomeDir(),
				})
			}

			// 4. Data directory reachable / writable.
			if cfg.DataDir != "" {
				if st, err := os.Stat(cfg.DataDir); err == nil && st.IsDir() {
					checks = append(checks, doctorCheck{
						Name:   "Diretorio de dados",
						OK:     true,
						Detail: cfg.DataDir,
					})
				} else {
					checks = append(checks, doctorCheck{
						Name:   "Diretorio de dados",
						OK:     false,
						Detail: fmt.Sprintf("%s inacessivel: %v", cfg.DataDir, err),
					})
				}
			}

			// 5. Provider reachability (static check, no network).
			//    We mark OK when we have both provider name and a model name
			//    configured; network probes are deferred to the CLI `doctor`
			//    subcommand which has a full *config.Config available.
			current := activeModelForSession(ctx.SessionID, cfg.Model)
			if cfg.Provider != "" && current != "" {
				checks = append(checks, doctorCheck{
					Name:   "Provider ativo",
					OK:     true,
					Detail: fmt.Sprintf("%s / %s (ping pulado em modo headless)", cfg.Provider, current),
				})
			} else {
				missing := []string{}
				if cfg.Provider == "" {
					missing = append(missing, "provider")
				}
				if current == "" {
					missing = append(missing, "modelo")
				}
				checks = append(checks, doctorCheck{
					Name:   "Provider ativo",
					OK:     false,
					Detail: "nao configurado: " + strings.Join(missing, ", "),
				})
			}

			// 6. MCP servers.
			checks = append(checks, doctorCheck{
				Name:   "Servidores MCP",
				OK:     true,
				Detail: fmt.Sprintf("%d configurado(s)", cfg.MCPCount),
			})

			// 7. Hooks count.
			checks = append(checks, doctorCheck{
				Name:   "Hooks",
				OK:     true,
				Detail: fmt.Sprintf("%d ativo(s)", cfg.Hooks),
			})

			// Sort deterministically by name for stable output.
			sort.SliceStable(checks, func(i, j int) bool {
				return checks[i].Name < checks[j].Name
			})

			// Format.
			var b strings.Builder
			b.WriteString("## Polypod Doctor\n\n")
			okCount, failCount := 0, 0
			for _, c := range checks {
				mark := "[OK]"
				if !c.OK {
					mark = "[XX]"
					failCount++
				} else {
					okCount++
				}
				fmt.Fprintf(&b, "  %s %-22s %s\n", mark, c.Name, c.Detail)
			}
			fmt.Fprintf(&b, "\nResultado: %d ok, %d falha(s)\n", okCount, failCount)
			if failCount == 0 {
				b.WriteString("Polypod parece saudavel.\n")
			} else {
				b.WriteString("Corrija as falhas acima antes de continuar.\n")
			}
			return okResult(b.String()), nil
		},
	}
}
