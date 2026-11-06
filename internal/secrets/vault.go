// Package secrets provides the agent's persistent generic secret vault.
// Values are returned to the agent in plaintext by design, while the backing
// file remains encrypted at rest by securestorage.
package secrets

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/costa/polypod/internal/securestorage"
)

const keyPrefix = "secret:"

// Record is a credential or other secret the agent may retrieve and use.
type Record struct {
	Name      string    `json:"name"`
	Value     string    `json:"value"`
	Username  string    `json:"username,omitempty"`
	URL       string    `json:"url,omitempty"`
	Notes     string    `json:"notes,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Vault stores generic secrets using encrypted secure storage.
type Vault struct {
	storage securestorage.SecureStorage
}

// NewVault creates an encrypted vault at path. An empty passphrase derives
// the encryption key from this machine's stable identity.
func NewVault(path, passphrase string) *Vault {
	return &Vault{storage: securestorage.NewFileStorage(path, passphrase)}
}

func normalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("nome do secret e obrigatorio")
	}
	if len(name) > 200 {
		return "", fmt.Errorf("nome do secret excede 200 caracteres")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("nome do secret contem caractere de controle")
		}
	}
	return strings.ToLower(name), nil
}

// Save creates or replaces a secret record.
func (v *Vault) Save(record Record) error {
	key, err := normalizeName(record.Name)
	if err != nil {
		return err
	}
	if record.Value == "" {
		return fmt.Errorf("valor do secret e obrigatorio")
	}
	record.Name = strings.TrimSpace(record.Name)
	record.UpdatedAt = time.Now().UTC()
	raw, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("serializando secret: %w", err)
	}
	return v.storage.Set(keyPrefix+key, string(raw))
}

// Get retrieves a secret record with its plaintext value.
func (v *Vault) Get(name string) (Record, error) {
	key, err := normalizeName(name)
	if err != nil {
		return Record{}, err
	}
	raw, err := v.storage.Get(keyPrefix + key)
	if err != nil {
		return Record{}, err
	}
	if raw == "" {
		return Record{}, fmt.Errorf("secret %q nao encontrado", strings.TrimSpace(name))
	}
	var record Record
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return Record{}, fmt.Errorf("decodificando secret %q: %w", name, err)
	}
	return record, nil
}

// Delete removes a secret by name.
func (v *Vault) Delete(name string) error {
	key, err := normalizeName(name)
	if err != nil {
		return err
	}
	return v.storage.Delete(keyPrefix + key)
}

// List returns all records, including their plaintext values.
func (v *Vault) List() ([]Record, error) {
	keys, err := v.storage.List()
	if err != nil {
		return nil, err
	}
	records := make([]Record, 0, len(keys))
	for _, key := range keys {
		if !strings.HasPrefix(key, keyPrefix) {
			continue
		}
		raw, err := v.storage.Get(key)
		if err != nil || raw == "" {
			continue
		}
		var record Record
		if json.Unmarshal([]byte(raw), &record) == nil {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool {
		return strings.ToLower(records[i].Name) < strings.ToLower(records[j].Name)
	})
	return records, nil
}
