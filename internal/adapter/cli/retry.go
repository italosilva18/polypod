package cli

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/costa/polypod/internal/retryux"
)

// retryTurn retains the exact composer input for one failed AI turn. The image
// paths remain valid for this CLI session and are added again on resubmission.
type retryTurn struct {
	raw    string
	images []string
}

var retryHTTPStatusPattern = regexp.MustCompile(`(?i)\b(?:status(?:\s+code)?|http)\s*:?\s*([45][0-9]{2})\b`)

func retryHTTPStatus(err error) int {
	if err == nil {
		return 0
	}
	match := retryHTTPStatusPattern.FindStringSubmatch(err.Error())
	if len(match) < 2 {
		return 0
	}
	code, parseErr := strconv.Atoi(match[1])
	if parseErr != nil {
		return 0
	}
	return code
}

func retryFailureHint(err error) string {
	status := retryHTTPStatus(err)
	_, retryable, _ := retryux.ClassifyError(err, status)
	if retryable {
		return "\n/retry reenvia esta mensagem."
	}
	if status != 0 {
		return fmt.Sprintf("\nHTTP %d. Corrija a causa e use /retry para reenviar.", status)
	}
	return "\nCorrija a causa e use /retry para reenviar."
}

func (m model) retryFailedTurn() (tea.Model, tea.Cmd) {
	m.textarea.SetValue("")
	m.textarea.Reset()
	if m.failedTurn == nil {
		m.messages = append(m.messages, chatEntry{
			role:      "system",
			content:   "Não há uma mensagem com falha para reenviar.",
			timestamp: time.Now(),
		})
		m.updateViewport()
		return m, m.startQueuedCmd()
	}
	turn := *m.failedTurn
	m.failedTurn = nil
	m.textarea.SetValue(turn.raw)
	m.pastedImages = append([]string(nil), turn.images...)
	// Use the same path as Enter so command expansion, history and streaming
	// behave exactly as they do for a fresh submission.
	return m.update(tea.KeyMsg{Type: tea.KeyEnter})
}

func isRetryCommand(input string) bool {
	return strings.EqualFold(strings.TrimSpace(input), "/retry")
}
