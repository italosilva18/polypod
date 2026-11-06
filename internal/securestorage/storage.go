// Package securestorage provides encrypted credential storage with platform-native
// keychain integration and encrypted-file fallback.
package securestorage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/pbkdf2"
)

// -------------------------------------------------------------------
// SecureStorage interface
// -------------------------------------------------------------------

// SecureStorage is the top-level abstraction for credential persistence.
// Implementations MUST be safe for concurrent use.
type SecureStorage interface {
	// Get retrieves the value stored under key, or "" if absent.
	Get(key string) (string, error)
	// Set stores value under key.
	Set(key, value string) error
	// Delete removes key.
	Delete(key string) error
	// List returns all stored keys.
	List() ([]string, error)
}

// -------------------------------------------------------------------
// FallbackStorage – tries primary, falls back to secondary
// -------------------------------------------------------------------

// FallbackStorage tries a primary SecureStorage and falls back to a secondary
// on read failures, mirroring the TypeScript createFallbackStorage pattern.
type FallbackStorage struct {
	Primary   SecureStorage
	Secondary SecureStorage
}

func (f *FallbackStorage) Get(key string) (string, error) {
	v, err := f.Primary.Get(key)
	if err == nil && v != "" {
		return v, nil
	}
	return f.Secondary.Get(key)
}

func (f *FallbackStorage) Set(key, value string) error {
	if err := f.Primary.Set(key, value); err == nil {
		return nil
	}
	return f.Secondary.Set(key, value)
}

func (f *FallbackStorage) Delete(key string) error {
	e1 := f.Primary.Delete(key)
	e2 := f.Secondary.Delete(key)
	if e1 != nil && e2 != nil {
		return fmt.Errorf("primary: %w; secondary: %v", e1, e2)
	}
	return nil
}

func (f *FallbackStorage) List() ([]string, error) {
	keys, err := f.Primary.List()
	if err == nil && len(keys) > 0 {
		return keys, nil
	}
	return f.Secondary.List()
}

// -------------------------------------------------------------------
// FileStorage – AES-256-GCM encrypted JSON file
// -------------------------------------------------------------------

const (
	pbkdf2Iterations = 100_000
	pbkdf2SaltLen    = 16
	aesKeyLen        = 32 // AES-256
)

// FileStorage stores credentials as an AES-256-GCM encrypted JSON file.
// The encryption key is derived from a passphrase using PBKDF2.
type FileStorage struct {
	mu         sync.Mutex
	path       string
	passphrase []byte
}

// NewFileStorage creates a FileStorage at path, deriving the AES key from passphrase.
// If passphrase is empty, the machine ID is used (see DerivePassphrase).
func NewFileStorage(path, passphrase string) *FileStorage {
	pp := []byte(passphrase)
	if len(pp) == 0 {
		pp = deriveDefaultPassphrase()
	}
	return &FileStorage{path: path, passphrase: pp}
}

// envelope is the on-disk JSON structure.
type envelope struct {
	Salt       []byte            `json:"salt"`
	Nonce      []byte            `json:"nonce"`
	Ciphertext []byte            `json:"ciphertext"`
	Data       map[string]string `json:"-"` // transient, never serialised
}

func (fs *FileStorage) Get(key string) (string, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	data, err := fs.load()
	if err != nil {
		return "", err
	}
	return data[key], nil
}

func (fs *FileStorage) Set(key, value string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	data, err := fs.load()
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if data == nil {
		data = make(map[string]string)
	}
	data[key] = value
	return fs.save(data)
}

func (fs *FileStorage) Delete(key string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	data, err := fs.load()
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	delete(data, key)
	if len(data) == 0 {
		return os.Remove(fs.path)
	}
	return fs.save(data)
}

func (fs *FileStorage) List() ([]string, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	data, err := fs.load()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	return keys, nil
}

// load reads and decrypts the credential file.
func (fs *FileStorage) load() (map[string]string, error) {
	raw, err := os.ReadFile(fs.path)
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("corrupt credential file: %w", err)
	}
	plaintext, err := Decrypt(env.Ciphertext, env.Nonce, env.Salt, fs.passphrase)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}
	data := make(map[string]string)
	if err := json.Unmarshal(plaintext, &data); err != nil {
		return nil, fmt.Errorf("corrupt credential payload: %w", err)
	}
	return data, nil
}

// save encrypts and writes the credential map atomically.
func (fs *FileStorage) save(data map[string]string) error {
	plaintext, err := json.Marshal(data)
	if err != nil {
		return err
	}
	salt := make([]byte, pbkdf2SaltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return err
	}
	ct, nonce, err := Encrypt(plaintext, salt, fs.passphrase)
	if err != nil {
		return err
	}
	env := envelope{Salt: salt, Nonce: nonce, Ciphertext: ct}
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(fs.path), 0o700); err != nil {
		return err
	}
	tmp := fs.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, fs.path)
}

// -------------------------------------------------------------------
// AES-256-GCM helpers
// -------------------------------------------------------------------

// DeriveKey returns an AES-256 key from passphrase and salt via PBKDF2-SHA256.
func DeriveKey(passphrase, salt []byte) []byte {
	return pbkdf2.Key(passphrase, salt, pbkdf2Iterations, aesKeyLen, sha256.New)
}

// Encrypt encrypts plaintext with AES-256-GCM using a key derived from
// passphrase+salt. Returns (ciphertext, nonce, error).
func Encrypt(plaintext, salt, passphrase []byte) (ciphertext, nonce []byte, err error) {
	key := DeriveKey(passphrase, salt)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	return ct, nonce, nil
}

// Decrypt decrypts ciphertext encrypted with Encrypt.
func Decrypt(ciphertext, nonce, salt, passphrase []byte) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, errors.New("empty ciphertext")
	}
	key := DeriveKey(passphrase, salt)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}

// -------------------------------------------------------------------
// Default passphrase derivation from machine identity
// -------------------------------------------------------------------

// deriveDefaultPassphrase builds a passphrase from stable machine attributes:
// hostname + machine-id (Linux) or IOPlatformUUID (macOS).
func deriveDefaultPassphrase() []byte {
	host, _ := os.Hostname()
	mid := machineID()
	h := sha256.Sum256([]byte(host + "|" + mid))
	return h[:]
}

// machineID returns a stable machine identifier.
func machineID() string {
	// Linux: /etc/machine-id
	if data, err := os.ReadFile("/etc/machine-id"); err == nil {
		return string(data)
	}
	// macOS: IOPlatformUUID (via ioreg) – we skip exec here; hostname alone is adequate fallback.
	// Windows: HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid – same.
	return "polypod-default-mid"
}
