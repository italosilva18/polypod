package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/costa/polypod/internal/adapter"
	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/ai"
	"github.com/costa/polypod/internal/command"
	"github.com/costa/polypod/internal/config"
	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/hooks"
	"github.com/costa/polypod/internal/mcp"
	"github.com/costa/polypod/internal/memory"
	"github.com/costa/polypod/internal/mentions"
	"github.com/costa/polypod/internal/modes"
	"github.com/costa/polypod/internal/permissions"
	"github.com/costa/polypod/internal/session"
	"github.com/costa/polypod/internal/skill"
	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/tracking"
)

// RestoredMessage represents a message restored from a persisted session
// for display in the CLI viewport.
type RestoredMessage struct {
	Role    string
	Content string
}

// Adapter implements an interactive CLI chat channel using Bubbletea.
type Adapter struct {
	streamHandler     adapter.StreamHandler
	aiSvc             *ai.Service
	memStore          memory.Store
	convMgr           *conversation.Manager
	agents            *agent.Registry
	skills            *skill.Registry
	logger            *slog.Logger
	dataDir           string
	sessStorage       session.SessionStorage
	restoredMessages  []RestoredMessage
	costEngine        *tracking.CostEngine
	userPromptHandler *tool.UserPromptHandler
	mcpMgr            *mcp.Manager
	hookMgr           *hooks.Manager
	permMgr           *permissions.Manager
	modeState         *modes.State
	planSim           *modes.PlanSimulator
	fileIndex         *mentions.FileIndex
	approvalHandler   *BubbleteaApprovalHandler
	cfg               *config.Config
	cfgPath           string
	commandRegistry   *command.Registry
}

// SetCostEngine attaches a CostEngine for the /cost command.
func (a *Adapter) SetCostEngine(ce *tracking.CostEngine) {
	a.costEngine = ce
}

// SetUserPromptHandler attaches the ask-user bidirectional handler.
func (a *Adapter) SetUserPromptHandler(h *tool.UserPromptHandler) {
	a.userPromptHandler = h
}

// SetMCPManager attaches an MCP manager so the /mcp and /doctor commands can
// inspect and manage connected MCP servers.
func (a *Adapter) SetMCPManager(m *mcp.Manager) {
	a.mcpMgr = m
}

// SetHookManager attaches a hooks manager so /hooks and /doctor can list and
// toggle registered hooks.
func (a *Adapter) SetHookManager(h *hooks.Manager) {
	a.hookMgr = h
}

// SetPermissionsManager attaches the permissions manager so the /permissions
// command can list and mutate rules.
func (a *Adapter) SetPermissionsManager(m *permissions.Manager) {
	a.permMgr = m
}

// SetModeState attaches the modes state so the /plan command can toggle plan
// mode and the current operating mode.
func (a *Adapter) SetModeState(s *modes.State) {
	a.modeState = s
}

// SetPlanSimulator attaches the plan-mode simulator so /plan show can list
// the simulated destructive operations queued while plan mode is active.
func (a *Adapter) SetPlanSimulator(p *modes.PlanSimulator) {
	a.planSim = p
}

// SetFileIndex attaches the project file index used by @-mention tab
// completion. When nil, the CLI falls back to on-demand directory
// walks.
func (a *Adapter) SetFileIndex(idx *mentions.FileIndex) {
	a.fileIndex = idx
}

// SetApprovalHandler attaches the Bubbletea-backed approval handler so
// the TUI can display the permission modal for tool-approval requests.
// When nil, approval requests fall back to whatever default handler
// was wired into the enforcer.
func (a *Adapter) SetApprovalHandler(h *BubbleteaApprovalHandler) {
	a.approvalHandler = h
}

// SetConfig attaches the live config and its on-disk path so the
// /config command can read, edit, save and reload it from inside the
// TUI. Without this wiring the /config command will report that
// configuration is unavailable.
func (a *Adapter) SetConfig(cfg *config.Config, path string) {
	a.cfg = cfg
	a.cfgPath = path
}

// SetCommandRegistry attaches the extended slash-command registry. Commands
// handled directly by the TUI keep precedence; the registry is the fallback
// for the larger autonomous command surface.
func (a *Adapter) SetCommandRegistry(reg *command.Registry) {
	a.commandRegistry = reg
}

