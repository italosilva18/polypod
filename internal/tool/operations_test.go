package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/costa/polypod/internal/audit"
	"github.com/costa/polypod/internal/evolution"
	"github.com/costa/polypod/internal/taskqueue"
	"github.com/costa/polypod/internal/types"
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

func TestTaskQueueCreatesPlanBeforePublishingVerifiedTask(t *testing.T) {
	dir := t.TempDir()
	queue, err := taskqueue.Open(filepath.Join(dir, "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	store, err := evolution.Open(filepath.Join(dir, "evolution.db"), filepath.Join(dir, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reg := NewRegistry()
	RegisterTaskQueueTool(reg, queue, store)
	tool, ok := reg.Get("task_queue")
	if !ok {
		t.Fatal("task_queue tool not registered")
	}
	path := filepath.Join(dir, "artifact.txt")
	digest := sha256.Sum256([]byte("expected report"))
	args := map[string]any{"action": "create", "prompt": "produce report",
		"verification_path": path, "verification_sha256": hex.EncodeToString(digest[:]),
		"verification_pattern": "report workflow"}
	for _, incomplete := range []map[string]any{
		{"action": "create", "prompt": "work", "verification_path": path},
		{"action": "create", "prompt": "work", "verification_path": "relative.txt", "verification_sha256": args["verification_sha256"], "verification_pattern": "report workflow"},
	} {
		if tool.ValidateInput(incomplete).Valid {
			t.Fatalf("incomplete verification accepted: %+v", incomplete)
		}
	}
	result, err := reg.Execute("task_queue", args, types.ToolContext{Context: context.Background()})
	if err != nil || result.IsError {
		t.Fatalf("verified create failed: %+v err=%v", result, err)
	}
	var task taskqueue.Task
	if err := json.Unmarshal([]byte(result.Content), &task); err != nil {
		t.Fatal(err)
	}
	plan, err := store.GetVerificationPlan(context.Background(), "task", task.ID)
	if err != nil || plan.ExpectedSHA256 != args["verification_sha256"] || plan.CreatedAt > task.CreatedAt ||
		task.NextRunAt < plan.CreatedAt+2 {
		t.Fatalf("plan not registered before task: task=%+v plan=%+v err=%v", task, plan, err)
	}
}
