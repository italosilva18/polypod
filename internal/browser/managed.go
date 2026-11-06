package browser

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ManagedConfig describes a Chrome process owned by Polypod. Its profile is
// separate from the user's normal browser and survives Polypod restarts.
type ManagedConfig struct {
	Binary           string
	ProfileDir       string
	Headless         bool
	FallbackHeadless bool
	DesktopUser      string
	DesktopHome      string
}

type ManagedStatus struct {
	Running  bool   `json:"running"`
	Mode     string `json:"mode"`
	Headless bool   `json:"headless"`
}

// ManagedBrowser owns one Chrome process. The process lifetime is deliberately
// independent of a tool-call context; Stop or the service supervisor ends it.
type ManagedBrowser struct {
	mu       sync.Mutex
	config   ManagedConfig
	cmd      *exec.Cmd
	done     chan error
	endpoint string
	headless bool
}

func NewManagedBrowser(config ManagedConfig) *ManagedBrowser {
	return &ManagedBrowser{config: config}
}

func (m *ManagedBrowser) Status() ManagedStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reapLocked()
	return ManagedStatus{Running: m.cmd != nil, Mode: "isolated_profile", Headless: m.headless}
}

// Start launches Chrome only when needed and returns its loopback CDP client.
func (m *ManagedBrowser) Start(ctx context.Context) (CDP, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reapLocked()
	if m.cmd != nil {
		return CDP{Endpoint: m.endpoint}, nil
	}
	if err := ctx.Err(); err != nil {
		return CDP{}, err
	}
	binary, err := m.binary()
	if err != nil {
		return CDP{}, err
	}
	identity, err := resolveLaunchIdentity(m.config)
	if err != nil {
		return CDP{}, err
	}
	profile, err := m.profileDir(identity)
	if err != nil {
		return CDP{}, err
	}
	modes := []bool{m.config.Headless}
	if !m.config.Headless && m.config.FallbackHeadless {
		modes = append(modes, true)
	}
	var startErr error
	for _, headless := range modes {
		client, err := m.launchLocked(ctx, binary, profile, headless, identity)
		if err == nil {
			return client, nil
		}
		if ctx.Err() != nil {
			return CDP{}, ctx.Err()
		}
		startErr = err
	}
	return CDP{}, startErr
}

func (m *ManagedBrowser) launchLocked(ctx context.Context, binary, profile string, headless bool, identity *launchIdentity) (CDP, error) {
	portFile := filepath.Join(profile, "DevToolsActivePort")
	if endpoint, err := endpointFromPortFile(portFile); err == nil && endpointAlive(ctx, endpoint) {
		return CDP{}, errors.New("o perfil Polypod já está aberto por outro processo")
	}
	if err := os.Remove(portFile); err != nil && !os.IsNotExist(err) {
		return CDP{}, fmt.Errorf("removendo porta CDP antiga: %w", err)
	}
	args := []string{
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=0",
		"--remote-allow-origins=http://localhost",
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-background-networking",
	}
	if headless {
		args = append(args, "--headless=new")
	}
	// Chromium will not start as root without this flag. The separate profile
	// still prevents accidental reuse of the user's ordinary Chrome session.
	if os.Geteuid() == 0 && identity == nil {
		args = append(args, "--no-sandbox")
	}
	args = append(args, "about:blank")
	cmd := exec.Command(binary, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	applyLaunchIdentity(cmd, identity)
	if err := cmd.Start(); err != nil {
		return CDP{}, fmt.Errorf("iniciando Chrome: %w", err)
	}
	m.cmd = cmd
	m.headless = headless
	done := make(chan error, 1)
	m.done = done
	go func() { done <- cmd.Wait() }()

	wait := 15 * time.Second
	if !headless && m.config.FallbackHeadless {
		wait = 6 * time.Second
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if endpoint, err := endpointFromPortFile(portFile); err == nil && endpointAlive(ctx, endpoint) {
			m.endpoint = endpoint
			return CDP{Endpoint: endpoint}, nil
		}
		select {
		case err := <-m.done:
			m.cmd, m.done, m.headless = nil, nil, false
			if err == nil {
				return CDP{}, errors.New("Chrome encerrou antes de abrir CDP")
			}
			return CDP{}, fmt.Errorf("Chrome encerrou antes de abrir CDP: %w", err)
		case <-ctx.Done():
			return CDP{}, errors.Join(ctx.Err(), m.stopLocked(context.Background()))
		case <-deadline.C:
			return CDP{}, errors.Join(fmt.Errorf("Chrome não abriu a porta CDP em %s", wait), m.stopLocked(context.Background()))
		case <-tick.C:
		}
	}
}

func (m *ManagedBrowser) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopLocked(ctx)
}

func (m *ManagedBrowser) stopLocked(ctx context.Context) error {
	m.reapLocked()
	if m.cmd == nil {
		return nil
	}
	cmd, done := m.cmd, m.done
	m.cmd, m.done, m.endpoint, m.headless = nil, nil, "", false
	if err := terminateBrowserProcess(cmd); err != nil {
		if killErr := killBrowserProcess(cmd); killErr != nil {
			return fmt.Errorf("encerrando Chrome: %w", killErr)
		}
	}
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		if err := killBrowserProcess(cmd); err != nil {
			return fmt.Errorf("encerrando Chrome: %w", err)
		}
		return ctx.Err()
	case <-timer.C:
		if err := killBrowserProcess(cmd); err != nil {
			return fmt.Errorf("forçando fim do Chrome: %w", err)
		}
		<-done
		return nil
	}
}

func (m *ManagedBrowser) reapLocked() {
	if m.done == nil {
		return
	}
	select {
	case <-m.done:
		m.cmd, m.done, m.endpoint, m.headless = nil, nil, "", false
	default:
	}
}

func (m *ManagedBrowser) binary() (string, error) {
	if m.config.Binary != "" {
		return exec.LookPath(m.config.Binary)
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "brave-browser", "microsoft-edge"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", errors.New("Chrome/Chromium não encontrado; configure POLYPOD_BROWSER_BINARY")
}

func (m *ManagedBrowser) profileDir(identity *launchIdentity) (string, error) {
	profile := m.config.ProfileDir
	if profile == "" {
		if identity != nil {
			profile = filepath.Join(identity.home, ".polypod-browser-profile")
		} else {
			cache, err := os.UserCacheDir()
			if err != nil {
				return "", err
			}
			profile = filepath.Join(cache, "polypod", "browser-profile")
		}
	}
	abs, err := filepath.Abs(profile)
	if err != nil {
		return "", err
	}
	if identity != nil {
		if err := createDesktopProfile(abs, identity); err != nil {
			return "", fmt.Errorf("criando perfil do navegador com usuário do desktop: %w", err)
		}
	} else if err := os.MkdirAll(abs, 0700); err != nil {
		return "", fmt.Errorf("criando perfil do navegador: %w", err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("perfil do navegador deve ser diretório privado, modo 0700")
	}
	if identity != nil && !profileOwnedBy(info, identity) {
		return "", errors.New("perfil do navegador pertence a outro usuário")
	}
	return abs, nil
}

func endpointFromPortFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(raw) > 1024 {
		return "", errors.New("porta CDP inválida")
	}
	line, _, _ := strings.Cut(string(raw), "\n")
	port, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || port < 1 || port > 65535 {
		return "", errors.New("porta CDP inválida")
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port), nil
}

func endpointAlive(ctx context.Context, endpoint string) bool {
	checkCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, endpoint+"/json/version", nil)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("redirecionamento CDP recusado")
	}}).Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
