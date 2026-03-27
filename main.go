package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
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
	"github.com/costa/polypod/internal/codemap"
	"github.com/costa/polypod/internal/commands"
	"github.com/costa/polypod/internal/config"
	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/database"
	"github.com/costa/polypod/internal/dbquery"
	"github.com/costa/polypod/internal/git"
	"github.com/costa/polypod/internal/hooks"
	"github.com/costa/polypod/internal/iot"
	"github.com/costa/polypod/internal/knowledge"
	"github.com/costa/polypod/internal/mcp"
	"github.com/costa/polypod/internal/memory"
	"github.com/costa/polypod/internal/multiread"
	"github.com/costa/polypod/internal/notify"
	"github.com/costa/polypod/internal/observability"
	"github.com/costa/polypod/internal/plugin"
	"github.com/costa/polypod/internal/project"
	"github.com/costa/polypod/internal/ratelimit"
	"github.com/costa/polypod/internal/review"
	"github.com/costa/polypod/internal/router"
	"github.com/costa/polypod/internal/sandbox"
	"github.com/costa/polypod/internal/scheduler"
	"github.com/costa/polypod/internal/security"
	"github.com/costa/polypod/internal/selfmod"
	"github.com/costa/polypod/internal/session"
	"github.com/costa/polypod/internal/setup"
	"github.com/costa/polypod/internal/skill"
	"github.com/costa/polypod/internal/template"
	"github.com/costa/polypod/internal/tracking"
	"github.com/costa/polypod/internal/vision"
	"github.com/costa/polypod/internal/voice"
	"github.com/costa/polypod/internal/web"
	"github.com/costa/polypod/internal/webui"
)

const defaultConfigPath = "config.yaml"

