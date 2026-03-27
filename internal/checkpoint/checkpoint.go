package checkpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Checkpoint captures the state of files at a point in time.
type Checkpoint struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Timestamp time.Time         `json:"timestamp"`
	Files     map[string]string `json:"files"` // path → content hash
	Message   string            `json:"message"`
}

// FileSnapshot stores a file's content for restoration.
type FileSnapshot struct {
	Path    string `json:"path"`
	Content []byte `json:"-"`
	Hash    string `json:"hash"`
	Exists  bool   `json:"exists"` // false if file didn't exist (was created)
}

// Manager manages checkpoints for rewind/undo.
type Manager struct {
	mu          sync.Mutex
	checkpoints []Checkpoint
	snapshots   map[string][]FileSnapshot // checkpoint ID → file snapshots
	dataDir     string
	logger      *slog.Logger
	maxHistory  int
}

// NewManager creates a checkpoint manager.
func NewManager(dataDir string, logger *slog.Logger) *Manager {
	dir := filepath.Join(dataDir, "checkpoints")
	os.MkdirAll(dir, 0755)
	return &Manager{
		snapshots:  make(map[string][]FileSnapshot),
		dataDir:    dir,
		logger:     logger,
		maxHistory: 50,
	}
}

// Create takes a snapshot of the given files and creates a checkpoint.
func (m *Manager) Create(name string, files []string, message string) (*Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := fmt.Sprintf("ckpt_%d", time.Now().UnixMilli())
	cp := Checkpoint{
		ID:        id,
		Name:      name,
		Timestamp: time.Now(),
		Files:     make(map[string]string),
		Message:   message,
	}

	var snapshots []FileSnapshot
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			// File doesn't exist — record as non-existent
			snapshots = append(snapshots, FileSnapshot{
				Path:   path,
				Exists: false,
			})
			continue
		}
		hash := hashContent(data)
		cp.Files[path] = hash

		// Save snapshot to disk
		snapPath := filepath.Join(m.dataDir, id+"_"+sanitizePath(path))
		os.WriteFile(snapPath, data, 0644)

		snapshots = append(snapshots, FileSnapshot{
			Path:    path,
			Content: data,
			Hash:    hash,
			Exists:  true,
		})
	}

	m.checkpoints = append(m.checkpoints, cp)
	m.snapshots[id] = snapshots

	// Trim old checkpoints
	if len(m.checkpoints) > m.maxHistory {
		old := m.checkpoints[0]
		m.checkpoints = m.checkpoints[1:]
		delete(m.snapshots, old.ID)
	}

	m.saveIndex()
	m.logger.Debug("checkpoint created", "id", id, "name", name, "files", len(files))
	return &cp, nil
}

// Rewind restores files to a checkpoint state.
func (m *Manager) Rewind(id string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	snapshots, ok := m.snapshots[id]
	if !ok {
		// Try loading from disk
		var err error
		snapshots, err = m.loadSnapshots(id)
		if err != nil {
			return nil, fmt.Errorf("checkpoint '%s' nao encontrado", id)
		}
	}

	var restored []string
	for _, snap := range snapshots {
		if !snap.Exists {
			// File was created after checkpoint — delete it
			os.Remove(snap.Path)
			restored = append(restored, fmt.Sprintf("removido: %s", snap.Path))
			continue
		}
		// Restore content
		data := snap.Content
		if len(data) == 0 {
			// Load from disk
			snapPath := filepath.Join(m.dataDir, id+"_"+sanitizePath(snap.Path))
			var err error
			data, err = os.ReadFile(snapPath)
			if err != nil {
				return nil, fmt.Errorf("restaurando %s: %w", snap.Path, err)
			}
		}
		os.MkdirAll(filepath.Dir(snap.Path), 0755)
		if err := os.WriteFile(snap.Path, data, 0644); err != nil {
			return nil, fmt.Errorf("escrevendo %s: %w", snap.Path, err)
		}
		restored = append(restored, fmt.Sprintf("restaurado: %s", snap.Path))
	}

	return restored, nil
}

// RewindLast restores the most recent checkpoint.
func (m *Manager) RewindLast() ([]string, error) {
	m.mu.Lock()
	if len(m.checkpoints) == 0 {
		m.mu.Unlock()
		return nil, fmt.Errorf("nenhum checkpoint disponivel")
	}
	last := m.checkpoints[len(m.checkpoints)-1]
	m.mu.Unlock()
	return m.Rewind(last.ID)
}

// List returns all checkpoints (newest first).
func (m *Manager) List() []Checkpoint {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]Checkpoint, len(m.checkpoints))
	copy(result, m.checkpoints)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Timestamp.After(result[j].Timestamp)
	})
	return result
}

// Fork creates a copy of a checkpoint's files in a new directory for alternative approach.
func (m *Manager) Fork(id, forkDir string) error {
	m.mu.Lock()
	snapshots, ok := m.snapshots[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("checkpoint '%s' nao encontrado", id)
	}

	os.MkdirAll(forkDir, 0755)
	for _, snap := range snapshots {
		if !snap.Exists {
			continue
		}
		data := snap.Content
		if len(data) == 0 {
			snapPath := filepath.Join(m.dataDir, id+"_"+sanitizePath(snap.Path))
			var err error
			data, err = os.ReadFile(snapPath)
			if err != nil {
				continue
			}
		}
		dest := filepath.Join(forkDir, snap.Path)
		os.MkdirAll(filepath.Dir(dest), 0755)
		os.WriteFile(dest, data, 0644)
	}
	return nil
}

func (m *Manager) saveIndex() {
	data, _ := json.MarshalIndent(m.checkpoints, "", "  ")
	os.WriteFile(filepath.Join(m.dataDir, "index.json"), data, 0644)
}

func (m *Manager) loadSnapshots(id string) ([]FileSnapshot, error) {
	// Load checkpoint index
	data, err := os.ReadFile(filepath.Join(m.dataDir, "index.json"))
	if err != nil {
		return nil, err
	}
	var checkpoints []Checkpoint
	json.Unmarshal(data, &checkpoints)

	for _, cp := range checkpoints {
		if cp.ID != id {
			continue
		}
		var snaps []FileSnapshot
		for path := range cp.Files {
			snapPath := filepath.Join(m.dataDir, id+"_"+sanitizePath(path))
			content, err := os.ReadFile(snapPath)
			if err != nil {
				continue
			}
			snaps = append(snaps, FileSnapshot{
				Path:    path,
				Content: content,
				Exists:  true,
			})
		}
		return snaps, nil
	}
	return nil, fmt.Errorf("checkpoint nao encontrado")
}

func hashContent(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:8])
}

func sanitizePath(path string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_")
	return r.Replace(path)
}
