package chatgptauth

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"

	"github.com/costa/polypod/internal/securestorage"
)

const (
	credentialKey = "chatgpt:oauth"
	hostIDKey     = "chatgpt:host_id"
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

func (s *Store) Load() (*Credential, error) {
	raw, err := s.storage.Get(credentialKey)
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

func (s *Store) Save(cred *Credential) error {
	cred.SavedAt = time.Now().UTC()
	raw, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	if err := s.storage.Set(credentialKey, string(raw)); err != nil {
		return err
	}
	return s.normalizeOwner()
}

func (s *Store) Delete() error {
	if err := s.storage.Delete(credentialKey); err != nil {
		return err
	}
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
