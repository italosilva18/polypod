package cli

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/session"
	"github.com/costa/polypod/internal/turncheckpoint"
)

func TestInspectRecoveryOffersOnlySafeCheckpoint(t *testing.T) {
	manager := turncheckpoint.New(t.TempDir(), nil)
	messages, _ := json.Marshal([]map[string]string{{"role": "user", "content": "do work"}})
	cp := &turncheckpoint.AICheckpoint{
		Version: turncheckpoint.AIVersion, SessionID: "branch", TurnID: "turn", Format: "provider",
		Model: "model", Messages: messages, Phase: turncheckpoint.AIPhaseTools,
	}
	if err := manager.SaveAI(cp); err != nil {
		t.Fatal(err)
	}
	adapter := &Adapter{turnCheckpoint: manager}
	conversation := []session.Message{{ID: "turn", Role: "user", Content: "do work"}}
	notice, draft, resumeID := adapter.inspectRecovery("branch", conversation)
	if resumeID != "turn" || draft != "" || !strings.Contains(notice, "/recover") {
		t.Fatalf("safe checkpoint did not offer resume: %q %q %q", notice, draft, resumeID)
	}
	cp.PendingToolCallID = "external-write"
	if err := manager.SaveAI(cp); err != nil {
		t.Fatal(err)
	}
	notice, draft, resumeID = adapter.inspectRecovery("branch", conversation)
	if resumeID != "" || draft != "" || !strings.Contains(notice, "Retomada bloqueada") || !strings.Contains(notice, "external-write") {
		t.Fatalf("pending tool offered replay: %q %q %q", notice, draft, resumeID)
	}
}

func TestDiscardRecoveryPersistsResolutionBeforeReleasingNextTurn(t *testing.T) {
	ctx := conversation.WithJournalScope(context.Background(), "cli", "local", "branch", "default")
	conv := conversation.NewManager(conversation.NewStore(nil, nil, ""), slog.Default())
	sess, err := conv.GetSession(ctx, "cli", "local")
	if err != nil {
		t.Fatal(err)
	}
	user, err := conv.AddUserMessageDurable(ctx, sess, "pedido")
	if err != nil {
		t.Fatal(err)
	}
	manager := turncheckpoint.New(t.TempDir(), nil)
	transcript, _ := json.Marshal([]map[string]string{{"role": "user", "content": "pedido"}})
	cp := &turncheckpoint.AICheckpoint{Version: turncheckpoint.AIVersion, SessionID: "branch", TurnID: user.ID,
		Format: "provider", Model: "model", Messages: transcript, Phase: turncheckpoint.AIPhaseTools, PendingToolCallID: "write-1"}
	if err := manager.SaveAI(cp); err != nil {
		t.Fatal(err)
	}
	adapter := &Adapter{convMgr: conv, turnCheckpoint: manager, activeSessionID: "branch", logger: slog.Default()}
	if err := adapter.pendingCheckpointGuard(); err == nil {
		t.Fatal("pending turn did not block next request")
	}
	message, err := adapter.discardRecovery(ctx)
	if err != nil || !strings.Contains(message, "abandonado") {
		t.Fatalf("discard failed: %q, %v", message, err)
	}
	if len(sess.Messages) != 2 || !strings.HasPrefix(sess.Messages[1].Content, turncheckpoint.RecoveryDiscardNotePrefix) {
		t.Fatalf("resolution was not persisted: %+v", sess.Messages)
	}
	if err := adapter.pendingCheckpointGuard(); err != nil {
		t.Fatalf("discard did not release next request: %v", err)
	}
	loaded, err := manager.LoadAI("branch", user.ID)
	if err != nil || loaded.Phase != turncheckpoint.AIPhaseAbandoned || loaded.PendingToolCallID != "write-1" {
		t.Fatalf("discard audit lost pending tool: %+v, %v", loaded, err)
	}
}
