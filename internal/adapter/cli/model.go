package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/costa/polypod/internal/adapter"
)

// state represents the current mode of the CLI.
type state int

const (
	stateIdle      state = iota
	stateStreaming
)

// streamMsg carries a streaming chunk for Bubbletea.
type streamMsg struct {
	delta string
	done  bool
	err   error
}

// model is the Bubbletea model for the CLI.
type model struct {
	textarea textarea.Model
	viewport viewport.Model
	spinner  spinner.Model
	history  *inputHistory

	messages  []chatEntry
	streaming *strings.Builder
	streamCh  <-chan streamMsg
	state     state

	streamHandler adapter.StreamHandler
	cmdDeps       commandDeps
	ctx           context.Context

	width  int
	height int
	ready  bool
}

// chatEntry represents a single message in the chat display.
type chatEntry struct {
	role    string // "user", "assistant", "system", "error"
	content string
}

func newModel(ctx context.Context, streamHandler adapter.StreamHandler, deps commandDeps, dataDir string) model {
	ta := textarea.New()
	ta.Placeholder = "Digite sua mensagem... (Enter=enviar, Alt+Enter=nova linha)"
	ta.Focus()
	ta.CharLimit = 4096
	ta.SetHeight(3)
	ta.ShowLineNumbers = false

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))

	return model{
		textarea:      ta,
		spinner:       sp,
		history:       newInputHistory(dataDir),
		streaming:     &strings.Builder{},
		streamHandler: streamHandler,
		cmdDeps:       deps,
		ctx:           ctx,
		state:         stateIdle,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.spinner.Tick)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		headerHeight := 2
		inputHeight := 5
		statusHeight := 1

		viewportHeight := m.height - headerHeight - inputHeight - statusHeight
		if viewportHeight < 3 {
			viewportHeight = 3
		}

		if !m.ready {
			m.viewport = viewport.New(m.width, viewportHeight)
			m.viewport.SetContent(m.renderMessages())
			m.ready = true
		} else {
			m.viewport.Width = m.width
			m.viewport.Height = viewportHeight
		}

		m.textarea.SetWidth(m.width)
		return m, nil

	case tea.KeyMsg:
		switch {
		case msg.Type == tea.KeyCtrlC:
			return m, tea.Quit

		case msg.Type == tea.KeyUp && m.state == stateIdle:
			if prev, ok := m.history.Previous(); ok {
				m.textarea.SetValue(prev)
				m.textarea.CursorEnd()
			}
			return m, nil

		case msg.Type == tea.KeyDown && m.state == stateIdle:
			if next, ok := m.history.Next(); ok {
				m.textarea.SetValue(next)
				m.textarea.CursorEnd()
			}
			return m, nil

		case msg.Type == tea.KeyEnter && !msg.Alt && m.state == stateIdle:
			input := strings.TrimSpace(m.textarea.Value())
			if input == "" {
				return m, nil
			}

			m.textarea.SetValue("")
			m.textarea.Reset()
			m.history.Add(input)
			m.history.ResetCursor()

			// Check for local commands
			result := processCommand(input, m.cmdDeps)
			if result.quit {
				return m, tea.Quit
			}
			if result.handled {
				m.messages = append(m.messages, chatEntry{role: "system", content: result.output})
				m.updateViewport()
				return m, nil
			}

			// Send to AI with streaming
			m.messages = append(m.messages, chatEntry{role: "user", content: input})
			m.state = stateStreaming
			m.streaming.Reset()
			m.updateViewport()

			ch := m.launchStream(input)
			m.streamCh = ch
			return m, waitForStream(ch)
		}

	case streamMsg:
		if msg.err != nil {
			m.state = stateIdle
			m.messages = append(m.messages, chatEntry{role: "error", content: msg.err.Error()})
			m.streaming.Reset()
			m.streamCh = nil
			m.updateViewport()
			return m, nil
		}
		if msg.done {
			m.state = stateIdle
			content := m.streaming.String()
			if content != "" {
				m.messages = append(m.messages, chatEntry{role: "assistant", content: content})
			}
			m.streaming.Reset()
			m.streamCh = nil
			m.updateViewport()
			return m, nil
		}
		// Delta token
		m.streaming.WriteString(msg.delta)
		m.updateViewport()
		return m, waitForStream(m.streamCh)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	// Update textarea when idle
	if m.state == stateIdle {
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

// launchStream starts the streaming in a goroutine and returns a channel for results.
func (m *model) launchStream(input string) <-chan streamMsg {
	ch := make(chan streamMsg, 64)

	go func() {
		defer close(ch)

		msg := adapter.InMessage{
			ID:        fmt.Sprintf("cli-%d", time.Now().UnixNano()),
			Channel:   "cli",
			UserID:    "local",
			UserName:  "local",
			Text:      input,
			Timestamp: time.Now(),
		}

		chunks := make(chan adapter.StreamChunk, 64)
		go m.streamHandler(m.ctx, msg, chunks)

		for chunk := range chunks {
			if chunk.Error != nil {
				ch <- streamMsg{err: chunk.Error, done: true}
				return
			}
			if chunk.Done {
				ch <- streamMsg{done: true}
				return
			}
			ch <- streamMsg{delta: chunk.Delta}
		}
		// Channel closed without done signal
		ch <- streamMsg{done: true}
	}()

	return ch
}

// waitForStream returns a tea.Cmd that waits for the next stream message.
func waitForStream(ch <-chan streamMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return streamMsg{done: true}
		}
		return msg
	}
}