// New creates a new CLI adapter with all dependencies.
func New(
	streamHandler adapter.StreamHandler,
	aiSvc *ai.Service,
	memStore memory.Store,
	convMgr *conversation.Manager,
	agents *agent.Registry,
	skills *skill.Registry,
	logger *slog.Logger,
	dataDir string,
	sessStorage ...session.SessionStorage,
) *Adapter {
	a := &Adapter{
		streamHandler: streamHandler,
		aiSvc:         aiSvc,
		memStore:      memStore,
		convMgr:       convMgr,
		agents:        agents,
		skills:        skills,
		logger:        logger,
		dataDir:       dataDir,
	}
	if len(sessStorage) > 0 && sessStorage[0] != nil {
		a.sessStorage = sessStorage[0]
	}
	return a
}

func (a *Adapter) Name() string { return "cli" }

// SetRestoredMessages sets messages restored from a previous session
// that will be displayed in the viewport when the CLI starts.
func (a *Adapter) SetRestoredMessages(msgs []RestoredMessage) {
	a.restoredMessages = msgs
}

func (a *Adapter) Start(ctx context.Context, handler adapter.MessageHandler) error {
	deps := a.buildCommandDeps()

	// Use stream handler if available, otherwise wrap the sync handler
	sh := a.streamHandler
	if sh == nil {
		sh = syncToStreamHandler(handler)
	}

	m := newModel(ctx, sh, deps, a.dataDir, a.userPromptHandler, a.approvalHandler, a.fileIndex)

	// Inject restored messages into the display
	if len(a.restoredMessages) > 0 {
		for _, rm := range a.restoredMessages {
			m.messages = append(m.messages, chatEntry{
				role:    rm.Role,
				content: rm.Content,
			})
		}
	}

	// Keep mouse reporting disabled so the terminal can select and copy text.
	// The conversation remains scrollable with PgUp and PgDown.
	p := tea.NewProgram(m, tea.WithAltScreen())

	// Cancel program when context is done
	go func() {
		<-ctx.Done()
		p.Quit()
	}()

	_, err := p.Run()
	if err != nil {
		return fmt.Errorf("cli bubbletea: %w", err)
	}
	return nil
}

func (a *Adapter) Send(ctx context.Context, msg adapter.OutMessage) error {
	return nil
}

