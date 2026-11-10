package cli

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/costa/polypod/internal/taskqueue"
)

// A /bg request creates a separate durable task. The live conversation turn
// remains in its own session and is never restarted or copied into the queue.
type backgroundWorkMsg struct {
	action string
	id     string
	task   taskqueue.Task
	tasks  []taskqueue.Task
	err    error
}

func parseBackgroundCommand(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	commandEnd := strings.IndexFunc(trimmed, unicode.IsSpace)
	if commandEnd < 0 {
		return "", strings.EqualFold(trimmed, "/bg")
	}
	if !strings.EqualFold(trimmed[:commandEnd], "/bg") {
		return "", false
	}
	return strings.TrimSpace(trimmed[commandEnd:]), true
}

func (m *model) submitBackgroundCommand(raw string) (bool, tea.Cmd) {
	argument, matched := parseBackgroundCommand(raw)
	if !matched {
		return false, nil
	}
	m.resetTab()
	m.history.Add(raw)
	m.history.ResetCursor()
	m.textarea.SetValue("")
	m.textarea.Reset()
	images := append([]string(nil), m.pastedImages...)
	m.pastedImages = nil
	m.messages = append(m.messages, chatEntry{role: "user", content: raw, timestamp: time.Now()})
	if m.taskQueue == nil {
		m.messages = append(m.messages, chatEntry{role: "error", content: "Fila de tarefas indisponível nesta sessão.", timestamp: time.Now()})
		m.updateViewport()
		return true, nil
	}
	fields := strings.Fields(argument)
	if len(fields) == 0 {
		m.messages = append(m.messages, chatEntry{role: "system", content: "Uso: /bg <objetivo> | /bg list | /bg status <id> | /bg retry <id> | /bg cancel <id>", timestamp: time.Now()})
		m.updateViewport()
		return true, nil
	}
	action := "create"
	value := argument
	switch strings.ToLower(fields[0]) {
	case "list":
		if len(fields) != 1 {
			m.backgroundUsage()
			return true, nil
		}
		action, value = "list", ""
	case "status", "retry", "cancel":
		if len(fields) != 2 {
			m.backgroundUsage()
			return true, nil
		}
		action, value = strings.ToLower(fields[0]), fields[1]
	}
	if action == "create" {
		value = backgroundPromptWithImages(value, images)
	}
	queue := m.taskQueue
	ctx := m.ctx
	id := taskqueue.NewTaskID()
	m.messages = append(m.messages, chatEntry{role: "system", content: "Consultando fila de tarefas…", timestamp: time.Now()})
	m.updateViewport()
	return true, func() tea.Msg {
		operationCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		result := backgroundWorkMsg{action: action, id: id}
		switch action {
		case "create":
			title := backgroundTitle(argument)
			result.task, result.err = queue.Create(operationCtx, taskqueue.CreateInput{
				ID: id, Title: title, Prompt: value, Channel: "cli_bg", Owner: "cli", MaxAttempts: 3,
			})
			if result.err != nil {
				// If the insert committed before an ambiguous error, the stable ID
				// lets us report the existing task without launching it twice.
				lookupCtx, lookupCancel := context.WithTimeout(ctx, 3*time.Second)
				defer lookupCancel()
				if existing, getErr := queue.Get(lookupCtx, id); getErr == nil &&
					existing.Channel == "cli_bg" && existing.Prompt == value {
					result.task, result.err = existing, nil
				}
			}
		case "list":
			result.tasks, result.err = queue.ListByChannel(operationCtx, "cli_bg", 20)
		case "status":
			result.task, result.err = backgroundTask(operationCtx, queue, value)
		case "retry":
			result.task, result.err = backgroundTask(operationCtx, queue, value)
			if result.err == nil {
				result.err = queue.Retry(operationCtx, value, 3)
				if result.err == nil {
					result.task, result.err = queue.Get(operationCtx, value)
				}
			}
		case "cancel":
			result.task, result.err = backgroundTask(operationCtx, queue, value)
			if result.err == nil {
				result.err = queue.Cancel(operationCtx, value)
				if result.err == nil {
					result.task, result.err = queue.Get(operationCtx, value)
				}
			}
		}
		return result
	}
}

