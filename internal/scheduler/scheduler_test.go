package scheduler

import (
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestScheduler creates a scheduler writing to a temp file.
func newTestScheduler(t *testing.T) *Scheduler {
	t.Helper()
	dir := t.TempDir()
	dataFile := filepath.Join(dir, "tasks.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(dataFile, logger)
}

func TestNew(t *testing.T) {
	s := newTestScheduler(t)
	if s == nil {
		t.Fatal("New returned nil")
	}
	if s.tasks == nil {
		t.Error("tasks map not initialized")
	}
	if s.quit == nil {
		t.Error("quit channel not initialized")
	}
	if s.logger == nil {
		t.Error("logger not set")
	}
	if s.dataFile == "" {
		t.Error("dataFile not set")
	}
	if got := len(s.ListTasks()); got != 0 {
		t.Errorf("expected empty task list, got %d", got)
	}
}

func TestAddTask_AssignsID(t *testing.T) {
	s := newTestScheduler(t)
	task := &Task{
		Name:     "backup",
		Schedule: "*/5 * * * *",
		Command:  "echo hello",
	}
	if err := s.AddTask(task); err != nil {
		t.Fatalf("AddTask failed: %v", err)
	}
	if task.ID == "" {
		t.Error("expected ID to be generated")
	}
	if !strings.HasPrefix(task.ID, "task_") {
		t.Errorf("expected ID to start with 'task_', got %q", task.ID)
	}
	if task.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
	if !task.Enabled {
		t.Error("expected task to be enabled by default")
	}
}

func TestAddTask_PreservesExplicitID(t *testing.T) {
	s := newTestScheduler(t)
	task := &Task{
		ID:       "custom_id_123",
		Name:     "mytask",
		Schedule: "0 * * * *",
		Command:  "true",
	}
	if err := s.AddTask(task); err != nil {
		t.Fatalf("AddTask failed: %v", err)
	}
	if task.ID != "custom_id_123" {
		t.Errorf("expected ID 'custom_id_123', got %q", task.ID)
	}
}

func TestAddTask_UniqueIDs(t *testing.T) {
	s := newTestScheduler(t)
	ids := make(map[string]bool)
	for i := 0; i < 20; i++ {
		task := &Task{Name: "t", Schedule: "* * * * *", Command: "true"}
		if err := s.AddTask(task); err != nil {
			t.Fatalf("AddTask failed: %v", err)
		}
		if ids[task.ID] {
			t.Errorf("duplicate ID generated: %s", task.ID)
		}
		ids[task.ID] = true
	}
	if len(s.ListTasks()) != 20 {
		t.Errorf("expected 20 tasks, got %d", len(s.ListTasks()))
	}
}

func TestListTasks_Empty(t *testing.T) {
	s := newTestScheduler(t)
	tasks := s.ListTasks()
	if tasks == nil {
		t.Error("ListTasks returned nil")
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestListTasks_Multiple(t *testing.T) {
	s := newTestScheduler(t)
	names := []string{"alpha", "beta", "gamma"}
	for _, n := range names {
		if err := s.AddTask(&Task{Name: n, Schedule: "* * * * *", Command: "echo " + n}); err != nil {
			t.Fatalf("AddTask failed: %v", err)
		}
	}
	tasks := s.ListTasks()
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}
	found := make(map[string]bool)
	for _, tk := range tasks {
		found[tk.Name] = true
	}
	for _, n := range names {
		if !found[n] {
			t.Errorf("task %q not found in list", n)
		}
	}
}

func TestGetByID_ViaInternalMap(t *testing.T) {
	// No public GetByID exists, so we test retrieval via the tasks map,
	// which is what the scheduler_run skill uses internally.
	s := newTestScheduler(t)
	task := &Task{Name: "findme", Schedule: "* * * * *", Command: "ls"}
	if err := s.AddTask(task); err != nil {
		t.Fatalf("AddTask failed: %v", err)
	}

	s.mu.RLock()
	got, ok := s.tasks[task.ID]
	s.mu.RUnlock()

	if !ok {
		t.Fatalf("task with ID %q not found", task.ID)
	}
	if got.Name != "findme" {
		t.Errorf("expected name 'findme', got %q", got.Name)
	}
}

func TestGetByID_NonExistent(t *testing.T) {
	s := newTestScheduler(t)
	s.mu.RLock()
	_, ok := s.tasks["nonexistent_id"]
	s.mu.RUnlock()
	if ok {
		t.Error("expected lookup of nonexistent ID to fail")
	}
}

func TestRemoveTask(t *testing.T) {
	s := newTestScheduler(t)
	task := &Task{Name: "todelete", Schedule: "* * * * *", Command: "true"}
	if err := s.AddTask(task); err != nil {
		t.Fatalf("AddTask failed: %v", err)
	}
	id := task.ID

	if len(s.ListTasks()) != 1 {
		t.Fatalf("expected 1 task before removal")
	}

	if err := s.RemoveTask(id); err != nil {
		t.Fatalf("RemoveTask failed: %v", err)
	}

	if len(s.ListTasks()) != 0 {
		t.Errorf("expected 0 tasks after removal, got %d", len(s.ListTasks()))
	}

	s.mu.RLock()
	_, ok := s.tasks[id]
	s.mu.RUnlock()
	if ok {
		t.Error("task still in map after removal")
	}
}

func TestRemoveTask_NonExistent(t *testing.T) {
	s := newTestScheduler(t)
	// Removing non-existent task should not error (delete is a no-op on missing keys).
	if err := s.RemoveTask("nope_nope"); err != nil {
		t.Errorf("RemoveTask on missing ID should not error, got: %v", err)
	}
}

func TestRemoveTask_OnlyRemovesTarget(t *testing.T) {
	s := newTestScheduler(t)
	t1 := &Task{Name: "one", Schedule: "* * * * *", Command: "true"}
	t2 := &Task{Name: "two", Schedule: "* * * * *", Command: "true"}
	t3 := &Task{Name: "three", Schedule: "* * * * *", Command: "true"}

	for _, tk := range []*Task{t1, t2, t3} {
		if err := s.AddTask(tk); err != nil {
			t.Fatalf("AddTask failed: %v", err)
		}
	}

	if err := s.RemoveTask(t2.ID); err != nil {
		t.Fatalf("RemoveTask failed: %v", err)
	}

	remaining := s.ListTasks()
	if len(remaining) != 2 {
		t.Fatalf("expected 2 remaining tasks, got %d", len(remaining))
	}

	for _, tk := range remaining {
		if tk.ID == t2.ID {
			t.Error("removed task still present")
		}
	}
}

func TestCronValidation_Wildcard(t *testing.T) {
	now := time.Now()
	if !matchCron("* * * * *", now) {
		t.Error("expected '* * * * *' to match any time")
	}
}

func TestCronValidation_Invalid(t *testing.T) {
	now := time.Now()
	invalidExprs := []string{
		"",              // empty
		"* * *",         // too few fields
		"* * * * * *",   // too many fields
		"abc * * * *",   // non-numeric
		"*/abc * * * *", // invalid step
		"*/0 * * * *",   // zero step
		"not-a-cron",    // garbage
	}
	for _, expr := range invalidExprs {
		if matchCron(expr, now) {
			t.Errorf("expected invalid expression %q to NOT match", expr)
		}
	}
}

func TestCronValidation_ExactMinute(t *testing.T) {
	// Construct a time at a known minute.
	ref := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

	// "30 * * * *" should match minute 30.
	if !matchCron("30 * * * *", ref) {
		t.Error("expected '30 * * * *' to match minute 30")
	}
	// "31 * * * *" should NOT match minute 30.
	if matchCron("31 * * * *", ref) {
		t.Error("expected '31 * * * *' to NOT match minute 30")
	}
}

func TestCronValidation_StepValues(t *testing.T) {
	// Minute 10: */5 should match (10 % 5 == 0), */3 should not (10 % 3 != 0).
	ref := time.Date(2024, 1, 15, 10, 10, 0, 0, time.UTC)

	if !matchCron("*/5 * * * *", ref) {
		t.Error("expected '*/5 * * * *' to match minute 10")
	}
	if matchCron("*/3 * * * *", ref) {
		t.Error("expected '*/3 * * * *' to NOT match minute 10")
	}
}

func TestCronValidation_AllFields(t *testing.T) {
	// Monday, January 15 2024, 10:30 UTC. Weekday=1 (Monday).
	ref := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

	// Exact match across all fields.
	if !matchCron("30 10 15 1 1", ref) {
		t.Error("expected exact match across all fields")
	}
	// Mismatch on hour.
	if matchCron("30 11 15 1 1", ref) {
		t.Error("expected mismatch on hour")
	}
	// Mismatch on weekday.
	if matchCron("30 10 15 1 2", ref) {
		t.Error("expected mismatch on weekday")
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	dataFile := filepath.Join(dir, "persist.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	s1 := New(dataFile, logger)
	task := &Task{Name: "persistent", Schedule: "*/10 * * * *", Command: "echo persist"}
	if err := s1.AddTask(task); err != nil {
		t.Fatalf("AddTask failed: %v", err)
	}
	savedID := task.ID

	// New scheduler loading the same file.
	s2 := New(dataFile, logger)
	if err := s2.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	tasks := s2.ListTasks()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 loaded task, got %d", len(tasks))
	}
	if tasks[0].ID != savedID {
		t.Errorf("expected ID %q after load, got %q", savedID, tasks[0].ID)
	}
	if tasks[0].Name != "persistent" {
		t.Errorf("expected name 'persistent', got %q", tasks[0].Name)
	}
}

func TestLoad_MissingFileIsNoError(t *testing.T) {
	dir := t.TempDir()
	dataFile := filepath.Join(dir, "does-not-exist.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(dataFile, logger)
	if err := s.Load(); err != nil {
		t.Errorf("Load on missing file should be a no-op, got: %v", err)
	}
	if len(s.ListTasks()) != 0 {
		t.Error("expected no tasks after loading missing file")
	}
}

func TestSetCallback(t *testing.T) {
	s := newTestScheduler(t)
	called := false
	s.SetCallback(func(task *Task, result string) {
		called = true
	})
	if s.onRun == nil {
		t.Error("callback not registered")
	}
	// Trigger directly to confirm wiring (uses a no-op task with no command).
	s.onRun(&Task{Name: "test"}, "result")
	if !called {
		t.Error("callback was not invoked")
	}
}
