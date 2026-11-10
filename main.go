package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sashabaranov/go-openai/jsonschema"
	"gopkg.in/yaml.v3"

	"github.com/costa/polypod/internal/adapter"
	cliAdapter "github.com/costa/polypod/internal/adapter/cli"
	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/ai"
	"github.com/costa/polypod/internal/approval"
	"github.com/costa/polypod/internal/audio"
	"github.com/costa/polypod/internal/audit"
	"github.com/costa/polypod/internal/auth"
	"github.com/costa/polypod/internal/automemory"
	"github.com/costa/polypod/internal/autoupdate"
	"github.com/costa/polypod/internal/chatgptauth"
	"github.com/costa/polypod/internal/codemap"
	"github.com/costa/polypod/internal/commands"
	"github.com/costa/polypod/internal/compact"
	"github.com/costa/polypod/internal/completion"
	"github.com/costa/polypod/internal/computeruse"
	"github.com/costa/polypod/internal/config"
	"github.com/costa/polypod/internal/configmerge"
	"github.com/costa/polypod/internal/configval"
	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/database"
	"github.com/costa/polypod/internal/debug"
	"github.com/costa/polypod/internal/depsgraph"
	"github.com/costa/polypod/internal/docgen"
	"github.com/costa/polypod/internal/doctor"
	"github.com/costa/polypod/internal/document"
	"github.com/costa/polypod/internal/dotenv"
	"github.com/costa/polypod/internal/evolution"
	"github.com/costa/polypod/internal/git"
	"github.com/costa/polypod/internal/githook"
	"github.com/costa/polypod/internal/goals"
	"github.com/costa/polypod/internal/headless"
	"github.com/costa/polypod/internal/hooks"
	"github.com/costa/polypod/internal/humanloop"
	"github.com/costa/polypod/internal/initcmd"
	"github.com/costa/polypod/internal/lsp"
	"github.com/costa/polypod/internal/mcp"
	"github.com/costa/polypod/internal/memory"
	"github.com/costa/polypod/internal/migrate"
	"github.com/costa/polypod/internal/multiread"
	"github.com/costa/polypod/internal/notifier"
	"github.com/costa/polypod/internal/observability"
	"github.com/costa/polypod/internal/openapitools"
	"github.com/costa/polypod/internal/permissions"
	"github.com/costa/polypod/internal/profiling"
	"github.com/costa/polypod/internal/project"
	"github.com/costa/polypod/internal/provider"
	"github.com/costa/polypod/internal/prstatus"
	"github.com/costa/polypod/internal/remote"
	"github.com/costa/polypod/internal/review"
	"github.com/costa/polypod/internal/sandbox"
	"github.com/costa/polypod/internal/scheduler"
	"github.com/costa/polypod/internal/secrets"
	"github.com/costa/polypod/internal/security"
	"github.com/costa/polypod/internal/selfmod"
	"github.com/costa/polypod/internal/session"
	"github.com/costa/polypod/internal/sessionmem"
	"github.com/costa/polypod/internal/setup"
	"github.com/costa/polypod/internal/skill"
	"github.com/costa/polypod/internal/sshexec"
	"github.com/costa/polypod/internal/taskplan"
	"github.com/costa/polypod/internal/taskqueue"
	"github.com/costa/polypod/internal/tasks"
	"github.com/costa/polypod/internal/template"
	"github.com/costa/polypod/internal/testgen"
	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/tracking"
	"github.com/costa/polypod/internal/tts"
	"github.com/costa/polypod/internal/turncheckpoint"
	"github.com/costa/polypod/internal/types"
	"github.com/costa/polypod/internal/watcher"
	"github.com/costa/polypod/internal/web"
	"github.com/costa/polypod/internal/webhookgateway"
	"github.com/costa/polypod/internal/whatsappgateway"
	"github.com/costa/polypod/internal/wiring"
	"github.com/costa/polypod/internal/workdir"
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
	if handleRemoteClientSubcommand() {
		return true
	}
	switch os.Args[1] {
	case "doctor":
		dotenv.Load(".")
		configPath, live := defaultConfigPath, false
		for _, arg := range os.Args[2:] {
			if arg == "--live" {
				live = true
			} else if !strings.HasPrefix(arg, "-") {
				configPath = arg
			}
		}
		cfg, err := config.Load(configPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "configuracao indisponivel; confira o caminho informado")
			return true
		}
		diagnosticCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		fmt.Print(doctor.FormatReport(doctor.RunDiagnosticsContext(diagnosticCtx, cfg, live)))
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
	case "auth":
		if len(os.Args) < 4 || os.Args[2] != "chatgpt" {
			fmt.Println("uso: polypod auth chatgpt <login|status|models|logout>")
			return true
		}
		manager := chatgptauth.NewManager(chatgptauth.DefaultCredentialPath())
		switch os.Args[3] {
		case "login":
			ctx, cancel := context.WithTimeout(context.Background(), 11*time.Minute)
			defer cancel()
			cred, err := manager.Login(ctx)
			if err != nil {
				fmt.Fprintf(os.Stderr, "erro conectando ChatGPT: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("ChatGPT conectado ao Polypod: %s\n", cred.Email)
		case "status":
			cred, err := manager.Status()
			if err != nil {
				fmt.Fprintf(os.Stderr, "erro lendo sessao ChatGPT: %v\n", err)
				os.Exit(1)
			}
			if cred == nil {
				fmt.Println("ChatGPT: nao conectado")
			} else {
				fmt.Printf("ChatGPT: conectado (%s), expira em %s\n", cred.Email, cred.ExpiresAt.Local().Format(time.RFC3339))
			}
		case "models":
			models, err := manager.Models(context.Background())
			if err != nil {
				fmt.Fprintf(os.Stderr, "erro listando modelos: %v\n", err)
				os.Exit(1)
			}
			for _, model := range models {
				fmt.Println(model.Name())
			}
		case "logout":
			if err := manager.Logout(); err != nil {
				fmt.Fprintf(os.Stderr, "erro desconectando ChatGPT: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("Sessao ChatGPT removida do Polypod.")
		default:
			fmt.Println("uso: polypod auth chatgpt <login|status|models|logout>")
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
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
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
	cfg.PreserveRecentGroups = 2
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

	if cfg.AI.Provider == "chatgpt" {
		manager := chatgptauth.NewManager(chatgptauth.DefaultCredentialPath())
		reg.Register(provider.NewChatGPT(manager.AccessToken))
	} else {
		primaryKey := cfg.AI.APIKey
		if cred, err := multiAuth.GetCredentials(cfg.AI.Provider); err == nil {
			primaryKey = cred.Token()
		}
		reg.Register(provider.NewOpenAICompat(cfg.AI.Provider, primaryKey, cfg.AI.BaseURL))
	}

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

// prepareSessionHistory keeps the persisted session and the next model context aligned.
func prepareSessionHistory(ctx context.Context, convMgr *conversation.Manager, aiSvc *ai.Service, mem *sessionmem.FileStore, scope string, sess *conversation.Session) ([]conversation.Message, error) {
	history := append([]conversation.Message(nil), convMgr.GetHistory(sess)...)
	prepared, changed := aiSvc.PrepareHistoryForScope(ctx, scope, history)
	if !changed {
		return history, nil
	}
	if mem != nil && scope != "" {
		if _, err := mem.CaptureCompaction(scope, history, prepared); err != nil {
			slog.Warn("session memory capture failed", "error", err, "scope", scope)
		}
	}
	if err := convMgr.ReplaceMessages(ctx, sess, prepared); err != nil {
		return nil, fmt.Errorf("persisting compacted conversation: %w", err)
	}
	return prepared, nil
}

// makeHandler builds the inline message handler that replaces router.Handler.
func sessionMemoryScope(msg adapter.InMessage, sess *conversation.Session) string {
	if msg.Channel == "cli" && msg.Metadata != nil {
		if id := msg.Metadata["polypod_session_id"]; id != "" {
			return id
		}
	}
	return sess.ID
}

func appendToolTrace(store *session.ToolTranscriptStore, scope, turnID string, event ai.ToolEvent, logger *slog.Logger) {
	if store == nil || scope == "" {
		return
	}
	var phase session.ToolTracePhase
	switch event.Type {
	case ai.ToolEventStart:
		phase = session.ToolTraceStart
	case ai.ToolEventComplete:
		phase = session.ToolTraceComplete
	case ai.ToolEventError:
		phase = session.ToolTraceError
	default:
		return
	}
	if err := store.Append(scope, session.ToolTraceEvent{
		TurnID: turnID, CallID: event.ToolID, ToolName: event.ToolName,
		Phase: phase, Arguments: event.Args, Result: event.Result, Error: event.Error,
		ResultBytes: event.ResultBytes, ResultSHA256: event.ResultSHA256,
		Elapsed: event.Elapsed, IsReadOnly: event.IsReadOnly,
	}); err != nil {
		logger.Warn("tool transcript event unavailable", "tool", event.ToolName, "error", err)
	}
}

func toolTranscriptSummary(store *session.ToolTranscriptStore, scope string, logger *slog.Logger) string {
	if store == nil || scope == "" {
		return ""
	}
	summary, err := store.Summary(scope, 400)
	if err != nil {
		logger.Warn("tool transcript summary unavailable", "error", err)
		return ""
	}
	return summary
}

func makeHandler(convMgr *conversation.Manager, aiSvc *ai.Service, mem *sessionmem.FileStore, trace *session.ToolTranscriptStore, turns *turncheckpoint.Manager, logger *slog.Logger) adapter.MessageHandler {
	return func(ctx context.Context, msg adapter.InMessage) (adapter.OutMessage, error) {
		sess, err := convMgr.GetSession(ctx, msg.Channel, msg.UserID)
		if err != nil {
			return adapter.OutMessage{Text: "Erro de sessao"}, err
		}
		scope := sessionMemoryScope(msg, sess)
		history, err := prepareSessionHistory(ctx, convMgr, aiSvc, mem, scope, sess)
		if err != nil {
			return adapter.OutMessage{Text: "Erro ao preparar o contexto"}, err
		}
		userMessage := convMgr.AddUserMessage(ctx, sess, msg.Text)
		turnCtx := turns.BeginTurn(ctx, sess.ID, userMessage.ID)
		turnCtx = ai.WithToolEventCallback(turnCtx, func(event ai.ToolEvent) {
			appendToolTrace(trace, scope, userMessage.ID, event, logger)
		})
		defer func() {
			if err := turns.FinishTurn(turnCtx); err != nil {
				logger.Warn("turn checkpoint finalization failed", "turn_id", userMessage.ID, "error", err)
			}
		}()
		resp, err := aiSvc.Answer(turnCtx, ai.AnswerRequest{
			Channel:               msg.Channel,
			UserID:                msg.UserID,
			Query:                 msg.Text,
			SessionMemoryScope:    scope,
			ToolTranscriptSummary: toolTranscriptSummary(trace, scope, logger),
			History:               history,
			HistoryPrepared:       true,
		})
		if err != nil {
			return adapter.OutMessage{Text: "Erro ao processar: " + err.Error()}, err
		}
		if err := convMgr.AddAssistantMessage(ctx, sess, resp.Content); err != nil {
			return adapter.OutMessage{Text: resp.Content}, err
		}
		return adapter.OutMessage{
			Text:    resp.Content,
			Channel: msg.Channel,
			UserID:  msg.UserID,
			ReplyTo: msg.ID,
		}, nil
	}
}

// makeStreamHandler builds the inline stream handler that replaces router.StreamHandler.
func makeStreamHandler(convMgr *conversation.Manager, aiSvc *ai.Service, mem *sessionmem.FileStore, trace *session.ToolTranscriptStore, turns *turncheckpoint.Manager, logger *slog.Logger) adapter.StreamHandler {
	return func(ctx context.Context, msg adapter.InMessage, chunks chan<- adapter.StreamChunk) {
		defer close(chunks)

		sess, err := convMgr.GetSession(ctx, msg.Channel, msg.UserID)
		if err != nil {
			chunks <- adapter.StreamChunk{Error: err, Done: true}
			return
		}
		scope := sessionMemoryScope(msg, sess)
		history, err := prepareSessionHistory(ctx, convMgr, aiSvc, mem, scope, sess)
		if err != nil {
			chunks <- adapter.StreamChunk{Error: err, Done: true}
			return
		}
		userMessage := convMgr.AddUserMessage(ctx, sess, msg.Text)
		turnCtx := turns.BeginTurn(ctx, sess.ID, userMessage.ID)
		turnCtx = ai.WithToolEventCallback(turnCtx, func(ev ai.ToolEvent) {
			appendToolTrace(trace, scope, userMessage.ID, ev, logger)
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
		defer func() {
			if err := turns.FinishTurn(turnCtx); err != nil {
				logger.Warn("turn checkpoint finalization failed", "turn_id", userMessage.ID, "error", err)
			}
		}()

		resp, err := aiSvc.AnswerStream(turnCtx, ai.AnswerRequest{
			Channel:               msg.Channel,
			UserID:                msg.UserID,
			Query:                 msg.Text,
			SessionMemoryScope:    scope,
			ToolTranscriptSummary: toolTranscriptSummary(trace, scope, logger),
			History:               history,
			HistoryPrepared:       true,
		}, func(delta string) {
			chunks <- adapter.StreamChunk{Delta: delta}
		})
		if err != nil {
			chunks <- adapter.StreamChunk{Error: err, Done: true}
			return
		}
		if err := convMgr.AddAssistantMessage(ctx, sess, resp.Content); err != nil {
			chunks <- adapter.StreamChunk{Error: err, Done: true}
			return
		}
		chunks <- adapter.StreamChunk{Done: true}
	}
}

// autoSaveSession persists the current CLI conversation to disk on shutdown.
func autoSaveSession(convMgr *conversation.Manager, sessStorage session.SessionStorage, sessMgr *session.Manager, currentSessionID, expectedRevision, agentName, mode string, logger *slog.Logger) {
	sess, err := convMgr.GetSession(context.Background(), "cli", "local")
	if err != nil || len(sess.Messages) == 0 {
		return
	}

	now := time.Now()
	cwd, _ := os.Getwd()
	createdAt := sess.CreatedAt
	metadata := session.SessionMetadata{ProjectRoot: cwd, Tag: "cli"}
	if previous, loadErr := sessStorage.Load(currentSessionID); loadErr != nil {
		logger.Warn("failed to load existing session metadata", "error", loadErr)
	} else if previous != nil {
		metadata = previous.Metadata
		if !previous.CreatedAt.IsZero() {
			createdAt = previous.CreatedAt
		}
	}
	if metadata.ProjectRoot == "" {
		metadata.ProjectRoot = cwd
	}
	if metadata.Tag == "" {
		metadata.Tag = "cli"
	}
	if agentName != "" {
		metadata.AgentName = agentName
	}
	if mode != "" {
		metadata.Mode = mode
	}

	persistedMsgs := make([]session.Message, len(sess.Messages))
	for i, m := range sess.Messages {
		persistedMsgs[i] = session.Message{ID: m.ID, Role: string(m.Role), Content: m.Content, Timestamp: m.Timestamp}
	}

	title := session.TitleFromFirstMessage(persistedMsgs)

	persisted := &session.RestoredSession{
		ID:        currentSessionID,
		Title:     title,
		Messages:  persistedMsgs,
		CreatedAt: createdAt,
		UpdatedAt: now,
		Version:   session.CurrentSessionVersion,
		Metadata:  metadata,
		Revision:  expectedRevision,
	}
	if persisted.Metadata.GitBranch == "" && filepath.Clean(persisted.Metadata.ProjectRoot) == filepath.Clean(cwd) {
		if env := session.DetectEnvironment(cwd); env != nil && env.GitBranch != "" {
			persisted.Metadata.GitBranch = env.GitBranch
		}
	}

	savedID := currentSessionID
	if err := sessStorage.Save(persisted); err != nil {
		if !errors.Is(err, session.ErrSessionConflict) {
			logger.Warn("failed to auto-save session", "error", err)
			return
		}
		// This process edited an older snapshot. Preserve its turns under a new
		// ID rather than replacing the version published by another process.
		// The metadata read above may belong to that other process, so the
		// recovery snapshot uses only this process's known CLI context.
		persisted.Metadata = session.SessionMetadata{
			ProjectRoot: cwd,
			Tag:         "cli",
			AgentName:   agentName,
			Mode:        mode,
		}
		if cwd != "" {
			if env := session.DetectEnvironment(cwd); env != nil {
				persisted.Metadata.GitBranch = env.GitBranch
			}
		}
		createdAt = sess.CreatedAt
		if createdAt.IsZero() {
			createdAt = now
		}
		persisted.CreatedAt = createdAt
		for attempt := 0; attempt < 3; attempt++ {
			persisted.ID = session.GenerateSessionID()
			persisted.Revision = ""
			if recoveryErr := sessStorage.Save(persisted); recoveryErr == nil {
				savedID = persisted.ID
				break
			} else if !errors.Is(recoveryErr, session.ErrSessionConflict) {
				logger.Error("failed to preserve conflicting CLI session", "original_id", currentSessionID, "error", recoveryErr)
				fmt.Fprintln(os.Stderr, "Não foi possível preservar a sessão local após conflito de gravação.")
				return
			}
		}
		if savedID == currentSessionID {
			logger.Error("failed to allocate recovery session ID", "original_id", currentSessionID)
			fmt.Fprintln(os.Stderr, "Não foi possível reservar um ID para a sessão de recuperação.")
			return
		}
		logger.Warn("CLI session save conflict; local transcript preserved", "original_id", currentSessionID, "recovery_id", savedID)
		fmt.Fprintf(os.Stderr, "A sessão foi alterada por outro processo. Sua conversa local foi salva como %s. Use /resume %s para abri-la.\n", savedID, savedID)
	}
	logger.Info("session auto-saved", "id", savedID, "messages", len(persistedMsgs), "title", title)

	sessMgr.SaveSessionIndex(session.SessionIndex{
		ID:        savedID,
		Channel:   "cli",
		UserID:    "local",
		Title:     title,
		CreatedAt: createdAt,
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
	var embedder memory.Embedder
	if cfg.Embedding.Enabled {
		backend := strings.ToLower(strings.TrimSpace(cfg.Embedding.Backend))
		switch backend {
		case "", "http":
			configured, err := memory.NewHTTPEmbedder(memory.HTTPEmbeddingConfig{
				BaseURL: cfg.Embedding.BaseURL, APIKey: cfg.Embedding.APIKey, Model: cfg.Embedding.Model})
			if err != nil {
				return fmt.Errorf("configuring semantic memory: %w", err)
			}
			embedder = configured
		case "local":
			configured, err := memory.NewLocalEmbedder(memory.LocalEmbeddingConfig{
				PythonBinary: cfg.Embedding.PythonBinary,
				CacheDir:     cfg.Embedding.CacheDir,
				Timeout:      time.Duration(cfg.Embedding.TimeoutSeconds) * time.Second,
			})
			if err != nil {
				return fmt.Errorf("configuring local semantic memory: %w", err)
			}
			defer configured.Close()
			cached, err := memory.NewCachedEmbedder(configured, filepath.Join(cfg.Data.Dir, "embedding-cache.db"))
			if err != nil {
				return fmt.Errorf("opening local embedding cache: %w", err)
			}
			defer cached.Close()
			embedder = cached
		default:
			return fmt.Errorf("unknown embedding backend %q", backend)
		}
		if managed, ok := memStore.(*memory.ManagedStore); ok {
			managed.SetEmbedder(embedder)
		}
		logger.Info("semantic memory enabled", "backend", backend)
	}
	documentIndex, err := document.OpenIndex(filepath.Join(cfg.Data.Dir, "document-index.db"), embedder)
	if err != nil {
		return fmt.Errorf("opening document knowledge index: %w", err)
	}
	defer documentIndex.Close()
	sys := wiring.WireAll(wiring.Config{
		Logger:      logger,
		WorkingDir:  ".",
		Model:       cfg.AI.Model,
		DataDir:     cfg.Data.Dir,
		MemoryStore: memStore,
	})
	defer sys.ToolRegistry.ClosePTY()
	turnCheckpoint := turncheckpoint.New(filepath.Join(cfg.Data.Dir, "turn-checkpoints"), logger)
	sys.ToolRegistry.SetBeforeFileWrite(func(toolCtx types.ToolContext, path string, original []byte, existed bool) error {
		if toolCtx.Context == nil {
			return nil
		}
		if err := turnCheckpoint.CaptureBefore(toolCtx.Context, path, original, existed); err != nil {
			logger.Warn("turn checkpoint unavailable; continuing file write", "path", path, "error", err)
		}
		return nil
	})
	lspMgr := lsp.NewLSPServerManager(".")
	lspMgr.InitializeDefaults()
	sys.ToolRegistry.AttachLSP(lspMgr)
	defer func() {
		if err := lspMgr.Shutdown(); err != nil {
			logger.Warn("LSP shutdown failed", "error", err)
		}
	}()
	tool.RegisterDocumentKnowledgeTool(sys.ToolRegistry, documentIndex, cfg.WhatsApp.OwnerChat)
	auditHome, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locating audit home: %w", err)
	}
	auditStore, err := audit.Open(filepath.Join(auditHome, ".local", "share", "polypod", "audit"))
	if err != nil {
		return fmt.Errorf("opening durable audit: %w", err)
	}
	sys.Orchestrator.SetAuditStore(auditStore)
	tool.RegisterAuditTool(sys.ToolRegistry, auditStore)
	taskQueue, err := taskqueue.Open(filepath.Join(cfg.Data.Dir, "taskqueue.db"))
	if err != nil {
		return fmt.Errorf("opening task queue: %w", err)
	}
	defer taskQueue.Close()
	planStore, err := taskplan.Open(cfg.Data.Dir)
	if err != nil {
		return fmt.Errorf("opening planned tasks: %w", err)
	}
	defer planStore.Close()
	tool.RegisterPlanTaskTools(sys.ToolRegistry, planStore, taskQueue)

	tool.RegisterAdvancedTools(sys.ToolRegistry, ".")
	tool.RegisterHardwareTools(sys.ToolRegistry)
	tool.RegisterSerialHardwareTools(sys.ToolRegistry)
	computerUseTools := computeruse.RegisterComputerUseTools(sys.ToolRegistry)
	if computerUseTools == 0 {
		logger.Warn("computer use tools unavailable", "reason", "no graphical display environment")
	}
	// New Wave 1 tools
	for _, t := range tool.NewSendMessageTools() {
		sys.ToolRegistry.Register(t)
	}
	agent.RegisterAutonomousAgents(sys.Agents)
	agent.RegisterBuiltinAgents(sys.Agents)

	// Skill registry — load + register all built-in skills
	skills := sys.SkillRegistry
	agents := sys.Agents
	if err := agents.LoadDir(cfg.Data.AgentsDir); err != nil {
		return fmt.Errorf("loading agent definitions: %w", err)
	}
	// Resolve the checked-in catalog from the config location so a CLI started
	// in another project still finds the PolyClaw reference skills. Installs
	// remain local to the current working directory.
	catalogSource := os.Getenv("POLYPOD_POLYCLAW_CATALOG")
	if catalogSource == "" {
		configAbs, absErr := filepath.Abs(configPath)
		if absErr != nil {
			return fmt.Errorf("resolving config path for skill catalog: %w", absErr)
		}
		if resolved, resolveErr := filepath.EvalSymlinks(configAbs); resolveErr == nil {
			configAbs = resolved
		}
		catalogSource = filepath.Join(filepath.Dir(configAbs), "third_party", "polyclaw", "skills")
	}
	localCatalog := skill.NewLocalCatalog(catalogSource, filepath.Join(".polypod", "skills"), skills)
	tool.RegisterSkillCatalogTools(sys.ToolRegistry, localCatalog)
	evolutionStore, err := evolution.Open(filepath.Join(cfg.Data.Dir, "evolution.db"),
		filepath.Join(".polypod", "skills"))
	if err != nil {
		return fmt.Errorf("opening skill evolution: %w", err)
	}
	defer evolutionStore.Close()
	tool.RegisterTaskQueueTool(sys.ToolRegistry, taskQueue, evolutionStore)
	skills.SetRegistrationGuard(func(name string) error {
		return evolutionStore.ProtectAppliedSkillName(ctx, name)
	})
	registerSkills(skills, memStore, agents, cfg.Data.AgentsDir, cfg.Data.Dir, logger)
	for _, name := range skills.List() {
		if loaded, ok := skills.Get(name); ok && loaded.Origin == "custom_script" {
			agents.AddSkill("default", name)
		}
	}
	appliedNames, appliedErr := evolutionStore.AppliedSkillNames(ctx)
	if appliedErr != nil {
		logger.Warn("applied skill unavailable; skipping until repaired", "error", appliedErr)
	}
	if len(appliedNames) > 0 {
		for _, name := range appliedNames {
			agents.AddSkill("default", name)
		}
	}
	tool.RegisterEvolutionTool(sys.ToolRegistry, evolutionStore, func(name string) error {
		path := filepath.Join(".polypod", "skills", name, "SKILL.md")
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		trusted, err := evolutionStore.ValidateAppliedSkillFile(ctx, path, name, content)
		if err != nil {
			return err
		}
		if !trusted {
			return fmt.Errorf("skill %q não consta como aplicada", name)
		}
		parsed, err := skill.ParseSkillFile(path, content)
		if err != nil {
			return err
		}
		if err := parsed.Frontmatter.Validate(); err != nil {
			return err
		}
		if parsed.Frontmatter.Name != name {
			return fmt.Errorf("skill aplicada tem nome divergente: %s", name)
		}
		body := parsed.Body
		if err := skills.RegisterTrusted(&skill.Skill{Name: name, Description: parsed.Frontmatter.Description,
			Origin:     skill.FileSkillOrigin(path),
			Parameters: jsonschema.Definition{Type: jsonschema.Object},
			Execute:    func(map[string]string) (string, error) { return body, nil }}); err != nil {
			return err
		}
		agents.AddSkill("default", name)
		return nil
	})

	// Filesystem skill loader — scans ~/.claude/skills, ./claude/skills and
	// ./.polypod/skills for SKILL.md files and registers each as a simple
	// body-returning skill in the runtime registry.
	fsLoader := skill.NewFSSkillLoader(skill.DefaultSkillDirs()...)
	parsedSkills, skillLoadErr := fsLoader.LoadAll(ctx)
	if skillLoadErr != nil {
		logger.Warn("filesystem skill loader skipped invalid files", "error", skillLoadErr)
	}
	for _, ps := range parsedSkills {
		content, err := os.ReadFile(ps.SourcePath)
		if err != nil {
			logger.Warn("filesystem skill unavailable", "path", ps.SourcePath, "error", err)
			continue
		}
		current, err := skill.ParseSkillFile(ps.SourcePath, content)
		if err != nil {
			logger.Warn("filesystem skill invalid", "path", ps.SourcePath, "error", err)
			continue
		}
		if err := current.Frontmatter.Validate(); err != nil {
			continue
		}
		trusted, err := evolutionStore.ValidateAppliedSkillFile(ctx, ps.SourcePath, current.Frontmatter.Name, content)
		if err != nil {
			logger.Warn("filesystem skill rejected", "path", ps.SourcePath, "error", err)
			continue
		}
		body := current.Body
		loaded := &skill.Skill{
			Name:        current.Frontmatter.Name,
			Description: current.Frontmatter.Description,
			Origin:      skill.FileSkillOrigin(ps.SourcePath),
			Parameters:  jsonschema.Definition{Type: jsonschema.Object},
			Execute:     func(map[string]string) (string, error) { return body, nil },
		}
		if trusted {
			if err := skills.RegisterTrusted(loaded); err != nil {
				logger.Warn("reviewed filesystem skill registration rejected", "path", ps.SourcePath, "error", err)
				continue
			}
		} else {
			if err := skills.TryRegister(loaded); err != nil {
				logger.Warn("filesystem skill registration rejected", "path", ps.SourcePath, "error", err)
				continue
			}
		}
		agents.AddSkill("default", loaded.Name)
	}

	// Plugin-based skills
	beforePlugins := make(map[string]bool)
	for _, name := range skills.List() {
		beforePlugins[name] = true
	}
	pluginManager := skill.NewPluginManager(skill.DefaultSkillDirs(), skills, logger)
	if n, err := pluginManager.Reconcile(ctx); err != nil {
		logger.Warn("plugin skill reconciliation error", "error", err)
	} else if n > 0 {
		logger.Info("plugin skills loaded", "count", n)
	}
	for _, name := range skills.List() {
		if !beforePlugins[name] {
			agents.AddSkill("default", name)
		}
	}

	// Skill hot-reload
	var skillWatcher *skill.SkillWatcher
	if watcher, err := skill.NewSkillWatcher(skill.DefaultSkillDirs(), fsLoader, skills, logger); err != nil {
		logger.Warn("skill watcher unavailable", "error", err)
	} else {
		skillWatcher = watcher
		skillWatcher.SetBeforeRegister(func(path, name string, content []byte) (bool, error) {
			return evolutionStore.ValidateAppliedSkillFile(ctx, path, name, content)
		})
	}
	// Session managers
	sessMgr := session.NewManager(cfg.Data.Dir, logger)
	session.RegisterSkills(skills, sessMgr)
	sessionsDir := filepath.Join(cfg.Data.Dir, "sessions")
	sessStorage := session.NewFileSystemStorage(sessionsDir, logger)
	sessRestorer := session.NewSessionRestorer(sessionsDir, logger)

	restoredSession, currentSessionID := loadRestoredSession(sessRestorer, sessStorage, continueSession, resumeSessionID, logger)

	// Preserve message identities when resuming so turn checkpoints remain
	// addressable after a process restart. A newer durable conversation snapshot
	// wins over an older auto-save when continuing the current CLI session.
	if restoredSession != nil {
		if sess, err := convMgr.GetSession(ctx, "cli", "local"); err == nil {
			if resumeSessionID == "" && sess.UpdatedAt.After(restoredSession.UpdatedAt) {
				restoredSession.Messages = make([]session.Message, len(sess.Messages))
				for i, msg := range sess.Messages {
					restoredSession.Messages[i] = session.Message{
						ID: msg.ID, Role: string(msg.Role), Content: msg.Content, Timestamp: msg.Timestamp,
					}
				}
				logger.Info("newer conversation snapshot retained", "messages", len(sess.Messages))
			} else {
				messages := make([]conversation.Message, 0, len(restoredSession.Messages))
				for i, msg := range restoredSession.Messages {
					id := msg.ID
					if id == "" {
						id = fmt.Sprintf("restored:%s:%d", restoredSession.ID, i)
						restoredSession.Messages[i].ID = id
					}
					messages = append(messages, conversation.Message{
						ID: id, Role: conversation.Role(msg.Role), Content: msg.Content, Timestamp: msg.Timestamp,
					})
				}
				if err := convMgr.ReplaceMessages(ctx, sess, messages); err != nil {
					logger.Warn("failed to save restored messages to conversation store", "error", err)
				}
				if err := sessStorage.Save(restoredSession); err != nil {
					logger.Warn("failed to preserve restored message IDs", "error", err)
				}
				logger.Info("restored messages into conversation", "count", len(restoredSession.Messages))
			}
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
	if err := sched.Load(); err != nil {
		return fmt.Errorf("loading scheduler state: %w", err)
	}
	if cfg.Scheduler.Enabled {
		created, err := scheduler.EnsureDefaultRoutines(sched)
		if err != nil {
			return fmt.Errorf("preparing default routines: %w", err)
		}
		if created > 0 {
			logger.Info("default routines created", "count", created)
		}
	}
	scheduler.RegisterSkills(skills, sched, evolutionStore)
	goalStore, err := goals.Open(filepath.Join(cfg.Data.Dir, "goals.json"))
	if err != nil {
		return fmt.Errorf("initializing operational goals: %w", err)
	}
	goals.RegisterSkills(skills, goalStore)
	humanStore, err := humanloop.NewStore(filepath.Join(cfg.Data.Dir, "humanloop.json"), 500)
	if err != nil {
		return fmt.Errorf("initializing durable human loop: %w", err)
	}
	var heartbeat *humanloop.Processor
	humanloop.RegisterSkills(skills, humanStore, func() {
		if heartbeat != nil {
			heartbeat.Wake()
		}
	})

	// Templates
	tmplReg := template.NewRegistry(cfg.Templates.Dir)
	tmplReg.LoadAll()
	tmplReg.RegisterSkills(skills)

	// MCP
	beforeMCP := make(map[string]bool)
	for _, name := range skills.List() {
		beforeMCP[name] = true
	}
	mcpMgr := mcp.NewManager(logger)
	if err := mcpMgr.SetSubscriptionStore(filepath.Join(cfg.Data.Dir, "mcp-subscriptions.json")); err != nil {
		logger.Warn("MCP resource subscription persistence unavailable", "error", err)
	}
	if err := mcpMgr.SetResourceWakeStore(filepath.Join(cfg.Data.Dir, "mcp-resource-wakes.json")); err != nil {
		return fmt.Errorf("initializing durable MCP resource wakes: %w", err)
	}
	mcpMgr.SetResourceUpdateHandler(func(wakeCtx context.Context, update mcp.ResourceUpdate) error {
		_, err := taskQueue.Create(wakeCtx, taskqueue.CreateInput{
			ID:          update.WakeID,
			Title:       "Revisar recurso MCP: " + update.Server,
			Prompt:      fmt.Sprintf("O recurso MCP assinado mudou. Servidor: %q; URI: %q; recebido: %s. Releia o recurso com read_mcp_resource, avalie a mudança, execute o trabalho pertinente e informe o resultado.", update.Server, update.URI, update.At.Format(time.RFC3339Nano)),
			Priority:    0,
			MaxAttempts: 3,
			Channel:     "mcp_resource",
			Owner:       "owner",
		})
		if err != nil && update.WakeID != "" {
			lookupCtx, cancelLookup := context.WithTimeout(context.Background(), 2*time.Second)
			_, getErr := taskQueue.Get(lookupCtx, update.WakeID)
			cancelLookup()
			if getErr == nil {
				return nil
			}
		}
		return err
	}, mcp.ResourceWakeOptions{MinInterval: 30 * time.Second})
	mcpMgr.SetElicitationHandler(func(qctx context.Context, req *mcp.ElicitationRequest) (*mcp.ElicitationResult, error) {
		return mcp.PromptElicitation(qctx, req, func(askCtx context.Context, question string) (string, error) {
			if sys.UserPromptHandler == nil {
				return "", fmt.Errorf("ask_user indisponivel")
			}
			return sys.UserPromptHandler.Ask(askCtx, tool.UserQuestion{Question: question})
		})
	})
	tool.GetMCPResourceStore().SetBackend(mcpMgr)
	for _, mcpCfg := range cfg.MCP {
		if !mcpCfg.AutoStart {
			continue
		}
		if err := mcpMgr.Connect(ctx, mcp.ServerConfig{
			Name: mcpCfg.Name, Transport: mcpCfg.Transport,
			Command: mcpCfg.Command, Args: mcpCfg.Args,
			URL: mcpCfg.URL, Env: mcpCfg.Env, Headers: mcpCfg.Headers,
		}); err != nil {
			logger.Warn("mcp server failed", "name", mcpCfg.Name, "error", err)
		}
	}
	mcpToolCount := mcpMgr.RegisterTools(skills)
	mcp.RegisterSkills(skills, mcpMgr)
	if mcpToolCount > 0 {
		logger.Info("mcp tools loaded", "count", mcpToolCount)
	}
	for _, name := range skills.List() {
		if !beforeMCP[name] {
			agents.AddSkill("default", name)
		}
	}
	defer mcpMgr.Close()
	// Wire every skill registered after WireAll, including scheduler, goals,
	// human loop, filesystem skills and MCP, into the agent's tool path.
	tool.RegisterAllSkills(sys.ToolRegistry, skills)
	// Custom skills created during a turn and filesystem hot reloads must also
	// pass through the tool registry without waiting for a process restart.
	var skillSync sync.Mutex
	skills.SetOnChange(func(name string, _ bool) {
		skillSync.Lock()
		defer skillSync.Unlock()
		if _, exists := skills.Get(name); exists {
			tool.RegisterAllSkills(sys.ToolRegistry, skills)
			agents.AddSkill("default", name)
		} else {
			sys.ToolRegistry.Remove("skill_" + name)
			agents.RemoveSkill("default", name)
		}
	})
	tool.RegisterPluginTools(sys.ToolRegistry, pluginManager)
	if skillWatcher != nil {
		go skillWatcher.Start(ctx)
	}
	pluginManager.Start(ctx)

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
	if vision, ok := providerReg.Get(cfg.AI.Provider); ok {
		tool.RegisterLocalImageVisionTool(sys.ToolRegistry, vision, cfg.AI.Model)
	}

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
	autoCompactSvc := compact.NewAutoCompactService(compact.AutoCompactConfig{
		CtxWinMgr:      ctxWinMgr,
		CompactService: compactSvc,
		Logger:         logger,
		Enabled:        true,
		Threshold:      0.70,
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
		ActionTrigger: func(triggerCtx context.Context, trigger watcher.Trigger) {
			prompt := fmt.Sprintf("Execute a instrução AI! no arquivo %s, linha %d: %s\n\nContexto do código:\n%s", trigger.File, trigger.Line, trigger.Prompt, trigger.Content)
			state, spawnErr := tasks.SpawnAgentTask(triggerCtx, sys.TaskRegistry,
				fmt.Sprintf("AI! %s:%d", trigger.File, trigger.Line), prompt, "general-purpose", logger)
			if spawnErr != nil {
				logger.Warn("AI! task could not start", "file", trigger.File, "line", trigger.Line, "error", spawnErr)
				return
			}
			logger.Info("AI! task started", "task_id", state.ID, "file", trigger.File, "line", trigger.Line)
		},
	})

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
	commands.BindTaskRuntime(sys.Commands, sys.TaskRegistry, sched, logger)
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
		"computer_use_tools", computerUseTools,
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
	workspaceStore, err := workdir.Open(filepath.Join(cfg.Data.Dir, "workdirs.json"))
	if err != nil {
		logger.Warn("scoped workdirs unavailable", "error", err)
	} else {
		aiSvc.SetWorkdirStore(workspaceStore)
	}
	aiSvc.SetAutoCompact(autoCompactSvc)
	sessionMemory, err := sessionmem.NewFileStore(cfg.Data.Dir)
	if err != nil {
		return fmt.Errorf("opening session memory: %w", err)
	}
	aiSvc.SetSessionMemory(sessionMemory)
	toolTranscript, err := session.NewToolTranscriptStore(cfg.Data.Dir)
	if err != nil {
		return fmt.Errorf("opening tool transcript: %w", err)
	}

	// Cost engine
	costEngine := tracking.NewCostEngine(logger)
	aiClient.SetCostEngine(costEngine)

	// Wire the real sub-agent executor now that ai.Client is available
	wiring.WireAIClient(sys, aiClient, logger)
	defer sys.Swarm.Shutdown(15 * time.Second)
	active.Start(ctx)
	defer active.Shutdown()

	// Log auto-detected provider credentials
	if detected := multiAuth.AutoDetect(); len(detected) > 0 {
		names := make([]string, 0, len(detected))
		for n := range detected {
			names = append(names, n)
		}
		logger.Info("auth: provider credentials available", "providers", names)
	}

	// Inline handlers (replace router)
	handler := makeHandler(convMgr, aiSvc, sessionMemory, toolTranscript, turnCheckpoint, logger)
	streamHandler := makeStreamHandler(convMgr, aiSvc, sessionMemory, toolTranscript, turnCheckpoint, logger)
	// Background routines and one-off tasks share the same operational state.
	// Serialize their full agent turns while leaving interactive messages free.
	backgroundAgentGate := make(chan struct{}, 1)
	taskQueue.SetHandler(func(taskCtx context.Context, task taskqueue.Task) (string, error) {
		select {
		case backgroundAgentGate <- struct{}{}:
			defer func() { <-backgroundAgentGate }()
		case <-taskCtx.Done():
			return "", taskCtx.Err()
		}
		runID := fmt.Sprintf("%s:terminal:%d", task.ID, task.Attempts)
		planned, beginErr := evolutionStore.BeginVerificationRun(taskCtx, "task", task.ID, runID)
		if beginErr != nil {
			logger.Warn("task verification baseline unavailable", "task_id", task.ID, "attempt", task.Attempts, "error", beginErr)
		}
		prompt := fmt.Sprintf("TAREFA PONTUAL: %s\nID: %s\nTentativa: %d\nObjetivo:\n%s\n\nExecute o objetivo completo e confira o resultado. Em uma retomada, confira primeiro se a tentativa anterior ja produziu o efeito desejado antes de repetir a acao.",
			task.Title, task.ID, task.Attempts, task.Prompt)
		turnCtx, toolTrace := evolution.WithToolTrace(taskCtx)
		out, err := handler(turnCtx, adapter.InMessage{
			ID:        fmt.Sprintf("task-%s-%d", task.ID, task.Attempts),
			Channel:   "task_queue",
			UserID:    task.ID,
			Text:      prompt,
			Timestamp: time.Now(),
		})
		if planned && (err == nil || task.Attempts >= task.MaxAttempts) {
			// Capture the postcondition now. A delayed notification must never
			// inspect a file that another run has since changed.
			finishCtx, finishCancel := context.WithTimeout(context.Background(), 60*time.Second)
			_, finishErr := evolutionStore.FinishVerificationRun(finishCtx, "task", task.ID, runID)
			finishCancel()
			if finishErr != nil {
				logger.Warn("task verification final state unavailable", "task_id", task.ID, "attempt", task.Attempts, "error", finishErr)
			}
			// The terminal notice uses this stable key. Save before the queue
			// persists its result so a retried notice can recover the trace.
			traceCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			traceErr := evolutionStore.SaveToolTrace(traceCtx, "task", runID, toolTrace.Names())
			cancel()
			if traceErr != nil {
				logger.Warn("task tool trace unavailable", "task_id", task.ID, "attempt", task.Attempts, "error", traceErr)
			}
		}
		return out.Text, err
	})
	if err := planStore.ReconcileExecutions(ctx, taskQueue); err != nil {
		logger.Warn("planned execution reconciliation incomplete", "error", err)
	}

	// Optional remote human relay for questions and notifications. Credentials
	// come from environment-expanded config and are never written by Polypod.
	var remoteRelay *approval.TelegramApprovalRelay
	if cfg.Remote.Enabled {
		if remoteErr := cfg.Remote.Validate(); remoteErr != nil {
			logger.Warn("remote relay disabled", "reason", remoteErr)
		} else {
			remoteRelay = approval.NewTelegramApprovalRelay(approval.TelegramRelayConfig{
				BotToken:       cfg.Remote.BotToken,
				ChatID:         cfg.Remote.ChatID,
				AllowedUserIDs: cfg.Remote.AllowedUserIDs,
				Timeout:        time.Duration(cfg.Remote.TimeoutSeconds) * time.Second,
				Logger:         logger,
				StatePath:      filepath.Join(cfg.Data.Dir, "telegram-relay.json"),
				Inbound: func(externalID, userID, text string) error {
					_, err := humanStore.EnqueueInboundOnce("telegram", userID, text, externalID)
					if err == nil && heartbeat != nil {
						heartbeat.Wake()
					}
					return err
				},
			})
			logger.Info("remote human relay active", "channel", "telegram", "chat_id", cfg.Remote.ChatID)
		}
	}

	localNotifier := notifier.NewNotifier(logger)
	var remoteSessions *remoteSessionRuntime
	var whatsapp *whatsappgateway.Gateway
	var whatsappNative *whatsappgateway.Native
	var whatsappMedia *whatsappgateway.MediaInterpreter
	var whatsappQuestions *whatsappgateway.QuestionRelay
	if cfg.WhatsApp.Enabled {
		whatsappNative = &whatsappgateway.Native{
			SessionDir: cfg.WhatsApp.SessionDir,
			StatusPath: filepath.Join(cfg.Data.Dir, "whatsapp-status.json"),
			Logger:     logger,
		}
		if err := whatsappNative.Preflight(); err != nil {
			return fmt.Errorf("checking whatsapp session: %w", err)
		}
		whatsapp, err = whatsappgateway.New(humanStore, whatsappNative,
			cfg.WhatsApp.AllowedChats, cfg.WhatsApp.AllowedSenders, func() {
				if heartbeat != nil {
					heartbeat.Wake()
				}
			})
		if err != nil {
			return fmt.Errorf("initializing whatsapp: %w", err)
		}
		whatsappQuestions, err = whatsappgateway.NewQuestionRelay(humanStore, whatsapp,
			filepath.Join(cfg.Data.Dir, "whatsapp-questions.json"), cfg.WhatsApp.OwnerChat)
		if err != nil {
			return fmt.Errorf("initializing whatsapp questions: %w", err)
		}
		whatsapp.SetQuestionRelay(whatsappQuestions)
		if cfg.WhatsApp.OwnerChat != "" {
			if err := whatsappgateway.RegisterNotificationSkill(skills, whatsapp, cfg.WhatsApp.OwnerChat); err != nil {
				return fmt.Errorf("registering whatsapp notification: %w", err)
			}
			agents.AddSkill("default", "whatsapp_notify")
			agents.AddSkill("default", "whatsapp_send_media")
			if cfg.WhatsApp.VoiceOutput.Enabled {
				voice := cfg.WhatsApp.VoiceOutput
				piper, err := tts.NewPiper(tts.PiperConfig{
					PythonBinary: voice.PythonBinary,
					ModelPath:    voice.ModelPath,
					FFmpegBinary: voice.FFmpegBinary,
					Timeout:      time.Duration(voice.TimeoutSeconds) * time.Second,
				})
				if err != nil {
					return fmt.Errorf("configuring WhatsApp voice output: %w", err)
				}
				if err := tts.RegisterWhatsAppVoiceSkill(skills, whatsapp, cfg.WhatsApp.OwnerChat, piper); err != nil {
					return fmt.Errorf("registering WhatsApp voice output: %w", err)
				}
				agents.AddSkill("default", "whatsapp_send_voice")
			}
			tool.RegisterAllSkills(sys.ToolRegistry, skills)
		}
		vision, _ := providerReg.Get(cfg.AI.Provider)
		whatsappMedia = &whatsappgateway.MediaInterpreter{
			MediaDir:    filepath.Join(cfg.WhatsApp.SessionDir, "media"),
			Vision:      vision,
			VisionModel: cfg.AI.Model,
			OCRLanguage: os.Getenv("POLYPOD_OCR_LANG"),
			IndexDocument: func(ctx context.Context, event whatsappgateway.Event, text, format string) (string, string, error) {
				sum := sha256.Sum256([]byte(event.ChatID + "\x00" + event.ID))
				sourceID := hex.EncodeToString(sum[:])
				indexed, _, err := documentIndex.IndexText(ctx, event.ChatID, sourceID, text, format)
				if err != nil {
					return "", "", err
				}
				return sourceID, indexed.Citation(1), nil
			},
		}
		if cfg.WhatsApp.AudioTranscription.Enabled {
			asr := cfg.WhatsApp.AudioTranscription
			switch strings.ToLower(strings.TrimSpace(asr.Backend)) {
			case "", "http":
				transcriber, err := audio.NewHTTPTranscriber(asr.Endpoint, asr.Model, asr.APIKey, nil)
				if err != nil {
					return fmt.Errorf("configuring WhatsApp HTTP audio transcription: %w", err)
				}
				whatsappMedia.TranscribeAudio = transcriber.Transcribe
			case "local":
				transcriber, err := audio.NewLocalTranscriber(audio.LocalConfig{
					PythonBinary: asr.PythonBinary,
					ScriptPath:   asr.ScriptPath,
					ModelPath:    asr.ModelPath,
					FFmpegBinary: asr.FFmpegBinary,
					Language:     asr.Language,
					Timeout:      time.Duration(asr.TimeoutSeconds) * time.Second,
				})
				if err != nil {
					return fmt.Errorf("configuring WhatsApp local audio transcription: %w", err)
				}
				whatsappMedia.TranscribeAudio = transcriber.Transcribe
			default:
				return fmt.Errorf("unsupported WhatsApp audio transcription backend %q", asr.Backend)
			}
		}
	}
	for _, name := range agents.Get("default").Skills {
		if _, direct := sys.ToolRegistry.Get(name); direct {
			continue
		}
		if _, bridged := sys.ToolRegistry.Get("skill_" + name); !bridged {
			logger.Warn("agent skill unavailable in tool registry", "skill", name)
		}
	}
	if remoteRelay != nil || whatsappQuestions != nil || os.Getenv("POLYPOD_REMOTE_SESSION_HOST") == "1" {
		sys.UserPromptHandler.SetQuestionResolved(func(requestID, answer string) error {
			if remoteSessions != nil {
				owned, err := remoteSessions.OwnsQuestion(requestID)
				if err != nil {
					return err
				}
				if owned {
					return remoteSessions.ResolveQuestion(requestID, answer)
				}
			}
			if whatsappQuestions != nil && whatsappQuestions.Owns(requestID) {
				return whatsappQuestions.ResolveQuestion(requestID, answer)
			}
			if remoteRelay != nil {
				return remoteRelay.ResolveQuestion(requestID, answer)
			}
			return nil
		})
		sys.UserPromptHandler.SetRemoteAsk(func(questionCtx context.Context, requestID string, question tool.UserQuestion) (string, error) {
			var prompt strings.Builder
			if question.Header != "" {
				fmt.Fprintf(&prompt, "%s\n\n", question.Header)
			}
			prompt.WriteString(question.Question)
			for _, option := range question.Options {
				fmt.Fprintf(&prompt, "\n- %s: %s", option.Label, option.Description)
			}
			if _, fromWhatsApp := whatsappgateway.InboundFromContext(questionCtx); fromWhatsApp {
				if whatsappQuestions == nil {
					return "", errors.New("whatsapp question relay is unavailable")
				}
				return whatsappQuestions.Ask(questionCtx, requestID, prompt.String())
			}
			if _, fromRemoteSession := remote.InboundFromContext(questionCtx); fromRemoteSession {
				return remoteSessions.Ask(questionCtx, requestID, prompt.String())
			}
			if remoteRelay != nil {
				return remoteRelay.AskQuestion(questionCtx, requestID, prompt.String())
			}
			if whatsappQuestions != nil {
				return whatsappQuestions.Ask(questionCtx, requestID, prompt.String())
			}
			return "", humanloop.ErrRemoteUnavailable
		})
	}
	heartbeat, err = humanloop.NewProcessor(humanloop.ProcessorConfig{
		Store:       humanStore,
		Interval:    30 * time.Second,
		Timeout:     30 * time.Minute,
		MaxAttempts: 4,
		Process: func(processCtx context.Context, msg humanloop.Message) (string, error) {
			processCtx = tool.WithRemoteDocumentScope(processCtx, msg.Channel, msg.UserID)
			if msg.Channel == remoteSessionChannel {
				if remoteSessions == nil {
					return "", humanloop.ErrRemoteUnavailable
				}
				sessionID, participant, found := strings.Cut(msg.UserID, ":")
				if !found {
					return "", fmt.Errorf("origem da sessão remota inválida")
				}
				processCtx = remote.WithInboundContext(processCtx, remote.InboundContext{
					MessageID: msg.ID, SessionID: sessionID, ParticipantID: participant,
				})
				if _, ok := remote.InboundFromContext(processCtx); !ok {
					return "", fmt.Errorf("identidade da sessão remota inválida")
				}
			}
			messageText := msg.Text
			var askStatus *whatsappgateway.AskDeliveryStatus
			if msg.Channel == whatsappgateway.Channel {
				event, decodeErr := whatsappgateway.Decode(msg)
				if decodeErr != nil {
					return "", fmt.Errorf("decoding whatsapp inbox: %w", decodeErr)
				}
				var cancel context.CancelFunc
				processCtx, cancel = context.WithCancel(processCtx)
				defer cancel()
				askStatus = whatsappgateway.NewAskDeliveryStatus(cancel)
				processCtx = whatsappgateway.WithInboundContext(processCtx, whatsappgateway.InboundContext{
					MessageID: msg.ID, ChatID: event.ChatID, SenderID: event.SenderID, AskStatus: askStatus,
				})
				messageText, decodeErr = whatsappMedia.Render(processCtx, event)
				if decodeErr != nil {
					return "", fmt.Errorf("interpreting whatsapp media: %w", decodeErr)
				}
			}
			out, processErr := handler(processCtx, adapter.InMessage{
				ID:        msg.ID,
				Channel:   msg.Channel,
				UserID:    msg.UserID,
				Text:      messageText,
				Timestamp: msg.CreatedAt,
			})
			if whatsappQuestions != nil && whatsappQuestions.ShouldDeferUnavailable(askStatus) {
				return "", humanloop.ErrRemoteUnavailable
			}
			return out.Text, processErr
		},
		Deliver: func(deliverCtx context.Context, msg humanloop.Message) error {
			if msg.Channel == remoteSessionChannel {
				if humanStore.DeliveredTo(msg.ID, remoteSessionChannel) {
					return nil
				}
				if err := remoteSessions.Deliver(deliverCtx, msg); err != nil {
					return err
				}
				return humanStore.MarkDelivered(msg.ID, remoteSessionChannel)
			}
			if msg.Channel == whatsappgateway.Channel {
				if whatsapp == nil {
					return errors.New("whatsapp delivery is not configured")
				}
				if humanStore.DeliveredTo(msg.ID, whatsappgateway.Channel) {
					return nil
				}
				if err := whatsapp.Deliver(deliverCtx, msg); err != nil {
					return err
				}
				return humanStore.MarkDelivered(msg.ID, whatsappgateway.Channel)
			}
			if msg.Channel == webhookgateway.Channel {
				// Webhook callers poll the durable inbound result by ID. Mark its
				// companion outbox item delivered without sending a desktop alert.
				if humanStore.DeliveredTo(msg.ID, "webhook:poll") {
					return nil
				}
				return humanStore.MarkDelivered(msg.ID, "webhook:poll")
			}
			var deliveryErr error
			if whatsapp != nil && cfg.WhatsApp.OwnerChat != "" &&
				(msg.Channel == "notification" || msg.Channel == "scheduler" || msg.Channel == "task_queue") &&
				!humanStore.DeliveredTo(msg.ID, whatsappgateway.Channel) {
				copy := msg
				copy.Channel, copy.UserID, copy.ReplyTo = whatsappgateway.Channel, cfg.WhatsApp.OwnerChat, ""
				err := whatsapp.Deliver(deliverCtx, copy)
				if err == nil {
					err = humanStore.MarkDelivered(msg.ID, whatsappgateway.Channel)
				}
				deliveryErr = errors.Join(deliveryErr, err)
			}
			if remoteRelay != nil && !humanStore.DeliveredTo(msg.ID, "telegram") {
				err := remoteRelay.SendNotificationChunks(deliverCtx, "Polypod", msg.Text,
					func(index int) bool { return humanStore.DeliveredTo(msg.ID, fmt.Sprintf("telegram:chunk:%d", index)) },
					func(index int) error {
						return humanStore.MarkDelivered(msg.ID, fmt.Sprintf("telegram:chunk:%d", index))
					})
				if err == nil {
					err = humanStore.MarkDelivered(msg.ID, "telegram")
				}
				deliveryErr = errors.Join(deliveryErr, err)
			}
			if !humanStore.DeliveredTo(msg.ID, "local") {
				err := localNotifier.Notify(deliverCtx, "Polypod", msg.Text)
				if err == nil {
					err = humanStore.MarkDelivered(msg.ID, "local")
				}
				deliveryErr = errors.Join(deliveryErr, err)
			}
			return deliveryErr
		},
		Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("initializing heartbeat: %w", err)
	}
	remoteSessions, err = startRemoteSessionRuntime(ctx, cfg.Data.Dir, humanStore, heartbeat.Wake, logger)
	if err != nil {
		return fmt.Errorf("initializing remote sessions: %w", err)
	}
	if remoteSessions != nil {
		defer func() {
			if closeErr := remoteSessions.Close(); closeErr != nil {
				logger.Warn("remote sessions close failed", "error", closeErr)
			}
		}()
	}
	taskQueue.SetNotifier(func(noticeCtx context.Context, notice taskqueue.Notice) error {
		label := fmt.Sprintf("Tarefa %s", notice.TaskID)
		if notice.Channel == "plan" {
			queued, err := taskQueue.Get(noticeCtx, notice.TaskID)
			if err != nil {
				return fmt.Errorf("consultando execução do plano: %w", err)
			}
			planned, err := planStore.SyncExecution(noticeCtx, queued)
			if err != nil {
				return fmt.Errorf("atualizando resultado do plano: %w", err)
			}
			label = fmt.Sprintf("Plano %s (execução %s)", planned.ID, notice.TaskID)
		}
		message := fmt.Sprintf("%s concluiu com status %s.", label, notice.Status)
		if notice.Error != "" {
			message += " Erro: " + notice.Error
		} else if strings.TrimSpace(notice.Result) != "" {
			message += " Resultado: " + notice.Result
		}
		if _, err := humanStore.EnqueueOutboundOnce("task_queue", "owner", message, notice.TaskID, notice.Key); err != nil {
			return err
		}
		heartbeat.Wake()
		planned, err := evolutionStore.HasVerificationPlan(noticeCtx, "task", notice.TaskID)
		if err != nil {
			return fmt.Errorf("checking task verification plan: %w", err)
		}
		if !planned {
			return nil
		}
		attempts, err := taskQueue.History(noticeCtx, notice.TaskID)
		if err != nil {
			return fmt.Errorf("loading task attempts for verification: %w", err)
		}
		var startedAt time.Time
		for _, attempt := range attempts {
			if attempt.StartedAt <= 0 {
				continue
			}
			started := time.UnixMilli(attempt.StartedAt)
			if startedAt.IsZero() || started.Before(startedAt) {
				startedAt = started
			}
		}
		if _, _, err := evolutionStore.VerifyAndRecordAt(noticeCtx, "task", notice.TaskID, notice.Key, nil, startedAt); err != nil {
			return fmt.Errorf("verifying task result: %w", err)
		}
		return nil
	})
	heartbeat.Start(ctx)
	defer heartbeat.Stop()
	if cfg.Webhook.Enabled {
		webhookIngress, err := webhookgateway.New(cfg.Webhook, humanStore, heartbeat.Wake, logger)
		if err != nil {
			return fmt.Errorf("initializing webhook ingress: %w", err)
		}
		if err := webhookIngress.Start(ctx); err != nil {
			return fmt.Errorf("starting webhook ingress: %w", err)
		}
		defer webhookIngress.Stop()
		logger.Info("webhook ingress active", "listen", cfg.Webhook.Listen)
	}
	taskQueue.Start(ctx)
	defer taskQueue.Stop()
	if whatsappQuestions != nil {
		go whatsappQuestions.Run(ctx)
	}
	if whatsappNative != nil {
		bridgeCtx, stopBridge := context.WithCancel(ctx)
		defer stopBridge()
		go func() {
			backoff := 5 * time.Second
			for bridgeCtx.Err() == nil {
				started := time.Now()
				err := whatsappNative.Run(bridgeCtx, whatsapp)
				if bridgeCtx.Err() != nil {
					return
				}
				logger.Error("whatsapp inbound stopped; reconnecting", "error", err, "retry_after", backoff)
				if time.Since(started) > 5*time.Minute {
					backoff = 5 * time.Second
				}
				timer := time.NewTimer(backoff)
				select {
				case <-bridgeCtx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
				if backoff < time.Minute {
					backoff *= 2
					if backoff > time.Minute {
						backoff = time.Minute
					}
				}
			}
		}()
	}
	if remoteRelay != nil {
		relayCtx, stopRelay := context.WithCancel(ctx)
		defer stopRelay()
		go func() {
			if err := remoteRelay.Run(relayCtx); err != nil && relayCtx.Err() == nil {
				logger.Error("remote inbound stopped", "error", err)
			}
		}()
	}

	// Automatic long-term memory consolidation uses a tool-free model call
	// and a durable watermark so restarts do not duplicate processed batches.
	dreamSvc := automemory.NewDreamService(automemory.DreamConfig{
		Logger:        logger,
		Memory:        memStore,
		SessionStore:  store,
		StateFile:     filepath.Join(cfg.Data.Dir, "dream-state.json"),
		IdleThreshold: 5 * time.Minute,
		Interval:      30 * time.Minute,
		MaxMessages:   50,
		ExtractFn: func(extractCtx context.Context, transcript string) ([]automemory.ExtractedMemory, error) {
			resp, err := aiClient.Complete(extractCtx, []ai.ChatMessage{
				{Role: "system", Content: automemory.DreamExtractionInstructions},
				{Role: "user", Content: "<transcript>\n" + transcript + "</transcript>"},
			})
			if err != nil {
				return nil, err
			}
			return automemory.ParseExtractedMemories(resp.Content)
		},
	})
	aiSvc.SetActivityCallback(dreamSvc.MarkActivity)
	dreamSvc.Start(ctx)
	defer dreamSvc.Stop()
	// Preserve a compact, low-confidence history of completed user turns even
	// when the fact extractor finds no durable preference or fact. This journal
	// does not assert that an external action succeeded.
	episodeSvc := automemory.NewEpisodeService(automemory.EpisodeConfig{
		Logger:       logger,
		Memory:       memStore,
		SessionStore: store,
		StateFile:    filepath.Join(cfg.Data.Dir, "episode-state.json"),
		Interval:     time.Minute,
		MaxMessages:  50,
	})
	episodeSvc.Start(ctx)
	defer episodeSvc.Stop()

	// The operational heartbeat is itself a full agent routine. Its persisted
	// schedule survives restarts and uses NO_REPLY when there is nothing useful
	// to report to the owner.
	const heartbeatID = "system_heartbeat"
	if cfg.Scheduler.Enabled && cfg.Scheduler.HeartbeatEnabled {
		heartbeatSchedule := "@every " + cfg.Scheduler.HeartbeatInterval
		const heartbeatPrompt = "Consulte goal_review, inbox_list (especialmente itens failed), scheduler_list, task_queue action=status (depois list para failed quando preciso), skill_evolution action=patterns e memoria de pendencias. Avance somente proximas acoes prontas e autorizadas; confirme o resultado e registre evidencia com goal_update. Para padroes repetidos e verificados, proponha/revise/aplique skill util. Respeite metas paused/blocked e dependencias. Avise o dono quando houver algo relevante. Se faltar uma informacao essencial, use ask_user. Se nao houver nada a fazer ou relatar, responda exatamente NO_REPLY."
		if existing, ok := sched.GetTask(heartbeatID); !ok {
			if err := sched.AddTask(&scheduler.Task{ID: heartbeatID, Name: "Acompanhamento operacional", Schedule: heartbeatSchedule, Prompt: heartbeatPrompt, Channel: "system", UserID: "owner", MisfirePolicy: "skip", MaxRetries: 1}); err != nil {
				logger.Error("failed to create operational heartbeat", "error", err)
			}
		} else {
			if existing.Schedule != heartbeatSchedule || existing.Prompt != heartbeatPrompt {
				if err := sched.UpdateTask(heartbeatID, map[string]string{"schedule": heartbeatSchedule, "prompt": heartbeatPrompt}); err != nil {
					logger.Error("failed to update operational heartbeat", "error", err)
				}
			}
			if err := sched.SetEnabled(heartbeatID, true); err != nil {
				logger.Error("failed to enable operational heartbeat", "error", err)
			}
		}
	} else if _, ok := sched.GetTask(heartbeatID); ok {
		if err := sched.SetEnabled(heartbeatID, false); err != nil {
			logger.Error("failed to disable operational heartbeat", "error", err)
		}
	}

	// Human-language routines wake the same complete operational agent used by
	// the CLI, with a separate persistent conversation per routine.
	// Scheduler ticks may launch several due routines at once. Use the same
	// gate as the one-off task queue so autonomous plans do not overlap.
	sched.SetPromptRunner(func(runCtx context.Context, task *scheduler.Task) (string, error) {
		select {
		case backgroundAgentGate <- struct{}{}:
			defer func() { <-backgroundAgentGate }()
		case <-runCtx.Done():
			return "", runCtx.Err()
		}
		planned, beginErr := evolutionStore.BeginVerificationRun(runCtx, "routine", task.ID, task.CurrentRunID)
		if beginErr != nil {
			logger.Warn("routine verification baseline unavailable", "routine_id", task.ID, "run_id", task.CurrentRunID, "error", beginErr)
		}
		prompt := fmt.Sprintf("ROTINA AGENDADA: %s\nID: %s\nExecucao: %s\nTentativa: %d\nObjetivo:\n%s\n\nExecute o objetivo completo, verifique o resultado e relate de forma objetiva. Se esta for uma retomada, confirme o efeito da tentativa anterior antes de repetir a acao. Nao crie outra rotina a menos que o objetivo mande explicitamente.", task.Name, task.ID, task.CurrentRunID, task.CurrentAttempt, task.Prompt)
		turnCtx, toolTrace := evolution.WithToolTrace(runCtx)
		out, err := handler(turnCtx, adapter.InMessage{
			ID:        fmt.Sprintf("routine-%s-%s-%d", task.ID, task.CurrentRunID, task.CurrentAttempt),
			Channel:   "scheduler",
			UserID:    task.ID,
			Text:      prompt,
			Timestamp: time.Now(),
		})
		if planned && (err == nil || task.CurrentAttempt > task.MaxRetries) {
			finishCtx, finishCancel := context.WithTimeout(context.Background(), 60*time.Second)
			_, finishErr := evolutionStore.FinishVerificationRun(finishCtx, "routine", task.ID, task.CurrentRunID)
			finishCancel()
			if finishErr != nil {
				logger.Warn("routine verification final state unavailable", "routine_id", task.ID, "run_id", task.CurrentRunID, "error", finishErr)
			}
			traceCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			traceErr := evolutionStore.SaveToolTrace(traceCtx, "routine", task.CurrentRunID, toolTrace.Names())
			cancel()
			if traceErr != nil {
				logger.Warn("routine tool trace unavailable", "routine_id", task.ID, "run_id", task.CurrentRunID, "error", traceErr)
			}
		}
		return out.Text, err
	})
	sched.SetDurableCallback(func(task *scheduler.Task, notice scheduler.CompletionNotice) error {
		if notice.Error != "" || strings.TrimSpace(notice.Result) != "NO_REPLY" {
			message := fmt.Sprintf("Rotina %q concluida com status %s.", task.Name, notice.Status)
			if notice.Error != "" {
				message += " Erro: " + notice.Error
			} else if strings.TrimSpace(notice.Result) != "" {
				message += " Resultado: " + notice.Result
			}
			if _, err := humanStore.EnqueueOutboundOnce("scheduler", "owner", message, task.ID, notice.RunID); err != nil {
				return err
			}
			heartbeat.Wake()
		}
		verifyCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		planned, err := evolutionStore.HasVerificationPlan(verifyCtx, "routine", task.ID)
		if err != nil {
			return fmt.Errorf("checking routine verification plan: %w", err)
		}
		if !planned {
			return nil
		}
		history, err := sched.History(task.ID)
		if err != nil {
			return fmt.Errorf("loading routine history for verification: %w", err)
		}
		var startedAt time.Time
		for _, run := range history {
			if run.RunID == notice.RunID && !run.StartedAt.IsZero() &&
				(startedAt.IsZero() || run.StartedAt.Before(startedAt)) {
				startedAt = run.StartedAt
			}
		}
		_, _, err = evolutionStore.VerifyAndRecordAt(verifyCtx, "routine", task.ID, notice.RunID, nil, startedAt)
		if err != nil {
			return fmt.Errorf("verifying routine result: %w", err)
		}
		return nil
	})
	if cfg.Scheduler.Enabled {
		go func() {
			if err := sched.Start(ctx); err != nil {
				logger.Error("scheduler stopped with error", "error", err)
			}
		}()
		defer sched.Stop()
	}
	if headlessPrompt == "" {
		// All tools, channels, and routine handlers are ready before workers
		// from an eligible crashed team can receive restored messages.
		sys.StartTeamRecovery(ctx)
		defer sys.StopTeamRecovery()
	}

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
	cliCh.SetTurnCheckpointManager(turnCheckpoint)
	cliCh.SetTaskRegistry(sys.TaskRegistry)
	cliCh.SetTaskQueue(taskQueue)
	cliCh.SetSessionEmbedder(embedder)
	cliCh.SetSessionMemory(sessionMemory)
	cliCh.SetToolTranscriptStore(toolTranscript)
	if restoredSession != nil {
		cliCh.SetActiveSessionSnapshot(currentSessionID, restoredSession.Revision)
	} else {
		cliCh.SetActiveSessionSnapshot(currentSessionID, "")
	}
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
	autoSaveSession(convMgr, sessStorage, sessMgr, cliCh.ActiveSessionID(), cliCh.ActiveSessionRevision(), aiSvc.ActiveAgent(), string(modeState.Current), logger)

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
