package scheduler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

var ErrConcurrentUpdate = errors.New("rotina alterada em outro processo; recarregue e tente novamente")

// readSnapshot keeps the established JSON array as the durable format. The
// stable .state.lock sidecar serializes the read/modify/rename sequence.
func (s *Scheduler) readSnapshot() (map[string]*Task, map[string][]byte, error) {
	raw, err := os.ReadFile(s.dataFile)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]*Task), make(map[string][]byte), nil
	}
	if err != nil {
		return nil, nil, err
	}
	var listed []*Task
	if err := json.Unmarshal(raw, &listed); err != nil {
		return nil, nil, fmt.Errorf("lendo rotinas: %w", err)
	}
	tasks := make(map[string]*Task, len(listed))
	encoded := make(map[string][]byte, len(listed))
	for _, task := range listed {
		if task == nil || task.ID == "" {
			continue
		}
		copy := cloneTask(task)
		data, err := json.Marshal(copy)
		if err != nil {
			return nil, nil, err
		}
		tasks[copy.ID], encoded[copy.ID] = copy, data
	}
	return tasks, encoded, nil
}

func cloneTask(task *Task) *Task {
	copy := *task
	copy.History = append([]RunRecord(nil), task.History...)
	copy.PendingNotices = append([]CompletionNotice(nil), task.PendingNotices...)
	return &copy
}

func encodeTask(task *Task) ([]byte, error) {
	if task == nil {
		return nil, nil
	}
	return json.Marshal(task)
}

func taskChanged(current *Task, currentOK bool, baseline []byte, baselineOK bool) (bool, []byte, error) {
	if !currentOK {
		return baselineOK, nil, nil
	}
	data, err := encodeTask(current)
	if err != nil {
		return false, nil, err
	}
	return !baselineOK || !bytes.Equal(data, baseline), data, nil
}

// refresh reconciles another process's committed changes with this process's
// clean in-memory tasks. Unsaved local edits keep their old baseline, so Save
// detects an edit conflict instead of silently overwriting it.
func (s *Scheduler) refresh() error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	lease, err := s.stateLease()
	if err != nil {
		return err
	}
	defer lease.Close()
	return s.refreshUnderStateLock()
}

func (s *Scheduler) refreshUnderStateLock() error {
	disk, diskBytes, err := s.readSnapshot()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mergeCleanLocked(disk, diskBytes)
}

// mergeCleanLocked assumes s.mu is held. It preserves pointers used by an
// active run and never replaces unsaved local edits.
func (s *Scheduler) mergeCleanLocked(disk map[string]*Task, diskBytes map[string][]byte) error {
	merged := make(map[string]*Task, len(disk)+len(s.tasks))
	baseline := make(map[string][]byte, len(disk)+len(s.baseline))
	for id, task := range s.tasks {
		prior, hadPrior := s.baseline[id]
		changed, localBytes, err := taskChanged(task, true, prior, hadPrior)
		if err != nil {
			return err
		}
		if changed {
			merged[id] = task
			if hadPrior {
				baseline[id] = prior
			}
			continue
		}
		if current, ok := disk[id]; ok {
			if bytes.Equal(localBytes, diskBytes[id]) {
				merged[id] = task
			} else if s.running[id] || s.notifying[id] {
				return fmt.Errorf("%w: %s em execução", ErrConcurrentUpdate, id)
			} else {
				merged[id] = current
			}
			baseline[id] = diskBytes[id]
		}
	}
	for id, task := range disk {
		if _, locallyPresent := s.tasks[id]; locallyPresent {
			continue
		}
		if prior, hadPrior := s.baseline[id]; hadPrior {
			// A missing local task is an unsaved delete.
			baseline[id] = prior
			continue
		}
		merged[id] = task
		baseline[id] = diskBytes[id]
	}
	for id, prior := range s.baseline {
		if _, locallyPresent := s.tasks[id]; !locallyPresent {
			baseline[id] = prior
		}
	}
	s.tasks, s.baseline = merged, baseline
	return nil
}

// saveUnderStateLock assumes s.saveMu and the cross-process state lease are
// held. It merges only task IDs changed since this process's last snapshot.
func (s *Scheduler) saveUnderStateLock() error {
	disk, diskBytes, err := s.readSnapshot()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changedIDs := make(map[string]struct{})
	for id, task := range s.tasks {
		prior, hadPrior := s.baseline[id]
		changed, localBytes, err := taskChanged(task, true, prior, hadPrior)
		if err != nil {
			return err
		}
		if !changed {
			continue
		}
		if err := checkSnapshotConflict(id, prior, hadPrior, diskBytes[id], disk[id] != nil, localBytes, true); err != nil {
			return err
		}
		changedIDs[id] = struct{}{}
		disk[id] = cloneTask(task)
	}
	for id, prior := range s.baseline {
		if _, present := s.tasks[id]; present {
			continue
		}
		if err := checkSnapshotConflict(id, prior, true, diskBytes[id], disk[id] != nil, nil, false); err != nil {
			return err
		}
		changedIDs[id] = struct{}{}
		delete(disk, id)
	}
	if len(changedIDs) > 0 {
		if err := s.writeSnapshot(disk); err != nil {
			return err
		}
		// Re-encode the final map, including this process's updated task IDs.
		for id := range changedIDs {
			if task, exists := disk[id]; exists {
				diskBytes[id], err = encodeTask(task)
				if err != nil {
					return err
				}
			} else {
				delete(diskBytes, id)
			}
		}
		merged := make(map[string]*Task, len(disk))
		for id, persisted := range disk {
			if local := s.tasks[id]; local != nil {
				if localBytes, encodeErr := encodeTask(local); encodeErr == nil && bytes.Equal(localBytes, diskBytes[id]) {
					merged[id] = local
					continue
				}
			}
			merged[id] = persisted
		}
		s.tasks, s.baseline = merged, diskBytes
		return nil
	}
	return s.mergeCleanLocked(disk, diskBytes)
}

func checkSnapshotConflict(id string, prior []byte, hadPrior bool, disk []byte, hasDisk bool, local []byte, hasLocal bool) error {
	if hadPrior {
		if !hasDisk {
			if !hasLocal {
				return nil // another process already removed the same task
			}
			return fmt.Errorf("%w: %s foi removida", ErrConcurrentUpdate, id)
		}
		if !bytes.Equal(prior, disk) && (!hasLocal || !bytes.Equal(local, disk)) {
			return fmt.Errorf("%w: %s", ErrConcurrentUpdate, id)
		}
		return nil
	}
	if hasDisk && (!hasLocal || !bytes.Equal(local, disk)) {
		return fmt.Errorf("%w: ID %s já existe", ErrConcurrentUpdate, id)
	}
	return nil
}

func (s *Scheduler) writeSnapshot(tasks map[string]*Task) error {
	ids := make([]string, 0, len(tasks))
	for id := range tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	listed := make([]*Task, 0, len(ids))
	for _, id := range ids {
		listed = append(listed, tasks[id])
	}
	data, err := json.MarshalIndent(listed, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.dataFile), 0o700); err != nil {
		return fmt.Errorf("criando diretorio do agendador: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(s.dataFile), "."+filepath.Base(s.dataFile)+".tmp-*")
	if err != nil {
		return fmt.Errorf("abrindo estado temporario do agendador: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("gravando estado temporario do agendador: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sincronizando estado do agendador: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("fechando estado temporario do agendador: %w", err)
	}
	if err := os.Rename(tmp, s.dataFile); err != nil {
		return fmt.Errorf("publicando estado do agendador: %w", err)
	}
	if dir, err := os.Open(filepath.Dir(s.dataFile)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
