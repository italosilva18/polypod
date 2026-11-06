package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sashabaranov/go-openai/jsonschema"
	"gopkg.in/yaml.v3"

	"github.com/costa/polypod/internal/adapter"
	cliAdapter "github.com/costa/polypod/internal/adapter/cli"
	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/ai"
	"github.com/costa/polypod/internal/approval"
	"github.com/costa/polypod/internal/auth"
	"github.com/costa/polypod/internal/automemory"
	"github.com/costa/polypod/internal/autoupdate"
	"github.com/costa/polypod/internal/codemap"
	"github.com/costa/polypod/internal/commands"
	"github.com/costa/polypod/internal/compact"
	"github.com/costa/polypod/internal/completion"
	"github.com/costa/polypod/internal/config"
	"github.com/costa/polypod/internal/configmerge"
	"github.com/costa/polypod/internal/configval"
	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/database"
	"github.com/costa/polypod/internal/debug"
	"github.com/costa/polypod/internal/depsgraph"
	"github.com/costa/polypod/internal/docgen"
	"github.com/costa/polypod/internal/doctor"
	"github.com/costa/polypod/internal/dotenv"
	"github.com/costa/polypod/internal/git"
	"github.com/costa/polypod/internal/githook"
	"github.com/costa/polypod/internal/headless"
	"github.com/costa/polypod/internal/hooks"
	"github.com/costa/polypod/internal/initcmd"
	"github.com/costa/polypod/internal/mcp"
	"github.com/costa/polypod/internal/memory"
	"github.com/costa/polypod/internal/migrate"
	"github.com/costa/polypod/internal/multiread"
	"github.com/costa/polypod/internal/observability"
	"github.com/costa/polypod/internal/openapitools"
	"github.com/costa/polypod/internal/permissions"
	"github.com/costa/polypod/internal/profiling"
	"github.com/costa/polypod/internal/project"
	"github.com/costa/polypod/internal/provider"
	"github.com/costa/polypod/internal/prstatus"
	"github.com/costa/polypod/internal/review"
	"github.com/costa/polypod/internal/sandbox"
	"github.com/costa/polypod/internal/scheduler"
	"github.com/costa/polypod/internal/secrets"
	"github.com/costa/polypod/internal/security"
	"github.com/costa/polypod/internal/selfmod"
	"github.com/costa/polypod/internal/session"
	"github.com/costa/polypod/internal/setup"
	"github.com/costa/polypod/internal/skill"
	"github.com/costa/polypod/internal/sshexec"
	"github.com/costa/polypod/internal/template"
	"github.com/costa/polypod/internal/testgen"
	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/tracking"
	"github.com/costa/polypod/internal/types"
	"github.com/costa/polypod/internal/web"
	"github.com/costa/polypod/internal/wiring"
)

const defaultConfigPath = "config.yaml"

