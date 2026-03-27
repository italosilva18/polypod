package retryux

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RetryInfo holds data about a retry attempt for display.
type RetryInfo struct {
	Attempt    int
	MaxRetries int
	WaitUntil  time.Time
	Reason     string
	Provider   string
}

// FormatCountdown returns a string like "Aguardando 1:25..." for display.
func FormatCountdown(info RetryInfo) string {
	remaining := time.Until(info.WaitUntil)
	if remaining <= 0 {
		return fmt.Sprintf("[%s] Tentando novamente... (tentativa %d/%d)", info.Provider, info.Attempt+1, info.MaxRetries)
	}

	secs := int(remaining.Seconds())
	mins := secs / 60
	secs = secs % 60

	if mins > 0 {
		return fmt.Sprintf("[%s] Aguardando %d:%02d... (%s, tentativa %d/%d)",
			info.Provider, mins, secs, info.Reason, info.Attempt, info.MaxRetries)
	}
	return fmt.Sprintf("[%s] Aguardando %ds... (%s, tentativa %d/%d)",
		info.Provider, secs, info.Reason, info.Attempt, info.MaxRetries)
}

// ClassifyError classifies an error for retry strategy.
func ClassifyError(err error, statusCode int) (category string, retryable bool, backoff time.Duration) {
	switch {
	case statusCode == 401 || statusCode == 403:
		return "auth", false, 0
	case statusCode == 429:
		return "rate_limit", true, 30 * time.Second
	case statusCode >= 500 && statusCode < 600:
		return "server_error", true, 5 * time.Second
	case statusCode == 0 && err != nil:
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline") {
			return "timeout", true, 10 * time.Second
		}
		if strings.Contains(msg, "connection refused") || strings.Contains(msg, "connection reset") {
			return "connection", true, 5 * time.Second
		}
		return "unknown", true, 5 * time.Second
	default:
		return "client_error", false, 0
	}
}

// CalculateBackoff returns the wait duration for a retry attempt with jitter.
func CalculateBackoff(attempt int, baseDelay time.Duration) time.Duration {
	delay := time.Duration(float64(baseDelay) * math.Pow(2, float64(attempt)))
	// Cap at 2 minutes
	if delay > 2*time.Minute {
		delay = 2 * time.Minute
	}
	// Add 10% jitter
	jitter := time.Duration(float64(delay) * 0.1)
	return delay + jitter
}

// ParseRetryAfter extracts wait duration from HTTP Retry-After header.
func ParseRetryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	header := resp.Header.Get("Retry-After")
	if header == "" {
		return 0
	}

	// Try as seconds
	if secs, err := strconv.Atoi(header); err == nil {
		return time.Duration(secs) * time.Second
	}

	// Try as HTTP date
	if t, err := http.ParseTime(header); err == nil {
		return time.Until(t)
	}

	return 0
}

// CategoryDescription returns a human-readable description for an error category.
func CategoryDescription(category string) string {
	switch category {
	case "auth":
		return "erro de autenticacao (verifique API key)"
	case "rate_limit":
		return "limite de taxa excedido"
	case "server_error":
		return "erro no servidor"
	case "timeout":
		return "timeout na conexao"
	case "connection":
		return "falha de conexao"
	case "context_overflow":
		return "context window excedido"
	default:
		return "erro desconhecido"
	}
}
