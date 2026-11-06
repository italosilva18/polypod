package vcr

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewRecorder verifies that the recorder constructor initializes the
// session with the provided ID, marks it as active, and sets a non-zero
// creation timestamp.
func TestNewRecorder(t *testing.T) {
	rec := NewRecorder("session-1")
	if rec == nil {
		t.Fatal("NewRecorder returned nil")
	}
	if !rec.IsActive() {
		t.Error("expected new recorder to be active")
	}
	snap := rec.recording
	if snap.ID != "session-1" {
		t.Errorf("ID = %q, want %q", snap.ID, "session-1")
	}
	if snap.CreatedAt.IsZero() {
		t.Error("CreatedAt should be non-zero")
	}
	if snap.Metadata == nil {
		t.Error("Metadata map should be initialized")
	}
}

// TestRecordHTTPInteractionWithHTTPTest exercises recording of an API call
// made against an httptest server. This simulates a real HTTP round-trip
// being captured as an APIResponse inside the recording.
func TestRecordHTTPInteractionWithHTTPTest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"gpt-test","text":"hello world","tokens":42}`))
	}))
	defer server.Close()

	rec := NewRecorder("http-session")

	// Record an outbound user message identifying the URL.
	rec.RecordMessage(Message{Role: "user", Content: "GET " + server.URL})

	// Perform the HTTP call.
	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	var api APIResponse
	if err := json.Unmarshal(body, &api); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	rec.RecordAPIResponse(api)
	rec.SetMetadata("url", server.URL)

	snap := rec.Stop()
	if len(snap.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(snap.Messages))
	}
	if len(snap.APIResponses) != 1 {
		t.Fatalf("apiResponses = %d, want 1", len(snap.APIResponses))
	}
	if snap.APIResponses[0].Text != "hello world" {
		t.Errorf("recorded text = %q", snap.APIResponses[0].Text)
	}
	if snap.APIResponses[0].Tokens != 42 {
		t.Errorf("recorded tokens = %d", snap.APIResponses[0].Tokens)
	}
	if snap.Metadata["url"] != server.URL {
		t.Errorf("metadata[url] = %q", snap.Metadata["url"])
	}
	if rec.IsActive() {
		t.Error("recorder should be inactive after Stop")
	}
}

// TestRecordAndReplayMessages records a sequence of messages and replays
// them in order via VCRPlayer.
func TestRecordAndReplayMessages(t *testing.T) {
	rec := NewRecorder("replay-msgs")
	rec.RecordMessage(Message{Role: "user", Content: "hi"})
	rec.RecordMessage(Message{Role: "assistant", Content: "hello"})
	rec.RecordMessage(Message{Role: "user", Content: "bye"})

	player := NewPlayer(rec.Stop())
	if !player.HasMore() {
		t.Fatal("HasMore should be true initially")
	}

	want := []string{"hi", "hello", "bye"}
	for i, w := range want {
		m, ok := player.NextMessage()
		if !ok {
			t.Fatalf("NextMessage[%d]: exhausted early", i)
		}
		if m.Content != w {
			t.Errorf("message[%d] = %q, want %q", i, m.Content, w)
		}
		if m.Timestamp == "" {
			t.Errorf("message[%d] missing auto timestamp", i)
		}
	}
	if _, ok := player.NextMessage(); ok {
		t.Error("expected exhausted message stream")
	}
}

// TestReplayToolCallsAndAPIResponses ensures the player returns tool calls
// and API responses in FIFO order and signals exhaustion correctly.
func TestReplayToolCallsAndAPIResponses(t *testing.T) {
	rec := NewRecorder("replay-mixed")
	rec.RecordToolCall(ToolCall{Name: "read", Input: map[string]any{"path": "a.txt"}, Output: "A"})
	rec.RecordToolCall(ToolCall{Name: "read", Input: map[string]any{"path": "b.txt"}, Output: "B"})
	rec.RecordAPIResponse(APIResponse{Model: "m1", Text: "one"})
	rec.RecordAPIResponse(APIResponse{Model: "m2", Text: "two"})

	player := NewPlayer(rec.Stop())

	tc1, ok := player.NextToolCall()
	if !ok || tc1.Name != "read" || tc1.Output != "A" {
		t.Errorf("tool call #1 = %+v", tc1)
	}
	tc2, ok := player.NextToolCall()
	if !ok || tc2.Output != "B" {
		t.Errorf("tool call #2 = %+v", tc2)
	}
	if _, ok := player.NextToolCall(); ok {
		t.Error("tool calls should be exhausted")
	}

	a1, ok := player.NextAPIResponse()
	if !ok || a1.Text != "one" {
		t.Errorf("api #1 = %+v", a1)
	}
	a2, ok := player.NextAPIResponse()
	if !ok || a2.Text != "two" {
		t.Errorf("api #2 = %+v", a2)
	}
	if _, ok := player.NextAPIResponse(); ok {
		t.Error("api responses should be exhausted")
	}
	if player.HasMore() {
		t.Error("HasMore should be false after exhaustion")
	}
}

// TestPlayerReset rewinds the player cursors so a recording can be replayed
// from the start.
func TestPlayerReset(t *testing.T) {
	rec := NewRecorder("reset")
	rec.RecordMessage(Message{Role: "user", Content: "one"})
	rec.RecordMessage(Message{Role: "user", Content: "two"})

	p := NewPlayer(rec.Stop())
	for i := 0; i < 2; i++ {
		if _, ok := p.NextMessage(); !ok {
			t.Fatalf("drain[%d]: exhausted early", i)
		}
	}
	if p.HasMore() {
		t.Fatal("HasMore should be false after draining")
	}
	p.Reset()
	if !p.HasMore() {
		t.Fatal("HasMore should be true after Reset")
	}
	m, ok := p.NextMessage()
	if !ok || m.Content != "one" {
		t.Errorf("after reset, first message = %+v", m)
	}
}

// TestSaveAndLoadRecording round-trips a recording through the filesystem
// using t.TempDir for storage and verifies fields survive the JSON
// marshal/unmarshal cycle.
func TestSaveAndLoadRecording(t *testing.T) {
	dir := t.TempDir()

	original := NewRecorder("persist-1")
	original.RecordMessage(Message{Role: "user", Content: "ping"})
	original.RecordToolCall(ToolCall{Name: "echo", Input: "x", Output: "x"})
	original.RecordAPIResponse(APIResponse{Model: "gpt", Text: "pong", Tokens: 5})
	original.SetMetadata("env", "test")

	snap := original.Stop()
	path, err := SaveRecording(snap, dir)
	if err != nil {
		t.Fatalf("SaveRecording: %v", err)
	}
	if !strings.HasSuffix(path, "persist-1.json") {
		t.Errorf("unexpected path %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("recording file missing: %v", err)
	}

	loaded, err := LoadRecording(path)
	if err != nil {
		t.Fatalf("LoadRecording: %v", err)
	}
	if loaded.ID != "persist-1" {
		t.Errorf("ID = %q", loaded.ID)
	}
	if len(loaded.Messages) != 1 || loaded.Messages[0].Content != "ping" {
		t.Errorf("messages = %+v", loaded.Messages)
	}
	if len(loaded.ToolCalls) != 1 || loaded.ToolCalls[0].Name != "echo" {
		t.Errorf("toolCalls = %+v", loaded.ToolCalls)
	}
	if len(loaded.APIResponses) != 1 || loaded.APIResponses[0].Text != "pong" {
		t.Errorf("apiResponses = %+v", loaded.APIResponses)
	}
	if loaded.Metadata["env"] != "test" {
		t.Errorf("metadata[env] = %q", loaded.Metadata["env"])
	}
}

// TestCassetteFormat inspects the on-disk JSON of a saved recording to
// verify the expected cassette schema: id, createdAt, messages, toolCalls,
// apiResponses, metadata.
func TestCassetteFormat(t *testing.T) {
	dir := t.TempDir()
	r := NewRecorder("format-check")
	r.RecordMessage(Message{Role: "assistant", Content: "hi"})
	r.RecordAPIResponse(APIResponse{Model: "m", Text: "t"})
	r.SetMetadata("k", "v")

	path, err := SaveRecording(r.Stop(), dir)
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Ensure it is indented JSON (SaveRecording uses MarshalIndent).
	if !strings.Contains(string(raw), "\n  ") {
		t.Error("expected indented JSON output")
	}

	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("unmarshal generic: %v", err)
	}

	for _, k := range []string{"id", "createdAt", "messages", "toolCalls", "apiResponses", "metadata"} {
		if _, ok := generic[k]; !ok {
			t.Errorf("cassette missing field %q", k)
		}
	}
	if generic["id"] != "format-check" {
		t.Errorf("id field = %v", generic["id"])
	}
}

// TestLoadRecordingNotFound ensures LoadRecording surfaces a read error for
// a missing cassette file.
func TestLoadRecordingNotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadRecording(filepath.Join(dir, "nope.json"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// TestRecorderInactiveIgnoresWrites confirms that once Stop is called,
// subsequent record calls are dropped and the snapshot remains unchanged.
func TestRecorderInactiveIgnoresWrites(t *testing.T) {
	r := NewRecorder("stopped")
	r.RecordMessage(Message{Role: "user", Content: "before"})
	snap := r.Stop()
	if len(snap.Messages) != 1 {
		t.Fatalf("pre-stop messages = %d", len(snap.Messages))
	}

	// These should all be no-ops.
	r.RecordMessage(Message{Role: "user", Content: "after"})
	r.RecordToolCall(ToolCall{Name: "x"})
	r.RecordAPIResponse(APIResponse{Model: "x"})

	// Internal state must not have grown.
	if len(r.recording.Messages) != 1 {
		t.Errorf("messages grew after Stop: %d", len(r.recording.Messages))
	}
	if len(r.recording.ToolCalls) != 0 {
		t.Errorf("toolCalls grew after Stop: %d", len(r.recording.ToolCalls))
	}
	if len(r.recording.APIResponses) != 0 {
		t.Errorf("apiResponses grew after Stop: %d", len(r.recording.APIResponses))
	}
}

// TestFixtureCacheRoundTrip verifies content-addressed fixture storage:
// CacheKey is deterministic, Put writes JSON, and Get returns the same
// payload. A missing key returns (nil, nil).
func TestFixtureCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	fc := NewFixtureCache(dir)

	input := map[string]any{"url": "https://example.test/api", "method": "GET"}
	key1 := CacheKey(input)
	key2 := CacheKey(input)
	if key1 != key2 {
		t.Errorf("CacheKey not deterministic: %q vs %q", key1, key2)
	}
	if key1 == "" {
		t.Error("CacheKey returned empty string")
	}

	// Miss before Put.
	got, err := fc.Get("http", key1)
	if err != nil {
		t.Fatalf("Get (miss): %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for cache miss, got %s", string(got))
	}

	// Put then Get.
	value := map[string]any{"status": 200, "body": "ok"}
	if err := fc.Put("http", key1, value); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err = fc.Get("http", key1)
	if err != nil {
		t.Fatalf("Get (hit): %v", err)
	}
	if got == nil {
		t.Fatal("expected fixture payload, got nil")
	}
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if decoded["body"] != "ok" {
		t.Errorf("fixture body = %v", decoded["body"])
	}

	// Ensure the file exists on disk with the expected name.
	expected := filepath.Join(dir, "http-"+key1+".json")
	if _, err := os.Stat(expected); err != nil {
		t.Errorf("fixture file missing: %v", err)
	}
}

// TestDehydratorRoundTrip checks that Dehydrate replaces CWD/ConfigHome
// tokens and Hydrate restores them, simulating "URL matching" style
// portable lookups in cassettes.
func TestDehydratorRoundTrip(t *testing.T) {
	d := &Dehydrator{
		CWD:        "/home/user/project",
		ConfigHome: "/home/user/.config/app",
	}
	original := "cwd=/home/user/project/src cfg=/home/user/.config/app/settings.json"
	deh := d.Dehydrate(original)
	if strings.Contains(deh, "/home/user/project") {
		t.Errorf("CWD not replaced: %q", deh)
	}
	if strings.Contains(deh, "/home/user/.config/app") {
		t.Errorf("ConfigHome not replaced: %q", deh)
	}
	if !strings.Contains(deh, "[CWD]") || !strings.Contains(deh, "[CONFIG_HOME]") {
		t.Errorf("missing placeholders: %q", deh)
	}

	hyd := d.Hydrate(deh)
	if hyd != original {
		t.Errorf("round trip mismatch:\n  got:  %q\n  want: %q", hyd, original)
	}
}
