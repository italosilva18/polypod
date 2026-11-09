package cli

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// cliSessionState holds runtime state that is local to a CLI session and
// mutated by slash commands (e.g. /files, /add-dir). Safe for concurrent
// access so the bubbletea update loop and background goroutines can share it.
type cliSessionState struct {
	mu sync.Mutex

	// readFiles tracks files referenced by tool calls during the session.
	// The key is the normalized path; values carry timestamps and edit
	// counts.
	readFiles map[string]*fileEntry

	// extraDirs is the ordered list of additional working directories
	// added via /add-dir. Kept in insertion order; duplicates are
	// ignored.
	extraDirs []dirEntry

	// effort is the current AI thinking effort/budget level. The next
	// AI call should respect this. Mutated by /effort.
	effort EffortLevel

	// branches holds the named persisted conversations managed by /branch.
	// The runtime refreshes this metadata from disk on each branch command.
	branches      map[string]*branchState
	currentBranch string
	branchOps     *branchCallbacks
}

// branchState identifies one persisted conversation and its fork point.
type branchState struct {
	name         string
	createdAt    time.Time
	parent       string // empty for "main"
	forkAtMsg    int    // message count in the parent at fork time
	sessionID    string // persisted transcript selected when this branch is active
	messageCount int
}

// branchCallbacks keep the CLI state independent of the transcript store.
// The adapter supplies implementations that save and restore the actual AI
// conversation before reporting a successful branch change.
type branchCallbacks struct {
	load     func() (branchSnapshot, error)
	create   func(name string, visible []chatEntry) (branchSnapshot, error)
	switchTo func(name string, visible []chatEntry) (branchSnapshot, error)
	delete   func(name string, visible []chatEntry) (branchSnapshot, error)
}

type branchSnapshot struct {
	branches map[string]*branchState
	current  string
	messages []chatEntry
}

var errBranchRuntimeUnavailable = errors.New("historico persistente de branches indisponivel")

// BindBranches attaches persistent branch operations and restores branch
// metadata for the currently selected CLI session.
func (s *cliSessionState) BindBranches(ops branchCallbacks) error {
	if ops.load == nil || ops.create == nil || ops.switchTo == nil || ops.delete == nil {
		return errBranchRuntimeUnavailable
	}
	snapshot, err := ops.load()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.branchOps = &ops
	s.applyBranchSnapshotLocked(snapshot)
	s.mu.Unlock()
	return nil
}

func (s *cliSessionState) branchesBound() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.branchOps != nil
}

func (s *cliSessionState) applyBranchSnapshotLocked(snapshot branchSnapshot) {
	s.branches = snapshot.branches
	s.currentBranch = snapshot.current
}

func (s *cliSessionState) refreshPersistentBranches() error {
	s.mu.Lock()
	ops := s.branchOps
	s.mu.Unlock()
	if ops == nil {
		return errBranchRuntimeUnavailable
	}
	snapshot, err := ops.load()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.applyBranchSnapshotLocked(snapshot)
	s.mu.Unlock()
	return nil
}

func (s *cliSessionState) mutatePersistentBranch(action, name string, visible []chatEntry) ([]chatEntry, error) {
	s.mu.Lock()
	ops := s.branchOps
	s.mu.Unlock()
	if ops == nil {
		return nil, errBranchRuntimeUnavailable
	}
	var snapshot branchSnapshot
	var err error
	switch action {
	case "create":
		snapshot, err = ops.create(name, visible)
	case "switch":
		snapshot, err = ops.switchTo(name, visible)
	case "delete":
		snapshot, err = ops.delete(name, visible)
	}
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.applyBranchSnapshotLocked(snapshot)
	s.mu.Unlock()
	return snapshot.messages, nil
}

// newCLISessionState creates an empty session state.
func newCLISessionState() *cliSessionState {
	s := &cliSessionState{
		readFiles: make(map[string]*fileEntry),
		effort:    EffortMedium,
		branches:  make(map[string]*branchState),
	}
	// Bootstrap main until the persisted branch runtime is bound.
	s.branches["main"] = &branchState{
		name:      "main",
		createdAt: time.Now(),
	}
	s.currentBranch = "main"
	return s
}

// getEffort returns the current effort level (thread-safe).
func (s *cliSessionState) getEffort() EffortLevel {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.effort
}

// setEffort updates the current effort level (thread-safe).
func (s *cliSessionState) setEffort(level EffortLevel) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.effort = level
}

// snapshotBranches returns a copy of all branches in deterministic order
// (main first, then alphabetical).
func (s *cliSessionState) snapshotBranches() ([]branchState, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]branchState, 0, len(s.branches))
	if main, ok := s.branches["main"]; ok {
		out = append(out, *main)
	}
	names := make([]string, 0, len(s.branches))
	for name := range s.branches {
		if name != "main" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, *s.branches[name])
	}
	return out, s.currentBranch
}

// recordFileRead marks a file as read in the current context.
func (s *cliSessionState) recordFileRead(path string) {
	if path == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.readFiles[path]; ok {
		f.readAt = time.Now()
		return
	}
	s.readFiles[path] = &fileEntry{path: path, readAt: time.Now()}
}

// recordFileEdit marks a file as edited in the current context.
func (s *cliSessionState) recordFileEdit(path string) {
	if path == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.readFiles[path]
	if !ok {
		f = &fileEntry{path: path, readAt: time.Now()}
		s.readFiles[path] = f
	}
	f.edited = true
	f.editCount++
	f.readAt = time.Now()
}

// snapshotFiles returns a copy of the currently tracked files.
func (s *cliSessionState) snapshotFiles() []fileEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]fileEntry, 0, len(s.readFiles))
	for _, f := range s.readFiles {
		out = append(out, *f)
	}
	return out
}

// clearFiles drops all tracked file entries.
func (s *cliSessionState) clearFiles() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readFiles = make(map[string]*fileEntry)
}

// addDir adds a working directory if it's not already present.
func (s *cliSessionState) addDir(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.extraDirs {
		if d.path == path {
			return
		}
	}
	s.extraDirs = append(s.extraDirs, dirEntry{path: path, addedAt: time.Now()})
}

// removeDir removes a working directory. Returns true if it was found.
func (s *cliSessionState) removeDir(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, d := range s.extraDirs {
		if d.path == path {
			s.extraDirs = append(s.extraDirs[:i], s.extraDirs[i+1:]...)
			return true
		}
	}
	return false
}

// snapshotDirs returns a copy of the additional working directories.
func (s *cliSessionState) snapshotDirs() []dirEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]dirEntry, len(s.extraDirs))
	copy(out, s.extraDirs)
	return out
}
