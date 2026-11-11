package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/adapter"
	"github.com/costa/polypod/internal/ai"
	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/turncheckpoint"
)

func TestQueuedCLIPromptBlockedBeforeAppendUntilDiscarded(t *testing.T) {
	ctx := conversation.WithJournalScope(context.Background(), "cli", "local", "branch", "default")
	conv := conversation.NewManager(conversation.NewStore(nil, nil, ""), slog.Default())
	sess, err := conv.GetSession(ctx, "cli", "local")
	if err != nil {
		t.Fatal(err)
	}
	user, err := conv.AddUserMessageDurable(ctx, sess, "pedido anterior")
	if err != nil {
		t.Fatal(err)
	}
	turns := turncheckpoint.New(t.TempDir(), nil)
	transcript, _ := json.Marshal([]map[string]string{{"role": "user", "content": "pedido anterior"}})
	cp := &turncheckpoint.AICheckpoint{Version: turncheckpoint.AIVersion, SessionID: "branch", TurnID: user.ID,
		Format: "provider", Model: "model", Messages: transcript, Phase: turncheckpoint.AIPhaseTools, PendingToolCallID: "external-write"}
	if err := turns.SaveAI(cp); err != nil {
		t.Fatal(err)
	}
	service := ai.NewService(nil, nil, nil, "default", nil, slog.Default())
	handler := makeStreamHandler(conv, service, nil, nil, turns, slog.Default())
	chunks := make(chan adapter.StreamChunk, 8)
	// This is the same handler path used when the composer auto-submits a
	// queued prompt immediately after a failed stream.
	handler(ctx, adapter.InMessage{Channel: "cli", UserID: "local", Text: "mensagem da fila",
		Metadata: map[string]string{"polypod_session_id": "branch"}}, chunks)
	var blocked error
	for chunk := range chunks {
		if chunk.Error != nil {
			blocked = chunk.Error
		}
	}
	if blocked == nil || !strings.Contains(blocked.Error(), "external-write") || len(sess.Messages) != 1 {
		t.Fatalf("queued turn was not stopped before append: error=%v messages=%+v", blocked, sess.Messages)
	}
	if _, err := conv.AddAssistantMessageDurable(ctx, sess, turncheckpoint.RecoveryDiscardNotePrefix+" Efeito externo incerto; não repetir."); err != nil {
		t.Fatal(err)
	}
	if err := turns.MarkAIAbandoned("branch", user.ID, "user_discard"); err != nil {
		t.Fatal(err)
	}
	if err := ensureCLINextTurnResolved(turns, "branch", sess); err != nil {
		t.Fatalf("explicit discard did not release next turn: %v", err)
	}
}
