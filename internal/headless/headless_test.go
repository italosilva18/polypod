package headless

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/costa/polypod/internal/adapter"
	"github.com/costa/polypod/internal/session"
)

// captureStdout redirects os.Stdout while fn runs and returns what was written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	defer func() {
		os.Stdout = orig
	}()

	fn()
	_ = w.Close()
	return <-done
}

func TestOutputFormatConstants(t *testing.T) {
	cases := []struct {
		got  OutputFormat
		want string
	}{
		{FormatText, "text"},
		{FormatJSON, "json"},
		{FormatStreamJSON, "stream-json"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("OutputFormat = %q, want %q", string(c.got), c.want)
		}
	}
}

func TestConfigStructFields(t *testing.T) {
	cfg := Config{
		Prompt:       "hello",
		OutputFormat: FormatJSON,
		SessionID:    "sess-123",
		Continue:     true,
		SystemPrompt: "sys",
	}
	if cfg.Prompt != "hello" {
		t.Errorf("Prompt = %q", cfg.Prompt)
	}
	if cfg.OutputFormat != FormatJSON {
		t.Errorf("OutputFormat = %q", cfg.OutputFormat)
	}
	if cfg.SessionID != "sess-123" {
		t.Errorf("SessionID = %q", cfg.SessionID)
	}
	if !cfg.Continue {
		t.Error("Continue should be true")
	}
	if cfg.SystemPrompt != "sys" {
		t.Errorf("SystemPrompt = %q", cfg.SystemPrompt)
	}
}

func TestResultJSONSerialization(t *testing.T) {
	r := Result{
		Content:    "answer",
		SessionID:  "abc",
		Model:      "gpt-4",
		CostUSD:    0.0125,
		DurationMS: 1500,
	}
	r.Tokens.Prompt = 10
	r.Tokens.Completion = 20
	r.Tokens.Total = 30

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Verify field presence and JSON tags
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if m["content"] != "answer" {
		t.Errorf("content = %v", m["content"])
	}
	if m["session_id"] != "abc" {
		t.Errorf("session_id = %v", m["session_id"])
	}
	if m["model"] != "gpt-4" {
		t.Errorf("model = %v", m["model"])
	}
	if m["cost_usd"].(float64) != 0.0125 {
		t.Errorf("cost_usd = %v", m["cost_usd"])
	}
	if m["duration_ms"].(float64) != 1500 {
		t.Errorf("duration_ms = %v", m["duration_ms"])
	}
	tokens, ok := m["tokens"].(map[string]any)
	if !ok {
		t.Fatalf("tokens missing or wrong type: %v", m["tokens"])
	}
	if tokens["prompt"].(float64) != 10 {
		t.Errorf("tokens.prompt = %v", tokens["prompt"])
	}
	if tokens["completion"].(float64) != 20 {
		t.Errorf("tokens.completion = %v", tokens["completion"])
	}
	if tokens["total"].(float64) != 30 {
		t.Errorf("tokens.total = %v", tokens["total"])
	}
}

func TestResultRoundTrip(t *testing.T) {
	orig := Result{
		Content:    "hi\nthere",
		SessionID:  "s1",
		Model:      "m1",
		CostUSD:    0.5,
		DurationMS: 42,
	}
	orig.Tokens.Prompt = 1
	orig.Tokens.Completion = 2
	orig.Tokens.Total = 3

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded Result
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded != orig {
		t.Errorf("roundtrip mismatch:\n got %+v\nwant %+v", decoded, orig)
	}
}

