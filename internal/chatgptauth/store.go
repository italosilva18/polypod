package chatgptauth

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

	"github.com/costa/polypod/internal/securestorage"
)

const (
	credentialKey    = "chatgpt:oauth"
	hostIDKey        = "chatgpt:host_id"
	accountsIndexKey = "chatgpt:accounts"
	activeAccountKey = "chatgpt:active_account"
	accountKeyPrefix = "chatgpt:account:"
)

func DefaultCredentialPath() string {
	if path := os.Getenv("POLYPOD_CHATGPT_AUTH_FILE"); path != "" {
		return path
	}
	return filepath.Join("data", "conversations", "chatgpt-auth.enc")
}

// Credential is the complete Sign in with ChatGPT session. It is stored
// encrypted at rest and never written to logs or the regular YAML config.
type Credential struct {
	AccountID    string    `json:"account_id,omitempty"`
	Email        string    `json:"email,omitempty"`
	Issuer       string    `json:"issuer"`
	Subject      string    `json:"subject"`
	ClientID     string    `json:"client_id"`
	HostID       string    `json:"host_id"`
	IDToken      string    `json:"id_token"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Scopes       []string  `json:"scopes"`
	ExpiresAt    time.Time `json:"expires_at"`
	SavedAt      time.Time `json:"saved_at"`
}

// AccountSummary describes a stored ChatGPT OAuth account without exposing tokens.
type AccountSummary struct {
	ID        string    `json:"id"`
	Email     string    `json:"email,omitempty"`
	ClientID  string    `json:"client_id,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	SavedAt   time.Time `json:"saved_at,omitempty"`
	Active    bool      `json:"active"`
}

func (c *Credential) NeedsRefresh() bool {
	return c == nil || c.AccessToken == "" || (!c.ExpiresAt.IsZero() && time.Now().Add(5*time.Minute).After(c.ExpiresAt))
}

type Store struct {
	path    string
	storage securestorage.SecureStorage
}

func NewStore(path string) *Store {
	return &Store{path: path, storage: securestorage.NewFileStorage(path, "")}
}

func normalizeAccountID(accountID, email, subject string) string {
	id := strings.TrimSpace(accountID)
	if id == "" {
		id = strings.TrimSpace(email)
	}
	if id == "" {
		id = strings.TrimSpace(subject)
	}
	if id == "" {
		id = "default"
	}
	return id
}

func (s *Store) loadRawCredential(storageKey string) (*Credential, error) {
	raw, err := s.storage.Get(storageKey)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	var cred Credential
	if err := json.Unmarshal([]byte(raw), &cred); err != nil {
		return nil, fmt.Errorf("decodificando credencial ChatGPT: %w", err)
	}
	return &cred, nil
}

func (s *Store) loadIndex() ([]string, error) {
	raw, err := s.storage.Get(accountsIndexKey)
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

func (s *Store) saveIndex(ids []string) error {
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
	return s.storage.Set(accountsIndexKey, string(raw))
}

func (s *Store) ActiveAccountID() (string, error) {
	id, err := s.storage.Get(activeAccountKey)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(id), nil
}

func (s *Store) Load() (*Credential, error) {
	activeID, err := s.ActiveAccountID()
	if err != nil {
		return nil, err
	}
	if activeID != "" {
		cred, loadErr := s.loadRawCredential(accountKeyPrefix + activeID)
		if loadErr != nil {
			return nil, loadErr
		}
		if cred != nil {
			if cred.AccountID == "" {
				cred.AccountID = activeID
			}
			return cred, nil
		}
	}
	cred, err := s.loadRawCredential(credentialKey)
	if err != nil || cred == nil {
		return cred, err
	}
	if cred.AccountID == "" {
		cred.AccountID = normalizeAccountID("", cred.Email, cred.Subject)
	}
	return cred, nil
}

func (s *Store) LoadAccount(accountID string) (*Credential, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return s.Load()
	}
	cred, err := s.loadRawCredential(accountKeyPrefix + accountID)
	if err != nil || cred != nil {
		return cred, err
	}
	legacy, err := s.loadRawCredential(credentialKey)
	if err != nil || legacy == nil {
		return nil, err
	}
	if strings.EqualFold(legacy.Email, accountID) || normalizeAccountID(legacy.AccountID, legacy.Email, legacy.Subject) == accountID {
		return legacy, nil
	}
	return nil, nil
}

func (s *Store) Save(cred *Credential) error {
	if cred == nil {
		return fmt.Errorf("credencial vazia")
	}
	return s.SaveAccount(cred.AccountID, cred)
}

func (s *Store) SaveAccount(accountID string, cred *Credential) error {
	if cred == nil {
		return fmt.Errorf("credencial vazia")
	}
	id := normalizeAccountID(accountID, cred.Email, cred.Subject)
	cred.AccountID = id
	cred.SavedAt = time.Now().UTC()
	raw, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	if err := s.storage.Set(accountKeyPrefix+id, string(raw)); err != nil {
		return err
	}
	if err := s.storage.Set(credentialKey, string(raw)); err != nil {
		return err
	}
	if err := s.storage.Set(activeAccountKey, id); err != nil {
		return err
	}
	ids, _ := s.loadIndex()
	ids = append(ids, id)
	if err := s.saveIndex(ids); err != nil {
		return err
	}
	return s.normalizeOwner()
}

