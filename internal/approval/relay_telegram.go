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

	// BaseURL overrides the Bot API endpoint for tests or compatible gateways.
	// Empty uses the official Telegram endpoint.
	BaseURL string
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

	// pending maps approval IDs to their awaiting callers. textPending does
	// the same for free-form human questions.
	mu          sync.Mutex
	pending     map[string]chan Reply
	textPending map[string]chan string
	pollMu      sync.Mutex

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
		cfg:         cfg,
		log:         log,
		client:      &http.Client{Timeout: 30 * time.Second},
		pending:     make(map[string]chan Reply),
		textPending: make(map[string]chan string),
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

// SendNotification delivers a one-way operational notification through the
// configured relay. It deliberately does not expose a general chat adapter.
func (r *TelegramApprovalRelay) SendNotification(ctx context.Context, title, body string) error {
	if err := r.validateConfig(); err != nil {
		return err
	}
	var text strings.Builder
	if strings.TrimSpace(title) != "" {
		fmt.Fprintf(&text, "*%s*\n\n", escapeMarkdown(strings.TrimSpace(title)))
	}
	text.WriteString(escapeMarkdown(strings.TrimSpace(body)))
	return r.sendMessage(ctx, text.String())
}

// AskQuestion sends a correlated free-form question and waits for a reply in
// the form "resposta <id> <texto>" ("r" is also accepted). Only messages
// from the configured chat are considered.
func (r *TelegramApprovalRelay) AskQuestion(ctx context.Context, requestID, question string) (string, error) {
	if err := r.validateConfig(); err != nil {
		return "", err
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return "", fmt.Errorf("telegram relay: request ID obrigatorio")
	}
	if strings.TrimSpace(question) == "" {
		return "", fmt.Errorf("telegram relay: pergunta obrigatoria")
	}

	ch := make(chan string, 1)
	r.mu.Lock()
	if _, exists := r.textPending[requestID]; exists {
		r.mu.Unlock()
		return "", fmt.Errorf("telegram relay: request ID ja esta pendente")
	}
	r.textPending[requestID] = ch
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.textPending, requestID)
		r.mu.Unlock()
	}()

	var prompt strings.Builder
	prompt.WriteString("*Pergunta do Polypod*\n\n")
	prompt.WriteString(escapeMarkdown(strings.TrimSpace(question)))
	fmt.Fprintf(&prompt, "\n\nResponda com: `resposta %s sua resposta`", escapeMarkdown(requestID))
	if err := r.sendMessage(ctx, prompt.String()); err != nil {
		return "", err
	}

	pollCtx := ctx
	if r.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		pollCtx, cancel = context.WithTimeout(ctx, r.cfg.Timeout)
		defer cancel()
	}
	for {
		select {
		case <-pollCtx.Done():
			return "", pollCtx.Err()
		case answer := <-ch:
			return answer, nil
		default:
		}
		if err := r.pollAndDispatch(pollCtx, ch, nil); err != nil {
			if pollCtx.Err() != nil {
				return "", pollCtx.Err()
			}
			r.log.WarnContext(pollCtx, "telegram: polling failed; retrying")
			if err := waitContext(pollCtx, 2*time.Second); err != nil {
				return "", err
			}
		}
	}
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

		if err := r.pollAndDispatch(pollCtx, nil, ch); err != nil {
			if pollCtx.Err() != nil {
				return Reply{}, pollCtx.Err()
			}
			r.log.WarnContext(pollCtx, "telegram: polling failed; retrying")
			// Brief back-off before retrying to avoid hammering the API on
			// transient network errors.
			select {
			case <-pollCtx.Done():
				return Reply{}, pollCtx.Err()
			case <-time.After(2 * time.Second):
			}
			continue
		}

		// Check again after dispatch — our own update might have arrived.
		select {
		case reply := <-ch:
			return reply, nil
		default:
		}
	}
}

