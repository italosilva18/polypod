package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/costa/polypod/internal/autoupdate"
	"github.com/costa/polypod/internal/config"
	"github.com/costa/polypod/internal/configmerge"
	"github.com/costa/polypod/internal/configval"
	"github.com/costa/polypod/internal/database"
	"github.com/costa/polypod/internal/dotenv"
	"github.com/costa/polypod/internal/observability"
	"github.com/costa/polypod/internal/setup"
)

const defaultConfigPath = "config.yaml"

func main() {
	// Handle subcommands before loading config
	if handleSubcommand() {
		return
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
