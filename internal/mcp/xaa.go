// Package mcp — Cross-App Access (XAA) provider.
//
// XAA is Claude's mechanism for obtaining a long-lived MCP access token
// without a browser consent screen, by exchanging a short-lived claude.ai
// session token at a configured exchange endpoint. The flow is:
//
//  1. Client holds a short-lived claude.ai token (e.g. from a device-grant
//     login or an injected session token).
//  2. Client POSTs that token to the XAA exchange endpoint together with
//     the target MCP server audience and the minimum set of scopes it
//     needs to call the server.
//  3. The exchange endpoint returns an OAuth 2.0 compliant token response
//     (access_token, token_type, expires_in, scope) — that becomes the
//     per-server MCP access token.
//  4. If the MCP server later rejects a request with
//     `WWW-Authenticate: Bearer error="insufficient_scope"`, the client
//     detects this as a step-up, re-runs the exchange asking for the
//     additional scopes, and retries the request.
//
// The reference TS implementation lives at
// src/services/mcp/xaa.ts (layer-2 `requestJwtAuthorizationGrant` +
// `exchangeJwtAuthGrant`, layer-3 `performCrossAppAccess`). Since the
// actual claude.ai exchange endpoint is not public, this Go port is
// structural: it posts to whatever URL is configured and expects an
// OAuth 2.0 token response. The focus is on getting the types and flow
// in place so higher layers (client.go, oauth_flow.go) can depend on
// a stable shape.
//
// This file deliberately does NOT touch oauth.go, oauth_flow.go,
// client.go, streamablehttp.go, or transport.go. It is purely additive.
package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ─── Provider types ─────────────────────────────────────────────────────────

// XAAProvider is a minimal Cross-App Access provider: it holds the
// caller-supplied claude.ai token and knows where to exchange it for
// per-server MCP access tokens. It is intentionally small — one
// provider instance is typically created per Claude session and then
// shared across MCP servers via an XAACache.
type XAAProvider struct {
	// ClaudeToken is the short-lived claude.ai session token supplied
	// by the caller. Treated as opaque — the provider never inspects it.
	ClaudeToken string
	// ExchangeURL is the full URL of the XAA token-exchange endpoint.
	// In production this is a claude.ai-hosted endpoint; in tests it
	// points at an httptest.Server.
	ExchangeURL string
	// HTTPClient is the HTTP client used for the exchange request. If
	// nil, a client with a 30s timeout is constructed lazily.
	HTTPClient *http.Client
}

// XAATokenRequest is the JSON body POSTed to the exchange endpoint.
// The field names match the wire format (snake_case) so this struct
// can be marshalled directly.
type XAATokenRequest struct {
	ClaudeToken    string   `json:"claude_token"`
	ServerAudience string   `json:"audience"`
	Scopes         []string `json:"scopes,omitempty"`
}

// XAATokenResponse is the OAuth 2.0 token response returned by the
// exchange endpoint. IssuedAt is a local field populated on parse so
// callers can compute expiry without re-reading the HTTP Date header.
type XAATokenResponse struct {
	AccessToken string    `json:"access_token"`
	TokenType   string    `json:"token_type"`
	ExpiresIn   int       `json:"expires_in"`
	Scope       string    `json:"scope"`
	IssuedAt    time.Time `json:"-"`
}

// ExpiresAt returns the absolute expiry time for this token, or the
// zero value if ExpiresIn is unset.
func (r *XAATokenResponse) ExpiresAt() time.Time {
	if r == nil || r.ExpiresIn <= 0 {
		return time.Time{}
	}
	base := r.IssuedAt
	if base.IsZero() {
		base = time.Now()
	}
	return base.Add(time.Duration(r.ExpiresIn) * time.Second)
}

// NewXAAProvider constructs a provider with a default HTTP client
// (30s timeout). Callers that need custom transports can set
// HTTPClient on the returned value.
func NewXAAProvider(claudeToken, exchangeURL string) *XAAProvider {
	return &XAAProvider{
		ClaudeToken: claudeToken,
		ExchangeURL: exchangeURL,
		HTTPClient:  &http.Client{Timeout: 30 * time.Second},
	}
}