func TestRunEmptyPromptReturnsError(t *testing.T) {
	// When Prompt is empty and stdin is a terminal (the default in `go test`
	// without piping), readStdin returns "" and Run should error.
	cfg := Config{Prompt: "", OutputFormat: FormatText}
	handler := func(ctx context.Context, msg adapter.InMessage) (adapter.OutMessage, error) {
		t.Fatal("handler should not be called")
		return adapter.OutMessage{}, nil
	}
	streamHandler := func(ctx context.Context, msg adapter.InMessage, chunks chan<- adapter.StreamChunk) {
		t.Fatal("streamHandler should not be called")
	}

	err := Run(context.Background(), cfg, handler, streamHandler, nil, nil)
	if err == nil {
		t.Fatal("expected error for empty prompt, got nil")
	}
	if !strings.Contains(err.Error(), "nenhum prompt") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRunJSONFormat(t *testing.T) {
	cfg := Config{
		Prompt:       "what is 2+2?",
		OutputFormat: FormatJSON,
		SessionID:    "sess-42",
	}

	var gotMsg adapter.InMessage
	handler := func(ctx context.Context, msg adapter.InMessage) (adapter.OutMessage, error) {
		gotMsg = msg
		return adapter.OutMessage{Text: "four"}, nil
	}
	streamHandler := func(ctx context.Context, msg adapter.InMessage, chunks chan<- adapter.StreamChunk) {
		t.Fatal("streamHandler should not be called in JSON mode")
	}

	out := captureStdout(t, func() {
		if err := Run(context.Background(), cfg, handler, streamHandler, nil, nil); err != nil {
			t.Fatalf("Run: %v", err)
		}
	})

	if gotMsg.Text != "what is 2+2?" {
		t.Errorf("handler got Text = %q", gotMsg.Text)
	}
	if gotMsg.Channel != "headless" {
		t.Errorf("handler got Channel = %q", gotMsg.Channel)
	}
	if gotMsg.UserID != "cli" {
		t.Errorf("handler got UserID = %q", gotMsg.UserID)
	}

	var res Result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode output as Result: %v\noutput: %s", err, out)
	}
	if res.Content != "four" {
		t.Errorf("Content = %q, want %q", res.Content, "four")
	}
	if res.SessionID != "sess-42" {
		t.Errorf("SessionID = %q", res.SessionID)
	}
	if res.DurationMS < 0 {
		t.Errorf("DurationMS negative: %d", res.DurationMS)
	}
}

func TestRunJSONFormatCarriesMeasuredUsage(t *testing.T) {
	zero := 0.0
	handler := func(context.Context, adapter.InMessage) (adapter.OutMessage, error) {
		return adapter.OutMessage{Text: "OK", Usage: &adapter.Usage{Provider: "ollama", Model: "qwen2.5-coder:3b",
			PromptTokens: 12, CompletionTokens: 4, TotalTokens: 16, CostUSD: &zero, Steps: 2, ToolFailures: 1}}, nil
	}
	out := captureStdout(t, func() {
		if err := Run(context.Background(), Config{Prompt: "responda", OutputFormat: FormatJSON}, handler, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	})
	var result Result
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Provider != "ollama" || result.Model != "qwen2.5-coder:3b" || !result.CostMeasured || result.CostUSD != 0 ||
		result.Tokens.Total != 16 || result.Steps != 2 || result.ToolFailures != 1 {
		t.Fatalf("usage lost: %+v", result)
	}
}

func TestRunJSONFormatHandlerError(t *testing.T) {
	cfg := Config{Prompt: "hi", OutputFormat: FormatJSON}
	boom := &stubError{"boom"}
	handler := func(ctx context.Context, msg adapter.InMessage) (adapter.OutMessage, error) {
		return adapter.OutMessage{}, boom
	}
	streamHandler := func(ctx context.Context, msg adapter.InMessage, chunks chan<- adapter.StreamChunk) {}

	var err error
	_ = captureStdout(t, func() {
		err = Run(context.Background(), cfg, handler, streamHandler, nil, nil)
	})
	if err == nil || err.Error() != "boom" {
		t.Errorf("expected boom error, got %v", err)
	}
}

func TestRunStreamJSONFormat(t *testing.T) {
	cfg := Config{Prompt: "stream please", OutputFormat: FormatStreamJSON}
	handler := func(ctx context.Context, msg adapter.InMessage) (adapter.OutMessage, error) {
		t.Fatal("handler should not be called in stream mode")
		return adapter.OutMessage{}, nil
	}
	streamHandler := func(ctx context.Context, msg adapter.InMessage, chunks chan<- adapter.StreamChunk) {
		defer close(chunks)
		chunks <- adapter.StreamChunk{Delta: "hel"}
		chunks <- adapter.StreamChunk{Delta: "lo"}
		chunks <- adapter.StreamChunk{Done: true}
	}

	out := captureStdout(t, func() {
		if err := Run(context.Background(), cfg, handler, streamHandler, nil, nil); err != nil {
			t.Fatalf("Run: %v", err)
		}
	})

	lines := splitNonEmpty(out)
	if len(lines) != 3 {
		t.Fatalf("want 3 JSON lines, got %d: %q", len(lines), out)
	}

	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("line0: %v", err)
	}
	if first["type"] != "delta" || first["delta"] != "hel" {
		t.Errorf("line0 = %v", first)
	}

	var second map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("line1: %v", err)
	}
	if second["type"] != "delta" || second["delta"] != "lo" {
		t.Errorf("line1 = %v", second)
	}

	var third map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &third); err != nil {
		t.Fatalf("line2: %v", err)
	}
	if third["type"] != "done" {
		t.Errorf("line2 type = %v", third["type"])
	}
	if third["content"] != "hello" {
		t.Errorf("line2 content = %v, want 'hello'", third["content"])
	}
	if _, ok := third["duration_ms"].(float64); !ok {
		t.Errorf("line2 duration_ms missing or wrong type: %v", third["duration_ms"])
	}
}

