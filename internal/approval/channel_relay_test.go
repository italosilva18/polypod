package approval

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/costa/polypod/internal/permissions"
)

// -------------------------------------------------------------------
// Test doubles
// -------------------------------------------------------------------

// fakeRelay is a programmable ChannelRelay. Set `reply` + `delay` to simulate
// a channel that responds; set `sendErr` or `awaitErr` to simulate failure;
// leave everything zero to simulate a channel that never replies (it will
// then only return when its ctx is cancelled, which is exactly what the
// timeout path exercises).
type fakeRelay struct {
	reply    Reply
	delay    time.Duration
	sendErr  error
	awaitErr error

	mu          sync.Mutex
	sentCount   int
	lastRequest RelayRequest
}

func (r *fakeRelay) SendApprovalRequest(_ context.Context, req RelayRequest) error {
	r.mu.Lock()
	r.sentCount++
	r.lastRequest = req
	r.mu.Unlock()
	return r.sendErr
}

func (r *fakeRelay) AwaitReply(ctx context.Context, _ string) (Reply, error) {
	if r.awaitErr != nil {
		return Reply{}, r.awaitErr
	}
	// Never-returning relay: delay == 0 means "block until ctx is done".
	if r.delay == 0 && r.reply.Decision == "" {
		<-ctx.Done()
		return Reply{}, ctx.Err()
	}
	select {
	case <-time.After(r.delay):
		return r.reply, nil
	case <-ctx.Done():
		return Reply{}, ctx.Err()
	}
}

// blockingFallback is an AskApprovalFn that never answers until its ctx is
// cancelled. Used when we want the relay to be the only possible winner.
func blockingFallback(ctx context.Context, _ permissions.ApprovalRequest) (permissions.ApprovalResponse, error) {
	<-ctx.Done()
	return permissions.ApprovalResponse{}, ctx.Err()
}

// fixedFallback returns a preset response after an optional delay.
func fixedFallback(resp permissions.ApprovalResponse, delay time.Duration) permissions.AskApprovalFn {
	return func(ctx context.Context, _ permissions.ApprovalRequest) (permissions.ApprovalResponse, error) {
		if delay == 0 {
			return resp, nil
		}
		select {
		case <-time.After(delay):
			return resp, nil
		case <-ctx.Done():
			return permissions.ApprovalResponse{}, ctx.Err()
		}
	}
}

func sampleRequest() permissions.ApprovalRequest {
	return permissions.ApprovalRequest{
		ToolName: "Bash",
		Args:     map[string]any{"command": "ls -la"},
		Risk:     permissions.ToolRisk{Level: permissions.ToolRiskMedium, Category: "command-exec"},
		Reason:   "test prompt",
	}
}

// -------------------------------------------------------------------
// Tests
// -------------------------------------------------------------------