// ExchangeForServer POSTs an XAATokenRequest to the configured
// ExchangeURL and parses the OAuth 2.0 token response. `serverName`
// is used as the audience and is echoed back in any StepUpError so
// callers can attribute failures to a specific MCP server.
//
// Non-2xx responses are mapped to errors. 401/403 responses carrying
// a `WWW-Authenticate: Bearer error="insufficient_scope"` header are
// converted to *StepUpError via DetectStepUpFromResponse so the caller
// can re-invoke with additional scopes.
func (p *XAAProvider) ExchangeForServer(
	ctx context.Context,
	serverName string,
	scopes []string,
) (*XAATokenResponse, error) {
	if p == nil {
		return nil, errors.New("xaa: nil provider")
	}
	if p.ClaudeToken == "" {
		return nil, errors.New("xaa: empty claude token")
	}
	if p.ExchangeURL == "" {
		return nil, errors.New("xaa: empty exchange URL")
	}

	body := XAATokenRequest{
		ClaudeToken:    p.ClaudeToken,
		ServerAudience: serverName,
		Scopes:         scopes,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("xaa: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.ExchangeURL, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("xaa: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// Authenticate the exchange call itself with the claude.ai token
	// as a bearer. The wire body also carries it for servers that
	// prefer body-auth — mirrors the TS reference which accepts both.
	req.Header.Set("Authorization", "Bearer "+p.ClaudeToken)

	client := p.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("xaa: exchange request: %w", err)
	}
	defer resp.Body.Close()

	const maxBody = 1 << 20 // 1 MiB, plenty for token responses
	buf := &bytes.Buffer{}
	if _, err := buf.ReadFrom(&limitedReader{R: resp.Body, N: maxBody}); err != nil {
		return nil, fmt.Errorf("xaa: read response: %w", err)
	}
	respBody := buf.Bytes()

	if resp.StatusCode >= 400 {
		// Surface step-up specifically so callers can unwrap via
		// IsStepUpRequired and retry with the required scopes.
		if stepUp := detectStepUpFromHeaders(resp.Header, respBody, serverName); stepUp != nil {
			return nil, stepUp
		}
		snippet := string(respBody)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, fmt.Errorf("xaa: exchange failed: HTTP %d: %s", resp.StatusCode, snippet)
	}

	var out XAATokenResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("xaa: decode response: %w", err)
	}
	if out.AccessToken == "" {
		return nil, errors.New("xaa: exchange response missing access_token")
	}
	if out.TokenType == "" {
		out.TokenType = "Bearer"
	}
	out.IssuedAt = time.Now()
	return &out, nil
}

// limitedReader is a tiny local alternative to io.LimitReader that
// avoids pulling in the io package just for this one use (and makes
// the size cap obvious at the call site).
type limitedReader struct {
	R interface {
		Read(p []byte) (int, error)
	}
	N int
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.N <= 0 {
		return 0, errReaderExhausted
	}
	if len(p) > l.N {
		p = p[:l.N]
	}
	n, err := l.R.Read(p)
	l.N -= n
	return n, err
}

var errReaderExhausted = errors.New("xaa: response body exceeds 1 MiB limit")

// ─── Step-up detection ─────────────────────────────────────────────────────

// StepUpError is returned when an MCP server (or the exchange
// endpoint on its behalf) reports that the current access token is
// missing required scopes. Callers should re-run the exchange with
// Required merged into the previously requested scopes.
type StepUpError struct {
	// Required is the set of scopes the server says it needs. Parsed
	// from the `scope="..."` challenge parameter (space-separated per
	// RFC 6750 §3).
	Required []string
	// ServerName is the audience / server identifier so callers can
	// log which MCP server issued the challenge.
	ServerName string
}

