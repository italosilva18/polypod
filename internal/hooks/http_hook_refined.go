package hooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"
)

// HTTPHookSigner signs HTTP request bodies with HMAC-SHA256 using a shared secret.
// The signature is added as the X-Hook-Signature header in the format "sha256=<hex>".
type HTTPHookSigner struct {
	secret []byte
}

// NewHTTPHookSigner returns an HTTPHookSigner that uses the given secret.
func NewHTTPHookSigner(secret string) *HTTPHookSigner {
	return &HTTPHookSigner{secret: []byte(secret)}
}

// Sign computes the HMAC-SHA256 of body and sets the X-Hook-Signature header on req.
// body must be the exact bytes that will be sent as the request body.
func (s *HTTPHookSigner) Sign(req *http.Request, body []byte) error {
	mac := hmac.New(sha256.New, s.secret)
	if _, err := mac.Write(body); err != nil {
		return fmt.Errorf("hmac write: %w", err)
	}
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	req.Header.Set("X-Hook-Signature", sig)
	return nil
}

// RefinedHTTPHook is an HTTP hook sender with retry/backoff, optional HMAC signing,
// and 2xx response validation.
type RefinedHTTPHook struct {
	// URL is the endpoint to send hook payloads to.
	URL string

	// Method is the HTTP method (default: POST).
	Method string

	// Headers holds extra headers added to every request.
	Headers map[string]string

	// Signer, when non-nil, signs each request with HMAC-SHA256.
	Signer *HTTPHookSigner

	// MaxRetries is the maximum number of retry attempts after the first failure.
	// A value of 3 means up to 4 total attempts.
	MaxRetries int

	// InitialBackoff is the wait before the first retry (doubles on each attempt).
	InitialBackoff time.Duration

	// Timeout is the per-request HTTP timeout.
	Timeout time.Duration

	// Logger receives structured log entries for each attempt.
	Logger *slog.Logger
}

// NewRefinedHTTPHook returns a RefinedHTTPHook pointing at url, with sensible defaults:
//   - Method: POST
//   - MaxRetries: 3
//   - InitialBackoff: 200ms
//   - Timeout: 10s
//   - Logger: slog.Default()
func NewRefinedHTTPHook(url string) *RefinedHTTPHook {
	return &RefinedHTTPHook{
		URL:            url,
		Method:         "POST",
		MaxRetries:     3,
		InitialBackoff: 200 * time.Millisecond,
		Timeout:        10 * time.Second,
		Logger:         slog.Default(),
	}
}

// Send marshals payload to JSON and delivers it to h.URL.
// It retries on non-2xx responses or transport errors using exponential backoff.
func (h *RefinedHTTPHook) Send(ctx context.Context, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("refined http hook: marshal payload: %w", err)
	}
	return h.SendRaw(ctx, body, "application/json")
}

// SendRaw delivers a pre-serialized body to h.URL with the given Content-Type.
// It applies the same retry, signing, and validation logic as Send.
func (h *RefinedHTTPHook) SendRaw(ctx context.Context, body []byte, contentType string) error {
	method := h.Method
	if method == "" {
		method = "POST"
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	logger := h.Logger
	if logger == nil {
		logger = slog.Default()
	}

	maxAttempts := h.MaxRetries + 1
	backoff := h.InitialBackoff
	if backoff <= 0 {
		backoff = 200 * time.Millisecond
	}

	timeout := h.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	client := &http.Client{Timeout: timeout}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 200ms, 400ms, 800ms, ...
			wait := time.Duration(float64(backoff) * math.Pow(2, float64(attempt-1)))
			logger.Debug("refined http hook: backing off before retry",
				"attempt", attempt+1,
				"wait", wait,
				"url", h.URL,
			)
			select {
			case <-ctx.Done():
				return fmt.Errorf("refined http hook: context cancelled during backoff: %w", ctx.Err())
			case <-time.After(wait):
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, h.URL, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("refined http hook: build request: %w", err)
		}

		req.Header.Set("Content-Type", contentType)
		for k, v := range h.Headers {
			req.Header.Set(k, v)
		}

		if h.Signer != nil {
			if err := h.Signer.Sign(req, body); err != nil {
				return fmt.Errorf("refined http hook: sign request: %w", err)
			}
		}

		logger.Debug("refined http hook: sending",
			"attempt", attempt+1,
			"method", method,
			"url", h.URL,
			"body_bytes", len(body),
		)

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("attempt %d: %w", attempt+1, err)
			logger.Warn("refined http hook: request error",
				"attempt", attempt+1,
				"error", err,
			)
			if ctx.Err() != nil {
				return fmt.Errorf("refined http hook: context cancelled: %w", ctx.Err())
			}
			continue
		}

		// Drain and close body so the connection can be reused.
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
		resp.Body.Close()

		ok := resp.StatusCode >= 200 && resp.StatusCode < 300
		logger.Debug("refined http hook: response received",
			"attempt", attempt+1,
			"status", resp.StatusCode,
			"ok", ok,
			"body_bytes", len(respBody),
		)

		if ok {
			return nil
		}

		// Non-2xx — collect the error and retry (if attempts remain).
		bodyPreview := strings.TrimSpace(string(respBody))
		if len(bodyPreview) > 200 {
			bodyPreview = bodyPreview[:200] + "..."
		}
		if readErr != nil {
			lastErr = fmt.Errorf("attempt %d: status %d (read body: %v)", attempt+1, resp.StatusCode, readErr)
		} else {
			lastErr = fmt.Errorf("attempt %d: status %d: %s", attempt+1, resp.StatusCode, bodyPreview)
		}
		logger.Warn("refined http hook: non-2xx response",
			"attempt", attempt+1,
			"status", resp.StatusCode,
		)
	}

	return fmt.Errorf("refined http hook: all %d attempts failed, last error: %w", maxAttempts, lastErr)
}