func TestChannelRelay_ApprovesQuickly(t *testing.T) {
	t.Parallel()

	relay := &fakeRelay{
		reply: Reply{Decision: "allow", Reason: "looks fine", RepliedBy: "alice"},
		delay: 5 * time.Millisecond,
	}
	// Fallback is blocking so the only way the test passes is if the
	// relay actually wins the race.
	h := NewChannelRelayApprovalHandler(relay, blockingFallback, 2*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	resp, err := h.AskApproval(ctx, sampleRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Allowed {
		t.Fatalf("expected allowed=true, got %+v", resp)
	}
	if resp.Feedback != "looks fine" {
		t.Fatalf("expected feedback to carry relay reason, got %q", resp.Feedback)
	}

	// Sanity: the relay actually got a request with the expected shape.
	relay.mu.Lock()
	defer relay.mu.Unlock()
	if relay.sentCount != 1 {
		t.Fatalf("expected 1 relay send, got %d", relay.sentCount)
	}
	if relay.lastRequest.ToolName != "Bash" {
		t.Fatalf("expected ToolName=Bash, got %q", relay.lastRequest.ToolName)
	}
	if relay.lastRequest.Risk != "medium" {
		t.Fatalf("expected Risk=medium, got %q", relay.lastRequest.Risk)
	}
	if len(relay.lastRequest.AllowedReplies) != 2 {
		t.Fatalf("expected 2 allowed replies, got %d", len(relay.lastRequest.AllowedReplies))
	}
	if len(relay.lastRequest.ID) != 5 {
		t.Fatalf("expected 5-letter ID, got %q", relay.lastRequest.ID)
	}
}

func TestChannelRelay_DeniesFromRelay(t *testing.T) {
	t.Parallel()

	relay := &fakeRelay{
		reply: Reply{Decision: "deny", Reason: "nope, not tonight"},
		delay: 5 * time.Millisecond,
	}
	h := NewChannelRelayApprovalHandler(relay, blockingFallback, 2*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	resp, err := h.AskApproval(ctx, sampleRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Allowed {
		t.Fatalf("expected allowed=false, got %+v", resp)
	}
	if resp.Feedback != "nope, not tonight" {
		t.Fatalf("expected feedback to carry relay reason, got %q", resp.Feedback)
	}
}

func TestChannelRelay_TimeoutFallsBackToFallback(t *testing.T) {
	t.Parallel()

	// Relay never answers → will time out.
	relay := &fakeRelay{}
	// Fallback responds instantly with "allow + permanent".
	fallback := fixedFallback(
		permissions.ApprovalResponse{Allowed: true, Permanent: true, Feedback: "from CLI"},
		10*time.Millisecond,
	)

	h := NewChannelRelayApprovalHandler(relay, fallback, 30*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	resp, err := h.AskApproval(ctx, sampleRequest())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Allowed || !resp.Permanent || resp.Feedback != "from CLI" {
		t.Fatalf("expected fallback response, got %+v", resp)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("handler took %v, expected to return promptly after fallback answers", elapsed)
	}
}

func TestChannelRelay_RelayErrorFallsBack(t *testing.T) {
	t.Parallel()

	relay := &fakeRelay{sendErr: errors.New("channel disconnected")}
	fallback := fixedFallback(
		permissions.ApprovalResponse{Allowed: true, Feedback: "ok"},
		0,
	)
	h := NewChannelRelayApprovalHandler(relay, fallback, 1*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	resp, err := h.AskApproval(ctx, sampleRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Allowed || resp.Feedback != "ok" {
		t.Fatalf("expected fallback response, got %+v", resp)
	}
}

func TestChannelRelay_NoFallbackSurfacesRelayError(t *testing.T) {
	t.Parallel()

	relay := &fakeRelay{awaitErr: errors.New("no reply matched")}
	h := NewChannelRelayApprovalHandler(relay, nil, 100*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, err := h.AskApproval(ctx, sampleRequest())
	if err == nil {
		t.Fatalf("expected error when relay fails with no fallback")
	}
	if !strings.Contains(err.Error(), "no reply matched") {
		t.Fatalf("expected relay error to surface, got %v", err)
	}
}

func TestChannelRelay_NilRelayUsesFallback(t *testing.T) {
	t.Parallel()

	fallback := fixedFallback(
		permissions.ApprovalResponse{Allowed: true, Feedback: "direct"},
		0,
	)
	h := NewChannelRelayApprovalHandler(nil, fallback, 1*time.Second)

	resp, err := h.AskApproval(context.Background(), sampleRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Allowed || resp.Feedback != "direct" {
		t.Fatalf("expected direct fallback response, got %+v", resp)
	}
}

func TestGenerateRequestID_Shape(t *testing.T) {
	t.Parallel()

	const runs = 100
	seen := make(map[string]struct{}, runs)
	for i := 0; i < runs; i++ {
		id := GenerateRequestID()
		if len(id) != 5 {
			t.Fatalf("run %d: expected 5-letter ID, got %q (len=%d)", i, id, len(id))
		}
		for _, c := range id {
			if !strings.ContainsRune(idAlphabet, c) {
				t.Fatalf("run %d: id %q contains illegal char %q", i, id, c)
			}
			// No vowels, no 'l', no 'y'.
			if strings.ContainsRune("aeioulyAEIOULY", c) {
				t.Fatalf("run %d: id %q contains vowel/l/y %q", i, id, c)
			}
		}
		if _, dup := seen[id]; dup {
			// With 18^5 ≈ 1.9M distinct IDs the birthday-collision
			// probability for 100 draws is astronomically small
			// (~2.6e-3), so a repeat almost certainly indicates a
			// broken generator.
			t.Fatalf("run %d: duplicate id %q in %d draws", i, id, runs)
		}
		seen[id] = struct{}{}
	}
}

func TestNoopRelay_BlocksUntilCancel(t *testing.T) {
	t.Parallel()

	relay := NoopRelay{}
	if err := relay.SendApprovalRequest(context.Background(), RelayRequest{ID: "xxxxx"}); err != nil {
		t.Fatalf("NoopRelay.SendApprovalRequest should be a no-op, got %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := relay.AwaitReply(ctx, "xxxxx")
	if err == nil {
		t.Fatalf("expected context error from NoopRelay.AwaitReply")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestNoopRelay_IntegratesWithHandlerAsRelay(t *testing.T) {
	t.Parallel()

	// NoopRelay never answers → fallback must win.
	fallback := fixedFallback(permissions.ApprovalResponse{Allowed: false, Feedback: "denied"}, 0)
	h := NewChannelRelayApprovalHandler(NoopRelay{}, fallback, 50*time.Millisecond)

	resp, err := h.AskApproval(context.Background(), sampleRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Allowed || resp.Feedback != "denied" {
		t.Fatalf("expected fallback denial, got %+v", resp)
	}
}

func TestReplyToResponse_UnknownDecision(t *testing.T) {
	t.Parallel()

	_, err := replyToResponse(Reply{Decision: "maybe"})
	if err == nil {
		t.Fatalf("expected error for unknown decision")
	}
	if !strings.Contains(err.Error(), "maybe") {
		t.Fatalf("error should name the bad decision, got %v", err)
	}
}

func TestSummarizeArgs_TruncatesLongPayload(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("x", 500)
	out := summarizeArgs(map[string]any{"data": big})
	if len(out) > 210 {
		t.Fatalf("expected truncated summary <=210 chars, got %d", len(out))
	}
	if !strings.HasSuffix(out, "...") {
		t.Fatalf("expected truncated summary to end with ..., got %q", out[len(out)-10:])
	}

	// Empty args → "{}".
	if got := summarizeArgs(nil); got != "{}" {
		t.Fatalf("expected {} for nil args, got %q", got)
	}
}

// Smoke-test that the handler type actually satisfies AskApprovalFn's
// signature. Purely a compile-time check; no behavior.
var _ permissions.AskApprovalFn = (&ChannelRelayApprovalHandler{}).AskApproval

// Debug helper for any future diagnostic prints — kept here so go vet
// doesn't complain about unused imports if one is added later.
var _ = fmt.Sprintf
