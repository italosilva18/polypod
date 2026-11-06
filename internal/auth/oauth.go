package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ─── OAuth Configuration ────────────────────────────────────────────────────

// OAuthConfig holds the settings needed to start an OAuth 2.0 flow.
type OAuthConfig struct {
	ProviderName string   // e.g. "google", "github"
	ClientID     string   // OAuth client ID
	ClientSecret string   // OAuth client secret (may be empty for PKCE flows)
	AuthURL      string   // Authorization endpoint
	TokenURL     string   // Token exchange endpoint
	RedirectURL  string   // Local callback URL (default: http://localhost:<port>/callback)
	Scopes       []string // Requested scopes
}

// OAuthTokenResponse is the JSON body returned by the token endpoint.
type OAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"` // seconds
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
	Error        string `json:"error,omitempty"`
	ErrorDesc    string `json:"error_description,omitempty"`
}

// ─── OAuth Flow ─────────────────────────────────────────────────────────────

// StartOAuthFlow builds and returns the authorization URL that the user should
// open in a browser. The state parameter is a random string for CSRF protection.
func StartOAuthFlow(cfg OAuthConfig, state string) (string, error) {
	if cfg.ClientID == "" {
		return "", fmt.Errorf("oauth: client_id is required")
	}
	if cfg.AuthURL == "" {
		return "", fmt.Errorf("oauth: auth_url is required")
	}

	redirectURL := cfg.RedirectURL
	if redirectURL == "" {
		redirectURL = "http://localhost:9876/callback"
	}

	params := url.Values{
		"client_id":     {cfg.ClientID},
		"redirect_uri":  {redirectURL},
		"response_type": {"code"},
		"state":         {state},
	}
	if len(cfg.Scopes) > 0 {
		params.Set("scope", strings.Join(cfg.Scopes, " "))
	}

	authURL := cfg.AuthURL
	if strings.Contains(authURL, "?") {
		authURL += "&" + params.Encode()
	} else {
		authURL += "?" + params.Encode()
	}

	return authURL, nil
}

// HandleOAuthCallback exchanges an authorization code for tokens.
func HandleOAuthCallback(cfg OAuthConfig, code string) (*Credentials, error) {
	if cfg.TokenURL == "" {
		return nil, fmt.Errorf("oauth: token_url is required")
	}

	redirectURL := cfg.RedirectURL
	if redirectURL == "" {
		redirectURL = "http://localhost:9876/callback"
	}

	data := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {redirectURL},
		"client_id":    {cfg.ClientID},
	}
	if cfg.ClientSecret != "" {
		data.Set("client_secret", cfg.ClientSecret)
	}

	resp, err := http.PostForm(cfg.TokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("oauth token exchange failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading token response: %w", err)
	}

	var tokenResp OAuthTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}

	if tokenResp.Error != "" {
		return nil, fmt.Errorf("oauth error: %s — %s", tokenResp.Error, tokenResp.ErrorDesc)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("oauth: received empty access token")
	}

	var expiresAt time.Time
	if tokenResp.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}

	var scopes []string
	if tokenResp.Scope != "" {
		scopes = strings.Split(tokenResp.Scope, " ")
	} else {
		scopes = cfg.Scopes
	}

	return &Credentials{
		Provider:     AuthProviderOAuth,
		OAuthToken:   tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    expiresAt,
		Scopes:       scopes,
	}, nil
}

// RefreshToken uses a refresh token to obtain new credentials.
func RefreshToken(cfg OAuthConfig, creds *Credentials) (*Credentials, error) {
	if creds.RefreshToken == "" {
		return nil, fmt.Errorf("oauth: no refresh token available")
	}
	if cfg.TokenURL == "" {
		return nil, fmt.Errorf("oauth: token_url is required for refresh")
	}

	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {creds.RefreshToken},
		"client_id":     {cfg.ClientID},
	}
	if cfg.ClientSecret != "" {
		data.Set("client_secret", cfg.ClientSecret)
	}

	resp, err := http.PostForm(cfg.TokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("oauth refresh failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading refresh response: %w", err)
	}

	var tokenResp OAuthTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parsing refresh response: %w", err)
	}

	if tokenResp.Error != "" {
		return nil, fmt.Errorf("oauth refresh error: %s — %s", tokenResp.Error, tokenResp.ErrorDesc)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("oauth: received empty access token on refresh")
	}

	var expiresAt time.Time
	if tokenResp.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}

	refreshToken := tokenResp.RefreshToken
	if refreshToken == "" {
		refreshToken = creds.RefreshToken // keep old refresh token if new one not provided
	}

	var scopes []string
	if tokenResp.Scope != "" {
		scopes = strings.Split(tokenResp.Scope, " ")
	} else {
		scopes = creds.Scopes
	}

	return &Credentials{
		Provider:     AuthProviderOAuth,
		OAuthToken:   tokenResp.AccessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt,
		Scopes:       scopes,
	}, nil
}

// ─── Local Callback Server ──────────────────────────────────────────────────

// OAuthCallbackResult holds the result received on the local callback server.
type OAuthCallbackResult struct {
	Code  string
	State string
	Error string
}

// ListenForOAuthCallback starts a temporary local HTTP server that waits for
// the OAuth callback, then shuts down. Returns the authorization code.
// The ctx can be used to time out or cancel the wait.
func ListenForOAuthCallback(ctx context.Context, port int) (*OAuthCallbackResult, error) {
	if port == 0 {
		port = 9876
	}

	var result *OAuthCallbackResult
	var mu sync.Mutex
	done := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		result = &OAuthCallbackResult{
			Code:  r.URL.Query().Get("code"),
			State: r.URL.Query().Get("state"),
			Error: r.URL.Query().Get("error"),
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if result.Error != "" {
			fmt.Fprintf(w, "<html><body><h2>Authorization Failed</h2><p>%s</p><p>You can close this window.</p></body></html>", result.Error)
		} else {
			fmt.Fprint(w, "<html><body><h2>Authorization Successful</h2><p>You can close this window and return to the terminal.</p></body></html>")
		}

		close(done)
	})

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("starting callback listener on %s: %w", addr, err)
	}

	srv := &http.Server{Handler: mux}
	go srv.Serve(listener) //nolint:errcheck

	select {
	case <-done:
		// Received callback
	case <-ctx.Done():
		result = &OAuthCallbackResult{Error: "timeout waiting for OAuth callback"}
	}

	// Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx) //nolint:errcheck

	if result == nil {
		return nil, fmt.Errorf("no OAuth callback received")
	}
	if result.Error != "" {
		return nil, fmt.Errorf("oauth callback error: %s", result.Error)
	}

	return result, nil
}