func (m *model) updateViewport() {
	if !m.ready {
		return
	}
	content := m.renderMessages()
	m.viewport.SetContent(content)
	m.viewport.GotoBottom()
}

func (m model) renderMessages() string {
	var b strings.Builder
	width := m.width
	if width <= 0 {
		width = 80
	}

	for _, entry := range m.messages {
		switch entry.role {
		case "user":
			b.WriteString(userStyle.Render("voce> "))
			b.WriteString(entry.content)
			b.WriteString("\n\n")
		case "assistant":
			b.WriteString(assistantStyle.Render("polypod> "))
			rendered := RenderMarkdown(entry.content, width-2)
			b.WriteString(rendered)
			b.WriteString("\n")
		case "system":
			b.WriteString(systemStyle.Render("sistema> "))
			b.WriteString(entry.content)
			b.WriteString("\n\n")
		case "error":
			b.WriteString(errorStyle.Render("erro> "))
			b.WriteString(entry.content)
			b.WriteString("\n\n")
		}
	}

	// Show streaming content in progress
	if m.state == stateStreaming {
		b.WriteString(assistantStyle.Render("polypod> "))
		if m.streaming.Len() > 0 {
			b.WriteString(m.streaming.String())
		}
		b.WriteString(dimStyle.Render("..."))
		b.WriteString("\n")
	}

	return b.String()
}

func (m model) View() string {
	if !m.ready {
		return "Inicializando..."
	}

	var b strings.Builder

	// Header
	header := headerStyle.Render("Polypod CLI")
	if m.cmdDeps.activeAgent != nil {
		agent := m.cmdDeps.activeAgent()
		if agent != "" {
			header += dimStyle.Render(fmt.Sprintf(" [%s]", agent))
		}
	}
	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", m.width))
	b.WriteString("\n")

	// Messages viewport
	b.WriteString(m.viewport.View())
	b.WriteString("\n")

	// Status bar
	status := ""
	if m.state == stateStreaming {
		status = m.spinner.View() + " Respondendo..."
	} else {
		status = dimStyle.Render("Enter=enviar | Alt+Enter=nova linha | /help | Ctrl+C=sair")
	}
	b.WriteString(statusBarStyle.Render(
		lipgloss.PlaceHorizontal(m.width, lipgloss.Left, status),
	))
	b.WriteString("\n")

	// Input
	b.WriteString(m.textarea.View())

	return b.String()
}
