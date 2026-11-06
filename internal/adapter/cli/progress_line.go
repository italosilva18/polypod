package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// progressSpinnerFrames is the braille spinner used for the per-tool
// running indicator. Reused with the same frames as the status bar so
// the two animations stay in visual sync.
var progressSpinnerFrames = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

// progressLineWidth is the maximum number of characters in a single
// progress line message before truncation.
const progressLineMaxMessage = 60

// progressLineMaxOutputLines is the maximum number of recent stdout
// lines we keep per tool.
const progressLineMaxOutputLines = 5

// progressLineCleanupAfter is how long a completed (non-error) tool
// stays in the display before Cleanup removes it.
const progressLineCleanupAfter = 2 * time.Second

// ToolProgress represents the live state of a single tool execution
// for purposes of the in-stream progress display. It is independent
// from the adapter.ToolEventInfo log and is mutated in place by the
// ProgressLineModel.
type ToolProgress struct {
	ToolName  string
	ToolID    string
	StartTime time.Time
	// Status is one of "running", "completed", "error".
	Status string
	// Message is the latest one-line progress message (set by UpdateTool).
	Message string
	// Lines holds the most recent output lines, oldest first, capped
	// at progressLineMaxOutputLines.
	Lines []string
	// Spinner tracks the current animation frame.
	Spinner int
	// CompletedAt records when Status transitioned to completed/error,
	// used by Cleanup to expire old entries.
	CompletedAt time.Time
}

// ProgressLineModel tracks all currently-displayed tool progress lines
// and renders them as a stack of single lines above the streaming
// response. It is owned by the TUI model and updated from tool
// lifecycle events.
type ProgressLineModel struct {
	items map[string]*ToolProgress
	order []string
	width int
}

// NewProgressLineModel constructs an empty ProgressLineModel ready to
// receive tool events.
func NewProgressLineModel() *ProgressLineModel {
	return &ProgressLineModel{
		items: make(map[string]*ToolProgress),
	}
}

// SetWidth records the available terminal width so Render can wrap or
// truncate lines appropriately.
func (p *ProgressLineModel) SetWidth(w int) {
	if w < 0 {
		w = 0
	}
	p.width = w
}

// StartTool records the beginning of a new tool execution. If a tool
// with the same id already exists it is reset to the running state.
func (p *ProgressLineModel) StartTool(id, name string) {
	if p == nil || id == "" {
		return
	}
	if existing, ok := p.items[id]; ok {
		existing.ToolName = name
		existing.Status = "running"
		existing.StartTime = time.Now()
		existing.CompletedAt = time.Time{}
		return
	}
	p.items[id] = &ToolProgress{
		ToolName:  name,
		ToolID:    id,
		StartTime: time.Now(),
		Status:    "running",
	}
	p.order = append(p.order, id)
}

// UpdateTool replaces the latest progress message for a running tool.
// Unknown ids are ignored.
func (p *ProgressLineModel) UpdateTool(id, message string) {
	if p == nil {
		return
	}
	t, ok := p.items[id]
	if !ok {
		return
	}
	t.Message = truncateRunes(message, progressLineMaxMessage)
}

// AppendLine adds a line of output to a running tool, evicting the
// oldest line when the buffer exceeds progressLineMaxOutputLines.
func (p *ProgressLineModel) AppendLine(id, line string) {
	if p == nil {
		return
	}
	t, ok := p.items[id]
	if !ok {
		return
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return
	}
	t.Lines = append(t.Lines, truncateRunes(line, progressLineMaxMessage))
	if len(t.Lines) > progressLineMaxOutputLines {
		t.Lines = t.Lines[len(t.Lines)-progressLineMaxOutputLines:]
	}
	// Mirror the latest line into Message so the single-line render
	// reflects the most recent activity.
	t.Message = t.Lines[len(t.Lines)-1]
}

// CompleteTool marks a tool as successfully completed. The entry stays
// visible (with a ✓) until Cleanup removes it after
// progressLineCleanupAfter.
func (p *ProgressLineModel) CompleteTool(id string) {
	if p == nil {
		return
	}
	t, ok := p.items[id]
	if !ok {
		return
	}
	t.Status = "completed"
	t.CompletedAt = time.Now()
}

