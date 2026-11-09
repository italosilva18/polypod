package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const taskStateVersion = 2

type persistentTask struct {
	TaskState
	Input string `json:"input,omitempty"`
}

type persistentTaskFile struct {
	Version int              `json:"version"`
	SavedAt time.Time        `json:"saved_at,omitempty"`
	Tasks   []persistentTask `json:"tasks"`
}

type recoveredTask struct {
	task    persistentTask
	savedAt time.Time
}

// EnablePersistence stores this process's tasks in a private, locked snapshot.
// It recovers tasks only from exited processes, so concurrent CLI instances
// cannot overwrite or claim each other's active work.
func (r *TaskRegistry) EnablePersistence(path string) (retErr error) {
	defer func() {
		if retErr != nil {
			r.mu.Lock()
			r.persistErr = retErr
			r.mu.Unlock()
		}
	}()
	if path == "" {
		return errors.New("task state path is required")
	}
	r.mu.RLock()
	alreadyEnabled := r.persistPath != ""
	r.mu.RUnlock()
	if alreadyEnabled {
		return errors.New("task persistence is already enabled")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolving task state path: %w", err)
	}
	dir := strings.TrimSuffix(absolute, filepath.Ext(absolute)) + ".d"
	if err := privateDirectory(dir); err != nil {
		return fmt.Errorf("creating task state directory: %w", err)
	}
	outputDir := filepath.Join(dir, "logs")
	if err := privateDirectory(outputDir); err != nil {
		return fmt.Errorf("creating task output directory: %w", err)
	}
	lockFile, err := os.CreateTemp(dir, "instance-*.lock")
	if err != nil {
		return fmt.Errorf("creating task instance lock: %w", err)
	}
	adopted := false
	defer func() {
		if !adopted {
			if err := lockFile.Close(); err != nil {
				slog.Default().Warn("closing task instance lock failed", "error", err)
			}
			if err := os.Remove(lockFile.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
				slog.Default().Warn("removing task instance lock failed", "error", err)
			}
		}
	}()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("locking task instance: %w", err)
	}
	recovered := make(map[string]recoveredTask)
	var recoveryErr error
	load := func(snapshotPath string) {
		stored, stamp, err := readTaskSnapshot(snapshotPath)
		if err != nil {
			recoveryErr = errors.Join(recoveryErr, err)
			slog.Default().Warn("task state recovery failed", "path", snapshotPath, "error", err)
			return
		}
		seen := make(map[string]bool, len(stored.Tasks))
		for _, saved := range stored.Tasks {
			if saved.ID == "" || seen[saved.ID] {
				recoveryErr = errors.Join(recoveryErr, fmt.Errorf("invalid or duplicate task ID in %s: %q", snapshotPath, saved.ID))
				continue
			}
			seen[saved.ID] = true
			if current, ok := recovered[saved.ID]; !ok || stamp.After(current.savedAt) {
				recovered[saved.ID] = recoveredTask{task: saved, savedAt: stamp}
			}
		}
	}
	// Read the legacy single-file snapshot from earlier versions, if present.
	if _, err := os.Stat(absolute); err == nil {
		load(absolute)
	} else if !errors.Is(err, os.ErrNotExist) {
		recoveryErr = errors.Join(recoveryErr, fmt.Errorf("stat legacy task state: %w", err))
	}
	entries, err := filepath.Glob(filepath.Join(dir, "instance-*.json"))
	if err != nil {
		return fmt.Errorf("listing task state instances: %w", err)
	}
	for _, snapshotPath := range entries {
		priorLockPath := strings.TrimSuffix(snapshotPath, ".json") + ".lock"
		priorLock, err := os.OpenFile(priorLockPath, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			recoveryErr = errors.Join(recoveryErr, fmt.Errorf("opening task instance lock %s: %w", priorLockPath, err))
			continue
		}
		err = syscall.Flock(int(priorLock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			if closeErr := priorLock.Close(); closeErr != nil {
				recoveryErr = errors.Join(recoveryErr, fmt.Errorf("closing live task lock: %w", closeErr))
			}
			continue
		}
		if err != nil {
			recoveryErr = errors.Join(recoveryErr, fmt.Errorf("locking task instance %s: %w", priorLockPath, err))
			if closeErr := priorLock.Close(); closeErr != nil {
				recoveryErr = errors.Join(recoveryErr, fmt.Errorf("closing task lock: %w", closeErr))
			}
			continue
		}
		load(snapshotPath)
		if err := syscall.Flock(int(priorLock.Fd()), syscall.LOCK_UN); err != nil {
			recoveryErr = errors.Join(recoveryErr, fmt.Errorf("unlocking task instance: %w", err))
		}
		if err := priorLock.Close(); err != nil {
			recoveryErr = errors.Join(recoveryErr, fmt.Errorf("closing task instance lock: %w", err))
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.persistPath != "" {
		return errors.New("task persistence is already enabled")
	}
	interrupted := 0
	for _, saved := range recovered {
		state := saved.task.TaskState
		state.input = saved.task.Input
		if state.Attempt <= 0 {
			state.Attempt = 1
		}
		if state.Status == StatusRunning || state.Status == StatusPending {
			now := time.Now()
			state.Status = StatusInterrupted
			state.EndTime = &now
			state.Error = "Execucao interrompida pelo reinicio do Polypod; use /task-restart para retomar com novo ID."
			interrupted++
		}
		if _, active := r.tasks[state.ID]; !active {
			copy := state
			r.tasks[state.ID] = &copy
		}
	}
	r.persistPath = strings.TrimSuffix(lockFile.Name(), ".lock") + ".json"
	r.outputDir = outputDir
	r.recoveryErr = recoveryErr
	r.lockFile = lockFile // The OS releases this lock when the process exits.
	adopted = true
	r.persistLocked()
	if interrupted > 0 {
		slog.Default().Info("recovered interrupted tasks", "count", interrupted)
	}
	return nil
}

func privateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func readTaskSnapshot(path string) (persistentTaskFile, time.Time, error) {
	var stored persistentTaskFile
	info, err := os.Stat(path)
	if err != nil {
		return stored, time.Time{}, fmt.Errorf("stat task state %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return stored, time.Time{}, fmt.Errorf("protecting task state %s: %w", path, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return stored, time.Time{}, fmt.Errorf("reading task state %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		return stored, time.Time{}, fmt.Errorf("parsing task state %s: %w", path, err)
	}
	// Version 1 had no subagent checkpoint. Its tasks remain readable; restart
	// applies the conservative legacy fallback in restart.go.
	if stored.Version != 1 && stored.Version != taskStateVersion {
		return stored, time.Time{}, fmt.Errorf("unsupported task state version in %s: %d", path, stored.Version)
	}
	stamp := stored.SavedAt
	if stamp.IsZero() {
		stamp = info.ModTime()
	}
	return stored, stamp, nil
}

// PersistenceError returns recovery and state-write errors, if any.
func (r *TaskRegistry) PersistenceError() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return errors.Join(r.recoveryErr, r.persistErr)
}

// CreateOutputFile creates a private task log in the persistent data directory
// when configured, or in the OS temporary directory for an in-memory registry.
func (r *TaskRegistry) CreateOutputFile(taskType TaskType, taskID string) (*os.File, error) {
	r.mu.RLock()
	dir := r.outputDir
	r.mu.RUnlock()
	pattern := fmt.Sprintf("polypod-%s-%s-*.log", taskType, taskID)
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("creating task output: %w", err)
	}
	return file, nil
}

// persistLocked is called with r.mu held after every task transition.
func (r *TaskRegistry) persistLocked() {
	if r.persistPath == "" {
		return
	}
	if err := r.saveLocked(); err != nil {
		if r.persistErr == nil || r.persistErr.Error() != err.Error() {
			slog.Default().Error("task state persistence failed", "path", r.persistPath, "error", err)
		}
		r.persistErr = err
		return
	}
	r.persistErr = nil
}

func (r *TaskRegistry) saveLocked() error {
	stored := persistentTaskFile{Version: taskStateVersion, SavedAt: time.Now().UTC(), Tasks: make([]persistentTask, 0, len(r.tasks))}
	for _, task := range r.tasks {
		stored.Tasks = append(stored.Tasks, persistentTask{TaskState: *snapshotTaskState(task), Input: task.input})
	}
	sort.Slice(stored.Tasks, func(i, j int) bool { return stored.Tasks[i].ID < stored.Tasks[j].ID })
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding task state: %w", err)
	}
	dir := filepath.Dir(r.persistPath)
	temp, err := os.CreateTemp(dir, ".task-state-*.tmp")
	if err != nil {
		return fmt.Errorf("creating task state temporary file: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		if removeErr := os.Remove(tempPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			slog.Default().Warn("removing task state temporary file failed", "path", tempPath, "error", removeErr)
		}
	}()
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("writing task state: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("syncing task state: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("closing task state: %w", err)
	}
	if err := os.Rename(tempPath, r.persistPath); err != nil {
		return fmt.Errorf("replacing task state: %w", err)
	}
	directory, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("opening task state directory for sync: %w", err)
	}
	if err := directory.Sync(); err != nil {
		directory.Close()
		return fmt.Errorf("syncing task state directory: %w", err)
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("closing task state directory: %w", err)
	}
	return nil
}