// buildCommandDeps creates the closure-based command deps from real implementations.
func (a *Adapter) buildCommandDeps() commandDeps {
	deps := commandDeps{commandRegistry: a.commandRegistry}

	if a.aiSvc != nil {
		deps.activeAgent = a.aiSvc.ActiveAgent
		deps.setAgent = a.aiSvc.SetAgent
		deps.currentModel = a.aiSvc.CurrentModel
		deps.setModel = a.aiSvc.SetModel
	}

	if a.agents != nil {
		deps.listAgents = a.agents.List
	}

	if a.skills != nil {
		deps.listSkills = a.skills.List
	}

	if a.memStore != nil {
		deps.listMemories = func() ([]memoryEntry, error) {
			mems, err := a.memStore.List(context.Background())
			if err != nil {
				return nil, err
			}
			entries := make([]memoryEntry, len(mems))
			for i, m := range mems {
				entries[i] = memoryEntry{topic: m.Topic, content: m.Content}
			}
			return entries, nil
		}
		deps.searchMemories = func(query string) ([]memoryEntry, error) {
			mems, err := a.memStore.Search(context.Background(), query)
			if err != nil {
				return nil, err
			}
			entries := make([]memoryEntry, len(mems))
			for i, m := range mems {
				entries[i] = memoryEntry{topic: m.Topic, content: m.Content}
			}
			return entries, nil
		}
	}

	if a.convMgr != nil {
		deps.clearSession = func() error {
			sess, err := a.convMgr.GetSession(context.Background(), "cli", "local")
			if err != nil {
				return err
			}
			return a.convMgr.ClearSession(context.Background(), sess)
		}
		deps.sessionInfo = func() (string, int, error) {
			sess, err := a.convMgr.GetSession(context.Background(), "cli", "local")
			if err != nil {
				return "", 0, err
			}
			return sess.ID, len(sess.Messages), nil
		}
	}

	if a.costEngine != nil {
		deps.costReport = a.costEngine.FormatDetailedReport
		deps.sessionCost = a.costEngine.SessionCost
		deps.modelBreakdown = a.costEngine.ModelBreakdown
	}

	// /commit: use the AI service to generate a commit message from a prompt.
	if a.aiSvc != nil {
		deps.generateCommitMsg = func(prompt string) (string, error) {
			resp, err := a.aiSvc.Answer(context.Background(), ai.AnswerRequest{
				Channel: "cli",
				UserID:  "local",
				Query:   prompt,
				// No history: this is a one-shot task, not part of the chat.
				History: nil,
			})
			if err != nil {
				return "", err
			}
			return resp.Content, nil
		}

		// /review and /security-review: generic one-shot text generation.
		deps.generateText = func(prompt string) (string, error) {
			resp, err := a.aiSvc.Answer(context.Background(), ai.AnswerRequest{
				Channel: "cli",
				UserID:  "local",
				Query:   prompt,
				History: nil,
			})
			if err != nil {
				return "", err
			}
			return resp.Content, nil
		}
	}

	// /compact: report availability. Actual compaction is handled by the
	// AI service's auto-compact pipeline. A dedicated manual flow would
	// need a wiring hook to compact.CompactService, which is not exposed
	// through the adapter yet.
	deps.compactAvailable = func() bool {
		return a.aiSvc != nil
	}

	if a.mcpMgr != nil {
		deps.listMCPServers = func() []mcpServerInfo {
			raw := a.mcpMgr.ListServers()
			out := make([]mcpServerInfo, 0, len(raw))
			for name, toolNames := range raw {
				out = append(out, mcpServerInfo{
					name:      name,
					transport: "stdio", // manager does not currently expose transport per server
					connected: true,    // Manager.ListServers only returns connected sessions
					toolCount: len(toolNames),
					tools:     toolNames,
				})
			}
			return out
		}
		deps.reconnectMCP = func(name string) error {
			// Best-effort: disconnect + reconnect. A full reconnect would
			// need the original ServerConfig which Manager does not store.
			if err := a.mcpMgr.Disconnect(name); err != nil {
				return err
			}
			return fmt.Errorf("reconexao automatica nao implementada — reinicie o polypod para reconectar %q", name)
		}
	}

	if a.hookMgr != nil {
		deps.listHooks = func() []hookInfo {
			raw := a.hookMgr.List()
			out := make([]hookInfo, 0, len(raw))
			for _, h := range raw {
				out = append(out, hookInfo{
					name:    h.Name,
					event:   string(h.Event),
					hType:   string(h.Type),
					command: h.Command,
					matcher: h.Matcher,
					enabled: h.Enabled,
				})
			}
			return out
		}
		deps.setHookEnabled = func(name string, enabled bool) bool {
			return a.hookMgr.SetEnabled(name, enabled)
		}
	}

	if a.sessStorage != nil {
		deps.listSessions = func(count int) ([]sessionEntry, error) {
			summaries, err := a.sessStorage.List(session.SessionFilter{MaxCount: count})
			if err != nil {
				return nil, err
			}
			entries := make([]sessionEntry, len(summaries))
			for i, s := range summaries {
				entries[i] = sessionEntry{
					id:        s.ID,
					title:     s.Title,
					messages:  s.Messages,
					updatedAt: s.UpdatedAt,
					preview:   s.Preview,
				}
			}
			return entries, nil
		}
		deps.searchSessions = func(query string) ([]sessionEntry, error) {
			summaries, err := a.sessStorage.Search(query)
			if err != nil {
				return nil, err
			}
			entries := make([]sessionEntry, len(summaries))
			for i, s := range summaries {
				entries[i] = sessionEntry{
					id:        s.ID,
					title:     s.Title,
					messages:  s.Messages,
					updatedAt: s.UpdatedAt,
					preview:   s.Preview,
				}
			}
			return entries, nil
		}

		// /resume: load a persisted session into the active conversation.
		deps.loadSession = func(id string) ([]chatEntry, error) {
			restored, err := a.sessStorage.Load(id)
			if err != nil {
				return nil, err
			}
			if restored == nil {
				return nil, fmt.Errorf("sessao %s nao encontrada", id)
			}
			entries := make([]chatEntry, 0, len(restored.Messages))
			for _, m := range restored.Messages {
				entries = append(entries, chatEntry{
					role:    m.Role,
					content: m.Content,
				})
			}

			// Also replay the messages into the active conversation
			// store if wired, so the next AI turn has full history.
			if a.convMgr != nil {
				sess, sessErr := a.convMgr.GetSession(context.Background(), "cli", "local")
				if sessErr == nil {
					conv := make([]conversation.Message, 0, len(restored.Messages))
					for _, m := range restored.Messages {
						role := conversation.RoleUser
						switch m.Role {
						case "assistant":
							role = conversation.RoleAssistant
						case "system":
							role = conversation.RoleSystem
						}
						conv = append(conv, conversation.Message{
							Role:    role,
							Content: m.Content,
						})
					}
					_ = a.convMgr.ReplaceMessages(context.Background(), sess, conv)
				}
			}
			return entries, nil
		}
	}

	// /rewind: drop the last N messages from the active conversation.
	if a.convMgr != nil {
		deps.truncateMessages = func(n int) (int, error) {
			sess, err := a.convMgr.GetSession(context.Background(), "cli", "local")
			if err != nil {
				return 0, err
			}
			removed, err := a.convMgr.TruncateMessages(context.Background(), sess, n)
			return len(removed), err
		}
	}

	// /permissions: expose the permissions manager.
	if a.permMgr != nil {
		deps.permissionsList = func() [][3]string {
			rules := a.permMgr.Rules()
			out := make([][3]string, 0, len(rules))
			for _, r := range rules {
				out = append(out, [3]string{r.Pattern, r.Decision, r.Source})
			}
			return out
		}
		deps.permissionsAdd = func(decision, pattern string) error {
			a.permMgr.AddRule(permissions.Rule{
				Pattern:  pattern,
				Decision: decision,
				Source:   "session",
			})
			return persistPermissions(a.permMgr)
		}
		deps.permissionsRemove = func(pattern string) (int, error) {
			n := a.permMgr.RemoveRulesForTool(pattern)
			if n == 0 {
				return 0, nil
			}
			return n, persistPermissions(a.permMgr)
		}
		deps.permissionsReset = func() error {
			a.permMgr.ResetRules()
			return persistPermissions(a.permMgr)
		}
	}

	// /plan: expose mode state and plan simulator.
	if a.modeState != nil {
		deps.planCurrent = func() string {
			return string(a.modeState.Current)
		}
		deps.planSet = func(mode string) (string, error) {
			return a.modeState.SetMode(mode)
		}
	}
	if a.planSim != nil {
		deps.planSummary = func() string {
			return a.planSim.FormatPlanSummary()
		}
		deps.planExecute = func() (string, error) {
			summary := a.planSim.FormatPlanSummary()
			// Switch back to edit mode so real execution can happen on
			// the next user prompt; the simulator is drained.
			if a.modeState != nil {
				if _, err := a.modeState.SetMode("edit"); err != nil {
					return summary, err
				}
			}
			a.planSim.Reset()
			return summary, nil
		}
	}

	// /config: expose live config snapshot, get/set/save/reload.
	if a.cfg != nil {
		deps.configSnapshot = func() *config.Config { return a.cfg }
		deps.configPath = func() string { return a.cfgPath }
		deps.configGet = func(key string) (string, bool) {
			return getConfigValue(a.cfg, key)
		}
		deps.configSet = func(key, value string) error {
			return setConfigValue(a.cfg, key, value)
		}
		deps.configSave = func() error {
			if a.cfgPath == "" {
				return fmt.Errorf("nenhum caminho de config configurado")
			}
			data, err := a.cfg.Marshal()
			if err != nil {
				return err
			}
			return os.WriteFile(a.cfgPath, data, 0644)
		}
		deps.configReload = func() error {
			if a.cfgPath == "" {
				return fmt.Errorf("nenhum caminho de config configurado")
			}
			cfg, err := config.Load(a.cfgPath)
			if err != nil {
				return err
			}
			*a.cfg = *cfg
			return nil
		}
	}

	return deps
}

