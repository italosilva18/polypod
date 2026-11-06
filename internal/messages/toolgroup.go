package messages

// Tool-event grouping for cleaner TUI display.
//
// This file provides smart grouping of consecutive tool calls in a stream
// so the TUI can render "Lido 5 arquivos: a, b, c, …" instead of 5
// separate read_file blocks. It operates on adapter.ToolEventInfo and is
// independent from the Message-level collapsing in collapse.go.

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/costa/polypod/internal/adapter"
)

// GroupKind classifies a ToolGroup. GroupSingle means "no grouping at all";
// the other kinds collapse consecutive similar tool calls.
type GroupKind int

const (
	GroupSingle      GroupKind = iota // single tool call, rendered normally
	GroupReadBatch                    // multiple read_file / glob / grep
	GroupSearchBatch                  // multiple grep / glob / search
	GroupEditBatch                    // multiple file_edit / file_write
	GroupBashBatch                    // multiple bash commands
	GroupMixed                        // 2-3 different tools related to same file
)

// ToolGroup represents a collapsed group of similar tool calls.
// A group always contains at least one ToolEventComplete / ToolEventError
// event (plus optional Start events). Tools inside a group are kept in
// original order.
type ToolGroup struct {
	Kind      GroupKind
	Tools     []adapter.ToolEventInfo
	TotalTime time.Duration
	Summary   string
}

// ── Tool-name classification ────────────────────────────────────────────────

// toolCategory buckets a tool name into the broad category used for
// grouping decisions. Unknown tools fall into "" (no category) and are
// treated as GroupSingle.
func toolCategory(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "read") || strings.Contains(n, "view") || strings.Contains(n, "cat"):
		return "read"
	case strings.Contains(n, "grep") || strings.Contains(n, "search") || strings.Contains(n, "glob") || strings.Contains(n, "find"):
		return "search"
	case strings.Contains(n, "edit") || strings.Contains(n, "write") || strings.Contains(n, "patch") ||
		strings.Contains(n, "str_replace") || strings.Contains(n, "create_file") || strings.Contains(n, "undo_edit"):
		return "edit"
	case strings.Contains(n, "bash") || strings.Contains(n, "shell") || strings.Contains(n, "exec") || strings.Contains(n, "run_command"):
		return "bash"
	}
	return ""
}

// categoryToKind converts a tool category string into a batch GroupKind.
func categoryToKind(cat string) GroupKind {
	switch cat {
	case "read":
		return GroupReadBatch
	case "search":
		return GroupSearchBatch
	case "edit":
		return GroupEditBatch
	case "bash":
		return GroupBashBatch
	}
	return GroupSingle
}

// ── Event normalization ────────────────────────────────────────────────────

// terminalEvents returns only the "terminal" events of interest for
// display grouping — Complete and Error — one per ToolID. We dedupe by
// ToolID because GroupToolEvents is called on a running slice that
// contains both Start and Complete events for the same call.
func terminalEvents(events []adapter.ToolEventInfo) []adapter.ToolEventInfo {
	seen := make(map[string]bool, len(events))
	out := make([]adapter.ToolEventInfo, 0, len(events))
	for _, ev := range events {
		if ev.Type != adapter.ToolEventComplete && ev.Type != adapter.ToolEventError {
			continue
		}
		if ev.ToolID != "" {
			if seen[ev.ToolID] {
				continue
			}
			seen[ev.ToolID] = true
		}
		out = append(out, ev)
	}
	return out
}

// ── Main grouping ──────────────────────────────────────────────────────────

// GroupToolEvents takes a sequence of tool events and groups similar
// consecutive ones. Returns a list of groups, each containing 1+ tools.
//
// Grouping rules:
//  1. 2+ consecutive read_file / view / cat calls → GroupReadBatch
//  2. 2+ consecutive grep / glob / search calls   → GroupSearchBatch
//  3. 2+ consecutive file_edit / file_write calls → GroupEditBatch
//  4. 2+ consecutive bash / shell calls           → GroupBashBatch
//  5. 2-3 different tools all referring to the same file → GroupMixed
//  6. Everything else → GroupSingle (no grouping)
func GroupToolEvents(events []adapter.ToolEventInfo) []ToolGroup {
	terminals := terminalEvents(events)
	if len(terminals) == 0 {
		return nil
	}

	var groups []ToolGroup
	i := 0
	for i < len(terminals) {
		ev := terminals[i]
		cat := toolCategory(ev.ToolName)

		// Try to extend a same-category run.
		if cat != "" {
			j := i + 1
			for j < len(terminals) && toolCategory(terminals[j].ToolName) == cat {
				j++
			}
			if j-i >= 2 {
				run := terminals[i:j]
				groups = append(groups, buildBatchGroup(categoryToKind(cat), run))
				i = j
				continue
			}
		}

		// Try GroupMixed: 2-3 consecutive events on the same file path
		// using different tool names (e.g. read_file → file_edit).
		if mixed, n := tryMixedGroup(terminals[i:]); n > 0 {
			groups = append(groups, mixed)
			i += n
			continue
		}

		// Fallback: single group.
		groups = append(groups, ToolGroup{
			Kind:      GroupSingle,
			Tools:     []adapter.ToolEventInfo{ev},
			TotalTime: ev.Elapsed,
			Summary:   singleSummary(ev),
		})
		i++
	}

	return groups
}

