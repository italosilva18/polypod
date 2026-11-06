package sidequery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// newTestServer spins up an httptest server that invokes handler for every
// request and returns the server.  The caller is responsible for Close().
// It also wires ANTHROPIC_BASE_URL + ANTHROPIC_API_KEY via t.Setenv so the
// SDK client constructed inside sidequery routes to the test server.
func newTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
	t.Setenv("ANTHROPIC_API_KEY", "test-key-not-used")
	return srv
}

// writeMessage writes a minimal, SDK-parseable POST /v1/messages response.
// content is the JSON array to embed in the "content" field.
func writeMessage(w http.ResponseWriter, contentJSON string) {
	body := `{
		"id": "msg_test_01",
		"type": "message",
		"role": "assistant",
		"model": "claude-haiku-4-5-20251001",
		"content": ` + contentJSON + `,
		"stop_reason": "end_turn",
		"stop_sequence": null,
		"usage": {"input_tokens": 1, "output_tokens": 1}
	}`
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, body)
}

// unmarshalMessage builds an anthropic.Message from a JSON body so that the
// AsAny() helpers (which read from block.JSON.raw) work as if the response had
// come from the wire.
func unmarshalMessage(t *testing.T, contentJSON string) *anthropic.Message {
	t.Helper()
	body := `{
		"id": "msg_test_01",
		"type": "message",
		"role": "assistant",
		"model": "claude-haiku-4-5-20251001",
		"content": ` + contentJSON + `,
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 1, "output_tokens": 1}
	}`
	var msg anthropic.Message
	if err := json.Unmarshal([]byte(body), &msg); err != nil {
		t.Fatalf("unmarshalMessage: %v", err)
	}
	return &msg
}

// ---------------------------------------------------------------------------
// Helper function tests
// ---------------------------------------------------------------------------

// TestStripJSONFence exercises the previously regex-buggy fence stripper.
// Covers plain JSON, fenced with language tag, fenced without tag, whitespace,
// unterminated fences, and strings containing backticks mid-content.
func TestStripJSONFence(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain json untouched", `{"a":1}`, `{"a":1}`},
		{"whitespace trimmed", "   {\"a\":1}\n\t", `{"a":1}`},
		{"standard json fence", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"bare fence no lang", "```\n{\"a\":1}\n```", `{"a":1}`},
		{"fence with trailing text stripped", "```json\n{\"a\":1}\n```\n", `{"a":1}`},
		{"non-fenced starting with brace and backticks inside string",
			"{\"doc\": \"use ``` for fences\"}",
			"{\"doc\": \"use ``` for fences\"}"},
		{"empty string", "", ""},
		{"only whitespace", "   \n  ", ""},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := stripJSONFence(tc.in)
			if got != tc.want {
				t.Errorf("stripJSONFence(%q)\n  got:  %q\n  want: %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestTruncate verifies the rune-aware truncation used for error messages.
// Guards against a byte-based regression that could split multi-byte runes.
func TestTruncate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"short untouched", "hello", 10, "hello"},
		{"exact fit", "hello", 5, "hello"},
		{"cut ascii", "helloworld", 5, "hello…"},
		{"rune aware", "héllo☃snowman", 6, "héllo☃…"},
		{"zero length cap", "abc", 0, "…"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := truncate(tc.in, tc.n)
			if got != tc.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
			}
		})
	}
}