// getConfigValue returns the string representation of a dotted-path
// key on the polypod config struct, plus an "ok" indicating whether
// the key was recognised.
func getConfigValue(cfg *config.Config, key string) (string, bool) {
	if cfg == nil {
		return "", false
	}
	switch strings.ToLower(key) {
	case "ai.provider":
		return cfg.AI.Provider, true
	case "ai.model":
		return cfg.AI.Model, true
	case "ai.api_key":
		if cfg.AI.APIKey == "" {
			return "", true
		}
		// Mask in get for safety; the form has its own renderer.
		if len(cfg.AI.APIKey) > 6 {
			return cfg.AI.APIKey[:3] + "***" + cfg.AI.APIKey[len(cfg.AI.APIKey)-3:], true
		}
		return "***", true
	case "ai.max_tokens":
		return strconv.Itoa(cfg.AI.MaxToks), true
	case "ai.temperature":
		return strconv.FormatFloat(float64(cfg.AI.Temp), 'f', 2, 32), true
	case "ai.tools":
		return strconv.FormatBool(cfg.AI.Tools), true
	case "ai.base_url":
		return cfg.AI.BaseURL, true
	case "data.dir":
		return cfg.Data.Dir, true
	case "data.agents_dir":
		return cfg.Data.AgentsDir, true
	case "log.level":
		return cfg.Log.Level, true
	case "log.format":
		return cfg.Log.Format, true
	}
	return "", false
}

