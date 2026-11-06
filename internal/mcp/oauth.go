package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ─── OAuth Configuration ────────────────────────────────────────────────────

// OAuthConfig holds OAuth settings for an MCP server.
type OAuthConfig struct {
	ClientID              string `json:"clientId,omitempty" yaml:"clientId,omitempty"`
	ClientSecret          string `json:"clientSecret,omitempty" yaml:"clientSecret,omitempty"`
	AuthURL               string `json:"authUrl,omitempty" yaml:"authUrl,omitempty"`
	TokenURL              string `json:"tokenUrl,omitempty" yaml:"tokenUrl,omitempty"`
	Scopes                string `json:"scopes,omitempty" yaml:"scopes,omitempty"`
	CallbackPort          int    `json:"callbackPort,omitempty" yaml:"callbackPort,omitempty"`
	AuthServerMetadataURL string `json:"authServerMetadataUrl,omitempty" yaml:"authServerMetadataUrl,omitempty"`
}

// OAuthTokens holds the tokens obtained from an OAuth flow.
type OAuthTokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	ExpiresIn    int       `json:"expires_in,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

// IsExpired returns true if the access token has expired (with a 60s buffer).
func (t *OAuthTokens) IsExpired() bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(t.ExpiresAt.Add(-60 * time.Second))
}

// ─── OAuth Authorization Server Metadata ────────────────────────────────────

// AuthServerMetadata represents RFC 8414 OAuth Authorization Server Metadata.
type AuthServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint,omitempty"`
	RevocationEndpoint                string   `json:"revocation_endpoint,omitempty"`
	ScopesSupported                   []string `json:"scopes_supported,omitempty"`
	ResponseTypesSupported            []string `json:"response_types_supported,omitempty"`
	GrantTypesSupported               []string `json:"grant_types_supported,omitempty"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported,omitempty"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported,omitempty"`
}

// ─── PKCE Support ───────────────────────────────────────────────────────────

// PKCEChallenge holds the PKCE code verifier and challenge.
type PKCEChallenge struct {
	CodeVerifier  string
	CodeChallenge string
	Method        string // "S256"
}

// NewPKCEChallenge generates a new PKCE code verifier and S256 challenge.
func NewPKCEChallenge() (*PKCEChallenge, error) {
	verifier := make([]byte, 32)
	if _, err := rand.Read(verifier); err != nil {
		return nil, fmt.Errorf("generate PKCE verifier: %w", err)
	}
	codeVerifier := base64.RawURLEncoding.EncodeToString(verifier)

	hash := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(hash[:])

	return &PKCEChallenge{
		CodeVerifier:  codeVerifier,
		CodeChallenge: codeChallenge,
		Method:        "S256",
	}, nil
}

// ─── OAuth State ────────────────────────────────────────────────────────────

func generateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ─── OAuth Flow ─────────────────────────────────────────────────────────────

// OAuthFlow manages the OAuth authorization code + PKCE flow.
type OAuthFlow struct {
	Config    *OAuthConfig
	Logger    *slog.Logger
	Metadata  *AuthServerMetadata
	OnAuthURL func(url string) // Called with the authorization URL for the user to open

	mu     sync.Mutex
	tokens *OAuthTokens
}

// NewOAuthFlow creates a new OAuth flow handler.
func NewOAuthFlow(cfg *OAuthConfig, logger *slog.Logger) *OAuthFlow {
	return &OAuthFlow{
		Config: cfg,
		Logger: logger,
	}
}

// DiscoverMetadata fetches the OAuth authorization server metadata via
// RFC 8414 well-known endpoint.
func (f *OAuthFlow) DiscoverMetadata(ctx context.Context, serverURL string) error {
	if f.Config.AuthServerMetadataURL != "" {
		return f.fetchMetadata(ctx, f.Config.AuthServerMetadataURL)
	}

	// Try RFC 9728 path-aware endpoint first, then root
	u, err := url.Parse(serverURL)
	if err != nil {
		return fmt.Errorf("parse server URL: %w", err)
	}

	paths := []string{
		fmt.Sprintf("/.well-known/oauth-authorization-server%s", u.Path),
		"/.well-known/oauth-authorization-server",
	}

	for _, path := range paths {
		metaURL := fmt.Sprintf("%s://%s%s", u.Scheme, u.Host, path)
		err := f.fetchMetadata(ctx, metaURL)
		if err == nil {
			return nil
		}
		if f.Logger != nil {
			f.Logger.Debug("metadata discovery failed", "url", metaURL, "error", err)
		}
	}

	return errors.New("OAuth metadata discovery failed: no valid metadata found")
}

func (f *OAuthFlow) fetchMetadata(ctx context.Context, metadataURL string) error {
	client := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, metadataURL)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var meta AuthServerMetadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return fmt.Errorf("parse metadata: %w", err)
	}

	if meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" {
		return errors.New("metadata missing required endpoints")
	}

	f.Metadata = &meta
	return nil
}

