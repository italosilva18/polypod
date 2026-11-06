package approval

// relay_discord.go — DiscordApprovalRelay implements ChannelRelay using the
// Discord REST API v10 (raw HTTP, no third-party Discord SDK).
//
// Flow:
//  1. SendApprovalRequest — POSTs a message to the configured channel.
//  2. AwaitReply — polls the same channel for new messages via GET with
//     "after=<last_seen_id>", parses the first message that contains one of
//     the recognised approval keywords, and returns a Reply.
//
// Keywords (case-insensitive, anywhere in the message body):
//   - approve:  "sim", "yes", "y"
//   - deny:     "não", "nao", "no", "n"

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	discordAPIBase      = "https://discord.com/api/v10"
	discordPollInterval = 2 * time.Second
)

// -------------------------------------------------------------------
// Config & constructor
// -------------------------------------------------------------------

// DiscordRelayConfig holds everything DiscordApprovalRelay needs to talk to
// Discord. All fields are required.
type DiscordRelayConfig struct {
	// BotToken is the Discord bot token (starts with "Bot ").
	// The relay automatically prepends "Bot " if not present.
	BotToken string

	// ChannelID is the numeric snowflake ID of the Discord text channel to
	// post approval prompts to and poll for replies.
	ChannelID string

	// Timeout is the maximum wall-clock time AwaitReply will wait for a human
	// reply before returning an error. Zero means no additional timeout beyond
	// the context passed to AwaitReply.
	Timeout time.Duration

	// Logger is the structured logger. If nil, slog.Default() is used.
	Logger *slog.Logger
}

// DiscordApprovalRelay is a ChannelRelay that delivers approval requests via
// Discord messages and polls the same channel for yes/no replies.
type DiscordApprovalRelay struct {
	cfg    DiscordRelayConfig
	client *http.Client
	token  string // normalised "Bot <token>"
	log    *slog.Logger
}

// NewDiscordApprovalRelay returns a ready-to-use DiscordApprovalRelay.
func NewDiscordApprovalRelay(cfg DiscordRelayConfig) *DiscordApprovalRelay {
	tok := cfg.BotToken
	if !strings.HasPrefix(tok, "Bot ") {
		tok = "Bot " + tok
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &DiscordApprovalRelay{
		cfg:    cfg,
		client: &http.Client{Timeout: 10 * time.Second},
		token:  tok,
		log:    log,
	}
}

// -------------------------------------------------------------------
// ChannelRelay implementation
// -------------------------------------------------------------------

// SendApprovalRequest posts the approval prompt to the Discord channel.
func (r *DiscordApprovalRelay) SendApprovalRequest(ctx context.Context, req RelayRequest) error {
	body := buildDiscordMessage(req)
	return r.postMessage(ctx, body)
}

// AwaitReply polls the Discord channel for a reply containing "sim"/"yes" or
// "nao"/"no". It respects ctx cancellation and the optional Timeout from config.
func (r *DiscordApprovalRelay) AwaitReply(ctx context.Context, _ string) (Reply, error) {
	// Apply the relay-level timeout on top of whatever the caller provided.
	pollCtx := ctx
	if r.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		pollCtx, cancel = context.WithTimeout(ctx, r.cfg.Timeout)
		defer cancel()
	}

	// Anchor: only look at messages that appear after this moment.
	// We fetch the latest message ID first so we don't match old messages.
	anchorID, err := r.latestMessageID(pollCtx)
	if err != nil {
		// Non-fatal: start polling from "nothing before" — worst case we pick
		// up a stale message, but that's better than hanging.
		r.log.Warn("discord relay: could not fetch anchor message ID", "error", err)
		anchorID = "0"
	}

	ticker := time.NewTicker(discordPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-pollCtx.Done():
			return Reply{}, pollCtx.Err()

		case <-ticker.C:
			reply, found, err := r.scanMessages(pollCtx, anchorID)
			if err != nil {
				r.log.Warn("discord relay: poll error", "error", err)
				continue
			}
			if found {
				return reply, nil
			}
			// No matching reply yet — keep polling, but advance the anchor
			// so we skip already-scanned messages next round. The scan returns
			// the latest message ID it saw when !found.
		}
	}
}

// -------------------------------------------------------------------
// Discord API helpers
// -------------------------------------------------------------------

// discordMessage is the JSON body for POST /channels/{id}/messages.
type discordMessage struct {
	Content string `json:"content"`
}

// discordReceivedMessage is the shape we care about from GET /channels/{id}/messages.
type discordReceivedMessage struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Author  struct {
		Username string `json:"username"`
		Bot      bool   `json:"bot"`
	} `json:"author"`
}

