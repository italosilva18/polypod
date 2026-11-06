package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/costa/polypod/internal/ssrfguard"
)

// SafeClient wraps http.Client with SSRF protection.
// Blocks requests to loopback, private networks, link-local,
// and known metadata service IPs.
type SafeClient struct {
	guard  *ssrfguard.Guard
	client *http.Client
}

// NewSafeClient creates a new client with SSRF protection enabled.
// Timeout is the per-request timeout.
func NewSafeClient(timeout time.Duration) *SafeClient {
	guard := ssrfguard.NewGuard(ssrfguard.DefaultConfig())

	transport := &http.Transport{
		MaxIdleConns:        10,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	wrapped := guard.WrapTransport(transport)

	return &SafeClient{
		guard: guard,
		client: &http.Client{
			Transport: wrapped,
			Timeout:   timeout,
		},
	}
}

// NewSafeClientWithAllowlist creates a client that permits specific hosts.
func NewSafeClientWithAllowlist(timeout time.Duration, allowlist []string) *SafeClient {
	cfg := ssrfguard.DefaultConfig()
	cfg.Allowlist = allowlist
	guard := ssrfguard.NewGuard(cfg)

	transport := &http.Transport{
		MaxIdleConns:        10,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	wrapped := guard.WrapTransport(transport)

	return &SafeClient{
		guard: guard,
		client: &http.Client{
			Transport: wrapped,
			Timeout:   timeout,
		},
	}
}

// Get performs a GET request through the SSRF-guarded client.
func (c *SafeClient) Get(ctx context.Context, targetURL string) ([]byte, error) {
	if err := c.validateURL(targetURL); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "polypod/0.4")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, errors.New("http error: " + resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024)) // 10MB cap
	if err != nil {
		return nil, err
	}
	return body, nil
}

// GetString is a convenience wrapper returning the body as a string.
func (c *SafeClient) GetString(ctx context.Context, targetURL string) (string, error) {
	body, err := c.Get(ctx, targetURL)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// Head performs a HEAD request.
func (c *SafeClient) Head(ctx context.Context, targetURL string) (*http.Response, error) {
	if err := c.validateURL(targetURL); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, targetURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "polypod/0.4")

	return c.client.Do(req)
}

// Do executes an arbitrary request through the guarded client.
func (c *SafeClient) Do(req *http.Request) (*http.Response, error) {
	if err := c.validateURL(req.URL.String()); err != nil {
		return nil, err
	}
	return c.client.Do(req)
}

// validateURL runs a pre-flight host check before making any DNS call.
func (c *SafeClient) validateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("only http/https schemes are allowed")
	}
	if ok, reason := c.guard.IsSafeHost(u.Host); !ok {
		return errors.New("blocked by SSRF guard: " + reason)
	}
	return nil
}

// Transport returns the underlying http.Transport (for embedding in other clients).
func (c *SafeClient) Transport() http.RoundTripper {
	return c.client.Transport
}
