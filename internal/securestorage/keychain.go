package securestorage

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

const defaultServiceName = "polypod"

// KeychainStorage uses the OS-native keychain for credential storage.
//
//   - macOS: security(1) CLI (Keychain Services)
//   - Linux: secret-tool (libsecret / GNOME Keyring / KWallet)
//   - Windows: cmdkey / Credential Manager (via powershell)
//
// Falls back to no-op with errors on unsupported platforms.
type KeychainStorage struct {
	mu      sync.Mutex
	Service string // scoping prefix, defaults to "polypod"
}

// NewKeychainStorage returns a KeychainStorage scoped to the given service name.
func NewKeychainStorage(service string) *KeychainStorage {
	if service == "" {
		service = defaultServiceName
	}
	return &KeychainStorage{Service: service}
}

// Get retrieves a credential from the platform keychain.
func (k *KeychainStorage) Get(key string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.getCredential(key)
}

// Set stores a credential in the platform keychain.
func (k *KeychainStorage) Set(key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.storeCredential(key, value)
}

// Delete removes a credential from the platform keychain.
func (k *KeychainStorage) Delete(key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.deleteCredential(key)
}

// List returns all keys stored under this service. Not all backends support
// enumeration efficiently; this is best-effort.
func (k *KeychainStorage) List() ([]string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.listCredentials()
}

// -------------------------------------------------------------------
// StoreCredential / GetCredential / DeleteCredential — public helpers
// -------------------------------------------------------------------

// StoreCredential stores a value under service/key in the OS keychain.
func StoreCredential(service, key, value string) error {
	ks := NewKeychainStorage(service)
	return ks.Set(key, value)
}

// GetCredential retrieves a value by service/key from the OS keychain.
func GetCredential(service, key string) (string, error) {
	ks := NewKeychainStorage(service)
	return ks.Get(key)
}

// DeleteCredential removes a service/key entry from the OS keychain.
func DeleteCredential(service, key string) error {
	ks := NewKeychainStorage(service)
	return ks.Delete(key)
}

// -------------------------------------------------------------------
// Platform-specific implementations
// -------------------------------------------------------------------

func (k *KeychainStorage) account() string {
	u := os.Getenv("USER")
	if u == "" {
		u = os.Getenv("USERNAME") // Windows
	}
	if u == "" {
		u = "polypod-user"
	}
	return u
}

func (k *KeychainStorage) serviceName(key string) string {
	return k.Service + "-" + key
}

// --- macOS ---

func (k *KeychainStorage) macGet(key string) (string, error) {
	svc := k.serviceName(key)
	out, err := exec.Command("security", "find-generic-password",
		"-a", k.account(), "-s", svc, "-w").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("keychain get %q: %w (%s)", key, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (k *KeychainStorage) macSet(key, value string) error {
	svc := k.serviceName(key)
	hexValue := fmt.Sprintf("%x", value)
	cmd := fmt.Sprintf("add-generic-password -U -a %q -s %q -X %q\n", k.account(), svc, hexValue)
	c := exec.Command("security", "-i")
	c.Stdin = strings.NewReader(cmd)
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("keychain set %q: %w (%s)", key, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (k *KeychainStorage) macDelete(key string) error {
	svc := k.serviceName(key)
	out, err := exec.Command("security", "delete-generic-password",
		"-a", k.account(), "-s", svc).CombinedOutput()
	if err != nil {
		return fmt.Errorf("keychain delete %q: %w (%s)", key, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// --- Linux (libsecret / secret-tool) ---

func (k *KeychainStorage) linuxGet(key string) (string, error) {
	out, err := exec.Command("secret-tool", "lookup", "service", k.Service, "key", key).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("secret-tool lookup %q: %w (%s)", key, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (k *KeychainStorage) linuxSet(key, value string) error {
	cmd := exec.Command("secret-tool", "store", "--label", k.serviceName(key),
		"service", k.Service, "key", key)
	cmd.Stdin = strings.NewReader(value)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("secret-tool store %q: %w (%s)", key, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (k *KeychainStorage) linuxDelete(key string) error {
	out, err := exec.Command("secret-tool", "clear", "service", k.Service, "key", key).CombinedOutput()
	if err != nil {
		return fmt.Errorf("secret-tool clear %q: %w (%s)", key, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// --- Windows (cmdkey) ---

func (k *KeychainStorage) winGet(key string) (string, error) {
	target := k.serviceName(key)
	// Use PowerShell to read the Credential Manager (cmdkey /list can't extract passwords).
	script := fmt.Sprintf(
		`[System.Runtime.InteropServices.Marshal]::PtrToStringAuto([System.Runtime.InteropServices.Marshal]::SecureStringToBSTR(((Get-StoredCredential -Target '%s').Password)))`,
		target,
	)
	out, err := exec.Command("powershell", "-NoProfile", "-Command", script).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("credential manager get %q: %w (%s)", key, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (k *KeychainStorage) winSet(key, value string) error {
	target := k.serviceName(key)
	_, _ = exec.Command("cmdkey", "/delete:"+target).CombinedOutput() // remove old
	out, err := exec.Command("cmdkey", "/generic:"+target,
		"/user:"+k.account(), "/pass:"+value).CombinedOutput()
	if err != nil {
		return fmt.Errorf("credential manager set %q: %w (%s)", key, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (k *KeychainStorage) winDelete(key string) error {
	target := k.serviceName(key)
	out, err := exec.Command("cmdkey", "/delete:"+target).CombinedOutput()
	if err != nil {
		return fmt.Errorf("credential manager delete %q: %w (%s)", key, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// -------------------------------------------------------------------
// Dispatch
// -------------------------------------------------------------------

func (k *KeychainStorage) getCredential(key string) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return k.macGet(key)
	case "linux":
		return k.linuxGet(key)
	case "windows":
		return k.winGet(key)
	default:
		return "", fmt.Errorf("keychain: unsupported platform %s", runtime.GOOS)
	}
}

func (k *KeychainStorage) storeCredential(key, value string) error {
	switch runtime.GOOS {
	case "darwin":
		return k.macSet(key, value)
	case "linux":
		return k.linuxSet(key, value)
	case "windows":
		return k.winSet(key, value)
	default:
		return fmt.Errorf("keychain: unsupported platform %s", runtime.GOOS)
	}
}

func (k *KeychainStorage) deleteCredential(key string) error {
	switch runtime.GOOS {
	case "darwin":
		return k.macDelete(key)
	case "linux":
		return k.linuxDelete(key)
	case "windows":
		return k.winDelete(key)
	default:
		return fmt.Errorf("keychain: unsupported platform %s", runtime.GOOS)
	}
}

// listCredentials is best-effort; not all backends have efficient enumeration.
// We store a key index in the keychain under a well-known meta-key.
func (k *KeychainStorage) listCredentials() ([]string, error) {
	raw, err := k.getCredential("__polypod_key_index__")
	if err != nil {
		return nil, nil // no index yet
	}
	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, nil
	}
	return keys, nil
}