// buildDiscordMessage formats a human-readable prompt from the RelayRequest.
func buildDiscordMessage(req RelayRequest) string {
	var sb strings.Builder
	sb.WriteString("**Approval required** (ID: `")
	sb.WriteString(req.ID)
	sb.WriteString("`)\n")
	sb.WriteString("**Tool:** `")
	sb.WriteString(req.ToolName)
	sb.WriteString("`\n")
	sb.WriteString("**Risk:** ")
	sb.WriteString(req.Risk)
	sb.WriteString("\n")
	if req.InputSummary != "" {
		sb.WriteString("**Input:** ```\n")
		sb.WriteString(req.InputSummary)
		sb.WriteString("\n```\n")
	}
	sb.WriteString("Reply **sim** / **yes** to allow or **não** / **no** to deny.")
	return sb.String()
}

// postMessage sends a plain-text Discord message to the configured channel.
func (r *DiscordApprovalRelay) postMessage(ctx context.Context, content string) error {
	payload, err := json.Marshal(discordMessage{Content: content})
	if err != nil {
		return fmt.Errorf("discord relay: marshal message: %w", err)
	}

	url := fmt.Sprintf("%s/channels/%s/messages", discordAPIBase, r.cfg.ChannelID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("discord relay: build POST request: %w", err)
	}
	req.Header.Set("Authorization", r.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("discord relay: POST message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("discord relay: POST message: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

// latestMessageID returns the snowflake ID of the most recent message in the
// channel, or "0" if there are no messages.
func (r *DiscordApprovalRelay) latestMessageID(ctx context.Context) (string, error) {
	msgs, err := r.fetchMessages(ctx, "0", 1)
	if err != nil {
		return "0", err
	}
	if len(msgs) == 0 {
		return "0", nil
	}
	return msgs[0].ID, nil
}

// scanMessages fetches messages posted after afterID and returns the first one
// that contains an approval keyword. Also returns the highest message ID seen
// (for advancing the polling anchor).
func (r *DiscordApprovalRelay) scanMessages(ctx context.Context, afterID string) (Reply, bool, error) {
	msgs, err := r.fetchMessages(ctx, afterID, 50)
	if err != nil {
		return Reply{}, false, err
	}

	for i := len(msgs) - 1; i >= 0; i-- { // oldest first
		m := msgs[i]
		if m.Author.Bot {
			continue // skip our own messages and other bots
		}
		lower := strings.ToLower(strings.TrimSpace(m.Content))
		if isApproveKeyword(lower) {
			return Reply{
				Decision:  "allow",
				RepliedBy: m.Author.Username,
			}, true, nil
		}
		if isDenyKeyword(lower) {
			return Reply{
				Decision:  "deny",
				RepliedBy: m.Author.Username,
			}, true, nil
		}
	}
	return Reply{}, false, nil
}

// fetchMessages calls GET /channels/{id}/messages?after={afterID}&limit={limit}.
// Returns messages in Discord's default order (newest first).
func (r *DiscordApprovalRelay) fetchMessages(ctx context.Context, afterID string, limit int) ([]discordReceivedMessage, error) {
	url := fmt.Sprintf("%s/channels/%s/messages?limit=%d", discordAPIBase, r.cfg.ChannelID, limit)
	if afterID != "" && afterID != "0" {
		url += "&after=" + afterID
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("discord relay: build GET request: %w", err)
	}
	req.Header.Set("Authorization", r.token)

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discord relay: GET messages: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("discord relay: GET messages: status %d: %s", resp.StatusCode, body)
	}

	var msgs []discordReceivedMessage
	if err := json.NewDecoder(resp.Body).Decode(&msgs); err != nil {
		return nil, fmt.Errorf("discord relay: decode messages: %w", err)
	}
	return msgs, nil
}

// -------------------------------------------------------------------
// Keyword helpers
// -------------------------------------------------------------------

// isApproveKeyword reports whether s (already lowercased/trimmed) signals approval.
func isApproveKeyword(s string) bool {
	switch s {
	case "sim", "yes", "y":
		return true
	}
	// Also match if the message starts with these words (e.g. "yes please").
	for _, kw := range []string{"sim ", "yes ", "y "} {
		if strings.HasPrefix(s, kw) {
			return true
		}
	}
	return false
}

// isDenyKeyword reports whether s (already lowercased/trimmed) signals denial.
func isDenyKeyword(s string) bool {
	switch s {
	case "não", "nao", "no", "n":
		return true
	}
	for _, kw := range []string{"não ", "nao ", "no ", "n "} {
		if strings.HasPrefix(s, kw) {
			return true
		}
	}
	return false
}