// Error satisfies the error interface.
func (e *StepUpError) Error() string {
	if e == nil {
		return "xaa: step-up required"
	}
	if len(e.Required) == 0 {
		return fmt.Sprintf("xaa: step-up required for server %q", e.ServerName)
	}
	return fmt.Sprintf(
		"xaa: step-up required for server %q: need scopes [%s]",
		e.ServerName,
		strings.Join(e.Required, " "),
	)
}

// IsStepUpRequired walks the error chain looking for a *StepUpError
// (including errors wrapped via fmt.Errorf("%w", ...)). Returns the
// unwrapped value and true on hit; nil, false otherwise.
func IsStepUpRequired(err error) (*StepUpError, bool) {
	if err == nil {
		return nil, false
	}
	var stepUp *StepUpError
	if errors.As(err, &stepUp) {
		return stepUp, true
	}
	return nil, false
}

// wwwAuthScopeRE extracts the `scope="..."` parameter from a
// WWW-Authenticate challenge. Per RFC 6750 §3 the value is a
// space-separated scope list; parameter ordering is unspecified so
// we can't anchor on position.
var wwwAuthScopeRE = regexp.MustCompile(`(?i)scope\s*=\s*"([^"]*)"`)

// wwwAuthErrorRE extracts the `error="..."` parameter so we can
// check for the insufficient_scope sentinel.
var wwwAuthErrorRE = regexp.MustCompile(`(?i)error\s*=\s*"([^"]*)"`)

// DetectStepUpFromResponse inspects a raw HTTP response (status +
// body) and returns a *StepUpError when the server signals
// insufficient_scope via a `WWW-Authenticate: Bearer` challenge or a
// JSON body field. This overload is useful for callers that only
// have the raw body (e.g. a streamable HTTP transport that already
// consumed the headers).
//
// Since this variant has no access to response headers, it inspects
// the body for `error`/`scope` keys. Prefer passing the full
// http.Header via detectStepUpFromHeaders when available.
func DetectStepUpFromResponse(statusCode int, body []byte) error {
	if statusCode < 400 {
		return nil
	}
	// Try parsing as a JSON OAuth error envelope:
	//   {"error":"insufficient_scope","scope":"read write"}
	var envelope struct {
		Error string `json:"error"`
		Scope string `json:"scope"`
	}
	if len(body) > 0 && json.Unmarshal(body, &envelope) == nil {
		if strings.EqualFold(envelope.Error, "insufficient_scope") {
			return &StepUpError{
				Required: splitScopes(envelope.Scope),
			}
		}
	}
	// Fallback: the body might literally contain a WWW-Authenticate
	// header echoed as text (some proxies do this).
	if m := wwwAuthErrorRE.FindSubmatch(body); m != nil &&
		strings.EqualFold(string(m[1]), "insufficient_scope") {
		scopes := ""
		if sm := wwwAuthScopeRE.FindSubmatch(body); sm != nil {
			scopes = string(sm[1])
		}
		return &StepUpError{Required: splitScopes(scopes)}
	}
	return nil
}

// detectStepUpFromHeaders is the header-aware form used internally by
// ExchangeForServer. It checks `WWW-Authenticate` first, then falls
// back to the body parser. `serverName` is stamped onto the returned
// error for attribution.
func detectStepUpFromHeaders(h http.Header, body []byte, serverName string) *StepUpError {
	for _, challenge := range h.Values("WWW-Authenticate") {
		if !strings.Contains(strings.ToLower(challenge), "bearer") {
			continue
		}
		errMatch := wwwAuthErrorRE.FindStringSubmatch(challenge)
		if errMatch == nil || !strings.EqualFold(errMatch[1], "insufficient_scope") {
			continue
		}
		scopes := ""
		if sm := wwwAuthScopeRE.FindStringSubmatch(challenge); sm != nil {
			scopes = sm[1]
		}
		return &StepUpError{
			Required:   splitScopes(scopes),
			ServerName: serverName,
		}
	}
	if bodyErr := DetectStepUpFromResponse(http.StatusUnauthorized, body); bodyErr != nil {
		if stepUp, ok := bodyErr.(*StepUpError); ok {
			stepUp.ServerName = serverName
			return stepUp
		}
	}
	return nil
}

