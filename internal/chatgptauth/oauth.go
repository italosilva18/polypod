package chatgptauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	issuerURL       = "https://auth.openai.com"
	authorizeURL    = issuerURL + "/api/accounts/authorize"
	tokenURL        = issuerURL + "/api/accounts/oauth/token"
	jwksURL         = issuerURL + "/.well-known/jwks.json"
	resourceURL     = "https://api.openai.com/v1"
	dynamicClientID = "dynamic_agent_client"
	requiredScope   = "chatgpt.tokens.use.direct"
	agentName       = "Polypod"
	scopes          = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"
)

type Manager struct {
	store      *Store
	httpClient *http.Client
	mu         sync.Mutex
}

type Model struct {
	ID         string `json:"id"`
	Slug       string `json:"slug"`
	Visibility string `json:"visibility"`
}

func (m Model) Name() string {
	if m.Slug != "" {
		return m.Slug
	}
	return m.ID
}

func NewManager(path string) *Manager {
	return &Manager{store: NewStore(path), httpClient: &http.Client{Timeout: 30 * time.Second}}
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	Description  string `json:"error_description"`
}

type idClaims struct {
	Email string `json:"email"`
	Nonce string `json:"nonce"`
	jwt.RegisteredClaims
}

type callbackResult struct {
	code     string
	state    string
	clientID string
	err      error
}

func randomURLSafe(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (m *Manager) Login(ctx context.Context) (*Credential, error) {
	registrationClientID := dynamicClientID
	if existing, loadErr := m.store.Load(); loadErr == nil && existing != nil && strings.HasPrefix(existing.ClientID, "oaiapp_") {
		registrationClientID = existing.ClientID
	}
	hostID, err := m.store.HostID()
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if hostID == "" {
		hostID = "urn:uuid:" + uuid.NewString()
		if err := m.store.SaveHostID(hostID); err != nil {
			return nil, err
		}
	}

	state, err := randomURLSafe(32)
	if err != nil {
		return nil, err
	}
	nonce, err := randomURLSafe(32)
	if err != nil {
		return nil, err
	}
	verifier, err := randomURLSafe(64)
	if err != nil {
		return nil, err
	}
	challengeHash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeHash[:])

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("abrindo callback local: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/auth/callback", port)
	resultCh := make(chan callbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		res := callbackResult{
			code:     q.Get("code"),
			state:    q.Get("state"),
			clientID: q.Get("client_id"),
		}
		if oauthErr := q.Get("error"); oauthErr != "" {
			res.err = fmt.Errorf("%s: %s", oauthErr, q.Get("error_description"))
		}
		if res.code == "" && res.err == nil {
			res.err = fmt.Errorf("callback sem codigo de autorizacao")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if res.err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "<h2>Falha na conexao</h2><p>%s</p>", html.EscapeString(res.err.Error()))
		} else {
			fmt.Fprint(w, "<h2>ChatGPT conectado ao Polypod</h2><p>Voce pode fechar esta janela.</p>")
		}
		select {
		case resultCh <- res:
		default:
		}
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {registrationClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {scopes},
		"resource":              {resourceURL},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
		"nonce":                 {nonce},
		"agent_name_hint":       {agentName},
		"ext_agent_host_id":     {hostID},
	}
	loginURL := authorizeURL + "?" + params.Encode()
	if err := openBrowser(loginURL); err != nil {
		return nil, fmt.Errorf("abrindo navegador: %w", err)
	}
	fmt.Println("Navegador aberto. Autorize o Polypod na sua conta ChatGPT...")

	var result callbackResult
	select {
	case result = <-resultCh:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(10 * time.Minute):
		return nil, fmt.Errorf("tempo de login expirado")
	}
	if result.err != nil {
		return nil, result.err
	}
	if result.state != state {
		return nil, fmt.Errorf("estado OAuth invalido")
	}
	if result.clientID == "" && strings.HasPrefix(registrationClientID, "oaiapp_") {
		result.clientID = registrationClientID
	}
	if !strings.HasPrefix(result.clientID, "oaiapp_") {
		return nil, fmt.Errorf("cliente OAuth emitido ausente ou invalido")
	}

	tr, err := m.exchange(ctx, result.clientID, result.code, verifier, redirectURI)
	if err != nil {
		return nil, err
	}
	claims, err := m.validateIDToken(ctx, tr.IDToken, result.clientID, nonce)
	if err != nil {
		return nil, err
	}
	granted := strings.Fields(tr.Scope)
	if !hasScope(granted, requiredScope) {
		return nil, fmt.Errorf("a conta nao concedeu o escopo obrigatorio %q", requiredScope)
	}
	cred := &Credential{
		Email: claims.Email, Issuer: claims.Issuer, Subject: claims.Subject,
		ClientID: result.clientID, HostID: hostID, IDToken: tr.IDToken,
		AccessToken: tr.AccessToken, RefreshToken: tr.RefreshToken, Scopes: granted,
		ExpiresAt: time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
	}
	if err := m.store.Save(cred); err != nil {
		return nil, err
	}
	return cred, nil
}

