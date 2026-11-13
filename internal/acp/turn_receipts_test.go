package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/adapter"
)

func TestPromptTurnIDReplaysResultWithoutRepeatingToolsAcrossClients(t *testing.T) {
	calls := 0
	stream := func(_ context.Context, _ adapter.InMessage, chunks chan<- adapter.StreamChunk) {
		calls++
		chunks <- adapter.StreamChunk{Delta: "feito"}
		close(chunks)
	}
	first := testServer(t, stream)
	first.EventDir = t.TempDir()
	cwd := t.TempDir()
	id, err := first.newSession(context.Background(), json.RawMessage(`{"cwd":`+jsonString(cwd)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	params := `{"sessionId":` + jsonString(id) + `,"prompt":[{"type":"text","text":"corrija"}],"_meta":{"github.com/costa/polypod/turnId":"test-turn-1"}}`
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"session/prompt","params":` + params + `}` + "\n"
	var output bytes.Buffer
	if err := first.Serve(context.Background(), strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !strings.Contains(output.String(), `"stopReason":"end_turn"`) {
		t.Fatalf("first turn did not run: calls=%d output=%s", calls, output.String())
	}
	events, err := first.readEvents(id, 0, 100)
	if err != nil || len(events) != 2 {
		t.Fatalf("first turn journal: %d events, %v", len(events), err)
	}
	second := New(stream, first.Manager, first.Store, first.Workdirs, "test")
	second.EventDir = first.EventDir
	output.Reset()
	if err := second.Serve(context.Background(), strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !strings.Contains(output.String(), `"stopReason":"end_turn"`) || strings.Contains(output.String(), `"session/update"`) {
		t.Fatalf("duplicate turn ran or replayed side effects: calls=%d output=%s", calls, output.String())
	}
	events, err = second.readEvents(id, 0, 100)
	if err != nil || len(events) != 2 {
		t.Fatalf("duplicate turn appended events: %d, %v", len(events), err)
	}
	changed := strings.Replace(input, "corrija", "outro prompt", 1)
	output.Reset()
	if err := second.Serve(context.Background(), strings.NewReader(changed), &output); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !strings.Contains(output.String(), `"error"`) {
		t.Fatalf("reused turn ID accepted another prompt: calls=%d output=%s", calls, output.String())
	}
}

func TestStartedTurnIDDoesNotRunAgainAfterLostClient(t *testing.T) {
	s := testServer(t, func(_ context.Context, _ adapter.InMessage, chunks chan<- adapter.StreamChunk) { close(chunks) })
	s.EventDir = t.TempDir()
	id, err := s.newSession(context.Background(), json.RawMessage(`{"cwd":`+jsonString(t.TempDir())+`}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, fresh, err := s.claimTurn(id, "lost-turn", "corrija"); err != nil || !fresh {
		t.Fatalf("claim failed: fresh=%v err=%v", fresh, err)
	}
	if _, fresh, err := s.claimTurn(id, "lost-turn", "corrija"); err == nil || fresh {
		t.Fatalf("started turn was claimed twice: fresh=%v err=%v", fresh, err)
	}
}
