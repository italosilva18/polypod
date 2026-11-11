package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/costa/polypod/internal/chatgptauth"
	"github.com/costa/polypod/internal/computeruse"
	"github.com/costa/polypod/internal/config"
)

// Operational probes never send a Telegram message, consume updates, inject
// desktop input, or print credentials. Live mode verifies remote connectivity
// and captures a screen in memory; callers explicitly opt into these checks.
type operationalProbes struct {
	credential  func() (*chatgptauth.Credential, error)
	models      func(context.Context) ([]chatgptauth.Model, error)
	capture     func(context.Context) ([]byte, error)
	command     func(context.Context, string, ...string) ([]byte, error)
	http        *http.Client
	telegramURL string
	now         func() time.Time
}

func defaultProbes() operationalProbes {
	manager := chatgptauth.NewManager(chatgptauth.DefaultCredentialPath())
	return operationalProbes{credential: manager.Status, models: manager.Models, capture: computeruse.CaptureScreen,
		command: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.WaitDelay = time.Second
			return cmd.Output()
		},
		http: &http.Client{Timeout: 8 * time.Second}, telegramURL: "https://api.telegram.org", now: time.Now}
}

// RunDiagnosticsContext includes operational checks. Live mode requires network
// and desktop access; all errors are summarized without upstream secret text.
func RunDiagnosticsContext(ctx context.Context, cfg *config.Config, live bool) []Check {
	if ctx == nil {
		ctx = context.Background()
	}
	checks := runBasicDiagnostics(cfg)
	if !live {
		return append(checks, operationalChecks(ctx, cfg, false, defaultProbes())...)
	}
	// A third-party OAuth or desktop callback may ignore context cancellation.
	// The one-shot doctor must still return a bounded, sanitized report.
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
	}
	return runLiveDiagnosticsBounded(ctx, checks, func() []Check {
		return operationalChecks(ctx, cfg, true, defaultProbes())
	})
}

func runLiveDiagnosticsBounded(ctx context.Context, basic []Check, run func() []Check) []Check {
	result := make(chan []Check, 1)
	go func() { result <- run() }()
	select {
	case checks := <-result:
		return append(basic, checks...)
	case <-ctx.Done():
		return append(basic, Check{Name: "Diagnóstico ao vivo", Status: "warn",
			Message: "uma sonda excedeu o prazo; confira OAuth, desktop e rede separadamente"})
	}
}

func operationalChecks(ctx context.Context, cfg *config.Config, live bool, p operationalProbes) []Check {
	checks := []Check{}
	if strings.EqualFold(cfg.AI.Provider, "chatgpt") {
		checks = append(checks, checkOAuth(ctx, cfg.AI.Model, live, p))
	}
	if live && cfg.Remote.Enabled && cfg.Remote.Validate() == nil {
		checks = append(checks, checkTelegramLive(ctx, cfg.Remote, p))
	}
	checks = append(checks, checkHeartbeat(cfg, p.now()), checkScheduledHeartbeat(cfg), checkBoot(ctx, p))
	checks = append(checks, checkDesktop(ctx, live, p)...)
	return checks
}

func checkOAuth(ctx context.Context, model string, live bool, p operationalProbes) Check {
	c := Check{Name: "ChatGPT OAuth", Status: "fail"}
	credential, err := p.credential()
	if err != nil || credential == nil || credential.AccessToken == "" {
		c.Message = "sessao indisponivel; execute polypod auth chatgpt login"
		return c
	}
	if !live {
		c.Status = "warn"
		c.Message = "sessao armazenada; execute polypod doctor --live para validar acesso e modelo"
		if credential.NeedsRefresh() {
			c.Message = "sessao precisa de renovacao; execute polypod doctor --live"
		}
		return c
	}
	probeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	models, err := p.models(probeCtx)
	if err != nil {
		c.Message = "falha ao validar OAuth/modelos; verifique a rede e execute polypod auth chatgpt login"
		return c
	}
	for _, available := range models {
		if available.Name() == model {
			c.Status = "ok"
			c.Message = "OAuth aceito e modelo selecionado acessivel; nenhuma geracao enviada"
			return c
		}
	}
	c.Status = "warn"
	c.Message = "OAuth aceito, mas modelo selecionado nao listado; consulte polypod auth chatgpt models"
	return c
}

