package background

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func newTestManager() *Manager {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewManager(logger)
}

// waitForStatus polls until the task reaches any of the target statuses or the
// timeout expires.
func waitForStatus(t *testing.T, m *Manager, id int, timeout time.Duration, targets ...string) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		task, ok := m.tasks[id]
		var status string
		if ok {
			status = task.Status
		}
		m.mu.Unlock()
		if !ok {
			t.Fatalf("task %d not found", id)
		}
		for _, target := range targets {
			if status == target {
				return status
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tasks[id].Status
}

func TestNewManager(t *testing.T) {
	m := newTestManager()
	if m == nil {
		t.Fatal("NewManager returned nil")
	}
	if m.tasks == nil {
		t.Fatal("tasks map not initialized")
	}
	if m.RunningCount() != 0 {
		t.Errorf("expected 0 running tasks, got %d", m.RunningCount())
	}
	if got := m.List(); !strings.Contains(got, "Nenhuma tarefa") {
		t.Errorf("expected empty list message, got %q", got)
	}
}

func TestRunAssignsIncrementingIDs(t *testing.T) {
	m := newTestManager()
	id1, err := m.Run("true")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	id2, err := m.Run("true")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if id1 != 1 || id2 != 2 {
		t.Errorf("expected IDs 1 and 2, got %d and %d", id1, id2)
	}
	waitForStatus(t, m, id1, 3*time.Second, "done", "failed")
	waitForStatus(t, m, id2, 3*time.Second, "done", "failed")
	m.Cleanup()
}

func TestRunSuccessfulCommand(t *testing.T) {
	m := newTestManager()
	id, err := m.Run("echo hello-world")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	status := waitForStatus(t, m, id, 3*time.Second, "done", "failed")
	if status != "done" {
		t.Errorf("expected status 'done', got %q", status)
	}

	m.mu.Lock()
	task := m.tasks[id]
	m.mu.Unlock()
	if task.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", task.ExitCode)
	}
	if task.FinishedAt.IsZero() {
		t.Error("FinishedAt should be set after completion")
	}

	output, err := m.GetOutput(id)
	if err != nil {
		t.Fatalf("GetOutput failed: %v", err)
	}
	if !strings.Contains(output, "hello-world") {
		t.Errorf("expected output to contain 'hello-world', got %q", output)
	}
	m.Cleanup()
}

func TestRunFailingCommand(t *testing.T) {
	m := newTestManager()
	id, err := m.Run("exit 42")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	status := waitForStatus(t, m, id, 3*time.Second, "done", "failed")
	if status != "failed" {
		t.Errorf("expected status 'failed', got %q", status)
	}

	m.mu.Lock()
	task := m.tasks[id]
	m.mu.Unlock()
	if task.ExitCode != 42 {
		t.Errorf("expected exit code 42, got %d", task.ExitCode)
	}
	m.Cleanup()
}

func TestKillRunningTask(t *testing.T) {
	m := newTestManager()
	id, err := m.Run("sleep 30")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Give it a moment to actually start
	time.Sleep(50 * time.Millisecond)

	if err := m.Kill(id); err != nil {
		t.Fatalf("Kill failed: %v", err)
	}

	m.mu.Lock()
	status := m.tasks[id].Status
	m.mu.Unlock()
	if status != "killed" {
		t.Errorf("expected status 'killed', got %q", status)
	}

	// Wait for execute goroutine to finish updating state
	time.Sleep(200 * time.Millisecond)
	m.Cleanup()
}

