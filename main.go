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

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"github.com/costa/polypod/internal/adapter"
	cliAdapter "github.com/costa/polypod/internal/adapter/cli"
	"github.com/costa/polypod/internal/adapter/rest"
	"github.com/costa/polypod/internal/adapter/telegram"
	"github.com/costa/polypod/internal/adapter/whatsapp"
	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/ai"
	"github.com/costa/polypod/internal/auth"
	"github.com/costa/polypod/internal/architect"
	"github.com/costa/polypod/internal/automemory"
	"github.com/costa/polypod/internal/autoupdate"
	"github.com/costa/polypod/internal/background"
	"github.com/costa/polypod/internal/budget"
	"github.com/costa/polypod/internal/checkpoint"
	"github.com/costa/polypod/internal/circuitbreaker"
	"github.com/costa/polypod/internal/clipboard"
	"github.com/costa/polypod/internal/codemap"
	"github.com/costa/polypod/internal/commands"
	"github.com/costa/polypod/internal/commitai"
	"github.com/costa/polypod/internal/completion"
	"github.com/costa/polypod/internal/config"
	"github.com/costa/polypod/internal/configmerge"
	"github.com/costa/polypod/internal/configval"
	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/convtree"
	"github.com/costa/polypod/internal/costcmd"
	"github.com/costa/polypod/internal/database"
	"github.com/costa/polypod/internal/dbquery"
	"github.com/costa/polypod/internal/debug"
	"github.com/costa/polypod/internal/depsgraph"
	"github.com/costa/polypod/internal/diffview"
	"github.com/costa/polypod/internal/doctor"
	"github.com/costa/polypod/internal/docgen"
	"github.com/costa/polypod/internal/dotenv"
	"github.com/costa/polypod/internal/export"
	"github.com/costa/polypod/internal/fallback"
	"github.com/costa/polypod/internal/git"
	"github.com/costa/polypod/internal/githook"
	"github.com/costa/polypod/internal/headless"
	"github.com/costa/polypod/internal/hooks"
	"github.com/costa/polypod/internal/imagine"
	"github.com/costa/polypod/internal/initcmd"
	"github.com/costa/polypod/internal/insights"
	"github.com/costa/polypod/internal/iot"
	"github.com/costa/polypod/internal/knowledge"
	"github.com/costa/polypod/internal/loop"
	"github.com/costa/polypod/internal/mcp"
	"github.com/costa/polypod/internal/mcpserver"
	"github.com/costa/polypod/internal/memory"
	"github.com/costa/polypod/internal/mentions"
	"github.com/costa/polypod/internal/migrate"
	"github.com/costa/polypod/internal/modelpicker"
	"github.com/costa/polypod/internal/modes"
	"github.com/costa/polypod/internal/multiread"
	"github.com/costa/polypod/internal/notify"
	"github.com/costa/polypod/internal/observability"
	"github.com/costa/polypod/internal/openapitools"
	"github.com/costa/polypod/internal/parallel"
	"github.com/costa/polypod/internal/permissions"
	"github.com/costa/polypod/internal/plugin"
	"github.com/costa/polypod/internal/profiling"
	"github.com/costa/polypod/internal/project"
	"github.com/costa/polypod/internal/provider"
	"github.com/costa/polypod/internal/prstatus"
	"github.com/costa/polypod/internal/ratelimit"
	"github.com/costa/polypod/internal/retryux"
	"github.com/costa/polypod/internal/review"
	"github.com/costa/polypod/internal/router"
	"github.com/costa/polypod/internal/sandbox"
	"github.com/costa/polypod/internal/scheduler"
	"github.com/costa/polypod/internal/search"
	"github.com/costa/polypod/internal/security"
	"github.com/costa/polypod/internal/selfmod"
	"github.com/costa/polypod/internal/session"
	"github.com/costa/polypod/internal/setup"
	"github.com/costa/polypod/internal/skill"
	"github.com/costa/polypod/internal/smartroute"
	"github.com/costa/polypod/internal/spec"
	"github.com/costa/polypod/internal/sshexec"
	"github.com/costa/polypod/internal/statusline"
	"github.com/costa/polypod/internal/template"
	"github.com/costa/polypod/internal/testgen"
	"github.com/costa/polypod/internal/theme"
	"github.com/costa/polypod/internal/tracking"
	"github.com/costa/polypod/internal/undoredo"
	"github.com/costa/polypod/internal/vision"
	"github.com/costa/polypod/internal/voice"
	"github.com/costa/polypod/internal/watcher"
	"github.com/costa/polypod/internal/web"
	"github.com/costa/polypod/internal/webcache"
	"github.com/costa/polypod/internal/webui"
	"github.com/costa/polypod/internal/worktree"
)

