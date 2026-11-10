package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unicode"

	"github.com/costa/polypod/internal/dotenv"
	"github.com/costa/polypod/internal/remote"
)

// handleRemoteClientSubcommand is called by handleSubcommand before the normal
// agent boots. It never places the shared-session token on the command line.
func handleRemoteClientSubcommand() bool {
	if len(os.Args) < 2 || os.Args[1] != "remote" {
		return false
	}
	dotenv.Load(".")
	if err := runRemoteClient(os.Args[2:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "sessão remota:", err)
		os.Exit(1)
	}
	return true
}

func runRemoteClient(args []string, input io.Reader, output, diagnostic io.Writer) error {
	flags := flag.NewFlagSet("remote", flag.ContinueOnError)
	flags.SetOutput(diagnostic)
	serverURL := flags.String("url", "", "URL do servidor (padrão: loopback local)")
	participant := flags.String("participant", "", "identidade estável deste cliente")
	sessionID := flags.String("session", "", "ID da sessão para entrar")
	stateFile := flags.String("state-file", "", "arquivo privado que guarda o ID da sessão")
	createNew := flags.Bool("new", false, "criar outra sessão e gravar seu ID no arquivo de estado")
	list := flags.Bool("list", false, "listar sessões disponíveis")
	flags.Usage = func() {
		fmt.Fprintln(diagnostic, "Uso: polypod remote [--list | --new | --session ID] [--url URL] --participant NOME")
		fmt.Fprintln(diagnostic, "Token: configure POLYPOD_REMOTE_SESSION_TOKEN no ambiente ou .env privado.")
		fmt.Fprintln(diagnostic, "No chat: digite uma mensagem e pressione Enter; /exit encerra o cliente.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("argumento inesperado; use --help")
	}
	if *createNew && (*sessionID != "" || *list) {
		return fmt.Errorf("--new não pode ser combinado com --session ou --list")
	}
	if *list && *sessionID != "" {
		return fmt.Errorf("--list não pode ser combinado com --session")
	}
	if *serverURL == "" {
		*serverURL = envOr("POLYPOD_REMOTE_SESSION_URL", "http://127.0.0.1:8789")
	}
	if *participant == "" {
		*participant = os.Getenv("POLYPOD_REMOTE_SESSION_PARTICIPANT")
	}
	baseURL, err := validateRemoteClientURL(*serverURL)
	if err != nil {
		return err
	}
	token := os.Getenv("POLYPOD_REMOTE_SESSION_TOKEN")
	if len(token) < 32 || len(token) > 512 {
		return fmt.Errorf("configure POLYPOD_REMOTE_SESSION_TOKEN com 32 a 512 caracteres no ambiente privado")
	}
	if !validRemoteParticipant(*participant) {
		return fmt.Errorf("configure --participant ou POLYPOD_REMOTE_SESSION_PARTICIPANT com 1 a 128 letras, números ou . _ - @ :")
	}
	if *stateFile == "" {
		*stateFile, err = defaultRemoteStateFile(*participant)
		if err != nil {
			return err
		}
	}
	manager := remote.NewSessionManager(remote.SessionConfig{
		ServerURL: baseURL, SessionID: *sessionID, AuthToken: token,
		ParticipantID: *participant, StateFile: *stateFile,
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *list {
		sessions, err := manager.ListSessions(ctx)
		if err != nil {
			return err
		}
		if len(sessions) == 0 {
			_, err = fmt.Fprintln(output, "Nenhuma sessão remota disponível.")
			return err
		}
		for _, session := range sessions {
			if _, err := fmt.Fprintf(output, "%s  %s  participantes: %d\n", session.ID, session.Status, len(session.Participants)); err != nil {
				return err
			}
		}
		return nil
	}
	var outputMu sync.Mutex
	var outputErr error
	manager.OnMessage(func(msg remote.Message) {
		if msg.Sender == *participant && msg.Type == "user" {
			return // The user's own line is already visible in the terminal.
		}
		label := msg.Sender
		if msg.Type == "assistant" {
			label = "Polypod"
		}
		outputMu.Lock()
		if _, err := fmt.Fprintf(output, "\n%s: %s\n> ", safeRemoteDisplay(label), safeRemoteDisplay(msg.Content)); err != nil {
			outputErr = err
			stop()
		}
		outputMu.Unlock()
	})
	if *createNew {
		if _, err := manager.CreateSession(ctx); err != nil {
			return err
		}
	}
	if err := manager.Connect(ctx); err != nil {
		return err
	}
	defer func() {
		if err := manager.Disconnect(); err != nil {
			fmt.Fprintln(diagnostic, "falha ao encerrar sessão remota:", err)
		}
	}()
	outputMu.Lock()
	_, err = fmt.Fprintf(output, "Conectado à sessão %s como %s. /exit para sair.\n> ", manager.SessionID(), *participant)
	outputMu.Unlock()
	if err != nil {
		return err
	}
	type inputLine struct {
		text string
		err  error
		eof  bool
	}
	lines := make(chan inputLine)
	go func() {
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 4096), 256<<10)
		for scanner.Scan() {
			select {
			case lines <- inputLine{text: scanner.Text()}:
			case <-ctx.Done():
				return
			}
		}
		select {
		case lines <- inputLine{err: scanner.Err(), eof: true}:
		case <-ctx.Done():
		}
	}()
	for {
		select {
		case <-ctx.Done():
			outputMu.Lock()
			err := outputErr
			outputMu.Unlock()
			return err
		case item := <-lines:
			if item.eof {
				if item.err != nil {
					return fmt.Errorf("lendo terminal remoto: %w", item.err)
				}
				return nil
			}
			line := strings.TrimSpace(item.text)
			if line == "/exit" || line == "/quit" {
				return nil
			}
			if line != "" {
				if err := manager.SendMessage(ctx, remote.Message{Type: "user", Content: line}); err != nil {
					return err
				}
			}
			outputMu.Lock()
			_, err := fmt.Fprint(output, "> ")
			outputMu.Unlock()
			if err != nil {
				return err
			}
		}
	}
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func validateRemoteClientURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("URL de sessão remota inválida; use apenas esquema e host")
	}
	if parsed.Scheme != "https" {
		ip := net.ParseIP(parsed.Hostname())
		if parsed.Scheme != "http" || (parsed.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return "", fmt.Errorf("URL remota exige HTTPS; para HTTP use loopback ou túnel SSH local")
		}
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func validRemoteParticipant(id string) bool {
	if len(id) < 1 || len(id) > 128 {
		return false
	}
	for _, ch := range id {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') || ch == '_' || ch == '-' || ch == '.' || ch == '@' || ch == ':') {
			return false
		}
	}
	return true
}

func defaultRemoteStateFile(participant string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("diretório de configuração do usuário indisponível: %w", err)
	}
	return filepath.Join(dir, "polypod", "remote-session-"+participant+".txt"), nil
}

func safeRemoteDisplay(value string) string {
	return strings.Map(func(ch rune) rune {
		if ch == '\n' || ch == '\t' || !unicode.IsControl(ch) {
			return ch
		}
		return '�'
	}, value)
}
