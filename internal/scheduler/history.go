package scheduler

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Every attempt is also kept in an append-only journal. The task snapshot
// retains a small tail for quick status display; the journal retains the full
// audit trail without making every scheduler save grow forever.
func (s *Scheduler) appendHistory(task *Task, record RunRecord) error {
	if record.TaskID == "" {
		record.TaskID = task.ID
	}
	if err := s.writeRunRecord(record); err != nil {
		return err
	}
	s.mu.Lock()
	task.History = append(task.History, record)
	if len(task.History) > 50 {
		task.History = append([]RunRecord(nil), task.History[len(task.History)-50:]...)
	}
	s.mu.Unlock()
	return nil
}

func (s *Scheduler) writeRunRecord(record RunRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	path := s.dataFile + ".runs.jsonl"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// History returns the complete persisted history for a routine, including
// legacy records that existed before the append-only journal was introduced.
func (s *Scheduler) History(id string) ([]RunRecord, error) {
	s.mu.RLock()
	task, ok := s.tasks[id]
	if !ok {
		s.mu.RUnlock()
		return nil, fmt.Errorf("tarefa %s nao encontrada", id)
	}
	legacy := append([]RunRecord(nil), task.History...)
	s.mu.RUnlock()

	journal, err := s.readRunJournal()
	if err != nil {
		return nil, err
	}
	history := make([]RunRecord, 0, len(legacy))
	seen := make(map[string]bool)
	for _, record := range journal {
		if record.TaskID == id && !seen[record.ID] {
			history = append(history, record)
			seen[record.ID] = true
		}
	}
	for _, record := range legacy {
		if !seen[record.ID] {
			history = append(history, record)
			seen[record.ID] = true
		}
	}
	sort.SliceStable(history, func(i, j int) bool { return history[i].StartedAt.Before(history[j].StartedAt) })
	return history, nil
}

func (s *Scheduler) readRunJournal() ([]RunRecord, error) {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	f, err := os.Open(s.dataFile + ".runs.jsonl")
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	records := make([]RunRecord, 0)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var record RunRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, fmt.Errorf("historico de rotinas corrompido: %w", err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}