// setConfigValue updates a dotted-path key on the polypod config
// struct, parsing typed values as needed.
func setConfigValue(cfg *config.Config, key, value string) error {
	if cfg == nil {
		return fmt.Errorf("config nil")
	}
	switch strings.ToLower(key) {
	case "ai.provider":
		cfg.AI.Provider = value
	case "ai.model":
		cfg.AI.Model = value
	case "ai.api_key":
		cfg.AI.APIKey = value
	case "ai.max_tokens":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("max_tokens deve ser inteiro: %w", err)
		}
		cfg.AI.MaxToks = n
	case "ai.temperature":
		f, err := strconv.ParseFloat(value, 32)
		if err != nil {
			return fmt.Errorf("temperature deve ser numero: %w", err)
		}
		cfg.AI.Temp = float32(f)
	case "ai.tools":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("tools deve ser bool: %w", err)
		}
		cfg.AI.Tools = b
	case "ai.base_url":
		cfg.AI.BaseURL = value
	case "data.dir":
		cfg.Data.Dir = value
	case "data.agents_dir":
		cfg.Data.AgentsDir = value
	case "log.level":
		cfg.Log.Level = value
	case "log.format":
		cfg.Log.Format = value
	default:
		return fmt.Errorf("chave desconhecida: %s", key)
	}
	return nil
}

// persistPermissions writes the current in-memory rule set back to
// .polypod/settings.json so session changes survive restarts.
func persistPermissions(mgr *permissions.Manager) error {
	settings := permissions.Settings{}
	for _, r := range mgr.Rules() {
		switch r.Decision {
		case "allow":
			settings.AllowedTools = append(settings.AllowedTools, r)
		case "deny":
			settings.DeniedTools = append(settings.DeniedTools, r)
		case "ask":
			settings.AskTools = append(settings.AskTools, r)
		}
	}
	return permissions.SaveProjectSettings(settings)
}

// syncToStreamHandler wraps a synchronous MessageHandler as a StreamHandler for fallback.
func syncToStreamHandler(handler adapter.MessageHandler) adapter.StreamHandler {
	return func(ctx context.Context, msg adapter.InMessage, chunks chan<- adapter.StreamChunk) {
		defer close(chunks)

		out, err := handler(ctx, msg)
		if err != nil {
			chunks <- adapter.StreamChunk{Error: err, Done: true}
			return
		}
		chunks <- adapter.StreamChunk{Delta: out.Text}
		chunks <- adapter.StreamChunk{Done: true}
	}
}
