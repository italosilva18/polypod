// Package cacerts manages custom CA certificate bundles for corporate proxies.
//
// It loads system certificates plus extra certs from file paths or inline PEM,
// and returns a configured tls.Config / http.Client ready for use behind
// TLS-intercepting proxies.
package cacerts

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config controls which extra CA certificates are loaded and how TLS behaves.
type Config struct {
	// ExtraCertPaths are filesystem paths to PEM-encoded certificate files
	// (or directories of .pem/.crt files) to add to the pool.
	ExtraCertPaths []string

	// ExtraCertPEM is inline PEM-encoded certificate data appended to the pool.
	ExtraCertPEM string

	// InsecureSkipVerify disables certificate verification entirely.
	// Use only for debugging; never in production.
	InsecureSkipVerify bool

	// Logger receives diagnostic messages. If nil, logging is silently skipped.
	Logger *slog.Logger
}

// log is a convenience that guards against a nil logger.
func (c *Config) log(msg string, args ...any) {
	if c.Logger != nil {
		c.Logger.Debug(msg, args...)
	}
}

// LoadCerts builds an [x509.CertPool] that contains the system root
// certificates plus any extra certificates specified in cfg.
//
// Extra certificates come from two sources (both optional):
//   - Files (or directories) listed in [Config.ExtraCertPaths].
//   - Inline PEM data in [Config.ExtraCertPEM].
//
// If a path points to a directory, every file with a .pem or .crt extension
// inside it is read.  Individual read errors are logged but do not fail the
// call — the function returns an error only when the system pool itself
// cannot be loaded.
func LoadCerts(cfg Config) (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("cacerts: load system cert pool: %w", err)
	}

	cfg.log("loaded system certificate pool")

	// Append certs from paths.
	for _, p := range cfg.ExtraCertPaths {
		appendFromPath(pool, p, &cfg)
	}

	// Append inline PEM.
	if cfg.ExtraCertPEM != "" {
		if ok := pool.AppendCertsFromPEM([]byte(cfg.ExtraCertPEM)); !ok {
			cfg.log("inline PEM contained no valid certificates")
		} else {
			cfg.log("appended inline PEM certificates")
		}
	}

	return pool, nil
}

// appendFromPath adds PEM certificates from a file or directory to pool.
func appendFromPath(pool *x509.CertPool, p string, cfg *Config) {
	info, err := os.Stat(p)
	if err != nil {
		cfg.log("skipping cert path", "path", p, "error", err)
		return
	}

	if info.IsDir() {
		entries, err := os.ReadDir(p)
		if err != nil {
			cfg.log("cannot read cert directory", "path", p, "error", err)
			return
		}
		for _, e := range entries {
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if ext == ".pem" || ext == ".crt" {
				appendFile(pool, filepath.Join(p, e.Name()), cfg)
			}
		}
		return
	}

	appendFile(pool, p, cfg)
}

// appendFile reads a single PEM file and appends its certificates to pool.
func appendFile(pool *x509.CertPool, path string, cfg *Config) {
	data, err := os.ReadFile(path)
	if err != nil {
		cfg.log("cannot read cert file", "path", path, "error", err)
		return
	}
	if ok := pool.AppendCertsFromPEM(data); !ok {
		cfg.log("no valid certificates found in file", "path", path)
	} else {
		cfg.log("appended certificates from file", "path", path)
	}
}

// ConfigureTLS returns a [tls.Config] with the combined certificate pool
// built by [LoadCerts].  If [Config.InsecureSkipVerify] is true the
// returned config skips server certificate verification.
func ConfigureTLS(cfg Config) (*tls.Config, error) {
	pool, err := LoadCerts(cfg)
	if err != nil {
		return nil, err
	}

	tlsCfg := &tls.Config{
		RootCAs: pool,
	}

	if cfg.InsecureSkipVerify {
		tlsCfg.InsecureSkipVerify = true //nolint:gosec // explicitly requested
		cfg.log("TLS certificate verification disabled (insecure)")
	}

	return tlsCfg, nil
}

// ConfigureHTTPClient returns an [http.Client] whose transport uses the
// custom TLS configuration produced by [ConfigureTLS].
// On TLS configuration failure the client falls back to default transport and
// the error is logged (if a Logger is set).
func ConfigureHTTPClient(cfg Config) *http.Client {
	tlsCfg, err := ConfigureTLS(cfg)
	if err != nil {
		cfg.log("falling back to default transport", "error", err)
		return &http.Client{Timeout: 30 * time.Second}
	}

	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: tlsCfg,
		},
	}
}

// commonCertDirs are well-known directories where Linux distributions and
// macOS keep CA certificate bundles.
var commonCertDirs = []string{
	"/etc/ssl/certs",
	"/usr/local/share/ca-certificates",
}

// envVarsWithCertPaths lists environment variables that may point to
// additional CA certificate files or directories.
var envVarsWithCertPaths = []string{
	"SSL_CERT_FILE",
	"SSL_CERT_DIR",
	"NODE_EXTRA_CA_CERTS",
}

// DetectProxyCerts probes common filesystem locations and environment
// variables for extra CA certificate files.
//
// It returns deduplicated, absolute paths that exist on disk.  The caller
// can feed the result into [Config.ExtraCertPaths].
func DetectProxyCerts() []string {
	seen := make(map[string]struct{})
	var paths []string

	add := func(p string) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return
		}
		if _, dup := seen[abs]; dup {
			return
		}
		if _, err := os.Stat(abs); err != nil {
			return
		}
		seen[abs] = struct{}{}
		paths = append(paths, abs)
	}

	for _, dir := range commonCertDirs {
		add(dir)
	}

	for _, env := range envVarsWithCertPaths {
		if v := os.Getenv(env); v != "" {
			add(v)
		}
	}

	return paths
}

// AutoConfigure detects proxy certificates, loads them, and returns a
// ready-to-use [http.Client].  If detection or loading fails it returns nil
// so callers can fall back to http.DefaultClient.
func AutoConfigure(logger *slog.Logger) *http.Client {
	extra := DetectProxyCerts()

	cfg := Config{
		ExtraCertPaths: extra,
		Logger:         logger,
	}

	if logger != nil {
		logger.Debug("cacerts: auto-configure", "detected_paths", extra)
	}

	tlsCfg, err := ConfigureTLS(cfg)
	if err != nil {
		if logger != nil {
			logger.Warn("cacerts: auto-configure failed", "error", err)
		}
		return nil
	}

	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: tlsCfg,
		},
	}
}