func TestKillNonexistentTask(t *testing.T) {
	m := newTestManager()
	err := m.Kill(9999)
	if err == nil {
		t.Fatal("expected error killing nonexistent task, got nil")
	}
	if !strings.Contains(err.Error(), "nao encontrada") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestKillAlreadyFinishedTask(t *testing.T) {
	m := newTestManager()
	id, err := m.Run("true")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	waitForStatus(t, m, id, 3*time.Second, "done", "failed")

	err = m.Kill(id)
	if err == nil {
		t.Fatal("expected error killing finished task, got nil")
	}
	if !strings.Contains(err.Error(), "ja finalizada") {
		t.Errorf("unexpected error message: %v", err)
	}
	m.Cleanup()
}

func TestGetOutputNonexistentTask(t *testing.T) {
	m := newTestManager()
	_, err := m.GetOutput(12345)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "nao encontrada") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunningCount(t *testing.T) {
	m := newTestManager()
	if m.RunningCount() != 0 {
		t.Fatalf("expected 0 initially, got %d", m.RunningCount())
	}

	id1, _ := m.Run("sleep 2")
	id2, _ := m.Run("sleep 2")
	time.Sleep(50 * time.Millisecond)

	if got := m.RunningCount(); got != 2 {
		t.Errorf("expected 2 running, got %d", got)
	}

	if err := m.Kill(id1); err != nil {
		t.Fatalf("Kill failed: %v", err)
	}
	if err := m.Kill(id2); err != nil {
		t.Fatalf("Kill failed: %v", err)
	}
	// Wait for executes to catch up
	time.Sleep(300 * time.Millisecond)
	m.Cleanup()
}

func TestList(t *testing.T) {
	m := newTestManager()
	id, err := m.Run("echo listing-test")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	waitForStatus(t, m, id, 3*time.Second, "done", "failed")

	out := m.List()
	if !strings.Contains(out, "Background Tasks") {
		t.Errorf("expected 'Background Tasks' header, got %q", out)
	}
	if !strings.Contains(out, "#1") {
		t.Errorf("expected task ID #1 in listing, got %q", out)
	}
	if !strings.Contains(out, "done") {
		t.Errorf("expected status 'done' in listing, got %q", out)
	}
	m.Cleanup()
}

func TestListTruncatesLongCommand(t *testing.T) {
	m := newTestManager()
	longCmd := "echo " + strings.Repeat("a", 100)
	id, err := m.Run(longCmd)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	waitForStatus(t, m, id, 3*time.Second, "done", "failed")

	out := m.List()
	if !strings.Contains(out, "...") {
		t.Errorf("expected truncation '...' in list for long command, got %q", out)
	}
	m.Cleanup()
}

func TestCleanupRemovesFinishedTasks(t *testing.T) {
	m := newTestManager()
	id1, _ := m.Run("true")
	id2, _ := m.Run("false")
	waitForStatus(t, m, id1, 3*time.Second, "done", "failed")
	waitForStatus(t, m, id2, 3*time.Second, "done", "failed")

	count := m.Cleanup()
	if count != 2 {
		t.Errorf("expected cleanup count 2, got %d", count)
	}

	m.mu.Lock()
	remaining := len(m.tasks)
	m.mu.Unlock()
	if remaining != 0 {
		t.Errorf("expected 0 tasks remaining, got %d", remaining)
	}
}

func TestCleanupPreservesRunningTasks(t *testing.T) {
	m := newTestManager()
	idRunning, _ := m.Run("sleep 5")
	idDone, _ := m.Run("true")
	waitForStatus(t, m, idDone, 3*time.Second, "done", "failed")
	time.Sleep(50 * time.Millisecond)

	count := m.Cleanup()
	if count != 1 {
		t.Errorf("expected cleanup count 1, got %d", count)
	}

	m.mu.Lock()
	_, stillThere := m.tasks[idRunning]
	_, goneTask := m.tasks[idDone]
	m.mu.Unlock()
	if !stillThere {
		t.Error("running task should not be cleaned up")
	}
	if goneTask {
		t.Error("finished task should have been removed")
	}

	// Clean up remaining running task
	_ = m.Kill(idRunning)
	time.Sleep(200 * time.Millisecond)
	m.Cleanup()
}

func TestRunCaptureStderr(t *testing.T) {
	m := newTestManager()
	id, err := m.Run("echo err-msg >&2")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	waitForStatus(t, m, id, 3*time.Second, "done", "failed")

	output, err := m.GetOutput(id)
	if err != nil {
		t.Fatalf("GetOutput failed: %v", err)
	}
	if !strings.Contains(output, "err-msg") {
		t.Errorf("expected stderr captured, got %q", output)
	}
	m.Cleanup()
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{5 * time.Second, "5s"},
		{30 * time.Second, "30s"},
		{90 * time.Second, "1m30s"},
		{2*time.Minute + 5*time.Second, "2m5s"},
	}
	for _, c := range cases {
		if got := formatDuration(c.d); got != c.want {
			t.Errorf("formatDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestTaskLimitEnforced(t *testing.T) {
	m := newTestManager()
	ids := make([]int, 0, 10)
	for i := 0; i < 10; i++ {
		id, err := m.Run("sleep 5")
		if err != nil {
			t.Fatalf("Run %d failed: %v", i, err)
		}
		ids = append(ids, id)
	}
	time.Sleep(50 * time.Millisecond)

	_, err := m.Run("true")
	if err == nil {
		t.Fatal("expected error when exceeding task limit, got nil")
	}
	if !strings.Contains(err.Error(), "limite") {
		t.Errorf("unexpected error message: %v", err)
	}

	// Cleanup
	for _, id := range ids {
		_ = m.Kill(id)
	}
	time.Sleep(300 * time.Millisecond)
	m.Cleanup()
}