// TestIsRetryable covers every branch of the retry classifier.
func TestIsRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"api 429 retryable", &anthropic.Error{StatusCode: http.StatusTooManyRequests}, true},
		{"api 500 retryable", &anthropic.Error{StatusCode: http.StatusInternalServerError}, true},
		{"api 502 retryable", &anthropic.Error{StatusCode: http.StatusBadGateway}, true},
		{"api 503 retryable", &anthropic.Error{StatusCode: http.StatusServiceUnavailable}, true},
		{"api 504 retryable", &anthropic.Error{StatusCode: http.StatusGatewayTimeout}, true},
		{"api 400 not retryable", &anthropic.Error{StatusCode: http.StatusBadRequest}, false},
		{"api 401 not retryable", &anthropic.Error{StatusCode: http.StatusUnauthorized}, false},
		{"generic connection reset", errors.New("read tcp: connection reset by peer"), true},
		{"generic connection refused", errors.New("dial tcp: connection refused"), true},
		{"generic EOF", errors.New("unexpected EOF"), true},
		{"generic timeout", errors.New("i/o timeout"), true},
		{"unrelated error", errors.New("invalid_argument: bad field"), false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := isRetryable(tc.err)
			if got != tc.want {
				t.Errorf("isRetryable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestExtractText verifies that text blocks are collected, joined, and that
// non-text blocks are ignored.  Nil messages must return empty string.
func TestExtractText(t *testing.T) {
	t.Run("nil message", func(t *testing.T) {
		if got := extractText(nil); got != "" {
			t.Errorf("extractText(nil) = %q, want empty", got)
		}
	})

	t.Run("single text", func(t *testing.T) {
		msg := unmarshalMessage(t, `[{"type":"text","text":"hello world"}]`)
		if got := extractText(msg); got != "hello world" {
			t.Errorf("extractText = %q, want %q", got, "hello world")
		}
	})

	t.Run("multiple text blocks joined", func(t *testing.T) {
		msg := unmarshalMessage(t, `[
			{"type":"text","text":"part one"},
			{"type":"text","text":"part two"}
		]`)
		got := extractText(msg)
		if got != "part one\n\npart two" {
			t.Errorf("extractText = %q, want %q", got, "part one\n\npart two")
		}
	})

	t.Run("mixed with tool use only returns text", func(t *testing.T) {
		msg := unmarshalMessage(t, `[
			{"type":"text","text":"thinking..."},
			{"type":"tool_use","id":"toolu_1","name":"weather","input":{"city":"SF"}}
		]`)
		got := extractText(msg)
		if got != "thinking..." {
			t.Errorf("extractText = %q, want %q", got, "thinking...")
		}
	})
}

// TestExtractToolUses verifies tool-use extraction including correct input
// unmarshalling and the empty/nil paths.
func TestExtractToolUses(t *testing.T) {
	t.Run("nil message", func(t *testing.T) {
		if uses := extractToolUses(nil); uses != nil {
			t.Errorf("extractToolUses(nil) = %v, want nil", uses)
		}
	})

	t.Run("no tool uses", func(t *testing.T) {
		msg := unmarshalMessage(t, `[{"type":"text","text":"just text"}]`)
		if uses := extractToolUses(msg); len(uses) != 0 {
			t.Errorf("extractToolUses len = %d, want 0", len(uses))
		}
	})

	t.Run("single tool use with input", func(t *testing.T) {
		msg := unmarshalMessage(t, `[
			{"type":"text","text":"let me check"},
			{"type":"tool_use","id":"toolu_abc","name":"get_weather","input":{"city":"Paris","unit":"C"}}
		]`)
		uses := extractToolUses(msg)
		if len(uses) != 1 {
			t.Fatalf("len(uses) = %d, want 1", len(uses))
		}
		if uses[0].ID != "toolu_abc" || uses[0].Name != "get_weather" {
			t.Errorf("unexpected id/name: %+v", uses[0])
		}
		if uses[0].Input["city"] != "Paris" || uses[0].Input["unit"] != "C" {
			t.Errorf("unexpected input: %+v", uses[0].Input)
		}
	})

	t.Run("two tool uses", func(t *testing.T) {
		msg := unmarshalMessage(t, `[
			{"type":"tool_use","id":"t1","name":"a","input":{"x":1}},
			{"type":"tool_use","id":"t2","name":"b","input":{"y":2}}
		]`)
		uses := extractToolUses(msg)
		if len(uses) != 2 {
			t.Fatalf("len(uses) = %d, want 2", len(uses))
		}
		if uses[0].ID != "t1" || uses[1].ID != "t2" {
			t.Errorf("unexpected ids: %+v", uses)
		}
	})
}

// ---------------------------------------------------------------------------
// Constructor / buildClient
// ---------------------------------------------------------------------------

// TestBuildClient ensures the client constructor returns a non-nil pointer in
// both branches (with and without an explicit API key).
func TestBuildClient(t *testing.T) {
	t.Run("with api key", func(t *testing.T) {
		c := buildClient("my-explicit-key")
		if c == nil {
			t.Fatal("buildClient returned nil")
		}
	})

	t.Run("without api key falls back to env", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "env-key")
		c := buildClient("")
		if c == nil {
			t.Fatal("buildClient returned nil")
		}
	})
}

// ---------------------------------------------------------------------------
// buildMessageParams — parameter assembly
// ---------------------------------------------------------------------------

// TestBuildMessageParams verifies that every SideQueryParams field is wired
// into the SDK request struct correctly.
func TestBuildMessageParams(t *testing.T) {
	t.Run("defaults inject system prefix", func(t *testing.T) {
		p := buildMessageParams("m", 100, SideQueryParams{
			SystemPrompt: "be concise",
			Messages: []Message{
				{Role: "user", Content: "hi"},
			},
		})
		if p.Model != "m" || p.MaxTokens != 100 {
			t.Errorf("model/max: %q / %d", p.Model, p.MaxTokens)
		}
		if len(p.System) != 2 {
			t.Fatalf("expected 2 system blocks (prefix + prompt), got %d", len(p.System))
		}
		if !strings.Contains(p.System[0].Text, "helpful") {
			t.Errorf("first system block should be default prefix: %q", p.System[0].Text)
		}
		if p.System[1].Text != "be concise" {
			t.Errorf("second system block = %q", p.System[1].Text)
		}
		if len(p.Messages) != 1 {
			t.Errorf("expected 1 message, got %d", len(p.Messages))
		}
	})

	t.Run("SkipSystemPrefix drops default prefix", func(t *testing.T) {
		p := buildMessageParams("m", 100, SideQueryParams{
			SkipSystemPrefix: true,
			SystemPrompt:     "only-mine",
		})
		if len(p.System) != 1 || p.System[0].Text != "only-mine" {
			t.Errorf("expected single custom system block, got %+v", p.System)
		}
	})

	t.Run("user and assistant roles pass through", func(t *testing.T) {
		p := buildMessageParams("m", 100, SideQueryParams{
			Messages: []Message{
				{Role: "user", Content: "q"},
				{Role: "assistant", Content: "a"},
				{Role: "unknown", Content: "ignored"},
			},
		})
		if len(p.Messages) != 2 {
			t.Errorf("expected 2 messages (unknown dropped), got %d", len(p.Messages))
		}
	})

	t.Run("temperature set only when positive", func(t *testing.T) {
		p := buildMessageParams("m", 100, SideQueryParams{Temperature: 0.7})
		if !p.Temperature.Valid() {
			t.Error("temperature should be set when > 0")
		}
		p0 := buildMessageParams("m", 100, SideQueryParams{Temperature: 0})
		if p0.Temperature.Valid() {
			t.Error("temperature should be unset when 0")
		}
	})

	t.Run("stop sequences propagated", func(t *testing.T) {
		p := buildMessageParams("m", 100, SideQueryParams{
			StopSequences: []string{"STOP", "END"},
		})
		if len(p.StopSequences) != 2 || p.StopSequences[0] != "STOP" {
			t.Errorf("stop sequences = %v", p.StopSequences)
		}
	})

	t.Run("thinking disabled branch", func(t *testing.T) {
		p := buildMessageParams("m", 100, SideQueryParams{ThinkingBudget: -1})
		if p.Thinking.OfDisabled == nil {
			t.Error("thinking should be explicitly disabled")
		}
	})

	t.Run("thinking enabled branch", func(t *testing.T) {
		p := buildMessageParams("m", 1000, SideQueryParams{ThinkingBudget: 500})
		if p.Thinking.OfEnabled == nil {
			t.Fatal("thinking should be enabled")
		}
		if p.Thinking.OfEnabled.BudgetTokens != 500 {
			t.Errorf("budget tokens = %d, want 500", p.Thinking.OfEnabled.BudgetTokens)
		}
	})

	t.Run("thinking budget clamped to maxTokens-1", func(t *testing.T) {
		p := buildMessageParams("m", 100, SideQueryParams{ThinkingBudget: 1000})
		if p.Thinking.OfEnabled == nil {
			t.Fatal("thinking should be enabled")
		}
		if p.Thinking.OfEnabled.BudgetTokens != 99 {
			t.Errorf("budget should be clamped to 99, got %d", p.Thinking.OfEnabled.BudgetTokens)
		}
	})

	t.Run("tools assembled", func(t *testing.T) {
		p := buildMessageParams("m", 100, SideQueryParams{
			Tools: []ToolDef{{
				Name:        "get_weather",
				Description: "fetches weather",
				InputSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{"city": map[string]any{"type": "string"}},
				},
			}},
		})
		if len(p.Tools) != 1 || p.Tools[0].OfTool == nil {
			t.Fatalf("expected 1 tool, got %+v", p.Tools)
		}
		if p.Tools[0].OfTool.Name != "get_weather" {
			t.Errorf("tool name = %q", p.Tools[0].OfTool.Name)
		}
	})
}

