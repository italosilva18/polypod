package scheduler

import (
	"fmt"
	"strings"
	"time"
)

// recoverOrphanedRun is called only while holding the task's OS lease. A
// running snapshot without a lease belongs to a process that has exited (or
// stopped the scheduler). The attempt journal may already contain its final
// result even if the process died before updating the JSON snapshot.
func (s *Scheduler) recoverOrphanedRun(id string, now time.Time) error {
	s.mu.RLock()
	task, ok := s.tasks[id]
	if !ok || task.CurrentRunID == "" || task.LastStatus != "running" {
		s.mu.RUnlock()
		return nil
	}
	runID, attempt := task.CurrentRunID, task.CurrentAttempt
	s.mu.RUnlock()

	journal, err := s.readRunJournal()
	if err != nil {
		return err
	}
	var latest *RunRecord
	for i := range journal {
		record := &journal[i]
		if record.TaskID != id || record.RunID != runID || record.Attempt != attempt {
			continue
		}
		if latest == nil || record.FinishedAt.After(latest.FinishedAt) {
			latest = record
		}
	}

	var interrupted *RunRecord
	s.mu.Lock()
	task, ok = s.tasks[id]
	if !ok || task.CurrentRunID != runID || task.CurrentAttempt != attempt || task.LastStatus != "running" {
		s.mu.Unlock()
		return nil
	}
	if latest != nil && (latest.Status == "success" || latest.Status == "failed") {
		if latest.Status == "success" || attempt > task.MaxRetries {
			finishRecoveredRun(task, latest, now)
		} else {
			task.LastStatus = "retrying"
			task.LastError = latest.Error
			task.NextRetryAt = now
		}
	} else if attempt == 0 {
		task.LastStatus = "retrying"
		task.NextRetryAt = now
	} else {
		if latest == nil || latest.Status != "interrupted" {
			record := RunRecord{
				ID:     fmt.Sprintf("%s_interrupted_%d", runID, attempt),
				TaskID: id, RunID: runID, ScheduledFor: task.CurrentDueAt,
				Attempt: attempt, StartedAt: task.CurrentStartedAt,
				FinishedAt: now, Status: "interrupted",
				Error: "processo interrompido antes de confirmar resultado",
			}
			task.History = append(task.History, record)
			if len(task.History) > 50 {
				task.History = append([]RunRecord(nil), task.History[len(task.History)-50:]...)
			}
			interrupted = &record
		}
		if attempt <= task.MaxRetries {
			task.LastStatus = "retrying"
			task.NextRetryAt = now
		} else {
			task.LastStatus = "failed"
			task.LastError = "execucao interrompida; resultado externo nao confirmado"
			if strings.HasPrefix(task.Schedule, "@at ") {
				task.Enabled = false
				task.NextRunAt = time.Time{}
			}
			task.PendingNotices = append(task.PendingNotices, CompletionNotice{
				RunID: runID, Status: task.LastStatus, Error: task.LastError,
				FinishedAt: now, NextAttemptAt: now,
			})
			task.CurrentRunID = ""
			task.CurrentAttempt = 0
			task.CurrentStartedAt = time.Time{}
			task.CurrentDueAt = time.Time{}
			task.NextRetryAt = time.Time{}
			task.RunCount++
			task.FailureCount++
		}
	}
	s.mu.Unlock()
	if interrupted != nil {
		if err := s.writeRunRecord(*interrupted); err != nil {
			s.logger.Warn("journal de tentativa interrompida indisponivel", "id", id, "error", err)
		}
	}
	return s.Save()
}

func finishRecoveredRun(task *Task, record *RunRecord, now time.Time) {
	task.LastRun = record.FinishedAt
	task.LastResult = record.Result
	task.LastError = record.Error
	task.LastStatus = record.Status
	task.LastDuration = record.FinishedAt.Sub(task.CurrentStartedAt).Round(time.Millisecond).String()
	task.RunCount++
	if record.Status == "failed" {
		task.FailureCount++
	} else {
		task.FailureCount = 0
	}
	task.PendingNotices = append(task.PendingNotices, CompletionNotice{
		RunID: task.CurrentRunID, Status: record.Status, Result: record.Result,
		Error: record.Error, FinishedAt: record.FinishedAt, NextAttemptAt: now,
	})
	task.CurrentRunID = ""
	task.CurrentAttempt = 0
	task.CurrentStartedAt = time.Time{}
	task.CurrentDueAt = time.Time{}
	task.NextRetryAt = time.Time{}
	if strings.HasPrefix(task.Schedule, "@at ") {
		task.Enabled = false
		task.NextRunAt = time.Time{}
	}
}
