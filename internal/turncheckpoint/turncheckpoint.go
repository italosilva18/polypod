// Package turncheckpoint records file state before agent edits so a conversation
// turn can be rewound after the process restarts.
package turncheckpoint

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type turnKey struct{}

type identity struct {
	sessionID string
	turnID    string
}

// Snapshot describes the file state around one agent turn. File bytes are
// stored in a private blob alongside the turn manifest.
type Snapshot struct {
	Path         string `json:"path"`
	BeforeExists bool   `json:"before_exists"`
	BeforeHash   string `json:"before_hash,omitempty"`
	BeforeMode   uint32 `json:"before_mode,omitempty"`
	Blob         string `json:"blob,omitempty"`
	AfterKnown   bool   `json:"after_known"`
	AfterExists  bool   `json:"after_exists"`
	AfterHash    string `json:"after_hash,omitempty"`
}

type turnRecord struct {
	SessionID string              `json:"session_id"`
	TurnID    string              `json:"turn_id"`
	StartedAt time.Time           `json:"started_at"`
	EndedAt   time.Time           `json:"ended_at,omitempty"`
	Files     map[string]Snapshot `json:"files"`
}

// RestoreResult reports completed file changes and conflicts. Conflicts are
// informational: the selected rewind still applies to the files.
type RestoreResult struct {
	Restored  []string
	Conflicts []string
	Warnings  []string
}

// Manager owns the active turn records. Disk manifests make completed and
// interrupted turns available to a later process.
type Manager struct {
	mu             sync.Mutex
	root           string
	logger         *slog.Logger
	active         map[identity]*turnRecord
	resumeTouched  map[identity]map[string]bool
	resumeConflict map[identity]map[string]bool
}

func New(root string, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{root: root, logger: logger, active: make(map[identity]*turnRecord), resumeTouched: make(map[identity]map[string]bool), resumeConflict: make(map[identity]map[string]bool)}
}

// BeginTurn associates later file-write callbacks with this conversation turn.
func (m *Manager) BeginTurn(ctx context.Context, sessionID, turnID string) context.Context {
	if sessionID == "" || turnID == "" {
		return ctx
	}
	id := identity{sessionID: sessionID, turnID: turnID}
	m.mu.Lock()
	delete(m.resumeTouched, id)
	delete(m.resumeConflict, id)
	m.active[id] = &turnRecord{
		SessionID: sessionID,
		TurnID:    turnID,
		StartedAt: time.Now(),
		Files:     make(map[string]Snapshot),
	}
	m.mu.Unlock()
	return context.WithValue(ctx, turnKey{}, id)
}

// ResumeTurn preserves file snapshots captured before an interrupted turn.
// A fresh BeginTurn would replace them and make rewind of already executed
// edits impossible after a safe AI checkpoint is resumed.
func (m *Manager) ResumeTurn(ctx context.Context, sessionID, turnID string) (context.Context, error) {
	if sessionID == "" || turnID == "" {
		return ctx, fmt.Errorf("identidade de turno vazia")
	}
	id := identity{sessionID: sessionID, turnID: turnID}
	m.mu.Lock()
	defer m.mu.Unlock()
	record, err := m.loadRecord(id)
	if os.IsNotExist(err) {
		record = &turnRecord{SessionID: sessionID, TurnID: turnID, StartedAt: time.Now(), Files: make(map[string]Snapshot)}
	} else if err != nil {
		return ctx, fmt.Errorf("lendo checkpoint de arquivos do turno: %w", err)
	}
	if record.Files == nil {
		record.Files = make(map[string]Snapshot)
	}
	record.EndedAt = time.Time{}
	m.active[id] = record
	m.resumeTouched[id] = make(map[string]bool)
	m.resumeConflict[id] = make(map[string]bool)
	return context.WithValue(ctx, turnKey{}, id), nil
}

// CaptureBefore records the first pre-write state for a file in this turn.
// Call it before the tool writes. A missing turn context is intentionally a
// no-op for standalone file tools outside conversation turns.
func (m *Manager) CaptureBefore(ctx context.Context, path string, original []byte, existed bool) error {
	id, ok := ctx.Value(turnKey{}).(identity)
	if !ok {
		return nil
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolving checkpoint path: %w", err)
	}
	absolute = filepath.Clean(absolute)

	m.mu.Lock()
	defer m.mu.Unlock()
	record := m.active[id]
	if record == nil {
		return nil
	}
	if touched := m.resumeTouched[id]; touched != nil {
		touched[absolute] = true
		if snap, found := record.Files[absolute]; found &&
			(!snap.AfterKnown || snap.AfterExists != existed || (existed && snap.AfterHash != digest(original))) {
			// An external edit occurred after the old snapshot, or the old
			// post-state was never confirmed. Keep the old expectation so
			// rewind still reports the uncertainty.
			m.resumeConflict[id][absolute] = true
		}
	}
	if _, found := record.Files[absolute]; found {
		return nil
	}
	file := Snapshot{Path: absolute, BeforeExists: existed}
	if existed {
		file.BeforeHash = digest(original)
		file.BeforeMode = 0600
		if info, statErr := os.Stat(absolute); statErr == nil {
			file.BeforeMode = uint32(info.Mode().Perm())
		}
		file.Blob = digest([]byte(absolute)) + ".before"
		if err := writePrivate(filepath.Join(m.turnDir(id), file.Blob), original); err != nil {
			return fmt.Errorf("saving pre-write file %s: %w", absolute, err)
		}
	}
	record.Files[absolute] = file
	if err := m.saveRecord(id, record); err != nil {
		delete(record.Files, absolute)
		return fmt.Errorf("saving turn checkpoint: %w", err)
	}
	return nil
}

