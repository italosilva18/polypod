package cli

import (
	"sync"
	"time"

	"github.com/costa/polypod/internal/convtree"
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

	// branches holds the named conversation branches managed by
	// /branch. The "main" branch is created lazily on first use and
	// is always present. currentBranch points to the active branch
	// name; the underlying convtree.Tree carries the branch state.
	branches      map[string]*branchState
	currentBranch string
}

// branchState wraps a convtree.Tree with metadata used by /branch
// (creation timestamp, parent branch, fork point).
type branchState struct {
	name       string
	tree       *convtree.Tree
	createdAt  time.Time
	parent     string // empty for "main"
	forkAtMsg  int    // 1-based index of the message in the parent at fork time
	forkAtNode string // parent node ID where the fork happened
}

// newCLISessionState creates an empty session state.
func newCLISessionState() *cliSessionState {
	s := &cliSessionState{
		readFiles: make(map[string]*fileEntry),
		effort:    EffortMedium,
		branches:  make(map[string]*branchState),
	}
	// Bootstrap the "main" branch so /branch always has somewhere to
	// go even before the user creates anything.
	s.branches["main"] = &branchState{
		name:      "main",
		tree:      convtree.NewTree(),
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
	// Add the rest in insertion-stable order.
	for name, b := range s.branches {
		if name == "main" {
			continue
		}
		out = append(out, *b)
	}
	return out, s.currentBranch
}

// getBranch returns the named branch, or nil if not found.
func (s *cliSessionState) getBranch(name string) *branchState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.branches[name]
}

// createBranch forks a new branch from the current one.
func (s *cliSessionState) createBranch(name string, currentMsgCount int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "" {
		return errBranchEmptyName
	}
	if _, exists := s.branches[name]; exists {
		return errBranchExists
	}
	parent := s.branches[s.currentBranch]
	parentNode := ""
	if parent != nil && parent.tree != nil && parent.tree.Current != nil {
		parentNode = parent.tree.Current.ID
	}
	s.branches[name] = &branchState{
		name:       name,
		tree:       convtree.NewTree(),
		createdAt:  time.Now(),
		parent:     s.currentBranch,
		forkAtMsg:  currentMsgCount,
		forkAtNode: parentNode,
	}
	s.currentBranch = name
	return nil
}

// switchBranch makes the named branch active.
func (s *cliSessionState) switchBranch(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.branches[name]; !ok {
		return errBranchNotFound
	}
	s.currentBranch = name
	return nil
}

// deleteBranch removes a branch (cannot delete main or current).
func (s *cliSessionState) deleteBranch(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "main" {
		return errBranchProtected
	}
	if name == s.currentBranch {
		return errBranchInUse
	}
	if _, ok := s.branches[name]; !ok {
		return errBranchNotFound
	}
	delete(s.branches, name)
	return nil
}

// recordBranchMessage adds a message to the current branch's tree so
// the visual /branch tree reflects the running conversation.
func (s *cliSessionState) recordBranchMessage(role, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.branches[s.currentBranch]
	if !ok || b.tree == nil {
		return
	}
	b.tree.AddMessage(role, content)
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
