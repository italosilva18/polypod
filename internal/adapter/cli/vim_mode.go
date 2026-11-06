package cli

import (
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

// ── Vim mode types ───────────────────────────────────────────────────────────

// VimMode represents the current editing mode of a VimInputModel.
type VimMode string

const (
	VimModeNormal VimMode = "NORMAL"
	VimModeInsert VimMode = "INSERT"
)

// VimInputModel is a bubbletea model that wraps text editing with vim-style
// modal input (Normal and Insert modes). It stores text as a plain string and
// tracks the cursor position in rune units.
type VimInputModel struct {
	mode       VimMode
	content    string
	cursor     int
	yankBuffer string

	// pendingKey accumulates multi-key sequences (e.g. "dd", "yy").
	pendingKey string
}

// NewVimInputModel returns a VimInputModel starting in Normal mode.
func NewVimInputModel() VimInputModel {
	return VimInputModel{mode: VimModeNormal}
}

// Init satisfies tea.Model; no initial commands are needed.
func (v VimInputModel) Init() tea.Cmd {
	return nil
}

// Update processes tea messages and returns the updated model and an optional
// command. It dispatches to normal-mode or insert-mode handlers depending on
// the current mode.
func (v VimInputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return v, nil
	}

	if v.mode == VimModeInsert {
		return v.updateInsert(keyMsg)
	}
	return v.updateNormal(keyMsg)
}

// updateInsert handles key presses while in Insert mode.
func (v VimInputModel) updateInsert(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.Type {
	case tea.KeyEsc:
		v.mode = VimModeNormal
		// Move cursor one position to the left when leaving insert mode,
		// matching classic vim behaviour.
		runes := []rune(v.content)
		if v.cursor > 0 && v.cursor >= len(runes) {
			v.cursor = len(runes) - 1
			if v.cursor < 0 {
				v.cursor = 0
			}
		}
		return v, nil

	case tea.KeyBackspace, tea.KeyDelete:
		runes := []rune(v.content)
		if v.cursor > 0 && len(runes) > 0 {
			out := append([]rune{}, runes[:v.cursor-1]...)
			out = append(out, runes[v.cursor:]...)
			v.content = string(out)
			v.cursor--
		}
		return v, nil

	case tea.KeyEnter:
		v = v.insertRune('\n')
		return v, nil

	case tea.KeyRunes:
		for _, r := range key.Runes {
			v = v.insertRune(r)
		}
		return v, nil

	default:
		return v, nil
	}
}

// updateNormal handles key presses while in Normal mode.
func (v VimInputModel) updateNormal(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle single-rune keys.
	if key.Type == tea.KeyRunes && len(key.Runes) == 1 {
		r := key.Runes[0]
		ch := string(r)

		// Handle two-key sequences (dd, yy).
		if v.pendingKey != "" {
			seq := v.pendingKey + ch
			v.pendingKey = ""
			switch seq {
			case "dd":
				v.content = ""
				v.cursor = 0
			case "yy":
				v.yankBuffer = v.content
			}
			return v, nil
		}

		runes := []rune(v.content)

		switch ch {
		case "i":
			v.mode = VimModeInsert
		case "a":
			// Append: enter insert mode with cursor after current character.
			v.mode = VimModeInsert
			if v.cursor < len(runes) {
				v.cursor++
			}
		case "h":
			if v.cursor > 0 {
				v.cursor--
			}
		case "l":
			if v.cursor < len(runes)-1 {
				v.cursor++
			}
		case "k":
			// Move up one visual line.
			v.cursor = v.lineUp(runes)
		case "j":
			// Move down one visual line.
			v.cursor = v.lineDown(runes)
		case "p":
			// Paste from yank buffer at current cursor position.
			if v.yankBuffer != "" {
				insert := []rune(v.yankBuffer)
				out := append([]rune{}, runes[:v.cursor]...)
				out = append(out, insert...)
				out = append(out, runes[v.cursor:]...)
				v.content = string(out)
				v.cursor += len(insert)
			}
		case "d", "y":
			// Start a two-key sequence.
			v.pendingKey = ch
		}
		return v, nil
	}

	// Non-rune special keys in Normal mode.
	switch key.Type {
	case tea.KeyEsc:
		// Clear any pending sequence on Escape.
		v.pendingKey = ""
	}
	return v, nil
}

// insertRune inserts r at the current cursor position and advances cursor.
func (v VimInputModel) insertRune(r rune) VimInputModel {
	runes := []rune(v.content)
	out := append([]rune{}, runes[:v.cursor]...)
	out = append(out, r)
	out = append(out, runes[v.cursor:]...)
	v.content = string(out)
	v.cursor++
	return v
}

// lineUp moves the cursor to the same column on the line above, returning the
// new cursor position.
func (v VimInputModel) lineUp(runes []rune) int {
	if v.cursor == 0 {
		return 0
	}
	// Find start of current line.
	lineStart := v.cursor
	for lineStart > 0 && runes[lineStart-1] != '\n' {
		lineStart--
	}
	if lineStart == 0 {
		return 0 // already on first line
	}
	col := v.cursor - lineStart

	// Find start of previous line.
	prevEnd := lineStart - 1 // position of '\n' that ends the previous line
	prevStart := prevEnd
	for prevStart > 0 && runes[prevStart-1] != '\n' {
		prevStart--
	}
	prevLen := prevEnd - prevStart
	if col > prevLen {
		col = prevLen
	}
	return prevStart + col
}