func main() {
	configPath, runSetup := parseArgs()

	if runSetup {
		if err := setup.Run(configPath); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	if err := setup.CheckAPIKey(cfg, configPath); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	var logger *slog.Logger
	if cfg.CLI.Enabled {
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
	logger.Info("polypod starting", "version", "0.3.0")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Database is optional — if disabled, runs fully in-memory or JSON files
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
		default: // "postgres"
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
	} else {
		logger.Info("database disabled, using JSON file persistence", "data_dir", cfg.Data.Dir)
	}

	if err := run(ctx, cfg, pgDB, sqliteDB, logger); err != nil {
		logger.Error("fatal error", "error", err)
		os.Exit(1)
	}

	logger.Info("polypod stopped")
}

// parseArgs decides the config path and whether to run the setup wizard.
func parseArgs() (configPath string, runSetup bool) {
	configPath = defaultConfigPath

	if len(os.Args) < 2 {
		if _, err := os.Stat(defaultConfigPath); os.IsNotExist(err) {
			return configPath, true
		}
		return configPath, false
	}

	arg := os.Args[1]
	if arg == "--setup" {
		return configPath, true
	}

	return arg, false
}

func run(ctx context.Context, cfg *config.Config, pgDB *database.DB, sqliteDB *database.SQLiteDB, logger *slog.Logger) error {
	var pool *pgxpool.Pool
	var sqlDB *sql.DB
	if pgDB != nil {
		pool = pgDB.Pool
	}
	if sqliteDB != nil {
		sqlDB = sqliteDB.DB
	}

	// Conversation manager with JSON persistence when no DB
	dataDir := ""
	if pool == nil && sqlDB == nil {
		dataDir = cfg.Data.Dir
	}
	store := conversation.NewStore(pool, sqlDB, dataDir)
	convMgr := conversation.NewManager(store, logger)

	if sqlDB != nil {
		sessions := store.ListSessions()
		if len(sessions) > 0 {
			logger.Info("loaded conversations from sqlite", "count", len(sessions))
		}
	} else if dataDir != "" {
		sessions := store.ListSessions()
		if len(sessions) > 0 {
			logger.Info("loaded conversations from disk", "count", len(sessions))
		}
	}

	// Skill registry
	skills := skill.NewRegistry()

	// Memory skills
	memStore := memory.NewStoreFromDB(sqlDB, cfg.Data.Dir)
	memory.RegisterSkills(skills, memStore)

	// Web skills (internet access)
	web.RegisterSkills(skills)

	// IoT/hardware skills
	iot.RegisterSkills(skills)

	// Agent registry
	agents := agent.NewRegistry()
	if err := agents.LoadDir(cfg.Data.AgentsDir); err != nil {
		logger.Warn("failed to load agents dir", "error", err, "dir", cfg.Data.AgentsDir)
	}

	// Self-modification skills
	selfmod.RegisterSkills(skills, agents, cfg.Data.AgentsDir)

	// Custom skills (script-based, loaded from data/skills/)
	customSkillsDir := "data/skills"
	os.MkdirAll(customSkillsDir, 0755)
	skill.LoadAndRegisterCustomSkills(skills, customSkillsDir)
	skill.RegisterDynamicManagement(skills, customSkillsDir)

	// === NEW MODULES ===

	// Git integration skills
	git.RegisterSkills(skills)
	git.RegisterFileSkills(skills)

	// Code review & testing skills
	review.RegisterSkills(skills)

	// Vision/image skills
	vision.RegisterSkills(skills)

	// Voice I/O skills
	voice.RegisterSkills(skills)

	// Security scanning skills
	security.RegisterSkills(skills)

	// Database query skills
	dbquery.RegisterSkills(skills)

	// Codebase mapping skills
	codemap.RegisterSkills(skills, ".", logger)

	// Project instruction files (.polypod.md)
	if projCtx := project.InjectProjectContext(); projCtx != "" {
		logger.Info("project instructions loaded")
	}

	// Session manager
	sessMgr := session.NewManager(cfg.Data.Dir, logger)
	session.RegisterSkills(skills, sessMgr)

	// Cost/token tracker
	tracker := tracking.NewTracker(filepath.Join(cfg.Data.Dir, "usage.json"), logger)
	tracking.RegisterSkills(skills, tracker)

	// Notification dispatcher
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

	// Docker sandbox
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

	// Plugin system
	pluginMgr := plugin.NewManager(cfg.Plugins.Dir, logger)
	pluginMgr.LoadAll()
	pluginCount := pluginMgr.RegisterAllSkills(skills)
	plugin.RegisterManagementSkills(skills, pluginMgr)
	if pluginCount > 0 {
		logger.Info("plugin skills loaded", "count", pluginCount)
	}

	// Prompt templates
	tmplReg := template.NewRegistry(cfg.Templates.Dir)
	tmplReg.LoadAll()
	tmplReg.RegisterSkills(skills)

	// MCP servers
	mcpMgr := mcp.NewManager(logger)
	for _, mcpCfg := range cfg.MCP {
		if mcpCfg.AutoStart {
			if err := mcpMgr.Connect(ctx, mcp.ServerConfig{
				Name:      mcpCfg.Name,
				Transport: mcpCfg.Transport,
				Command:   mcpCfg.Command,
				Args:      mcpCfg.Args,
				URL:       mcpCfg.URL,
				Env:       mcpCfg.Env,
			}); err != nil {
				logger.Warn("mcp server failed to connect", "name", mcpCfg.Name, "error", err)
			}
		}
	}
	mcpToolCount := mcpMgr.RegisterTools(skills)
	mcp.RegisterSkills(skills, mcpMgr)
	if mcpToolCount > 0 {
		logger.Info("mcp tools loaded", "count", mcpToolCount)
	}
	defer mcpMgr.Close()

	// Multi-file read skills
	multiread.RegisterSkills(skills)

	// Hooks system
	hookMgr := hooks.NewManager(logger)
	for _, h := range cfg.Hooks {
		hookMgr.Register(hooks.Hook{
			Name:    h.Name,
			Event:   hooks.Event(h.Event),
			Type:    hooks.HandlerType(h.Type),
			Command: h.Command,
			URL:     h.URL,
			Matcher: h.Matcher,
			Timeout: h.Timeout,
			Enabled: h.Enabled,
		})
	}

	// Project commands (.polypod/commands/*.md)
	cmdReg := commands.NewRegistry()
	cmdReg.LoadFromDir(".polypod")
	if cmds := cmdReg.ListCommands(); len(cmds) > 0 {
		logger.Info("project commands loaded", "count", len(cmds))
	}

	// Fire SessionStart hook
	hookMgr.Fire(ctx, hooks.HookPayload{
		Event:     hooks.EventSessionStart,
		Timestamp: time.Now().Format(time.RFC3339),
	})

	// === END NEW MODULES ===

	logger.Info("skills loaded", "count", len(skills.List()))

	activeAgent := agents.Get("default")
	logger.Info("agent active", "name", activeAgent.Name, "skills", activeAgent.Skills)

	// Knowledge service (optional — needs database + embedding)
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
			logger.Info("knowledge service enabled", "backend", "sqlite")
		} else if pool != nil {
			vs := knowledge.NewVectorSearch(pool, embedder, logger)
			dbQuery := knowledge.NewDBQueryService(pool, logger)
			knowledgeSvc = knowledge.NewService(vs, dbQuery, nil, logger)
			logger.Info("knowledge service enabled", "backend", "postgres")
		}
	}

	// AI client + service (with skill registry, agent registry, and memory store)
	aiClient := ai.NewClient(cfg.AI, skills)
	aiSvc := ai.NewService(aiClient, knowledgeSvc, agents, "default", memStore, logger)

	// Auth + rate limiter
	authz := auth.New(cfg)
	limiter := ratelimit.New(cfg.Rate.RequestsPerMinute, cfg.Rate.BurstSize)

	// Central router
	rtr := router.New(convMgr, aiSvc, authz, limiter, pool, sqlDB, logger)
	handler := rtr.Handler()
	streamHandler := rtr.StreamHandler()

	// Collect channels to start
	var channels []adapter.Channel

	if cfg.CLI.Enabled {
		channels = append(channels, cliAdapter.New(
			streamHandler,
			aiSvc,
			memStore,
			convMgr,
			agents,
			skills,
			logger,
			cfg.Data.Dir,
		))
	}

	if cfg.REST.Enabled {
		restAdapter := rest.New(
			cfg.Server.Host,
			cfg.Server.Port,
			authz,
			logger,
			memStore,
			agents,
			skills,
			convMgr,
			aiSvc,
			streamHandler,
		)
		channels = append(channels, restAdapter)
	}

	if cfg.Telegram.Enabled {
		tgAdapter := telegram.New(cfg.Telegram.Token, logger)
		channels = append(channels, tgAdapter)
	}

	if cfg.WhatsApp.Enabled {
		waAdapter := whatsapp.New(cfg.WhatsApp.IDInstance, cfg.WhatsApp.APIToken, logger)
		channels = append(channels, waAdapter)
	}

	// Web UI (browser interface)
	if cfg.WebUI.Enabled {
		go func() {
			wui := webui.New(cfg.WebUI.Host, cfg.WebUI.Port, streamHandler, logger)
			if err := wui.Start(ctx); err != nil {
				logger.Warn("webui failed", "error", err)
			}
		}()
	}

	if len(channels) == 0 {
		logger.Warn("no channels enabled, nothing to do")
		<-ctx.Done()
		return nil
	}

	// Start all channels concurrently.
	// Non-CLI channels log errors but don't bring down the process.
	g, gCtx := errgroup.WithContext(ctx)
	for _, ch := range channels {
		ch := ch
		if ch.Name() == "cli" {
			g.Go(func() error {
				logger.Info("starting channel", "name", ch.Name())
				return ch.Start(gCtx, handler)
			})
		} else {
			g.Go(func() error {
				logger.Info("starting channel", "name", ch.Name())
				if err := ch.Start(gCtx, handler); err != nil {
					logger.Warn("channel failed (non-fatal)", "name", ch.Name(), "error", err)
				}
				return nil
			})
		}
	}

	return g.Wait()
}
