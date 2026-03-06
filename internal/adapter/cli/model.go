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

const version = "0.3.0"

// streamMsg carries a streaming chunk for Bubbletea.
type streamMsg struct {
	delta string
	done  bool
	err   error
}

// notifyMsg triggers a timed notification in the status bar.
type notifyMsg struct {
	text  string
	level string // "info", "success", "warn", "error"
}

// clearNotifyMsg clears the notification after timeout.
type clearNotifyMsg struct{}

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

	// Elapsed time tracking
	streamStart time.Time

	// Notification system
	notification string
	notifyLevel  string

	// Tab completion
	tabIndex    int
	tabPrefix   string
	tabMatches  []string
}

// chatEntry represents a single message in the chat display.
type chatEntry struct {
	role      string // "user", "assistant", "system", "error"
	content   string
	timestamp time.Time
	elapsed   time.Duration // time to generate (assistant only)
}

func newModel(ctx context.Context, streamHandler adapter.StreamHandler, deps commandDeps, dataDir string) model {
	ta := textarea.New()
	ta.Placeholder = "Mensagem..."
	ta.Focus()
	ta.CharLimit = 4096
	ta.SetHeight(2)
	ta.ShowLineNumbers = false
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Base = lipgloss.NewStyle()
	ta.BlurredStyle.Base = lipgloss.NewStyle()
	ta.Cursor.Style = lipgloss.NewStyle().Foreground(colorCyan)

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = streamingSpinnerStyle

	return model{
		textarea:      ta,
		spinner:       sp,
		history:       newInputHistory(dataDir),
		streaming:     &strings.Builder{},
		streamHandler: streamHandler,
		cmdDeps:       deps,
		ctx:           ctx,
		state:         stateIdle,
		tabIndex:      -1,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.spinner.Tick)
}

// ── Slash command completions ───────────────────────────────────────────────

var slashCommands = []string{
	"/help", "/clear", "/quit", "/exit",
	"/agents", "/agent switch ",
	"/skills",
	"/memory list", "/memory search ",
	"/model", "/session",
}

func (m *model) resetTab() {
	m.tabIndex = -1
	m.tabPrefix = ""
	m.tabMatches = nil
}

func (m *model) tryTabComplete() bool {
	input := m.textarea.Value()
	if !strings.HasPrefix(input, "/") {
		return false
	}

	// First tab press — compute matches
	if m.tabIndex < 0 {
		m.tabPrefix = strings.ToLower(input)
		m.tabMatches = nil
		for _, cmd := range slashCommands {
			if strings.HasPrefix(cmd, m.tabPrefix) && cmd != m.tabPrefix {
				m.tabMatches = append(m.tabMatches, cmd)
			}
		}
		if len(m.tabMatches) == 0 {
			return false
		}
		m.tabIndex = 0
	} else {
		m.tabIndex = (m.tabIndex + 1) % len(m.tabMatches)
	}

	m.textarea.SetValue(m.tabMatches[m.tabIndex])
	m.textarea.CursorEnd()
	return true
}

