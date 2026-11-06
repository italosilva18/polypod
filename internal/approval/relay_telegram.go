package approval

// relay_telegram.go — ChannelRelay implementation that uses the Telegram Bot API
// over raw HTTP (no third-party SDK).
//
// Flow:
//   1. SendApprovalRequest → POST /sendMessage with a human-readable prompt.
//   2. AwaitReply          → long-poll /getUpdates until the user sends a message
//      whose text contains one of the recognised keywords ("sim", "yes" → allow;
//      "nao", "não", "no" → deny) followed by the request ID.
//
// Timeout is managed by the caller via context; internally we use 20-second
// long-poll windows so goroutines don't hang inside a single HTTP call longer
// than necessary.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// -------------------------------------------------------------------
// Config
// -------------------------------------------------------------------

// TelegramRelayConfig holds the settings needed to connect to the Bot API.
type TelegramRelayConfig struct {
	// BotToken is the token issued by @BotFather (e.g. "123456:ABC-DEF…").
	BotToken string

	// ChatID is the Telegram chat ID to send messages to and read replies from.
	// Stored as a string for flexibility (supports both numeric IDs and
	// @username channel handles that start with "@").
	ChatID string

	// Timeout caps the total time spent waiting for a human reply. A zero
	// value means "no extra deadline beyond whatever the caller's context
	// carries". The internal long-poll window is always 20 s; this just caps
	// how many polling rounds we do.
	Timeout time.Duration

	// Logger is the structured logger to use. If nil, slog.Default() is used.
	Logger *slog.Logger
}

// -------------------------------------------------------------------
// TelegramApprovalRelay
// -------------------------------------------------------------------

// TelegramApprovalRelay implements ChannelRelay using the Telegram Bot API.
// It is safe for concurrent use: multiple ApprovalRequests may be in flight
// simultaneously, and each AwaitReply call independently scans incoming
// updates for its own request ID.
type TelegramApprovalRelay struct {
	cfg    TelegramRelayConfig
	log    *slog.Logger
	client *http.Client

	// pending maps a request ID to the channel that AwaitReply is blocking on.
	mu      sync.Mutex
	pending map[string]chan Reply

	// offset is the Telegram update offset, advanced as we consume updates.
	// Protected by mu.
	offset int64
}

// NewTelegramApprovalRelay builds a relay ready for use. The returned relay
// does not start any background goroutines; polling happens inside AwaitReply.
func NewTelegramApprovalRelay(cfg TelegramRelayConfig) *TelegramApprovalRelay {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &TelegramApprovalRelay{
		cfg:     cfg,
		log:     log,
		client:  &http.Client{Timeout: 30 * time.Second},
		pending: make(map[string]chan Reply),
	}
}

// -------------------------------------------------------------------
// ChannelRelay implementation
// -------------------------------------------------------------------

// SendApprovalRequest posts a human-readable approval prompt to the configured
// Telegram chat. It returns an error if the Bot API call fails.
func (r *TelegramApprovalRelay) SendApprovalRequest(ctx context.Context, req RelayRequest) error {
	text := buildPromptText(req)
	r.log.InfoContext(ctx, "telegram: sending approval request",
		"request_id", req.ID,
		"tool", req.ToolName,
	)
	return r.sendMessage(ctx, text)
}

// AwaitReply long-polls Telegram getUpdates until a message arrives that
// contains a recognised keyword ("sim"/"yes" → allow; "nao"/"não"/"no" →
// deny) along with the given requestID, or until ctx is cancelled.
//
// Multiple concurrent AwaitReply calls are supported. Each call registers its
// request ID in the pending map; the first call in a given process acts as the
// poller and distributes matching updates to whichever AwaitReply registered
// interest. If there is no concurrent poller, each call polls itself.
func (r *TelegramApprovalRelay) AwaitReply(ctx context.Context, requestID string) (Reply, error) {
	ch := make(chan Reply, 1)

	r.mu.Lock()
	r.pending[requestID] = ch
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		delete(r.pending, requestID)
		r.mu.Unlock()
	}()

	// Apply per-relay timeout on top of whatever the caller provided.
	pollCtx := ctx
	if r.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		pollCtx, cancel = context.WithTimeout(ctx, r.cfg.Timeout)
		defer cancel()
	}

	for {
		select {
		case <-pollCtx.Done():
			return Reply{}, pollCtx.Err()
		case reply := <-ch:
			return reply, nil
		default:
		}

		updates, err := r.getUpdates(pollCtx, 20)
		if err != nil {
			if pollCtx.Err() != nil {
				return Reply{}, pollCtx.Err()
			}
			r.log.WarnContext(pollCtx, "telegram: getUpdates error, retrying", "error", err)
			// Brief back-off before retrying to avoid hammering the API on
			// transient network errors.
			select {
			case <-pollCtx.Done():
				return Reply{}, pollCtx.Err()
			case <-time.After(2 * time.Second):
			}
			continue
		}

		r.dispatchUpdates(updates)

		// Check again after dispatch — our own update might have arrived.
		select {
		case reply := <-ch:
			return reply, nil
		default:
		}
	}
}

// -------------------------------------------------------------------
// Internal helpers
// -------------------------------------------------------------------