// tryMixedGroup looks at the next 2-3 events and, if they all touch the
// same file path but use different tool names, returns a GroupMixed. Otherwise
// returns (_, 0).
func tryMixedGroup(run []adapter.ToolEventInfo) (ToolGroup, int) {
	if len(run) < 2 {
		return ToolGroup{}, 0
	}
	first := extractToolPath(run[0])
	if first == "" {
		return ToolGroup{}, 0
	}
	count := 1
	names := map[string]bool{run[0].ToolName: true}
	for k := 1; k < len(run) && k < 3; k++ {
		if extractToolPath(run[k]) != first {
			break
		}
		names[run[k].ToolName] = true
		count++
	}
	if count < 2 || len(names) < 2 {
		return ToolGroup{}, 0
	}
	tools := run[:count]
	var total time.Duration
	for _, t := range tools {
		total += t.Elapsed
	}
	return ToolGroup{
		Kind:      GroupMixed,
		Tools:     tools,
		TotalTime: total,
		Summary:   fmt.Sprintf("%d operações em %s", count, filepath.Base(first)),
	}, count
}

func buildBatchGroup(kind GroupKind, tools []adapter.ToolEventInfo) ToolGroup {
	var total time.Duration
	for _, t := range tools {
		total += t.Elapsed
	}
	g := ToolGroup{
		Kind:      kind,
		Tools:     tools,
		TotalTime: total,
	}
	g.Summary = batchSummary(kind, tools, total)
	return g
}

// ── Summary text ───────────────────────────────────────────────────────────

func singleSummary(ev adapter.ToolEventInfo) string {
	path := extractToolPath(ev)
	if path != "" {
		return fmt.Sprintf("%s: %s", ev.ToolName, filepath.Base(path))
	}
	if cmd, ok := ev.Args["command"].(string); ok && cmd != "" {
		return fmt.Sprintf("%s: %s", ev.ToolName, Truncate(cmd, 60))
	}
	return ev.ToolName
}

func batchSummary(kind GroupKind, tools []adapter.ToolEventInfo, total time.Duration) string {
	n := len(tools)
	switch kind {
	case GroupReadBatch:
		names := collectBaseNames(tools, 4)
		if len(names) > 0 {
			return fmt.Sprintf("Lido %d arquivos: %s (%s)", n, strings.Join(names, ", "), formatGroupDuration(total))
		}
		return fmt.Sprintf("Lido %d arquivos (%s)", n, formatGroupDuration(total))

	case GroupSearchBatch:
		return fmt.Sprintf("Buscou %d padrões (%s)", n, formatGroupDuration(total))

	case GroupEditBatch:
		names := collectBaseNames(tools, 4)
		if len(names) > 0 {
			return fmt.Sprintf("Editado %d arquivos: %s (%s)", n, strings.Join(names, ", "), formatGroupDuration(total))
		}
		return fmt.Sprintf("Editado %d arquivos (%s)", n, formatGroupDuration(total))

	case GroupBashBatch:
		return fmt.Sprintf("Executado %d comandos (%s)", n, formatGroupDuration(total))
	}
	return fmt.Sprintf("%d operações (%s)", n, formatGroupDuration(total))
}

// collectBaseNames returns up to max unique basenames of file paths
// extracted from the tools. Duplicates are skipped.
func collectBaseNames(tools []adapter.ToolEventInfo, max int) []string {
	seen := make(map[string]bool)
	var out []string
	for _, t := range tools {
		p := extractToolPath(t)
		if p == "" {
			continue
		}
		base := filepath.Base(p)
		if seen[base] {
			continue
		}
		seen[base] = true
		out = append(out, base)
		if len(out) >= max {
			// Indicate more when there are additional unique names.
			if len(tools) > max {
				out = append(out, "…")
			}
			break
		}
	}
	return out
}

