package httputil

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// -------------------------------------------------------------------
// NewHTTPClient / ClientOption tests
// -------------------------------------------------------------------

func TestNewHTTPClient_DefaultTimeout(t *testing.T) {
	c, err := NewHTTPClient()
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.Timeout != 30*time.Second {
		t.Errorf("default timeout = %v, want 30s", c.Timeout)
	}
	if c.Transport == nil {
		t.Error("expected non-nil Transport")
	}
}

func TestNewHTTPClient_WithTimeout(t *testing.T) {
	want := 5 * time.Second
	c, err := NewHTTPClient(WithTimeout(want))
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	if c.Timeout != want {
		t.Errorf("timeout = %v, want %v", c.Timeout, want)
	}
}

func TestNewHTTPClient_GetRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "hello")
	}))
	defer srv.Close()

	c, err := NewHTTPClient(WithTimeout(3 * time.Second))
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello" {
		t.Errorf("body = %q, want %q", string(body), "hello")
	}
}

func TestNewHTTPClient_DefaultUserAgentInjected(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := NewHTTPClient()
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()

	if !strings.HasPrefix(gotUA, "polypod/") {
		t.Errorf("User-Agent = %q, want prefix %q", gotUA, "polypod/")
	}
	if !strings.Contains(gotUA, runtime.GOOS) {
		t.Errorf("User-Agent = %q, missing GOOS %q", gotUA, runtime.GOOS)
	}
}

func TestNewHTTPClient_WithUserAgent(t *testing.T) {
	want := "custom-agent/1.2.3"
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := NewHTTPClient(WithUserAgent(want))
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()

	if gotUA != want {
		t.Errorf("User-Agent = %q, want %q", gotUA, want)
	}
}

func TestNewHTTPClient_UserAgentPreservedIfSet(t *testing.T) {
	preset := "preset-agent/9.9"
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := NewHTTPClient(WithUserAgent("should-not-win"))
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("User-Agent", preset)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	if gotUA != preset {
		t.Errorf("User-Agent = %q, want %q (preset must not be overridden)", gotUA, preset)
	}
}

func TestDefaultUserAgent_Format(t *testing.T) {
	ua := DefaultUserAgent()
	want := fmt.Sprintf("polypod/%s (%s; %s)", Version, runtime.GOOS, runtime.GOARCH)
	if ua != want {
		t.Errorf("DefaultUserAgent = %q, want %q", ua, want)
	}
}

// -------------------------------------------------------------------
// Retry-aware client tests
// -------------------------------------------------------------------

