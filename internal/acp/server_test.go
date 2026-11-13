package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/adapter"
	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/workdir"
)

func testServer(t *testing.T, stream adapter.StreamHandler) *Server {
	t.Helper()
	store := conversation.NewStore(nil, nil, "")
	workdirs, err := workdir.Open(t.TempDir() + "/workdirs.json")
	if err != nil {
		t.Fatal(err)
	}
	return New(stream, conversation.NewManager(store, slog.New(slog.NewTextHandler(io.Discard, nil))), store, workdirs, "test")
}

func TestHandshakeAndSessionLifecycle(t *testing.T) {
	dir := t.TempDir()
	s := testServer(t, func(ctx context.Context, msg adapter.InMessage, chunks chan<- adapter.StreamChunk) {
		defer close(chunks)
		chunks <- adapter.StreamChunk{ToolEvent: &adapter.ToolEventInfo{Type: adapter.ToolEventStart, ToolName: "read_file", Args: map[string]any{"path": "x.go"}}}
		chunks <- adapter.StreamChunk{ToolEvent: &adapter.ToolEventInfo{Type: adapter.ToolEventProgress, ToolName: "read_file", Result: "lendo"}}
		chunks <- adapter.StreamChunk{ToolEvent: &adapter.ToolEventInfo{Type: adapter.ToolEventComplete, ToolName: "read_file", Result: "content"}}
		chunks <- adapter.StreamChunk{Delta: "resposta"}
	})
	var first bytes.Buffer
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":` + jsonString(dir) + `,"mcpServers":[]}}` + "\n"
	if err := s.Serve(context.Background(), strings.NewReader(input), &first); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(first.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want two responses, got %q", first.String())
	}
	var created struct {
		Result struct {
			SessionID string `json:"sessionId"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &created); err != nil {
		t.Fatal(err)
	}
	if created.Result.SessionID == "" {
		t.Fatalf("missing session ID: %s", lines[1])
	}
	if got := workdir.Dir(s.scoped(created.Result.SessionID), ""); got != dir {
		t.Fatalf("session cwd = %q, want %q", got, dir)
	}
	var output bytes.Buffer
	input = `{"jsonrpc":"2.0","id":3,"method":"initialize","params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{"sessionId":` + jsonString(created.Result.SessionID) + `,"prompt":[{"type":"text","text":"olá"}]}}` + "\n" +
		`{"jsonrpc":"2.0","id":5,"method":"session/list","params":{"cwd":` + jsonString(dir) + `}}` + "\n"
	if err := s.Serve(context.Background(), strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"agent_message_chunk"`) || !strings.Contains(output.String(), `"end_turn"`) || !strings.Contains(output.String(), `"tool_call_update"`) || !strings.Contains(output.String(), `"rawOutput":"content"`) || !strings.Contains(output.String(), created.Result.SessionID) {
		t.Fatalf("unexpected ACP transcript: %s", output.String())
	}
	var toolIDs []string
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var event struct {
			Params struct {
				Update struct {
					ToolCallID string `json:"toolCallId"`
				} `json:"update"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Params.Update.ToolCallID != "" {
			toolIDs = append(toolIDs, event.Params.Update.ToolCallID)
		}
	}
	if len(toolIDs) != 3 || toolIDs[0] != toolIDs[1] || toolIDs[1] != toolIDs[2] {
		t.Fatalf("tool updates lost their identity: %v", toolIDs)
	}
}

func TestInvalidSessionDoesNotRunStream(t *testing.T) {
	called := false
	s := testServer(t, func(ctx context.Context, msg adapter.InMessage, chunks chan<- adapter.StreamChunk) {
		called = true
		close(chunks)
	})
	var output bytes.Buffer
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"session/prompt","params":{"sessionId":"missing","prompt":[{"type":"text","text":"olá"}]}}` + "\n"
	if err := s.Serve(context.Background(), strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	if called || !strings.Contains(output.String(), `"error"`) {
		t.Fatalf("invalid session accepted: %s", output.String())
	}
}

func TestCancelPrompt(t *testing.T) {
	s := testServer(t, func(ctx context.Context, msg adapter.InMessage, chunks chan<- adapter.StreamChunk) {
		<-ctx.Done()
		close(chunks)
	})
	dir := t.TempDir()
	id, err := s.newSession(context.Background(), json.RawMessage(`{"cwd":`+jsonString(dir)+`,"mcpServers":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"session/prompt","params":{"sessionId":` + jsonString(id) + `,"prompt":[{"type":"text","text":"olá"}]}}` + "\n" +
		`{"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":` + jsonString(id) + `}}` + "\n"
	var output bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"cancelled"`) {
		t.Fatalf("cancel did not stop turn: %s", output.String())
	}
}

func jsonString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
