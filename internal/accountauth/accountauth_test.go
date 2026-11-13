package accountauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStoreEncryptedMultiAccountAllProviders(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "oauth-accounts.enc")
	cgPath := filepath.Join(dir, "chatgpt-auth.enc")
	store := NewStoreWithChatGPT(storePath, cgPath)

	for _, prov := range []string{"openrouter", "google", "github", "qwen", "anthropic"} {
		if err := store.SaveAccount(prov, "conta-a", &Credential{
			Email:       "a@" + prov + ".test",
			AccessToken: "secret-a-" + prov,
			ExpiresAt:   time.Now().Add(time.Hour).UTC(),
		}); err != nil {
			t.Fatalf("SaveAccount(%s, conta-a): %v", prov, err)
		}
		if err := store.SaveAccount(prov, "conta-b", &Credential{
			Email:       "b@" + prov + ".test",
			AccessToken: "secret-b-" + prov,
			ExpiresAt:   time.Now().Add(time.Hour).UTC(),
		}); err != nil {
			t.Fatalf("SaveAccount(%s, conta-b): %v", prov, err)
		}

		list, err := store.ListAccounts(prov)
		if err != nil {
			t.Fatalf("ListAccounts(%s): %v", prov, err)
		}
		if len(list) != 2 {
			t.Fatalf("%s: len(list)=%d, esperado 2", prov, len(list))
		}

		if err := store.SetActiveAccount(prov, "conta-a"); err != nil {
			t.Fatalf("SetActiveAccount(%s): %v", prov, err)
		}
		active, err := store.LoadActive(prov)
		if err != nil || active == nil || active.AccessToken != "secret-a-"+prov {
			t.Fatalf("LoadActive(%s) = %+v, err=%v", prov, active, err)
		}

		if err := store.DeleteAccount(prov, "conta-a"); err != nil {
			t.Fatalf("DeleteAccount(%s): %v", prov, err)
		}
		promoted, err := store.LoadActive(prov)
		if err != nil || promoted == nil || promoted.AccessToken != "secret-b-"+prov {
			t.Fatalf("promocao automatica falhou em %s: %+v, err=%v", prov, promoted, err)
		}
	}

	raw, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-b-openrouter") {
		t.Fatal("token OAuth gravado em texto puro no arquivo")
	}
	info, err := os.Stat(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissao = %o, esperado 600", info.Mode().Perm())
	}
}