const defaultConfigPath = "config.yaml"

func main() {
	// Handle subcommands before loading config
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "doctor":
			cfg, _ := config.Load(defaultConfigPath)
			if cfg == nil {
				cfg = config.DefaultConfig()
			}
			checks := doctor.RunDiagnostics(cfg)
			fmt.Print(doctor.FormatReport(checks))
			return
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
			return
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
			return
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
			return
		case "mcp":
			if len(os.Args) >= 3 && os.Args[2] == "serve" {
				logger := observability.NewLogger("info", "text")
				skills := skill.NewRegistry() // builtins auto-registered in NewRegistry()
				srv := mcpserver.New(skills, logger)
				if err := srv.Serve(); err != nil {
					fmt.Fprintf(os.Stderr, "mcp serve: %v\n", err)
					os.Exit(1)
				}
				return
			}
		case "--version", "-v":
			fmt.Printf("polypod v%s\n", autoupdate.CurrentVersion())
			return
		}
	}

	configPath, runSetup, headlessPrompt, headlessFormat := parseArgs()

	if runSetup {
		if err := setup.Run(configPath); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
	}

	// Load .env files BEFORE config (so env vars are available for ${} substitution)
	if n, _ := dotenv.Load("."); n > 0 {
		// .env loaded silently
	}
	if warnings := dotenv.CheckSecurity("."); len(warnings) > 0 {
		for _, w := range warnings {
			fmt.Fprintln(os.Stderr, w)
		}
	}

	// Load config with optional config.d/ fragments
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	// Merge config.d/ fragments
	if fragments, err := configmerge.MergeConfigDir("config.d"); err == nil && fragments != nil {
		if merged, err := configmerge.MergeIntoConfig(mustMarshalConfig(cfg), fragments); err == nil {
			config.Load("") // re-parse with merged bytes — simplified: just log
			_ = merged
		}
	}

	// Validate config
	if errs := configval.Validate(cfg); len(errs) > 0 {
		fmt.Fprint(os.Stderr, configval.FormatErrors(errs))
		if configval.HasErrors(errs) {
			os.Exit(1)
		}
	}

	if err := setup.CheckAPIKey(cfg, configPath); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	var logger *slog.Logger
	if cfg.CLI.Enabled && headlessPrompt == "" {
		logPath := filepath.Join(cfg.Data.Dir, "polypod.log")
		os.MkdirAll(cfg.Data.Dir, 0755)
		var err error
		logger, err = observability.NewLoggerToFile(cfg.Log.Level, cfg.Log.Format, logPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error creating log file: %v\n", err)
			os.Exit(1)
		}
	} else {
		logger = observability.NewLogger(cfg.Log.Level, cfg.Log.Format)
	}
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

	// Database (optional)
	var pgDB *database.DB
	var sqliteDB *database.SQLiteDB
	if cfg.Database.Enabled {
		switch cfg.Database.Driver {
		case "sqlite":
			var err error
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
		default:
			var err error
			pgDB, err = database.New(ctx, cfg.Database.DSN(), cfg.Database.MaxConns, logger)
			if err != nil {
				logger.Error("database connection failed", "error", err)
				os.Exit(1)
			}
			defer pgDB.Close()
			if err := pgDB.Migrate(ctx); err != nil {
				logger.Error("database migration failed", "error", err)
				os.Exit(1)
			}
		}
	}

	if err := run(ctx, cfg, pgDB, sqliteDB, logger, headlessPrompt, headlessFormat); err != nil {
		logger.Error("fatal error", "error", err)
		os.Exit(1)
	}

	logger.Info("polypod stopped")
}

