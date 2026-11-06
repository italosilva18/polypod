// Package mcp — OAuth 401 retry + token refresh wrapper.
//
// This file adds a small, single-responsibility helper that callers can wrap
// around any MCP request that needs an OAuth access token. When the wrapped
// request fails with HTTP 401 Unauthorized, OAuthRetryClient:
//
//  1. Invokes a pluggable TokenRefresher to obtain a fresh access token.
//  2. Persists the fresh token to a pluggable TokenStore.
//  3. Retries the original request exactly once with the new token.
//
// The design mirrors the TypeScript reference at
// src/services/mcp/client.ts (createClaudeAiProxyFetch / handleOAuth401Error),
// which gates retry on an actual token change to avoid double round-trips
// against servers that legitimately need re-authentication.
//
// This file intentionally does NOT touch streamablehttp.go, transport.go,
// client.go, or oauth.go. Wiring is described at the bottom of this file.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ─── Errors ─────────────────────────────────────────────────────────────────

// ErrUnauthorized401 is the canonical sentinel for MCP requests that failed
// with HTTP 401 Unauthorized. OAuthRetryClient uses errors.Is against this
// value (and against ErrStreamableHTTPUnauthorized, defined in
// streamablehttp.go) to decide whether to attempt a token refresh + retry.
//
// Callers that build their own transports can wrap this sentinel with
// fmt.Errorf("%w: ...", ErrUnauthorized401, ...) to opt into the retry logic.
var ErrUnauthorized401 = errors.New("mcp oauth: 401 unauthorized")

// ─── Interfaces ─────────────────────────────────────────────────────────────

// TokenRefresher performs a token refresh against an OAuth authorization
// server. Implementations are free to use a refresh_token grant, a
// client_credentials grant, or any custom mechanism — OAuthRetryClient only
// cares about the returned access token and its expiry.
type TokenRefresher interface {
	Refresh(ctx context.Context) (accessToken string, expiresAt time.Time, err error)
}

// TokenStore persists per-server OAuth access tokens. It is deliberately
// narrower than the existing TokenStorage interface in oauth.go so that
// OAuthRetryClient can be used with minimal plumbing (no OAuthTokens struct,
// no refresh-token bookkeeping — the refresher owns that).
//
// Implementations MUST be safe for concurrent use from multiple goroutines.
type TokenStore interface {
	GetAccessToken(ctx context.Context, server string) (string, error)
	SetAccessToken(ctx context.Context, server, token string, expires time.Time) error
}

// ─── Config ─────────────────────────────────────────────────────────────────

// OAuthRetryConfig configures an OAuthRetryClient.
type OAuthRetryConfig struct {
	// Store provides the current access token for a server and accepts
	// updates after a successful refresh. Required.
	Store TokenStore

	// Refresher obtains a new access token when a 401 is observed.
	// Required for retry to happen; if nil, Execute returns the original
	// 401 error without attempting a refresh.
	Refresher TokenRefresher

	// MaxRetries caps the number of refresh+retry attempts. Zero means
	// "use the default", which is 1 — matching the TS reference, which
	// retries at most once per request.
	MaxRetries int

	// RetryBackoff is the sleep inserted between the 401 and the retry.
	// Zero means "retry immediately". Useful to paper over clock skew on
	// token introspection endpoints.
	RetryBackoff time.Duration
}

// ─── Client ─────────────────────────────────────────────────────────────────

// OAuthRetryClient wraps arbitrary token-bearing MCP calls with
// refresh-on-401 semantics. It holds no persistent connection state and is
// safe for concurrent use across goroutines.
type OAuthRetryClient struct {
	cfg    OAuthRetryConfig
	server string
}

// NewOAuthRetryClient constructs a retry client for a single MCP server.
// The server string is used as the key for TokenStore lookups and for
// contextual error messages.
func NewOAuthRetryClient(server string, cfg OAuthRetryConfig) *OAuthRetryClient {
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 1
	}
	return &OAuthRetryClient{cfg: cfg, server: server}
}

// Server returns the server identifier this client was constructed with.
func (c *OAuthRetryClient) Server() string { return c.server }

