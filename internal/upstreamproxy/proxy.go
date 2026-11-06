// Package upstreamproxy provides an HTTP relay proxy that forwards API
// requests through a configured upstream proxy. It validates that the
// target host is in an allow-list, injects an authorization header, and
// relays the response back to the caller.
package upstreamproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ProxyConfig holds configuration for the upstream relay proxy.
type ProxyConfig struct {
	// ListenAddr is the TCP address the proxy binds to.
	// Defaults to ":0" (ephemeral port) when empty.
	ListenAddr string

	// UpstreamURL is the full URL of the upstream proxy to forward
	// requests through (e.g. "http://proxy.internal:8080").
	UpstreamURL string

	// AuthHeader is the value injected as the Proxy-Authorization
	// header on every forwarded request. May be empty if the upstream
	// proxy does not require authentication.
	AuthHeader string

	// AllowedHosts lists the host (or host:port) values that this proxy
	// is willing to relay to. An empty list rejects every request.
	AllowedHosts []string

	// Logger is the structured logger used for diagnostics. When nil a
	// no-op logger is used.
	Logger *slog.Logger
}

// Proxy is an HTTP relay proxy that forwards requests through a
// configured upstream proxy, validating hosts and injecting auth.
type Proxy struct {
	cfg          ProxyConfig
	log          *slog.Logger
	upstreamURL  *url.URL
	allowedHosts map[string]struct{}
	transport    *http.Transport
	server       *http.Server
	listener     net.Listener

	mu      sync.Mutex
	started bool
}

// NewProxy validates cfg and returns a ready-to-start Proxy.
func NewProxy(cfg ProxyConfig) (*Proxy, error) {
	if cfg.UpstreamURL == "" {
		return nil, errors.New("upstreamproxy: UpstreamURL is required")
	}
	parsed, err := url.Parse(cfg.UpstreamURL)
	if err != nil {
		return nil, fmt.Errorf("upstreamproxy: invalid UpstreamURL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("upstreamproxy: UpstreamURL scheme must be http or https, got %q", parsed.Scheme)
	}

	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":0"
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	allowed := make(map[string]struct{}, len(cfg.AllowedHosts))
	for _, h := range cfg.AllowedHosts {
		allowed[strings.ToLower(h)] = struct{}{}
	}

	transport := &http.Transport{
		Proxy: http.ProxyURL(parsed),
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	p := &Proxy{
		cfg:          cfg,
		log:          logger,
		upstreamURL:  parsed,
		allowedHosts: allowed,
		transport:    transport,
	}
	return p, nil
}

// Start binds the listener, starts serving HTTP, and returns the actual
// listen address (useful when ListenAddr was ":0"). The server runs in
// the background; call Stop to shut it down.
func (p *Proxy) Start(ctx context.Context) (listenAddr string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.started {
		return "", errors.New("upstreamproxy: already started")
	}

	ln, err := net.Listen("tcp", p.cfg.ListenAddr)
	if err != nil {
		return "", fmt.Errorf("upstreamproxy: listen: %w", err)
	}
	p.listener = ln

	p.server = &http.Server{
		Handler:      p,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
		BaseContext:  func(_ net.Listener) context.Context { return ctx },
	}

	p.started = true
	addr := ln.Addr().String()
	p.log.Info("upstreamproxy listening", "addr", addr)

	go func() {
		if serveErr := p.server.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			p.log.Error("upstreamproxy serve error", "err", serveErr)
		}
	}()

	return addr, nil
}

// Stop gracefully shuts down the proxy server.
func (p *Proxy) Stop(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.started || p.server == nil {
		return nil
	}
	p.started = false
	p.log.Info("upstreamproxy shutting down")
	return p.server.Shutdown(ctx)
}

// ServeHTTP validates the request host against AllowedHosts, injects
// the auth header, forwards the request through the upstream proxy, and
// copies the response back to the caller.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	// Strip port for the allow-list check so callers can list just the
	// hostname. We check both "host:port" and bare "host".
	hostOnly := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostOnly = h
	}

	if !p.isAllowed(host, hostOnly) {
		p.log.Warn("upstreamproxy: host not allowed", "host", host)
		http.Error(w, "Forbidden: host not in allow-list", http.StatusForbidden)
		return
	}

	// Handle CONNECT for HTTPS tunneling.
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r, host)
		return
	}

	// Build the outbound request.
	outURL := *r.URL
	if outURL.Host == "" {
		outURL.Host = host
	}
	if outURL.Scheme == "" {
		outURL.Scheme = "https"
	}

	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, outURL.String(), r.Body)
	if err != nil {
		p.log.Error("upstreamproxy: failed to create request", "err", err)
		http.Error(w, "Internal proxy error", http.StatusBadGateway)
		return
	}
	copyHeaders(outReq.Header, r.Header)
	outReq.Host = host

	if p.cfg.AuthHeader != "" {
		outReq.Header.Set("Proxy-Authorization", p.cfg.AuthHeader)
	}

	resp, err := p.transport.RoundTrip(outReq)
	if err != nil {
		p.log.Error("upstreamproxy: upstream request failed", "err", err, "host", host)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		p.log.Error("upstreamproxy: response copy error", "err", err)
	}
}

// handleConnect implements the HTTP CONNECT method for HTTPS tunneling.
func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request, host string) {
	// Dial through the upstream proxy.
	upstreamConn, err := p.transport.DialContext(r.Context(), "tcp", host)
	if err != nil {
		p.log.Error("upstreamproxy: CONNECT dial failed", "err", err, "host", host)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		upstreamConn.Close()
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	clientConn, clientBuf, err := hj.Hijack()
	if err != nil {
		upstreamConn.Close()
		p.log.Error("upstreamproxy: hijack failed", "err", err)
		return
	}
	// Flush any buffered data from the response writer.
	if clientBuf.Reader.Buffered() > 0 {
		buffered := make([]byte, clientBuf.Reader.Buffered())
		_, _ = clientBuf.Read(buffered)
		_, _ = upstreamConn.Write(buffered)
	}

	// Bidirectional copy.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(upstreamConn, clientConn)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(clientConn, upstreamConn)
	}()
	wg.Wait()
	clientConn.Close()
	upstreamConn.Close()
}

// WrapClient returns a shallow copy of client whose Transport routes
// all requests through this proxy. If client is nil, http.DefaultClient
// is used as the base.
func (p *Proxy) WrapClient(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	c := *client
	c.Transport = p.transport
	return &c
}

// isAllowed returns true when the host (with or without port) appears
// in the allow-list.
func (p *Proxy) isAllowed(hostPort, hostOnly string) bool {
	if len(p.allowedHosts) == 0 {
		return false
	}
	_, okFull := p.allowedHosts[strings.ToLower(hostPort)]
	_, okBare := p.allowedHosts[strings.ToLower(hostOnly)]
	return okFull || okBare
}

// copyHeaders copies all headers from src into dst.
func copyHeaders(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}
