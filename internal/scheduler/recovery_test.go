package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestScheduleFormatsAndBounds(t *testing.T) {
	for _, schedule := range []string{
		"0,15,30,45 9-17 * * 1-5",
		"*/10 8-18/2 * * 1,3,5",
		"@every 15m",
		"@at 2030-01-02T09:00:00-03:00",
	} {
		if _, err := parseSchedule(schedule); err != nil {
			t.Errorf("parseSchedule(%q): %v", schedule, err)
		}
	}
	for _, schedule := range []string{
		"99 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *",
		"* * * * 8", "*/0 * * * *", "1-90 * * * *", "@every 0s",
		"@at tomorrow", "0 0 30 2 *",
	} {
		if _, err := nextOccurrence(schedule, time.Now(), ""); err == nil {
			t.Errorf("nextOccurrence(%q) accepted invalid schedule", schedule)
		}
	}
	if _, err := nextOccurrence("0 9 * * *", time.Now(), "Mars/Base"); err == nil {
		t.Fatal("unknown timezone accepted")
	}
}

func TestNextOccurrenceUsesTimeZoneAndOneShot(t *testing.T) {
	after := time.Date(2026, 11, 6, 11, 0, 0, 0, time.UTC) // 08:00 in Sao Paulo.
	next, err := nextOccurrence("0 9 * * *", after, "America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	if got := next.In(time.UTC); !got.Equal(time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected next run: %s", got)
	}
	once, err := nextOccurrence("@at 2026-11-06T09:00:00-03:00", after, "")
	if err != nil || !once.Equal(next) {
		t.Fatalf("one-shot next=%s err=%v", once, err)
	}
	if expired, err := nextOccurrence("@at 2026-11-06T09:00:00-03:00", next, ""); err != nil || !expired.IsZero() {
		t.Fatalf("expired one-shot next=%s err=%v", expired, err)
	}
}

func TestZeroRetriesAndMissedRunSurviveRestart(t *testing.T) {
	s1 := newTestScheduler(t)
	task := &Task{Name: "sem repeticao", Schedule: "@every 1h", Prompt: "fazer", MaxRetries: 0}
	if err := s1.AddTask(task); err != nil {
		t.Fatal(err)
	}
	task.NextRunAt = time.Now().Add(-2 * time.Hour)
	if err := s1.Save(); err != nil {
		t.Fatal(err)
	}
	s2 := New(s1.dataFile, s1.logger)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	reloaded := s2.tasks[task.ID]
	if reloaded.MaxRetries != 0 || reloaded.NextRunAt.After(time.Now()) {
		t.Fatalf("retry or due time lost: %+v", reloaded)
	}
	calls := 0
	s2.SetPromptRunner(func(context.Context, *Task) (string, error) {
		calls++
		return "", errors.New("falha definitiva")
	})
	s2.runTaskAt(reloaded, time.Now(), false)
	if calls != 1 || reloaded.LastStatus != "failed" || reloaded.CurrentRunID != "" {
		t.Fatalf("calls=%d task=%+v", calls, reloaded)
	}
	if !reloaded.NextRunAt.After(time.Now()) {
		t.Fatalf("next future occurrence not scheduled: %s", reloaded.NextRunAt)
	}
}

func TestInterruptedRunKeepsAttemptAndRunID(t *testing.T) {
	s1 := newTestScheduler(t)
	task := &Task{Name: "interrompida", Schedule: "@every 1h", Prompt: "fazer", MaxRetries: 2}
	if err := s1.AddTask(task); err != nil {
		t.Fatal(err)
	}
	task.CurrentRunID = "run_fixed"
	task.CurrentAttempt = 1
	task.CurrentStartedAt = time.Now().Add(-time.Minute)
	task.CurrentDueAt = time.Now().Add(-2 * time.Minute)
	task.LastStatus = "running"
	if err := s1.Save(); err != nil {
		t.Fatal(err)
	}
	s2 := New(s1.dataFile, s1.logger)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	reloaded := s2.tasks[task.ID]
	if reloaded.CurrentRunID != "run_fixed" || reloaded.CurrentAttempt != 1 || reloaded.LastStatus != "retrying" {
		t.Fatalf("run state not recovered: %+v", reloaded)
	}
	if len(reloaded.History) != 1 || reloaded.History[0].Status != "interrupted" {
		t.Fatalf("interruption not audited: %+v", reloaded.History)
	}
	s2.SetPromptRunner(func(_ context.Context, got *Task) (string, error) {
		if got.CurrentRunID != "run_fixed" || got.CurrentAttempt != 2 {
			t.Fatalf("resumed as a new run: %+v", got)
		}
		return "recuperada", nil
	})
	s2.runTaskAt(reloaded, time.Now(), false)
	if reloaded.LastStatus != "success" || len(reloaded.History) != 2 || reloaded.History[1].Attempt != 2 || reloaded.History[1].RunID != "run_fixed" {
		t.Fatalf("resumption failed: %+v", reloaded)
	}
}

