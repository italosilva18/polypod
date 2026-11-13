package acp

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/costa/polypod/internal/adapter"
)

func TestToolEditEmitsACPFileDiff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "math.go")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var eventPath string
	s := testServer(t, func(_ context.Context, _ adapter.InMessage, chunks chan<- adapter.StreamChunk) {
		defer close(chunks)
		chunks <- adapter.StreamChunk{ToolEvent: &adapter.ToolEventInfo{Type: adapter.ToolEventStart, ToolName: "edit_file", ToolID: "edit-1", Args: map[string]any{"path": "math.go"}}}
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Stat(eventPath); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Error("tool start event was not persisted")
				return
			}
			time.Sleep(time.Millisecond)
		}
		if err := os.WriteFile(path, []byte("new\n"), 0o600); err != nil {
			t.Error(err)
		}
		chunks <- adapter.StreamChunk{ToolEvent: &adapter.ToolEventInfo{Type: adapter.ToolEventComplete, ToolName: "edit_file", ToolID: "edit-1", Result: "feito"}}
	})
	id, err := s.newSession(context.Background(), []byte(`{"cwd":`+jsonString(dir)+`,"mcpServers":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	s.EventDir = t.TempDir()
	eventPath = filepath.Join(s.EventDir, id, "00000000000000000002.json")
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"session/prompt","params":{"sessionId":` + jsonString(id) + `,"prompt":[{"type":"text","text":"edite"}]}}` + "\n"
	var output bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"type":"diff"`, `"oldText":"old\n"`, `"newText":"new\n"`, `"path":` + jsonString(path)} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("ACP edit missing %s: %s", want, output.String())
		}
	}
}

func TestBuiltinFileWriteWithFilePathEmitsACPDiff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "calc.go")
	if err := os.WriteFile(path, []byte("package calc\n\nfunc Add(a, b int) int { return a - b }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var eventPath string
	s := testServer(t, func(_ context.Context, _ adapter.InMessage, chunks chan<- adapter.StreamChunk) {
		defer close(chunks)
		chunks <- adapter.StreamChunk{ToolEvent: &adapter.ToolEventInfo{
			Type:     adapter.ToolEventStart,
			ToolName: "file_write",
			ToolID:   "fw-1",
			Args:     map[string]any{"file_path": "calc.go"},
		}}
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Stat(eventPath); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Error("tool start event was not persisted")
				return
			}
			time.Sleep(time.Millisecond)
		}
		if err := os.WriteFile(path, []byte("package calc\n\nfunc Add(a, b int) int { return a + b }\n"), 0o600); err != nil {
			t.Error(err)
		}
		chunks <- adapter.StreamChunk{ToolEvent: &adapter.ToolEventInfo{
			Type:     adapter.ToolEventComplete,
			ToolName: "file_write",
			ToolID:   "fw-1",
			Result:   "Updated calc.go",
		}}
	})
	id, err := s.newSession(context.Background(), []byte(`{"cwd":`+jsonString(dir)+`,"mcpServers":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	s.EventDir = t.TempDir()
	eventPath = filepath.Join(s.EventDir, id, "00000000000000000002.json")
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"session/prompt","params":{"sessionId":` + jsonString(id) + `,"prompt":[{"type":"text","text":"corrija calc.go"}]}}` + "\n"
	var output bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"type":"diff"`, `return a - b`, `return a + b`, `"path":` + jsonString(path)} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("ACP file_write diff missing %s: %s", want, output.String())
		}
	}
}