func main() {
	if handled := handleSubcommand(); handled {
		return
	}

	configPath, runSetup, headlessPrompt, headlessFormat, continueSession, resumeSessionID := parseArgs()

	if runSetup {
		if err := setup.Run(configPath); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
	}

	// Load .env files BEFORE config (so env vars are available for ${} substitution)
	dotenv.Load(".")
	for _, w := range dotenv.CheckSecurity(".") {
		fmt.Fprintln(os.Stderr, w)
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	// Force CLI on in CLI-only build
	if !cfg.CLI.Enabled {
		cfg.CLI.Enabled = true
	}

	// Auth: KeyStore + MultiProviderAuth
	keyStore := auth.NewKeyStore(cfg.Data.Dir)
	multiAuth := auth.NewMultiProviderAuth(keyStore)
	seedKeyStore(keyStore, cfg)

	if headlessPrompt == "" {
		if err := setup.CheckAPIKey(cfg, configPath); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
	}

	logger := buildLogger(cfg, headlessPrompt)
	slog.SetDefault(logger)
	logger.Info("polypod starting", "version", autoupdate.CurrentVersion())

	// Check for updates (async, non-blocking)
	go func() {
		if release, err := autoupdate.CheckForUpdate(); err == nil && release != nil {
			logger.Info("update available", "version", release.TagName)
			if headlessPrompt == "" {
				fmt.Fprintln(os.Stderr, autoupdate.FormatUpdateNotification(release))
			}
		}
	}()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Database (sqlite-only in CLI build)
	var sqliteDB *database.SQLiteDB
	if cfg.Database.Enabled {
		sqliteDB, err = database.NewSQLite(cfg.Database.Path, logger)
		if err != nil {
			logger.Error("sqlite connection failed", "error", err)
			os.Exit(1)
		}
		defer sqliteDB.Close()
		if err := sqliteDB.Migrate(ctx); err != nil {
			logger.Error("sqlite migration failed", "error", err)
			os.Exit(1)
		}
	}

	if err := run(ctx, cfg, configPath, sqliteDB, logger, headlessPrompt, headlessFormat, continueSession, resumeSessionID, multiAuth); err != nil {
		logger.Error("fatal error", "error", err)
		os.Exit(1)
	}

	logger.Info("polypod stopped")
}

// handleSubcommand processes subcommands that exit before config is loaded.
// Returns true if a subcommand was handled.
func handleSubcommand() bool {
	if len(os.Args) < 2 {
		return false
	}
	switch os.Args[1] {
	case "doctor":
		cfg, _ := config.Load(defaultConfigPath)
		if cfg == nil {
			cfg = config.DefaultConfig()
		}
		fmt.Print(doctor.FormatReport(doctor.RunDiagnostics(cfg)))
		return true
	case "init":
		stack := initcmd.Detect(".")
		files, err := initcmd.WriteInit(".", stack)
		if err != nil {
			fmt.Fprintf(os.Stderr, "erro: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Projeto inicializado. %d arquivo(s) criado(s):\n", len(files))
		for _, f := range files {
			fmt.Printf("  - %s\n", f)
		}
		return true
	case "completion":
		shell := "bash"
		if len(os.Args) >= 3 {
			shell = os.Args[2]
		}
		script, err := completion.Generate(shell)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		fmt.Print(script)
		return true
	case "hook":
		action := "status"
		if len(os.Args) >= 3 {
			action = os.Args[2]
		}
		switch action {
		case "install":
			installed, err := githook.Install()
			if err != nil {
				fmt.Fprintf(os.Stderr, "erro: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Hooks instalados: %s\n", strings.Join(installed, ", "))
		case "uninstall":
			removed, _ := githook.Uninstall()
			fmt.Printf("Hooks removidos: %s\n", strings.Join(removed, ", "))
		case "status":
			active, _ := githook.Status()
			if len(active) == 0 {
				fmt.Println("Nenhum hook polypod instalado.")
			} else {
				fmt.Printf("Hooks ativos: %s\n", strings.Join(active, ", "))
			}
		}
		return true
	case "--version", "-v":
		fmt.Printf("polypod v%s\n", autoupdate.CurrentVersion())
		return true
	}
	return false
}

// loadConfig loads config.yaml plus optional config.d/ fragments and validates.
func loadConfig(configPath string) (*config.Config, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}

	if fragments, err := configmerge.MergeConfigDir("config.d"); err == nil && len(fragments) > 0 {
		if origBytes, marshalErr := cfg.Marshal(); marshalErr == nil {
			if merged, mergeErr := configmerge.MergeIntoConfig(origBytes, fragments); mergeErr == nil {
				var mergedCfg config.Config
				if yaml.Unmarshal(merged, &mergedCfg) == nil {
					cfg = &mergedCfg
					fmt.Fprintf(os.Stderr, "merged %d config.d fragment(s)\n", len(fragments))
				}
			}
		}
	}

	if errs := configval.Validate(cfg); len(errs) > 0 {
		fmt.Fprint(os.Stderr, configval.FormatErrors(errs))
		if configval.HasErrors(errs) {
			os.Exit(1)
		}
	}
	return cfg, nil
}

// seedKeyStore copies API keys from the config into the secure KeyStore on first run.
func seedKeyStore(keyStore *auth.KeyStore, cfg *config.Config) {
	if cfg.AI.APIKey != "" && cfg.AI.Provider != "" {
		if existing, _ := keyStore.GetKey(cfg.AI.Provider); existing == "" {
			_ = keyStore.SetKey(cfg.AI.Provider, cfg.AI.APIKey)
		}
	}
	for _, p := range cfg.Providers {
		if p.APIKey != "" && p.Name != "" {
			if existing, _ := keyStore.GetKey(p.Name); existing == "" {
				_ = keyStore.SetKey(p.Name, p.APIKey)
			}
		}
	}
}

// buildLogger constructs a slog.Logger appropriate for headless or interactive mode.
func buildLogger(cfg *config.Config, headlessPrompt string) *slog.Logger {
	logPath := filepath.Join(cfg.Data.Dir, "polypod.log")
	os.MkdirAll(cfg.Data.Dir, 0755)

	level := cfg.Log.Level
	if headlessPrompt != "" {
		level = "warn"
	}

	logger, err := observability.NewLoggerToFile(level, cfg.Log.Format, logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating log file: %v\n", err)
		os.Exit(1)
	}
	return logger
}

func parseArgs() (configPath string, runSetup bool, headlessPrompt string, headlessFormat string, continueSession bool, resumeSessionID string) {
	configPath = defaultConfigPath
	headlessFormat = "text"

	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		switch {
		case arg == "--setup":
			runSetup = true
		case arg == "-p" && i+1 < len(os.Args):
			i++
			headlessPrompt = os.Args[i]
		case arg == "--output-format" && i+1 < len(os.Args):
			i++
			headlessFormat = os.Args[i]
		case arg == "-c" || arg == "--continue":
			continueSession = true
		case arg == "-r" || arg == "--resume":
			if i+1 < len(os.Args) && !strings.HasPrefix(os.Args[i+1], "-") {
				i++
				resumeSessionID = os.Args[i]
			}
		case !strings.HasPrefix(arg, "-"):
			configPath = arg
		}
	}

	if headlessPrompt == "" && !runSetup {
		if _, err := os.Stat(defaultConfigPath); os.IsNotExist(err) {
			runSetup = true
		}
	}

	return
}

// registerSkills bundles all the skill registrations into one place.
func registerSkills(skills *skill.Registry, memStore memory.Store, agents *agent.Registry, agentsDir, dataDir string, logger *slog.Logger) {
	memory.RegisterSkills(skills, memStore)
	secrets.RegisterSkills(skills, secrets.NewVault(filepath.Join(dataDir, "secrets.enc"), ""))
	web.RegisterSkills(skills)
	selfmod.RegisterSkills(skills, agents, agentsDir)

	customSkillsDir := "data/skills"
	os.MkdirAll(customSkillsDir, 0755)
	skill.LoadAndRegisterCustomSkills(skills, customSkillsDir)
	skill.RegisterDynamicManagement(skills, customSkillsDir)

	git.RegisterSkills(skills)
	git.RegisterFileSkills(skills)
	review.RegisterSkills(skills)
	security.RegisterSkills(skills)
	codemap.RegisterSkills(skills, ".", logger)
	multiread.RegisterSkills(skills)

	testgen.RegisterSkills(skills)
	docgen.RegisterSkills(skills)
	profiling.RegisterSkills(skills)
	openapitools.RegisterSkills(skills)
	sshexec.RegisterSkills(skills)
	depsgraph.RegisterSkills(skills)
	migrate.RegisterSkills(skills)
}

// loadRestoredSession resolves -c/-r into an optional restored session.
// Returns the restored session (or nil) and the current session ID to use.
func loadRestoredSession(restorer *session.SessionRestorer, storage session.SessionStorage, continueSession bool, resumeSessionID string, logger *slog.Logger) (*session.RestoredSession, string) {
	currentSessionID := session.GenerateSessionID()

	var restored *session.RestoredSession
	switch {
	case continueSession:
		summaries, err := storage.List(session.SessionFilter{Channel: "cli", MaxCount: 1})
		if err != nil || len(summaries) == 0 {
			logger.Info("no previous session found to continue")
			return nil, currentSessionID
		}
		r, err := restorer.RestoreSession(summaries[0].ID)
		if err != nil {
			logger.Warn("failed to restore latest session", "error", err)
			return nil, currentSessionID
		}
		restored = r
	case resumeSessionID != "":
		r, err := restorer.RestoreSession(resumeSessionID)
		if err != nil {
			logger.Error("failed to resume session", "id", resumeSessionID, "error", err)
			return nil, currentSessionID
		}
		if r == nil {
			logger.Error("session not found", "id", resumeSessionID)
			return nil, currentSessionID
		}
		restored = r
	default:
		return nil, currentSessionID
	}

	for _, w := range restorer.ValidateRestoredSession(restored) {
		logger.Warn("session warning", "warning", w)
	}
	logger.Info("session restored", "id", restored.ID, "title", restored.Title, "messages", len(restored.Messages))
	return restored, restored.ID
}

// buildCompactService creates a compact.CompactService that summarizes via the
// (late-bound) ai.Client. The aiClient pointer is captured by reference.
func buildCompactService(aiClientRef **ai.Client, logger *slog.Logger) *compact.CompactService {
	cfg := compact.DefaultConfig()
	return compact.NewCompactService(cfg, func(ctx context.Context, systemPrompt string, messages []conversation.Message, userPrompt string) (string, error) {
		client := *aiClientRef
		if client == nil {
			return "", fmt.Errorf("ai client not available for compaction")
		}

		parts := make([]string, 0, len(messages)+2)
		if systemPrompt != "" {
			parts = append(parts, "SYSTEM: "+systemPrompt)
		}
		for _, m := range messages {
			parts = append(parts, fmt.Sprintf("%s: %s", strings.ToUpper(string(m.Role)), m.Content))
		}
		if userPrompt != "" {
			parts = append(parts, "USER: "+userPrompt)
		}

		prompt := `Resuma a conversa abaixo em portugues. Mantenha:
- As decisoes tomadas
- Os arquivos modificados
- O contexto essencial para continuar a conversa
- Qualquer informacao critica

Seja conciso mas nao perca informacao relevante.

CONVERSA:
` + strings.Join(parts, "\n\n---\n\n") + `

RESUMO:`

		response, err := client.Complete(ctx, []ai.ChatMessage{{Role: "user", Content: prompt}})
		if err != nil {
			return "", fmt.Errorf("summarization failed: %w", err)
		}
		return response.Content, nil
	}, logger)
}

// buildProviderRegistry creates the provider.Registry, resolving keys via multiAuth.
func buildProviderRegistry(cfg *config.Config, multiAuth *auth.MultiProviderAuth) (*provider.Registry, []provider.Provider) {
	reg := provider.NewRegistry()

	primaryKey := cfg.AI.APIKey
	if cred, err := multiAuth.GetCredentials(cfg.AI.Provider); err == nil {
		primaryKey = cred.Token()
	}
	reg.Register(provider.NewOpenAICompat(cfg.AI.Provider, primaryKey, cfg.AI.BaseURL))

	for _, p := range cfg.Providers {
		key := p.APIKey
		if cred, err := multiAuth.GetCredentials(p.Name); err == nil && cred.Token() != "" {
			key = cred.Token()
		}
		switch p.Name {
		case "ollama":
			reg.Register(provider.NewOllama(p.BaseURL))
		case "anthropic":
			reg.Register(provider.NewAnthropic(key, p.BaseURL))
		case "google":
			reg.Register(provider.NewGoogle(key, p.BaseURL))
		case "openai":
			reg.Register(provider.NewOpenAICompat("openai", key, p.BaseURL))
		default:
			reg.Register(provider.ProviderFactory(p.Name, key, p.BaseURL))
		}
	}

	var list []provider.Provider
	for _, name := range reg.List() {
		if p, ok := reg.Get(name); ok {
			list = append(list, p)
		}
	}
	return reg, list
}

// makeHandler builds the inline message handler that replaces router.Handler.
func makeHandler(convMgr *conversation.Manager, aiSvc *ai.Service) adapter.MessageHandler {
	return func(ctx context.Context, msg adapter.InMessage) (adapter.OutMessage, error) {
		sess, err := convMgr.GetSession(ctx, msg.Channel, msg.UserID)
		if err != nil {
			return adapter.OutMessage{Text: "Erro de sessao"}, nil
		}
		convMgr.AddUserMessage(ctx, sess, msg.Text)
		resp, err := aiSvc.Answer(ctx, ai.AnswerRequest{
			Channel: msg.Channel,
			UserID:  msg.UserID,
			Query:   msg.Text,
			History: convMgr.GetHistory(sess),
		})
		if err != nil {
			return adapter.OutMessage{Text: "Erro ao processar: " + err.Error()}, nil
		}
		convMgr.AddAssistantMessage(ctx, sess, resp.Content)
		return adapter.OutMessage{
			Text:    resp.Content,
			Channel: msg.Channel,
			UserID:  msg.UserID,
			ReplyTo: msg.ID,
		}, nil
	}
}

// makeStreamHandler builds the inline stream handler that replaces router.StreamHandler.
func makeStreamHandler(convMgr *conversation.Manager, aiSvc *ai.Service) adapter.StreamHandler {
	return func(ctx context.Context, msg adapter.InMessage, chunks chan<- adapter.StreamChunk) {
		defer close(chunks)

		aiSvc.SetToolEventCallback(func(ev ai.ToolEvent) {
			chunks <- adapter.StreamChunk{
				ToolEvent: &adapter.ToolEventInfo{
					Type:     adapter.ToolEventType(ev.Type),
					ToolName: ev.ToolName,
					ToolID:   ev.ToolID,
					Args:     ev.Args,
					Result:   ev.Result,
					Error:    ev.Error,
					Elapsed:  ev.Elapsed,
				},
			}
		})
		defer aiSvc.SetToolEventCallback(nil)

		sess, err := convMgr.GetSession(ctx, msg.Channel, msg.UserID)
		if err != nil {
			chunks <- adapter.StreamChunk{Error: err, Done: true}
			return
		}
		convMgr.AddUserMessage(ctx, sess, msg.Text)

		resp, err := aiSvc.AnswerStream(ctx, ai.AnswerRequest{
			Channel: msg.Channel,
			UserID:  msg.UserID,
			Query:   msg.Text,
			History: convMgr.GetHistory(sess),
		}, func(delta string) {
			chunks <- adapter.StreamChunk{Delta: delta}
		})
		if err != nil {
			chunks <- adapter.StreamChunk{Error: err, Done: true}
			return
		}
		convMgr.AddAssistantMessage(ctx, sess, resp.Content)
		chunks <- adapter.StreamChunk{Done: true}
	}
}

// autoSaveSession persists the current CLI conversation to disk on shutdown.
func autoSaveSession(convMgr *conversation.Manager, sessStorage session.SessionStorage, sessMgr *session.Manager, currentSessionID string, logger *slog.Logger) {
	sess, err := convMgr.GetSession(context.Background(), "cli", "local")
	if err != nil || len(sess.Messages) == 0 {
		return
	}

	now := time.Now()
	cwd, _ := os.Getwd()

	persistedMsgs := make([]session.Message, len(sess.Messages))
	for i, m := range sess.Messages {
		persistedMsgs[i] = session.Message{Role: string(m.Role), Content: m.Content}
	}

	title := session.TitleFromFirstMessage(persistedMsgs)

	persisted := &session.RestoredSession{
		ID:        currentSessionID,
		Title:     title,
		Messages:  persistedMsgs,
		CreatedAt: sess.CreatedAt,
		UpdatedAt: now,
		Version:   session.CurrentSessionVersion,
		Metadata: session.SessionMetadata{
			ProjectRoot: cwd,
			Tag:         "cli",
			Mode:        "interactive",
		},
	}
	if env := session.DetectEnvironment(cwd); env != nil && env.GitBranch != "" {
		persisted.Metadata.GitBranch = env.GitBranch
	}

	if err := sessStorage.Save(persisted); err != nil {
		logger.Warn("failed to auto-save session", "error", err)
	} else {
		logger.Info("session auto-saved", "id", currentSessionID, "messages", len(persistedMsgs), "title", title)
	}

	sessMgr.SaveSessionIndex(session.SessionIndex{
		ID:        currentSessionID,
		Channel:   "cli",
		UserID:    "local",
		Title:     title,
		CreatedAt: sess.CreatedAt,
		UpdatedAt: now,
		Messages:  len(persistedMsgs),
	})
}

func run(ctx context.Context, cfg *config.Config, configPath string, sqliteDB *database.SQLiteDB, logger *slog.Logger, headlessPrompt, headlessFormat string, continueSession bool, resumeSessionID string, multiAuth *auth.MultiProviderAuth) error {
	var sqlDB *sql.DB
	if sqliteDB != nil {
		sqlDB = sqliteDB.DB
	}

	// Approval audit logger (SECURITY CRITICAL): install a file-backed audit
	// sink as early as possible so every approval decision downstream is
	// persisted. Falls back to the noop logger if the file cannot be opened.
	if fl, err := approval.NewFileAuditLogger(approval.DefaultAuditLogPath()); err == nil {
		approval.SetAuditLogger(fl)
		defer fl.Close()
	} else {
		slog.Warn("approval: falling back to noop audit logger", "err", err)
	}

	// Conversation manager
	dataDir := ""
	if sqlDB == nil {
		dataDir = cfg.Data.Dir
	}
	store := conversation.NewStore(nil, sqlDB, dataDir)
	convMgr := conversation.NewManager(store, logger)
	if sessions := store.ListSessions(); len(sessions) > 0 {
		logger.Info("loaded conversations", "count", len(sessions))
	}

	// Wire core subsystems via wiring.WireAll
	memStore := memory.NewStoreFromDB(sqlDB, cfg.Data.Dir)
	sys := wiring.WireAll(wiring.Config{
		Logger:      logger,
		WorkingDir:  ".",
		Model:       cfg.AI.Model,
		DataDir:     cfg.Data.Dir,
		MemoryStore: memStore,
	})

	tool.RegisterAdvancedTools(sys.ToolRegistry, ".")
	// New Wave 1 tools
	for _, t := range tool.NewSendMessageTools() {
		sys.ToolRegistry.Register(t)
	}
	agent.RegisterAutonomousAgents(sys.Agents)
	agent.RegisterBuiltinAgents(sys.Agents)

	// Skill registry — load + register all built-in skills
	skills := sys.SkillRegistry
	agents := sys.Agents
	agents.LoadDir(cfg.Data.AgentsDir)
	registerSkills(skills, memStore, agents, cfg.Data.AgentsDir, cfg.Data.Dir, logger)

	// Filesystem skill loader — scans ~/.claude/skills, ./claude/skills and
	// ./.polypod/skills for SKILL.md files and registers each as a simple
	// body-returning skill in the runtime registry.
	fsLoader := skill.NewFSSkillLoader(skill.DefaultSkillDirs()...)
	if parsed, err := fsLoader.LoadAll(ctx); err == nil {
		for _, ps := range parsed {
			if err := ps.Frontmatter.Validate(); err != nil {
				continue
			}
			body := ps.Body
			skills.Register(&skill.Skill{
				Name:        ps.Frontmatter.Name,
				Description: ps.Frontmatter.Description,
				Parameters:  jsonschema.Definition{Type: jsonschema.Object},
				Execute:     func(map[string]string) (string, error) { return body, nil },
			})
		}
	}

	// Plugin-based skills
	pluginLoader := skill.NewPluginLoader(skill.DefaultSkillDirs(), logger)
	if n, err := pluginLoader.LoadAll(skills); err != nil {
		logger.Warn("plugin skill loader error", "error", err)
	} else if n > 0 {
		logger.Info("plugin skills loaded", "count", n)
	}

	// Skill hot-reload
	if skillWatcher, err := skill.NewSkillWatcher(skill.DefaultSkillDirs(), fsLoader, skills, logger); err != nil {
		logger.Warn("skill watcher unavailable", "error", err)
	} else {
		go skillWatcher.Start(ctx)
	}

	// Session managers
	sessMgr := session.NewManager(cfg.Data.Dir, logger)
	session.RegisterSkills(skills, sessMgr)
	sessionsDir := filepath.Join(cfg.Data.Dir, "sessions")
	sessStorage := session.NewFileSystemStorage(sessionsDir, logger)
	sessRestorer := session.NewSessionRestorer(sessionsDir, logger)

	restoredSession, currentSessionID := loadRestoredSession(sessRestorer, sessStorage, continueSession, resumeSessionID, logger)

	// If we have a restored session, inject messages into the conversation store
	if restoredSession != nil {
		if sess, err := convMgr.GetSession(ctx, "cli", "local"); err == nil {
			for _, msg := range restoredSession.Messages {
				sess.AddMessage(conversation.Role(msg.Role), msg.Content)
			}
			if err := store.Save(ctx, sess); err != nil {
				logger.Warn("failed to save restored messages to conversation store", "error", err)
			}
			logger.Info("restored messages into conversation", "count", len(restoredSession.Messages))
		}
	}

	if projCtx := project.InjectProjectContext(); projCtx != "" {
		logger.Info("project instructions loaded")
	}

	// Auto-memory
	autoMem := automemory.NewStore(cfg.Data.Dir, logger)

	// Tracking
	tracker := sys.Tracker
	tracking.RegisterSkills(skills, tracker)

	// Sandbox
	sb := sandbox.New(sandbox.Config{
		Enabled:     cfg.Sandbox.Enabled,
		Image:       cfg.Sandbox.Image,
		MemoryLimit: cfg.Sandbox.MemoryLimit,
		CPULimit:    cfg.Sandbox.CPULimit,
		Timeout:     cfg.Sandbox.Timeout,
		Network:     cfg.Sandbox.Network,
	}, logger)
	sandbox.RegisterSkills(skills, sb)

	// Scheduler
	sched := scheduler.New(cfg.Scheduler.DataFile, logger)
	sched.Load()
	scheduler.RegisterSkills(skills, sched)
	if cfg.Scheduler.Enabled {
		go sched.Start(ctx)
	}

	// Templates
	tmplReg := template.NewRegistry(cfg.Templates.Dir)
	tmplReg.LoadAll()
	tmplReg.RegisterSkills(skills)

	// MCP
	mcpMgr := mcp.NewManager(logger)
	for _, mcpCfg := range cfg.MCP {
		if !mcpCfg.AutoStart {
			continue
		}
		if err := mcpMgr.Connect(ctx, mcp.ServerConfig{
			Name: mcpCfg.Name, Transport: mcpCfg.Transport,
			Command: mcpCfg.Command, Args: mcpCfg.Args,
			URL: mcpCfg.URL, Env: mcpCfg.Env,
		}); err != nil {
			logger.Warn("mcp server failed", "name", mcpCfg.Name, "error", err)
		}
	}
	mcpToolCount := mcpMgr.RegisterTools(skills)
	mcp.RegisterSkills(skills, mcpMgr)
	if mcpToolCount > 0 {
		logger.Info("mcp tools loaded", "count", mcpToolCount)
	}
	defer mcpMgr.Close()

	// Hooks
	hookMgr := sys.Hooks
	for _, h := range cfg.Hooks {
		hookMgr.Register(hooks.Hook{
			Name: h.Name, Event: hooks.Event(h.Event),
			Type: hooks.HandlerType(h.Type), Command: h.Command,
			URL: h.URL, Matcher: h.Matcher,
			Timeout: h.Timeout, Enabled: h.Enabled,
		})
	}

	// Permissions + Modes
	permMgr := sys.Permissions
	if err := permMgr.LoadFromFiles(); err != nil {
		logger.Warn("permission settings load failed", "error", err)
	}
	modeState := sys.Modes
	unrestricted := cfg.Permissions != nil && cfg.Permissions.PermissionMode() == "bypassPermissions"
	if unrestricted {
		permMgr.SetMode(types.ModeBypass)
		if _, modeErr := modeState.SetMode("auto"); modeErr != nil {
			logger.Warn("failed to activate autonomous mode", "error", modeErr)
		}
		logger.Warn("unrestricted agent mode active", "permissions", "bypassPermissions", "sandbox", cfg.Sandbox.Enabled)
	}
	permMgr.SetModeBlocked(modeState.BlockedTools())

	// Approval handler + Enforcer.
	//
	// In headless mode the policy-based handler auto-decides every
	// request. In interactive mode we create a Bubbletea-backed handler
	// now so the enforcer has a stable callback, and wire it into the
	// CLI adapter further down. The stdin-based CLIApprovalHandler is
	// no longer used for interactive mode: it conflicts with Bubbletea
	// owning the terminal.
	var askFn permissions.AskApprovalFn
	var bubbleApproval *cliAdapter.BubbleteaApprovalHandler
	if headlessPrompt != "" {
		askFn = approval.NewHeadlessApprovalHandler(approval.HeadlessPolicy{}).AskApproval
	} else {
		bubbleApproval = cliAdapter.NewBubbleteaApprovalHandler()
		bubbleApproval.SetYOLO(unrestricted)
		askFn = bubbleApproval.AskApproval
	}
	enforcer := permissions.NewEnforcer(permissions.EnforcerConfig{
		Logger:     logger,
		Manager:    permMgr,
		Modes:      modeState,
		ProjectDir: ".",
		AskFn:      askFn,
		YOLO:       unrestricted,
	})
	sys.Orchestrator.SetEnforcer(enforcer)

	debugState := debug.New()

	// Provider registry
	providerReg, providerList := buildProviderRegistry(cfg, multiAuth)

	// PR status (async)
	go func() {
		if pr, _ := prstatus.GetCurrentPR(); pr != nil {
			logger.Info("PR detected", "number", pr.Number, "state", pr.State)
		}
	}()

	// Context window + compact service (compact uses a late-bound ai.Client)
	ctxWinMgr := sys.ContextWindow
	var aiClient *ai.Client
	compactSvc := buildCompactService(&aiClient, logger)
	ctxWinMgr.SetAutoCompactCallback(func() {
		logger.Info("context window auto-compact threshold reached — triggering compaction")
		compactSvc.Compact(ctx, nil, "", true)
	})

	// Wire all extra subsystems
	active := wiring.WireSubsystems(wiring.SubsystemConfig{
		Logger:      logger,
		DataDir:     cfg.Data.Dir,
		WorkingDir:  ".",
		Providers:   providerList,
		ProviderReg: providerReg,
		SkillExecFn: skills.Execute,
		CompactSvc:  compactSvc,
		AutoMem:     autoMem,
	})
	active.Start(ctx)
	defer active.Shutdown()

	// Slash commands (project-level, from .polypod/commands/*.md)
	cmdReg := sys.SlashCommands
	cmdReg.LoadFromDir(".polypod")
	if cmds := cmdReg.ListCommands(); len(cmds) > 0 {
		logger.Info("project commands loaded", "count", len(cmds))
	}

	// Built-in slash command batches — registered into the command.Registry
	// (sys.Commands) that backs the in-process slash command dispatcher.
	commands.RegisterBatch3Core(sys.Commands)
	commands.RegisterBatch4Security(sys.Commands)
	commands.RegisterBatch5Observability(sys.Commands)
	commands.SetObservabilityTracker(sys.BudgetTracker)
	commands.RegisterBatch6Context(sys.Commands)
	commands.RegisterBatch7Debug(sys.Commands)
	commands.RegisterBatch8Git(sys.Commands)
	commands.RegisterBatch9Setup(sys.Commands)
	commands.RegisterBatch10Env(sys.Commands)
	commands.RegisterBatch11Theme(sys.Commands)
	commands.RegisterBatch12Export(sys.Commands)
	commands.RegisterBatch13Plugins(sys.Commands)
	commands.RegisterBatch14Issues(sys.Commands)
	commands.RegisterBatch15Auth(sys.Commands)
	commands.RegisterBatch16Plan(sys.Commands)
	commands.RegisterBatch17Trace(sys.Commands)
	commands.RegisterBatch18Sandbox(sys.Commands)
	commands.RegisterBatch19Misc(sys.Commands)
	commands.RegisterBatch20Status(sys.Commands)
	commands.RegisterBatch21Tasks(sys.Commands)
	commands.RegisterBatch22MCPCmds(sys.Commands)
	commands.RegisterBatch23Security(sys.Commands)
	commands.RegisterBatch24Testing(sys.Commands)
	commands.RegisterBatch25AI(sys.Commands)
	commands.RegisterBatch26Workspace(sys.Commands)
	commands.RegisterBatch27Session(sys.Commands)
	commands.RegisterBatch28Skills(sys.Commands)
	commands.RegisterBatch29Voice(sys.Commands)
	commands.RegisterAutonomousCommands(sys.Commands)
	commands.RegisterPromptReview(sys.PromptCommands)
	commands.RegisterPromptCommit(sys.PromptCommands)

	// SessionStart hook
	hookMgr.Fire(ctx, hooks.HookPayload{
		Event:     hooks.EventSessionStart,
		Timestamp: time.Now().Format(time.RFC3339),
	})

	logger.Info("polypod ready",
		"skills", len(skills.List()),
		"tools", sys.ToolRegistry.Count(),
		"providers", len(providerReg.List()),
		"mcp_tools", mcpToolCount,
		"mode", string(modeState.Current),
		"debug", debugState.Enabled,
		"hooks", len(hookMgr.List()),
		"orchestrator", sys.Orchestrator != nil,
	)
	activeAgent := agents.Get("default")
	logger.Info("agent active", "name", activeAgent.Name, "skills", len(activeAgent.Skills))

	// AI client (no knowledge service in CLI-only build)
	aiClient = ai.NewClient(cfg.AI, sys.ToolAdapter)
	aiSvc := ai.NewService(aiClient, nil, agents, "default", memStore, logger)

	// Cost engine
	costEngine := tracking.NewCostEngine(logger)
	aiClient.SetCostEngine(costEngine)

	// Wire the real sub-agent executor now that ai.Client is available
	wiring.WireAIClient(sys, aiClient, logger)

	// Log auto-detected provider credentials
	if detected := multiAuth.AutoDetect(); len(detected) > 0 {
		names := make([]string, 0, len(detected))
		for n := range detected {
			names = append(names, n)
		}
		logger.Info("auth: provider credentials available", "providers", names)
	}

	// Inline handlers (replace router)
	handler := makeHandler(convMgr, aiSvc)
	streamHandler := makeStreamHandler(convMgr, aiSvc)

	// Headless mode
	if headlessPrompt != "" {
		return headless.Run(ctx, headless.Config{
			Prompt:       headlessPrompt,
			OutputFormat: headless.OutputFormat(headlessFormat),
		}, handler, streamHandler, convMgr, aiSvc)
	}

	// CLI channel
	cliCh := cliAdapter.New(
		streamHandler, aiSvc, memStore, convMgr, agents, skills, logger, cfg.Data.Dir, sessStorage,
	)
	cliCh.SetCostEngine(costEngine)
	cliCh.SetUserPromptHandler(sys.UserPromptHandler)
	cliCh.SetMCPManager(mcpMgr)
	cliCh.SetHookManager(hookMgr)
	cliCh.SetPermissionsManager(permMgr)
	cliCh.SetModeState(modeState)
	cliCh.SetPlanSimulator(sys.Orchestrator.PlanSimulator())
	cliCh.SetConfig(cfg, configPath)
	cliCh.SetCommandRegistry(sys.Commands)
	if bubbleApproval != nil {
		cliCh.SetApprovalHandler(bubbleApproval)
	}
	if active != nil && active.FileIndex != nil {
		cliCh.SetFileIndex(active.FileIndex)
	}

	if restoredSession != nil && len(restoredSession.Messages) > 0 {
		restored := make([]cliAdapter.RestoredMessage, len(restoredSession.Messages))
		for i, m := range restoredSession.Messages {
			restored[i] = cliAdapter.RestoredMessage{Role: m.Role, Content: m.Content}
		}
		cliCh.SetRestoredMessages(restored)
	}

	// Start
	cliErr := cliCh.Start(ctx, handler)
	if cliErr != nil {
		logger.Error("cli error", "error", cliErr)
	}

	// Shutdown
	autoSaveSession(convMgr, sessStorage, sessMgr, currentSessionID, logger)

	hookMgr.Fire(context.Background(), hooks.HookPayload{
		Event:     hooks.EventSessionEnd,
		Timestamp: time.Now().Format(time.RFC3339),
	})

	active.Shutdown()
	tracker.Save()
	logger.Info("polypod shutdown",
		"context_usage_level", ctxWinMgr.UsageLevel(),
		"context_usage_percent", ctxWinMgr.UsagePercent(),
		"subsystem_stats", active.Stats(),
	)

	return cliErr
}