// StartOAuthFlow initiates the OAuth authorization code flow with PKCE.
// It opens a browser to the authorization URL and waits for the callback.
// Returns nil on success (tokens stored internally), or an error.
func (f *OAuthFlow) StartOAuthFlow(ctx context.Context, serverURL string) error {
	// Discover metadata if not provided
	if f.Metadata == nil {
		authURL := f.Config.AuthURL
		tokenURL := f.Config.TokenURL
		if authURL == "" || tokenURL == "" {
			if err := f.DiscoverMetadata(ctx, serverURL); err != nil {
				return fmt.Errorf("discover metadata: %w", err)
			}
		} else {
			f.Metadata = &AuthServerMetadata{
				AuthorizationEndpoint: authURL,
				TokenEndpoint:         tokenURL,
			}
		}
	}

	// Generate PKCE challenge
	pkce, err := NewPKCEChallenge()
	if err != nil {
		return err
	}

	// Generate state
	state, err := generateState()
	if err != nil {
		return fmt.Errorf("generate state: %w", err)
	}

	// Find available port for callback
	port, err := findAvailablePort(f.Config.CallbackPort)
	if err != nil {
		return fmt.Errorf("find callback port: %w", err)
	}
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", port)

	// Build authorization URL
	authURL, err := url.Parse(f.Metadata.AuthorizationEndpoint)
	if err != nil {
		return fmt.Errorf("parse auth endpoint: %w", err)
	}
	q := authURL.Query()
	q.Set("response_type", "code")
	q.Set("client_id", f.Config.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", pkce.CodeChallenge)
	q.Set("code_challenge_method", pkce.Method)
	if f.Config.Scopes != "" {
		q.Set("scope", f.Config.Scopes)
	}
	authURL.RawQuery = q.Encode()

	// Set up callback server
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		qp := r.URL.Query()

		if errParam := qp.Get("error"); errParam != "" {
			desc := qp.Get("error_description")
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "<h1>Authentication Error</h1><p>%s: %s</p><p>You can close this window.</p>", errParam, desc)
			errCh <- fmt.Errorf("OAuth error: %s - %s", errParam, desc)
			return
		}

		if qp.Get("state") != state {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, "<h1>Error</h1><p>State mismatch - possible CSRF attack.</p>")
			errCh <- errors.New("OAuth state mismatch")
			return
		}

		code := qp.Get("code")
		if code == "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, "<h1>Error</h1><p>Missing authorization code.</p>")
			errCh <- errors.New("missing authorization code")
			return
		}

		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "<h1>Authentication Successful</h1><p>You can close this window. Return to Polypod.</p>")
		codeCh <- code
	})

	server := &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", port),
		Handler: mux,
	}

	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("listen on port %d: %w", port, err)
	}

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	// Open browser
	authURLStr := authURL.String()
	if f.OnAuthURL != nil {
		f.OnAuthURL(authURLStr)
	}
	if err := openBrowser(authURLStr); err != nil {
		if f.Logger != nil {
			f.Logger.Warn("failed to open browser", "error", err)
		}
	}

	// Wait for callback or timeout
	timeout := time.After(5 * time.Minute)
	var code string

	select {
	case code = <-codeCh:
		// got the code
	case err := <-errCh:
		return err
	case <-timeout:
		return errors.New("OAuth flow timed out after 5 minutes")
	case <-ctx.Done():
		return ctx.Err()
	}

	// Exchange code for tokens
	return f.exchangeCodeForToken(ctx, code, redirectURI, pkce.CodeVerifier)
}

// exchangeCodeForToken exchanges the authorization code for tokens.
func (f *OAuthFlow) exchangeCodeForToken(ctx context.Context, code, redirectURI, codeVerifier string) error {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {f.Config.ClientID},
		"code_verifier": {codeVerifier},
	}
	if f.Config.ClientSecret != "" {
		data.Set("client_secret", f.Config.ClientSecret)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.Metadata.TokenEndpoint,
		strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token exchange failed: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var tokens OAuthTokens
	if err := json.Unmarshal(body, &tokens); err != nil {
		return fmt.Errorf("parse token response: %w", err)
	}

	if tokens.AccessToken == "" {
		return errors.New("token response missing access_token")
	}

	if tokens.ExpiresIn > 0 {
		tokens.ExpiresAt = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
	}

	f.mu.Lock()
	f.tokens = &tokens
	f.mu.Unlock()

	return nil
}

