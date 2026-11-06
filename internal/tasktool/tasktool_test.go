package tasktool

import (
	"strings"
	"testing"
)

func TestNewTaskList(t *testing.T) {
	tl := NewTaskList()
	if tl == nil {
		t.Fatal("NewTaskList returned nil")
	}
	if got := len(tl.List()); got != 0 {
		t.Errorf("expected empty task list, got %d tasks", got)
	}
}

func TestAdd(t *testing.T) {
	tl := NewTaskList()
	task := tl.Add("Write tests", "high")
	if task == nil {
		t.Fatal("Add returned nil")
	}
	if task.Content != "Write tests" {
		t.Errorf("expected content 'Write tests', got %q", task.Content)
	}
	if task.Priority != "high" {
		t.Errorf("expected priority 'high', got %q", task.Priority)
	}
	if task.Status != "pending" {
		t.Errorf("expected status 'pending', got %q", task.Status)
	}
	if task.ID == "" {
		t.Error("expected non-empty ID")
	}
	if task.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
	if task.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
	}
}

func TestAddMultipleTasksUniqueIDs(t *testing.T) {
	tl := NewTaskList()
	t1 := tl.Add("Task 1", "high")
	t2 := tl.Add("Task 2", "medium")
	t3 := tl.Add("Task 3", "low")

	if t1.ID == t2.ID || t2.ID == t3.ID || t1.ID == t3.ID {
		t.Errorf("expected unique IDs, got %s, %s, %s", t1.ID, t2.ID, t3.ID)
	}
	if got := len(tl.List()); got != 3 {
		t.Errorf("expected 3 tasks, got %d", got)
	}
}

func TestUpdate(t *testing.T) {
	tl := NewTaskList()
	task := tl.Add("Initial task", "medium")

	if err := tl.Update(task.ID, "in_progress"); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	got, err := tl.Get(task.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Status != "in_progress" {
		t.Errorf("expected status 'in_progress', got %q", got.Status)
	}
}

func TestUpdateNotFound(t *testing.T) {
	tl := NewTaskList()
	err := tl.Update("nonexistent_id", "completed")
	if err == nil {
		t.Error("expected error for nonexistent task, got nil")
	}
	if !strings.Contains(err.Error(), "nao encontrada") {
		t.Errorf("expected 'nao encontrada' in error, got %q", err.Error())
	}
}

func TestRemove(t *testing.T) {
	tl := NewTaskList()
	t1 := tl.Add("Task A", "high")
	t2 := tl.Add("Task B", "medium")
	tl.Add("Task C", "low")

	if err := tl.Remove(t1.ID); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	tasks := tl.List()
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks after remove, got %d", len(tasks))
	}

	if _, err := tl.Get(t1.ID); err == nil {
		t.Error("expected error getting removed task, got nil")
	}

	if _, err := tl.Get(t2.ID); err != nil {
		t.Errorf("expected to find task B, got error: %v", err)
	}
}

func TestRemoveNotFound(t *testing.T) {
	tl := NewTaskList()
	tl.Add("Task 1", "medium")
	err := tl.Remove("task_999")
	if err == nil {
		t.Error("expected error for nonexistent task removal")
	}
}

