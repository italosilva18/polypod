package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/adapter"
)

func TestDurableEventsReplayOneHundredTurnsAcrossClients(t *testing.T) {
	streamCalls := 0
	stream := func(_ context.Context, _ adapter.InMessage, chunks chan<- adapter.StreamChunk) {
		streamCalls++
		close(chunks)
	}
	s := testServer(t, stream)
	s.EventDir = t.TempDir()
	cwd := t.TempDir()
	id, err := s.newSession(context.Background(), json.RawMessage(`{"cwd":`+jsonString(cwd)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	s.output = json.NewEncoder(io.Discard)
	for turn := range 100 {
		if err := s.appendEvent(id, map[string]any{"sessionUpdate": "user_message_chunk", "turn": turn}); err != nil {
			t.Fatal(err)
		}
		if err := s.update(id, map[string]any{"sessionUpdate": "agent_message_chunk", "turn": turn}); err != nil {
			t.Fatal(err)
		}
	}
	second := New(stream, s.Manager, s.Store, s.Workdirs, "test")
	second.EventDir = s.EventDir
	firstPage, err := second.readEvents(id, 0, 125)
	if err != nil || len(firstPage) != 125 || firstPage[0].Sequence != 1 || firstPage[124].Sequence != 125 {
		t.Fatalf("first client replay: %d events, %v", len(firstPage), err)
	}
	secondPage, err := second.readEvents(id, 125, 125)
	if err != nil || len(secondPage) != 75 || secondPage[0].Sequence != 126 || secondPage[74].Sequence != 200 {
		t.Fatalf("second client replay: %d events, %v", len(secondPage), err)
	}
	var output bytes.Buffer
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"_polypod/session_events","params":{"sessionId":` + jsonString(id) + `,"after":198,"limit":10}}` + "\n"
	if err := second.Serve(context.Background(), strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"sequence":199`) || !strings.Contains(output.String(), `"sequence":200`) || streamCalls != 0 {
		t.Fatalf("replay reran turn or lost cursor: %s", output.String())
	}
	output.Reset()
	input = `{"jsonrpc":"2.0","id":3,"method":"initialize","params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","id":4,"method":"session/load","params":{"sessionId":` + jsonString(id) + `,"cwd":` + jsonString(cwd) + `}}` + "\n"
	if err := second.Serve(context.Background(), strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(output.String(), `"method":"session/update"`); got != 200 || streamCalls != 0 {
		t.Fatalf("load replayed %d updates and called stream %d times", got, streamCalls)
	}
	if events, err := second.readEvents(id, 0, 1000); err != nil || len(events) != 200 {
		t.Fatalf("load duplicated durable events: %d, %v", len(events), err)
	}
}
