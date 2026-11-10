package scheduler

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	// The task snapshot is the recovery fallback if the append-only journal is
	// temporarily unwritable. The caller still receives the journal error so it
	// can log the degraded audit path before saving the task snapshot.
	journalErr := s.writeRunRecord(record)
	s.mu.Lock()
	task.History = append(task.History, record)
	if len(task.History) > 50 {
		task.History = append([]RunRecord(nil), task.History[len(task.History)-50:]...)
	}
	s.mu.Unlock()
	return journalErr
}

func (s *Scheduler) writeRunRecord(record RunRecord) error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	lease, err := s.stateLease()
	if err != nil {
		return err
	}
	defer lease.Close()
	return s.writeRunRecordUnlocked(record)
}

func (s *Scheduler) writeRunRecordUnlocked(record RunRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	path := s.dataFile + ".runs.jsonl"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := s.repairJournalTailBeforeAppend(path); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	payload := append(data, '\n')
	n, err := f.Write(payload)
	if err != nil {
		return errors.Join(err, f.Close())
	}
	if n != len(payload) {
		return errors.Join(io.ErrShortWrite, f.Close())
	}
	if err := f.Sync(); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Close(); err != nil {
		return err
	}
	// Sync the directory too: a newly created journal must survive a crash
	// before the task snapshot is marked complete.
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

// A surviving process can append after another process died mid-write. Check
// the last byte before every append so an incomplete tail never becomes an
// invalid middle line. The full repair runs only for an unterminated tail.
func (s *Scheduler) repairJournalTailBeforeAppend(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	info, statErr := f.Stat()
	if statErr != nil {
		return errors.Join(statErr, f.Close())
	}
	if info.Size() == 0 {
		return f.Close()
	}
	var last [1]byte
	_, readErr := f.ReadAt(last[:], info.Size()-1)
	closeErr := f.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}
	_, err = s.readRunJournalUnlocked()
	return err
}

// History returns the complete persisted history for a routine, including
// legacy records that existed before the append-only journal was introduced.
func (s *Scheduler) History(id string) ([]RunRecord, error) {
	if err := s.refresh(); err != nil {
		return nil, err
	}
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
	lease, err := s.stateLease()
	if err != nil {
		return nil, err
	}
	defer lease.Close()
	return s.readRunJournalUnlocked()
}

func (s *Scheduler) readRunJournalUnlocked() (records []RunRecord, err error) {
	path := s.dataFile + ".runs.jsonl"
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	records = make([]RunRecord, 0)
	reader := bufio.NewReaderSize(f, 64*1024)
	var offset int64
	for {
		line, readErr := readJournalLine(reader)
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			return records, nil
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, fmt.Errorf("lendo histórico de rotinas na posição %d: %w", offset, readErr)
		}
		var record RunRecord
		if decodeErr := json.Unmarshal(line, &record); decodeErr != nil {
			// Only an unterminated final line can be a partial write. A
			// newline-terminated invalid record is corruption, even at EOF.
			if !errors.Is(readErr, io.EOF) {
				return nil, fmt.Errorf("histórico de rotinas corrompido na posição %d: %w", offset, decodeErr)
			}
			if err := f.Truncate(offset); err != nil {
				return nil, fmt.Errorf("removendo cauda incompleta do histórico: %w", err)
			}
			if err := f.Sync(); err != nil {
				return nil, fmt.Errorf("sincronizando histórico reparado: %w", err)
			}
			s.logger.Warn("cauda incompleta do histórico de rotinas descartada", "path", path, "offset", offset, "error", decodeErr)
			return records, nil
		}
		records = append(records, record)
		offset += int64(len(line))
		if errors.Is(readErr, io.EOF) {
			// A complete JSON object without a newline can be kept. Add the
			// separator before the next append, otherwise two records merge.
			if _, err := f.WriteAt([]byte{'\n'}, offset); err != nil {
				return nil, fmt.Errorf("finalizando última linha do histórico: %w", err)
			}
			if err := f.Sync(); err != nil {
				return nil, fmt.Errorf("sincronizando última linha do histórico: %w", err)
			}
			return records, nil
		}
	}
}

func readJournalLine(reader *bufio.Reader) ([]byte, error) {
	const maxLineBytes = 16 << 20
	line := make([]byte, 0, 1024)
	for {
		part, err := reader.ReadSlice('\n')
		if len(line)+len(part) > maxLineBytes {
			return nil, fmt.Errorf("linha do histórico excede %d bytes", maxLineBytes)
		}
		line = append(line, part...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, err
	}
}
