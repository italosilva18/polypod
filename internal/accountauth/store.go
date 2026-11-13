package accountauth

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/costa/polypod/internal/chatgptauth"
	"github.com/costa/polypod/internal/securestorage"
)

// SupportedProviders lists every provider that supports account-based OAuth in Polypod.
func SupportedProviders() []string {
	return []string{"chatgpt", "openrouter", "google", "github", "qwen", "anthropic"}
}

// IsOAuthProvider reports whether name is one of the 6 OAuth-capable account providers.
func IsOAuthProvider(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, p := range SupportedProviders() {
		if p == name {
			return true
		}
	}
	return false
}

// DefaultModelFor returns a sensible default model when activating an OAuth-connected provider.
func DefaultModelFor(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "chatgpt":
		return "gpt-6.1-sol"
	case "openrouter":
		return "anthropic/claude-sonnet-4"
	case "google":
		return "gemini-2.5-pro"
	case "github":
		return "gpt-4o"
	case "qwen":
		return "qwen3-coder-plus"
	case "anthropic":
		return "claude-sonnet-4-20250514"
	default:
		return ""
	}
}

// DefaultStorePath resolves the encrypted multi-provider OAuth store path.
// By default it sits next to POLYPOD_CHATGPT_AUTH_FILE so interactive CLI and
// service processes share the same 0600 encrypted store.
func DefaultStorePath() string {
	if p := strings.TrimSpace(os.Getenv("POLYPOD_OAUTH_STORE_FILE")); p != "" {
		return p
	}
	chatPath := chatgptauth.DefaultCredentialPath()
	return filepath.Join(filepath.Dir(chatPath), "oauth-accounts.enc")
}

// Credential stores one OAuth account session encrypted at rest.
type Credential struct {
	Provider     string    `json:"provider"`
	AccountID    string    `json:"account_id"`
	Email        string    `json:"email,omitempty"`
	DisplayName  string    `json:"display_name,omitempty"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	IDToken      string    `json:"id_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	ClientID     string    `json:"client_id,omitempty"`
	ClientSecret string    `json:"client_secret,omitempty"`
	TokenURL     string    `json:"token_url,omitempty"`
	Issuer       string    `json:"issuer,omitempty"`
	BaseURL      string    `json:"base_url,omitempty"`
	Scopes       []string  `json:"scopes,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	SavedAt      time.Time `json:"saved_at"`
}

func (c *Credential) NeedsRefresh() bool {
	if c == nil || c.AccessToken == "" {
		return true
	}
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().Add(5 * time.Minute).After(c.ExpiresAt)
}

// AccountSummary is a safe, token-free summary of a stored OAuth account.
type AccountSummary struct {
	Provider    string    `json:"provider"`
	ID          string    `json:"id"`
	Email       string    `json:"email,omitempty"`
	DisplayName string    `json:"display_name,omitempty"`
	BaseURL     string    `json:"base_url,omitempty"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
	SavedAt     time.Time `json:"saved_at,omitempty"`
	Active      bool      `json:"active"`
}

// Store persists encrypted multi-account OAuth credentials for non-ChatGPT providers
// (while delegating ChatGPT account storage to chatgptauth.Store for backward compatibility).
type Store struct {
	path        string
	chatGPTPath string
	storage     securestorage.SecureStorage
}

func NewStore(path string) *Store {
	if strings.TrimSpace(path) == "" {
		path = DefaultStorePath()
	}
	return &Store{
		path:        path,
		chatGPTPath: chatgptauth.DefaultCredentialPath(),
		storage:     securestorage.NewFileStorage(path, ""),
	}
}

func NewStoreWithChatGPT(path, chatGPTPath string) *Store {
	if strings.TrimSpace(path) == "" {
		path = DefaultStorePath()
	}
	if strings.TrimSpace(chatGPTPath) == "" {
		chatGPTPath = chatgptauth.DefaultCredentialPath()
	}
	return &Store{
		path:        path,
		chatGPTPath: chatGPTPath,
		storage:     securestorage.NewFileStorage(path, ""),
	}
}

func indexKey(provider string) string  { return "oauth:" + provider + ":index" }
func activeKey(provider string) string { return "oauth:" + provider + ":active" }
func credKey(provider, accountID string) string {
	return "oauth:" + provider + ":account:" + accountID
}