// ErrorTool marks a tool as failed and records the error message. The
// entry stays visible until the next conversation turn (Cleanup keeps
// errored entries indefinitely).
func (p *ProgressLineModel) ErrorTool(id, errMsg string) {
	if p == nil {
		return
	}
	t, ok := p.items[id]
	if !ok {
		return
	}
	t.Status = "error"
	t.Message = truncateRunes(errMsg, progressLineMaxMessage)
	t.CompletedAt = time.Now()
}

// Tick advances all spinners by one frame. Callers should arrange to
// invoke this roughly every 100ms while ActiveCount() > 0.
func (p *ProgressLineModel) Tick() {
	if p == nil {
		return
	}
	for _, t := range p.items {
		if t.Status == "running" {
			t.Spinner = (t.Spinner + 1) % len(progressSpinnerFrames)
		}
	}
}

// ActiveCount returns the number of tools currently in the running
// state. Used by the model to decide whether to keep the spinner
// ticker scheduled.
func (p *ProgressLineModel) ActiveCount() int {
	if p == nil {
		return 0
	}
	n := 0
	for _, t := range p.items {
		if t.Status == "running" {
			n++
		}
	}
	return n
}

// Cleanup removes completed (non-error) entries that have been visible
// for longer than progressLineCleanupAfter. Errored entries are kept
// so the user always sees them after the turn ends.
func (p *ProgressLineModel) Cleanup() {
	if p == nil {
		return
	}
	now := time.Now()
	kept := p.order[:0]
	for _, id := range p.order {
		t, ok := p.items[id]
		if !ok {
			continue
		}
		if t.Status == "completed" && !t.CompletedAt.IsZero() &&
			now.Sub(t.CompletedAt) > progressLineCleanupAfter {
			delete(p.items, id)
			continue
		}
		kept = append(kept, id)
	}
	p.order = kept
}

// Reset clears every tracked progress entry. Called by the model when
// a new conversation turn starts so stale lines don't carry over.
func (p *ProgressLineModel) Reset() {
	if p == nil {
		return
	}
	p.items = make(map[string]*ToolProgress)
	p.order = nil
}

// Render returns the multi-line string representation of all currently
// tracked tools. Returns "" when there is nothing to display.
//
// Visual layout (one line per tool):
//
//	⠋ bash: go build ./...                       2.3s
//	⠙ read_file: internal/large/file.go          0.5s
//	✓ glob: **/*.go (50 files)                   0.1s
func (p *ProgressLineModel) Render() string {
	if p == nil || len(p.order) == 0 {
		return ""
	}

	var b strings.Builder
	for _, id := range p.order {
		t, ok := p.items[id]
		if !ok {
			continue
		}
		b.WriteString(p.renderOne(t))
		b.WriteString("\n")
	}
	return b.String()
}

// renderOne builds a single styled progress line for one tool.
func (p *ProgressLineModel) renderOne(t *ToolProgress) string {
	var marker string
	var markerStyle lipgloss.Style

	switch t.Status {
	case "running":
		frame := progressSpinnerFrames[t.Spinner%len(progressSpinnerFrames)]
		marker = string(frame)
		markerStyle = lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	case "completed":
		marker = "✓"
		markerStyle = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	case "error":
		marker = "✗"
		markerStyle = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
	default:
		marker = "•"
		markerStyle = lipgloss.NewStyle().Foreground(colorDim)
	}

	label := t.ToolName
	if t.Message != "" {
		label = fmt.Sprintf("%s: %s", t.ToolName, t.Message)
	}
	label = truncateRunes(label, progressLineMaxMessage)

	elapsed := time.Since(t.StartTime)
	if t.Status != "running" && !t.CompletedAt.IsZero() {
		elapsed = t.CompletedAt.Sub(t.StartTime)
	}
	timing := formatDuration(elapsed)

	labelStyle := lipgloss.NewStyle().Foreground(colorWhiteDim)
	if t.Status == "error" {
		labelStyle = labelStyle.Foreground(colorRed)
	}
	timingStyle := lipgloss.NewStyle().Foreground(colorSubtle)

	var b strings.Builder
	b.WriteString("  ")
	b.WriteString(markerStyle.Render(marker))
	b.WriteString(" ")
	b.WriteString(labelStyle.Render(label))
	b.WriteString("  ")
	b.WriteString(timingStyle.Render(timing))
	return b.String()
}

// truncateRunes returns s truncated (with a trailing ellipsis) so its
// rune length does not exceed max. When max <= 0 the string is
// returned unchanged.
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}
