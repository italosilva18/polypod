package envutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// clearCIVars unsets all known CI env vars so tests are isolated from the
// actual CI environment they might run in.
func clearCIVars(t *testing.T) {
	t.Helper()
	ciVars := []string{
		"CI", "GITHUB_ACTIONS", "GITLAB_CI", "JENKINS_URL", "CIRCLECI",
		"TRAVIS", "BITBUCKET_PIPELINE", "BUILDKITE", "CODEBUILD_BUILD_ID",
		"TF_BUILD", "DRONE", "SEMAPHORE", "TEAMCITY_VERSION",
	}
	for _, v := range ciVars {
		t.Setenv(v, "")
		os.Unsetenv(v)
	}
}

func TestGetEnvOrDefault(t *testing.T) {
	t.Run("set var returns value", func(t *testing.T) {
		t.Setenv("TEST_ENVUTIL_VAR", "value")
		if got := GetEnvOrDefault("TEST_ENVUTIL_VAR", "default"); got != "value" {
			t.Errorf("set var = %q; want %q", got, "value")
		}
	})

	t.Run("empty var returns default", func(t *testing.T) {
		t.Setenv("TEST_ENVUTIL_EMPTY", "")
		// Per implementation, empty string is treated as unset.
		if got := GetEnvOrDefault("TEST_ENVUTIL_EMPTY", "default"); got != "default" {
			t.Errorf("empty var = %q; want %q", got, "default")
		}
	})

	t.Run("unset var returns default", func(t *testing.T) {
		os.Unsetenv("TEST_ENVUTIL_UNLIKELY_XYZ123")
		if got := GetEnvOrDefault("TEST_ENVUTIL_UNLIKELY_XYZ123", "default"); got != "default" {
			t.Errorf("unset var = %q; want %q", got, "default")
		}
	})
}

func TestGetEnvBool(t *testing.T) {
	tests := []struct {
		name       string
		val        string
		set        bool
		defaultVal bool
		want       bool
	}{
		{"true lowercase", "true", true, false, true},
		{"TRUE uppercase", "TRUE", true, false, true},
		{"one", "1", true, false, true},
		{"yes", "yes", true, false, true},
		{"on", "on", true, false, true},
		{"false lowercase", "false", true, true, false},
		{"FALSE uppercase", "FALSE", true, true, false},
		{"zero", "0", true, true, false},
		{"no", "no", true, true, false},
		{"off", "off", true, true, false},
		{"unset returns default true", "", false, true, true},
		{"unset returns default false", "", false, false, false},
		{"garbage returns default", "notabool", true, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv("TEST_ENVUTIL_BOOL", tt.val)
			} else {
				os.Unsetenv("TEST_ENVUTIL_BOOL")
			}
			if got := GetEnvBool("TEST_ENVUTIL_BOOL", tt.defaultVal); got != tt.want {
				t.Errorf("GetEnvBool(%q) = %v; want %v", tt.val, got, tt.want)
			}
		})
	}
}

func TestGetEnvInt(t *testing.T) {
	t.Run("valid int", func(t *testing.T) {
		t.Setenv("TEST_ENVUTIL_INT", "42")
		if got := GetEnvInt("TEST_ENVUTIL_INT", 0); got != 42 {
			t.Errorf("valid int = %d; want 42", got)
		}
	})

	t.Run("negative int", func(t *testing.T) {
		t.Setenv("TEST_ENVUTIL_INT", "-7")
		if got := GetEnvInt("TEST_ENVUTIL_INT", 0); got != -7 {
			t.Errorf("negative int = %d; want -7", got)
		}
	})

	t.Run("invalid int returns default", func(t *testing.T) {
		t.Setenv("TEST_ENVUTIL_INT", "notanumber")
		if got := GetEnvInt("TEST_ENVUTIL_INT", 99); got != 99 {
			t.Errorf("invalid int = %d; want 99", got)
		}
	})

	t.Run("missing returns default", func(t *testing.T) {
		os.Unsetenv("TEST_ENVUTIL_INT_MISSING")
		if got := GetEnvInt("TEST_ENVUTIL_INT_MISSING", 123); got != 123 {
			t.Errorf("missing int = %d; want 123", got)
		}
	})
}

func TestExpandEnvVars(t *testing.T) {
	t.Setenv("TEST_EXPAND_FOO", "bar")
	t.Setenv("TEST_EXPAND_NAME", "polypod")

	t.Run("dollar form", func(t *testing.T) {
		got := ExpandEnvVars("value=$TEST_EXPAND_FOO")
		if got != "value=bar" {
			t.Errorf("got %q; want %q", got, "value=bar")
		}
	})

	t.Run("brace form", func(t *testing.T) {
		got := ExpandEnvVars("value=${TEST_EXPAND_FOO}")
		if got != "value=bar" {
			t.Errorf("got %q; want %q", got, "value=bar")
		}
	})

	t.Run("multiple vars", func(t *testing.T) {
		got := ExpandEnvVars("$TEST_EXPAND_NAME-${TEST_EXPAND_FOO}")
		if got != "polypod-bar" {
			t.Errorf("got %q; want %q", got, "polypod-bar")
		}
	})

	t.Run("no vars unchanged", func(t *testing.T) {
		got := ExpandEnvVars("plain text")
		if got != "plain text" {
			t.Errorf("got %q; want %q", got, "plain text")
		}
	})

	t.Run("unset var expands to empty", func(t *testing.T) {
		os.Unsetenv("TEST_EXPAND_UNSET_XYZ")
		got := ExpandEnvVars("[${TEST_EXPAND_UNSET_XYZ}]")
		if got != "[]" {
			t.Errorf("got %q; want %q", got, "[]")
		}
	})
}

