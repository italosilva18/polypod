package approval

// This file implements a channel-relay approval handler. When the permission
// enforcer needs a human decision, this handler forwards the prompt to an
// external channel (Telegram, Discord, Slack, generic webhook, ...) and
// races the remote reply against a local fallback handler (usually the CLI
// prompt). Whichever returns first wins.
//
// Mirrors the TS reference at
// src_extracted/src/services/mcp/channelPermissions.ts — specifically the
// "short 5-letter ID" format and the allow/deny reply contract. The Go
// version only needs the callback shape: the actual server-side parsing of
// "yes tbxkq" replies lives in whatever ChannelRelay implementation wires up
// the transport.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/costa/polypod/internal/permissions"
)

// -------------------------------------------------------------------
// ChannelRelay interface + payload types
// -------------------------------------------------------------------

// ChannelRelay abstracts an outbound/inbound permission channel. An
// implementation is responsible for delivering the prompt (SendApprovalRequest)
// and matching the human's reply back to a request ID (AwaitReply).
//
// Implementations must be safe for concurrent use — the handler may have
// several prompts in flight simultaneously.
type ChannelRelay interface {
	// SendApprovalRequest posts the prompt to the external channel. The
	// channel is responsible for rendering a human-friendly message that
	// tells the user how to reply (typically "y <id>" or "n <id>").
	SendApprovalRequest(ctx context.Context, req RelayRequest) error

	// AwaitReply blocks until a matching reply is available for the given
	// request ID, ctx is cancelled, or the relay gives up. The returned
	// Reply.Decision must be "allow" or "deny" — anything else is treated
	// as a protocol error by the handler.
	AwaitReply(ctx context.Context, requestID string) (Reply, error)
}

// RelayRequest is the payload sent to the channel. Deliberately flat and
// JSON-friendly so implementations can marshal it without extra plumbing.
type RelayRequest struct {
	ID             string   `json:"id"`
	ToolName       string   `json:"tool_name"`
	Risk           string   `json:"risk"`
	InputSummary   string   `json:"input_summary"`
	AllowedReplies []string `json:"allowed_replies"`
}

// Reply is the structured decision parsed out of the channel message.
type Reply struct {
	Decision  string `json:"decision"` // "allow" or "deny"
	Reason    string `json:"reason,omitempty"`
	RepliedBy string `json:"replied_by,omitempty"`
}

// -------------------------------------------------------------------
// Handler
// -------------------------------------------------------------------

// ChannelRelayApprovalHandler fulfils permissions.AskApprovalFn by racing a
// remote channel prompt against a local fallback handler. First winner's
// answer is used; the loser's work is cancelled via context.
type ChannelRelayApprovalHandler struct {
	relay    ChannelRelay
	fallback permissions.AskApprovalFn
	timeout  time.Duration
}

// NewChannelRelayApprovalHandler builds a handler that will prompt through
// the given relay, fall back to `fallback` on relay timeout/error, and cap
// the total relay race at `timeout`. A zero or negative timeout disables the
// timeout (relay only loses if it errors or the caller cancels).
func NewChannelRelayApprovalHandler(
	relay ChannelRelay,
	fallback permissions.AskApprovalFn,
	timeout time.Duration,
) *ChannelRelayApprovalHandler {
	return &ChannelRelayApprovalHandler{
		relay:    relay,
		fallback: fallback,
		timeout:  timeout,
	}
}

// raceResult is an internal tagged union for the select below.
type raceResult struct {
	source   string // "relay" or "fallback"
	response permissions.ApprovalResponse
	err      error
}