// lineDown moves the cursor to the same column on the line below, returning
// the new cursor position.
func (v VimInputModel) lineDown(runes []rune) int {
	if v.cursor >= len(runes) {
		return v.cursor
	}
	// Find start of current line.
	lineStart := v.cursor
	for lineStart > 0 && runes[lineStart-1] != '\n' {
		lineStart--
	}
	col := v.cursor - lineStart

	// Find end of current line.
	lineEnd := v.cursor
	for lineEnd < len(runes) && runes[lineEnd] != '\n' {
		lineEnd++
	}
	if lineEnd >= len(runes) {
		return v.cursor // already on last line
	}

	// lineEnd points to the '\n'. Next line starts at lineEnd+1.
	nextStart := lineEnd + 1
	nextEnd := nextStart
	for nextEnd < len(runes) && runes[nextEnd] != '\n' {
		nextEnd++
	}
	nextLen := nextEnd - nextStart
	if col > nextLen {
		col = nextLen
	}
	return nextStart + col
}

// View renders the input box with a mode indicator.
func (v VimInputModel) View() string {
	var sb strings.Builder

	// Mode badge.
	modeLabel := fmt.Sprintf(" %s ", v.mode)
	sb.WriteString(modeLabel)
	sb.WriteString(" ")

	// Render content with a cursor marker.
	runes := []rune(v.content)
	for i, r := range runes {
		if i == v.cursor && v.mode == VimModeNormal {
			// Highlight cursor position with brackets.
			sb.WriteString("[")
			sb.WriteRune(r)
			sb.WriteString("]")
		} else {
			sb.WriteRune(r)
		}
	}
	// Show cursor at end when in insert mode or content is empty.
	if v.mode == VimModeInsert && v.cursor == len(runes) {
		sb.WriteString("|")
	} else if v.mode == VimModeNormal && len(runes) == 0 {
		sb.WriteString("[]")
	}

	return sb.String()
}

// Content returns the current text content.
func (v VimInputModel) Content() string {
	return v.content
}

// SetContent replaces the content and resets the cursor.
func (v VimInputModel) SetContent(s string) VimInputModel {
	v.content = s
	v.cursor = 0
	return v
}

// Mode returns the current VimMode.
func (v VimInputModel) Mode() VimMode {
	return v.mode
}

// ── Attachment manager ───────────────────────────────────────────────────────

// maxAttachmentSize is the maximum allowed attachment size (10 MiB).
const maxAttachmentSize = 10 * 1024 * 1024

// Attachment describes a file that has been queued for upload.
type Attachment struct {
	Path     string
	Size     int64
	MIMEType string
}

// AttachmentManager manages a list of file attachments pending upload.
type AttachmentManager struct {
	mu    sync.Mutex
	items []Attachment
}

// NewAttachmentManager returns an empty AttachmentManager.
func NewAttachmentManager() *AttachmentManager {
	return &AttachmentManager{}
}

// Add validates that path exists and is under 10 MiB, then appends it to the
// attachment list. Returns an error if the file is missing or too large.
func (a *AttachmentManager) Add(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("attachment: cannot access %q: %w", path, err)
	}
	if info.Size() > maxAttachmentSize {
		return fmt.Errorf("attachment: %q is %.1f MiB, exceeds 10 MiB limit",
			path, float64(info.Size())/(1024*1024))
	}

	att := Attachment{
		Path:     path,
		Size:     info.Size(),
		MIMEType: detectMIMEType(path),
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	// Deduplicate by path.
	for _, existing := range a.items {
		if existing.Path == path {
			return nil
		}
	}
	a.items = append(a.items, att)
	return nil
}

// Remove removes the attachment with the given path, if present.
func (a *AttachmentManager) Remove(path string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	filtered := a.items[:0]
	for _, att := range a.items {
		if att.Path != path {
			filtered = append(filtered, att)
		}
	}
	a.items = filtered
}

// List returns a copy of the current attachment list.
func (a *AttachmentManager) List() []Attachment {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Attachment, len(a.items))
	copy(out, a.items)
	return out
}

// Clear removes all attachments.
func (a *AttachmentManager) Clear() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.items = a.items[:0]
}

// detectMIMEType returns a MIME type based on the file extension.
// Falls back to "text/plain" for unrecognised extensions.
func detectMIMEType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".pdf":
		return "application/pdf"
	}
	// Try the stdlib mime package as a fallback.
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return "text/plain"
}

// ── Global attachment manager singleton ─────────────────────────────────────

var (
	globalAttachmentManager     *AttachmentManager
	globalAttachmentManagerOnce sync.Once
)

// GetAttachmentManager returns the process-wide singleton AttachmentManager,
// creating it on first call.
func GetAttachmentManager() *AttachmentManager {
	globalAttachmentManagerOnce.Do(func() {
		globalAttachmentManager = NewAttachmentManager()
	})
	return globalAttachmentManager
}