// splitScopes tokenises an OAuth scope string (space-separated per
// RFC 6749 §3.3), dropping empty elements.
func splitScopes(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Fields(s)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ─── Token cache ────────────────────────────────────────────────────────────

// XAACache is a thread-safe cache of exchanged tokens keyed by the
// tuple (server, scopes). Scope order is normalised before hashing
// so `[read write]` and `[write read]` hit the same entry.
//
// Entries expire based on XAATokenResponse.ExpiresAt() with a small
// safety margin — callers that want zero risk of handing out a
// just-expired token can subtract additional seconds when reading.
type XAACache struct {
	mu      sync.RWMutex
	entries map[string]*xaaCacheEntry
	// safetyMargin is subtracted from a token's expiry when checking
	// for staleness. Defaults to 30s.
	safetyMargin time.Duration
	// now is a clock function for deterministic tests.
	now func() time.Time
}

type xaaCacheEntry struct {
	resp      *XAATokenResponse
	expiresAt time.Time
}

// NewXAACache constructs an empty cache with a 30-second safety margin.
func NewXAACache() *XAACache {
	return &XAACache{
		entries:      make(map[string]*xaaCacheEntry),
		safetyMargin: 30 * time.Second,
		now:          time.Now,
	}
}

// cacheKey normalises (server, scopes) into a stable string suitable
// for map lookup. Scopes are sorted and SHA-256 hashed to keep the
// key short and avoid embedding user-supplied strings directly.
func cacheKey(server string, scopes []string) string {
	sorted := append([]string(nil), scopes...)
	sort.Strings(sorted)
	h := sha256.New()
	h.Write([]byte(server))
	h.Write([]byte{0})
	for _, s := range sorted {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Get returns a cached, non-expired token for (server, scopes) and
// true on hit; nil, false otherwise. Expired entries are left in
// place — Put overwrites them — so concurrent callers don't race on
// deletion during a refresh.
func (c *XAACache) Get(server string, scopes []string) (*XAATokenResponse, bool) {
	if c == nil {
		return nil, false
	}
	key := cacheKey(server, scopes)
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || entry == nil {
		return nil, false
	}
	if !entry.expiresAt.IsZero() && !c.now().Before(entry.expiresAt) {
		return nil, false
	}
	return entry.resp, true
}

// Put stores a token response for (server, scopes). Expiry is
// derived from resp.ExpiresAt() minus the cache's safety margin; if
// ExpiresAt is zero the entry is cached indefinitely (the server
// didn't tell us when it expires, so we trust it until overwritten).
func (c *XAACache) Put(server string, scopes []string, resp *XAATokenResponse) {
	if c == nil || resp == nil {
		return
	}
	expiry := resp.ExpiresAt()
	if !expiry.IsZero() && c.safetyMargin > 0 {
		expiry = expiry.Add(-c.safetyMargin)
	}
	key := cacheKey(server, scopes)
	c.mu.Lock()
	c.entries[key] = &xaaCacheEntry{resp: resp, expiresAt: expiry}
	c.mu.Unlock()
}

// ─── Integration notes ──────────────────────────────────────────────────────
//
// Higher layers are expected to wire XAA in as follows:
//
//  1. On startup, construct a single XAAProvider from the user's
//     claude.ai session token + the configured exchange URL, plus a
//     shared XAACache.
//  2. Before each MCP request that needs auth, call
//     cache.Get(server, scopes). On miss, call
//     provider.ExchangeForServer(ctx, server, scopes) and Put the
//     result back into the cache.
//  3. On a 401/403 from the MCP server, call DetectStepUpFromResponse
//     on the response body (or check errors.As for *StepUpError if
//     the transport already surfaced it). If IsStepUpRequired reports
//     true, merge stepUp.Required into the requested scopes, clear
//     the matching cache entry, and retry.
//
// This file is self-contained and does not import any other mcp/*.go
// symbols. See oauth_flow.go for the 401-retry wrapper that would
// compose with this provider.
