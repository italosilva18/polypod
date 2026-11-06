package xdg

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// clearXDGEnv unsets all XDG_* environment variables for the duration of the
// test so that defaults are exercised deterministically. It uses t.Setenv with
// an empty value, then explicitly unsets via t.Setenv-aware path: t.Setenv
// always restores the original value at test end.
func clearXDGEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"XDG_CONFIG_HOME",
		"XDG_DATA_HOME",
		"XDG_CACHE_HOME",
		"XDG_STATE_HOME",
		"XDG_RUNTIME_DIR",
	} {
		t.Setenv(k, "")
		// t.Setenv won't unset; emulate "unset" by ensuring empty so the
		// production code's `os.Getenv(...) != ""` check falls through.
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("unsetenv %s: %v", k, err)
		}
	}
}

// setHome forces a known HOME for the duration of the test on Unix-like
// systems. On Windows the production code consults USERPROFILE / APPDATA /
// LOCALAPPDATA so we set those as well.
func setHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func TestConfigDir_DefaultLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("linux-specific default; runtime=%s", runtime.GOOS)
	}
	clearXDGEnv(t)
	home := t.TempDir()
	setHome(t, home)

	got := ConfigDir()
	want := filepath.Join(home, ".config", "polypod")
	if got != want {
		t.Errorf("ConfigDir() = %q, want %q", got, want)
	}
}

func TestConfigDir_HonorsXDGConfigHome(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("XDG_CONFIG_HOME only applies on linux; runtime=%s", runtime.GOOS)
	}
	clearXDGEnv(t)
	home := t.TempDir()
	setHome(t, home)

	custom := filepath.Join(t.TempDir(), "custom-config")
	t.Setenv("XDG_CONFIG_HOME", custom)

	got := ConfigDir()
	want := filepath.Join(custom, "polypod")
	if got != want {
		t.Errorf("ConfigDir() = %q, want %q", got, want)
	}
}

func TestDataDir_DefaultLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("linux-specific default; runtime=%s", runtime.GOOS)
	}
	clearXDGEnv(t)
	home := t.TempDir()
	setHome(t, home)

	got := DataDir()
	want := filepath.Join(home, ".local", "share", "polypod")
	if got != want {
		t.Errorf("DataDir() = %q, want %q", got, want)
	}
}

func TestDataDir_HonorsXDGDataHome(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("XDG_DATA_HOME only applies on linux; runtime=%s", runtime.GOOS)
	}
	clearXDGEnv(t)
	home := t.TempDir()
	setHome(t, home)

	custom := filepath.Join(t.TempDir(), "custom-data")
	t.Setenv("XDG_DATA_HOME", custom)

	got := DataDir()
	want := filepath.Join(custom, "polypod")
	if got != want {
		t.Errorf("DataDir() = %q, want %q", got, want)
	}
}

func TestCacheDir_DefaultLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("linux-specific default; runtime=%s", runtime.GOOS)
	}
	clearXDGEnv(t)
	home := t.TempDir()
	setHome(t, home)

	got := CacheDir()
	want := filepath.Join(home, ".cache", "polypod")
	if got != want {
		t.Errorf("CacheDir() = %q, want %q", got, want)
	}
}

func TestCacheDir_HonorsXDGCacheHome(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("XDG_CACHE_HOME only applies on linux; runtime=%s", runtime.GOOS)
	}
	clearXDGEnv(t)
	home := t.TempDir()
	setHome(t, home)

	custom := filepath.Join(t.TempDir(), "custom-cache")
	t.Setenv("XDG_CACHE_HOME", custom)

	got := CacheDir()
	want := filepath.Join(custom, "polypod")
	if got != want {
		t.Errorf("CacheDir() = %q, want %q", got, want)
	}
}

func TestStateDir_DefaultLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("linux-specific default; runtime=%s", runtime.GOOS)
	}
	clearXDGEnv(t)
	home := t.TempDir()
	setHome(t, home)

	got := StateDir()
	want := filepath.Join(home, ".local", "state", "polypod")
	if got != want {
		t.Errorf("StateDir() = %q, want %q", got, want)
	}
}

func TestStateDir_HonorsXDGStateHome(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("XDG_STATE_HOME only applies on linux; runtime=%s", runtime.GOOS)
	}
	clearXDGEnv(t)
	home := t.TempDir()
	setHome(t, home)

	custom := filepath.Join(t.TempDir(), "custom-state")
	t.Setenv("XDG_STATE_HOME", custom)

	got := StateDir()
	want := filepath.Join(custom, "polypod")
	if got != want {
		t.Errorf("StateDir() = %q, want %q", got, want)
	}
}