func TestInterruptedOneShotDoesNotStartASecondRun(t *testing.T) {
	s1 := newTestScheduler(t)
	task := &Task{Name: "unica", Schedule: "@at 2030-01-02T09:00:00-03:00", Prompt: "enviar", MaxRetries: 0}
	if err := s1.AddTask(task); err != nil {
		t.Fatal(err)
	}
	task.CurrentRunID = "run_once_fixed"
	task.CurrentAttempt = 1
	task.CurrentStartedAt = time.Now().Add(-time.Minute)
	task.CurrentDueAt = time.Now().Add(-2 * time.Minute)
	task.NextRunAt = time.Time{} // A one-shot clears its next due time when claimed.
	task.LastStatus = "running"
	if err := s1.Save(); err != nil {
		t.Fatal(err)
	}
	s2 := New(s1.dataFile, s1.logger)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := s2.GetTask(task.ID)
	if reloaded.Enabled || !reloaded.NextRunAt.IsZero() || reloaded.CurrentRunID != "" || reloaded.LastStatus != "failed" {
		t.Fatalf("one-shot was rescheduled after interruption: %+v", reloaded)
	}
	if len(reloaded.PendingNotices) != 1 || reloaded.PendingNotices[0].RunID != "run_once_fixed" {
		t.Fatalf("failure notification missing: %+v", reloaded.PendingNotices)
	}
	calls := 0
	s2.SetPromptRunner(func(context.Context, *Task) (string, error) { calls++; return "", nil })
	s2.tick(time.Now())
	time.Sleep(20 * time.Millisecond)
	if calls != 0 {
		t.Fatalf("one-shot executed %d extra times", calls)
	}
}

func TestSuccessfulAttemptJournalPreventsReplayAfterSnapshotCrash(t *testing.T) {
	s1 := newTestScheduler(t)
	task := &Task{Name: "efeito externo", Schedule: "@at 2030-01-02T09:00:00-03:00", Prompt: "enviar", MaxRetries: 1}
	if err := s1.AddTask(task); err != nil {
		t.Fatal(err)
	}
	task.CurrentRunID = "run_success_before_snapshot"
	task.CurrentAttempt = 1
	task.CurrentStartedAt = time.Now().Add(-time.Minute)
	task.CurrentDueAt = time.Now().Add(-2 * time.Minute)
	task.NextRunAt = time.Time{}
	task.LastStatus = "running"
	if err := s1.Save(); err != nil {
		t.Fatal(err)
	}
	if err := s1.writeRunRecord(RunRecord{ID: "run_success_before_snapshot_attempt_1", TaskID: task.ID, RunID: task.CurrentRunID, Attempt: 1, StartedAt: task.CurrentStartedAt, FinishedAt: time.Now(), Status: "success", Result: "enviado"}); err != nil {
		t.Fatal(err)
	}
	s2 := New(s1.dataFile, s1.logger)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := s2.GetTask(task.ID)
	if reloaded.LastStatus != "success" || reloaded.LastResult != "enviado" || reloaded.Enabled || reloaded.RunCount != 1 || reloaded.CurrentRunID != "" {
		t.Fatalf("journal result was not reconciled: %+v", reloaded)
	}
	if len(reloaded.PendingNotices) != 1 || reloaded.PendingNotices[0].RunID != "run_success_before_snapshot" {
		t.Fatalf("completion was not queued: %+v", reloaded.PendingNotices)
	}
	calls := 0
	s2.SetPromptRunner(func(context.Context, *Task) (string, error) { calls++; return "", nil })
	s2.tick(time.Now())
	time.Sleep(20 * time.Millisecond)
	if calls != 0 {
		t.Fatalf("successful external action replayed %d times", calls)
	}
}

