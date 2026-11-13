package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sashabaranov/go-openai/jsonschema"

	"github.com/costa/polypod/internal/adapter"
	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/ai"
	"github.com/costa/polypod/internal/config"
	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/headless"
	"github.com/costa/polypod/internal/session"
	"github.com/costa/polypod/internal/skill"
	"github.com/costa/polypod/internal/turncheckpoint"
)

func TestUnifiedSessionEventBusTwoClientsNoDuplicateToolsAndCancel(t *testing.T) {
	var toolCalls atomic.Int32
	var apiRound atomic.Int32
	blockStream := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		text := string(body)
		if strings.Contains(text, "BLOQUEAR_CANCELAMENTO") {
			select {
			case <-r.Context().Done():
				return
			case <-blockStream:
			}
		}
		round := apiRound.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if round == 1 {
			_, _ = io.WriteString(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_once\",\"type\":\"function\",\"function\":{\"name\":\"mutate_once\",\"arguments\":\"{}\"}}]}}]}\n\n")
			_, _ = io.WriteString(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		_, _ = io.WriteString(w, "data: {\"id\":\"2\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"concluído sem duplicar\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"2\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	defer close(blockStream)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	skills := skill.NewRegistry()
	skills.Register(&skill.Skill{
		Name:        "mutate_once",
		Description: "Executa mutação controlada uma única vez",
		Parameters:  jsonschema.Definition{Type: jsonschema.Object},
		Execute: func(map[string]string) (string, error) {
			toolCalls.Add(1)
			return "ok", nil
		},
	})
	agents := agent.NewRegistry()
	agents.Register(&agent.Agent{
		Name:    "default",
		Persona: "assistente",
		Skills:  []string{"mutate_once"},
	})

	client := ai.NewClient(config.AIConfig{
		Provider: "openai",
		APIKey:   "test-key",
		BaseURL:  srv.URL + "/v1",
		Model:    "gpt-test",
		MaxToks:  256,
		Tools:    true,
	}, skills)
	aiSvc := ai.NewService(client, nil, agents, "default", nil, logger)
	store := conversation.NewStore(nil, nil, "")
	convMgr := conversation.NewManager(store, logger)
	turns := turncheckpoint.New(t.TempDir(), nil)
	journalDir := t.TempDir()
	journalA := session.NewEventJournal(journalDir)
	journalB := session.NewEventJournal(journalDir)

	streamA := makeStreamHandlerWithEvents(convMgr, aiSvc, nil, nil, turns, journalA, logger, nil)
	streamB := makeStreamHandlerWithEvents(convMgr, aiSvc, nil, nil, turns, journalB, logger, nil)
	syncB := makeHandlerWithEvents(convMgr, aiSvc, nil, nil, turns, journalB, logger)

	const sharedSession = "sess-unified-bus"
	const idempotentTurn = "turn-idem-01"

	// Client 1 (headless stream-json) runs the turn and invokes mutate_once.
	var out1 bytes.Buffer
	err := headless.Run(context.Background(), headless.Config{
		Prompt:       "aplique o patch",
		OutputFormat: headless.FormatStreamJSON,
		SessionID:    sharedSession,
		TurnID:       idempotentTurn,
		EventJournal: journalA,
		Stdout:       &out1,
	}, nil, streamA, convMgr, aiSvc)
	if err != nil {
		t.Fatalf("client 1 failed: %v", err)
	}
	if got := toolCalls.Load(); got != 1 {
		t.Fatalf("toolCalls after client 1 = %d, want 1", got)
	}

	// Client 2 reconnects with the same session & turnId over stream AND sync channel:
	// neither must re-execute mutate_once.
	var out2 bytes.Buffer
	err = headless.Run(context.Background(), headless.Config{
		Prompt:       "aplique o patch",
		OutputFormat: headless.FormatStreamJSON,
		SessionID:    sharedSession,
		TurnID:       idempotentTurn,
		EventJournal: journalB,
		Stdout:       &out2,
	}, nil, streamB, convMgr, aiSvc)
	if err != nil {
		t.Fatalf("client 2 stream replay failed: %v", err)
	}
	syncResp, err := syncB(context.Background(), adapter.InMessage{
		Channel: "whatsapp",
		UserID:  "user1",
		Text:    "aplique o patch",
		Metadata: map[string]string{
			"polypod_session_id": sharedSession,
			"polypod_turn_id":    idempotentTurn,
		},
	})
	if err != nil {
		t.Fatalf("client 2 sync replay failed: %v", err)
	}
	if got := toolCalls.Load(); got != 1 {
		t.Fatalf("duplicate tool execution detected: toolCalls = %d, want 1", got)
	}
	if !strings.Contains(out2.String(), "concluído sem duplicar") || strings.TrimSpace(syncResp.Text) != "concluído sem duplicar" {
		t.Fatalf("unexpected idempotent replay payloads: stream=%q sync=%q", out2.String(), syncResp.Text)
	}

	events, err := journalB.ReadAfter(sharedSession, 0, 100)
	if err != nil || len(events) < 4 {
		t.Fatalf("expected >=4 durable events (user, tool_call, tool_call_update, delta, complete), got %d (err=%v)", len(events), err)
	}

	// Explicit cancellation over the shared session journal stops an in-flight turn.
	cancelChunks := make(chan adapter.StreamChunk, 16)
	go streamA(context.Background(), adapter.InMessage{
		Channel:  "headless",
		UserID:   "cli",
		Text:     "BLOQUEAR_CANCELAMENTO",
		Metadata: map[string]string{"polypod_session_id": sharedSession},
	}, cancelChunks)

	deadline := time.Now().Add(2 * time.Second)
	cancelled := false
	for time.Now().Before(deadline) {
		if journalA.CancelSession(sharedSession) {
			cancelled = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !cancelled {
		t.Fatal("CancelSession did not find active streaming turn")
	}
	var cancelErr error
	for chunk := range cancelChunks {
		if chunk.Error != nil {
			cancelErr = chunk.Error
		}
	}
	if cancelErr == nil {
		t.Fatal("expected cancelled stream to report an error")
	}
	afterCancel, err := journalB.ReadAfter(sharedSession, events[len(events)-1].Sequence, 50)
	if err != nil || len(afterCancel) == 0 {
		t.Fatalf("expected cancellation events in journal, got %d (err=%v)", len(afterCancel), err)
	}
	foundCancelled := false
	for _, ev := range afterCancel {
		if ev.Update["sessionUpdate"] == "turn_complete" && ev.Update["stopReason"] == "cancelled" {
			foundCancelled = true
		}
	}
	if !foundCancelled {
		t.Fatalf("expected turn_complete with stopReason=cancelled in %+v", afterCancel)
	}
}