// dispatchUpdates scans the received Telegram updates and delivers any
// matching replies to the registered pending channels.
func (r *TelegramApprovalRelay) dispatchUpdates(updates []tgUpdate) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, u := range updates {
		// Advance the offset so we don't re-process this update.
		if u.UpdateID >= r.offset {
			r.offset = u.UpdateID + 1
		}

		if u.Message == nil {
			continue
		}
		text := strings.TrimSpace(u.Message.Text)
		if text == "" {
			continue
		}

		decision, id, ok := parseReplyText(text)
		if !ok {
			continue
		}

		ch, waiting := r.pending[id]
		if !waiting {
			continue
		}

		repliedBy := ""
		if u.Message.From != nil {
			repliedBy = u.Message.From.Username
			if repliedBy == "" {
				repliedBy = strings.TrimSpace(u.Message.From.FirstName + " " + u.Message.From.LastName)
			}
		}

		select {
		case ch <- Reply{
			Decision:  decision,
			RepliedBy: repliedBy,
		}:
		default:
			// Channel already has a reply (shouldn't happen, but be defensive).
		}
	}
}

// parseReplyText extracts a decision and request ID from a raw Telegram
// message. Recognised formats:
//
//	"sim <id>"   / "yes <id>"          → allow
//	"nao <id>"   / "não <id>" / "no <id>" → deny
//
// The keyword and ID may be separated by whitespace and/or punctuation.
// The comparison is case-insensitive.
func parseReplyText(text string) (decision, requestID string, ok bool) {
	lower := strings.ToLower(text)
	fields := strings.Fields(lower)
	if len(fields) < 2 {
		return "", "", false
	}

	keyword := fields[0]
	id := fields[1]

	switch keyword {
	case "sim", "yes", "s", "y":
		return "allow", id, true
	case "nao", "não", "no", "n":
		return "deny", id, true
	}
	return "", "", false
}

// buildPromptText formats a human-readable Telegram message for the given
// approval request.
func buildPromptText(req RelayRequest) string {
	var b strings.Builder
	b.WriteString("*Aprovacao necessaria*\n\n")
	fmt.Fprintf(&b, "Ferramenta: `%s`\n", escapeMarkdown(req.ToolName))
	fmt.Fprintf(&b, "Risco: %s\n", escapeMarkdown(req.Risk))
	if req.InputSummary != "" {
		fmt.Fprintf(&b, "Parametros: `%s`\n", escapeMarkdown(req.InputSummary))
	}
	b.WriteString("\nResponda com:\n")
	fmt.Fprintf(&b, "  `sim %s` — para aprovar\n", req.ID)
	fmt.Fprintf(&b, "  `nao %s` — para negar\n", req.ID)
	return b.String()
}

// escapeMarkdown escapes characters that have special meaning in Telegram's
// MarkdownV1 mode (the simpler of the two supported by the Bot API).
func escapeMarkdown(s string) string {
	replacer := strings.NewReplacer(
		"`", "\\`",
		"*", "\\*",
		"_", "\\_",
		"[", "\\[",
	)
	return replacer.Replace(s)
}

// sendMessage calls the Telegram Bot API sendMessage endpoint.
func (r *TelegramApprovalRelay) sendMessage(ctx context.Context, text string) error {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", r.cfg.BotToken)

	payload := map[string]string{
		"chat_id":    r.cfg.ChatID,
		"text":       text,
		"parse_mode": "Markdown",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("telegram sendMessage: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram sendMessage: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram sendMessage: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("telegram sendMessage: status %d: %s", resp.StatusCode, raw)
	}

	var result tgAPIResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("telegram sendMessage: decode response: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("telegram sendMessage: api error: %s", result.Description)
	}
	return nil
}

// getUpdates calls the Telegram Bot API getUpdates endpoint with a long-poll
// timeout of `longPollSecs` seconds. It advances r.offset automatically.
func (r *TelegramApprovalRelay) getUpdates(ctx context.Context, longPollSecs int) ([]tgUpdate, error) {
	r.mu.Lock()
	offset := r.offset
	r.mu.Unlock()

	params := url.Values{}
	params.Set("offset", strconv.FormatInt(offset, 10))
	params.Set("timeout", strconv.Itoa(longPollSecs))
	params.Set("allowed_updates", `["message"]`)

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?%s",
		r.cfg.BotToken, params.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("telegram getUpdates: build request: %w", err)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram getUpdates: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("telegram getUpdates: status %d: %s", resp.StatusCode, raw)
	}

	var result tgUpdatesResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("telegram getUpdates: decode: %w", err)
	}
	if !result.OK {
		return nil, fmt.Errorf("telegram getUpdates: api error: %s", result.Description)
	}
	return result.Result, nil
}

// -------------------------------------------------------------------
// Telegram Bot API response types (minimal subset)
// -------------------------------------------------------------------

type tgAPIResult struct {
	OK          bool   `json:"ok"`
	Description string `json:"description,omitempty"`
}

type tgUpdatesResult struct {
	OK          bool       `json:"ok"`
	Description string     `json:"description,omitempty"`
	Result      []tgUpdate `json:"result"`
}

type tgUpdate struct {
	UpdateID int64      `json:"update_id"`
	Message  *tgMessage `json:"message,omitempty"`
}

type tgMessage struct {
	MessageID int64   `json:"message_id"`
	From      *tgUser `json:"from,omitempty"`
	Text      string  `json:"text,omitempty"`
}

type tgUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
}