// pollAndDispatch serializes Bot API long polls. After acquiring the poll
// lock it rechecks the caller channel, avoiding an unnecessary 20-second poll
// when another waiter already dispatched the matching response.
func (r *TelegramApprovalRelay) pollAndDispatch(ctx context.Context, textCh <-chan string, approvalCh <-chan Reply) error {
	r.pollMu.Lock()
	defer r.pollMu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if textCh != nil {
		select {
		case answer := <-textCh:
			// Put the value back for the caller's normal receive path.
			r.mu.Lock()
			for _, ch := range r.textPending {
				if ch == textCh {
					ch <- answer
					break
				}
			}
			r.mu.Unlock()
			return nil
		default:
		}
	}
	if approvalCh != nil {
		select {
		case reply := <-approvalCh:
			r.mu.Lock()
			for _, ch := range r.pending {
				if ch == approvalCh {
					ch <- reply
					break
				}
			}
			r.mu.Unlock()
			return nil
		default:
		}
	}
	updates, err := r.getUpdates(ctx, 20)
	if err != nil {
		return err
	}
	r.dispatchUpdates(updates)
	return nil
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
		if !r.allowedChat(u.Message.Chat) {
			continue
		}
		text := strings.TrimSpace(u.Message.Text)
		if text == "" {
			continue
		}

		if answer, id, ok := parseTextReply(text); ok {
			if ch, waiting := r.textPending[id]; waiting {
				select {
				case ch <- answer:
				default:
				}
				continue
			}
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

func parseTextReply(text string) (answer, requestID string, ok bool) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) < 3 {
		return "", "", false
	}
	keyword := strings.ToLower(fields[0])
	if keyword != "resposta" && keyword != "reply" && keyword != "r" {
		return "", "", false
	}
	answer = strings.TrimSpace(strings.Join(fields[2:], " "))
	if answer == "" {
		return "", "", false
	}
	return answer, fields[1], true
}

func (r *TelegramApprovalRelay) allowedChat(chat *tgChat) bool {
	if chat == nil {
		return false
	}
	want := strings.TrimSpace(r.cfg.ChatID)
	if strings.HasPrefix(want, "@") {
		return strings.EqualFold(strings.TrimPrefix(want, "@"), strings.TrimPrefix(chat.Username, "@"))
	}
	return strconv.FormatInt(chat.ID, 10) == want
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
	if err := r.validateConfig(); err != nil {
		return err
	}
	apiURL := r.apiURL("sendMessage")

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
		return fmt.Errorf("telegram sendMessage: URL do relay invalida")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram sendMessage: falha na requisicao")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
		return fmt.Errorf("telegram sendMessage: status %d", resp.StatusCode)
	}

	var result tgAPIResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("telegram sendMessage: decode response: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("telegram sendMessage: API recusou a requisicao")
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

	apiURL := r.apiURL("getUpdates") + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("telegram getUpdates: URL do relay invalida")
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram getUpdates: falha na requisicao")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("telegram getUpdates: status %d", resp.StatusCode)
	}

	var result tgUpdatesResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("telegram getUpdates: decode: %w", err)
	}
	if !result.OK {
		return nil, fmt.Errorf("telegram getUpdates: API recusou a requisicao")
	}
	return result.Result, nil
}

func (r *TelegramApprovalRelay) validateConfig() error {
	if strings.TrimSpace(r.cfg.BotToken) == "" {
		return fmt.Errorf("telegram relay: token nao configurado")
	}
	if strings.TrimSpace(r.cfg.ChatID) == "" {
		return fmt.Errorf("telegram relay: chat ID nao configurado")
	}
	return nil
}

func (r *TelegramApprovalRelay) apiURL(method string) string {
	base := strings.TrimRight(strings.TrimSpace(r.cfg.BaseURL), "/")
	if base == "" {
		base = "https://api.telegram.org"
	}
	return fmt.Sprintf("%s/bot%s/%s", base, r.cfg.BotToken, method)
}

func waitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
	Chat      *tgChat `json:"chat,omitempty"`
	Text      string  `json:"text,omitempty"`
}

type tgChat struct {
	ID       int64  `json:"id"`
	Username string `json:"username,omitempty"`
}

type tgUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
}