func (s *Store) ListAccounts() ([]AccountSummary, error) {
	ids, err := s.loadIndex()
	if err != nil {
		return nil, err
	}
	activeID, _ := s.ActiveAccountID()
	var out []AccountSummary
	for _, id := range ids {
		cred, loadErr := s.loadRawCredential(accountKeyPrefix + id)
		if loadErr != nil || cred == nil {
			continue
		}
		out = append(out, AccountSummary{
			ID:        id,
			Email:     cred.Email,
			ClientID:  cred.ClientID,
			ExpiresAt: cred.ExpiresAt,
			SavedAt:   cred.SavedAt,
			Active:    id == activeID,
		})
	}
	if len(out) == 0 {
		legacy, legacyErr := s.loadRawCredential(credentialKey)
		if legacyErr != nil {
			return nil, legacyErr
		}
		if legacy != nil {
			id := normalizeAccountID(legacy.AccountID, legacy.Email, legacy.Subject)
			out = append(out, AccountSummary{
				ID:        id,
				Email:     legacy.Email,
				ClientID:  legacy.ClientID,
				ExpiresAt: legacy.ExpiresAt,
				SavedAt:   legacy.SavedAt,
				Active:    true,
			})
		}
	}
	return out, nil
}

func (s *Store) SetActiveAccount(accountID string) error {
	target := strings.TrimSpace(accountID)
	if target == "" {
		return fmt.Errorf("informe o identificador ou e-mail da conta")
	}
	cred, err := s.LoadAccount(target)
	if err != nil {
		return err
	}
	if cred == nil {
		// Allow matching by email case-insensitively across stored accounts.
		ids, _ := s.loadIndex()
		for _, id := range ids {
			c, loadErr := s.loadRawCredential(accountKeyPrefix + id)
			if loadErr == nil && c != nil && strings.EqualFold(c.Email, target) {
				target = id
				cred = c
				break
			}
		}
	}
	if cred == nil {
		return fmt.Errorf("conta ChatGPT %q nao encontrada", accountID)
	}
	cred.AccountID = target
	raw, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	if err := s.storage.Set(activeAccountKey, target); err != nil {
		return err
	}
	if err := s.storage.Set(credentialKey, string(raw)); err != nil {
		return err
	}
	return s.normalizeOwner()
}

func (s *Store) Delete() error {
	activeID, _ := s.ActiveAccountID()
	if activeID != "" {
		return s.DeleteAccount(activeID)
	}
	if err := s.storage.Delete(credentialKey); err != nil {
		return err
	}
	return s.normalizeOwner()
}

func (s *Store) DeleteAccount(accountID string) error {
	target := strings.TrimSpace(accountID)
	ids, _ := s.loadIndex()
	if target != "" {
		found := false
		for _, id := range ids {
			if id == target {
				found = true
				break
			}
		}
		if !found {
			for _, id := range ids {
				if c, err := s.loadRawCredential(accountKeyPrefix + id); err == nil && c != nil && strings.EqualFold(c.Email, target) {
					target = id
					found = true
					break
				}
			}
		}
		if !found {
			legacy, _ := s.loadRawCredential(credentialKey)
			if legacy != nil && (strings.EqualFold(legacy.Email, target) || normalizeAccountID(legacy.AccountID, legacy.Email, legacy.Subject) == target) {
				_ = s.storage.Delete(credentialKey)
				_ = s.storage.Delete(activeAccountKey)
				return s.normalizeOwner()
			}
			return fmt.Errorf("conta ChatGPT %q nao encontrada", accountID)
		}
	}

	_ = s.storage.Delete(accountKeyPrefix + target)
	remaining := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != target {
			remaining = append(remaining, id)
		}
	}
	if err := s.saveIndex(remaining); err != nil {
		return err
	}

	activeID, _ := s.ActiveAccountID()
	if activeID == target || activeID == "" {
		if len(remaining) > 0 {
			nextID := remaining[0]
			if nextCred, err := s.loadRawCredential(accountKeyPrefix + nextID); err == nil && nextCred != nil {
				raw, _ := json.Marshal(nextCred)
				_ = s.storage.Set(activeAccountKey, nextID)
				_ = s.storage.Set(credentialKey, string(raw))
				return s.normalizeOwner()
			}
		}
		_ = s.storage.Delete(activeAccountKey)
		_ = s.storage.Delete(credentialKey)
	}
	return s.normalizeOwner()
}

func (s *Store) DeleteAll() error {
	ids, _ := s.loadIndex()
	for _, id := range ids {
		_ = s.storage.Delete(accountKeyPrefix + id)
	}
	_ = s.storage.Delete(accountsIndexKey)
	_ = s.storage.Delete(activeAccountKey)
	_ = s.storage.Delete(credentialKey)
	return s.normalizeOwner()
}

func (s *Store) HostID() (string, error) {
	return s.storage.Get(hostIDKey)
}

func (s *Store) SaveHostID(hostID string) error {
	if err := s.storage.Set(hostIDKey, hostID); err != nil {
		return err
	}
	return s.normalizeOwner()
}

// The root service refreshes the same session used by the desktop user. Keep
// the file private (0600) but owned by POLYPOD_DESKTOP_USER after root writes.
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
