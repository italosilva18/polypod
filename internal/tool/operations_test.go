package tool

import (
	"path/filepath"
	"testing"

	"github.com/costa/polypod/internal/audit"
	"github.com/costa/polypod/internal/taskqueue"
)

func TestAuditToolRestoreRequiresExplicitInspectAndConfirmation(t *testing.T) {
	store, err := audit.Open(filepath.Join(t.TempDir(), "audit"))
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	RegisterAuditTool(reg, store)
	tool, ok := reg.Get("audit")
	if !ok {
		t.Fatal("audit tool not registered")
	}
	for _, args := range []map[string]any{
		{"action": "restore", "checkpoint_id": "id"},
		{"action": "restore", "checkpoint_id": "id", "confirm": true},
	} {
		if tool.ValidateInput(args).Valid {
			t.Fatalf("restore accepted incomplete confirmation: %+v", args)
		}
	}
	complete := map[string]any{"action": "restore", "checkpoint_id": "id", "confirm": true, "current_sha256": "missing"}
	if !tool.ValidateInput(complete).Valid || !tool.IsDestructive(complete) {
		t.Fatal("confirmed restore was not classified as destructive")
	}
}

func TestTaskQueueToolAcceptsNegativePriority(t *testing.T) {
	queue, err := taskqueue.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	reg := NewRegistry()
	RegisterTaskQueueTool(reg, queue)
	tool, ok := reg.Get("task_queue")
	if !ok {
		t.Fatal("task_queue tool not registered")
	}
	if !tool.ValidateInput(map[string]any{"action": "create", "prompt": "work", "priority": -10}).Valid {
		t.Fatal("valid negative priority rejected")
	}
	if tool.ValidateInput(map[string]any{"action": "create", "prompt": "work", "priority": -101}).Valid {
		t.Fatal("priority below bound accepted")
	}
}