func (m *model) backgroundUsage() {
	m.messages = append(m.messages, chatEntry{role: "system", content: "Uso: /bg <objetivo> | /bg list | /bg status <id> | /bg retry <id> | /bg cancel <id>", timestamp: time.Now()})
	m.updateViewport()
}

func backgroundTask(ctx context.Context, queue *taskqueue.Queue, id string) (taskqueue.Task, error) {
	task, err := queue.Get(ctx, id)
	if err != nil {
		return taskqueue.Task{}, err
	}
	if task.Channel != "cli_bg" {
		return taskqueue.Task{}, fmt.Errorf("%s não é uma tarefa criada pelo /bg", id)
	}
	return task, nil
}

func backgroundPromptWithImages(prompt string, images []string) string {
	if len(images) == 0 {
		return prompt
	}
	var b strings.Builder
	b.WriteString(prompt)
	b.WriteString("\n\n[imagens coladas nesta mensagem]\n")
	for _, path := range images {
		b.WriteString("  - ")
		b.WriteString(path)
		b.WriteByte('\n')
	}
	return b.String()
}

func backgroundTitle(objective string) string {
	title := strings.TrimSpace(strings.SplitN(objective, "\n", 2)[0])
	runes := []rune(title)
	if len(runes) > 100 {
		return string(runes[:100]) + "…"
	}
	return title
}

func (m *model) showBackgroundResult(msg backgroundWorkMsg) {
	// Remove the transient progress line, preserving messages that arrived
	// from the live stream while the SQLite operation was in flight.
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].role == "system" && m.messages[i].content == "Consultando fila de tarefas…" {
			m.messages = append(m.messages[:i], m.messages[i+1:]...)
			break
		}
	}
	if msg.err != nil {
		content := "Tarefa em segundo plano: " + msg.err.Error()
		if msg.action == "create" {
			content += fmt.Sprintf("\nA criação pode ter sido gravada. Confira /bg status %s antes de repetir o objetivo.", msg.id)
		}
		m.messages = append(m.messages, chatEntry{role: "error", content: content, timestamp: time.Now()})
		m.notification, m.notifyLevel = "Falha ao consultar a fila; veja o erro acima", "error"
		m.updateViewport()
		return
	}
	var content string
	switch msg.action {
	case "create":
		content = fmt.Sprintf("Tarefa %s registrada na fila (%s). Executa em sessão separada; resultado: /bg status %s", msg.task.ID, msg.task.Status, msg.task.ID)
	case "list":
		if len(msg.tasks) == 0 {
			content = "Nenhuma tarefa /bg recente."
		} else {
			var b strings.Builder
			b.WriteString("Tarefas /bg recentes:\n")
			for _, task := range msg.tasks {
				fmt.Fprintf(&b, "  %s  %-9s  %s\n", task.ID, task.Status, task.Title)
			}
			content = strings.TrimRight(b.String(), "\n")
		}
	case "status", "retry", "cancel":
		content = formatBackgroundTask(msg.task)
	}
	m.messages = append(m.messages, chatEntry{role: "system", content: content, timestamp: time.Now()})
	m.notification, m.notifyLevel = "Fila de tarefas atualizada", "success"
	m.updateViewport()
}

func formatBackgroundTask(task taskqueue.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Tarefa %s · %s · tentativa %d/%d\n%s", task.ID, task.Status, task.Attempts, task.MaxAttempts, task.Title)
	if task.LastError != "" {
		b.WriteString("\nErro: ")
		b.WriteString(task.LastError)
	} else if task.Result != "" {
		b.WriteString("\nResultado:\n")
		b.WriteString(task.Result)
	}
	return b.String()
}
