package scheduler

import (
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureDefaultRoutinesSeedsOnceAndPreservesUserChanges(t *testing.T) {
	file := filepath.Join(t.TempDir(), "scheduler.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(file, logger)
	created, err := EnsureDefaultRoutines(s)
	if err != nil || created != 3 {
		t.Fatalf("first seed: created=%d err=%v", created, err)
	}
	for id, schedule := range map[string]string{
		"system_daily_plan": "0 9 * * *", "system_daily_review": "0 18 * * *",
		"system_weekly_review": "0 10 * * 1",
	} {
		task, ok := s.GetTask(id)
		if !ok || task.Schedule != schedule || task.TimeZone != "America/Sao_Paulo" ||
			task.Channel != "system" || task.UserID != "owner" || task.MisfirePolicy != "run_once" ||
			!task.Enabled || task.NextRunAt.IsZero() || !strings.Contains(task.Prompt, "NO_REPLY") {
			t.Fatalf("invalid default %s: %+v (exists=%t)", id, task, ok)
		}
		zone, err := time.LoadLocation(defaultRoutineZone)
		if err != nil {
			t.Fatal(err)
		}
		local := task.NextRunAt.In(zone)
		if local.Minute() != 0 || (id == "system_daily_review" && local.Hour() != 18) ||
			(id == "system_daily_plan" && local.Hour() != 9) ||
			(id == "system_weekly_review" && local.Hour() != 10) ||
			(id == "system_weekly_review" && local.Weekday() != time.Monday) {
			t.Fatalf("wrong local execution time for %s: %s", id, local)
		}
	}
	if err := s.UpdateTask("system_daily_plan", map[string]string{"prompt": "Plano personalizado"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEnabled("system_daily_plan", false); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveTask("system_daily_review"); err != nil {
		t.Fatal(err)
	}
	if created, err := EnsureDefaultRoutines(s); err != nil || created != 0 {
		t.Fatalf("repeated seed: created=%d err=%v", created, err)
	}

	restarted := New(file, logger)
	if err := restarted.Load(); err != nil {
		t.Fatal(err)
	}
	if created, err := EnsureDefaultRoutines(restarted); err != nil || created != 0 {
		t.Fatalf("restart seed: created=%d err=%v", created, err)
	}
	if plan, ok := restarted.GetTask("system_daily_plan"); !ok || plan.Prompt != "Plano personalizado" || plan.Enabled {
		t.Fatalf("user edits lost: %+v (exists=%t)", plan, ok)
	}
	if _, ok := restarted.GetTask("system_daily_review"); ok {
		t.Fatal("removed routine reappeared after restart")
	}
}