func TestOpenRouterPKCEFlowAndCLI(t *testing.T) {
	var capturedChallenge string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/keys":
			var req struct {
				Code         string `json:"code"`
				CodeVerifier string `json:"code_verifier"`
				Method       string `json:"code_challenge_method"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			sum := sha256.Sum256([]byte(req.CodeVerifier))
			gotChallenge := base64.RawURLEncoding.EncodeToString(sum[:])
			if req.Code != "or-code-123" || gotChallenge != capturedChallenge || req.Method != "S256" {
				http.Error(w, "pkce mismatch", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"key":     "sk-or-oauth-session-token",
				"user_id": "user_or_42",
			})
		case "/api/v1/models":
			if r.Header.Get("Authorization") != "Bearer sk-or-oauth-session-token" {
				http.Error(w, "missing bearer", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"id": "anthropic/claude-sonnet-4"}, {"id": "google/gemini-2.5-pro"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	store := NewStoreWithChatGPT(filepath.Join(dir, "oauth.enc"), filepath.Join(dir, "cg.enc"))
	mgr := NewManager(store)
	mgr.SetHTTPClient(srv.Client())
	mgr.OpenRouterAuthURL = srv.URL + "/auth"
	mgr.OpenRouterKeysURL = srv.URL + "/api/v1/auth/keys"
	mgr.SetBrowserOpener(func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		capturedChallenge = u.Query().Get("code_challenge")
		cbURL := u.Query().Get("callback_url")
		go func() {
			resp, err := http.Get(cbURL + "?code=or-code-123")
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cred, err := mgr.Login(ctx, "openrouter", LoginOptions{
		AccountID: "pessoal",
		BaseURL:   srv.URL + "/api/v1",
	})
	if err != nil {
		t.Fatalf("OpenRouter Login: %v", err)
	}
	if cred.AccountID != "pessoal" || cred.AccessToken != "sk-or-oauth-session-token" {
		t.Fatalf("credencial inesperada: %+v", cred)
	}

	var out, errBuf bytes.Buffer
	if err := RunCLI(ctx, mgr, []string{"openrouter", "models"}, &out, &errBuf); err != nil {
		t.Fatalf("RunCLI models: %v (%s)", err, errBuf.String())
	}
	if !strings.Contains(out.String(), "anthropic/claude-sonnet-4") {
		t.Fatalf("saida de modelos inesperada: %s", out.String())
	}
}

func TestGooglePKCERefreshAndADCImport(t *testing.T) {
	var refreshCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_ = r.ParseForm()
			switch r.Form.Get("grant_type") {
			case "authorization_code":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token":  "ya29.initial",
					"refresh_token": "1//refresh-google",
					"expires_in":    1, // force near-immediate refresh window
					"token_type":    "Bearer",
				})
			case "refresh_token":
				atomic.AddInt32(&refreshCalls, 1)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token": "ya29.refreshed-token",
					"expires_in":   3600,
					"token_type":   "Bearer",
				})
			default:
				http.Error(w, "unsupported grant", http.StatusBadRequest)
			}
		case "/userinfo":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"email": "gemini.dev@example.com",
				"name":  "Gemini Dev",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	clientFile := filepath.Join(dir, "client_secret.json")
	clientDoc := fmt.Sprintf(`{"installed":{"client_id":"gid-123.apps.googleusercontent.com","client_secret":"gsec","auth_uri":%q,"token_uri":%q}}`,
		srv.URL+"/auth", srv.URL+"/token")
	if err := os.WriteFile(clientFile, []byte(clientDoc), 0o600); err != nil {
		t.Fatal(err)
	}

	store := NewStoreWithChatGPT(filepath.Join(dir, "oauth.enc"), filepath.Join(dir, "cg.enc"))
	mgr := NewManager(store)
	mgr.SetHTTPClient(srv.Client())
	mgr.GoogleUserInfoURL = srv.URL + "/userinfo"
	mgr.SetBrowserOpener(func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		redir := u.Query().Get("redirect_uri")
		st := u.Query().Get("state")
		go func() {
			resp, err := http.Get(redir + "?code=google-code&state=" + url.QueryEscape(st))
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cred, err := mgr.Login(ctx, "google", LoginOptions{ClientIDFile: clientFile})
	if err != nil {
		t.Fatalf("Google PKCE Login: %v", err)
	}
	if cred.Email != "gemini.dev@example.com" {
		t.Fatalf("email Google = %q", cred.Email)
	}

	// AccessToken must automatically rotate using refresh_token because expires_in=1 is inside the 5-min window!
	tok, err := mgr.AccessToken(ctx, "google")
	if err != nil {
		t.Fatalf("AccessToken refresh: %v", err)
	}
	if tok != "ya29.refreshed-token" || atomic.LoadInt32(&refreshCalls) == 0 {
		t.Fatalf("esperava token renovado ya29.refreshed-token, obteve %q (calls=%d)", tok, refreshCalls)
	}

	// Test importing a second Google account via Application Default Credentials (--adc)
	adcFile := filepath.Join(dir, "adc.json")
	if err := os.WriteFile(adcFile, []byte(`{"type":"authorized_user","client_id":"gcloud-cid","client_secret":"gcloud-sec","refresh_token":"1//adc-refresh"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", adcFile)
	adcCred, err := mgr.Login(ctx, "google", LoginOptions{
		AccountID: "cloud-adc",
		UseADC:    true,
		TokenURL:  srv.URL + "/token",
	})
	if err != nil {
		t.Fatalf("Google ADC import: %v", err)
	}
	if adcCred.AccountID != "cloud-adc" {
		t.Fatalf("adcCred.AccountID = %q", adcCred.AccountID)
	}
	accounts, _ := store.ListAccounts("google")
	if len(accounts) != 2 {
		t.Fatalf("esperava 2 contas Google salvas, obteve %d", len(accounts))
	}
}

func fmtJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestGitHubDeviceFlowAndQwenImportAndAnthropicDCR(t *testing.T) {
	var pollCount int32
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		// GitHub RFC 8628 Device Flow
		case "/github/device/code":
			fmtJSON(w, map[string]any{
				"device_code":      "gh-dev-code",
				"user_code":        "ABCD-1234",
				"verification_uri": srvURL + "/verify",
				"expires_in":       60,
				"interval":         1,
			})
		case "/github/oauth/access_token":
			if atomic.AddInt32(&pollCount, 1) == 1 {
				fmtJSON(w, map[string]string{"error": "authorization_pending"})
				return
			}
			fmtJSON(w, map[string]any{
				"access_token": "ghu_device_oauth_token",
				"scope":        "read:user user:email",
			})
		case "/github/user":
			fmtJSON(w, map[string]string{
				"login": "octocat-dev",
				"email": "octocat@github.test",
			})

		// Anthropic OAuth 2.1 Discovery (RFC 8414) + Dynamic Client Registration (RFC 7591) + PKCE
		case "/.well-known/oauth-authorization-server":
			fmtJSON(w, map[string]string{
				"issuer":                 srvURL,
				"authorization_endpoint": srvURL + "/anthropic/authorize",
				"token_endpoint":         srvURL + "/anthropic/token",
				"registration_endpoint":  srvURL + "/anthropic/register",
			})
		case "/anthropic/register":
			w.WriteHeader(http.StatusCreated)
			fmtJSON(w, map[string]string{
				"client_id": "dcr-claude-client-01",
			})
		case "/anthropic/token":
			fmtJSON(w, map[string]any{
				"access_token":  "sk-ant-oauth-dcr-token",
				"refresh_token": "ant-refresh-01",
				"expires_in":    3600,
				"token_type":    "Bearer",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	dir := t.TempDir()
	store := NewStoreWithChatGPT(filepath.Join(dir, "oauth.enc"), filepath.Join(dir, "cg.enc"))
	mgr := NewManager(store)
	mgr.SetHTTPClient(srv.Client())
	mgr.GitHubDeviceURL = srv.URL + "/github/device/code"
	mgr.GitHubTokenURL = srv.URL + "/github/oauth/access_token"
	mgr.GitHubUserURL = srv.URL + "/github/user"
	mgr.SetBrowserOpener(func(target string) error {
		u, err := url.Parse(target)
		if err != nil {
			return nil
		}
		if redir := u.Query().Get("redirect_uri"); redir != "" {
			st := u.Query().Get("state")
			go func() {
				resp, err := http.Get(redir + "?code=ant-code-99&state=" + url.QueryEscape(st))
				if err == nil {
					resp.Body.Close()
				}
			}()
		}
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. GitHub Device Flow
	ghCred, err := mgr.Login(ctx, "github", LoginOptions{ClientID: "Iv1.githubtest"})
	if err != nil {
		t.Fatalf("GitHub Device Flow: %v", err)
	}
	if ghCred.AccountID != "octocat-dev" || ghCred.AccessToken != "ghu_device_oauth_token" {
		t.Fatalf("ghCred inesperado: %+v", ghCred)
	}

	// 2. Qwen ~/.qwen/oauth_creds.json import
	fakeHome := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(fakeHome, ".qwen"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeHome, ".qwen", "oauth_creds.json"),
		[]byte(`{"access_token":"qwen-oauth-tok","refresh_token":"qwen-ref","resource_url":"portal.qwen.ai","expiry_date":4102444800000}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", fakeHome)
	qwenCred, err := mgr.Login(ctx, "qwen", LoginOptions{AccountID: "alibaba-dev"})
	if err != nil {
		t.Fatalf("Qwen import: %v", err)
	}
	if qwenCred.AccessToken != "qwen-oauth-tok" || qwenCred.BaseURL != "https://portal.qwen.ai/v1" {
		t.Fatalf("qwenCred inesperado: %+v", qwenCred)
	}

	// 3. Anthropic OAuth 2.1 Discovery + DCR + PKCE
	antCred, err := mgr.Login(ctx, "anthropic", LoginOptions{
		AccountID: "claude-team",
		IssuerURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("Anthropic DCR+PKCE: %v", err)
	}
	if antCred.ClientID != "dcr-claude-client-01" || antCred.AccessToken != "sk-ant-oauth-dcr-token" {
		t.Fatalf("antCred inesperado: %+v", antCred)
	}

	// Verify CLI overview lists all 3 connected providers without leaking any token
	overview := FormatOverview(mgr)
	for _, want := range []string{"octocat-dev", "alibaba-dev", "claude-team", "3/6 provedores OAuth conectados"} {
		if !strings.Contains(overview, want) {
			t.Fatalf("FormatOverview nao contem %q:\n%s", want, overview)
		}
	}
	for _, secret := range []string{"ghu_device_oauth_token", "qwen-oauth-tok", "sk-ant-oauth-dcr-token"} {
		if strings.Contains(overview, secret) {
			t.Fatalf("FormatOverview vazou segredo %q!", secret)
		}
	}
}
