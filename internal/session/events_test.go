package session

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEventJournalReplayOneHundredTurnsAcrossTwoClients(t *testing.T) {
	dir := t.TempDir()
	writer := NewEventJournal(dir)
	reader := NewEventJournal(dir)
	const sessionID = "cli:shared-100-turns"

	for turn := 0; turn < 100; turn++ {
		turnID := fmt.Sprintf("turn-%03d", turn)
		u, err := writer.AppendTurnEvent(sessionID, "cli", turnID, map[string]any{
			"sessionUpdate": "user_message_chunk",
			"turn":          turn,
		})
		if err != nil {
			t.Fatalf("append user turn %d: %v", turn, err)
		}
		a, err := writer.AppendTurnEvent(sessionID, "cli", turnID, map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"turn":          turn,
		})
		if err != nil {
			t.Fatalf("append agent turn %d: %v", turn, err)
		}
		if u.Sequence != uint64(turn*2+1) || a.Sequence != uint64(turn*2+2) {
			t.Fatalf("unexpected sequence numbers on turn %d: got (%d, %d)", turn, u.Sequence, a.Sequence)
		}
	}

	latest, err := reader.LatestSequence(sessionID)
	if err != nil || latest != 200 {
		t.Fatalf("LatestSequence = %d, err = %v; want 200", latest, err)
	}

	page1, err := reader.ReadAfter(sessionID, 0, 120)
	if err != nil || len(page1) != 120 || page1[0].Sequence != 1 || page1[119].Sequence != 120 {
		t.Fatalf("page1 mismatch: len=%d err=%v", len(page1), err)
	}
	page2, err := reader.ReadAfter(sessionID, 120, 120)
	if err != nil || len(page2) != 80 || page2[0].Sequence != 121 || page2[79].Sequence != 200 {
		t.Fatalf("page2 mismatch: len=%d err=%v", len(page2), err)
	}
}

func TestEventJournalIdempotentTurnAcrossTwoClients(t *testing.T) {
	dir := t.TempDir()
	clientA := NewEventJournal(dir)
	clientB := NewEventJournal(dir)

	var toolExecutions atomic.Int32
	runIdempotentTurn := func(j *EventJournal, key, prompt string) (string, error) {
		receipt, fresh, err := j.ClaimTurn("sess-shared", key, prompt)
		if err != nil {
			return "", err
		}
		if !fresh {
			return receipt.Content, nil
		}
		toolExecutions.Add(1)
		if _, err := j.AppendTurnEvent("sess-shared", "cli", key, map[string]any{
			"sessionUpdate": "tool_call",
			"name":          "edit_file",
		}); err != nil {
			return "", err
		}
		answer := "patch aplicado uma única vez"
		if err := j.CompleteTurn("sess-shared", key, prompt, "end_turn", answer, ""); err != nil {
			return "", err
		}
		return answer, nil
	}

	first, err := runIdempotentTurn(clientA, "turn-key-1", "corrija o bug")
	if err != nil {
		t.Fatalf("clientA failed: %v", err)
	}
	second, err := runIdempotentTurn(clientB, "turn-key-1", "corrija o bug")
	if err != nil {
		t.Fatalf("clientB replay failed: %v", err)
	}
	if first != second {
		t.Fatalf("clientB got %q, want %q", second, first)
	}
	if got := toolExecutions.Load(); got != 1 {
		t.Fatalf("tool executed %d times, want 1", got)
	}
	if _, _, err := clientB.ClaimTurn("sess-shared", "turn-key-1", "outro prompt diferente"); err == nil {
		t.Fatal("expected error when reusing turnId with a different prompt")
	}
}

func TestEventJournalConcurrentAppendsAndCancellation(t *testing.T) {
	j := NewEventJournal(t.TempDir())
	const sessionID = "whatsapp:5511999999999"

	subCh, unsub := j.Subscribe(sessionID, 64)
	defer unsub()

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for n := 0; n < 5; n++ {
				if _, err := j.AppendTurnEvent(sessionID, "whatsapp", fmt.Sprintf("w-%d", worker), map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"worker":        worker,
					"step":          n,
				}); err != nil {
					t.Errorf("concurrent append error: %v", err)
				}
			}
		}(g)
	}
	wg.Wait()

	events, err := j.ReadAfter(sessionID, 0, 100)
	if err != nil || len(events) != 40 {
		t.Fatalf("expected 40 ordered events, got %d (err=%v)", len(events), err)
	}
	for i, ev := range events {
		if ev.Sequence != uint64(i+1) || ev.Version != EventProtocolVersion {
			t.Fatalf("event %d sequence=%d version=%d", i, ev.Sequence, ev.Version)
		}
	}
	select {
	case <-subCh:
	default:
		t.Fatal("expected live subscriber to receive events")
	}

	turnCtx, release := j.BeginTurnContext(context.Background(), sessionID)
	defer release()

	cancelled := make(chan struct{})
	go func() {
		<-turnCtx.Done()
		close(cancelled)
	}()
	if !j.CancelSession(sessionID) {
		t.Fatal("CancelSession returned false for active turn")
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("turn context was not cancelled")
	}
}