// RefreshToken refreshes the access token using the refresh token.
func (f *OAuthFlow) RefreshToken(ctx context.Context) error {
	f.mu.Lock()
	tokens := f.tokens
	f.mu.Unlock()

	if tokens == nil || tokens.RefreshToken == "" {
		return errors.New("no refresh token available")
	}

	if f.Metadata == nil || f.Metadata.TokenEndpoint == "" {
		return errors.New("no token endpoint configured")
	}

	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {tokens.RefreshToken},
		"client_id":     {f.Config.ClientID},
	}
	if f.Config.ClientSecret != "" {
		data.Set("client_secret", f.Config.ClientSecret)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.Metadata.TokenEndpoint,
		strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("refresh token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("refresh failed: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var newTokens OAuthTokens
	if err := json.Unmarshal(body, &newTokens); err != nil {
		return fmt.Errorf("parse refresh response: %w", err)
	}

	if newTokens.AccessToken == "" {
		return errors.New("refresh response missing access_token")
	}

	if newTokens.ExpiresIn > 0 {
		newTokens.ExpiresAt = time.Now().Add(time.Duration(newTokens.ExpiresIn) * time.Second)
	}

	// Preserve refresh token if not returned
	if newTokens.RefreshToken == "" {
		newTokens.RefreshToken = tokens.RefreshToken
	}

	f.mu.Lock()
	f.tokens = &newTokens
	f.mu.Unlock()

	return nil
}

// Tokens returns the current OAuth tokens, refreshing if expired.
func (f *OAuthFlow) Tokens(ctx context.Context) (*OAuthTokens, error) {
	f.mu.Lock()
	tokens := f.tokens
	f.mu.Unlock()

	if tokens == nil {
		return nil, errors.New("not authenticated")
	}

	if tokens.IsExpired() && tokens.RefreshToken != "" {
		if err := f.RefreshToken(ctx); err != nil {
			return nil, fmt.Errorf("auto-refresh failed: %w", err)
		}
		f.mu.Lock()
		tokens = f.tokens
		f.mu.Unlock()
	}

	return tokens, nil
}

// SetTokens sets tokens directly (for restoring from storage).
func (f *OAuthFlow) SetTokens(tokens *OAuthTokens) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = tokens
}

// HasTokens returns true if tokens are available.
func (f *OAuthFlow) HasTokens() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokens != nil && f.tokens.AccessToken != ""
}

// RevokeTokens revokes tokens on the OAuth server.
func (f *OAuthFlow) RevokeTokens(ctx context.Context) error {
	f.mu.Lock()
	tokens := f.tokens
	f.mu.Unlock()

	if tokens == nil {
		return nil
	}

	if f.Metadata == nil || f.Metadata.RevocationEndpoint == "" {
		// No revocation endpoint, just clear locally
		f.mu.Lock()
		f.tokens = nil
		f.mu.Unlock()
		return nil
	}

	client := &http.Client{Timeout: 30 * time.Second}

	// Revoke refresh token first (more important)
	if tokens.RefreshToken != "" {
		_ = revokeToken(ctx, client, f.Metadata.RevocationEndpoint, tokens.RefreshToken, "refresh_token", f.Config.ClientID)
	}

	// Then access token
	if tokens.AccessToken != "" {
		_ = revokeToken(ctx, client, f.Metadata.RevocationEndpoint, tokens.AccessToken, "access_token", f.Config.ClientID)
	}

	f.mu.Lock()
	f.tokens = nil
	f.mu.Unlock()

	return nil
}

func revokeToken(ctx context.Context, client *http.Client, endpoint, token, hint, clientID string) error {
	data := url.Values{
		"token":           {token},
		"token_type_hint": {hint},
		"client_id":       {clientID},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("revocation failed: HTTP %d", resp.StatusCode)
	}
	return nil
}

// ─── Token Storage ──────────────────────────────────────────────────────────

// TokenStorage persists OAuth tokens.
type TokenStorage interface {
	// Save persists tokens for a server.
	Save(serverKey string, tokens *OAuthTokens) error
	// Load retrieves tokens for a server.
	Load(serverKey string) (*OAuthTokens, error)
	// Delete removes tokens for a server.
	Delete(serverKey string) error
}

// MemoryTokenStorage is an in-memory token store (for testing/ephemeral use).
type MemoryTokenStorage struct {
	mu     sync.RWMutex
	tokens map[string]*OAuthTokens
}

// NewMemoryTokenStorage creates an in-memory token store.
func NewMemoryTokenStorage() *MemoryTokenStorage {
	return &MemoryTokenStorage{
		tokens: make(map[string]*OAuthTokens),
	}
}

func (s *MemoryTokenStorage) Save(serverKey string, tokens *OAuthTokens) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[serverKey] = tokens
	return nil
}

func (s *MemoryTokenStorage) Load(serverKey string) (*OAuthTokens, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tokens, ok := s.tokens[serverKey]
	if !ok {
		return nil, fmt.Errorf("no tokens for server %q", serverKey)
	}
	return tokens, nil
}

func (s *MemoryTokenStorage) Delete(serverKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, serverKey)
	return nil
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func findAvailablePort(preferred int) (int, error) {
	if preferred > 0 {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferred))
		if err == nil {
			listener.Close()
			return preferred, nil
		}
	}

	// Try random ports in range 49152-65535
	for i := 0; i < 100; i++ {
		b := make([]byte, 2)
		rand.Read(b)
		port := 49152 + int(b[0])<<8 + int(b[1])
		port = port%(65535-49152) + 49152

		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			listener.Close()
			return port, nil
		}
	}

	// Fallback: let OS pick
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, errors.New("no available port for OAuth callback")
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return port, nil
}

func openBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "linux":
		cmd = "xdg-open"
		args = []string{url}
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	return exec.Command(cmd, args...).Start()
}
