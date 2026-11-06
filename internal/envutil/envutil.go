package envutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// GetEnvOrDefault returns the value of the environment variable named by key,
// or defaultVal if the variable is not set or empty.
func GetEnvOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// GetEnvBool returns the boolean value of the environment variable named by key.
// Accepted truthy values: "1", "true", "yes", "on" (case-insensitive).
// Returns defaultVal if the variable is not set or cannot be interpreted.
func GetEnvBool(key string, defaultVal bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return defaultVal
	}
}

// GetEnvInt returns the integer value of the environment variable named by key.
// Returns defaultVal if the variable is not set or cannot be parsed as an integer.
func GetEnvInt(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}

// ExpandEnvVars replaces ${VAR} and $VAR references in s with their
// environment variable values. Uses os.ExpandEnv which handles both forms.
func ExpandEnvVars(s string) string {
	return os.ExpandEnv(s)
}

// IsCI returns true if a known CI environment variable is set, indicating
// the process is running inside a CI/CD pipeline.
func IsCI() bool {
	ciVars := []string{
		"CI",
		"GITHUB_ACTIONS",
		"GITLAB_CI",
		"JENKINS_URL",
		"CIRCLECI",
		"TRAVIS",
		"BITBUCKET_PIPELINE",
		"BUILDKITE",
		"CODEBUILD_BUILD_ID",
		"TF_BUILD",
		"DRONE",
		"SEMAPHORE",
		"TEAMCITY_VERSION",
	}
	for _, v := range ciVars {
		if os.Getenv(v) != "" {
			return true
		}
	}
	return false
}

// IsDocker returns true if the process appears to be running inside a
// Docker container. It checks for /.dockerenv and cgroup indicators.
func IsDocker() bool {
	// Check for /.dockerenv file (most common indicator)
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	// Check /proc/1/cgroup for docker references
	data, err := os.ReadFile("/proc/1/cgroup")
	if err == nil {
		content := string(data)
		if strings.Contains(content, "docker") || strings.Contains(content, "containerd") {
			return true
		}
	}
	// Check /proc/self/mountinfo for overlay filesystem (common in containers)
	data, err = os.ReadFile("/proc/self/mountinfo")
	if err == nil {
		if strings.Contains(string(data), "/docker/") {
			return true
		}
	}
	return false
}

// HomeDir returns the user's home directory. It tries os.UserHomeDir first,
// then falls back to the HOME environment variable (or USERPROFILE on Windows).
func HomeDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	if runtime.GOOS == "windows" {
		return GetEnvOrDefault("USERPROFILE", GetEnvOrDefault("HOME", "."))
	}
	return GetEnvOrDefault("HOME", ".")
}

// CacheDir returns the XDG cache directory or a platform-appropriate fallback.
// On Linux: $XDG_CACHE_HOME or ~/.cache
// On macOS: ~/Library/Caches
// On Windows: %LOCALAPPDATA% or ~/AppData/Local
func CacheDir() string {
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		return v
	}
	home := HomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Caches")
	case "windows":
		if v := os.Getenv("LOCALAPPDATA"); v != "" {
			return v
		}
		return filepath.Join(home, "AppData", "Local")
	default:
		return filepath.Join(home, ".cache")
	}
}

// ConfigDir returns the XDG config directory or a platform-appropriate fallback.
// On Linux: $XDG_CONFIG_HOME or ~/.config
// On macOS: ~/Library/Application Support
// On Windows: %APPDATA% or ~/AppData/Roaming
func ConfigDir() string {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return v
	}
	home := HomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support")
	case "windows":
		if v := os.Getenv("APPDATA"); v != "" {
			return v
		}
		return filepath.Join(home, "AppData", "Roaming")
	default:
		return filepath.Join(home, ".config")
	}
}

// DataDir returns the XDG data directory or a platform-appropriate fallback.
// On Linux: $XDG_DATA_HOME or ~/.local/share
// On macOS: ~/Library/Application Support
// On Windows: %LOCALAPPDATA% or ~/AppData/Local
func DataDir() string {
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return v
	}
	home := HomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support")
	case "windows":
		if v := os.Getenv("LOCALAPPDATA"); v != "" {
			return v
		}
		return filepath.Join(home, "AppData", "Local")
	default:
		return filepath.Join(home, ".local", "share")
	}
}