func TestRuntimeDir_HonorsXDGRuntimeDir(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("XDG_RUNTIME_DIR only applies on linux; runtime=%s", runtime.GOOS)
	}
	clearXDGEnv(t)
	home := t.TempDir()
	setHome(t, home)

	custom := filepath.Join(t.TempDir(), "custom-runtime")
	t.Setenv("XDG_RUNTIME_DIR", custom)

	got := RuntimeDir()
	want := filepath.Join(custom, "polypod")
	if got != want {
		t.Errorf("RuntimeDir() = %q, want %q", got, want)
	}
}

func TestRuntimeDir_DefaultLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("linux-specific default; runtime=%s", runtime.GOOS)
	}
	clearXDGEnv(t)
	home := t.TempDir()
	setHome(t, home)

	got := RuntimeDir()
	want := filepath.Join("/run", "user", fmt.Sprintf("%d", os.Getuid()), "polypod")
	if got != want {
		t.Errorf("RuntimeDir() = %q, want %q", got, want)
	}
}

func TestAllDirs_AppNameSuffix(t *testing.T) {
	// Independent of platform: every directory must end with the application
	// name (or, in some cases, "polypod/cache" or "polypod/state" suffixes).
	// We at least verify the substring "polypod" is present.
	dirs := map[string]string{
		"ConfigDir":  ConfigDir(),
		"DataDir":    DataDir(),
		"CacheDir":   CacheDir(),
		"StateDir":   StateDir(),
		"RuntimeDir": RuntimeDir(),
	}
	for name, d := range dirs {
		if !strings.Contains(d, "polypod") {
			t.Errorf("%s = %q does not contain app name", name, d)
		}
		if d == "" {
			t.Errorf("%s returned empty string", name)
		}
	}
}

func TestAllDirs_DistinctPaths(t *testing.T) {
	// On Linux with no env vars, all four user-facing dirs should be distinct.
	if runtime.GOOS != "linux" {
		t.Skipf("path layout assertion specific to linux; runtime=%s", runtime.GOOS)
	}
	clearXDGEnv(t)
	home := t.TempDir()
	setHome(t, home)

	paths := []string{
		ConfigDir(),
		DataDir(),
		CacheDir(),
		StateDir(),
	}
	seen := make(map[string]bool, len(paths))
	for _, p := range paths {
		if seen[p] {
			t.Errorf("duplicate path returned: %q", p)
		}
		seen[p] = true
	}
}

func TestEnsureDirs_CreatesAllDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on POSIX HOME indirection")
	}
	clearXDGEnv(t)
	home := t.TempDir()
	setHome(t, home)
	// Redirect runtime dir into a writable temp location to avoid /run/user.
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(t.TempDir(), "rt"))

	if err := EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs() error = %v", err)
	}

	for name, d := range map[string]string{
		"ConfigDir":  ConfigDir(),
		"DataDir":    DataDir(),
		"CacheDir":   CacheDir(),
		"StateDir":   StateDir(),
		"RuntimeDir": RuntimeDir(),
	} {
		info, err := os.Stat(d)
		if err != nil {
			t.Errorf("%s: stat %q failed: %v", name, d, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s: %q is not a directory", name, d)
		}
	}
}

func TestPlatformSpecificDefaults(t *testing.T) {
	// Sanity check: confirm the per-platform branches return paths that
	// match the expected layout for the host OS.
	clearXDGEnv(t)
	home := t.TempDir()
	setHome(t, home)

	got := ConfigDir()
	switch runtime.GOOS {
	case "linux", "freebsd":
		want := filepath.Join(home, ".config", "polypod")
		if got != want {
			t.Errorf("ConfigDir() on %s = %q, want %q", runtime.GOOS, got, want)
		}
	case "darwin":
		want := filepath.Join(home, "Library", "Application Support", "polypod")
		if got != want {
			t.Errorf("ConfigDir() on darwin = %q, want %q", got, want)
		}
	case "windows":
		// On Windows, ConfigDir consults APPDATA. We didn't set it, so
		// expect the USERPROFILE\AppData\Roaming\polypod fallback.
		t.Setenv("APPDATA", "")
		_ = os.Unsetenv("APPDATA")
		got = ConfigDir()
		want := filepath.Join(home, "AppData", "Roaming", "polypod")
		if got != want {
			t.Errorf("ConfigDir() on windows = %q, want %q", got, want)
		}
	default:
		// Other platforms hit the linux branch by default.
		want := filepath.Join(home, ".config", "polypod")
		if got != want {
			t.Errorf("ConfigDir() on %s = %q, want %q", runtime.GOOS, got, want)
		}
	}
}