func checkTelegramLive(ctx context.Context, cfg config.RemoteConfig, p operationalProbes) Check {
	c := Check{Name: "Telegram conectividade", Status: "fail"}
	for _, method := range []string{"getMe", "getChat"} {
		endpoint := p.telegramURL + "/bot" + cfg.BotToken + "/" + method
		if method == "getChat" {
			endpoint += "?" + url.Values{"chat_id": {cfg.ChatID}}.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			c.Message = "endpoint invalido"
			return c
		}
		resp, err := p.http.Do(req)
		if err != nil {
			c.Message = "sem conexao com Telegram; verifique rede e token no ambiente"
			return c
		}
		var result struct {
			OK bool `json:"ok"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || decodeErr != nil || !result.OK {
			c.Message = "Telegram recusou acesso; confira bot_token/chat_id e inicie uma conversa com o bot"
			return c
		}
	}
	c.Status = "ok"
	c.Message = "bot e chat acessiveis; envie uma mensagem ao bot para confirmar inbox e resposta"
	return c
}

func checkHeartbeat(cfg *config.Config, now time.Time) Check {
	c := Check{Name: "Heartbeat", Status: "warn"}
	raw, err := os.ReadFile(filepath.Join(cfg.Data.Dir, "humanloop.json"))
	if err != nil {
		c.Message = "journal indisponivel; inicie Polypod para criar o heartbeat"
		if os.IsPermission(err) {
			c.Message = "sem permissao para ler journal; execute doctor como o usuario do servico"
		}
		return c
	}
	var journal struct {
		State struct {
			LastHeartbeat time.Time `json:"last_heartbeat"`
			LastError     string    `json:"last_error"`
		} `json:"state"`
		Messages []struct {
			Status string `json:"status"`
		} `json:"messages"`
	}
	if json.Unmarshal(raw, &journal) != nil {
		c.Status = "fail"
		c.Message = "journal ilegivel; preserve o arquivo e restaure um backup valido"
		return c
	}
	if journal.State.LastHeartbeat.IsZero() || now.Sub(journal.State.LastHeartbeat) > 2*time.Minute {
		c.Message = "sem heartbeat recente; verifique se o processo esta ativo"
		return c
	}
	pending, failed := 0, 0
	for _, msg := range journal.Messages {
		if msg.Status == "pending" || msg.Status == "processing" {
			pending++
		}
		if msg.Status == "failed" {
			failed++
		}
	}
	c.Status = "ok"
	c.Message = fmt.Sprintf("ativo; %d itens em andamento, %d falhas terminais", pending, failed)
	if journal.State.LastError != "" || failed > 0 {
		c.Status = "warn"
		c.Message += "; consulte inbox_list para detalhes"
	}
	return c
}

func checkBoot(ctx context.Context, p operationalProbes) Check {
	c := Check{Name: "Servico de boot", Status: "warn", Message: "servico nao detectado; configure a unidade para a sessao desktop e execute systemctl status"}
	units := []string{"polypod-root.service", "polypod.service"}
	if configured := os.Getenv("POLYPOD_SERVICE_NAME"); configured != "" {
		units = []string{configured}
	}
	for _, unit := range units {
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		raw, err := p.command(probeCtx, "systemctl", "show", unit, "--property=LoadState,ActiveState,UnitFileState", "--no-pager")
		cancel()
		if err != nil || !strings.Contains(string(raw), "LoadState=loaded") {
			continue
		}
		properties := make(map[string]string)
		for _, line := range strings.Split(string(raw), "\n") {
			if key, value, ok := strings.Cut(line, "="); ok {
				properties[key] = value
			}
		}
		active := properties["ActiveState"] == "active"
		enabled := properties["UnitFileState"] == "enabled"
		if active && enabled {
			c.Status = "ok"
			c.Message = "servico ativo e habilitado no boot"
		} else {
			c.Message = "servico instalado; confira systemctl is-active e is-enabled antes de habilitar"
		}
		return c
	}
	return c
}

func checkDesktop(ctx context.Context, live bool, p operationalProbes) []Check {
	capture := Check{Name: "Desktop captura", Status: "warn", Message: "execute polypod doctor --live dentro da sessao desktop"}
	input := Check{Name: "Desktop entrada", Status: "warn", Message: "entrada nao verificada; necessario acesso a sessao grafica"}
	// A direct sudo invocation strips the desktop variables from the shell.
	// The service itself has a delegated Wayland session, but this doctor
	// process cannot observe it unless launched with the same environment.
	if live && os.Geteuid() == 0 && os.Getenv("POLYPOD_DESKTOP_USER") == "" && serviceHasDelegatedDesktop(ctx, p) {
		capture.Message = "diagnostico fora do ambiente grafico do servico; execute python3 scripts/doctor-live-service.py"
		input.Message = capture.Message
		return []Check{capture, input}
	}
	display := computeruse.DetectDisplayServer()
	if display == computeruse.DisplayUnknown {
		capture.Message = "sessao grafica indisponivel; configure DISPLAY ou WAYLAND_DISPLAY no servico"
		return []Check{capture, input}
	}
	if !live {
		return []Check{capture, input}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	data, err := p.capture(probeCtx)
	if err == nil && len(data) > 0 {
		capture.Status = "ok"
		capture.Message = "captura realizada e descartada; nenhuma imagem salva no relatorio"
	} else {
		capture.Status = "fail"
		capture.Message = "captura falhou; confira permissao do portal e usuario da sessao desktop"
	}
	if display == computeruse.DisplayX11 {
		if _, err := p.command(probeCtx, "xdotool", "getmouselocation", "--shell"); err == nil {
			input.Status = "ok"
			input.Message = "consulta ao cursor X11 aceita; nenhum evento de entrada enviado"
		} else {
			input.Message = "xdotool/sessao X11 indisponivel; confira DISPLAY e autorizacao X11"
		}
	} else if display == computeruse.DisplayWayland {
		socket := os.Getenv("YDOTOOL_SOCKET")
		if socket == "" {
			socket = "/tmp/.ydotool_socket"
			if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
				socket = filepath.Join(runtimeDir, ".ydotool_socket")
			}
		}
		if _, err := exec.LookPath("ydotool"); err != nil {
			input.Message = "ydotool ausente; instale ydotool >= 1.0 e configure ydotoold"
			return []Check{capture, input}
		}
		connection, err := net.DialTimeout("unixgram", socket, time.Second)
		if err == nil {
			connection.Close()
			input.Status = "ok"
			input.Message = "socket ydotool acessivel; nenhum evento enviado"
		} else {
			input.Message = "socket ydotool indisponivel; confira ydotoold, YDOTOOL_SOCKET e permissoes"
		}
	}
	return []Check{capture, input}
}

func serviceHasDelegatedDesktop(ctx context.Context, p operationalProbes) bool {
	if p.command == nil {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	raw, err := p.command(probeCtx, "systemctl", "show", "polypod-root.service", "--property=Environment", "--value", "--no-pager")
	if err != nil {
		return false
	}
	user, display := false, false
	for _, field := range strings.Fields(string(raw)) {
		if strings.HasPrefix(field, "POLYPOD_DESKTOP_USER=") && field != "POLYPOD_DESKTOP_USER=" {
			user = true
		}
		if strings.HasPrefix(field, "WAYLAND_DISPLAY=") && field != "WAYLAND_DISPLAY=" {
			display = true
		}
	}
	return user && display
}

func checkScheduledHeartbeat(cfg *config.Config) Check {
	c := Check{Name: "Heartbeat agendado", Status: "warn"}
	if !cfg.Scheduler.Enabled || !cfg.Scheduler.HeartbeatEnabled {
		c.Message = "desativado; habilite scheduler.enabled e scheduler.heartbeat_enabled para revisao periodica"
		return c
	}
	interval, err := time.ParseDuration(cfg.Scheduler.HeartbeatInterval)
	if err != nil || interval <= 0 {
		c.Status = "fail"
		c.Message = "scheduler.heartbeat_interval invalido; use uma duracao positiva como 1h"
		return c
	}
	c.Status = "ok"
	c.Message = "habilitado a cada " + interval.String() + "; consulte historico de execucoes para resultados"
	return c
}