// ── Update ──────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		headerHeight := 3 // logo + separator + blank
		inputHeight := 6  // border(1) + textarea(2) + border(1) + status(1) + pad(1)
		statusHeight := 0

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

		m.textarea.SetWidth(m.width - 4) // account for border padding
		return m, nil

	case tea.KeyMsg:
		switch {
		case msg.Type == tea.KeyCtrlC:
			return m, tea.Quit

		case msg.Type == tea.KeyTab && m.state == stateIdle:
			if m.tryTabComplete() {
				return m, nil
			}

		case msg.Type == tea.KeyUp && m.state == stateIdle:
			m.resetTab()
			if prev, ok := m.history.Previous(); ok {
				m.textarea.SetValue(prev)
				m.textarea.CursorEnd()
			}
			return m, nil

		case msg.Type == tea.KeyDown && m.state == stateIdle:
			m.resetTab()
			if next, ok := m.history.Next(); ok {
				m.textarea.SetValue(next)
				m.textarea.CursorEnd()
			}
			return m, nil

		case msg.Type == tea.KeyEsc && m.state == stateIdle:
			m.textarea.SetValue("")
			m.textarea.Reset()
			m.resetTab()
			return m, nil

		case msg.Type == tea.KeyEnter && !msg.Alt && m.state == stateIdle:
			m.resetTab()
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
				m.messages = append(m.messages, chatEntry{
					role:      "system",
					content:   result.output,
					timestamp: time.Now(),
				})
				m.updateViewport()
				return m, nil
			}

			// Send to AI with streaming
			m.messages = append(m.messages, chatEntry{
				role:      "user",
				content:   input,
				timestamp: time.Now(),
			})
			m.state = stateStreaming
			m.streamStart = time.Now()
			m.streaming.Reset()
			m.updateViewport()

			ch := m.launchStream(input)
			m.streamCh = ch
			return m, waitForStream(ch)

		default:
			// Any non-tab key resets tab completion
			if msg.Type != tea.KeyTab {
				m.resetTab()
			}
		}

	case streamMsg:
		if msg.err != nil {
			m.state = stateIdle
			m.messages = append(m.messages, chatEntry{
				role:      "error",
				content:   msg.err.Error(),
				timestamp: time.Now(),
			})
			m.streaming.Reset()
			m.streamCh = nil
			m.updateViewport()
			return m, nil
		}
		if msg.done {
			elapsed := time.Since(m.streamStart)
			m.state = stateIdle
			content := m.streaming.String()
			if content != "" {
				m.messages = append(m.messages, chatEntry{
					role:      "assistant",
					content:   content,
					timestamp: time.Now(),
					elapsed:   elapsed,
				})
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

	case clearNotifyMsg:
		m.notification = ""
		m.notifyLevel = ""
		return m, nil

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

// ── Stream launch ───────────────────────────────────────────────────────────

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
		ch <- streamMsg{done: true}
	}()

	return ch
}

func waitForStream(ch <-chan streamMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return streamMsg{done: true}
		}
		return msg
	}
}

// ── Viewport update ─────────────────────────────────────────────────────────

func (m *model) updateViewport() {
	if !m.ready {
		return
	}
	content := m.renderMessages()
	m.viewport.SetContent(content)
	m.viewport.GotoBottom()
}

// ── Render messages ─────────────────────────────────────────────────────────

func (m model) renderMessages() string {
	var b strings.Builder
	width := m.width
	if width <= 0 {
		width = 80
	}

	if len(m.messages) == 0 && m.state != stateStreaming {
		b.WriteString(m.renderWelcome())
		return b.String()
	}

	for i, entry := range m.messages {
		switch entry.role {
		case "user":
			b.WriteString(userStyle.Render("  > "))
			b.WriteString(contentStyle.Render(entry.content))
			b.WriteString("\n\n")

		case "assistant":
			rendered := RenderMarkdown(entry.content, width-4)
			// Indent the markdown block
			for _, line := range strings.Split(rendered, "\n") {
				b.WriteString("  ")
				b.WriteString(line)
				b.WriteString("\n")
			}
			// Show elapsed time
			if entry.elapsed > 0 {
				elapsed := formatDuration(entry.elapsed)
				b.WriteString("  ")
				b.WriteString(subtleStyle.Render(elapsed))
				b.WriteString("\n")
			}
			b.WriteString("\n")

		case "system":
			lines := strings.Split(entry.content, "\n")
			for _, line := range lines {
				b.WriteString("  ")
				b.WriteString(line)
				b.WriteString("\n")
			}
			b.WriteString("\n")

		case "error":
			b.WriteString("  ")
			b.WriteString(errorStyle.Render("erro "))
			b.WriteString(contentStyle.Render(entry.content))
			b.WriteString("\n\n")
		}

		// Subtle separator between message pairs
		if i < len(m.messages)-1 && entry.role == "assistant" {
			sep := separatorStyle.Render(strings.Repeat("─", width/2))
			b.WriteString("  ")
			b.WriteString(sep)
			b.WriteString("\n\n")
		}
	}

	// Show streaming content with cursor
	if m.state == stateStreaming {
		if m.streaming.Len() > 0 {
			rendered := m.streaming.String()
			for _, line := range strings.Split(rendered, "\n") {
				b.WriteString("  ")
				b.WriteString(contentStyle.Render(line))
				b.WriteString("\n")
			}
		}
		b.WriteString("  ")
		b.WriteString(streamingCursorStyle.Render("▍"))
		b.WriteString("\n")
	}

	return b.String()
}

