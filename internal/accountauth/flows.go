package accountauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/chatgptauth"
)

const (
	openRouterAuthEndpoint = "https://openrouter.ai/auth"
	openRouterKeysEndpoint = "https://openrouter.ai/api/v1/auth/keys"
	openRouterAPIBaseURL   = "https://openrouter.ai/api/v1"

	googleAuthEndpoint     = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenEndpoint    = "https://oauth2.googleapis.com/token"
	googleUserInfoEndpoint = "https://openidconnect.googleapis.com/v1/userinfo"
	googleGeminiBaseURL    = "https://generativelanguage.googleapis.com/v1beta"
	googleGeminiScopes     = "https://www.googleapis.com/auth/generative-language openid email"

	githubDeviceEndpoint = "https://github.com/login/device/code"
	githubTokenEndpoint  = "https://github.com/login/oauth/access_token"
	githubUserEndpoint   = "https://api.github.com/user"

	qwenDashScopeBaseURL = "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"
)

// LoginOptions customizes an interactive or device-code OAuth login.
type LoginOptions struct {
	AccountID    string
	ClientID     string
	ClientSecret string
	ClientIDFile string
	IssuerURL    string
	AuthURL      string
	TokenURL     string
	DeviceURL    string
	BaseURL      string
	UseADC       bool
	UseGH        bool
	OnNotice     func(string)
}

// Manager coordinates OAuth login, multi-account selection, token refresh, and model listing.
type Manager struct {
	store       *Store
	httpClient  *http.Client
	openBrowser func(string) error
	mu          sync.Mutex

	// Overridable endpoints for deterministic unit testing.
	OpenRouterAuthURL string
	OpenRouterKeysURL string
	GoogleAuthURL     string
	GoogleTokenURL    string
	GoogleUserInfoURL string
	GitHubDeviceURL   string
	GitHubTokenURL    string
	GitHubUserURL     string
}

func NewManager(store *Store) *Manager {
	if store == nil {
		store = NewStore("")
	}
	return &Manager{
		store:             store,
		httpClient:        &http.Client{Timeout: 30 * time.Second},
		openBrowser:       defaultOpenBrowser,
		OpenRouterAuthURL: openRouterAuthEndpoint,
		OpenRouterKeysURL: openRouterKeysEndpoint,
		GoogleAuthURL:     googleAuthEndpoint,
		GoogleTokenURL:    googleTokenEndpoint,
		GoogleUserInfoURL: googleUserInfoEndpoint,
		GitHubDeviceURL:   githubDeviceEndpoint,
		GitHubTokenURL:    githubTokenEndpoint,
		GitHubUserURL:     githubUserEndpoint,
	}
}

func (m *Manager) Store() *Store { return m.store }

func (m *Manager) SetHTTPClient(c *http.Client) {
	if c != nil {
		m.httpClient = c
	}
}

func (m *Manager) SetBrowserOpener(fn func(string) error) {
	if fn != nil {
		m.openBrowser = fn
	}
}

func (m *Manager) notify(opts LoginOptions, msg string) {
	if opts.OnNotice != nil && strings.TrimSpace(msg) != "" {
		opts.OnNotice(msg)
	}
}

