// Package browser provides cross-platform browser and URL launching utilities.
package browser

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// LaunchConfig holds configuration for launching a browser with full control.
type LaunchConfig struct {
	// Browser is the browser binary name or path. If empty, the system default is used.
	Browser string
	// Private enables private/incognito mode (--incognito for Chrome/Chromium/Brave/Edge, --private-window for Firefox).
	Private bool
	// Args contains additional command-line arguments passed to the browser.
	Args []string
}

// knownBrowsers lists browser binaries to probe when detecting installed browsers.
var knownBrowsers = []string{
	"google-chrome",
	"google-chrome-stable",
	"chromium",
	"chromium-browser",
	"firefox",
	"brave-browser",
	"microsoft-edge",
	"microsoft-edge-stable",
}

// validateURL checks that the URL is well-formed and uses http or https.
func validateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL format: %s", rawURL)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("invalid URL protocol: must use http:// or https://, got %s", u.Scheme)
	}
	return nil
}

// launcher returns the platform-specific command and base args used to open a URL.
func launcher() (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return "open", nil
	case "windows":
		return "cmd", []string{"/c", "start"}
	default: // linux, freebsd, etc.
		return "xdg-open", nil
	}
}

// Open opens the given URL in the user's default browser.
// On Linux it uses xdg-open, on macOS open, and on Windows cmd /c start.
func Open(rawURL string) error {
	if err := validateURL(rawURL); err != nil {
		return err
	}

	// Honour the BROWSER environment variable when set.
	if env := os.Getenv("BROWSER"); env != "" {
		return run(env, rawURL)
	}

	cmd, baseArgs := launcher()
	args := append(baseArgs, rawURL)
	return run(cmd, args...)
}

// OpenWith opens the given URL using a specific browser binary.
func OpenWith(browser, rawURL string) error {
	if err := validateURL(rawURL); err != nil {
		return err
	}
	return run(browser, rawURL)
}

// Available reports whether a browser launcher command can be found in PATH.
func Available() bool {
	cmd, _ := launcher()
	_, err := exec.LookPath(cmd)
	return err == nil
}

// DetectBrowsers returns the list of well-known browser binaries found in PATH.
// On macOS and Windows the platform launcher (open / cmd) is always available,
// so this function focuses on explicit browser executables.
func DetectBrowsers() []string {
	var found []string
	for _, name := range knownBrowsers {
		if _, err := exec.LookPath(name); err == nil {
			found = append(found, name)
		}
	}
	return found
}

// OpenFile opens a local file in the default browser by converting it to a
// file:// URL. The path is resolved to an absolute path first.
func OpenFile(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolving path: %w", err)
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("file not found: %w", err)
	}

	fileURL := "file://" + abs

	cmd, baseArgs := launcher()
	args := append(baseArgs, fileURL)
	return run(cmd, args...)
}

// OpenHTML writes the provided HTML string to a temporary file and opens it in
// the default browser.
func OpenHTML(html string) error {
	f, err := os.CreateTemp("", "polypod-*.html")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(html); err != nil {
		return fmt.Errorf("writing HTML to temp file: %w", err)
	}
	// Ensure data is flushed before we hand the path to the browser.
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	return OpenFile(f.Name())
}

// Launch opens a URL using the full LaunchConfig. When cfg.Browser is empty the
// system default launcher is used. If cfg.Private is true, the appropriate
// private-browsing flag is added (--incognito for Chrome-family browsers,
// --private-window for Firefox).
func Launch(cfg LaunchConfig, rawURL string) error {
	if err := validateURL(rawURL); err != nil {
		return err
	}

	browser := cfg.Browser
	if browser == "" {
		if env := os.Getenv("BROWSER"); env != "" {
			browser = env
		}
	}

	// When we have an explicit browser binary, call it directly.
	if browser != "" {
		var args []string
		args = append(args, cfg.Args...)
		if cfg.Private {
			args = append(args, privateFlag(browser))
		}
		args = append(args, rawURL)
		return run(browser, args...)
	}

	// Fallback: use the platform launcher. Private mode is not supported when
	// going through the generic launcher because we don't know which browser
	// the OS will choose.
	if cfg.Private {
		return errors.New("private mode requires an explicit browser binary (set LaunchConfig.Browser or BROWSER env var)")
	}

	cmd, baseArgs := launcher()
	var args []string
	args = append(args, baseArgs...)
	args = append(args, cfg.Args...)
	args = append(args, rawURL)
	return run(cmd, args...)
}

// privateFlag returns the appropriate incognito/private flag for the given
// browser binary name.
func privateFlag(browser string) string {
	lower := strings.ToLower(filepath.Base(browser))
	switch {
	case strings.Contains(lower, "firefox"):
		return "--private-window"
	default:
		// Chrome, Chromium, Brave, Edge all use --incognito.
		return "--incognito"
	}
}

// run executes a command with the given arguments, detaching it from the
// current process group so the browser survives after the CLI exits.
func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	// Prevent the child from inheriting stdin; the browser should run
	// independently of the terminal.
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}