// extractToolPath pulls a file path out of a ToolEventInfo's Args, trying
// several common key names used across tools.
func extractToolPath(ev adapter.ToolEventInfo) string {
	if ev.Args == nil {
		return ""
	}
	keys := []string{"path", "file_path", "filepath", "filename", "file", "target", "pattern"}
	for _, k := range keys {
		if v, ok := ev.Args[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// extractCommand pulls a shell command string from a bash-like tool's args.
func extractCommand(ev adapter.ToolEventInfo) string {
	if ev.Args == nil {
		return ""
	}
	for _, k := range []string{"command", "cmd", "script"} {
		if v, ok := ev.Args[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// ── Rendering ──────────────────────────────────────────────────────────────

// FormatGroup renders a group for display. The output is plain text with
// a leading indent ("  ") and a trailing newline. When verbose is true the
// group is expanded into one FormatToolBadge line per tool.
//
//   - Single tools       → one FormatToolBadge line
//   - Read batches       → "▶ Lido 5 arquivos: a, b, c, d (0.4s)"
//   - Search batches     → "▶ Buscou 3 padrões (0.5s)"
//   - Edit batches       → "▶ Editado 4 arquivos: a, b, c (1.2s)"
//   - Bash batches       → "▶ Executado 3 comandos (1.8s):" + list
//   - Mixed              → "▶ 3 operações em main.go (0.3s)"
func FormatGroup(g ToolGroup, verbose bool) string {
	if verbose || g.Kind == GroupSingle || len(g.Tools) == 0 {
		var b strings.Builder
		for _, t := range g.Tools {
			b.WriteString(FormatToolBadge(t))
			b.WriteString("\n")
		}
		return b.String()
	}

	var b strings.Builder
	b.WriteString("  ▶ ")
	b.WriteString(g.Summary)

	// Bash batches list each command on its own indented line.
	if g.Kind == GroupBashBatch {
		b.WriteString(":\n")
		for _, t := range g.Tools {
			cmd := extractCommand(t)
			if cmd == "" {
				cmd = t.ToolName
			}
			b.WriteString("    - ")
			b.WriteString(Truncate(cmd, 100))
			b.WriteString("\n")
		}
		return b.String()
	}

	b.WriteString("\n")
	return b.String()
}

// FormatToolBadge returns a compact, single-line status badge for one
// tool event, including elapsed time when available.
//
// Examples:
//
//	"  ✓ read_file: main.go (0.1s)"
//	"  ✗ bash: go build (1.2s)"
//	"  ▶ running bash…"
func FormatToolBadge(ev adapter.ToolEventInfo) string {
	var mark, tail string
	switch ev.Type {
	case adapter.ToolEventStart, adapter.ToolEventProgress:
		mark = "▶"
		tail = "running " + ev.ToolName + "…"
		return fmt.Sprintf("  %s %s", mark, tail)
	case adapter.ToolEventComplete:
		mark = "✓"
	case adapter.ToolEventError:
		mark = "✗"
	default:
		mark = "•"
	}

	label := ev.ToolName
	if path := extractToolPath(ev); path != "" {
		label = fmt.Sprintf("%s: %s", ev.ToolName, filepath.Base(path))
	} else if cmd := extractCommand(ev); cmd != "" {
		label = fmt.Sprintf("%s: %s", ev.ToolName, Truncate(cmd, 60))
	}

	if ev.Elapsed > 0 {
		return fmt.Sprintf("  %s %s (%s)", mark, label, formatGroupDuration(ev.Elapsed))
	}
	return fmt.Sprintf("  %s %s", mark, label)
}

// ShouldCollapse reports whether the given sequence should be visually
// collapsed. Current heuristic: more than 3 consecutive calls share the
// same tool category.
func ShouldCollapse(events []adapter.ToolEventInfo) bool {
	terminals := terminalEvents(events)
	if len(terminals) < 4 {
		return false
	}
	var prevCat string
	run := 0
	for _, ev := range terminals {
		cat := toolCategory(ev.ToolName)
		if cat != "" && cat == prevCat {
			run++
			if run >= 3 { // 3 additional after the first = 4 in a row
				return true
			}
		} else {
			run = 0
			prevCat = cat
		}
	}
	return false
}

// ── Helpers ────────────────────────────────────────────────────────────────

func formatGroupDuration(d time.Duration) string {
	if d <= 0 {
		return "0ms"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