func normalizeID(accountID, email, display string) string {
	for _, candidate := range []string{accountID, email, display} {
		v := strings.TrimSpace(candidate)
		if v != "" {
			return v
		}
	}
	return "default"
}

func (s *Store) loadIndex(provider string) ([]string, error) {
	raw, err := s.storage.Get(indexKey(provider))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *Store) saveIndex(provider string, ids []string) error {
	seen := make(map[string]bool, len(ids))
	clean := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		clean = append(clean, id)
	}
	sort.Strings(clean)
	raw, err := json.Marshal(clean)
	if err != nil {
		return err
	}
	return s.storage.Set(indexKey(provider), string(raw))
}

func (s *Store) ActiveAccountID(provider string) (string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "chatgpt" {
		return chatgptauth.NewStore(s.chatGPTPath).ActiveAccountID()
	}
	id, err := s.storage.Get(activeKey(provider))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(id), nil
}

func (s *Store) loadCred(provider, accountID string) (*Credential, error) {
	raw, err := s.storage.Get(credKey(provider, accountID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	var c Credential
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, fmt.Errorf("decodificando credencial OAuth %s: %w", provider, err)
	}
	return &c, nil
}

func (s *Store) SaveAccount(provider, accountID string, cred *Credential) error {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if cred == nil {
		return fmt.Errorf("credencial OAuth vazia")
	}
	if provider == "" {
		provider = strings.ToLower(strings.TrimSpace(cred.Provider))
	}
	if provider == "" {
		return fmt.Errorf("provedor OAuth obrigatorio")
	}
	id := normalizeID(accountID, cred.Email, cred.DisplayName)
	cred.Provider = provider
	cred.AccountID = id
	cred.SavedAt = time.Now().UTC()

	raw, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	if err := s.storage.Set(credKey(provider, id), string(raw)); err != nil {
		return err
	}
	if err := s.storage.Set(activeKey(provider), id); err != nil {
		return err
	}
	ids, _ := s.loadIndex(provider)
	ids = append(ids, id)
	if err := s.saveIndex(provider, ids); err != nil {
		return err
	}
	return s.normalizeOwner()
}

func (s *Store) LoadActive(provider string) (*Credential, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "chatgpt" {
		cg, err := chatgptauth.NewStore(s.chatGPTPath).Load()
		if err != nil || cg == nil {
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
	}
	activeID, err := s.ActiveAccountID(provider)
	if err != nil {
		return nil, err
	}
	if activeID != "" {
		c, loadErr := s.loadCred(provider, activeID)
		if loadErr != nil || c != nil {
			return c, loadErr
		}
	}
	ids, err := s.loadIndex(provider)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	return s.loadCred(provider, ids[0])
}

func (s *Store) LoadAccount(provider, accountID string) (*Credential, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return s.LoadActive(provider)
	}
	if provider == "chatgpt" {
		cg, err := chatgptauth.NewStore(s.chatGPTPath).LoadAccount(accountID)
		if err != nil || cg == nil {
			return nil, err
		}
		return &Credential{
			Provider: "chatgpt", AccountID: cg.AccountID, Email: cg.Email,
			AccessToken: cg.AccessToken, RefreshToken: cg.RefreshToken,
			ClientID: cg.ClientID, Issuer: cg.Issuer, BaseURL: "https://api.openai.com/v1",
			Scopes: cg.Scopes, ExpiresAt: cg.ExpiresAt, SavedAt: cg.SavedAt,
		}, nil
	}
	c, err := s.loadCred(provider, accountID)
	if err != nil || c != nil {
		return c, err
	}
	ids, _ := s.loadIndex(provider)
	for _, id := range ids {
		cand, loadErr := s.loadCred(provider, id)
		if loadErr == nil && cand != nil && (strings.EqualFold(cand.Email, accountID) || strings.EqualFold(cand.DisplayName, accountID)) {
			return cand, nil
		}
	}
	return nil, nil
}

func (s *Store) ListAccounts(provider string) ([]AccountSummary, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "chatgpt" {
		list, err := chatgptauth.NewStore(s.chatGPTPath).ListAccounts()
		if err != nil {
			return nil, err
		}
		out := make([]AccountSummary, 0, len(list))
		for _, item := range list {
			out = append(out, AccountSummary{
				Provider:  "chatgpt",
				ID:        item.ID,
				Email:     item.Email,
				BaseURL:   "https://api.openai.com/v1",
				ExpiresAt: item.ExpiresAt,
				SavedAt:   item.SavedAt,
				Active:    item.Active,
			})
		}
		return out, nil
	}
	ids, err := s.loadIndex(provider)
	if err != nil {
		return nil, err
	}
	activeID, _ := s.ActiveAccountID(provider)
	out := make([]AccountSummary, 0, len(ids))
	for _, id := range ids {
		c, loadErr := s.loadCred(provider, id)
		if loadErr != nil || c == nil {
			continue
		}
		out = append(out, AccountSummary{
			Provider:    provider,
			ID:          id,
			Email:       c.Email,
			DisplayName: c.DisplayName,
			BaseURL:     c.BaseURL,
			ExpiresAt:   c.ExpiresAt,
			SavedAt:     c.SavedAt,
			Active:      id == activeID || (activeID == "" && len(out) == 0),
		})
	}
	return out, nil
}

func (s *Store) SetActiveAccount(provider, accountID string) error {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "chatgpt" {
		return chatgptauth.NewStore(s.chatGPTPath).SetActiveAccount(accountID)
	}
	target := strings.TrimSpace(accountID)
	if target == "" {
		return fmt.Errorf("informe o identificador ou e-mail da conta")
	}
	c, err := s.LoadAccount(provider, target)
	if err != nil {
		return err
	}
	if c == nil {
		return fmt.Errorf("conta %q nao encontrada em %s", accountID, provider)
	}
	if err := s.storage.Set(activeKey(provider), c.AccountID); err != nil {
		return err
	}
	return s.normalizeOwner()
}

func (s *Store) DeleteAccount(provider, accountID string) error {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "chatgpt" {
		if strings.TrimSpace(accountID) == "" {
			return chatgptauth.NewStore(s.chatGPTPath).Delete()
		}
		return chatgptauth.NewStore(s.chatGPTPath).DeleteAccount(accountID)
	}
	target := strings.TrimSpace(accountID)
	if target == "" {
		active, _ := s.ActiveAccountID(provider)
		target = active
	}
	c, err := s.LoadAccount(provider, target)
	if err != nil {
		return err
	}
	if c == nil {
		return fmt.Errorf("conta %q nao encontrada em %s", accountID, provider)
	}
	target = c.AccountID
	_ = s.storage.Delete(credKey(provider, target))
	ids, _ := s.loadIndex(provider)
	remaining := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != target {
			remaining = append(remaining, id)
		}
	}
	if err := s.saveIndex(provider, remaining); err != nil {
		return err
	}
	active, _ := s.ActiveAccountID(provider)
	if active == target || active == "" {
		if len(remaining) > 0 {
			_ = s.storage.Set(activeKey(provider), remaining[0])
		} else {
			_ = s.storage.Delete(activeKey(provider))
		}
	}
	return s.normalizeOwner()
}

func (s *Store) DeleteAll(provider string) error {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "chatgpt" {
		return chatgptauth.NewStore(s.chatGPTPath).DeleteAll()
	}
	ids, _ := s.loadIndex(provider)
	for _, id := range ids {
		_ = s.storage.Delete(credKey(provider, id))
	}
	_ = s.storage.Delete(indexKey(provider))
	_ = s.storage.Delete(activeKey(provider))
	return s.normalizeOwner()
}

func (s *Store) normalizeOwner() error {
	if err := os.Chmod(s.path, 0o600); err != nil && !os.IsNotExist(err) {
		return err
	}
	if os.Geteuid() != 0 {
		return nil
	}
	name := os.Getenv("POLYPOD_DESKTOP_USER")
	if name == "" {
		name = "costa"
	}
	u, err := user.Lookup(name)
	if err != nil {
		return nil
	}
	uid, err1 := strconv.Atoi(u.Uid)
	gid, err2 := strconv.Atoi(u.Gid)
	if err1 != nil || err2 != nil {
		return nil
	}
	return os.Chown(s.path, uid, gid)
}
