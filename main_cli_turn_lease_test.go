package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/costa/polypod/internal/adapter"
	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/session"
	"github.com/costa/polypod/internal/turncheckpoint"
)

func TestCLITurnRejectsStaleManifestBeforeNextTool(t *testing.T) {
	storage := session.NewFileSystemStorage(t.TempDir(), slog.Default())
	turns := turncheckpoint.New(t.TempDir(), nil)
	msg := adapter.InMessage{Channel: "cli", UserID: "local", Metadata: map[string]string{
		"polypod_session_id": "branch", "polypod_session_revision": "",
	}}
	guard, err := beginCLITurnGuard(context.Background(), msg, turns, storage)
	if err != nil {
		t.Fatal(err)
	}
	sess := conversation.NewSession("cli:local", "cli", "local")
	sess.AddMessage(conversation.RoleUser, "pedido")
	sess.AddMessage(conversation.RoleAssistant, "resposta")
	revision, err := guard.PublishSnapshot(sess, "default")
	if err != nil || revision == "" {
		t.Fatalf("first turn did not publish a manifest revision: %q, %v", revision, err)
	}
	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}
	header, err := storage.LoadHeader("branch")
	if err != nil || header == nil || header.Revision != revision || header.MessageCount != 2 || header.Metadata.ProjectRoot == "" {
		t.Fatalf("published branch lost transcript or project root: %+v, %v", header, err)
	}
	if stale, err := beginCLITurnGuard(context.Background(), msg, turns, storage); !errors.Is(err, session.ErrSessionConflict) {
		if stale != nil {
			_ = stale.Close()
		}
		t.Fatalf("second CLI accepted stale session before tool execution: %v", err)
	}
	msg.Metadata["polypod_session_revision"] = revision
	fresh, err := beginCLITurnGuard(context.Background(), msg, turns, storage)
	if err != nil {
		t.Fatalf("current CLI revision was blocked: %v", err)
	}
	if err := fresh.Close(); err != nil {
		t.Fatal(err)
	}
}