func TestRunStreamJSONPropagatesError(t *testing.T) {
	cfg := Config{Prompt: "x", OutputFormat: FormatStreamJSON}
	handler := func(ctx context.Context, msg adapter.InMessage) (adapter.OutMessage, error) {
		return adapter.OutMessage{}, nil
	}
	streamHandler := func(ctx context.Context, msg adapter.InMessage, chunks chan<- adapter.StreamChunk) {
		defer close(chunks)
		chunks <- adapter.StreamChunk{Error: &stubError{"stream-fail"}}
	}

	var err error
	out := captureStdout(t, func() {
		err = Run(context.Background(), cfg, handler, streamHandler, nil, nil)
	})
	if err == nil || err.Error() != "stream-fail" {
		t.Errorf("want stream-fail error, got %v", err)
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &event); err != nil {
		t.Fatalf("decode error event: %v", err)
	}
	if event["type"] != "error" || event["error"] != "stream-fail" {
		t.Fatalf("unexpected error event: %+v", event)
	}
}

func TestRunStreamJSONIncludesToolLifecycleAndSequence(t *testing.T) {
	cfg := Config{Prompt: "programar", OutputFormat: FormatStreamJSON}
	streamHandler := func(_ context.Context, _ adapter.InMessage, chunks chan<- adapter.StreamChunk) {
		defer close(chunks)
		chunks <- adapter.StreamChunk{ToolEvent: &adapter.ToolEventInfo{Type: adapter.ToolEventStart, ToolName: "file_edit", ToolID: "call-1"}}
		chunks <- adapter.StreamChunk{ToolEvent: &adapter.ToolEventInfo{Type: adapter.ToolEventComplete, ToolName: "file_edit", ToolID: "call-1", Elapsed: 25 * time.Millisecond}}
		chunks <- adapter.StreamChunk{Delta: "feito"}
		chunks <- adapter.StreamChunk{Done: true}
	}
	out := captureStdout(t, func() {
		if err := Run(context.Background(), cfg, nil, streamHandler, nil, nil); err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
	lines := splitNonEmpty(out)
	if len(lines) != 4 {
		t.Fatalf("want four events, got %d: %q", len(lines), out)
	}
	for i, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
		if event["version"] != float64(1) || event["sequence"] != float64(i+1) {
			t.Fatalf("event %d has wrong protocol metadata: %+v", i, event)
		}
		if i < 2 && (event["type"] != "tool" || event["tool_name"] != "file_edit" || event["tool_id"] != "call-1") {
			t.Fatalf("event %d has wrong tool identity: %+v", i, event)
		}
	}
}

func TestRunTextFormat(t *testing.T) {
	cfg := Config{Prompt: "hi", OutputFormat: FormatText}
	handler := func(ctx context.Context, msg adapter.InMessage) (adapter.OutMessage, error) {
		t.Fatal("handler should not be called in text mode")
		return adapter.OutMessage{}, nil
	}
	streamHandler := func(ctx context.Context, msg adapter.InMessage, chunks chan<- adapter.StreamChunk) {
		defer close(chunks)
		chunks <- adapter.StreamChunk{Delta: "Hello, "}
		chunks <- adapter.StreamChunk{Delta: "world!"}
		chunks <- adapter.StreamChunk{Done: true}
	}

	out := captureStdout(t, func() {
		if err := Run(context.Background(), cfg, handler, streamHandler, nil, nil); err != nil {
			t.Fatalf("Run: %v", err)
		}
	})

	// text mode prints deltas then a trailing newline on Done
	want := "Hello, world!\n"
	if out != want {
		t.Errorf("text output = %q, want %q", out, want)
	}
}

func TestRunTextFormatDefaultOnUnknown(t *testing.T) {
	// An unknown format falls through to the default (text) branch.
	cfg := Config{Prompt: "hi", OutputFormat: OutputFormat("weird")}
	handler := func(ctx context.Context, msg adapter.InMessage) (adapter.OutMessage, error) {
		t.Fatal("handler should not be called")
		return adapter.OutMessage{}, nil
	}
	streamHandler := func(ctx context.Context, msg adapter.InMessage, chunks chan<- adapter.StreamChunk) {
		defer close(chunks)
		chunks <- adapter.StreamChunk{Delta: "ok"}
		chunks <- adapter.StreamChunk{Done: true}
	}

	out := captureStdout(t, func() {
		if err := Run(context.Background(), cfg, handler, streamHandler, nil, nil); err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
	if out != "ok\n" {
		t.Errorf("default branch output = %q, want %q", out, "ok\n")
	}
}

func TestRunPassesTimestamp(t *testing.T) {
	cfg := Config{Prompt: "time check", OutputFormat: FormatJSON}
	before := time.Now()

	var gotTs time.Time
	handler := func(ctx context.Context, msg adapter.InMessage) (adapter.OutMessage, error) {
		gotTs = msg.Timestamp
		return adapter.OutMessage{Text: "ok"}, nil
	}
	streamHandler := func(ctx context.Context, msg adapter.InMessage, chunks chan<- adapter.StreamChunk) {}

	_ = captureStdout(t, func() {
		if err := Run(context.Background(), cfg, handler, streamHandler, nil, nil); err != nil {
			t.Fatalf("Run: %v", err)
		}
	})

	after := time.Now()
	if gotTs.Before(before) || gotTs.After(after) {
		t.Errorf("timestamp %v not in [%v,%v]", gotTs, before, after)
	}
}

func TestRunStreamJSONReplaysMissedSessionEventsAndContinuesSequence(t *testing.T) {
	journal := session.NewEventJournal(t.TempDir())
	for i := 0; i < 3; i++ {
		if _, err := journal.AppendTurnEvent("sess-shared", "cli", "turn-1", map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"index":         i,
		}); err != nil {
			t.Fatal(err)
		}
	}
	var out strings.Builder
	cfg := Config{
		Prompt:        "continuar",
		OutputFormat:  FormatStreamJSON,
		SessionID:     "sess-shared",
		TurnID:        "turn-2",
		AfterSequence: 1,
		EventJournal:  journal,
		Stdout:        &out,
	}
	streamHandler := func(_ context.Context, msg adapter.InMessage, chunks chan<- adapter.StreamChunk) {
		defer close(chunks)
		if msg.Metadata["polypod_session_id"] != "sess-shared" || msg.Metadata["polypod_turn_id"] != "turn-2" {
			t.Errorf("unexpected metadata: %+v", msg.Metadata)
		}
		ev1, _ := journal.AppendTurnEvent("sess-shared", "headless", "turn-2", map[string]any{"sessionUpdate": "agent_message_chunk"})
		chunks <- adapter.StreamChunk{Delta: "ok", EventVersion: ev1.Version, EventSequence: ev1.Sequence}
		ev2, _ := journal.AppendTurnEvent("sess-shared", "headless", "turn-2", map[string]any{"sessionUpdate": "turn_complete"})
		chunks <- adapter.StreamChunk{Done: true, EventVersion: ev2.Version, EventSequence: ev2.Sequence}
	}
	if err := Run(context.Background(), cfg, nil, streamHandler, nil, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	lines := splitNonEmpty(out.String())
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines (2 replayed + delta + done), got %d: %s", len(lines), out.String())
	}
	wantSeq := []float64{2, 3, 4, 5}
	for i, line := range lines {
		var payload map[string]any
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["sequence"] != wantSeq[i] {
			t.Fatalf("line %d sequence = %v, want %v (%s)", i, payload["sequence"], wantSeq[i], line)
		}
	}
}

// --- helpers ---

type stubError struct{ msg string }

func (e *stubError) Error() string { return e.msg }

func splitNonEmpty(s string) []string {
	raw := strings.Split(s, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}
