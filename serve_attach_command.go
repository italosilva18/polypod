package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/costa/polypod/internal/accountauth"
	"github.com/costa/polypod/internal/ai"
	"github.com/costa/polypod/internal/auth"
	"github.com/costa/polypod/internal/config"
	"github.com/costa/polypod/internal/dotenv"
	"github.com/costa/polypod/internal/humanloop"
	"github.com/costa/polypod/internal/localcontrol"
	"github.com/costa/polypod/internal/models"
	"github.com/costa/polypod/internal/provider"
	"github.com/costa/polypod/internal/session"
	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/wiring"
)

func handleServeAndAttachSubcommands() (handled bool, continueIntoTUI bool) {
	if len(os.Args) < 2 {
		return false, false
	}
	switch os.Args[1] {
	case "serve":
		dotenv.Load(".")
		if err := runServeSubcommand(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "polypod serve: %v\n", err)
			os.Exit(1)
		}
		return true, false
	case "attach":
		dotenv.Load(".")
		bootTUI, err := runAttachSubcommand(os.Args[2:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "polypod attach: %v\n", err)
			os.Exit(1)
		}
		if bootTUI {
			return false, true
		}
		return true, false
	default:
		return false, false
	}
}

func runServeSubcommand(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	localCtrl := fs.Bool("local-control", false, "expõe Web Shell seguro na LAN privada via QR Code ANSI + token revogável")
	listenAddr := fs.String("listen", "", "endereço host:porta (padrão: 127.0.0.1:8789, ou <ip-lan>:8789 com --local-control)")
	port := fs.Int("port", 8789, "porta TCP para o daemon Web Shell")
	inhibitSleep := fs.Bool("inhibit-sleep", true, "inibe suspensão ociosa do sistema durante --local-control")
	configPath := fs.String("config", defaultConfigPath, "caminho do config.yaml")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if dir := filepath.Dir(*configPath); dir != "" && dir != "." {
		dotenv.Load(dir)
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("carregando config: %w", err)
	}
	logger := buildLogger(cfg, "")
	keyStore := auth.NewKeyStore(cfg.Data.Dir)
	seedKeyStore(keyStore, cfg)

	// Hydrate cached models.dev capabilities asynchronously.
	go func() { _, _ = models.RefreshFromModelsDev(context.Background(), cfg.Data.Dir, "") }()

	bindAddr := strings.TrimSpace(*listenAddr)
	if bindAddr == "" {
		host := "127.0.0.1"
		if *localCtrl {
			if lanIP, ipErr := localcontrol.DetectPrivateLANIPv4(); ipErr == nil && lanIP != "" {
				host = lanIP
			}
		}
		bindAddr = net.JoinHostPort(host, strconv.Itoa(*port))
	}

	// Wire core agent runtime for Web Shell execution.
	sys := wiring.WireAll(wiring.Config{
		Logger:     logger,
		WorkingDir: ".",
		Model:      cfg.AI.Model,
		DataDir:    cfg.Data.Dir,
	})
	defer sys.ToolRegistry.ClosePTY()

	cwd, _ := os.Getwd()
	tool.RegisterAdvancedTools(sys.ToolRegistry, ".")
	tool.RegisterProjectChecksTool(sys.ToolRegistry, ".")
	tool.RegisterImpactTool(sys.ToolRegistry, ".")
	_ = sys.Agents.LoadDir(cfg.Data.AgentsDir)

	childQuestions := humanloop.NewMultiChildQuestionQueue()
	tool.RegisterMultiChildQuestionTool(sys.ToolRegistry, childQuestions)

	provider.SetAccountRotator(accountauth.NewManager(nil).RotateActiveOnRateLimit)
	aiClient := ai.NewClient(cfg.AI, sys.ToolAdapter)
	aiClient.SetRepoPlaybook(ai.NewRepoPlaybookStore(cfg.Data.Dir))
	wiring.WireAIClient(sys, aiClient, logger)
	defer sys.Swarm.Shutdown(5 * time.Second)

	var history []ai.ChatMessage
	var histMu = make(chan struct{}, 1)
	histMu <- struct{}{}

	srv, err := localcontrol.NewServer(localcontrol.Config{
		ListenAddr:   bindAddr,
		LocalControl: *localCtrl,
		InhibitSleep: *localCtrl && *inhibitSleep,
		Workspace:    cwd,
		ModelLabel:   fmt.Sprintf("%s/%s", cfg.AI.Provider, cfg.AI.Model),
		Questions:    childQuestions,
		Executor: func(ctx context.Context, prompt string, onDelta func(string)) (string, error) {
			<-histMu
			defer func() { histMu <- struct{}{} }()
			history = append(history, ai.ChatMessage{Role: "user", Content: prompt})
			resp, runErr := aiClient.CompleteWithToolsStream(ctx, history, onDelta, logger)
			if runErr != nil {
				return "", runErr
			}
			history = append(history, ai.ChatMessage{Role: "assistant", Content: resp.Content})
			return resp.Content, nil
		},
	})
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", bindAddr)
	if err != nil {
		return fmt.Errorf("abrindo porta %s: %w", bindAddr, err)
	}
	defer listener.Close()

	actualAddr := listener.Addr().String()
	pairURL := srv.PairingURL(actualAddr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *localCtrl && *inhibitSleep {
		stopInhibit := localcontrol.StartSleepInhibitor(ctx)
		defer stopInhibit()
	}

	fmt.Printf("\nPolypod Web Shell ativo em %s\n", actualAddr)
	fmt.Printf("URL segura (token efêmero no fragmento #): %s\n\n", pairURL)
	if *localCtrl {
		if qr, qrErr := localcontrol.RenderQRANSI(pairURL); qrErr == nil {
			fmt.Println("Escaneie o QR Code na mesma rede Wi-Fi/LAN para controlar a Poly pelo celular:")
			fmt.Println(qr)
		}
	}
	fmt.Println("Pressione Ctrl+C para encerrar (ou clique em 'Revogar Acesso' no Web Shell).")

	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
	}()
	if err := httpSrv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func runAttachSubcommand(args []string) (bootTUI bool, err error) {
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	sessionID := fs.String("session", "", "ID da sessão a retomar no terminal")
	listOnly := fs.Bool("list", false, "lista sessões persistidas disponíveis para attach")
	remoteURL := fs.String("url", "", "opcional: anexa a um servidor remoto polypod remote")
	participant := fs.String("participant", "", "identidade do participante quando usando --url")
	configPath := fs.String("config", defaultConfigPath, "caminho do config.yaml")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return false, nil
		}
		return false, err
	}

	// Support positional `polypod attach <session-id>`
	if *sessionID == "" && fs.NArg() > 0 {
		*sessionID = strings.TrimSpace(fs.Arg(0))
	}

	if strings.TrimSpace(*remoteURL) != "" {
		var remoteArgs []string
		remoteArgs = append(remoteArgs, "--url", *remoteURL)
		if *sessionID != "" {
			remoteArgs = append(remoteArgs, "--session", *sessionID)
		}
		if *participant != "" {
			remoteArgs = append(remoteArgs, "--participant", *participant)
		}
		return false, runRemoteClient(remoteArgs, os.Stdin, os.Stdout, os.Stderr)
	}

	if dir := filepath.Dir(*configPath); dir != "" && dir != "." {
		dotenv.Load(dir)
	}

	if *listOnly {
		cfg, cfgErr := config.Load(*configPath)
		if cfgErr != nil {
			return false, cfgErr
		}
		dataDir := cfg.Data.Dir
		if !filepath.IsAbs(dataDir) {
			if cfgDir := filepath.Dir(*configPath); cfgDir != "" && cfgDir != "." {
				if _, statErr := os.Stat(dataDir); os.IsNotExist(statErr) {
					dataDir = filepath.Join(cfgDir, dataDir)
				}
			}
		}
		sessionsDir := filepath.Join(dataDir, "sessions")
		store := session.NewFileSystemStorage(sessionsDir, slog.Default())
		items, listErr := store.List(session.SessionFilter{MaxCount: 25})
		if listErr != nil {
			return false, listErr
		}
		if len(items) == 0 {
			fmt.Println("Nenhuma sessão salva encontrada.")
			return false, nil
		}
		fmt.Printf("Sessões disponíveis para `polypod attach --session <id>` (%d):\n\n", len(items))
		for _, s := range items {
			title := strings.TrimSpace(s.Title)
			if title == "" {
				title = strings.TrimSpace(s.Preview)
			}
			if len(title) > 64 {
				title = title[:61] + "..."
			}
			fmt.Printf("  %-28s  %3d msgs  %s  %s\n",
				s.ID, s.Messages, s.UpdatedAt.Format("2006-01-02 15:04"), title)
		}
		return false, nil
	}

	if _, cfgErr := loadConfig(*configPath); cfgErr != nil {
		return false, cfgErr
	}

	// Rewrite os.Args so main() boots directly into the interactive TUI attached to the session!
	newArgs := []string{os.Args[0]}
	if *configPath != defaultConfigPath {
		newArgs = append(newArgs, *configPath)
	}
	if strings.TrimSpace(*sessionID) != "" {
		newArgs = append(newArgs, "-r", strings.TrimSpace(*sessionID))
	} else {
		newArgs = append(newArgs, "-c")
	}
	os.Args = newArgs
	return true, nil
}