func TestMisfireSkipAndUpdate(t *testing.T) {
	s := newTestScheduler(t)
	task := &Task{Name: "rotina", Schedule: "@every 1h", Prompt: "primeiro", MisfirePolicy: "skip"}
	if err := s.AddTask(task); err != nil {
		t.Fatal(err)
	}
	task.NextRunAt = time.Now().Add(-2 * time.Hour)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s.runTaskAt(task, time.Now(), false)
	if len(task.History) != 1 || task.History[0].Status != "skipped" || !task.NextRunAt.After(time.Now()) {
		t.Fatalf("misfire was not skipped: %+v", task)
	}
	if err := s.UpdateTask(task.ID, map[string]string{"prompt": "segundo", "schedule": "@every 2h", "max_retries": "0"}); err != nil {
		t.Fatal(err)
	}
	if task.Prompt != "segundo" || task.MaxRetries != 0 || task.Schedule != "@every 2h" || len(task.History) != 1 {
		t.Fatalf("update lost history or values: %+v", task)
	}
	s.Stop()
	s.Stop()
}

func TestCompletionNoticeSurvivesRestartAndDeduplicatesCallback(t *testing.T) {
	s1 := newTestScheduler(t)
	task := &Task{Name: "entrega", Schedule: "@every 1h", Prompt: "fazer"}
	if err := s1.AddTask(task); err != nil {
		t.Fatal(err)
	}
	s1.SetPromptRunner(func(context.Context, *Task) (string, error) { return "feito", nil })
	failure := errors.New("outbox indisponivel")
	s1.SetDurableCallback(func(*Task, CompletionNotice) error { return failure })
	s1.runTask(task)
	if len(task.PendingNotices) != 1 || task.PendingNotices[0].Attempts != 1 {
		t.Fatalf("failed notice was not kept: %+v", task.PendingNotices)
	}
	s2 := New(s1.dataFile, s1.logger)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	reloaded := s2.tasks[task.ID]
	if len(reloaded.PendingNotices) != 1 {
		t.Fatalf("notice disappeared after restart: %+v", reloaded)
	}
	runID := reloaded.PendingNotices[0].RunID
	reloaded.PendingNotices[0].NextAttemptAt = time.Now().Add(-time.Second)
	if err := s2.Save(); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s2.SetDurableCallback(func(_ *Task, notice CompletionNotice) error {
		calls++
		if notice.RunID != runID || notice.Result != "feito" || notice.Status != "success" {
			t.Fatalf("wrong notice: %+v", notice)
		}
		return nil
	})
	s2.deliverNotices(task.ID)
	s2.deliverNotices(task.ID)
	if calls != 1 || len(reloaded.PendingNotices) != 0 {
		t.Fatalf("calls=%d pending=%+v", calls, reloaded.PendingNotices)
	}
}

func TestRunHistoryJournalOutlivesSnapshotTail(t *testing.T) {
	s1 := newTestScheduler(t)
	task := &Task{Name: "historico", Schedule: "@every 1h", Prompt: "fazer"}
	if err := s1.AddTask(task); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 75; i++ {
		record := RunRecord{ID: "run_" + string(rune('a'+i)), TaskID: task.ID, StartedAt: time.Now().Add(time.Duration(i) * time.Second), Status: "success"}
		if err := s1.appendHistory(task, record); err != nil {
			t.Fatal(err)
		}
	}
	if err := s1.Save(); err != nil {
		t.Fatal(err)
	}
	if len(task.History) != 50 {
		t.Fatalf("snapshot tail = %d, want 50", len(task.History))
	}
	s2 := New(s1.dataFile, s1.logger)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	history, err := s2.History(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 75 {
		t.Fatalf("journal history = %d, want 75", len(history))
	}
}
