// Package fingerprint computes a stable, anonymized fingerprint for the install.
package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

var (
	cached     string
	cachedOnce sync.Once
	testReset  sync.Mutex
)

// Get returns a stable anonymized fingerprint for this installation.
// First call computes and caches; subsequent calls return cached value.
func Get() (string, error) {
	var err error
	cachedOnce.Do(func() {
		cached, err = compute()
		if err != nil {
			return
		}
		// Try to persist to cache file (best-effort)
		_ = persist(cached)
	})
	if cached == "" && err == nil {
		// Try loading from disk
		if loaded, lerr := load(); lerr == nil && loaded != "" {
			cached = loaded
		} else {
			cached, err = compute()
			if err == nil {
				_ = persist(cached)
			}
		}
	}
	return cached, err
}

// MustGet returns the fingerprint or panics on error.
func MustGet() string {
	f, err := Get()
	if err != nil {
		panic(err)
	}
	return f
}

// ResetForTest clears the cache. Use only in tests.
func ResetForTest() {
	testReset.Lock()
	defer testReset.Unlock()
	cached = ""
	cachedOnce = sync.Once{}
}

// compute generates a fresh fingerprint from system properties.
func compute() (string, error) {
	h := sha256.New()

	h.Write([]byte(runtime.GOOS))
	h.Write([]byte("|"))
	h.Write([]byte(runtime.GOARCH))
	h.Write([]byte("|"))

	// Hostname
	if hn, err := os.Hostname(); err == nil {
		h.Write([]byte(hn))
	}
	h.Write([]byte("|"))

	// Executable path (canonical)
	if exe, err := os.Executable(); err == nil {
		if abs, err := filepath.Abs(exe); err == nil {
			h.Write([]byte(abs))
		}
	}
	h.Write([]byte("|"))

	// First non-loopback MAC address (best-effort)
	if mac := firstMAC(); mac != "" {
		h.Write([]byte(mac))
	}
	h.Write([]byte("|"))

	// User ID (uid on unix, username on windows)
	if user := os.Getenv("USER"); user != "" {
		h.Write([]byte(user))
	} else if user := os.Getenv("USERNAME"); user != "" {
		h.Write([]byte(user))
	}

	sum := h.Sum(nil)
	return hex.EncodeToString(sum)[:32], nil // 32 hex chars = 128 bits
}

// firstMAC returns the first non-loopback MAC address found, or empty.
func firstMAC() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		if len(iface.HardwareAddr) > 0 {
			return iface.HardwareAddr.String()
		}
	}
	return ""
}

// cacheFilePath returns the path to the cache file.
func cacheFilePath() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cacheDir, "polypod")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "fingerprint"), nil
}

// persist writes the fingerprint to disk.
func persist(fp string) error {
	path, err := cacheFilePath()
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(fp), 0600)
}

// load reads the fingerprint from disk.
func load() (string, error) {
	path, err := cacheFilePath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	fp := strings.TrimSpace(string(data))
	if !isValidFingerprint(fp) {
		return "", fmt.Errorf("invalid fingerprint format")
	}
	return fp, nil
}

// isValidFingerprint checks if a string looks like a valid fingerprint.
func isValidFingerprint(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// Anonymized returns a shorter, more obviously anonymized version (8 chars).
func Anonymized() (string, error) {
	fp, err := Get()
	if err != nil {
		return "", err
	}
	return fp[:8], nil
}

// Info returns a struct with information used in the fingerprint.
type Info struct {
	OS         string
	Arch       string
	Hostname   string
	Executable string
	HasMAC     bool
	User       string
}

// CurrentInfo returns information about the current system.
func CurrentInfo() Info {
	info := Info{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
	}
	if hn, err := os.Hostname(); err == nil {
		info.Hostname = hn
	}
	if exe, err := os.Executable(); err == nil {
		info.Executable = exe
	}
	info.HasMAC = firstMAC() != ""
	if user := os.Getenv("USER"); user != "" {
		info.User = user
	} else if user := os.Getenv("USERNAME"); user != "" {
		info.User = user
	}
	return info
}
