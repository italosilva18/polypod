package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

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
	"github.com/costa/polypod/internal/config"
	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/database"
	"github.com/costa/polypod/internal/iot"
	"github.com/costa/polypod/internal/knowledge"
	"github.com/costa/polypod/internal/memory"
	"github.com/costa/polypod/internal/observability"
	"github.com/costa/polypod/internal/ratelimit"
	"github.com/costa/polypod/internal/router"
	"github.com/costa/polypod/internal/selfmod"
	"github.com/costa/polypod/internal/setup"
	"github.com/costa/polypod/internal/skill"
	"github.com/costa/polypod/internal/web"
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

	logger := observability.NewLogger(cfg.Log.Level, cfg.Log.Format)
	slog.SetDefault(logger)
	logger.Info("polypod starting", "version", "0.2.0")

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
//
//	./polypod           → config.yaml exists? yes → use it. no → run wizard
//	./polypod --setup   → force wizard (overwrites if confirmed)
//	./polypod config.yaml → legacy behavior
func parseArgs() (configPath string, runSetup bool) {
	configPath = defaultConfigPath

	if len(os.Args) < 2 {
		// No args: check if default config exists
		if _, err := os.Stat(defaultConfigPath); os.IsNotExist(err) {
			return configPath, true
		}
		return configPath, false
	}

	arg := os.Args[1]
	if arg == "--setup" {
		return configPath, true
	}

	// Explicit config path
	return arg, false
}

func run(ctx context.Context, cfg *config.Config, pgDB *database.DB, sqliteDB *database.SQLiteDB, logger *slog.Logger) error {
	// Get pool and sqlDB (nil if not configured)
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

	logger.Info("skills loaded", "count", len(skills.List()), "skills", skills.List())

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

	// AI client + service (with skill registry and agent registry)
	aiClient := ai.NewClient(cfg.AI, skills)
	aiSvc := ai.NewService(aiClient, knowledgeSvc, agents, "default", logger)

	// Auth + rate limiter
	authz := auth.New(cfg)
	limiter := ratelimit.New(cfg.Rate.RequestsPerMinute, cfg.Rate.BurstSize)

	// Central router
	rtr := router.New(convMgr, aiSvc, authz, limiter, pool, sqlDB, logger)
	handler := rtr.Handler()

	// Collect channels to start
	var channels []adapter.Channel

	if cfg.CLI.Enabled {
		channels = append(channels, cliAdapter.New())
	}

	if cfg.REST.Enabled {
		restAdapter := rest.New(cfg.Server.Host, cfg.Server.Port, authz, logger)
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

	if len(channels) == 0 {
		logger.Warn("no channels enabled, nothing to do")
		<-ctx.Done()
		return nil
	}

	// Start all channels concurrently
	g, gCtx := errgroup.WithContext(ctx)
	for _, ch := range channels {
		ch := ch
		g.Go(func() error {
			logger.Info("starting channel", "name", ch.Name())
			return ch.Start(gCtx, handler)
		})
	}

	return g.Wait()
}
