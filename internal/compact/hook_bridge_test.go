package compact

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
)

// fakeDispatcher records each Dispatch invocation for test assertions
// and can be programmed to return an error.
type fakeDispatcher struct {
	calls []fakeDispatchCall
	err   error
}

type fakeDispatchCall struct {
	event string
	input map[string]any
}

func (f *fakeDispatcher) Dispatch(ctx context.Context, event string, input map[string]any) ([]byte, error) {
	f.calls = append(f.calls, fakeDispatchCall{event: event, input: input})
	if f.err != nil {
		return nil, f.err
	}
	return []byte("ok"), nil
}

func newSilentBridge(d HookDispatcher) (*HookBridge, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return &HookBridge{Dispatcher: d, Logger: logger}, buf
}

func TestFirePreCompactCallsDispatcherWithCorrectEventAndInput(t *testing.T) {
	d := &fakeDispatcher{}
	bridge, _ := newSilentBridge(d)

	meta := CompactMeta{
		Trigger:            "auto",
		SessionID:          "sess-123",
		MessageCount:       42,
		EstimatedTokens:    98_765,
		CustomInstructions: "focus on code",
	}

	if err := bridge.FirePreCompact(context.Background(), meta); err != nil {
		t.Fatalf("FirePreCompact returned error: %v", err)
	}

	if len(d.calls) != 1 {
		t.Fatalf("expected 1 dispatch call, got %d", len(d.calls))
	}
	call := d.calls[0]
	if call.event != "PreCompact" {
		t.Errorf("expected event PreCompact, got %q", call.event)
	}
	if got := call.input["trigger"]; got != "auto" {
		t.Errorf("expected trigger=auto, got %v", got)
	}
	if got := call.input["session_id"]; got != "sess-123" {
		t.Errorf("expected session_id=sess-123, got %v", got)
	}
	if got := call.input["message_count"]; got != 42 {
		t.Errorf("expected message_count=42, got %v", got)
	}
	if got := call.input["estimated_tokens"]; got != 98_765 {
		t.Errorf("expected estimated_tokens=98765, got %v", got)
	}
	if got := call.input["custom_instructions"]; got != "focus on code" {
		t.Errorf("expected custom_instructions=focus on code, got %v", got)
	}
	// Ensure result payload is NOT present on PreCompact.
	if _, hasResult := call.input["result"]; hasResult {
		t.Errorf("PreCompact input should not contain result payload")
	}
}

func TestFirePostCompactCallsDispatcherWithCorrectEvent(t *testing.T) {
	d := &fakeDispatcher{}
	bridge, _ := newSilentBridge(d)

	meta := CompactMeta{Trigger: "manual", SessionID: "s1"}
	summary := CompactSummary{
		TokensBefore:   12_000,
		TokensAfter:    3_000,
		MessagesBefore: 80,
		MessagesAfter:  10,
		DurationMs:     450,
		Success:        true,
	}

	if err := bridge.FirePostCompact(context.Background(), meta, summary); err != nil {
		t.Fatalf("FirePostCompact returned error: %v", err)
	}

	if len(d.calls) != 1 {
		t.Fatalf("expected 1 dispatch call, got %d", len(d.calls))
	}
	call := d.calls[0]
	if call.event != "PostCompact" {
		t.Errorf("expected event PostCompact, got %q", call.event)
	}
	if got := call.input["trigger"]; got != "manual" {
		t.Errorf("expected trigger=manual, got %v", got)
	}

	resRaw, ok := call.input["result"]
	if !ok {
		t.Fatalf("expected result payload in PostCompact input")
	}
	res, ok := resRaw.(map[string]any)
	if !ok {
		t.Fatalf("result payload is not a map[string]any, got %T", resRaw)
	}
	if res["tokens_before"] != 12_000 {
		t.Errorf("expected tokens_before=12000, got %v", res["tokens_before"])
	}
	if res["tokens_after"] != 3_000 {
		t.Errorf("expected tokens_after=3000, got %v", res["tokens_after"])
	}
	if res["messages_before"] != 80 {
		t.Errorf("expected messages_before=80, got %v", res["messages_before"])
	}
	if res["messages_after"] != 10 {
		t.Errorf("expected messages_after=10, got %v", res["messages_after"])
	}
	if res["duration_ms"] != int64(450) {
		t.Errorf("expected duration_ms=450 (int64), got %v (%T)", res["duration_ms"], res["duration_ms"])
	}
	if res["success"] != true {
		t.Errorf("expected success=true, got %v", res["success"])
	}
}

func TestDispatcherErrorIsLoggedAndSuppressed(t *testing.T) {
	sentinel := errors.New("boom")
	d := &fakeDispatcher{err: sentinel}
	bridge, logBuf := newSilentBridge(d)

	// PreCompact: err must be suppressed
	if err := bridge.FirePreCompact(context.Background(), CompactMeta{Trigger: "auto"}); err != nil {
		t.Errorf("FirePreCompact should suppress dispatcher error, got %v", err)
	}
	if !bytes.Contains(logBuf.Bytes(), []byte("PreCompact hook dispatch failed")) {
		t.Errorf("expected PreCompact warning in log, got: %s", logBuf.String())
	}

	logBuf.Reset()

	// PostCompact: err must also be suppressed
	if err := bridge.FirePostCompact(context.Background(), CompactMeta{Trigger: "auto"}, CompactSummary{}); err != nil {
		t.Errorf("FirePostCompact should suppress dispatcher error, got %v", err)
	}
	if !bytes.Contains(logBuf.Bytes(), []byte("PostCompact hook dispatch failed")) {
		t.Errorf("expected PostCompact warning in log, got: %s", logBuf.String())
	}
}

func TestNoopHookBridgeNeverPanics(t *testing.T) {
	var b NoopHookBridge
	meta := CompactMeta{Trigger: "reactive", SessionID: "s1", MessageCount: 5}
	summary := CompactSummary{TokensBefore: 1, TokensAfter: 0, Success: true}

	if err := b.FirePreCompact(context.Background(), meta); err != nil {
		t.Errorf("NoopHookBridge.FirePreCompact returned error: %v", err)
	}
	if err := b.FirePostCompact(context.Background(), meta, summary); err != nil {
		t.Errorf("NoopHookBridge.FirePostCompact returned error: %v", err)
	}

	// Also verify a zero-valued HookBridge is safe (nil dispatcher).
	var h HookBridge
	if err := h.FirePreCompact(context.Background(), meta); err != nil {
		t.Errorf("zero HookBridge.FirePreCompact returned error: %v", err)
	}
	if err := h.FirePostCompact(context.Background(), meta, summary); err != nil {
		t.Errorf("zero HookBridge.FirePostCompact returned error: %v", err)
	}

	// And a HookBridge whose Dispatcher was left nil by NewHookBridge.
	nb := NewHookBridge(nil)
	if err := nb.FirePreCompact(context.Background(), meta); err != nil {
		t.Errorf("NewHookBridge(nil).FirePreCompact returned error: %v", err)
	}
	if err := nb.FirePostCompact(context.Background(), meta, summary); err != nil {
		t.Errorf("NewHookBridge(nil).FirePostCompact returned error: %v", err)
	}
}
