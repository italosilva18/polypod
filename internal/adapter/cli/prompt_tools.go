package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type editorResultMsg struct {
	content string
	err     error
}

type sideResultMsg struct {
	answer string
	err    error
}

func (m *model) openPromptEditor() tea.Cmd {
	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if editor == "" {
		editor = "vi"
	}
	parts := strings.Fields(editor)
	if len(parts) == 0 {
		m.notification = "editor externo não configurado"
		m.notifyLevel = "error"
		return nil
	}
	f, err := os.CreateTemp("", "polypod-prompt-*.md")
	if err != nil {
		m.notification = "não foi possível abrir o editor: " + err.Error()
		m.notifyLevel = "error"
		return nil
	}
	path := f.Name()
	if _, err := f.WriteString(m.textarea.Value()); err != nil {
		f.Close()
		os.Remove(path)
		m.notification = "não foi possível preparar o editor: " + err.Error()
		m.notifyLevel = "error"
		return nil
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		m.notification = "não foi possível preparar o editor: " + err.Error()
		m.notifyLevel = "error"
		return nil
	}
	args := append(parts[1:], path)
	return tea.ExecProcess(exec.Command(parts[0], args...), func(err error) tea.Msg {
		defer os.Remove(path)
		if err != nil {
			return editorResultMsg{err: err}
		}
		content, readErr := os.ReadFile(path)
		return editorResultMsg{content: string(content), err: readErr}
	})
}

func (m *model) askSideQuestion(question string) tea.Cmd {
	if m.cmdDeps.generateSideText == nil {
		m.notification = "pergunta rápida indisponível"
		m.notifyLevel = "error"
		return nil
	}
	var context strings.Builder
	start := max(0, len(m.messages)-12)
	for _, entry := range m.messages[start:] {
		if entry.role != "user" && entry.role != "assistant" {
			continue
		}
		content := []rune(entry.content)
		if len(content) > 1800 {
			content = content[:1800]
		}
		fmt.Fprintf(&context, "%s: %s\n", entry.role, string(content))
	}
	prompt := "Responda à pergunta rápida em português brasileiro usando o contexto abaixo. " +
		"Não execute ferramentas nem altere arquivos. Esta resposta não faz parte da conversa principal.\n\n" +
		context.String() + "\nPergunta: " + question
	generate := m.cmdDeps.generateSideText
	m.sideAnswer = "Consultando..."
	m.sideScroll = 0
	return func() tea.Msg {
		answer, err := generate(prompt)
		return sideResultMsg{answer: answer, err: err}
	}
}

func (m model) sideAnswerView() string {
	width := max(20, min(m.width-4, 100))
	lines := strings.Split(ansi.Wrap(m.sideAnswer, max(12, width-8), ""), "\n")
	visible := max(3, m.height-9)
	start := min(m.sideScroll, max(0, len(lines)-visible))
	end := min(len(lines), start+visible)
	content := "Pergunta rápida\n\n" + strings.Join(lines[start:end], "\n") + "\n\n↑↓ rolar  Esc/Enter fechar"
	body := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2).
		Width(width).Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, body)
}
