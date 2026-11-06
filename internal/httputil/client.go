// Package httputil provides HTTP client construction with proxy, TLS, timeout,
// retry, and user-agent support.
package httputil

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

// -------------------------------------------------------------------
// TLS / mTLS configuration
// -------------------------------------------------------------------

// TLSConfig holds optional TLS / mTLS parameters.
type TLSConfig struct {
	// CACertFiles are paths to additional CA certificate PEM files.
	CACertFiles []string
	// ClientCertFile is the path to the client certificate PEM (mTLS).
	ClientCertFile string
	// ClientKeyFile is the path to the client private key PEM (mTLS).
	ClientKeyFile string
}

// BuildTLSConfig returns a *tls.Config assembled from TLSConfig.
// If cfg is nil or empty, nil is returned (use Go defaults).
func BuildTLSConfig(cfg *TLSConfig) (*tls.Config, error) {
	if cfg == nil {
		return nil, nil
	}
	tc := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	// Custom CA certs
	if len(cfg.CACertFiles) > 0 {
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		for _, f := range cfg.CACertFiles {
			pem, err := os.ReadFile(f)
			if err != nil {
				return nil, fmt.Errorf("reading CA cert %s: %w", f, err)
			}
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("invalid PEM in %s", f)
			}
		}
		tc.RootCAs = pool
	}
	// mTLS client certificate
	if cfg.ClientCertFile != "" && cfg.ClientKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.ClientCertFile, cfg.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("loading client cert/key: %w", err)
		}
		tc.Certificates = []tls.Certificate{cert}
	}
	return tc, nil
}

// -------------------------------------------------------------------
// Client options
// -------------------------------------------------------------------

// ClientOption configures NewHTTPClient.
type ClientOption func(*clientOptions)

type clientOptions struct {
	timeout      time.Duration
	tls          *TLSConfig
	proxy        *ProxyConfig
	userAgent    string
	maxRetries   int
	retryBackoff time.Duration
}

// WithTimeout sets the overall client timeout (default 30s).
func WithTimeout(d time.Duration) ClientOption {
	return func(o *clientOptions) { o.timeout = d }
}

// WithTLS supplies custom TLS / mTLS settings.
func WithTLS(cfg *TLSConfig) ClientOption {
	return func(o *clientOptions) { o.tls = cfg }
}

// WithProxy supplies proxy settings. If nil, proxy is auto-detected from env.
func WithProxy(cfg *ProxyConfig) ClientOption {
	return func(o *clientOptions) { o.proxy = cfg }
}

// WithUserAgent overrides the default User-Agent string.
func WithUserAgent(ua string) ClientOption {
	return func(o *clientOptions) { o.userAgent = ua }
}

// WithRetries enables automatic retries with exponential backoff.
func WithRetries(n int, backoff time.Duration) ClientOption {
	return func(o *clientOptions) {
		o.maxRetries = n
		o.retryBackoff = backoff
	}
}

// -------------------------------------------------------------------
// NewHTTPClient
// -------------------------------------------------------------------

// NewHTTPClient builds an *http.Client with proxy, TLS, timeouts, and
// optional retry wrapping.
func NewHTTPClient(opts ...ClientOption) (*http.Client, error) {
	o := &clientOptions{
		timeout:      30 * time.Second,
		retryBackoff: 500 * time.Millisecond,
	}
	for _, fn := range opts {
		fn(o)
	}

	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	// TLS
	if o.tls != nil {
		tc, err := BuildTLSConfig(o.tls)
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = tc
	}

	// Proxy
	proxy := o.proxy
	if proxy == nil {
		proxy = DetectProxy()
	}
	if proxy != nil && proxy.URL != "" {
		transport.Proxy = proxy.ProxyFunc()
	}

	client := &http.Client{
		Timeout:   o.timeout,
		Transport: transport,
	}

	// Wrap with retry transport if requested
	if o.maxRetries > 0 {
		client.Transport = &retryTransport{
			base:    client.Transport,
			max:     o.maxRetries,
			backoff: o.retryBackoff,
		}
	}

	// Wrap with user-agent injector
	ua := o.userAgent
	if ua == "" {
		ua = DefaultUserAgent()
	}
	client.Transport = &uaTransport{base: client.Transport, ua: ua}

	return client, nil
}

// -------------------------------------------------------------------
// User-Agent
// -------------------------------------------------------------------

// Version may be set at build time via -ldflags.
var Version = "dev"

// DefaultUserAgent returns the default User-Agent string for polypod.
func DefaultUserAgent() string {
	return fmt.Sprintf("polypod/%s (%s; %s)", Version, runtime.GOOS, runtime.GOARCH)
}

type uaTransport struct {
	base http.RoundTripper
	ua   string
}

func (t *uaTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", t.ua)
	}
	return t.base.RoundTrip(req)
}

// -------------------------------------------------------------------
// Retryable transport
// -------------------------------------------------------------------

type retryTransport struct {
	base    http.RoundTripper
	max     int
	backoff time.Duration
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	var err error
	for attempt := 0; attempt <= t.max; attempt++ {
		resp, err = t.base.RoundTrip(req)
		if err == nil && resp.StatusCode < 500 {
			return resp, nil
		}
		if !isRetryable(resp, err) || attempt == t.max {
			break
		}
		wait := time.Duration(float64(t.backoff) * math.Pow(2, float64(attempt)))
		if wait > 30*time.Second {
			wait = 30 * time.Second
		}
		select {
		case <-time.After(wait):
		case <-req.Context().Done():
			return resp, context.Cause(req.Context())
		}
	}
	return resp, err
}

func isRetryable(resp *http.Response, err error) bool {
	if err != nil {
		// Network-level errors are retryable
		s := strings.ToLower(err.Error())
		return strings.Contains(s, "connection reset") ||
			strings.Contains(s, "eof") ||
			strings.Contains(s, "timeout")
	}
	if resp != nil {
		return resp.StatusCode == 429 || resp.StatusCode >= 500
	}
	return false
}
