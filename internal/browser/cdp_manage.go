package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// NewTab creates a page through Chrome's local DevTools HTTP endpoint.
func (c CDP) NewTab(ctx context.Context) (Tab, error) {
	var tab Tab
	if err := c.command(ctx, http.MethodPut, "/json/new", "about:blank", &tab); err != nil {
		return Tab{}, err
	}
	if tab.ID == "" || tab.Type != "page" {
		return Tab{}, errors.New("Chrome não retornou uma aba válida")
	}
	return tab, nil
}

func (c CDP) ActivateTab(ctx context.Context, tabID string) error {
	if err := c.requireTab(ctx, tabID); err != nil {
		return err
	}
	return c.command(ctx, http.MethodGet, "/json/activate/"+url.PathEscape(tabID), "", nil)
}

func (c CDP) CloseTab(ctx context.Context, tabID string) error {
	if err := c.requireTab(ctx, tabID); err != nil {
		return err
	}
	return c.command(ctx, http.MethodGet, "/json/close/"+url.PathEscape(tabID), "", nil)
}

func (c CDP) requireTab(ctx context.Context, tabID string) error {
	if tabID == "" {
		return errors.New("tab_id obrigatório")
	}
	tabs, err := c.Tabs(ctx)
	if err != nil {
		return err
	}
	for _, tab := range tabs {
		if tab.ID == tabID {
			return nil
		}
	}
	return fmt.Errorf("aba %q não encontrada", tabID)
}

func (c CDP) command(ctx context.Context, method, path, query string, out any) error {
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = "http://127.0.0.1:9222"
	}
	u, err := localURL(endpoint, "http")
	if err != nil {
		return err
	}
	// A paired extension uses an unguessable path prefix on its local CDP
	// bridge. Keep that prefix for management endpoints as well as /json/list.
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = query
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return err
	}
	client := http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("redirecionamento CDP recusado")
	}}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("CDP indisponível: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("CDP HTTP %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

// Navigate starts navigation, then observes its result without retrying the
// side effect if the browser loses the acknowledgement.
func (c CDP) Navigate(ctx context.Context, tabID, rawURL string) (*ActionResult, error) {
	if err := ValidateNavigationURL(rawURL); err != nil {
		return nil, err
	}
	s, tab, err := c.connect(ctx, tabID)
	if err != nil {
		return nil, err
	}
	defer s.close()
	var nav struct {
		ErrorText string `json:"errorText"`
	}
	if err := s.call(ctx, "Page.navigate", map[string]any{"url": rawURL}, &nav); err != nil {
		return &ActionResult{Verification: "Resultado da navegação desconhecido; observe antes de tentar novamente."}, err
	}
	if nav.ErrorText != "" {
		return nil, fmt.Errorf("navegação recusada: %s", nav.ErrorText)
	}
	result := &ActionResult{Executed: true, Verification: "Navegação iniciada; confira a observação posterior."}
	// Chrome may acknowledge Page.navigate before the destination DOM exists.
	// Give it a short opportunity to change location, then report what is known.
	timer := time.NewTimer(350 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return result, nil
	case <-timer.C:
	}
	obs, err := s.observe(ctx, tab.ID)
	if err != nil {
		result.Verification += " Observação pendente: " + err.Error()
		return result, nil
	}
	result.Observation = obs
	result.Verified = obs.URL == rawURL
	if result.Verified {
		result.Verification = "URL confirmada na observação posterior."
	} else {
		result.Verification = "Destino ainda não confirmado ou redirecionado; observe novamente sem repetir a navegação."
	}
	return result, nil
}

// ValidateNavigationURL checks destinations before creating a new tab.
func ValidateNavigationURL(raw string) error {
	if raw == "about:blank" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "file" {
		return errors.New("url deve usar http, https, file ou about:blank")
	}
	if u.Scheme != "file" && u.Hostname() == "" {
		return errors.New("url deve ter domínio")
	}
	if u.Scheme == "file" && !strings.HasPrefix(u.Path, "/") {
		return errors.New("file URL deve conter caminho absoluto")
	}
	return nil
}

// Evaluate runs JavaScript only in the exact observed tab. The caller must
// classify this as a side effect because script can mutate the page.
func (c CDP) Evaluate(ctx context.Context, tabID, expression string) (json.RawMessage, error) {
	if strings.TrimSpace(expression) == "" || len(expression) > 16<<10 {
		return nil, errors.New("javascript obrigatório e limitado a 16 KiB")
	}
	s, _, err := c.connect(ctx, tabID)
	if err != nil {
		return nil, err
	}
	defer s.close()
	var result struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception json.RawMessage `json:"exceptionDetails"`
	}
	if err := s.call(ctx, "Runtime.evaluate", map[string]any{
		"expression": expression, "returnByValue": true, "awaitPromise": true,
	}, &result); err != nil {
		return nil, err
	}
	if len(result.Exception) > 0 {
		return nil, errors.New("javascript falhou na página")
	}
	if len(result.Result.Value) == 0 {
		return json.RawMessage("null"), nil
	}
	if len(result.Result.Value) > 64<<10 {
		return nil, errors.New("resultado javascript excede 64 KiB")
	}
	return result.Result.Value, nil
}
