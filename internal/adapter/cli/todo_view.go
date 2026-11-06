package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// TodoItem represents a single entry in a todo list rendered by the TUI.
type TodoItem struct {
	ID       string
	Content  string
	Status   string // "pending", "in_progress", "completed", "cancelled"
	Priority string // "high", "medium", "low"
}

// ── Todo view styles ───────────────────────────────────────────────────────

var (
	todoBoxStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(colorCyan).
			Padding(1, 2)

	todoTitleStyle = lipgloss.NewStyle().
			Foreground(colorCyan).
			Bold(true)

	todoSectionStyle = lipgloss.NewStyle().
				Foreground(colorWhiteDim).
				Bold(true)

	todoCountStyle = lipgloss.NewStyle().
			Foreground(colorDim)

	// Status icon styles
	todoIconPendingStyle = lipgloss.NewStyle().
				Foreground(colorDim)

	todoIconInProgressStyle = lipgloss.NewStyle().
				Foreground(colorYellow).
				Bold(true)

	todoIconCompletedStyle = lipgloss.NewStyle().
				Foreground(colorGreen).
				Bold(true)

	todoIconCancelledStyle = lipgloss.NewStyle().
				Foreground(colorRed).
				Strikethrough(true)

	// Content styles
	todoContentInProgressStyle = lipgloss.NewStyle().
					Foreground(colorWhite).
					Bold(true)

	todoContentPendingStyle = lipgloss.NewStyle().
				Foreground(colorWhite)

	todoContentCompletedStyle = lipgloss.NewStyle().
					Foreground(colorWhiteDim).
					Strikethrough(true)

	todoContentCancelledStyle = lipgloss.NewStyle().
					Foreground(colorDim).
					Strikethrough(true)

	// Priority badges
	todoBadgeHighStyle = lipgloss.NewStyle().
				Foreground(colorRed).
				Bold(true)

	todoBadgeMediumStyle = lipgloss.NewStyle().
				Foreground(colorYellow)

	todoBadgeLowStyle = lipgloss.NewStyle().
				Foreground(colorDim)

	todoNowBadgeStyle = lipgloss.NewStyle().
				Foreground(colorYellow).
				Bold(true)

	todoBarStyle = lipgloss.NewStyle().
			Foreground(colorYellow)

	todoFooterStyle = lipgloss.NewStyle().
			Foreground(colorDim).
			Italic(true)
)

// FormatTodoList renders a todo list as a styled box, ordering items by
// status: in_progress first, then pending, then completed, then cancelled.
//
// title is the heading shown at the top of the box. When empty, "TODO LIST"
// is used as a default.
func FormatTodoList(items []TodoItem, title string) string {
	if title == "" {
		title = "TODO LIST"
	}

	// Bucket items by status
	var inProgress, pending, completed, cancelled []TodoItem
	for _, it := range items {
		switch it.Status {
		case "in_progress":
			inProgress = append(inProgress, it)
		case "completed":
			completed = append(completed, it)
		case "cancelled":
			cancelled = append(cancelled, it)
		default:
			pending = append(pending, it)
		}
	}

	var b strings.Builder

	// Title
	b.WriteString(todoTitleStyle.Render("📋 " + title))
	b.WriteString("\n")
	b.WriteString(separatorStyle.Render(strings.Repeat("─", 50)))
	b.WriteString("\n\n")

	// In-progress section
	if len(inProgress) > 0 {
		b.WriteString(todoIconInProgressStyle.Render("⏳ "))
		b.WriteString(todoSectionStyle.Render("EM PROGRESSO "))
		b.WriteString(todoCountStyle.Render(fmt.Sprintf("(%d)", len(inProgress))))
		b.WriteString("\n")
		for _, it := range inProgress {
			b.WriteString(renderInProgressItem(it))
		}
		b.WriteString("\n")
	}

	// Pending section
	if len(pending) > 0 {
		b.WriteString(todoIconPendingStyle.Render("⏸ "))
		b.WriteString(todoSectionStyle.Render("PENDENTES "))
		b.WriteString(todoCountStyle.Render(fmt.Sprintf("(%d)", len(pending))))
		b.WriteString("\n")
		for _, it := range pending {
			b.WriteString(renderPendingItem(it))
		}
		b.WriteString("\n")
	}

	// Completed section
	if len(completed) > 0 {
		b.WriteString(todoIconCompletedStyle.Render("✓ "))
		b.WriteString(todoSectionStyle.Render("CONCLUIDOS "))
		b.WriteString(todoCountStyle.Render(fmt.Sprintf("(%d)", len(completed))))
		b.WriteString("\n")
		for _, it := range completed {
			b.WriteString(renderCompletedItem(it))
		}
		b.WriteString("\n")
	}

	// Cancelled section
	if len(cancelled) > 0 {
		b.WriteString(todoIconCancelledStyle.Render("✗ "))
		b.WriteString(todoSectionStyle.Render("CANCELADOS "))
		b.WriteString(todoCountStyle.Render(fmt.Sprintf("(%d)", len(cancelled))))
		b.WriteString("\n")
		for _, it := range cancelled {
			b.WriteString(renderCancelledItem(it))
		}
		b.WriteString("\n")
	}

	// Footer summary
	total := len(items)
	if total == 0 {
		b.WriteString(todoFooterStyle.Render("Nenhuma tarefa registrada."))
	} else {
		footer := fmt.Sprintf("Total: %d tarefas (%d ✓, %d ⚡, %d ○",
			total, len(completed), len(inProgress), len(pending))
		if len(cancelled) > 0 {
			footer += fmt.Sprintf(", %d ✗", len(cancelled))
		}
		footer += ")"
		b.WriteString(todoFooterStyle.Render(footer))
	}

	return todoBoxStyle.Render(b.String())
}

