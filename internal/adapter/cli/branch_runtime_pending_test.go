package cli

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/session"
)

func TestBranchMutationsStopBeforeCopyingUnresolvedTurn(t *testing.T) {
	dir := t.TempDir()
	controller := newPersistentBranchController(
		dir,
		session.NewFileSystemStorage(filepath.Join(dir, "sessions"), nil),
		conversation.NewManager(conversation.NewStore(nil, nil, filepath.Join(dir, "conversation")), nil),
		func() string { return "parent" },
		func(string, string) {},
		func(*conversation.Session) error { return nil },
	)
	blocked := errors.New("turno anterior com ferramenta pendente")
	checks := 0
	controller.checkPending = func() error {
		checks++
		return blocked
	}
	if _, err := controller.create("child", nil); !errors.Is(err, blocked) {
		t.Fatalf("fork should stop at unresolved tool: %v", err)
	}
	if _, err := controller.switchTo("main", nil); !errors.Is(err, blocked) {
		t.Fatalf("switch should stop at unresolved tool: %v", err)
	}
	if checks != 2 {
		t.Fatalf("guard calls = %d, want 2", checks)
	}
	if _, err := controller.storage.Load("parent"); err != nil {
		t.Fatalf("guard changed parent session: %v", err)
	}
}

func TestBranchSwitchRefusesTargetWithUnresolvedTurn(t *testing.T) {
	dir := t.TempDir()
	storage := session.NewFileSystemStorage(filepath.Join(dir, "sessions"), nil)
	if err := storage.Save(&session.RestoredSession{
		ID: "child", Messages: []session.Message{{ID: "turn-1", Role: "user", Content: "ação pendente"}},
	}); err != nil {
		t.Fatal(err)
	}
	activeID := "parent"
	controller := newPersistentBranchController(
		dir, storage,
		conversation.NewManager(conversation.NewStore(nil, nil, filepath.Join(dir, "conversation")), nil),
		func() string { return activeID },
		func(id, _ string) { activeID = id },
		func(*conversation.Session) error { return nil },
	)
	manifest := &branchManifest{Root: "parent", Active: "main", Branches: map[string]branchRecord{
		"main": {SessionID: "parent"}, "child": {SessionID: "child", Parent: "main"},
	}}
	if err := controller.writeManifest(manifest); err != nil {
		t.Fatal(err)
	}
	blocked := errors.New("branch alvo exige /resume antes de trocar")
	controller.checkTarget = func(id string, messages []session.Message) error {
		if id != "child" || len(messages) != 1 || messages[0].ID != "turn-1" {
			t.Fatalf("unexpected target inspection: %q %+v", id, messages)
		}
		return blocked
	}
	if _, err := controller.switchTo("child", nil); !errors.Is(err, blocked) {
		t.Fatalf("switch should reject unresolved target: %v", err)
	}
	if activeID != "parent" {
		t.Fatalf("active branch changed despite rejection: %s", activeID)
	}
	active, err := controller.activeConversation()
	if err != nil || len(active.Messages) != 0 {
		t.Fatalf("active conversation was replaced: %+v %v", active, err)
	}
}

func TestStaleBranchSelectionCannotOverwriteAnotherInstance(t *testing.T) {
	dir := t.TempDir()
	activeID := "parent"
	controller := newPersistentBranchController(dir,
		session.NewFileSystemStorage(filepath.Join(dir, "sessions"), nil),
		conversation.NewManager(conversation.NewStore(nil, nil, filepath.Join(dir, "conversation")), nil),
		func() string { return activeID }, func(id, _ string) { activeID = id },
		func(*conversation.Session) error { return nil })
	manifest := &branchManifest{Root: "parent", Active: "child", Branches: map[string]branchRecord{
		"main": {SessionID: "parent"}, "child": {SessionID: "child", Parent: "main"},
	}}
	if err := controller.writeManifest(manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.create("new", nil); !errors.Is(err, session.ErrSessionConflict) {
		t.Fatalf("stale CLI silently reverted branch pointer: %v", err)
	}
	if err := controller.activateSessionForResume("parent"); err != nil {
		t.Fatalf("explicit resume could not select the branch: %v", err)
	}
	updated, err := controller.manifestForMutation()
	if err != nil || updated.Active != "main" {
		t.Fatalf("explicit resume did not persist branch selection: %+v, %v", updated, err)
	}
}