// AskApproval satisfies permissions.AskApprovalFn. It runs the remote relay
// and the local fallback concurrently and returns the first usable answer.
// If the relay errors or times out, the fallback's answer is awaited and
// returned. If both error, the relay error wins (it's usually the more
// actionable one — "channel not configured", "server disconnected", etc).
func (h *ChannelRelayApprovalHandler) AskApproval(
	ctx context.Context,
	req permissions.ApprovalRequest,
) (permissions.ApprovalResponse, error) {
	// No relay wired up → degrade to the fallback. Keeps callers that
	// always construct a handler but sometimes have nothing to relay to
	// from needing a nil check.
	if h.relay == nil {
		if h.fallback == nil {
			return permissions.ApprovalResponse{}, errors.New("channel-relay: no relay or fallback configured")
		}
		return h.fallback(ctx, req)
	}

	// A scoped context so the loser of the race is cancelled as soon as
	// the winner's answer is committed.
	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Optional hard deadline on the relay leg only. The fallback uses the
	// original (but still cancellable) context so that e.g. a CLI prompt
	// isn't cut off by the relay timeout if it's the one answering.
	relayCtx := raceCtx
	if h.timeout > 0 {
		var relayCancel context.CancelFunc
		relayCtx, relayCancel = context.WithTimeout(raceCtx, h.timeout)
		defer relayCancel()
	}

	relayCh := make(chan raceResult, 1)
	fallbackCh := make(chan raceResult, 1)

	// Relay leg: send the prompt, then block on the reply.
	go func() {
		id := GenerateRequestID()
		relayReq := RelayRequest{
			ID:             id,
			ToolName:       req.ToolName,
			Risk:           req.Risk.Level.String(),
			InputSummary:   summarizeArgs(req.Args),
			AllowedReplies: []string{"y " + id, "n " + id},
		}
		if err := h.relay.SendApprovalRequest(relayCtx, relayReq); err != nil {
			relayCh <- raceResult{source: "relay", err: fmt.Errorf("relay send: %w", err)}
			return
		}
		reply, err := h.relay.AwaitReply(relayCtx, id)
		if err != nil {
			relayCh <- raceResult{source: "relay", err: fmt.Errorf("relay await: %w", err)}
			return
		}
		resp, err := replyToResponse(reply)
		relayCh <- raceResult{source: "relay", response: resp, err: err}
	}()

	// Fallback leg: only runs if a fallback is actually configured. If
	// there's no fallback and the relay loses, we still want to surface
	// the relay error, so we just leave fallbackCh silent.
	if h.fallback != nil {
		go func() {
			resp, err := h.fallback(raceCtx, req)
			fallbackCh <- raceResult{source: "fallback", response: resp, err: err}
		}()
	}

	// Wait for the first usable answer. Policy:
	//   - A successful relay answer wins immediately.
	//   - A relay error does NOT win; we wait for the fallback.
	//   - A successful fallback answer wins immediately.
	//   - If both fail, surface the relay error (more actionable).
	var relayErr error
	var relayDone, fallbackDone bool
	for {
		select {
		case <-ctx.Done():
			return permissions.ApprovalResponse{}, ctx.Err()

		case r := <-relayCh:
			relayDone = true
			if r.err == nil {
				return r.response, nil
			}
			relayErr = r.err
			// Relay lost. If there's no fallback at all, return the
			// relay error now — nothing else is coming.
			if h.fallback == nil {
				return permissions.ApprovalResponse{}, relayErr
			}
			if fallbackDone {
				// Both finished and both failed; already handled below,
				// but defensive.
				return permissions.ApprovalResponse{}, relayErr
			}
			// Otherwise keep waiting for the fallback.

		case r := <-fallbackCh:
			fallbackDone = true
			if r.err == nil {
				return r.response, nil
			}
			// Fallback failed. If the relay also already failed, return
			// the relay error (more actionable). Otherwise wait for the
			// relay to finish — it might still succeed.
			if relayDone {
				if relayErr != nil {
					return permissions.ApprovalResponse{}, relayErr
				}
				return permissions.ApprovalResponse{}, r.err
			}
		}
	}
}

