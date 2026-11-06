// Package apipreconnect pre-warms HTTP connections to AI provider APIs at
// startup. Sending a lightweight HEAD request overlaps TCP+TLS handshake
// (~100-200 ms) with application initialization so the first real API call
// reuses an already-established connection from Go's default transport pool.
package apipreconnect

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Target describes a single endpoint to pre-warm.
type Target struct {
	Name   string // human-readable label (used as map key in results)
	URL    string // full URL to connect to
	Method string // HTTP method; defaults to HEAD when empty
}

// method returns the effective HTTP method, defaulting to HEAD.
func (t Target) method() string {
	if t.Method == "" {
		return http.MethodHead
	}
	return t.Method
}

// PreconnectConfig holds options for a preconnect run.
type PreconnectConfig struct {
	Targets []Target
	Timeout time.Duration // per-target timeout; defaults to 5s when zero
	Logger  *slog.Logger  // optional; nil silences all logging
}

// timeout returns the effective timeout, defaulting to 5 seconds.
func (c PreconnectConfig) timeout() time.Duration {
	if c.Timeout <= 0 {
		return 5 * time.Second
	}
	return c.Timeout
}

// logger returns the effective logger, falling back to a discard logger.
func (c PreconnectConfig) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.New(slog.DiscardHandler)
}

// DefaultTargets returns preconnect targets for the major AI provider APIs.
func DefaultTargets() []Target {
	return []Target{
		{Name: "anthropic", URL: "https://api.anthropic.com"},
		{Name: "openai", URL: "https://api.openai.com"},
		{Name: "google", URL: "https://generativelanguage.googleapis.com"},
		{Name: "groq", URL: "https://api.groq.com"},
	}
}

// Preconnect concurrently sends HEAD (or the configured method) requests to
// every target in cfg.Targets. It returns a map of target name -> error for
// targets that failed; successful targets are omitted from the map. A nil
// map means every target succeeded.
func Preconnect(ctx context.Context, cfg PreconnectConfig) map[string]error {
	if len(cfg.Targets) == 0 {
		return nil
	}

	timeout := cfg.timeout()
	log := cfg.logger()

	type result struct {
		name string
		err  error
	}

	results := make(chan result, len(cfg.Targets))

	var wg sync.WaitGroup
	for _, t := range cfg.Targets {
		wg.Add(1)
		go func(t Target) {
			defer wg.Done()

			tctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			req, err := http.NewRequestWithContext(tctx, t.method(), t.URL, nil)
			if err != nil {
				log.Debug("preconnect: bad request", "target", t.Name, "err", err)
				results <- result{name: t.Name, err: err}
				return
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				log.Debug("preconnect: failed", "target", t.Name, "err", err)
				results <- result{name: t.Name, err: err}
				return
			}
			resp.Body.Close()

			log.Debug("preconnect: ok", "target", t.Name, "status", resp.StatusCode)
			results <- result{name: t.Name, err: nil}
		}(t)
	}

	// Close the channel once all goroutines finish.
	go func() {
		wg.Wait()
		close(results)
	}()

	var errs map[string]error
	for r := range results {
		if r.err != nil {
			if errs == nil {
				errs = make(map[string]error)
			}
			errs[r.name] = r.err
		}
	}
	return errs
}

// PreconnectAsync is a fire-and-forget variant of Preconnect. It launches
// all connection warming in a background goroutine and returns immediately.
// Any errors are logged (if a Logger is configured) but otherwise discarded.
func PreconnectAsync(ctx context.Context, cfg PreconnectConfig) {
	go Preconnect(ctx, cfg) //nolint:errcheck
}
