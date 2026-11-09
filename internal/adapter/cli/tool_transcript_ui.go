package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/costa/polypod/internal/session"
)

// formatToolTranscript shows completed observations from a resumed session.
// These entries are display-only; they never become executable tool calls.
func formatToolTranscript(records []session.ToolTraceRecord) string {
	var lines []string
	for _, record := range records {
		if record.Phase != session.ToolTraceComplete && record.Phase != session.ToolTraceError {
			continue
		}
		status := "concluída"
		preview := record.ResultPreview
		if record.Phase == session.ToolTraceError {
			status = "falhou"
			preview = record.ErrorPreview
		}
		line := fmt.Sprintf("%s · %s · %s", record.At.Local().Format("15:04:05"), record.ToolName, status)
		if preview != "" {
			line += ": " + preview
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > 8 {
		lines = lines[len(lines)-8:]
	}
	return "Ferramentas registradas nesta sessão (histórico, sem reexecução):\n- " + strings.Join(lines, "\n- ")
}

func (a *Adapter) appendTranscriptHistory(id string, entries []chatEntry) []chatEntry {
	if a.toolTranscript == nil || id == "" {
		return entries
	}
	records, err := a.toolTranscript.Load(id, 32)
	if err != nil {
		a.logger.Warn("tool transcript unavailable on resume", "session_id", id, "error", err)
		return entries
	}
	if summary := formatToolTranscript(records); summary != "" {
		entries = append(entries, chatEntry{role: "system", content: summary, timestamp: time.Now()})
	}
	return entries
}