// ── Welcome screen ──────────────────────────────────────────────────────────

func (m model) renderWelcome() string {
	var b strings.Builder

	b.WriteString("\n")

	logo := `    ____        __                      __
   / __ \____  / /_  ______  ____  ____/ /
  / /_/ / __ \/ / / / / __ \/ __ \/ __  /
 / ____/ /_/ / / /_/ / /_/ / /_/ / /_/ /
/_/    \____/_/\__, / .___/\____/\__,_/
              /____/_/`

	b.WriteString(welcomeTitleStyle.Render(logo))
	b.WriteString("\n\n")

	agent := "default"
	if m.cmdDeps.activeAgent != nil {
		if a := m.cmdDeps.activeAgent(); a != "" {
			agent = a
		}
	}

	info := welcomeSubStyle.Render(fmt.Sprintf("  agente: %s  |  v%s", agent, version))
	b.WriteString(info)
	b.WriteString("\n\n")

	// Hints
	hints := []struct{ key, desc string }{
		{"Enter", "enviar mensagem"},
		{"Tab", "completar comando"},
		{"Esc", "limpar input"},
		{"/help", "ver comandos"},
		{"Ctrl+C", "sair"},
	}

	b.WriteString(welcomeHintStyle.Render("  atalhos:"))
	b.WriteString("\n")
	for _, h := range hints {
		b.WriteString("    ")
		b.WriteString(welcomeKeyStyle.Render(h.key))
		b.WriteString(welcomeDescStyle.Render("  " + h.desc))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(welcomeHintStyle.Render("  Digite qualquer coisa para comecar..."))
	b.WriteString("\n")

	return b.String()
}

// ── View ────────────────────────────────────────────────────────────────────

func (m model) View() string {
	if !m.ready {
		return ""
	}

	var b strings.Builder

	// ── Header ──
	header := logoStyle.Render(" polypod")
	if m.cmdDeps.activeAgent != nil {
		agent := m.cmdDeps.activeAgent()
		if agent != "" {
			header += dimStyle.Render(" ") + headerAgentStyle.Render(agent)
		}
	}
	header += "  " + headerVersionStyle.Render("v"+version)
	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString(separatorStyle.Render(strings.Repeat("─", m.width)))
	b.WriteString("\n")

	// ── Messages viewport ──
	b.WriteString(m.viewport.View())
	b.WriteString("\n")

	// ── Status line ──
	if m.notification != "" {
		switch m.notifyLevel {
		case "success":
			b.WriteString(notifySuccessStyle.Render(" " + m.notification))
		case "warn":
			b.WriteString(notifyWarnStyle.Render(" " + m.notification))
		case "error":
			b.WriteString(notifyErrorStyle.Render(" " + m.notification))
		default:
			b.WriteString(notifyInfoStyle.Render(" " + m.notification))
		}
	} else if m.state == stateStreaming {
		elapsed := time.Since(m.streamStart)
		b.WriteString(" ")
		b.WriteString(m.spinner.View())
		b.WriteString(streamingDimStyle.Render(fmt.Sprintf(" pensando... %s", formatDuration(elapsed))))
	} else {
		// Show tab completion hints or default status
		if len(m.tabMatches) > 0 && m.tabIndex >= 0 {
			b.WriteString(statusBarStyle.Render(fmt.Sprintf(" Tab: %s (%d/%d)",
				m.tabMatches[m.tabIndex], m.tabIndex+1, len(m.tabMatches))))
		} else {
			b.WriteString(statusBarStyle.Render(" Enter enviar  Tab completar  /help comandos  Ctrl+C sair"))
		}
	}
	b.WriteString("\n")

	// ── Input with border ──
	inputContent := m.textarea.View()
	bordered := inputBorderActiveStyle.Width(m.width - 2).Render(inputContent)
	b.WriteString(bordered)

	return b.String()
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