// Execute runs fn with the current access token for the configured server.
// If fn returns a 401-class error, Execute asks the refresher for a new
// token, stores it, and retries fn up to MaxRetries times. If the final
// attempt still fails, Execute returns that last error.
//
// Non-401 errors are returned immediately without any refresh attempt.
func (c *OAuthRetryClient) Execute(ctx context.Context, fn func(token string) error) error {
	if fn == nil {
		return errors.New("mcp oauth: Execute requires a non-nil fn")
	}
	if c.cfg.Store == nil {
		return errors.New("mcp oauth: OAuthRetryClient requires a TokenStore")
	}

	token, err := c.cfg.Store.GetAccessToken(ctx, c.server)
	if err != nil {
		// Not fatal on its own — fn may be happy with an empty token
		// (e.g. a first-time request that will itself 401 and trigger
		// the initial refresh).
		token = ""
	}

	lastErr := fn(token)
	if lastErr == nil {
		return nil
	}
	if !IsUnauthorizedError(lastErr) {
		return lastErr
	}

	// 401 observed: attempt refresh + retry up to MaxRetries times.
	if c.cfg.Refresher == nil {
		return fmt.Errorf("mcp oauth: 401 from %q and no refresher configured: %w", c.server, lastErr)
	}

	for attempt := 0; attempt < c.cfg.MaxRetries; attempt++ {
		if c.cfg.RetryBackoff > 0 {
			select {
			case <-time.After(c.cfg.RetryBackoff):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		newToken, expires, refreshErr := c.cfg.Refresher.Refresh(ctx)
		if refreshErr != nil {
			return fmt.Errorf("mcp oauth: refresh for %q failed: %w (original: %v)", c.server, refreshErr, lastErr)
		}
		if newToken == "" {
			return fmt.Errorf("mcp oauth: refresh for %q returned empty token (original: %w)", c.server, lastErr)
		}

		if storeErr := c.cfg.Store.SetAccessToken(ctx, c.server, newToken, expires); storeErr != nil {
			return fmt.Errorf("mcp oauth: store token for %q: %w", c.server, storeErr)
		}

		lastErr = fn(newToken)
		if lastErr == nil {
			return nil
		}
		if !IsUnauthorizedError(lastErr) {
			// A post-refresh error that is no longer 401 is a real
			// downstream failure — surface it verbatim.
			return lastErr
		}
	}

	return fmt.Errorf("mcp oauth: retries exhausted for %q: %w", c.server, lastErr)
}

// ─── 401 detection ──────────────────────────────────────────────────────────

// IsUnauthorizedError reports whether err represents an HTTP 401 / OAuth
// unauthorized response. It recognises:
//
//   - The exported sentinel ErrUnauthorized401.
//   - The streamable-http sentinel ErrStreamableHTTPUnauthorized (defined
//     in streamablehttp.go) via errors.Is.
//   - Error messages that mention "401", "Unauthorized", or
//     "unauthenticated" (case-insensitive), which covers the JSON-RPC
//     error bodies returned by most MCP servers.
//
// The substring fallback exists because a number of MCP implementations
// return the 401 as a JSON-RPC error wrapped in a plain Go error, with no
// sentinel to match against.
func IsUnauthorizedError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrUnauthorized401) {
		return true
	}
	if errors.Is(err, ErrStreamableHTTPUnauthorized) {
		return true
	}

	// Try a typed unwrap for anything exposing HTTPStatus() int.
	type httpStatuser interface{ HTTPStatus() int }
	var hs httpStatuser
	if errors.As(err, &hs) && hs.HTTPStatus() == 401 {
		return true
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "401"):
		return true
	case strings.Contains(msg, "unauthorized"):
		return true
	case strings.Contains(msg, "unauthenticated"):
		return true
	case strings.Contains(msg, "invalid_token"):
		return true
	case strings.Contains(msg, "token expired"):
		return true
	}
	return false
}

// ─── In-memory token store ──────────────────────────────────────────────────

// memoryTokenEntry is the value side of MemoryTokenStore's map.
type memoryTokenEntry struct {
	token   string
	expires time.Time
}

// MemoryTokenStore is a simple, thread-safe, in-memory implementation of
// TokenStore. It is primarily useful for tests and for short-lived CLI
// sessions that don't need durable token persistence.
//
// For durable storage, implement TokenStore against the keychain / a file
// in the user's config dir / a database. The contract is intentionally
// tiny to make alternate implementations easy.
type MemoryTokenStore struct {
	mu      sync.RWMutex
	entries map[string]memoryTokenEntry
}

// NewMemoryTokenStore constructs an empty in-memory TokenStore.
func NewMemoryTokenStore() *MemoryTokenStore {
	return &MemoryTokenStore{entries: make(map[string]memoryTokenEntry)}
}

// GetAccessToken returns the currently stored access token for server, or
// an empty string (with no error) if none has been set. Returning nil on
// "not found" keeps the caller's happy path simple: an empty token is a
// perfectly valid starting state for the first request.
func (s *MemoryTokenStore) GetAccessToken(_ context.Context, server string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.entries[server]
	if !ok {
		return "", nil
	}
	return entry.token, nil
}

// SetAccessToken replaces the stored token for server. A zero expires time
// means "unknown expiry" and is stored verbatim.
func (s *MemoryTokenStore) SetAccessToken(_ context.Context, server, token string, expires time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[server] = memoryTokenEntry{token: token, expires: expires}
	return nil
}

// Expiry returns the stored expiry for server, useful for tests.
func (s *MemoryTokenStore) Expiry(server string) (time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.entries[server]
	if !ok {
		return time.Time{}, false
	}
	return entry.expires, true
}

// ─── Wiring note for streamablehttp.go (informational only) ─────────────────
//
// streamablehttp.go already exports ErrStreamableHTTPUnauthorized and its
// Send method wraps the HTTP 401 with that sentinel. To adopt
// OAuthRetryClient without editing that file, wrap Send calls at the
// caller layer (e.g. client.go / connection.go) like this:
//
//	retry := mcp.NewOAuthRetryClient(serverName, mcp.OAuthRetryConfig{
//	    Store:     store,
//	    Refresher: refresher,
//	})
//	err := retry.Execute(ctx, func(token string) error {
//	    transport.cfg.BearerToken = token // or rebuild transport with token
//	    return transport.Send(ctx, msg)
//	})
//
// Because errors.Is(err, ErrStreamableHTTPUnauthorized) is true for 401s
// bubbled up from Send, IsUnauthorizedError will detect them and trigger a
// refresh. No changes to streamablehttp.go are required.