// RotateActiveOnRateLimit switches the active OAuth account for providerName to
// the next saved account in round-robin order when an HTTP 429 quota limit is
// hit. Returns (nextAccountID, true) when a second account was available and
// activated, or ("", false) when 0 or 1 account is saved.
func (m *Manager) RotateActiveOnRateLimit(providerName string) (string, bool) {
	if m == nil || m.store == nil {
		return "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	p := strings.ToLower(strings.TrimSpace(providerName))
	accounts, err := m.store.ListAccounts(p)
	if err != nil || len(accounts) < 2 {
		return "", false
	}
	activeIdx := 0
	for i, acc := range accounts {
		if acc.Active {
			activeIdx = i
			break
		}
	}
	next := accounts[(activeIdx+1)%len(accounts)]
	if err := m.store.SetActiveAccount(p, next.ID); err != nil {
		return "", false
	}
	return next.ID, true
}

// Login authenticates an account on one of the 6 supported OAuth providers.
func (m *Manager) Login(ctx context.Context, provider string, opts LoginOptions) (*Credential, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	switch provider {
	case "chatgpt":
		cgMgr := chatgptauth.NewManager(m.store.chatGPTPath)
		cg, err := cgMgr.LoginAccount(ctx, opts.AccountID)
		if err != nil {
			return nil, err
		}
		return &Credential{
			Provider:     "chatgpt",
			AccountID:    cg.AccountID,
			Email:        cg.Email,
			AccessToken:  cg.AccessToken,
			RefreshToken: cg.RefreshToken,
			IDToken:      cg.IDToken,
			ClientID:     cg.ClientID,
			Issuer:       cg.Issuer,
			BaseURL:      "https://api.openai.com/v1",
			Scopes:       cg.Scopes,
			ExpiresAt:    cg.ExpiresAt,
			SavedAt:      cg.SavedAt,
		}, nil
	case "openrouter":
		return m.loginOpenRouterPKCE(ctx, opts)
	case "google":
		return m.loginGoogle(ctx, opts)
	case "github":
		return m.loginGitHub(ctx, opts)
	case "qwen":
		return m.loginQwen(ctx, opts)
	case "anthropic":
		return m.loginAnthropic(ctx, opts)
	default:
		return nil, fmt.Errorf("provedor OAuth desconhecido %q (suportados: %s)", provider, strings.Join(SupportedProviders(), ", "))
	}
}

// TokenSource returns a provider.TokenSource closure that automatically refreshes OAuth tokens.
func (m *Manager) TokenSource(provider string) func(context.Context) (string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "chatgpt" {
		cgMgr := chatgptauth.NewManager(m.store.chatGPTPath)
		return cgMgr.AccessToken
	}
	return func(ctx context.Context) (string, error) {
		return m.AccessToken(ctx, provider)
	}
}

// HasActiveAccount reports whether provider currently has at least one saved OAuth account.
func (m *Manager) HasActiveAccount(provider string) bool {
	cred, err := m.store.LoadActive(provider)
	return err == nil && cred != nil && cred.AccessToken != ""
}

// ActiveBaseURL returns the custom/discovered BaseURL stored on the active account, if any.
func (m *Manager) ActiveBaseURL(provider string) string {
	cred, err := m.store.LoadActive(provider)
	if err != nil || cred == nil {
		return ""
	}
	return strings.TrimSpace(cred.BaseURL)
}

// AccessToken returns a valid OAuth token for provider's active account, refreshing if expired.
func (m *Manager) AccessToken(ctx context.Context, provider string) (string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "chatgpt" {
		return chatgptauth.NewManager(m.store.chatGPTPath).AccessToken(ctx)
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	cred, err := m.store.LoadActive(provider)
	if err != nil {
		return "", err
	}
	if cred == nil || cred.AccessToken == "" {
		return "", fmt.Errorf("%s nao conectado via OAuth; execute: polypod auth %s login", provider, provider)
	}
	if cred.NeedsRefresh() && cred.RefreshToken != "" && cred.TokenURL != "" {
		refreshed, refreshErr := m.refreshOAuthToken(ctx, cred)
		if refreshErr != nil {
			return "", refreshErr
		}
		cred = refreshed
	}
	return cred.AccessToken, nil
}

func (m *Manager) refreshOAuthToken(ctx context.Context, cred *Credential) (*Credential, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {cred.RefreshToken},
	}
	if cred.ClientID != "" {
		form.Set("client_id", cred.ClientID)
	}
	if cred.ClientSecret != "" {
		form.Set("client_secret", cred.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cred.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("renovando token OAuth %s: %w", cred.Provider, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
		Error        string `json:"error"`
		Description  string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("resposta OAuth invalida em %s (HTTP %d)", cred.Provider, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || tr.Error != "" || tr.AccessToken == "" {
		return nil, fmt.Errorf("falha renovando OAuth %s (HTTP %d): %s %s", cred.Provider, resp.StatusCode, tr.Error, tr.Description)
	}
	cred.AccessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		cred.RefreshToken = tr.RefreshToken
	}
	if tr.IDToken != "" {
		cred.IDToken = tr.IDToken
	}
	if tr.TokenType != "" {
		cred.TokenType = tr.TokenType
	}
	if tr.Scope != "" {
		cred.Scopes = strings.Fields(tr.Scope)
	}
	if tr.ExpiresIn > 0 {
		cred.ExpiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second).UTC()
	}
	if err := m.store.SaveAccount(cred.Provider, cred.AccountID, cred); err != nil {
		return nil, err
	}
	return cred, nil
}

// ─── 1. OpenRouter OAuth PKCE ───────────────────────────────────────────────

func (m *Manager) loginOpenRouterPKCE(ctx context.Context, opts LoginOptions) (*Credential, error) {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return nil, err
	}
	cb, err := startLoopbackCallbackServer(ctx, "OpenRouter")
	if err != nil {
		return nil, err
	}
	defer cb.Close()

	authEndpoint := m.OpenRouterAuthURL
	if opts.AuthURL != "" {
		authEndpoint = opts.AuthURL
	}
	params := url.Values{
		"callback_url":          {cb.RedirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	targetURL := authEndpoint + "?" + params.Encode()
	m.notify(opts, "Abrindo navegador para autenticar conta OpenRouter (PKCE): "+targetURL)
	if err := m.openBrowser(targetURL); err != nil {
		m.notify(opts, "Abra manualmente no navegador: "+targetURL)
	}

	res, err := cb.Wait(ctx)
	if err != nil {
		return nil, err
	}

	keysURL := m.OpenRouterKeysURL
	if opts.TokenURL != "" {
		keysURL = opts.TokenURL
	}
	payload, _ := json.Marshal(map[string]string{
		"code":                  res.Code,
		"code_verifier":         verifier,
		"code_challenge_method": "S256",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, keysURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trocando codigo PKCE OpenRouter: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Key    string `json:"key"`
		UserID string `json:"user_id"`
		Error  any    `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("resposta OpenRouter invalida (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || strings.TrimSpace(parsed.Key) == "" {
		return nil, fmt.Errorf("OpenRouter recusou troca PKCE (HTTP %d)", resp.StatusCode)
	}

	baseURL := openRouterAPIBaseURL
	if opts.BaseURL != "" {
		baseURL = opts.BaseURL
	}
	accountID := strings.TrimSpace(opts.AccountID)
	if accountID == "" {
		if parsed.UserID != "" {
			accountID = parsed.UserID
		} else {
			accountID = "openrouter-default"
		}
	}
	cred := &Credential{
		Provider:    "openrouter",
		AccountID:   accountID,
		DisplayName: parsed.UserID,
		AccessToken: strings.TrimSpace(parsed.Key),
		TokenType:   "Bearer",
		BaseURL:     baseURL,
	}
	if err := m.store.SaveAccount("openrouter", accountID, cred); err != nil {
		return nil, err
	}
	return cred, nil
}

// ─── 2. Google Gemini OAuth 2.0 PKCE + ADC ──────────────────────────────────

func loadGoogleClientJSON(path string) (clientID, clientSecret, authURI, tokenURI string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", "", err
	}
	var doc struct {
		Installed *struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
			AuthURI      string `json:"auth_uri"`
			TokenURI     string `json:"token_uri"`
		} `json:"installed"`
		Web *struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
			AuthURI      string `json:"auth_uri"`
			TokenURI     string `json:"token_uri"`
		} `json:"web"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", "", "", "", err
	}
	if doc.Installed != nil && doc.Installed.ClientID != "" {
		return doc.Installed.ClientID, doc.Installed.ClientSecret, doc.Installed.AuthURI, doc.Installed.TokenURI, nil
	}
	if doc.Web != nil && doc.Web.ClientID != "" {
		return doc.Web.ClientID, doc.Web.ClientSecret, firstNonEmpty(doc.Web.AuthURI, googleAuthEndpoint), doc.Web.TokenURI, nil
	}
	return "", "", "", "", fmt.Errorf("client_id nao encontrado em %s", path)
}

func defaultGoogleADCPath() string {
	if p := strings.TrimSpace(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "gcloud", "application_default_credentials.json")
}

func (m *Manager) loginGoogle(ctx context.Context, opts LoginOptions) (*Credential, error) {
	clientID := firstNonEmpty(opts.ClientID, os.Getenv("POLYPOD_GOOGLE_OAUTH_CLIENT_ID"))
	clientSecret := firstNonEmpty(opts.ClientSecret, os.Getenv("POLYPOD_GOOGLE_OAUTH_CLIENT_SECRET"))
	clientFile := firstNonEmpty(opts.ClientIDFile, os.Getenv("POLYPOD_GOOGLE_OAUTH_CLIENT_FILE"))
	authEndpoint := firstNonEmpty(opts.AuthURL, m.GoogleAuthURL)
	tokenEndpoint := firstNonEmpty(opts.TokenURL, m.GoogleTokenURL)

	if clientFile != "" {
		cid, csec, aURL, tURL, err := loadGoogleClientJSON(clientFile)
		if err != nil {
			return nil, fmt.Errorf("lendo client JSON Google: %w", err)
		}
		clientID = firstNonEmpty(clientID, cid)
		clientSecret = firstNonEmpty(clientSecret, csec)
		authEndpoint = firstNonEmpty(opts.AuthURL, aURL, authEndpoint)
		tokenEndpoint = firstNonEmpty(opts.TokenURL, tURL, tokenEndpoint)
	}

	// Support importing Google Application Default Credentials (gcloud auth application-default login)
	// when requested via --adc or when no desktop OAuth client_id was supplied.
	if opts.UseADC || clientID == "" {
		adcPath := defaultGoogleADCPath()
		if adcPath != "" {
			if cred, err := m.importGoogleADC(ctx, adcPath, tokenEndpoint, opts); err == nil && cred != nil {
				return cred, nil
			} else if opts.UseADC {
				return nil, fmt.Errorf("importando Google ADC (%s): %w", adcPath, err)
			}
		}
		if clientID == "" {
			return nil, fmt.Errorf("Google Gemini OAuth requer um Client ID Desktop (defina POLYPOD_GOOGLE_OAUTH_CLIENT_ID / --client-id / --client-id-file) ou credenciais locais do gcloud (`gcloud auth application-default login` + `--adc`)")
		}
	}

	return m.runStandardAuthCodePKCE(ctx, "google", pkceProviderSpec{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		AuthURL:      authEndpoint,
		TokenURL:     tokenEndpoint,
		Scopes:       googleGeminiScopes,
		BaseURL:      firstNonEmpty(opts.BaseURL, googleGeminiBaseURL),
		ExtraAuthParams: map[string]string{
			"access_type": "offline",
			"prompt":      "consent",
		},
		FetchProfile: func(pCtx context.Context, accessToken string) (email, display string) {
			return m.fetchGoogleEmail(pCtx, accessToken)
		},
	}, opts)
}

func (m *Manager) importGoogleADC(ctx context.Context, path, tokenEndpoint string, opts LoginOptions) (*Credential, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var adc struct {
		Type         string `json:"type"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(data, &adc); err != nil {
		return nil, err
	}
	if adc.ClientID == "" || adc.RefreshToken == "" {
		return nil, fmt.Errorf("arquivo ADC sem client_id/refresh_token de usuario")
	}
	seed := &Credential{
		Provider:     "google",
		AccountID:    firstNonEmpty(opts.AccountID, "gcloud-adc"),
		RefreshToken: adc.RefreshToken,
		ClientID:     adc.ClientID,
		ClientSecret: adc.ClientSecret,
		TokenURL:     tokenEndpoint,
		BaseURL:      firstNonEmpty(opts.BaseURL, googleGeminiBaseURL),
	}
	refreshed, err := m.refreshOAuthToken(ctx, seed)
	if err != nil {
		return nil, err
	}
	if email, name := m.fetchGoogleEmail(ctx, refreshed.AccessToken); email != "" || name != "" {
		refreshed.Email = email
		refreshed.DisplayName = name
		if opts.AccountID == "" && email != "" && refreshed.AccountID == "gcloud-adc" {
			_ = m.store.DeleteAccount("google", "gcloud-adc")
			refreshed.AccountID = email
		}
		_ = m.store.SaveAccount("google", refreshed.AccountID, refreshed)
	}
	return refreshed, nil
}

func (m *Manager) fetchGoogleEmail(ctx context.Context, token string) (string, string) {
	if m.GoogleUserInfoURL == "" {
		return "", ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.GoogleUserInfoURL, nil)
	if err != nil {
		return "", ""
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", ""
	}
	var info struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&info)
	return info.Email, info.Name
}

// ─── 3. GitHub OAuth Device Flow (RFC 8628) + gh CLI import ─────────────────

func (m *Manager) loginGitHub(ctx context.Context, opts LoginOptions) (*Credential, error) {
	clientID := firstNonEmpty(opts.ClientID, os.Getenv("POLYPOD_GITHUB_OAUTH_CLIENT_ID"))
	baseURL := firstNonEmpty(opts.BaseURL, os.Getenv("POLYPOD_GITHUB_BASE_URL"), "https://api.githubcopilot.com")

	if opts.UseGH || clientID == "" {
		if cred, err := m.importGitHubCLI(ctx, baseURL, opts); err == nil && cred != nil {
			return cred, nil
		} else if opts.UseGH {
			return nil, fmt.Errorf("importando sessao `gh auth`: %w", err)
		}
		if clientID == "" {
			return nil, fmt.Errorf("GitHub OAuth requer POLYPOD_GITHUB_OAUTH_CLIENT_ID (--client-id de um GitHub OAuth App com Device Flow habilitado) ou sessao autenticada no `gh auth login` (--gh)")
		}
	}

	deviceEndpoint := firstNonEmpty(opts.DeviceURL, m.GitHubDeviceURL)
	tokenEndpoint := firstNonEmpty(opts.TokenURL, m.GitHubTokenURL)
	token, refreshTok, expiresIn, scopes, err := m.runDeviceCodeFlow(ctx, deviceFlowSpec{
		Provider:  "github",
		ClientID:  clientID,
		DeviceURL: deviceEndpoint,
		TokenURL:  tokenEndpoint,
		Scope:     "read:user user:email",
	}, opts)
	if err != nil {
		return nil, err
	}

	login, email := m.fetchGitHubUser(ctx, token)
	accountID := firstNonEmpty(opts.AccountID, login, email, "github-default")
	var expiresAt time.Time
	if expiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second).UTC()
	}
	cred := &Credential{
		Provider:     "github",
		AccountID:    accountID,
		Email:        email,
		DisplayName:  login,
		AccessToken:  token,
		RefreshToken: refreshTok,
		TokenType:    "Bearer",
		ClientID:     clientID,
		TokenURL:     tokenEndpoint,
		BaseURL:      baseURL,
		Scopes:       scopes,
		ExpiresAt:    expiresAt,
	}
	if err := m.store.SaveAccount("github", accountID, cred); err != nil {
		return nil, err
	}
	return cred, nil
}

func (m *Manager) importGitHubCLI(ctx context.Context, baseURL string, opts LoginOptions) (*Credential, error) {
	cmd := exec.CommandContext(ctx, "gh", "auth", "token")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return nil, fmt.Errorf("token vazio do `gh auth token`")
	}
	login, email := m.fetchGitHubUser(ctx, token)
	accountID := firstNonEmpty(opts.AccountID, login, email, "gh-cli")
	cred := &Credential{
		Provider:    "github",
		AccountID:   accountID,
		Email:       email,
		DisplayName: login,
		AccessToken: token,
		TokenType:   "Bearer",
		BaseURL:     baseURL,
	}
	if err := m.store.SaveAccount("github", accountID, cred); err != nil {
		return nil, err
	}
	return cred, nil
}

func (m *Manager) fetchGitHubUser(ctx context.Context, token string) (string, string) {
	if m.GitHubUserURL == "" {
		return "", ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.GitHubUserURL, nil)
	if err != nil {
		return "", ""
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", ""
	}
	var u struct {
		Login string `json:"login"`
		Email string `json:"email"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&u)
	return u.Login, u.Email
}

// ─── 4. Qwen / Alibaba Cloud OAuth 2.0 (Device Flow / PKCE / ~/.qwen import) ─

func (m *Manager) loginQwen(ctx context.Context, opts LoginOptions) (*Credential, error) {
	clientID := firstNonEmpty(opts.ClientID, os.Getenv("POLYPOD_QWEN_OAUTH_CLIENT_ID"))
	clientSecret := firstNonEmpty(opts.ClientSecret, os.Getenv("POLYPOD_QWEN_OAUTH_CLIENT_SECRET"))
	deviceURL := firstNonEmpty(opts.DeviceURL, os.Getenv("POLYPOD_QWEN_OAUTH_DEVICE_URL"))
	authURL := firstNonEmpty(opts.AuthURL, os.Getenv("POLYPOD_QWEN_OAUTH_AUTH_URL"))
	tokenURL := firstNonEmpty(opts.TokenURL, os.Getenv("POLYPOD_QWEN_OAUTH_TOKEN_URL"))
	baseURL := firstNonEmpty(opts.BaseURL, os.Getenv("POLYPOD_QWEN_BASE_URL"), qwenDashScopeBaseURL)

	if clientID != "" && deviceURL != "" && tokenURL != "" {
		tok, refreshTok, expiresIn, scopes, err := m.runDeviceCodeFlow(ctx, deviceFlowSpec{
			Provider:  "qwen",
			ClientID:  clientID,
			DeviceURL: deviceURL,
			TokenURL:  tokenURL,
			Scope:     "openid profile model.completion",
		}, opts)
		if err != nil {
			return nil, err
		}
		accountID := firstNonEmpty(opts.AccountID, "qwen-oauth")
		var exp time.Time
		if expiresIn > 0 {
			exp = time.Now().Add(time.Duration(expiresIn) * time.Second).UTC()
		}
		cred := &Credential{
			Provider: "qwen", AccountID: accountID, AccessToken: tok,
			RefreshToken: refreshTok, TokenType: "Bearer", ClientID: clientID,
			ClientSecret: clientSecret, TokenURL: tokenURL, BaseURL: baseURL,
			Scopes: scopes, ExpiresAt: exp,
		}
		if err := m.store.SaveAccount("qwen", accountID, cred); err != nil {
			return nil, err
		}
		return cred, nil
	}

	if clientID != "" && authURL != "" && tokenURL != "" {
		return m.runStandardAuthCodePKCE(ctx, "qwen", pkceProviderSpec{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			AuthURL:      authURL,
			TokenURL:     tokenURL,
			Scopes:       "openid profile email",
			BaseURL:      baseURL,
		}, opts)
	}

	if cred, err := m.importQwenCredsFile(baseURL, opts); err == nil && cred != nil {
		return cred, nil
	}

	return nil, fmt.Errorf("Qwen OAuth: o fluxo gratuito anonimo de chat.qwen.ai foi encerrado pela Alibaba em abril/2026; informe --client-id + endpoints OAuth corporativos (POLYPOD_QWEN_OAUTH_CLIENT_ID / POLYPOD_QWEN_OAUTH_AUTH_URL / POLYPOD_QWEN_OAUTH_TOKEN_URL), credenciais em ~/.qwen/oauth_creds.json, ou conecte DASHSCOPE_API_KEY")
}

func (m *Manager) importQwenCredsFile(defaultBase string, opts LoginOptions) (*Credential, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, ".qwen", "oauth_creds.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ResourceURL  string `json:"resource_url"`
		ExpiryDate   int64  `json:"expiry_date"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || strings.TrimSpace(raw.AccessToken) == "" {
		return nil, fmt.Errorf("oauth_creds.json invalido")
	}
	baseURL := defaultBase
	if strings.TrimSpace(raw.ResourceURL) != "" {
		ru := strings.TrimSpace(raw.ResourceURL)
		if !strings.HasPrefix(ru, "http://") && !strings.HasPrefix(ru, "https://") {
			ru = "https://" + ru
		}
		if !strings.HasSuffix(ru, "/v1") {
			ru = strings.TrimRight(ru, "/") + "/v1"
		}
		baseURL = ru
	}
	var exp time.Time
	if raw.ExpiryDate > 0 {
		exp = time.UnixMilli(raw.ExpiryDate).UTC()
	}
	accountID := firstNonEmpty(opts.AccountID, "qwen-cli")
	cred := &Credential{
		Provider:     "qwen",
		AccountID:    accountID,
		AccessToken:  strings.TrimSpace(raw.AccessToken),
		RefreshToken: strings.TrimSpace(raw.RefreshToken),
		TokenType:    "Bearer",
		BaseURL:      baseURL,
		ExpiresAt:    exp,
	}
	if err := m.store.SaveAccount("qwen", accountID, cred); err != nil {
		return nil, err
	}
	return cred, nil
}

// ─── 5. Anthropic / Claude OAuth 2.1 PKCE + RFC 8414 Discovery + RFC 7591 DCR ─

func (m *Manager) loginAnthropic(ctx context.Context, opts LoginOptions) (*Credential, error) {
	issuer := strings.TrimRight(firstNonEmpty(opts.IssuerURL, os.Getenv("POLYPOD_ANTHROPIC_OAUTH_ISSUER")), "/")
	clientID := firstNonEmpty(opts.ClientID, os.Getenv("POLYPOD_ANTHROPIC_OAUTH_CLIENT_ID"))
	clientSecret := firstNonEmpty(opts.ClientSecret, os.Getenv("POLYPOD_ANTHROPIC_OAUTH_CLIENT_SECRET"))
	authURL := firstNonEmpty(opts.AuthURL, os.Getenv("POLYPOD_ANTHROPIC_OAUTH_AUTH_URL"))
	tokenURL := firstNonEmpty(opts.TokenURL, os.Getenv("POLYPOD_ANTHROPIC_OAUTH_TOKEN_URL"))
	baseURL := firstNonEmpty(opts.BaseURL, os.Getenv("POLYPOD_ANTHROPIC_BASE_URL"))

	var registrationURL string
	if issuer != "" && (authURL == "" || tokenURL == "" || clientID == "") {
		meta, err := m.discoverOAuthMetadata(ctx, issuer)
		if err != nil {
			return nil, fmt.Errorf("descoberta OAuth 2.1 em %s: %w", issuer, err)
		}
		authURL = firstNonEmpty(authURL, meta.AuthorizationEndpoint)
		tokenURL = firstNonEmpty(tokenURL, meta.TokenEndpoint)
		registrationURL = meta.RegistrationEndpoint
	}

	if authURL == "" || tokenURL == "" {
		return nil, fmt.Errorf("Anthropic OAuth: o OAuth consumidor de claude.ai e restrito pela Anthropic aos clientes proprietarios oficiais; para conectar um gateway/IdP OAuth 2.1 compativel (DCR RFC 7591 + PKCE), informe --issuer / POLYPOD_ANTHROPIC_OAUTH_ISSUER (ou --auth-url, --token-url e --client-id), ou use ANTHROPIC_API_KEY")
	}

	verifier, challenge, err := generatePKCE()
	if err != nil {
		return nil, err
	}
	state, err := randomB64URL(32)
	if err != nil {
		return nil, err
	}
	cb, err := startLoopbackCallbackServer(ctx, "Anthropic")
	if err != nil {
		return nil, err
	}
	defer cb.Close()

	if clientID == "" && registrationURL != "" {
		registeredID, registeredSecret, regErr := m.registerDynamicOAuthClient(ctx, registrationURL, cb.RedirectURI)
		if regErr != nil {
			return nil, fmt.Errorf("registro dinamico RFC 7591 Anthropic OAuth: %w", regErr)
		}
		clientID = registeredID
		clientSecret = firstNonEmpty(clientSecret, registeredSecret)
	}
	if clientID == "" {
		return nil, fmt.Errorf("Anthropic OAuth requer --client-id (ou endpoint `registration_endpoint` RFC 7591 via --issuer)")
	}

	return m.finishAuthCodePKCE(ctx, "anthropic", cb, verifier, challenge, state, pkceProviderSpec{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Issuer:       issuer,
		AuthURL:      authURL,
		TokenURL:     tokenURL,
		Scopes:       "openid profile email",
		BaseURL:      baseURL,
	}, opts)
}

type oauthServerMetadata struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RegistrationEndpoint  string `json:"registration_endpoint"`
}

func (m *Manager) discoverOAuthMetadata(ctx context.Context, issuer string) (*oauthServerMetadata, error) {
	wellKnown := strings.TrimRight(issuer, "/") + "/.well-known/oauth-authorization-server"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnown, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d em %s", resp.StatusCode, wellKnown)
	}
	var meta oauthServerMetadata
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(&meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func (m *Manager) registerDynamicOAuthClient(ctx context.Context, regURL, redirectURI string) (string, string, error) {
	body, _ := json.Marshal(map[string]any{
		"client_name":                "Polypod",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, regURL, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var out struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(&out); err != nil {
		return "", "", err
	}
	if out.ClientID == "" {
		return "", "", fmt.Errorf("client_id ausente na resposta DCR")
	}
	return out.ClientID, out.ClientSecret, nil
}

// ─── Shared PKCE & RFC 8628 Device Flow Engines ─────────────────────────────

type pkceProviderSpec struct {
	ClientID        string
	ClientSecret    string
	Issuer          string
	AuthURL         string
	TokenURL        string
	Scopes          string
	BaseURL         string
	ExtraAuthParams map[string]string
	FetchProfile    func(ctx context.Context, accessToken string) (email, display string)
}

func (m *Manager) runStandardAuthCodePKCE(ctx context.Context, provider string, spec pkceProviderSpec, opts LoginOptions) (*Credential, error) {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return nil, err
	}
	state, err := randomB64URL(32)
	if err != nil {
		return nil, err
	}
	cb, err := startLoopbackCallbackServer(ctx, provider)
	if err != nil {
		return nil, err
	}
	defer cb.Close()
	return m.finishAuthCodePKCE(ctx, provider, cb, verifier, challenge, state, spec, opts)
}

func (m *Manager) finishAuthCodePKCE(ctx context.Context, provider string, cb *loopbackServer, verifier, challenge, state string, spec pkceProviderSpec, opts LoginOptions) (*Credential, error) {
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {spec.ClientID},
		"redirect_uri":          {cb.RedirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}
	if spec.Scopes != "" {
		params.Set("scope", spec.Scopes)
	}
	for k, v := range spec.ExtraAuthParams {
		params.Set(k, v)
	}
	sep := "?"
	if strings.Contains(spec.AuthURL, "?") {
		sep = "&"
	}
	loginURL := spec.AuthURL + sep + params.Encode()
	m.notify(opts, fmt.Sprintf("Abrindo navegador para autenticar %s: %s", provider, loginURL))
	if err := m.openBrowser(loginURL); err != nil {
		m.notify(opts, "Abra manualmente no navegador: "+loginURL)
	}

	res, err := cb.Wait(ctx)
	if err != nil {
		return nil, err
	}
	if res.State != "" && res.State != state {
		return nil, fmt.Errorf("estado OAuth invalido em %s", provider)
	}

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {res.Code},
		"redirect_uri":  {cb.RedirectURI},
		"client_id":     {spec.ClientID},
		"code_verifier": {verifier},
	}
	if spec.ClientSecret != "" {
		form.Set("client_secret", spec.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, spec.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("troca de token OAuth %s: %w", provider, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
		Error        string `json:"error"`
		Description  string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("resposta OAuth invalida em %s (HTTP %d)", provider, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || tr.Error != "" || tr.AccessToken == "" {
		return nil, fmt.Errorf("OAuth %s recusado (HTTP %d): %s %s", provider, resp.StatusCode, tr.Error, tr.Description)
	}

	var email, display string
	if spec.FetchProfile != nil {
		email, display = spec.FetchProfile(ctx, tr.AccessToken)
	}
	accountID := firstNonEmpty(opts.AccountID, email, display, provider+"-default")
	var expiresAt time.Time
	if tr.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second).UTC()
	}
	scopes := strings.Fields(tr.Scope)
	if len(scopes) == 0 && spec.Scopes != "" {
		scopes = strings.Fields(spec.Scopes)
	}
	cred := &Credential{
		Provider:     provider,
		AccountID:    accountID,
		Email:        email,
		DisplayName:  display,
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		IDToken:      tr.IDToken,
		TokenType:    firstNonEmpty(tr.TokenType, "Bearer"),
		ClientID:     spec.ClientID,
		ClientSecret: spec.ClientSecret,
		Issuer:       spec.Issuer,
		TokenURL:     spec.TokenURL,
		BaseURL:      spec.BaseURL,
		Scopes:       scopes,
		ExpiresAt:    expiresAt,
	}
	if err := m.store.SaveAccount(provider, accountID, cred); err != nil {
		return nil, err
	}
	return cred, nil
}

type deviceFlowSpec struct {
	Provider  string
	ClientID  string
	DeviceURL string
	TokenURL  string
	Scope     string
}

func (m *Manager) runDeviceCodeFlow(ctx context.Context, spec deviceFlowSpec, opts LoginOptions) (accessToken, refreshToken string, expiresIn int, scopes []string, err error) {
	form := url.Values{
		"client_id": {spec.ClientID},
	}
	if spec.Scope != "" {
		form.Set("scope", spec.Scope)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, spec.DeviceURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", 0, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", "", 0, nil, fmt.Errorf("iniciando Device Flow %s: %w", spec.Provider, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return "", "", 0, nil, err
	}
	var dc struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := json.Unmarshal(body, &dc); err != nil || dc.DeviceCode == "" {
		return "", "", 0, nil, fmt.Errorf("resposta Device Flow invalida em %s (HTTP %d)", spec.Provider, resp.StatusCode)
	}
	targetURI := firstNonEmpty(dc.VerificationURIComplete, dc.VerificationURI)
	m.notify(opts, fmt.Sprintf("Autorize %s em %s usando o codigo: %s", spec.Provider, dc.VerificationURI, dc.UserCode))
	if targetURI != "" {
		_ = m.openBrowser(targetURI)
	}

	interval := time.Duration(dc.Interval) * time.Second
	if interval <= 0 {
		interval = 2 * time.Second
	}
	deadline := time.Now().Add(10 * time.Minute)
	if dc.ExpiresIn > 0 {
		deadline = time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	}

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", "", 0, nil, ctx.Err()
		case <-time.After(interval):
		}

		pollForm := url.Values{
			"client_id":   {spec.ClientID},
			"device_code": {dc.DeviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		}
		pReq, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, spec.TokenURL, strings.NewReader(pollForm.Encode()))
		if reqErr != nil {
			return "", "", 0, nil, reqErr
		}
		pReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		pReq.Header.Set("Accept", "application/json")
		pResp, doErr := m.httpClient.Do(pReq)
		if doErr != nil {
			continue
		}
		pBody, _ := io.ReadAll(io.LimitReader(pResp.Body, 256<<10))
		pResp.Body.Close()

		var tr struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresIn    int    `json:"expires_in"`
			Scope        string `json:"scope"`
			Error        string `json:"error"`
			Description  string `json:"error_description"`
		}
		if json.Unmarshal(pBody, &tr) != nil {
			continue
		}
		if tr.AccessToken != "" {
			return tr.AccessToken, tr.RefreshToken, tr.ExpiresIn, strings.Fields(tr.Scope), nil
		}
		switch tr.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		case "":
			continue
		default:
			return "", "", 0, nil, fmt.Errorf("Device Flow %s recusado: %s %s", spec.Provider, tr.Error, tr.Description)
		}
	}
	return "", "", 0, nil, fmt.Errorf("tempo esgotado aguardando autorizacao Device Flow %s", spec.Provider)
}

// ─── Models Discovery for Connected OAuth Providers ─────────────────────────

func (m *Manager) ListModels(ctx context.Context, provider string) ([]string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "chatgpt" {
		models, err := chatgptauth.NewManager(m.store.chatGPTPath).Models(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(models))
		for _, md := range models {
			out = append(out, md.Name())
		}
		return out, nil
	}
	token, err := m.AccessToken(ctx, provider)
	if err != nil {
		return nil, err
	}
	cred, _ := m.store.LoadActive(provider)
	baseURL := ""
	if cred != nil {
		baseURL = strings.TrimRight(cred.BaseURL, "/")
	}

	if provider == "google" {
		if baseURL == "" {
			baseURL = googleGeminiBaseURL
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := m.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("lista de modelos Google HTTP %d", resp.StatusCode)
		}
		var body struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
			return nil, err
		}
		var out []string
		for _, md := range body.Models {
			out = append(out, strings.TrimPrefix(md.Name, "models/"))
		}
		return out, nil
	}

	if baseURL == "" {
		switch provider {
		case "openrouter":
			baseURL = openRouterAPIBaseURL
		case "qwen":
			baseURL = qwenDashScopeBaseURL
		case "anthropic":
			baseURL = "https://api.anthropic.com/v1"
		default:
			baseURL = "https://api.githubcopilot.com"
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if provider == "anthropic" {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lista de modelos %s HTTP %d", provider, resp.StatusCode)
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(body.Data))
	for _, item := range body.Data {
		if item.ID != "" {
			out = append(out, item.ID)
		}
	}
	return out, nil
}

// ─── Loopback Callback & Crypto Helpers ─────────────────────────────────────

type callbackResult struct {
	Code  string
	State string
	Err   error
}

type loopbackServer struct {
	RedirectURI string
	server      *http.Server
	resultCh    chan callbackResult
}

func startLoopbackCallbackServer(_ context.Context, label string) (*loopbackServer, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("abrindo callback OAuth local: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/auth/callback", port)
	resultCh := make(chan callbackResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		res := callbackResult{
			Code:  q.Get("code"),
			State: q.Get("state"),
		}
		if oauthErr := q.Get("error"); oauthErr != "" {
			res.Err = fmt.Errorf("%s: %s", oauthErr, q.Get("error_description"))
		} else if res.Code == "" {
			res.Err = fmt.Errorf("callback sem codigo de autorizacao")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if res.Err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "<h2>Falha na conexao %s</h2><p>%s</p>", html.EscapeString(label), html.EscapeString(res.Err.Error()))
		} else {
			fmt.Fprintf(w, "<h2>%s conectado ao Polypod</h2><p>Voce pode fechar esta janela.</p>", html.EscapeString(label))
		}
		select {
		case resultCh <- res:
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(listener) }()
	return &loopbackServer{
		RedirectURI: redirectURI,
		server:      srv,
		resultCh:    resultCh,
	}, nil
}

func (l *loopbackServer) Wait(ctx context.Context) (callbackResult, error) {
	select {
	case res := <-l.resultCh:
		if res.Err != nil {
			return callbackResult{}, res.Err
		}
		return res, nil
	case <-ctx.Done():
		return callbackResult{}, ctx.Err()
	case <-time.After(10 * time.Minute):
		return callbackResult{}, fmt.Errorf("tempo de login OAuth expirado")
	}
}

func (l *loopbackServer) Close() {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = l.server.Shutdown(shutdownCtx)
}

func randomB64URL(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func generatePKCE() (verifier, challenge string, err error) {
	verifier, err = randomB64URL(48)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func defaultOpenBrowser(target string) error {
	if os.Geteuid() == 0 {
		name := os.Getenv("POLYPOD_DESKTOP_USER")
		if name == "" {
			name = "costa"
		}
		return exec.Command("/usr/sbin/runuser", "-u", name, "--", "/usr/bin/xdg-open", target).Start()
	}
	return exec.Command("xdg-open", target).Start()
}
