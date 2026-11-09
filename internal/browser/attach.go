package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ValidateCDPEndpoint accepts a local HTTP debugging endpoint, not a web page
// or a remote DevTools service. Browser credentials remain in the browser's
// existing profile; Polypod never imports them into its managed profile.
func ValidateCDPEndpoint(raw string) (string, error) {
	u, err := localURL(strings.TrimSpace(raw), "http")
	if err != nil {
		return "", err
	}
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return "", fmt.Errorf("endpoint CDP deve conter apenas endereço local e porta")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("endpoint CDP requer porta válida")
	}
	return "http://" + net.JoinHostPort(u.Hostname(), strconv.Itoa(port)), nil
}

// EndpointFromProfileDir reads the port published by an already running
// Chromium browser that was launched with remote debugging enabled.
func EndpointFromProfileDir(profileDir string) (string, error) {
	if !filepath.IsAbs(profileDir) {
		return "", fmt.Errorf("profile_dir deve ser caminho absoluto")
	}
	return endpointFromPortFile(filepath.Join(profileDir, "DevToolsActivePort"))
}

// ConnectProfileCDP verifies that the local endpoint still belongs to this
// profile. DevToolsActivePort can outlive a browser process, and its port can
// later be reused by a different browser.
func ConnectProfileCDP(ctx context.Context, profileDir string) (CDP, error) {
	endpoint, err := EndpointFromProfileDir(profileDir)
	if err != nil {
		return CDP{}, err
	}
	raw, err := os.ReadFile(filepath.Join(profileDir, "DevToolsActivePort"))
	if err != nil {
		return CDP{}, err
	}
	if len(raw) > 1024 {
		return CDP{}, fmt.Errorf("arquivo DevToolsActivePort inválido")
	}
	lines := strings.SplitN(string(raw), "\n", 3)
	if len(lines) < 2 || !strings.HasPrefix(strings.TrimSpace(lines[1]), "/devtools/browser/") {
		return CDP{}, fmt.Errorf("identidade CDP ausente no perfil")
	}
	filePort, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || filePort < 1 || filePort > 65535 {
		return CDP{}, fmt.Errorf("porta CDP inválida no perfil")
	}
	endpointURL, err := localURL(endpoint, "http")
	if err != nil || endpointURL.Port() != strconv.Itoa(filePort) {
		return CDP{}, fmt.Errorf("porta CDP mudou durante a conexão")
	}
	expectedPath := strings.TrimSpace(lines[1])
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, endpoint+"/json/version", nil)
	if err != nil {
		return CDP{}, err
	}
	httpClient := http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return fmt.Errorf("redirecionamento CDP recusado")
	}}
	resp, err := httpClient.Do(req)
	if err != nil {
		return CDP{}, fmt.Errorf("CDP do perfil indisponível: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return CDP{}, fmt.Errorf("CDP do perfil respondeu HTTP %d", resp.StatusCode)
	}
	var version struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&version); err != nil {
		return CDP{}, fmt.Errorf("identidade CDP inválida: %w", err)
	}
	ws, err := localURL(version.WebSocketDebuggerURL, "ws")
	if err != nil || ws.Path != expectedPath || ws.Port() != endpointURL.Port() {
		return CDP{}, fmt.Errorf("porta CDP não corresponde ao navegador desse perfil")
	}
	return CDP{Endpoint: endpoint}, nil
}

// ConnectLocalCDP probes an existing browser before switching the runtime to it.
// The browser remains owned by its user and is never started or stopped here.
func ConnectLocalCDP(ctx context.Context, raw string) (CDP, []Tab, error) {
	endpoint, err := ValidateCDPEndpoint(raw)
	if err != nil {
		return CDP{}, nil, err
	}
	client := CDP{Endpoint: endpoint}
	tabs, err := client.Tabs(ctx)
	if err != nil {
		return CDP{}, nil, err
	}
	return client, tabs, nil
}
