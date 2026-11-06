package auth

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/securestorage"
)

// ─── Auth Provider Types ────────────────────────────────────────────────────

// AuthProvider identifies the authentication mechanism in use.
type AuthProvider string

const (
	AuthProviderAPIKey AuthProvider = "api_key"
	AuthProviderOAuth  AuthProvider = "oauth"
	AuthProviderEnvVar AuthProvider = "env_var"
)

// Credentials holds authentication material for an AI provider.
type Credentials struct {
	Provider     AuthProvider `json:"provider"`
	APIKey       string       `json:"api_key,omitempty"`
	OAuthToken   string       `json:"oauth_token,omitempty"`
	RefreshToken string       `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time    `json:"expires_at,omitempty"`
	Scopes       []string     `json:"scopes,omitempty"`
}

// IsExpired returns true if the credentials have an expiration and it has passed.
func (c *Credentials) IsExpired() bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(c.ExpiresAt)
}

// Token returns the active bearer token: OAuthToken if set, otherwise APIKey.
func (c *Credentials) Token() string {
	if c.OAuthToken != "" {
		return c.OAuthToken
	}
	return c.APIKey
}

// ─── Known env var → provider mapping ───────────────────────────────────────

// EnvVarProviderMap maps environment variable names to provider names.
var EnvVarProviderMap = map[string]string{
	"ANTHROPIC_API_KEY":  "anthropic",
	"OPENAI_API_KEY":     "openai",
	"GOOGLE_API_KEY":     "google",
	"DEEPSEEK_API_KEY":   "deepseek",
	"GROQ_API_KEY":       "groq",
	"MISTRAL_API_KEY":    "mistral",
	"COHERE_API_KEY":     "cohere",
	"TOGETHER_API_KEY":   "together",
	"PERPLEXITY_API_KEY": "perplexity",
	"XAI_API_KEY":        "xai",
}

// ProviderEnvVarMap is the reverse: provider → env var name.
var ProviderEnvVarMap = map[string]string{
	"anthropic":  "ANTHROPIC_API_KEY",
	"openai":     "OPENAI_API_KEY",
	"google":     "GOOGLE_API_KEY",
	"deepseek":   "DEEPSEEK_API_KEY",
	"groq":       "GROQ_API_KEY",
	"mistral":    "MISTRAL_API_KEY",
	"cohere":     "COHERE_API_KEY",
	"together":   "TOGETHER_API_KEY",
	"perplexity": "PERPLEXITY_API_KEY",
	"xai":        "XAI_API_KEY",
}

// ─── KeyStore ───────────────────────────────────────────────────────────────

// KeyStore provides secure API key storage using the platform keychain
// with an encrypted file fallback (0600 permissions).
type KeyStore struct {
	mu      sync.RWMutex
	storage securestorage.SecureStorage
	dataDir string
}

// NewKeyStore creates a KeyStore rooted at dataDir.
// It attempts to use the OS keychain first, falling back to an AES-encrypted
// JSON file at <dataDir>/credentials.enc.
func NewKeyStore(dataDir string) *KeyStore {
	primary := securestorage.NewKeychainStorage("polypod")

	credPath := filepath.Join(dataDir, "credentials.enc")
	secondary := securestorage.NewFileStorage(credPath, "")

	storage := &securestorage.FallbackStorage{
		Primary:   primary,
		Secondary: secondary,
	}

	return &KeyStore{
		storage: storage,
		dataDir: dataDir,
	}
}

// SetKey stores an API key for the named provider.
func (ks *KeyStore) SetKey(provider, key string) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	return ks.storage.Set("apikey:"+provider, key)
}

// GetKey retrieves the API key for the named provider.
// Returns ("", nil) if no key is stored.
func (ks *KeyStore) GetKey(provider string) (string, error) {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return ks.storage.Get("apikey:" + provider)
}

// DeleteKey removes the stored key for a provider.
func (ks *KeyStore) DeleteKey(provider string) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	return ks.storage.Delete("apikey:" + provider)
}

// ListProviders returns the names of providers that have stored keys.
func (ks *KeyStore) ListProviders() []string {
	ks.mu.RLock()
	defer ks.mu.RUnlock()

	keys, err := ks.storage.List()
	if err != nil {
		return nil
	}

	var providers []string
	for _, k := range keys {
		if strings.HasPrefix(k, "apikey:") {
			providers = append(providers, strings.TrimPrefix(k, "apikey:"))
		}
	}
	return providers
}

// ─── MultiProviderAuth ──────────────────────────────────────────────────────

// MultiProviderAuth manages authentication credentials for multiple AI providers.
// It checks the KeyStore first, then falls back to environment variables.
type MultiProviderAuth struct {
	mu       sync.RWMutex
	keyStore *KeyStore
	cache    map[string]*Credentials // in-memory cache
}

// NewMultiProviderAuth creates a new multi-provider authenticator.
func NewMultiProviderAuth(keyStore *KeyStore) *MultiProviderAuth {
	return &MultiProviderAuth{
		keyStore: keyStore,
		cache:    make(map[string]*Credentials),
	}
}

// GetCredentials returns credentials for the named provider.
// Resolution order: in-memory cache → KeyStore → environment variable.
func (m *MultiProviderAuth) GetCredentials(provider string) (*Credentials, error) {
	m.mu.RLock()
	if cred, ok := m.cache[provider]; ok {
		m.mu.RUnlock()
		if cred.IsExpired() {
			return nil, fmt.Errorf("credentials for %q have expired", provider)
		}
		return cred, nil
	}
	m.mu.RUnlock()

	// Try KeyStore
	key, err := m.keyStore.GetKey(provider)
	if err == nil && key != "" {
		cred := &Credentials{
			Provider: AuthProviderAPIKey,
			APIKey:   key,
		}
		m.mu.Lock()
		m.cache[provider] = cred
		m.mu.Unlock()
		return cred, nil
	}

	// Try environment variable
	if envVar, ok := ProviderEnvVarMap[provider]; ok {
		if val := os.Getenv(envVar); val != "" {
			cred := &Credentials{
				Provider: AuthProviderEnvVar,
				APIKey:   val,
			}
			m.mu.Lock()
			m.cache[provider] = cred
			m.mu.Unlock()
			return cred, nil
		}
	}

	return nil, fmt.Errorf("no credentials found for provider %q", provider)
}

// SetCredentials stores credentials for a provider in both cache and KeyStore.
func (m *MultiProviderAuth) SetCredentials(provider string, creds Credentials) error {
	m.mu.Lock()
	m.cache[provider] = &creds
	m.mu.Unlock()

	// Persist the API key (or OAuth token) to the KeyStore
	token := creds.Token()
	if token != "" {
		return m.keyStore.SetKey(provider, token)
	}
	return nil
}

// DeleteCredentials removes credentials for a provider from cache and KeyStore.
func (m *MultiProviderAuth) DeleteCredentials(provider string) error {
	m.mu.Lock()
	delete(m.cache, provider)
	m.mu.Unlock()
	return m.keyStore.DeleteKey(provider)
}

// ValidateCredentials tests the key for a provider by sending a minimal
// chat completion request to the provider's API.
// Returns nil on success, an error describing the failure otherwise.
func (m *MultiProviderAuth) ValidateCredentials(provider string) error {
	cred, err := m.GetCredentials(provider)
	if err != nil {
		return err
	}

	baseURL := providerBaseURL(provider)
	if baseURL == "" {
		return fmt.Errorf("unknown base URL for provider %q; cannot validate", provider)
	}

	url := strings.TrimRight(baseURL, "/") + "/chat/completions"
	model := providerDefaultModel(provider)
	body := `{"model":"` + model + `","messages":[{"role":"user","content":"hi"}],"max_tokens":1}`

	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("building validation request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cred.Token())
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("validation request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("API key for %q is invalid (HTTP %d)", provider, resp.StatusCode)
	}

	return nil
}

// AutoDetect checks environment variables and the KeyStore, returning a map
// of provider → available (true if credentials exist).
func (m *MultiProviderAuth) AutoDetect() map[string]bool {
	result := make(map[string]bool)

	// Check env vars
	for envVar, prov := range EnvVarProviderMap {
		if os.Getenv(envVar) != "" {
			result[prov] = true
		}
	}

	// Check KeyStore
	for _, prov := range m.keyStore.ListProviders() {
		result[prov] = true
	}

	return result
}

// ClearCache invalidates the in-memory credential cache.
func (m *MultiProviderAuth) ClearCache() {
	m.mu.Lock()
	m.cache = make(map[string]*Credentials)
	m.mu.Unlock()
}

// ─── API Key Setup Wizard ───────────────────────────────────────────────────

// ANSI color codes for terminal output.
const (
	cW = "\033[97m" // bright white
	cB = "\033[1m"  // bold
	cR = "\033[0m"  // reset
	cG = "\033[32m" // green
	cY = "\033[33m" // yellow
)

// RunKeySetup runs an interactive wizard that detects existing env vars,
// prompts for missing keys, validates them, and stores them securely.
func RunKeySetup(store *KeyStore) error {
	scanner := bufio.NewScanner(os.Stdin)
	multiAuth := NewMultiProviderAuth(store)

	fmt.Println()
	fmt.Println(cB + cW + "==========================================================" + cR)
	fmt.Println(cB + cW + "  Polypod — API Key Setup" + cR)
	fmt.Println(cB + cW + "==========================================================" + cR)
	fmt.Println()

	// Auto-detect existing credentials
	detected := multiAuth.AutoDetect()
	if len(detected) > 0 {
		fmt.Println(cW + "Detected existing credentials:" + cR)
		for prov := range detected {
			fmt.Printf("  %s%s%s %s(via env or keystore)%s\n", cG, prov, cR, cY, cR)
		}
		fmt.Println()
	}

	// Offer to configure key providers
	knownProviders := []struct {
		Name   string
		EnvVar string
	}{
		{"anthropic", "ANTHROPIC_API_KEY"},
		{"openai", "OPENAI_API_KEY"},
		{"deepseek", "DEEPSEEK_API_KEY"},
		{"google", "GOOGLE_API_KEY"},
		{"groq", "GROQ_API_KEY"},
	}

	for _, p := range knownProviders {
		if detected[p.Name] {
			continue // already has a key
		}

		fmt.Printf("%sConfigure %s? (s/N): %s", cW, p.Name, cR)
		if !scanner.Scan() {
			break
		}
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if answer != "s" && answer != "sim" && answer != "y" && answer != "yes" {
			continue
		}

		fmt.Printf("%sAPI key for %s: %s", cW, p.Name, cR)
		if !scanner.Scan() {
			break
		}
		key := strings.TrimSpace(scanner.Text())
		if key == "" {
			fmt.Println(cY + "  Skipped (empty key)." + cR)
			continue
		}

		// Store the key
		if err := store.SetKey(p.Name, key); err != nil {
			fmt.Printf("  %sError storing key: %v%s\n", cY, err, cR)
			continue
		}

		// Validate
		cred := Credentials{Provider: AuthProviderAPIKey, APIKey: key}
		multiAuth.SetCredentials(p.Name, cred)

		fmt.Printf("  %sValidating...%s ", cW, cR)
		if err := multiAuth.ValidateCredentials(p.Name); err != nil {
			fmt.Printf("%sfailed (%v) — stored anyway%s\n", cY, err, cR)
		} else {
			fmt.Printf("%sOK%s\n", cG, cR)
		}
	}

	fmt.Println()
	fmt.Println(cW + "Key setup complete." + cR)
	fmt.Println()
	return nil
}

// ─── Provider metadata helpers ──────────────────────────────────────────────

func providerBaseURL(provider string) string {
	m := map[string]string{
		"deepseek":   "https://api.deepseek.com/v1",
		"openai":     "https://api.openai.com/v1",
		"anthropic":  "https://api.anthropic.com/v1",
		"google":     "https://generativelanguage.googleapis.com/v1beta",
		"groq":       "https://api.groq.com/openai/v1",
		"mistral":    "https://api.mistral.ai/v1",
		"cohere":     "https://api.cohere.com/compatibility/v1",
		"together":   "https://api.together.xyz/v1",
		"perplexity": "https://api.perplexity.ai",
		"xai":        "https://api.x.ai/v1",
	}
	return m[provider]
}

func providerDefaultModel(provider string) string {
	m := map[string]string{
		"deepseek":   "deepseek-chat",
		"openai":     "gpt-4o-mini",
		"anthropic":  "claude-sonnet-4-20250514",
		"google":     "gemini-1.5-flash",
		"groq":       "llama-3.1-8b-instant",
		"mistral":    "mistral-small-latest",
		"cohere":     "command-r",
		"together":   "meta-llama/Llama-3-8b-chat-hf",
		"perplexity": "sonar-small-chat",
		"xai":        "grok-beta",
	}
	if v, ok := m[provider]; ok {
		return v
	}
	return "default"
}
