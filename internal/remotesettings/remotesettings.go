// Package remotesettings fetches, caches, and polls remote-managed settings
// from an enterprise endpoint. Ported from the TypeScript
// remoteManagedSettings service.
//
// The client uses ETag / If-None-Match caching to minimise network traffic and
// falls open on errors (returns the last cached value, or empty settings).
package remotesettings

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

// RemoteConfig holds the parameters needed to connect to the managed-settings
// endpoint.
type RemoteConfig struct {
	// Endpoint is the full URL to GET for settings (e.g.
	// "https://api.example.com/api/claude_code/settings").
	Endpoint string

	// PollInterval is how often StartPolling re-fetches.  Zero disables
	// periodic polling (Fetch can still be called manually).
	PollInterval time.Duration

	// AuthToken is sent as a Bearer token in the Authorization header.
	// Leave empty to skip auth.
	AuthToken string

	// Logger receives structured log messages.  If nil a no-op logger is
	// used.
	Logger *slog.Logger
}

// ---------------------------------------------------------------------------
// ManagedSettings
// ---------------------------------------------------------------------------

// ManagedSettings is the snapshot returned by a successful Fetch.
type ManagedSettings struct {
	// Settings maps setting keys to arbitrary JSON values.
	Settings map[string]any

	// LastFetch records when the settings were retrieved (or confirmed
	// unchanged via 304).
	LastFetch time.Time

	// ETag is the opaque validator returned by the server, forwarded back
	// as If-None-Match on subsequent requests.
	ETag string
}

// ---------------------------------------------------------------------------
// Client
// ---------------------------------------------------------------------------

// Client polls a remote endpoint for managed settings and caches the latest
// snapshot in memory.  It is safe for concurrent use.
type Client struct {
	cfg    RemoteConfig
	log    *slog.Logger
	http   *http.Client
	cancel context.CancelFunc // cancels the polling goroutine

	mu    sync.RWMutex
	cache *ManagedSettings
}

// NewClient creates a ready-to-use Client.  Call StartPolling to begin
// background refresh, or use Fetch for one-shot retrieval.
func NewClient(cfg RemoteConfig) *Client {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &Client{
		cfg:  cfg,
		log:  logger,
		http: &http.Client{Timeout: 10 * time.Second},
	}
}

// ---------------------------------------------------------------------------
// Fetch
// ---------------------------------------------------------------------------

// Fetch performs a single GET to the configured endpoint, parses the JSON
// response, and updates the in-memory cache.
//
// ETag / 304 handling: if a previous ETag is cached the request includes
// If-None-Match.  On 304 the existing cache is kept and returned.
//
// On 204 or 404 the cache is set to an empty map (the server reports no
// settings for this user).
//
// The returned *ManagedSettings is never nil on success.
func (c *Client) Fetch(ctx context.Context) (*ManagedSettings, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.Endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("remotesettings: build request: %w", err)
	}

	// Auth header.
	if c.cfg.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.AuthToken)
	}

	// ETag-based conditional GET.
	c.mu.RLock()
	cachedETag := ""
	if c.cache != nil {
		cachedETag = c.cache.ETag
	}
	c.mu.RUnlock()

	if cachedETag != "" {
		req.Header.Set("If-None-Match", cachedETag)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		c.log.Warn("remotesettings: fetch failed", "error", err)
		// Fail open: return cached value if available.
		c.mu.RLock()
		cached := c.cache
		c.mu.RUnlock()
		if cached != nil {
			return cached, nil
		}
		return nil, fmt.Errorf("remotesettings: fetch: %w", err)
	}
	defer resp.Body.Close()

	now := time.Now()

	switch resp.StatusCode {
	case http.StatusNotModified: // 304
		c.log.Debug("remotesettings: cache still valid (304)")
		c.mu.Lock()
		if c.cache != nil {
			c.cache.LastFetch = now
		}
		snap := c.cache
		c.mu.Unlock()
		if snap != nil {
			return snap, nil
		}
		// No prior cache — treat as empty.
		return c.storeEmpty(now, ""), nil

	case http.StatusNoContent, http.StatusNotFound: // 204, 404
		c.log.Debug("remotesettings: no settings from server", "status", resp.StatusCode)
		return c.storeEmpty(now, ""), nil

	case http.StatusOK: // 200
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("remotesettings: read body: %w", err)
		}

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("remotesettings: parse json: %w", err)
		}

		// Extract nested "settings" key if present (the TS API wraps
		// settings inside { uuid, checksum, settings }).
		if inner, ok := payload["settings"]; ok {
			if m, ok := inner.(map[string]any); ok {
				payload = m
			}
		}

		etag := resp.Header.Get("ETag")
		// Also accept checksum from the JSON body when the server does
		// not set an HTTP ETag header.
		if etag == "" {
			if cs, ok := payload["checksum"]; ok {
				if s, ok := cs.(string); ok {
					etag = s
				}
			}
		}

		ms := &ManagedSettings{
			Settings:  payload,
			LastFetch: now,
			ETag:      etag,
		}

		c.mu.Lock()
		c.cache = ms
		c.mu.Unlock()

		c.log.Debug("remotesettings: fetched successfully", "keys", len(payload))
		return ms, nil

	default:
		return nil, fmt.Errorf("remotesettings: unexpected status %d", resp.StatusCode)
	}
}

// storeEmpty replaces the cache with an empty settings map.
func (c *Client) storeEmpty(t time.Time, etag string) *ManagedSettings {
	ms := &ManagedSettings{
		Settings:  make(map[string]any),
		LastFetch: t,
		ETag:      etag,
	}
	c.mu.Lock()
	c.cache = ms
	c.mu.Unlock()
	return ms
}

// ---------------------------------------------------------------------------
// Polling
// ---------------------------------------------------------------------------

// StartPolling launches a background goroutine that calls Fetch at the
// configured PollInterval.  It performs an initial fetch immediately.
//
// If PollInterval is zero the goroutine returns after the first fetch.
// Calling StartPolling more than once is safe; subsequent calls are no-ops
// while a poller is active.
//
// Use the provided ctx (or call StopPolling) to stop the goroutine.
func (c *Client) StartPolling(ctx context.Context) {
	c.mu.Lock()
	if c.cancel != nil {
		c.mu.Unlock()
		return // already running
	}
	pollCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.mu.Unlock()

	go func() {
		// Initial fetch.
		if _, err := c.Fetch(pollCtx); err != nil {
			c.log.Warn("remotesettings: initial poll fetch failed", "error", err)
		}

		if c.cfg.PollInterval <= 0 {
			return
		}

		ticker := time.NewTicker(c.cfg.PollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-pollCtx.Done():
				return
			case <-ticker.C:
				if _, err := c.Fetch(pollCtx); err != nil {
					c.log.Warn("remotesettings: poll fetch failed", "error", err)
				}
			}
		}
	}()
}

// StopPolling cancels the background polling goroutine started by
// StartPolling.  It is safe to call even if polling was never started.
func (c *Client) StopPolling() {
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	c.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Accessors
// ---------------------------------------------------------------------------

// Get returns the value for a single cached setting key and a boolean
// indicating whether the key was present.
func (c *Client) Get(key string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.cache == nil {
		return nil, false
	}
	v, ok := c.cache.Settings[key]
	return v, ok
}

// Snapshot returns a shallow copy of all cached settings.  Returns an empty
// (non-nil) map when no settings have been fetched yet.
func (c *Client) Snapshot() map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make(map[string]any)
	if c.cache == nil {
		return out
	}
	for k, v := range c.cache.Settings {
		out[k] = v
	}
	return out
}