// ── Per-item rendering helpers ─────────────────────────────────────────────

func renderInProgressItem(it TodoItem) string {
	var b strings.Builder
	b.WriteString(todoBarStyle.Render("┃  "))
	b.WriteString(todoIconInProgressStyle.Render("⚡ "))
	b.WriteString(todoContentInProgressStyle.Render(it.Content))
	b.WriteString("\n")
	b.WriteString(todoBarStyle.Render("┃     "))
	b.WriteString(renderPriorityBadge(it.Priority))
	b.WriteString("  ")
	b.WriteString(todoNowBadgeStyle.Render("AGORA"))
	b.WriteString("\n")
	return b.String()
}

func renderPendingItem(it TodoItem) string {
	var b strings.Builder
	b.WriteString(todoIconPendingStyle.Render("○  "))
	b.WriteString(todoContentPendingStyle.Render(it.Content))
	b.WriteString("\n")
	b.WriteString("   ")
	b.WriteString(renderPriorityBadge(it.Priority))
	b.WriteString("\n")
	return b.String()
}

func renderCompletedItem(it TodoItem) string {
	var b strings.Builder
	b.WriteString(todoIconCompletedStyle.Render("✓  "))
	b.WriteString(todoContentCompletedStyle.Render(it.Content))
	b.WriteString("\n")
	return b.String()
}

func renderCancelledItem(it TodoItem) string {
	var b strings.Builder
	b.WriteString(todoIconCancelledStyle.Render("✗  "))
	b.WriteString(todoContentCancelledStyle.Render(it.Content))
	b.WriteString("\n")
	return b.String()
}

// renderPriorityBadge returns a styled "[HIGH]"/"[MEDIUM]"/"[LOW]" tag.
func renderPriorityBadge(priority string) string {
	switch strings.ToLower(priority) {
	case "high":
		return todoBadgeHighStyle.Render("[HIGH]")
	case "medium", "":
		return todoBadgeMediumStyle.Render("[MEDIUM]")
	case "low":
		return todoBadgeLowStyle.Render("[LOW]")
	default:
		return todoBadgeMediumStyle.Render("[" + strings.ToUpper(priority) + "]")
	}
}

// ── Parsing tool results into TodoItem slices ──────────────────────────────

// ParseTodoFromToolResult parses a todo_write/todo_read tool result into
// TodoItem entries. The result may be JSON (preferred) or markdown text in
// the format produced by tasktool.TaskList.Format(). Both shapes are
// supported so the renderer can handle results from any tool registration
// style.
//
// JSON shapes accepted:
//
//   - {"tasks": [ {id, content, status, priority}, ... ]}
//   - {"todos": [ ... ]}
//   - [ {id, content, status, priority}, ... ]
//
// Markdown shape accepted (matching tasktool.TaskList.Format):
//
//	=== Lista de Tarefas ===
//
//	[x] Setup inicial !!!
//	[>] Implementar auth
//	[ ] Documentar API
//	[-] Refatorar
func ParseTodoFromToolResult(result string) ([]TodoItem, error) {
	result = strings.TrimSpace(result)
	if result == "" {
		return nil, fmt.Errorf("resultado vazio")
	}

	// Try JSON first.
	if result[0] == '{' || result[0] == '[' {
		if items, err := parseTodoJSON(result); err == nil && len(items) > 0 {
			return items, nil
		}
	}

	// Fall back to markdown text format.
	return parseTodoMarkdown(result), nil
}

