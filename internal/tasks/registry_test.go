package tasks

import (
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// TaskState basics
// ---------------------------------------------------------------------------

func TestNewTaskState(t *testing.T) {
	ts := NewTaskState(TaskTypeShell, "run ls")
	if ts.ID == "" {
		t.Error("ID should not be empty")
	}
	if ts.Type != TaskTypeShell {
		t.Errorf("Type = %s; want %s", ts.Type, TaskTypeShell)
	}
	if ts.Status != StatusPending {
		t.Errorf("Status = %s; want %s", ts.Status, StatusPending)
	}
	if ts.Description != "run ls" {
		t.Errorf("Description = %q; want %q", ts.Description, "run ls")
	}
	if ts.StartTime.IsZero() {
		t.Error("StartTime should be set")
	}
	if ts.OutputFile == "" {
		t.Error("OutputFile should be set")
	}
}

func TestTaskStatus_IsTerminal(t *testing.T) {
	cases := []struct {
		status TaskStatus
		want   bool
	}{
		{StatusPending, false},
		{StatusRunning, false},
		{StatusCompleted, true},
		{StatusFailed, true},
		{StatusKilled, true},
	}
	for _, c := range cases {
		if got := c.status.IsTerminal(); got != c.want {
			t.Errorf("%s.IsTerminal() = %v; want %v", c.status, got, c.want)
		}
	}
}

func TestGenerateTaskID_Prefixes(t *testing.T) {
	cases := []struct {
		taskType TaskType
		prefix   byte
	}{
		{TaskTypeShell, 'b'},
		{TaskTypeAgent, 'a'},
		{TaskTypeRemote, 'r'},
		{TaskTypeTeammate, 't'},
		{TaskTypeWorkflow, 'w'},
		{TaskTypeMonitor, 'm'},
		{TaskTypeDream, 'd'},
	}
	for _, c := range cases {
		id := GenerateTaskID(c.taskType)
		if len(id) != 9 {
			t.Errorf("len(%s) = %d; want 9", id, len(id))
		}
		if id[0] != c.prefix {
			t.Errorf("prefix for %s = %c; want %c", c.taskType, id[0], c.prefix)
		}
	}
}

func TestGenerateTaskID_UnknownType(t *testing.T) {
	id := GenerateTaskID(TaskType("bogus"))
	if len(id) != 9 {
		t.Errorf("len = %d; want 9", len(id))
	}
	if id[0] != 'x' {
		t.Errorf("prefix = %c; want x", id[0])
	}
}

func TestDefaultTaskConfigs_HasAllTypes(t *testing.T) {
	types := []TaskType{
		TaskTypeShell, TaskTypeAgent, TaskTypeRemote,
		TaskTypeTeammate, TaskTypeWorkflow, TaskTypeMonitor, TaskTypeDream,
	}
	for _, tt := range types {
		cfg, ok := DefaultTaskConfigs[tt]
		if !ok {
			t.Errorf("missing config for %s", tt)
			continue
		}
		if cfg.MaxConcurrent <= 0 {
			t.Errorf("%s MaxConcurrent = %d; want > 0", tt, cfg.MaxConcurrent)
		}
	}
}

// ---------------------------------------------------------------------------
// TaskRegistry
// ---------------------------------------------------------------------------

func TestTaskRegistry_RegisterAndGet(t *testing.T) {
	r := NewTaskRegistry()
	ts := NewTaskState(TaskTypeShell, "test")
	r.Register(ts)

	got, ok := r.Get(ts.ID)
	if !ok {
		t.Fatal("Get returned not found")
	}
	if got.ID != ts.ID {
		t.Errorf("got.ID = %s; want %s", got.ID, ts.ID)
	}

	if _, ok := r.Get("missing"); ok {
		t.Error("Get should return false for missing")
	}
}

func TestTaskRegistry_List(t *testing.T) {
	r := NewTaskRegistry()
	if len(r.List()) != 0 {
		t.Error("empty registry should return empty list")
	}
	r.Register(NewTaskState(TaskTypeShell, "a"))
	r.Register(NewTaskState(TaskTypeAgent, "b"))
	r.Register(NewTaskState(TaskTypeDream, "c"))

	if got := len(r.List()); got != 3 {
		t.Errorf("List size = %d; want 3", got)
	}
}

func TestTaskRegistry_Remove(t *testing.T) {
	r := NewTaskRegistry()
	ts := NewTaskState(TaskTypeShell, "bye")
	r.Register(ts)
	r.Remove(ts.ID)
	if _, ok := r.Get(ts.ID); ok {
		t.Error("task should be removed")
	}
	// Remove of missing id should be a no-op (not panic)
	r.Remove("nope")
}

func TestTaskRegistry_ListByStatus(t *testing.T) {
	r := NewTaskRegistry()
	a := NewTaskState(TaskTypeShell, "a")
	b := NewTaskState(TaskTypeShell, "b")
	c := NewTaskState(TaskTypeShell, "c")
	r.Register(a)
	r.Register(b)
	r.Register(c)

	// a stays pending, b→running, c→completed
	r.Update(b.ID, func(ts *TaskState) { ts.Status = StatusRunning })
	r.Update(c.ID, func(ts *TaskState) { ts.Status = StatusCompleted })

	pending := r.ListByStatus(StatusPending)
	if len(pending) != 1 || pending[0].ID != a.ID {
		t.Errorf("pending = %v", pending)
	}
	running := r.ListByStatus(StatusRunning)
	if len(running) != 1 || running[0].ID != b.ID {
		t.Errorf("running = %v", running)
	}
	completed := r.ListByStatus(StatusCompleted)
	if len(completed) != 1 || completed[0].ID != c.ID {
		t.Errorf("completed = %v", completed)
	}
}

func TestTaskRegistry_ListByType(t *testing.T) {
	r := NewTaskRegistry()
	r.Register(NewTaskState(TaskTypeShell, "s1"))
	r.Register(NewTaskState(TaskTypeShell, "s2"))
	r.Register(NewTaskState(TaskTypeAgent, "a1"))

	shells := r.ListByType(TaskTypeShell)
	if len(shells) != 2 {
		t.Errorf("shells = %d; want 2", len(shells))
	}
	agents := r.ListByType(TaskTypeAgent)
	if len(agents) != 1 {
		t.Errorf("agents = %d; want 1", len(agents))
	}
	dreams := r.ListByType(TaskTypeDream)
	if len(dreams) != 0 {
		t.Errorf("dreams = %d; want 0", len(dreams))
	}
}

func TestTaskRegistry_Update_Lifecycle(t *testing.T) {
	r := NewTaskRegistry()
	ts := NewTaskState(TaskTypeShell, "lifecycle")
	r.Register(ts)

	// Pending → Running
	ok := r.Update(ts.ID, func(s *TaskState) { s.Status = StatusRunning })
	if !ok {
		t.Fatal("Update returned false")
	}
	got, _ := r.Get(ts.ID)
	if got.Status != StatusRunning {
		t.Errorf("status = %s; want %s", got.Status, StatusRunning)
	}

	// Running → Completed with end time
	end := time.Now()
	r.Update(ts.ID, func(s *TaskState) {
		s.Status = StatusCompleted
		s.EndTime = &end
	})
	got, _ = r.Get(ts.ID)
	if got.Status != StatusCompleted {
		t.Errorf("status = %s; want %s", got.Status, StatusCompleted)
	}
	if got.EndTime == nil {
		t.Error("EndTime should be set")
	}

	// Update on missing id returns false
	if ok := r.Update("missing", func(s *TaskState) {}); ok {
		t.Error("Update on missing id should return false")
	}
}

func TestTaskRegistry_RunningCount(t *testing.T) {
	r := NewTaskRegistry()
	if r.RunningCount() != 0 {
		t.Error("empty registry RunningCount should be 0")
	}
	a := NewTaskState(TaskTypeShell, "a")
	b := NewTaskState(TaskTypeShell, "b")
	c := NewTaskState(TaskTypeShell, "c")
	r.Register(a)
	r.Register(b)
	r.Register(c)
	r.Update(a.ID, func(s *TaskState) { s.Status = StatusRunning })
	r.Update(b.ID, func(s *TaskState) { s.Status = StatusRunning })
	r.Update(c.ID, func(s *TaskState) { s.Status = StatusCompleted })

	if got := r.RunningCount(); got != 2 {
		t.Errorf("RunningCount = %d; want 2", got)
	}
}

func TestTaskRegistry_Kill_NoKiller(t *testing.T) {
	r := NewTaskRegistry()
	ts := NewTaskState(TaskTypeShell, "no killer")
	r.Register(ts)
	err := r.Kill(ts.ID)
	if err == nil {
		t.Error("Kill should error when no killer registered")
	}
}

func TestTaskRegistry_Kill_Missing(t *testing.T) {
	r := NewTaskRegistry()
	if err := r.Kill("ghost"); err == nil {
		t.Error("Kill on missing id should error")
	}
}

func TestTaskRegistry_Kill_AlreadyTerminal(t *testing.T) {
	r := NewTaskRegistry()
	ts := NewTaskState(TaskTypeShell, "done")
	r.Register(ts)
	r.Update(ts.ID, func(s *TaskState) { s.Status = StatusCompleted })
	if err := r.Kill(ts.ID); err == nil {
		t.Error("Kill on terminal task should error")
	}
}

// fakeKiller implements TaskKiller for tests.
type fakeKiller struct {
	typ    TaskType
	called bool
}

func (f *fakeKiller) Name() string   { return "fake" }
func (f *fakeKiller) Type() TaskType { return f.typ }
func (f *fakeKiller) Kill(id string, r *TaskRegistry) error {
	f.called = true
	r.Update(id, func(s *TaskState) { s.Status = StatusKilled })
	return nil
}

func TestTaskRegistry_Kill_WithKiller(t *testing.T) {
	r := NewTaskRegistry()
	k := &fakeKiller{typ: TaskTypeShell}
	r.RegisterKiller(k)

	ts := NewTaskState(TaskTypeShell, "killable")
	r.Register(ts)
	r.Update(ts.ID, func(s *TaskState) { s.Status = StatusRunning })

	if err := r.Kill(ts.ID); err != nil {
		t.Fatalf("Kill error: %v", err)
	}
	if !k.called {
		t.Error("killer should be called")
	}
	got, _ := r.Get(ts.ID)
	if got.Status != StatusKilled {
		t.Errorf("status = %s; want %s", got.Status, StatusKilled)
	}
}

func TestIsBackgroundTask(t *testing.T) {
	cases := []struct {
		status TaskStatus
		want   bool
	}{
		{StatusPending, true},
		{StatusRunning, true},
		{StatusCompleted, false},
		{StatusFailed, false},
		{StatusKilled, false},
	}
	for _, c := range cases {
		ts := &TaskState{Status: c.status}
		if got := IsBackgroundTask(ts); got != c.want {
			t.Errorf("IsBackgroundTask(%s) = %v; want %v", c.status, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// PriorityTaskQueue
// ---------------------------------------------------------------------------

func TestPriorityTaskQueue_EmptyDequeue(t *testing.T) {
	q := NewPriorityTaskQueue()
	if !q.IsEmpty() {
		t.Error("new queue should be empty")
	}
	if got, ok := q.Dequeue(); ok || got != nil {
		t.Errorf("empty Dequeue = (%v, %v); want (nil, false)", got, ok)
	}
	if _, ok := q.Peek(); ok {
		t.Error("empty Peek should return false")
	}
}

func TestPriorityTaskQueue_Ordering(t *testing.T) {
	q := NewPriorityTaskQueue()

	q.Enqueue(&PriorityTask{ID: "a", Priority: 1, Enqueued: time.Now()})
	q.Enqueue(&PriorityTask{ID: "b", Priority: 10, Enqueued: time.Now()})
	q.Enqueue(&PriorityTask{ID: "c", Priority: 5, Enqueued: time.Now()})

	got, _ := q.Dequeue()
	if got.ID != "b" {
		t.Errorf("first = %s; want b", got.ID)
	}
	got, _ = q.Dequeue()
	if got.ID != "c" {
		t.Errorf("second = %s; want c", got.ID)
	}
	got, _ = q.Dequeue()
	if got.ID != "a" {
		t.Errorf("third = %s; want a", got.ID)
	}
}

func TestPriorityTaskQueue_EnqueueNil(t *testing.T) {
	q := NewPriorityTaskQueue()
	q.Enqueue(nil)
	if q.Len() != 0 {
		t.Errorf("Len after nil enqueue = %d; want 0", q.Len())
	}
}

func TestPriorityTaskQueue_Deadline(t *testing.T) {
	q := NewPriorityTaskQueue()
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)

	q.Enqueue(&PriorityTask{ID: "expired", Deadline: past})
	q.Enqueue(&PriorityTask{ID: "alive", Deadline: future})

	expired := q.DequeueExpired(time.Now())
	if len(expired) != 1 || expired[0].ID != "expired" {
		t.Errorf("expired = %v", expired)
	}
	if q.Len() != 1 {
		t.Errorf("Len after DequeueExpired = %d; want 1", q.Len())
	}

	remaining, _ := q.Dequeue()
	if remaining.ID != "alive" {
		t.Errorf("remaining = %s; want alive", remaining.ID)
	}
}

func TestPriorityTaskQueue_DequeueByID(t *testing.T) {
	q := NewPriorityTaskQueue()
	q.Enqueue(&PriorityTask{ID: "a", Priority: 1})
	q.Enqueue(&PriorityTask{ID: "b", Priority: 2})
	q.Enqueue(&PriorityTask{ID: "c", Priority: 3})

	got, ok := q.DequeueByID("b")
	if !ok {
		t.Fatal("DequeueByID should find b")
	}
	if got.ID != "b" {
		t.Errorf("got = %s; want b", got.ID)
	}
	if q.Len() != 2 {
		t.Errorf("Len = %d; want 2", q.Len())
	}

	// Missing id
	if _, ok := q.DequeueByID("zzz"); ok {
		t.Error("DequeueByID on missing should return false")
	}

	// Higher priority should still dequeue first (c before a)
	next, _ := q.Dequeue()
	if next.ID != "c" {
		t.Errorf("next = %s; want c", next.ID)
	}
}

func TestPriorityTaskQueue_Clear(t *testing.T) {
	q := NewPriorityTaskQueue()
	q.Enqueue(&PriorityTask{ID: "a", Priority: 1})
	q.Enqueue(&PriorityTask{ID: "b", Priority: 2})
	if q.Len() != 2 {
		t.Errorf("before Clear Len = %d; want 2", q.Len())
	}
	q.Clear()
	if q.Len() != 0 {
		t.Errorf("after Clear Len = %d; want 0", q.Len())
	}
	if !q.IsEmpty() {
		t.Error("should be empty after Clear")
	}
}

func TestPriorityTaskQueue_Peek(t *testing.T) {
	q := NewPriorityTaskQueue()
	q.Enqueue(&PriorityTask{ID: "a", Priority: 1})
	q.Enqueue(&PriorityTask{ID: "b", Priority: 10})

	got, ok := q.Peek()
	if !ok || got.ID != "b" {
		t.Errorf("Peek = %v; want b", got)
	}
	// Peek does not remove
	if q.Len() != 2 {
		t.Errorf("Len after Peek = %d; want 2", q.Len())
	}
}

func TestPriorityTaskQueue_DrainAll(t *testing.T) {
	q := NewPriorityTaskQueue()
	q.Enqueue(&PriorityTask{ID: "a", Priority: 1})
	q.Enqueue(&PriorityTask{ID: "b", Priority: 3})
	q.Enqueue(&PriorityTask{ID: "c", Priority: 2})

	drained := q.DrainAll()
	if len(drained) != 3 {
		t.Fatalf("drained = %d; want 3", len(drained))
	}
	if drained[0].ID != "b" {
		t.Errorf("drained[0] = %s; want b", drained[0].ID)
	}
	if !q.IsEmpty() {
		t.Error("queue should be empty after DrainAll")
	}
}

func TestPriorityTaskQueue_Enqueue_AutoTimestamp(t *testing.T) {
	q := NewPriorityTaskQueue()
	task := &PriorityTask{ID: "a", Priority: 1}
	q.Enqueue(task)
	if task.Enqueued.IsZero() {
		t.Error("Enqueue should set Enqueued time if zero")
	}
}

func TestPriorityTaskQueue_Snapshot(t *testing.T) {
	q := NewPriorityTaskQueue()
	q.Enqueue(&PriorityTask{ID: "a", Priority: 1})
	q.Enqueue(&PriorityTask{ID: "b", Priority: 2})
	snap := q.Snapshot()
	if len(snap) != 2 {
		t.Errorf("snapshot size = %d; want 2", len(snap))
	}
	// Snapshot should not drain the queue
	if q.Len() != 2 {
		t.Errorf("Len after Snapshot = %d; want 2", q.Len())
	}
}