// ---------------------------------------------------------------------------
// Submit / run a query — end-to-end via httptest server
// ---------------------------------------------------------------------------

// TestSideQuerySubmit verifies that SideQuery POSTs to /v1/messages with the
// expected body and returns the text from the model response.
func TestSideQuerySubmit(t *testing.T) {
	var gotPath, gotMethod, gotBody string
	var hits int32

	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotPath = r.URL.Path
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		writeMessage(w, `[{"type":"text","text":"answer from model"}]`)
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	text, err := SideQuery(ctx, SideQueryParams{
		SystemPrompt: "classify",
		Messages: []Message{
			{Role: "user", Content: "what category?"},
		},
		MaxTokens:  50,
		MaxRetries: 1,
	})
	if err != nil {
		t.Fatalf("SideQuery err: %v", err)
	}
	if text != "answer from model" {
		t.Errorf("text = %q, want %q", text, "answer from model")
	}
	if gotMethod != "POST" {
		t.Errorf("method = %q", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/v1/messages") {
		t.Errorf("path = %q, want to end with /v1/messages", gotPath)
	}
	if !strings.Contains(gotBody, `"what category?"`) {
		t.Errorf("body does not contain user message: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"max_tokens":50`) {
		t.Errorf("body does not contain explicit max_tokens: %s", gotBody)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("expected exactly 1 hit on success, got %d", hits)
	}
}

// TestSideQueryJSONParse verifies that SideQueryJSON unmarshals the model's
// text response into the target struct, including when the model wraps it in
// a ```json fence (regression guard for the previous stripJSONFence bug).
func TestSideQueryJSONParse(t *testing.T) {
	type classified struct {
		Label      string  `json:"label"`
		Confidence float64 `json:"confidence"`
	}

	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Model wraps JSON in a fenced code block (common real behaviour).
		writeMessage(w, `[{"type":"text","text":"`+"```json\\n{\\\"label\\\":\\\"safe\\\",\\\"confidence\\\":0.93}\\n```"+`"}]`)
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var out classified
	if err := SideQueryJSON(ctx, SideQueryParams{
		MaxRetries: 1,
		Messages:   []Message{{Role: "user", Content: "classify"}},
	}, &out); err != nil {
		t.Fatalf("SideQueryJSON err: %v", err)
	}
	if out.Label != "safe" || out.Confidence < 0.92 || out.Confidence > 0.94 {
		t.Errorf("parsed result wrong: %+v", out)
	}
}

// TestSideQueryWithToolsReturnsToolUses verifies that a tool_use response is
// surfaced via the typed ToolUse slice, and the text component is also
// returned.
func TestSideQueryWithToolsReturnsToolUses(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeMessage(w, `[
			{"type":"text","text":"Let me look that up."},
			{"type":"tool_use","id":"toolu_01","name":"search","input":{"q":"kilo"}}
		]`)
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	text, tools, err := SideQueryWithTools(ctx, SideQueryParams{
		MaxRetries: 1,
		Messages:   []Message{{Role: "user", Content: "find something"}},
		Tools: []ToolDef{{
			Name:        "search",
			Description: "searches",
			InputSchema: map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		t.Fatalf("SideQueryWithTools err: %v", err)
	}
	if text != "Let me look that up." {
		t.Errorf("text = %q", text)
	}
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	if tools[0].ID != "toolu_01" || tools[0].Name != "search" {
		t.Errorf("unexpected tool use: %+v", tools[0])
	}
	if tools[0].Input["q"] != "kilo" {
		t.Errorf("tool input = %+v", tools[0].Input)
	}
}

// ---------------------------------------------------------------------------
// Cancellation
// ---------------------------------------------------------------------------

// TestCancellationPreCancelled verifies that a pre-cancelled context causes
// SideQuery to return a context error without ever completing a request.
func TestCancellationPreCancelled(t *testing.T) {
	var hits int32
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		writeMessage(w, `[{"type":"text","text":"should not be returned"}]`)
	})
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel BEFORE the call

	_, err := SideQuery(ctx, SideQueryParams{
		MaxRetries: 1,
		Messages:   []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "canceled") {
		t.Errorf("expected context.Canceled in error chain, got: %v", err)
	}
}

// TestCancellationInFlight verifies that cancelling the context while the
// server is hanging causes the call to return promptly with a context error.
func TestCancellationInFlight(t *testing.T) {
	serverEnter := make(chan struct{})
	serverRelease := make(chan struct{})

	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		close(serverEnter)
		select {
		case <-serverRelease:
		case <-r.Context().Done():
		}
		// We don't bother writing a body — the client should have given up.
	})
	defer srv.Close()
	defer close(serverRelease)

	ctx, cancel := context.WithCancel(context.Background())

	type result struct {
		err error
	}
	resultCh := make(chan result, 1)
	go func() {
		_, err := SideQuery(ctx, SideQueryParams{
			MaxRetries: 1,
			Messages:   []Message{{Role: "user", Content: "hi"}},
		})
		resultCh <- result{err: err}
	}()

	// Wait until the server handler has received the request, then cancel.
	select {
	case <-serverEnter:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("timed out waiting for server to receive request")
	}
	cancel()

	select {
	case res := <-resultCh:
		if res.err == nil {
			t.Fatal("expected error from cancelled in-flight request, got nil")
		}
		lower := strings.ToLower(res.err.Error())
		if !strings.Contains(lower, "cancel") && !strings.Contains(lower, "context") {
			t.Errorf("expected cancellation error, got: %v", res.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SideQuery did not return after context cancel")
	}
}
