// Package xdg implements the XDG Base Directory specification with
// platform-aware defaults for Linux, macOS, and Windows.
package xdg

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const appName = "polypod"

// -------------------------------------------------------------------
// Core directory resolvers
// -------------------------------------------------------------------

// ConfigDir returns the configuration directory.
//
//	Linux:   $XDG_CONFIG_HOME/polypod  (default ~/.config/polypod)
//	macOS:   ~/Library/Application Support/polypod
//	Windows: %APPDATA%\polypod
func ConfigDir() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir(), "Library", "Application Support", appName)
	case "windows":
		if d := os.Getenv("APPDATA"); d != "" {
			return filepath.Join(d, appName)
		}
		return filepath.Join(homeDir(), "AppData", "Roaming", appName)
	default: // linux, freebsd, etc.
		if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
			return filepath.Join(d, appName)
		}
		return filepath.Join(homeDir(), ".config", appName)
	}
}

// DataDir returns the data directory.
//
//	Linux:   $XDG_DATA_HOME/polypod  (default ~/.local/share/polypod)
//	macOS:   ~/Library/Application Support/polypod
//	Windows: %LOCALAPPDATA%\polypod
func DataDir() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir(), "Library", "Application Support", appName)
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, appName)
		}
		return filepath.Join(homeDir(), "AppData", "Local", appName)
	default:
		if d := os.Getenv("XDG_DATA_HOME"); d != "" {
			return filepath.Join(d, appName)
		}
		return filepath.Join(homeDir(), ".local", "share", appName)
	}
}

// CacheDir returns the cache directory.
//
//	Linux:   $XDG_CACHE_HOME/polypod  (default ~/.cache/polypod)
//	macOS:   ~/Library/Caches/polypod
//	Windows: %LOCALAPPDATA%\polypod\cache
func CacheDir() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir(), "Library", "Caches", appName)
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, appName, "cache")
		}
		return filepath.Join(homeDir(), "AppData", "Local", appName, "cache")
	default:
		if d := os.Getenv("XDG_CACHE_HOME"); d != "" {
			return filepath.Join(d, appName)
		}
		return filepath.Join(homeDir(), ".cache", appName)
	}
}

// StateDir returns the state directory.
//
//	Linux:   $XDG_STATE_HOME/polypod  (default ~/.local/state/polypod)
//	macOS:   ~/Library/Application Support/polypod/state
//	Windows: %LOCALAPPDATA%\polypod\state
func StateDir() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir(), "Library", "Application Support", appName, "state")
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, appName, "state")
		}
		return filepath.Join(homeDir(), "AppData", "Local", appName, "state")
	default:
		if d := os.Getenv("XDG_STATE_HOME"); d != "" {
			return filepath.Join(d, appName)
		}
		return filepath.Join(homeDir(), ".local", "state", appName)
	}
}

// RuntimeDir returns the runtime directory.
//
//	Linux:   $XDG_RUNTIME_DIR/polypod  (default /run/user/$UID/polypod)
//	macOS:   /tmp/polypod-$UID
//	Windows: %TEMP%\polypod
func RuntimeDir() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d", appName, os.Getuid()))
	case "windows":
		return filepath.Join(os.TempDir(), appName)
	default:
		if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
			return filepath.Join(d, appName)
		}
		return filepath.Join("/run", "user", fmt.Sprintf("%d", os.Getuid()), appName)
	}
}

// -------------------------------------------------------------------
// Directory creation
// -------------------------------------------------------------------

// EnsureDirs creates all standard directories with appropriate permissions.
// Config/Data/State directories use 0700; Cache/Runtime use 0700.
func EnsureDirs() error {
	dirs := []string{
		ConfigDir(),
		DataDir(),
		CacheDir(),
		StateDir(),
		RuntimeDir(),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("creating %s: %w", d, err)
		}
	}
	return nil
}

// -------------------------------------------------------------------
// Helpers
// -------------------------------------------------------------------

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	if h := os.Getenv("USERPROFILE"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}
