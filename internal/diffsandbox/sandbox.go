package diffsandbox

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// PendingChange represents a staged file change awaiting approval.
type PendingChange struct {
	Path       string
	Action     string // "create", "edit", "delete"
	OldContent []byte // nil for create
	NewContent []byte // nil for delete
	OldText    string // for edit: the text being replaced
	NewText    string // for edit: the replacement text
}

// Sandbox holds all pending changes in a staging area.
type Sandbox struct {
	mu      sync.Mutex
	changes []PendingChange
	enabled bool
}

// New creates a diff sandbox.
func New(enabled bool) *Sandbox {
	return &Sandbox{enabled: enabled}
}

// IsEnabled returns whether sandbox mode is active.
func (s *Sandbox) IsEnabled() bool { return s.enabled }

// Enable turns on sandbox mode.
func (s *Sandbox) Enable() { s.enabled = true }

// Disable turns off sandbox mode.
func (s *Sandbox) Disable() { s.enabled = false }

// StageCreate stages a file creation.
func (s *Sandbox) StageCreate(path string, content []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.changes = append(s.changes, PendingChange{
		Path:       path,
		Action:     "create",
		NewContent: content,
	})
}

// StageEdit stages a file edit.
func (s *Sandbox) StageEdit(path string, oldContent, newContent []byte, oldText, newText string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.changes = append(s.changes, PendingChange{
		Path:       path,
		Action:     "edit",
		OldContent: oldContent,
		NewContent: newContent,
		OldText:    oldText,
		NewText:    newText,
	})
}

// StageDelete stages a file deletion.
func (s *Sandbox) StageDelete(path string, oldContent []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.changes = append(s.changes, PendingChange{
		Path:       path,
		Action:     "delete",
		OldContent: oldContent,
	})
}

// Pending returns the number of staged changes.
func (s *Sandbox) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.changes)
}

// FormatDiff shows all pending changes as a combined diff.
func (s *Sandbox) FormatDiff() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.changes) == 0 {
		return "Nenhuma mudanca pendente."
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## %d mudanca(s) pendente(s)\n\n", len(s.changes))

	totalAdded, totalRemoved := 0, 0

	for i, c := range s.changes {
		switch c.Action {
		case "create":
			lines := strings.Count(string(c.NewContent), "\n") + 1
			totalAdded += lines
			fmt.Fprintf(&sb, "### %d. NOVO: %s (+%d linhas)\n", i+1, c.Path, lines)
			content := string(c.NewContent)
			if len(content) > 500 {
				content = content[:500] + "\n... [truncado]"
			}
			fmt.Fprintf(&sb, "```\n%s\n```\n\n", content)

		case "edit":
			oldLines := strings.Count(c.OldText, "\n") + 1
			newLines := strings.Count(c.NewText, "\n") + 1
			totalRemoved += oldLines
			totalAdded += newLines
			fmt.Fprintf(&sb, "### %d. EDITAR: %s (-%d +%d linhas)\n", i+1, c.Path, oldLines, newLines)
			fmt.Fprintf(&sb, "```diff\n")
			for _, line := range strings.Split(c.OldText, "\n") {
				fmt.Fprintf(&sb, "- %s\n", line)
			}
			for _, line := range strings.Split(c.NewText, "\n") {
				fmt.Fprintf(&sb, "+ %s\n", line)
			}
			fmt.Fprintf(&sb, "```\n\n")

		case "delete":
			lines := strings.Count(string(c.OldContent), "\n") + 1
			totalRemoved += lines
			fmt.Fprintf(&sb, "### %d. EXCLUIR: %s (-%d linhas)\n\n", i+1, c.Path, lines)
		}
	}

	fmt.Fprintf(&sb, "**Total**: +%d -%d linhas em %d arquivo(s)\n", totalAdded, totalRemoved, len(s.changes))
	fmt.Fprintf(&sb, "\nUse `/apply` para aplicar ou `/reject` para descartar.")

	return sb.String()
}

// Apply executes all pending changes on the real filesystem.
func (s *Sandbox) Apply() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var applied []string

	for _, c := range s.changes {
		switch c.Action {
		case "create":
			dir := strings.TrimSuffix(c.Path, "/"+strings.Split(c.Path, "/")[len(strings.Split(c.Path, "/"))-1])
			if dir != c.Path {
				os.MkdirAll(dir, 0755)
			}
			if err := os.WriteFile(c.Path, c.NewContent, 0644); err != nil {
				return applied, fmt.Errorf("criando %s: %w", c.Path, err)
			}
			applied = append(applied, fmt.Sprintf("criado: %s", c.Path))

		case "edit":
			if err := os.WriteFile(c.Path, c.NewContent, 0644); err != nil {
				return applied, fmt.Errorf("editando %s: %w", c.Path, err)
			}
			applied = append(applied, fmt.Sprintf("editado: %s", c.Path))

		case "delete":
			if err := os.Remove(c.Path); err != nil {
				return applied, fmt.Errorf("excluindo %s: %w", c.Path, err)
			}
			applied = append(applied, fmt.Sprintf("excluido: %s", c.Path))
		}
	}

	s.changes = nil
	return applied, nil
}

// Reject discards all pending changes.
func (s *Sandbox) Reject() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.changes)
	s.changes = nil
	return n
}

// Changes returns a copy of pending changes.
func (s *Sandbox) Changes() []PendingChange {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]PendingChange, len(s.changes))
	copy(result, s.changes)
	return result
}