func (m *Manager) exchange(ctx context.Context, clientID, code, verifier, redirectURI string) (*tokenResponse, error) {
	values := url.Values{
		"grant_type": {"authorization_code"}, "client_id": {clientID},
		"code": {code}, "code_verifier": {verifier}, "redirect_uri": {redirectURI},
		"resource": {resourceURL},
	}
	return m.postToken(ctx, values)
}

func (m *Manager) postToken(ctx context.Context, values url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token OAuth ChatGPT: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("resposta OAuth invalida (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || tr.Error != "" {
		return nil, fmt.Errorf("OAuth ChatGPT recusado (HTTP %d): %s %s", resp.StatusCode, tr.Error, tr.Description)
	}
	if tr.AccessToken == "" {
		return nil, fmt.Errorf("OAuth ChatGPT retornou token vazio")
	}
	return &tr, nil
}

func (m *Manager) Refresh(ctx context.Context, cred *Credential) (*Credential, error) {
	if cred == nil || cred.RefreshToken == "" {
		return nil, fmt.Errorf("sessao ChatGPT sem refresh token; conecte novamente")
	}
	tr, err := m.postToken(ctx, url.Values{
		"grant_type": {"refresh_token"}, "client_id": {cred.ClientID},
		"refresh_token": {cred.RefreshToken}, "resource": {resourceURL},
	})
	if err != nil {
		return nil, err
	}
	cred.AccessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		cred.RefreshToken = tr.RefreshToken
	}
	if tr.IDToken != "" {
		cred.IDToken = tr.IDToken
	}
	if tr.Scope != "" {
		cred.Scopes = strings.Fields(tr.Scope)
	}
	cred.ExpiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	if err := m.store.Save(cred); err != nil {
		return nil, err
	}
	return cred, nil
}

func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cred, err := m.store.Load()
	if err != nil {
		return "", err
	}
	if cred == nil {
		return "", fmt.Errorf("ChatGPT nao conectado; execute: polypod auth chatgpt login")
	}
	if cred.NeedsRefresh() {
		cred, err = m.Refresh(ctx, cred)
		if err != nil {
			return "", err
		}
	}
	return cred.AccessToken, nil
}

func (m *Manager) Status() (*Credential, error) { return m.store.Load() }
func (m *Manager) Logout() error                { return m.store.Delete() }

func (m *Manager) Models(ctx context.Context) ([]Model, error) {
	token, err := m.AccessToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listando modelos ChatGPT: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lista de modelos ChatGPT: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Models []Model `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return nil, err
	}
	visible := body.Models[:0]
	for _, model := range body.Models {
		if model.Visibility == "" || model.Visibility == "list" {
			visible = append(visible, model)
		}
	}
	return visible, nil
}

func hasScope(scopes []string, wanted string) bool {
	for _, scope := range scopes {
		if scope == wanted {
			return true
		}
	}
	return false
}

type jwks struct {
	Keys []struct {
		Kty string `json:"kty"`
		Kid string `json:"kid"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

func (m *Manager) validateIDToken(ctx context.Context, raw, audience, nonce string) (*idClaims, error) {
	if raw == "" {
		return nil, fmt.Errorf("OAuth ChatGPT nao retornou ID token")
	}
	claims := &idClaims{}
	parsed, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodRS256.Alg() {
			return nil, fmt.Errorf("algoritmo ID token inesperado: %s", token.Method.Alg())
		}
		kid, _ := token.Header["kid"].(string)
		return m.fetchRSAKey(ctx, kid)
	}, jwt.WithIssuer(issuerURL), jwt.WithAudience(audience), jwt.WithExpirationRequired())
	if err != nil || !parsed.Valid {
		return nil, fmt.Errorf("ID token ChatGPT invalido: %w", err)
	}
	if claims.Nonce != nonce {
		return nil, fmt.Errorf("nonce do ID token invalido")
	}
	return claims, nil
}

func (m *Manager) fetchRSAKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS ChatGPT HTTP %d", resp.StatusCode)
	}
	var set jwks
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return nil, err
	}
	for _, key := range set.Keys {
		if key.Kid != kid || key.Kty != "RSA" {
			continue
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(key.N)
		if err != nil {
			return nil, err
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(key.E)
		if err != nil {
			return nil, err
		}
		e := new(big.Int).SetBytes(eBytes)
		return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(e.Int64())}, nil
	}
	return nil, fmt.Errorf("chave JWKS %q nao encontrada", kid)
}

func openBrowser(target string) error {
	if os.Geteuid() == 0 {
		name := os.Getenv("POLYPOD_DESKTOP_USER")
		if name == "" {
			name = "costa"
		}
		return exec.Command("/usr/sbin/runuser", "-u", name, "--", "/usr/bin/xdg-open", target).Start()
	}
	return exec.Command("xdg-open", target).Start()
}