func TestGet(t *testing.T) {
	tl := NewTaskList()
	added := tl.Add("Find me", "high")

	got, err := tl.Get(added.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Content != "Find me" {
		t.Errorf("expected content 'Find me', got %q", got.Content)
	}

	if _, err := tl.Get("bogus_id"); err == nil {
		t.Error("expected error for bogus ID")
	}
}

func TestListByStatus(t *testing.T) {
	tl := NewTaskList()
	t1 := tl.Add("Pending task", "high")
	t2 := tl.Add("Progress task", "medium")
	t3 := tl.Add("Done task", "low")
	t4 := tl.Add("Another pending", "medium")

	_ = tl.Update(t2.ID, "in_progress")
	_ = tl.Update(t3.ID, "completed")

	pending := tl.ListByStatus("pending")
	if len(pending) != 2 {
		t.Errorf("expected 2 pending tasks, got %d", len(pending))
	}

	inProgress := tl.ListByStatus("in_progress")
	if len(inProgress) != 1 {
		t.Errorf("expected 1 in_progress task, got %d", len(inProgress))
	}
	if len(inProgress) > 0 && inProgress[0].ID != t2.ID {
		t.Errorf("expected in_progress task to be %s, got %s", t2.ID, inProgress[0].ID)
	}

	completed := tl.ListByStatus("completed")
	if len(completed) != 1 {
		t.Errorf("expected 1 completed task, got %d", len(completed))
	}

	cancelled := tl.ListByStatus("cancelled")
	if len(cancelled) != 0 {
		t.Errorf("expected 0 cancelled tasks, got %d", len(cancelled))
	}

	// Silence unused variable warnings
	_ = t1
	_ = t4
}

func TestStatusTransitions(t *testing.T) {
	tl := NewTaskList()
	task := tl.Add("Transition task", "medium")

	transitions := []string{"in_progress", "completed", "cancelled", "pending"}
	for _, status := range transitions {
		if err := tl.Update(task.ID, status); err != nil {
			t.Fatalf("Update to %s failed: %v", status, err)
		}
		got, err := tl.Get(task.ID)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if got.Status != status {
			t.Errorf("expected status %q, got %q", status, got.Status)
		}
	}
}

func TestFormatEmpty(t *testing.T) {
	tl := NewTaskList()
	out := tl.Format()
	if !strings.Contains(out, "Nenhuma tarefa") {
		t.Errorf("expected 'Nenhuma tarefa' in output, got %q", out)
	}
}

func TestFormatWithTasks(t *testing.T) {
	tl := NewTaskList()
	t1 := tl.Add("Pending item", "high")
	t2 := tl.Add("In progress item", "medium")
	t3 := tl.Add("Completed item", "low")
	t4 := tl.Add("Cancelled item", "medium")

	_ = tl.Update(t2.ID, "in_progress")
	_ = tl.Update(t3.ID, "completed")
	_ = tl.Update(t4.ID, "cancelled")

	out := tl.Format()

	if !strings.Contains(out, "Lista de Tarefas") {
		t.Error("expected header 'Lista de Tarefas' in output")
	}
	if !strings.Contains(out, "[ ] Pending item") {
		t.Errorf("expected pending marker '[ ] Pending item', output:\n%s", out)
	}
	if !strings.Contains(out, "[>] In progress item") {
		t.Errorf("expected in_progress marker '[>] In progress item', output:\n%s", out)
	}
	if !strings.Contains(out, "[x] Completed item") {
		t.Errorf("expected completed marker '[x] Completed item', output:\n%s", out)
	}
	if !strings.Contains(out, "[-] Cancelled item") {
		t.Errorf("expected cancelled marker '[-] Cancelled item', output:\n%s", out)
	}
	if !strings.Contains(out, "!!!") {
		t.Error("expected high priority marker '!!!' in output")
	}

	_ = t1
}

func TestFormatPriorityMarkers(t *testing.T) {
	tl := NewTaskList()
	tl.Add("High task", "high")
	tl.Add("Medium task", "medium")
	tl.Add("Low task", "low")

	out := tl.Format()

	if !strings.Contains(out, "High task !!!") {
		t.Errorf("expected 'High task !!!', got:\n%s", out)
	}
	if !strings.Contains(out, "Low task -") {
		t.Errorf("expected 'Low task -', got:\n%s", out)
	}
	// Medium should have no suffix
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Medium task") {
			if strings.Contains(line, "!!!") || strings.HasSuffix(strings.TrimSpace(line), "-") {
				t.Errorf("medium priority should not have priority marker, got %q", line)
			}
		}
	}
}

func TestClear(t *testing.T) {
	tl := NewTaskList()
	tl.Add("Task 1", "high")
	tl.Add("Task 2", "medium")
	tl.Add("Task 3", "low")

	if got := len(tl.List()); got != 3 {
		t.Fatalf("expected 3 tasks before clear, got %d", got)
	}

	tl.Clear()

	if got := len(tl.List()); got != 0 {
		t.Errorf("expected 0 tasks after clear, got %d", got)
	}
}

func TestStats(t *testing.T) {
	tl := NewTaskList()
	t1 := tl.Add("Task 1", "high")
	t2 := tl.Add("Task 2", "medium")
	t3 := tl.Add("Task 3", "low")
	t4 := tl.Add("Task 4", "medium")
	t5 := tl.Add("Task 5", "high")

	_ = tl.Update(t2.ID, "in_progress")
	_ = tl.Update(t3.ID, "completed")
	_ = tl.Update(t4.ID, "completed")
	_ = tl.Update(t5.ID, "cancelled")

	stats := tl.Stats()

	if stats["total"] != 5 {
		t.Errorf("expected total 5, got %d", stats["total"])
	}
	if stats["pending"] != 1 {
		t.Errorf("expected pending 1, got %d", stats["pending"])
	}
	if stats["in_progress"] != 1 {
		t.Errorf("expected in_progress 1, got %d", stats["in_progress"])
	}
	if stats["completed"] != 2 {
		t.Errorf("expected completed 2, got %d", stats["completed"])
	}
	if stats["cancelled"] != 1 {
		t.Errorf("expected cancelled 1, got %d", stats["cancelled"])
	}

	_ = t1
}

func TestListReturnsCopy(t *testing.T) {
	tl := NewTaskList()
	tl.Add("Original", "high")

	tasks := tl.List()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}

	// Modifying the returned slice should not affect internal state
	tasks[0].Content = "Modified"

	tasks2 := tl.List()
	if tasks2[0].Content != "Original" {
		t.Errorf("List did not return a copy; internal state was modified: %q", tasks2[0].Content)
	}
}

func TestUpdateChangesUpdatedAt(t *testing.T) {
	tl := NewTaskList()
	task := tl.Add("Time task", "medium")
	originalUpdatedAt := task.UpdatedAt

	// Sleep briefly to ensure timestamp difference
	// Using a tight retry instead of time.Sleep to keep tests fast
	for i := 0; i < 1000; i++ {
		if err := tl.Update(task.ID, "in_progress"); err != nil {
			t.Fatalf("Update failed: %v", err)
		}
		got, _ := tl.Get(task.ID)
		if got.UpdatedAt.After(originalUpdatedAt) {
			return
		}
	}
	t.Error("expected UpdatedAt to be updated after Update call")
}