func TestIsCI(t *testing.T) {
	t.Run("CI=true", func(t *testing.T) {
		clearCIVars(t)
		t.Setenv("CI", "true")
		if !IsCI() {
			t.Error("IsCI() = false; want true with CI=true")
		}
	})

	t.Run("GITHUB_ACTIONS set", func(t *testing.T) {
		clearCIVars(t)
		t.Setenv("GITHUB_ACTIONS", "true")
		if !IsCI() {
			t.Error("IsCI() = false; want true with GITHUB_ACTIONS set")
		}
	})

	t.Run("no CI vars", func(t *testing.T) {
		clearCIVars(t)
		if IsCI() {
			t.Error("IsCI() = true; want false with no CI vars set")
		}
	})
}

func TestIsDocker(t *testing.T) {
	// In a normal test environment /.dockerenv won't exist, and /proc/1/cgroup
	// / /proc/self/mountinfo typically won't contain docker markers (unless
	// the tests themselves happen to run inside a container). We assert the
	// function returns a bool and does not panic. If we can verify the
	// filesystem has no docker indicators, we also assert it returns false.
	got := IsDocker()

	_, errDockerenv := os.Stat("/.dockerenv")
	dockerenvExists := errDockerenv == nil

	hasDockerCgroup := false
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		s := string(data)
		if strings.Contains(s, "docker") || strings.Contains(s, "containerd") {
			hasDockerCgroup = true
		}
	}

	hasDockerMount := false
	if data, err := os.ReadFile("/proc/self/mountinfo"); err == nil {
		if strings.Contains(string(data), "/docker/") {
			hasDockerMount = true
		}
	}

	expected := dockerenvExists || hasDockerCgroup || hasDockerMount
	if got != expected {
		t.Errorf("IsDocker() = %v; want %v (dockerenv=%v cgroup=%v mount=%v)",
			got, expected, dockerenvExists, hasDockerCgroup, hasDockerMount)
	}
}

func TestHomeDir(t *testing.T) {
	home := HomeDir()
	if home == "" {
		t.Error("HomeDir() returned empty string")
	}
	// Should match os.UserHomeDir when available.
	if got, err := os.UserHomeDir(); err == nil {
		if home != got {
			t.Errorf("HomeDir() = %q; want %q", home, got)
		}
	}
}

func TestCacheDir(t *testing.T) {
	t.Run("XDG_CACHE_HOME wins", func(t *testing.T) {
		t.Setenv("XDG_CACHE_HOME", "/tmp/custom-cache")
		if got := CacheDir(); got != "/tmp/custom-cache" {
			t.Errorf("CacheDir() = %q; want /tmp/custom-cache", got)
		}
	})

	t.Run("fallback to platform default", func(t *testing.T) {
		os.Unsetenv("XDG_CACHE_HOME")
		got := CacheDir()
		if got == "" {
			t.Fatal("CacheDir() returned empty string")
		}
		home := HomeDir()
		var want string
		switch runtime.GOOS {
		case "darwin":
			want = filepath.Join(home, "Library", "Caches")
		case "windows":
			if v := os.Getenv("LOCALAPPDATA"); v != "" {
				want = v
			} else {
				want = filepath.Join(home, "AppData", "Local")
			}
		default:
			want = filepath.Join(home, ".cache")
		}
		if got != want {
			t.Errorf("CacheDir() = %q; want %q", got, want)
		}
	})
}

func TestConfigDir(t *testing.T) {
	t.Run("XDG_CONFIG_HOME wins", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "/tmp/custom-config")
		if got := ConfigDir(); got != "/tmp/custom-config" {
			t.Errorf("ConfigDir() = %q; want /tmp/custom-config", got)
		}
	})

	t.Run("fallback to platform default", func(t *testing.T) {
		os.Unsetenv("XDG_CONFIG_HOME")
		got := ConfigDir()
		if got == "" {
			t.Fatal("ConfigDir() returned empty string")
		}
		home := HomeDir()
		var want string
		switch runtime.GOOS {
		case "darwin":
			want = filepath.Join(home, "Library", "Application Support")
		case "windows":
			if v := os.Getenv("APPDATA"); v != "" {
				want = v
			} else {
				want = filepath.Join(home, "AppData", "Roaming")
			}
		default:
			want = filepath.Join(home, ".config")
		}
		if got != want {
			t.Errorf("ConfigDir() = %q; want %q", got, want)
		}
	})
}

func TestDataDir(t *testing.T) {
	t.Run("XDG_DATA_HOME wins", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "/tmp/custom-data")
		if got := DataDir(); got != "/tmp/custom-data" {
			t.Errorf("DataDir() = %q; want /tmp/custom-data", got)
		}
	})

	t.Run("fallback to platform default", func(t *testing.T) {
		os.Unsetenv("XDG_DATA_HOME")
		got := DataDir()
		if got == "" {
			t.Fatal("DataDir() returned empty string")
		}
		home := HomeDir()
		var want string
		switch runtime.GOOS {
		case "darwin":
			want = filepath.Join(home, "Library", "Application Support")
		case "windows":
			if v := os.Getenv("LOCALAPPDATA"); v != "" {
				want = v
			} else {
				want = filepath.Join(home, "AppData", "Local")
			}
		default:
			want = filepath.Join(home, ".local", "share")
		}
		if got != want {
			t.Errorf("DataDir() = %q; want %q", got, want)
		}
	})
}