// parseTodoJSON handles the various JSON shapes supported by
// ParseTodoFromToolResult.
func parseTodoJSON(s string) ([]TodoItem, error) {
	// Try wrapping object first.
	var wrap struct {
		Tasks []rawTodo `json:"tasks"`
		Todos []rawTodo `json:"todos"`
		Items []rawTodo `json:"items"`
	}
	if err := json.Unmarshal([]byte(s), &wrap); err == nil {
		switch {
		case len(wrap.Tasks) > 0:
			return convertRawTodos(wrap.Tasks), nil
		case len(wrap.Todos) > 0:
			return convertRawTodos(wrap.Todos), nil
		case len(wrap.Items) > 0:
			return convertRawTodos(wrap.Items), nil
		}
	}

	// Try a bare array.
	var arr []rawTodo
	if err := json.Unmarshal([]byte(s), &arr); err == nil {
		return convertRawTodos(arr), nil
	}

	return nil, fmt.Errorf("nao foi possivel decodificar JSON de tarefas")
}

// rawTodo is the wire format used to decode tasks from JSON, accommodating
// both snake_case and camelCase keys.
type rawTodo struct {
	ID       string `json:"id"`
	TaskID   string `json:"task_id"`
	Content  string `json:"content"`
	Text     string `json:"text"`
	Status   string `json:"status"`
	Priority string `json:"priority"`
}

func convertRawTodos(raw []rawTodo) []TodoItem {
	out := make([]TodoItem, 0, len(raw))
	for _, r := range raw {
		id := r.ID
		if id == "" {
			id = r.TaskID
		}
		content := r.Content
		if content == "" {
			content = r.Text
		}
		if content == "" {
			continue
		}
		out = append(out, TodoItem{
			ID:       id,
			Content:  content,
			Status:   normalizeStatus(r.Status),
			Priority: normalizePriority(r.Priority),
		})
	}
	return out
}

// parseTodoMarkdown handles the format produced by tasktool.TaskList.Format
// and similar bullet-style listings. Lines that don't look like task entries
// are ignored.
func parseTodoMarkdown(s string) []TodoItem {
	var items []TodoItem
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		// Skip header lines like "=== Lista de Tarefas ===".
		if strings.HasPrefix(line, "===") {
			continue
		}

		status := ""
		var rest string
		switch {
		case strings.HasPrefix(line, "[x]") || strings.HasPrefix(line, "[X]"):
			status = "completed"
			rest = strings.TrimSpace(line[3:])
		case strings.HasPrefix(line, "[>]"):
			status = "in_progress"
			rest = strings.TrimSpace(line[3:])
		case strings.HasPrefix(line, "[-]"):
			status = "cancelled"
			rest = strings.TrimSpace(line[3:])
		case strings.HasPrefix(line, "[ ]"):
			status = "pending"
			rest = strings.TrimSpace(line[3:])
		default:
			continue
		}

		// Detect priority suffix markers used by tasktool.Format ("!!!", "-").
		priority := "medium"
		switch {
		case strings.HasSuffix(rest, " !!!"):
			priority = "high"
			rest = strings.TrimSuffix(rest, " !!!")
		case strings.HasSuffix(rest, " -"):
			priority = "low"
			rest = strings.TrimSuffix(rest, " -")
		}

		items = append(items, TodoItem{
			Content:  strings.TrimSpace(rest),
			Status:   status,
			Priority: priority,
		})
	}
	return items
}

// normalizeStatus maps a free-form status string to one of the canonical
// values FormatTodoList expects.
func normalizeStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "in_progress", "in progress", "doing", "active", "working":
		return "in_progress"
	case "completed", "done", "finished", "complete":
		return "completed"
	case "cancelled", "canceled", "skipped", "abandoned":
		return "cancelled"
	default:
		return "pending"
	}
}

// normalizePriority maps a free-form priority string to "high", "medium" or
// "low". Empty / unknown values default to "medium".
func normalizePriority(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "high", "h", "urgent", "critical":
		return "high"
	case "low", "l", "minor":
		return "low"
	default:
		return "medium"
	}
}

// IsTodoToolName reports whether the given tool name is one whose result
// should be rendered as a todo list. Used by the chat renderer to opt-in to
// FormatTodoList.
func IsTodoToolName(name string) bool {
	switch name {
	case "todo_write", "todo_read", "todowrite", "todoread", "task_list":
		return true
	}
	return false
}