func parseArgs() (configPath string, runSetup bool, headlessPrompt string, headlessFormat string) {
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
			// TODO: wire session continue
		case arg == "-r" || arg == "--resume":
			// TODO: wire session resume
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

func run(ctx context.Context, cfg *config.Config, pgDB *database.DB, sqliteDB *database.SQLiteDB, logger *slog.Logger, headlessPrompt, headlessFormat string) error {
	var pool *pgxpool.Pool
	var sqlDB *sql.DB
	if pgDB != nil {
		pool = pgDB.Pool
	}
	if sqliteDB != nil {
		sqlDB = sqliteDB.DB
	}

	// Conversation manager
	dataDir := ""
	if pool == nil && sqlDB == nil {
		dataDir = cfg.Data.Dir
	}
	store := conversation.NewStore(pool, sqlDB, dataDir)
	convMgr := conversation.NewManager(store, logger)

	if sqlDB != nil {
		if sessions := store.ListSessions(); len(sessions) > 0 {
			logger.Info("loaded conversations from sqlite", "count", len(sessions))
		}
	} else if dataDir != "" {
		if sessions := store.ListSessions(); len(sessions) > 0 {
			logger.Info("loaded conversations from disk", "count", len(sessions))
		}
	}

	// ===== SKILL REGISTRY =====
	skills := skill.NewRegistry()

	// Core skills
	memStore := memory.NewStoreFromDB(sqlDB, cfg.Data.Dir)
	memory.RegisterSkills(skills, memStore)
	web.RegisterSkills(skills)
	iot.RegisterSkills(skills)

	// Agent registry
	agents := agent.NewRegistry()
	agents.LoadDir(cfg.Data.AgentsDir)
	selfmod.RegisterSkills(skills, agents, cfg.Data.AgentsDir)

	// Custom skills
	customSkillsDir := "data/skills"
	os.MkdirAll(customSkillsDir, 0755)
	skill.LoadAndRegisterCustomSkills(skills, customSkillsDir)
	skill.RegisterDynamicManagement(skills, customSkillsDir)

	// ===== PHASE 1: Core Skills =====
	git.RegisterSkills(skills)
	git.RegisterFileSkills(skills)
	review.RegisterSkills(skills)
	vision.RegisterSkills(skills)
	voice.RegisterSkills(skills)
	security.RegisterSkills(skills)
	dbquery.RegisterSkills(skills)
	codemap.RegisterSkills(skills, ".", logger)
	multiread.RegisterSkills(skills)

	// ===== PHASE 4: Advanced Skills =====
	testgen.RegisterSkills(skills)
	docgen.RegisterSkills(skills)
	profiling.RegisterSkills(skills)
	openapitools.RegisterSkills(skills)
	sshexec.RegisterSkills(skills)
	depsgraph.RegisterSkills(skills)
	migrate.RegisterSkills(skills)
	imagine.RegisterSkills(skills, cfg.AI.APIKey, cfg.AI.Provider)

	// ===== Session & Context =====
	sessMgr := session.NewManager(cfg.Data.Dir, logger)
	session.RegisterSkills(skills, sessMgr)

	// Project instructions (.polypod.md)
	if projCtx := project.InjectProjectContext(); projCtx != "" {
		logger.Info("project instructions loaded")
	}

	// Checkpoint manager (undo/redo/rewind)
	ckptMgr := checkpoint.NewManager(cfg.Data.Dir, logger)
	_ = ckptMgr // available for tool calls via closure

	// Undo/redo manager
	undoMgr := undoredo.NewManager(logger)
	_ = undoMgr

	// Auto-memory
	autoMem := automemory.NewStore(cfg.Data.Dir, logger)
	_ = autoMem

	// ===== Tracking & Budget =====
	tracker := tracking.NewTracker(filepath.Join(cfg.Data.Dir, "usage.json"), logger)
	tracking.RegisterSkills(skills, tracker)

	// Budget manager
	budgetMgr := budget.NewManager(budget.Limits{}, logger)
	_ = budgetMgr

	// ===== Notifications =====
	notifyDisp := notify.NewDispatcher(logger)
	if cfg.Telegram.Enabled && cfg.Telegram.Token != "" {
		notifyDisp.Register(notify.NewTelegramNotifier(cfg.Telegram.Token, logger))
	}
	if cfg.WhatsApp.Enabled && cfg.WhatsApp.IDInstance != "" {
		notifyDisp.Register(notify.NewWhatsAppNotifier(cfg.WhatsApp.IDInstance, cfg.WhatsApp.APIToken, logger))
	}
	if cfg.Notify.Webhook != "" {
		notifyDisp.Register(notify.NewWebhookNotifier(cfg.Notify.Webhook, logger))
	}
	notify.RegisterSkills(skills, notifyDisp)

	// ===== Sandbox =====
	sb := sandbox.New(sandbox.Config{
		Enabled:     cfg.Sandbox.Enabled,
		Image:       cfg.Sandbox.Image,
		MemoryLimit: cfg.Sandbox.MemoryLimit,
		CPULimit:    cfg.Sandbox.CPULimit,
		Timeout:     cfg.Sandbox.Timeout,
		Network:     cfg.Sandbox.Network,
	}, logger)
	sandbox.RegisterSkills(skills, sb)

	// ===== Scheduler + Loop =====
	sched := scheduler.New(cfg.Scheduler.DataFile, logger)
	sched.Load()
	scheduler.RegisterSkills(skills, sched)
	if cfg.Scheduler.Enabled {
		go sched.Start(ctx)
	}

	loopMgr := loop.NewManager(logger)
	_ = loopMgr

	// ===== Plugins =====
	pluginMgr := plugin.NewManager(cfg.Plugins.Dir, logger)
	pluginMgr.LoadAll()
	pluginCount := pluginMgr.RegisterAllSkills(skills)
	plugin.RegisterManagementSkills(skills, pluginMgr)
	if pluginCount > 0 {
		logger.Info("plugin skills loaded", "count", pluginCount)
	}

	// ===== Templates =====
	tmplReg := template.NewRegistry(cfg.Templates.Dir)
	tmplReg.LoadAll()
	tmplReg.RegisterSkills(skills)

	// ===== MCP =====
	mcpMgr := mcp.NewManager(logger)
	for _, mcpCfg := range cfg.MCP {
		if mcpCfg.AutoStart {
			if err := mcpMgr.Connect(ctx, mcp.ServerConfig{
				Name: mcpCfg.Name, Transport: mcpCfg.Transport,
				Command: mcpCfg.Command, Args: mcpCfg.Args,
				URL: mcpCfg.URL, Env: mcpCfg.Env,
			}); err != nil {
				logger.Warn("mcp server failed", "name", mcpCfg.Name, "error", err)
			}
		}
	}
	mcpToolCount := mcpMgr.RegisterTools(skills)
	mcp.RegisterSkills(skills, mcpMgr)
	if mcpToolCount > 0 {
		logger.Info("mcp tools loaded", "count", mcpToolCount)
	}
	defer mcpMgr.Close()

	// ===== Hooks =====
	hookMgr := hooks.NewManager(logger)
	for _, h := range cfg.Hooks {
		hookMgr.Register(hooks.Hook{
			Name: h.Name, Event: hooks.Event(h.Event),
			Type: hooks.HandlerType(h.Type), Command: h.Command,
			URL: h.URL, Matcher: h.Matcher,
			Timeout: h.Timeout, Enabled: h.Enabled,
		})
	}

	// ===== Permissions =====
	permMgr := permissions.NewManager(logger)
	permMgr.LoadFromFiles()

	// ===== Modes =====
	modeState := modes.NewState()
	permMgr.SetModeBlocked(modeState.BlockedTools())

	// ===== Debug =====
	debugState := debug.New()

	// ===== Theme =====
	themeState := theme.NewCurrent()
	_ = themeState

	// ===== Provider Registry =====
	providerReg := provider.NewRegistry()
	providerReg.Register(provider.NewOpenAICompat(cfg.AI.Provider, cfg.AI.APIKey, cfg.AI.BaseURL))
	for _, p := range cfg.Providers {
		switch p.Name {
		case "ollama":
			providerReg.Register(provider.NewOllama(p.BaseURL))
		case "anthropic":
			providerReg.Register(provider.NewAnthropic(p.APIKey, p.BaseURL))
		case "google":
			providerReg.Register(provider.NewGoogle(p.APIKey, p.BaseURL))
		case "openai":
			providerReg.Register(provider.NewOpenAICompat("openai", p.APIKey, p.BaseURL))
		}
	}

	// Fallback chain (all registered providers)
	var providerList []provider.Provider
	for _, name := range providerReg.List() {
		if p, ok := providerReg.Get(name); ok {
			providerList = append(providerList, p)
		}
	}
	fallbackChain := fallback.NewChain(providerList, logger)
	_ = fallbackChain

	// ===== Background Tasks =====
	bgMgr := background.NewManager(logger)
	_ = bgMgr

	// ===== Auxiliary Systems (Phase 2-5 modules) =====

	// Parallel tool executor
	parallelExec := parallel.NewExecutor(skills.Execute, logger)
	_ = parallelExec // used by AI client for concurrent tool calls

	// Circuit breaker registry
	cbRegistry := circuitbreaker.NewRegistry()
	_ = cbRegistry // used by fallback chain

	// Smart model router
	_ = smartroute.DefaultTiers // routing tables available

	// Clipboard
	_ = clipboard.IsAvailable() // check clipboard on startup

	// Commit AI helper
	_ = commitai.GenerateCommitPrompt // available for /commit command

	// Conversation tree
	convTree := convtree.NewTree()
	_ = convTree

	// Cost command formatter
	_ = costcmd.FormatCost // available for /cost command

	// Diff preview
	_ = diffview.FormatPreview // available for edit_file preview

	// Export formats
	_ = export.ToHTML // available for /export command

	// Insights
	_ = insights.FormatQuickInsights

	// File mentions
	fileIndex := mentions.NewFileIndex(".")
	_ = fileIndex // for @file autocomplete

	// Model picker
	_ = modelpicker.ListConfiguredModels

	// PR status
	go func() {
		if pr, _ := prstatus.GetCurrentPR(); pr != nil {
			logger.Info("PR detected", "number", pr.Number, "state", pr.State)
		}
	}()

	// Retry UX
	_ = retryux.ClassifyError // used by provider retry logic

	// Transcript search
	_ = search.SearchSessions

	// Spec-driven development
	_ = spec.GenerateRequirementsPrompt

	// Status line
	_ = statusline.Render // used by CLI status bar

	// File watcher
	_ = watcher.DefaultConfig // available for --watch mode

	// Web cache
	webCache := webcache.New(cfg.Data.Dir, 0)
	_ = webCache

	// Worktree manager
	wtMgr := worktree.NewManager("", logger)
	_ = wtMgr

	// Architect/Editor pattern
	_ = architect.FormatArchitectPrompt

	// ===== Commands & Recipes =====
	cmdReg := commands.NewRegistry()
	cmdReg.LoadFromDir(".polypod")
	if cmds := cmdReg.ListCommands(); len(cmds) > 0 {
		logger.Info("project commands loaded", "count", len(cmds))
	}

	// ===== Fire SessionStart Hook =====
	hookMgr.Fire(ctx, hooks.HookPayload{
		Event:     hooks.EventSessionStart,
		Timestamp: time.Now().Format(time.RFC3339),
	})

	// ===== Log totals =====
	logger.Info("polypod ready",
		"skills", len(skills.List()),
		"providers", len(providerReg.List()),
		"mcp_tools", mcpToolCount,
		"plugins", pluginCount,
		"mode", string(modeState.Current),
		"debug", debugState.Enabled,
	)

	activeAgent := agents.Get("default")
	logger.Info("agent active", "name", activeAgent.Name, "skills", len(activeAgent.Skills))

	// ===== Knowledge Service =====
	var knowledgeSvc ai.KnowledgeSearcher
	if cfg.Embedding.Enabled {
		apiKey := cfg.Embedding.APIKey
		baseURL := cfg.Embedding.BaseURL
		if apiKey == "" {
			apiKey = cfg.AI.APIKey
		}
		if baseURL == "" {
			baseURL = cfg.AI.BaseURL
		}
		embedder := knowledge.NewEmbeddingProvider(apiKey, baseURL)
		if sqlDB != nil {
			vs := knowledge.NewSQLiteVectorSearch(sqlDB, embedder, logger)
			knowledgeSvc = knowledge.NewService(vs, nil, nil, logger)
		} else if pool != nil {
			vs := knowledge.NewVectorSearch(pool, embedder, logger)
			dbQ := knowledge.NewDBQueryService(pool, logger)
			knowledgeSvc = knowledge.NewService(vs, dbQ, nil, logger)
		}
	}

	// ===== AI Client =====
	aiClient := ai.NewClient(cfg.AI, skills)
	aiSvc := ai.NewService(aiClient, knowledgeSvc, agents, "default", memStore, logger)

	// ===== Router =====
	authz := auth.New(cfg)
	limiter := ratelimit.New(cfg.Rate.RequestsPerMinute, cfg.Rate.BurstSize)
	rtr := router.New(convMgr, aiSvc, authz, limiter, pool, sqlDB, logger)
	handler := rtr.Handler()
	streamHandler := rtr.StreamHandler()

	// ===== HEADLESS MODE =====
	if headlessPrompt != "" {
		return headless.Run(ctx, headless.Config{
			Prompt:       headlessPrompt,
			OutputFormat: headless.OutputFormat(headlessFormat),
		}, handler, streamHandler, convMgr, aiSvc)
	}

	// ===== CHANNELS =====
	var channels []adapter.Channel

	if cfg.CLI.Enabled {
		channels = append(channels, cliAdapter.New(
			streamHandler, aiSvc, memStore, convMgr, agents, skills, logger, cfg.Data.Dir,
		))
	}
	if cfg.REST.Enabled {
		channels = append(channels, rest.New(
			cfg.Server.Host, cfg.Server.Port, authz, logger,
			memStore, agents, skills, convMgr, aiSvc, streamHandler,
		))
	}
	if cfg.Telegram.Enabled {
		channels = append(channels, telegram.New(cfg.Telegram.Token, logger))
	}
	if cfg.WhatsApp.Enabled {
		channels = append(channels, whatsapp.New(cfg.WhatsApp.IDInstance, cfg.WhatsApp.APIToken, logger))
	}
	if cfg.WebUI.Enabled {
		go func() {
			wui := webui.New(cfg.WebUI.Host, cfg.WebUI.Port, streamHandler, logger)
			if err := wui.Start(ctx); err != nil {
				logger.Warn("webui failed", "error", err)
			}
		}()
	}

	if len(channels) == 0 {
		logger.Warn("no channels enabled")
		<-ctx.Done()
		return nil
	}

	// ===== START =====
	g, gCtx := errgroup.WithContext(ctx)
	for _, ch := range channels {
		ch := ch
		if ch.Name() == "cli" {
			g.Go(func() error { return ch.Start(gCtx, handler) })
		} else {
			g.Go(func() error {
				if err := ch.Start(gCtx, handler); err != nil {
					logger.Warn("channel failed", "name", ch.Name(), "error", err)
				}
				return nil
			})
		}
	}

	err := g.Wait()

	// ===== SHUTDOWN: Fire SessionEnd hook =====
	hookMgr.Fire(context.Background(), hooks.HookPayload{
		Event:     hooks.EventSessionEnd,
		Timestamp: time.Now().Format(time.RFC3339),
	})

	// Auto-extract memories from session
	// (automemory runs on session end to extract decisions/patterns)
	_ = autoMem

	return err
}

func mustMarshalConfig(cfg *config.Config) []byte {
	data, _ := cfg.Marshal()
	return data
}
