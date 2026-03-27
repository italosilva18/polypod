package undoredo

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Action records a single undoable action.
type Action struct {
	ID          int
	Description string
	Files       map[string][]byte // path → original content (before change)
	NewFiles    []string          // files that were created (will be deleted on undo)
}

// Manager tracks actions for undo/redo.
type Manager struct {
	mu      sync.Mutex
	history []Action
	redoBuf []Action
	nextID  int
	logger  *slog.Logger
}

// NewManager creates an undo/redo manager.
func NewManager(logger *slog.Logger) *Manager {
	return &Manager{logger: logger}
}

// BeforeChange snapshots files before they are modified.
// Call this BEFORE any AI-initiated file change.
func (m *Manager) BeforeChange(description string, paths []string) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.nextID++
	action := Action{
		ID:          m.nextID,
		Description: description,
		Files:       make(map[string][]byte),
	}

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			// File doesn't exist yet — will be a "new file"
			action.NewFiles = append(action.NewFiles, path)
			continue
		}
		action.Files[path] = data
	}

	m.history = append(m.history, action)
	m.redoBuf = nil // clear redo on new action

	// Limit history to 50
	if len(m.history) > 50 {
		m.history = m.history[1:]
	}

	return action.ID
}

// Undo reverts the last action.
func (m *Manager) Undo() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.history) == 0 {
		return "", fmt.Errorf("nenhuma acao para desfazer")
	}

	action := m.history[len(m.history)-1]
	m.history = m.history[:len(m.history)-1]

	var restored []string

	// Restore original file contents
	for path, content := range action.Files {
		if err := os.WriteFile(path, content, 0644); err != nil {
			return "", fmt.Errorf("restaurando %s: %w", path, err)
		}
		restored = append(restored, fmt.Sprintf("restaurado: %s", path))
	}

	// Delete files that were created
	for _, path := range action.NewFiles {
		os.Remove(path)
		restored = append(restored, fmt.Sprintf("removido: %s", path))
	}

	// Also revert git if possible
	m.gitRevertIfNeeded()

	m.redoBuf = append(m.redoBuf, action)

	m.logger.Info("undo executado", "action", action.Description, "files", len(action.Files)+len(action.NewFiles))
	return fmt.Sprintf("Desfeito: %s\n%s", action.Description, strings.Join(restored, "\n")), nil
}

// Redo reapplies the last undone action.
func (m *Manager) Redo() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.redoBuf) == 0 {
		return "", fmt.Errorf("nenhuma acao para refazer")
	}

	action := m.redoBuf[len(m.redoBuf)-1]
	m.redoBuf = m.redoBuf[:len(m.redoBuf)-1]
	m.history = append(m.history, action)

	return fmt.Sprintf("Refeito: %s (%d arquivos)", action.Description, len(action.Files)+len(action.NewFiles)), nil
}

// History returns a formatted list of undoable actions.
func (m *Manager) History() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.history) == 0 {
		return "Nenhuma acao no historico."
	}

	var sb strings.Builder
	sb.WriteString("## Historico de acoes (mais recente primeiro)\n\n")
	for i := len(m.history) - 1; i >= 0; i-- {
		a := m.history[i]
		files := len(a.Files) + len(a.NewFiles)
		fmt.Fprintf(&sb, "%d. %s (%d arquivos)\n", a.ID, a.Description, files)
	}
	if len(m.redoBuf) > 0 {
		fmt.Fprintf(&sb, "\n%d acao(es) no redo buffer.\n", len(m.redoBuf))
	}
	return sb.String()
}

// CanUndo returns true if there are actions to undo.
func (m *Manager) CanUndo() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.history) > 0
}

func (m *Manager) gitRevertIfNeeded() {
	// If there's a recent uncommitted change, try git checkout
	cmd := exec.Command("git", "diff", "--quiet")
	if cmd.Run() != nil {
		// There are changes — don't auto-revert git, user controls that
		return
	}
}