func TestNewHTTPClient_RetryOn5xxThenSuccess(t *testing.T) {
	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&count, 1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	c, err := NewHTTPClient(
		WithTimeout(5*time.Second),
		WithRetries(3, 5*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("final status = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&count); got != 3 {
		t.Errorf("server hits = %d, want 3", got)
	}
}

func TestNewHTTPClient_RetryExhaustedReturns5xx(t *testing.T) {
	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c, err := NewHTTPClient(
		WithTimeout(5*time.Second),
		WithRetries(2, 1*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	// max=2 means total attempts = 3 (initial + 2 retries)
	if got := atomic.LoadInt32(&count); got != 3 {
		t.Errorf("server hits = %d, want 3 (1 initial + 2 retries)", got)
	}
}

// TestNewHTTPClient_NoRetryOn429 documents current behavior: although
// isRetryable() classifies 429 as retryable, retryTransport.RoundTrip
// short-circuits on any StatusCode < 500, so 429 is NOT retried.
func TestNewHTTPClient_NoRetryOn429(t *testing.T) {
	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c, err := NewHTTPClient(WithRetries(3, 1*time.Millisecond))
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&count); got != 1 {
		t.Errorf("server hits = %d, want 1 (current impl does not retry <500)", got)
	}
}

func TestNewHTTPClient_NoRetryOn4xx(t *testing.T) {
	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c, err := NewHTTPClient(WithRetries(3, 1*time.Millisecond))
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&count); got != 1 {
		t.Errorf("server hits = %d, want 1 (4xx must not retry)", got)
	}
}

func TestNewHTTPClient_RetryContextCancel(t *testing.T) {
	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c, err := NewHTTPClient(WithRetries(10, 100*time.Millisecond))
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	_, err = c.Do(req)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

// -------------------------------------------------------------------
// isRetryable unit tests
// -------------------------------------------------------------------

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name string
		resp *http.Response
		err  error
		want bool
	}{
		{"500", &http.Response{StatusCode: 500}, nil, true},
		{"503", &http.Response{StatusCode: 503}, nil, true},
		{"429", &http.Response{StatusCode: 429}, nil, true},
		{"200", &http.Response{StatusCode: 200}, nil, false},
		{"404", &http.Response{StatusCode: 404}, nil, false},
		{"nil", nil, nil, false},
		{"timeout-err", nil, fmt.Errorf("dial tcp: i/o timeout"), true},
		{"conn-reset", nil, fmt.Errorf("read: connection reset by peer"), true},
		{"eof", nil, fmt.Errorf("unexpected EOF"), true},
		{"other-err", nil, fmt.Errorf("some other error"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRetryable(tc.resp, tc.err); got != tc.want {
				t.Errorf("isRetryable = %v, want %v", got, tc.want)
			}
		})
	}
}

// -------------------------------------------------------------------
// JSON request/response helpers (built on top of the client)
// -------------------------------------------------------------------

func TestJSONRoundTrip_ThroughServer(t *testing.T) {
	type reqBody struct {
		Name string `json:"name"`
	}
	type respBody struct {
		Greeting string `json:"greeting"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		var in reqBody
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(respBody{Greeting: "hello " + in.Name})
	}))
	defer srv.Close()

	c, err := NewHTTPClient(WithTimeout(3 * time.Second))
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}

	payload, _ := json.Marshal(reqBody{Name: "world"})
	req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(string(payload)))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	var out respBody
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Greeting != "hello world" {
		t.Errorf("greeting = %q, want %q", out.Greeting, "hello world")
	}
}

// -------------------------------------------------------------------
// BuildTLSConfig tests
// -------------------------------------------------------------------

func TestBuildTLSConfig_Nil(t *testing.T) {
	tc, err := BuildTLSConfig(nil)
	if err != nil {
		t.Fatalf("BuildTLSConfig(nil): %v", err)
	}
	if tc != nil {
		t.Errorf("expected nil tls.Config for nil input, got %#v", tc)
	}
}

func TestBuildTLSConfig_Empty(t *testing.T) {
	tc, err := BuildTLSConfig(&TLSConfig{})
	if err != nil {
		t.Fatalf("BuildTLSConfig({}): %v", err)
	}
	if tc == nil {
		t.Fatal("expected non-nil tls.Config")
	}
	if tc.MinVersion == 0 {
		t.Error("expected MinVersion to be set")
	}
}

func TestBuildTLSConfig_InvalidCAFile(t *testing.T) {
	_, err := BuildTLSConfig(&TLSConfig{CACertFiles: []string{"/nonexistent/ca.pem"}})
	if err == nil {
		t.Fatal("expected error for nonexistent CA file")
	}
}

func TestBuildTLSConfig_BadPEM(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/bad.pem"
	if err := os.WriteFile(path, []byte("not a pem"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := BuildTLSConfig(&TLSConfig{CACertFiles: []string{path}})
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}

// -------------------------------------------------------------------
// Proxy / URL-helper tests
// -------------------------------------------------------------------

func TestProxyConfig_ProxyFuncBypassesNoProxy(t *testing.T) {
	p := &ProxyConfig{URL: "http://proxy.corp:8080", NoProxy: "localhost,.internal"}
	pf := p.ProxyFunc()
	if pf == nil {
		t.Fatal("expected non-nil ProxyFunc")
	}

	// Matches noProxy -> should return nil URL (bypass)
	req, _ := http.NewRequest(http.MethodGet, "http://localhost/path", nil)
	u, err := pf(req)
	if err != nil {
		t.Fatalf("ProxyFunc: %v", err)
	}
	if u != nil {
		t.Errorf("expected nil (bypass) for localhost, got %v", u)
	}

	// Does NOT match noProxy -> should return proxy URL
	req, _ = http.NewRequest(http.MethodGet, "http://example.com/path", nil)
	u, err = pf(req)
	if err != nil {
		t.Fatalf("ProxyFunc: %v", err)
	}
	if u == nil || u.Host != "proxy.corp:8080" {
		t.Errorf("expected proxy.corp:8080, got %v", u)
	}
}

func TestProxyConfig_ProxyFuncNilWhenEmpty(t *testing.T) {
	if (&ProxyConfig{}).ProxyFunc() != nil {
		t.Error("expected nil ProxyFunc for empty URL")
	}
	if (*ProxyConfig)(nil).ProxyFunc() != nil {
		t.Error("expected nil ProxyFunc for nil receiver")
	}
}

func TestProxyConfig_ProxyFuncInvalidURL(t *testing.T) {
	p := &ProxyConfig{URL: "http://[::1:bad"}
	if p.ProxyFunc() != nil {
		t.Error("expected nil ProxyFunc for invalid URL")
	}
}

func TestShouldBypassProxy_Wildcard(t *testing.T) {
	if !ShouldBypassProxy("http://example.com", "*") {
		t.Error("wildcard * must bypass everything")
	}
}

func TestShouldBypassProxy_Empty(t *testing.T) {
	if ShouldBypassProxy("http://example.com", "") {
		t.Error("empty noProxy must not bypass")
	}
}

func TestShouldBypassProxy_Cases(t *testing.T) {
	tests := []struct {
		name, url, noProxy string
		want               bool
	}{
		{"exact-host", "http://localhost/path", "localhost", true},
		{"domain-suffix", "http://api.internal.corp", ".internal.corp", true},
		{"domain-suffix-exact", "http://internal.corp", ".internal.corp", true},
		{"no-match", "http://example.com", "other.com,foo.bar", false},
		{"multi-with-match", "http://foo.bar", "other.com, foo.bar ,baz", true},
		{"port-match", "http://api.example.com:8080", "api.example.com:8080", true},
		{"port-mismatch", "http://api.example.com:9090", "api.example.com:8080", false},
		{"cidr-match", "http://10.1.2.3", "10.0.0.0/8", true},
		{"cidr-no-match", "http://192.168.1.1", "10.0.0.0/8", false},
		{"ip-exact", "http://127.0.0.1", "127.0.0.1", true},
		{"case-insensitive", "http://EXAMPLE.com", "example.com", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldBypassProxy(tc.url, tc.noProxy); got != tc.want {
				t.Errorf("ShouldBypassProxy(%q, %q) = %v, want %v", tc.url, tc.noProxy, got, tc.want)
			}
		})
	}
}

func TestShouldBypassProxy_BadURL(t *testing.T) {
	// Malformed URL should be treated as non-bypass (no panic).
	if ShouldBypassProxy("http://[::1:bad", "localhost") {
		t.Error("bad URL should not bypass")
	}
}

// -------------------------------------------------------------------
// DetectProxy env tests
// -------------------------------------------------------------------

func TestDetectProxy_None(t *testing.T) {
	clearProxyEnv(t)
	if p := DetectProxy(); p != nil {
		t.Errorf("DetectProxy with no env = %#v, want nil", p)
	}
}

func TestDetectProxy_HTTPSPreferred(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HTTP_PROXY", "http://http-proxy:1111")
	t.Setenv("HTTPS_PROXY", "http://https-proxy:2222")
	t.Setenv("NO_PROXY", "localhost,.corp")

	p := DetectProxy()
	if p == nil {
		t.Fatal("expected non-nil ProxyConfig")
	}
	if p.URL != "http://https-proxy:2222" {
		t.Errorf("URL = %q, want https proxy", p.URL)
	}
	if p.NoProxy != "localhost,.corp" {
		t.Errorf("NoProxy = %q, want localhost,.corp", p.NoProxy)
	}
}

func TestDetectProxy_LowercaseHTTPSWins(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("https_proxy", "http://lower:3333")
	t.Setenv("HTTPS_PROXY", "http://upper:4444")

	p := DetectProxy()
	if p == nil || p.URL != "http://lower:3333" {
		t.Errorf("DetectProxy = %+v, want lowercase https_proxy to win", p)
	}
}

func TestDetectProxy_IntegrationWithClient(t *testing.T) {
	clearProxyEnv(t)

	// Spin up a fake "proxy" server. Any request will land here regardless
	// of the original URL because of transport.Proxy.
	var hits int32
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "via-proxy")
	}))
	defer proxySrv.Close()

	c, err := NewHTTPClient(
		WithTimeout(3*time.Second),
		WithProxy(&ProxyConfig{URL: proxySrv.URL}),
	)
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}

	// Use a bogus target; the proxy should intercept.
	resp, err := c.Get("http://nonexistent.invalid.test/")
	if err != nil {
		t.Fatalf("Get via proxy: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "via-proxy" {
		t.Errorf("body = %q, want %q", string(body), "via-proxy")
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Error("proxy server received 0 requests")
	}
}

// -------------------------------------------------------------------
// URL helper sanity: ensure httptest.Server URLs round-trip cleanly
// through url.Parse (documents assumption used elsewhere).
// -------------------------------------------------------------------

func TestHTTPTestServerURL_Parses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", srv.URL, err)
	}
	if u.Scheme != "http" {
		t.Errorf("scheme = %q, want http", u.Scheme)
	}
	if u.Host == "" {
		t.Error("host must not be empty")
	}
}

// -------------------------------------------------------------------
// helpers
// -------------------------------------------------------------------

func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"http_proxy", "HTTP_PROXY",
		"https_proxy", "HTTPS_PROXY",
		"no_proxy", "NO_PROXY",
	} {
		t.Setenv(k, "")
	}
}