// replyToResponse converts the relay's structured decision into the
// permissions package response shape.
func replyToResponse(reply Reply) (permissions.ApprovalResponse, error) {
	switch strings.ToLower(strings.TrimSpace(reply.Decision)) {
	case "allow", "y", "yes":
		return permissions.ApprovalResponse{
			Allowed:  true,
			Feedback: reply.Reason,
		}, nil
	case "deny", "n", "no":
		return permissions.ApprovalResponse{
			Allowed:  false,
			Feedback: reply.Reason,
		}, nil
	default:
		return permissions.ApprovalResponse{}, fmt.Errorf(
			"channel-relay: unrecognized decision %q (expected allow|deny)", reply.Decision,
		)
	}
}

// summarizeArgs renders a compact JSON preview of the tool arguments,
// truncated to roughly a phone-screen width. Mirrors the TS
// `truncateForPreview` helper — we send a summary, not the full payload,
// because the channel may be SMS-sized.
func summarizeArgs(args map[string]any) string {
	if len(args) == 0 {
		return "{}"
	}
	b, err := json.Marshal(args)
	if err != nil {
		return "(unserializable)"
	}
	const maxLen = 200
	if len(b) > maxLen {
		return string(b[:maxLen]) + "..."
	}
	return string(b)
}

// -------------------------------------------------------------------
// Request ID generation
// -------------------------------------------------------------------

// idAlphabet is the alphabet used for short request IDs: lowercase letters
// only, no vowels (avoids accidental words — see the blocklist comment in
// channelPermissions.ts), no 'l' (looks like 1/I), no 'y' (acts as a vowel
// often enough). This is 18 consonants, so 18^5 ≈ 1.9M distinct IDs.
const idAlphabet = "bcdfghjkmnpqrstvwxz"

// idLength is the length of a generated request ID.
const idLength = 5

// GenerateRequestID returns a 5-letter ID drawn from a consonant-only
// alphabet. Letters only so phone users don't fight the keyboard, no vowels
// so the ID can't accidentally spell anything, no 'l' so it can't be
// confused with a digit. Uses crypto/rand for uniform draws; on the
// (extraordinarily unlikely) event that crypto/rand fails, falls back to a
// deterministic time-derived mix so the caller never sees an empty ID.
func GenerateRequestID() string {
	var b strings.Builder
	b.Grow(idLength)
	max := big.NewInt(int64(len(idAlphabet)))
	for i := 0; i < idLength; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			// Degraded fallback — still letters-only, still
			// valid-shaped, just not cryptographically random.
			return fallbackRequestID()
		}
		b.WriteByte(idAlphabet[n.Int64()])
	}
	return b.String()
}

// fallbackRequestID is the crypto/rand-unavailable escape hatch. Deterministic
// per nanosecond, still inside the alphabet.
func fallbackRequestID() string {
	var b strings.Builder
	b.Grow(idLength)
	n := time.Now().UnixNano()
	for i := 0; i < idLength; i++ {
		b.WriteByte(idAlphabet[int(n)%len(idAlphabet)])
		n /= int64(len(idAlphabet))
		if n == 0 {
			n = time.Now().UnixNano() ^ int64(i+1)
		}
	}
	return b.String()
}

// -------------------------------------------------------------------
// NoopRelay — default wiring / tests
// -------------------------------------------------------------------

// NoopRelay is a ChannelRelay that never actually sends anything and never
// returns a reply. It's intended as:
//
//  1. A placeholder in wiring code ("channel relay feature off → plug in a
//     NoopRelay so the handler shape stays the same").
//  2. A helper in tests that want to force the race to the fallback leg.
//
// SendApprovalRequest is a silent no-op. AwaitReply blocks until the context
// is cancelled and then returns that cancellation error, so the relay leg
// of the race will always lose to any fallback that answers at all.
type NoopRelay struct{}

// SendApprovalRequest is a no-op.
func (NoopRelay) SendApprovalRequest(_ context.Context, _ RelayRequest) error {
	return nil
}

// AwaitReply blocks until ctx is done and returns ctx.Err().
func (NoopRelay) AwaitReply(ctx context.Context, _ string) (Reply, error) {
	<-ctx.Done()
	return Reply{}, ctx.Err()
}