// FinishTurn records the final state of files touched by an agent. A turn that
// did not edit files does not create a disk checkpoint.
func (m *Manager) FinishTurn(ctx context.Context) error {
	id, ok := ctx.Value(turnKey{}).(identity)
	if !ok {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record := m.active[id]
	if record == nil {
		return nil
	}
	defer delete(m.active, id)
	defer delete(m.resumeTouched, id)
	defer delete(m.resumeConflict, id)
	if touched := m.resumeTouched[id]; touched != nil && len(touched) == 0 {
		// A failed resume may not rewrite the expected post-state of files
		// captured before the process stopped.
		return nil
	}
	if len(record.Files) == 0 {
		return nil
	}
	for path, snap := range record.Files {
		if touched := m.resumeTouched[id]; touched != nil && !touched[path] {
			continue
		}
		if m.resumeConflict[id][path] {
			continue
		}
		exists, hash, err := state(path)
		if err != nil {
			m.logger.Warn("checkpoint post-write state unavailable", "path", path, "error", err)
			continue
		}
		snap.AfterKnown = true
		snap.AfterExists = exists
		snap.AfterHash = hash
		record.Files[path] = snap
	}
	record.EndedAt = time.Now()
	return m.saveRecord(id, record)
}

// RestoreTurns restores file state from immediately before the oldest selected
// turn. turnIDs must be ordered oldest to newest. A later external edit is
// reported as a conflict and overwritten, matching the selected rewind mode.
func (m *Manager) RestoreTurns(ctx context.Context, sessionID string, turnIDs []string) (RestoreResult, error) {
	var result RestoreResult
	if sessionID == "" || len(turnIDs) == 0 {
		return result, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	first := make(map[string]Snapshot)
	firstOwner := make(map[string]string)
	last := make(map[string]Snapshot)
	for _, turnID := range turnIDs {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		id := identity{sessionID: sessionID, turnID: turnID}
		record, err := m.loadRecord(id)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("checkpoint do turno %s indisponivel: %v", turnID, err))
			continue
		}
		if record.EndedAt.IsZero() {
			result.Warnings = append(result.Warnings, fmt.Sprintf("turno %s foi interrompido; estado final nao confirmado", turnID))
		}
		for path, snap := range record.Files {
			if _, present := first[path]; !present {
				first[path] = snap
				firstOwner[path] = turnID
			}
			last[path] = snap
		}
	}

	paths := make([]string, 0, len(first))
	for path := range first {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		expected := last[path]
		exists, hash, stateErr := state(path)
		if stateErr != nil {
			result.Conflicts = append(result.Conflicts, fmt.Sprintf("%s: estado atual nao pode ser confirmado: %v", path, stateErr))
		} else if expected.AfterKnown && (exists != expected.AfterExists || hash != expected.AfterHash) {
			result.Conflicts = append(result.Conflicts, fmt.Sprintf("%s: alterado desde o turno", path))
		} else if !expected.AfterKnown {
			result.Conflicts = append(result.Conflicts, fmt.Sprintf("%s: estado final do turno desconhecido", path))
		}

		snap := first[path]
		if !snap.BeforeExists {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				result.Warnings = append(result.Warnings, fmt.Sprintf("%s: remocao falhou: %v", path, err))
				continue
			}
			result.Restored = append(result.Restored, path)
			continue
		}
		id := identity{sessionID: sessionID, turnID: firstOwner[path]}
		// The oldest matching turn owns the pre-write blob.
		blob, err := os.ReadFile(filepath.Join(m.turnDir(id), snap.Blob))
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: snapshot indisponivel: %v", path, err))
			continue
		}
		if err := writeRestored(path, blob, os.FileMode(snap.BeforeMode)); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: restauracao falhou: %v", path, err))
			continue
		}
		result.Restored = append(result.Restored, path)
	}
	if err := m.appendAudit(sessionID, turnIDs, result); err != nil {
		m.logger.Warn("checkpoint rewind audit unavailable", "error", err)
	}
	return result, nil
}

func (m *Manager) turnDir(id identity) string {
	return filepath.Join(m.root, digest([]byte(id.sessionID)), digest([]byte(id.turnID)))
}

func (m *Manager) saveRecord(id identity, record *turnRecord) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return writePrivate(filepath.Join(m.turnDir(id), "manifest.json"), data)
}

func (m *Manager) loadRecord(id identity) (*turnRecord, error) {
	data, err := os.ReadFile(filepath.Join(m.turnDir(id), "manifest.json"))
	if err != nil {
		return nil, err
	}
	var record turnRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if record.SessionID != id.sessionID || record.TurnID != id.turnID {
		return nil, fmt.Errorf("checkpoint identity mismatch")
	}
	return &record, nil
}

func (m *Manager) appendAudit(sessionID string, turnIDs []string, result RestoreResult) error {
	entry := struct {
		At        time.Time     `json:"at"`
		SessionID string        `json:"session_id"`
		TurnIDs   []string      `json:"turn_ids"`
		Result    RestoreResult `json:"result"`
	}{At: time.Now(), SessionID: sessionID, TurnIDs: turnIDs, Result: result}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.root, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(m.root, "rewind.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
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

func state(path string) (bool, string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	return true, digest(data), nil
}

func digest(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func writePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".checkpoint-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func writeRestored(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".polypod-restore-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
