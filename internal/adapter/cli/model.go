package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/costa/polypod/internal/adapter"
	"github.com/costa/polypod/internal/inputprocessing"
	"github.com/costa/polypod/internal/mentions"
	"github.com/costa/polypod/internal/messages"
	"github.com/costa/polypod/internal/permissions"
	"github.com/costa/polypod/internal/sliceansi"
	"github.com/costa/polypod/internal/taskqueue"
	"github.com/costa/polypod/internal/tasks"
	"github.com/costa/polypod/internal/tool"
)

// state represents the current mode of the CLI.
type state int

const (
	stateIdle state = iota
	stateStreaming
)

const version = "0.6.0"

// streamMsg carries a streaming chunk for Bubbletea.
type streamMsg struct {
	delta     string
	done      bool
	err       error
	toolEvent *adapter.ToolEventInfo // non-nil when this carries a tool event
}

// notifyMsg triggers a timed notification in the status bar.
type notifyMsg struct {
	text  string
	level string // "info", "success", "warn", "error"
}

// clearNotifyMsg clears a notification only if it is still the active one.
type clearNotifyMsg struct{ text string }

// queuedInputMsg starts the next user submission after a stream completes.
type queuedInputMsg struct{}

// streamRedrawMsg groups frequent token deltas into one viewport render.
// Tool events, questions, errors and turn completion still render immediately.
type streamRedrawMsg struct{ generation uint64 }

const streamRedrawInterval = 33 * time.Millisecond

type voiceResultMsg struct {
	text       string
	err        error
	generation uint64
}

type voicePartialMsg struct {
	text       string
	ok         bool
	source     <-chan string
	generation uint64
}

type taskEventMsg struct{ event tasks.TaskEvent }

type queuedInput struct {
	raw    string
	images []string
}

const historyPageSize = 60

// toolEventMsg carries a tool lifecycle event to the Bubbletea update loop.
type toolEventMsg struct {
	event adapter.ToolEventInfo
}

// userQuestionMsg carries a question from the AI ask_user tool to the TUI.
type userQuestionMsg struct {
	question tool.UserQuestion
}

// approvalRequestMsg carries a permission approval request from the
// enforcer (via BubbleteaApprovalHandler) into the Bubbletea update loop.
type approvalRequestMsg struct {
	id  string
	req permissions.ApprovalRequest
}

// statusTickMsg is delivered roughly every 100ms while a tool is running,
// so the rich status bar's spinner animation can advance. We stop
// scheduling it as soon as the active tool clears.
type statusTickMsg struct{}

// statusTickInterval controls how frequently the status bar spinner ticks
// while a tool is active.
const statusTickInterval = 100 * time.Millisecond

// scheduleStatusTick returns a tea.Cmd that emits a statusTickMsg after
// statusTickInterval. Used by the model to drive the active-tool spinner.
func scheduleStatusTick() tea.Cmd {
	return tea.Tick(statusTickInterval, func(time.Time) tea.Msg {
		return statusTickMsg{}
	})
}

// model is the Bubbletea model for the CLI.
type model struct {
	textarea textarea.Model
	viewport viewport.Model
	spinner  spinner.Model
	history  *inputHistory

	messages               []chatEntry
	streaming              *strings.Builder
	streamCh               <-chan streamMsg
	state                  state
	queued                 []queuedInput
	voiceRecording         bool
	voiceStopping          bool
	voicePartialText       string
	voicePartials          <-chan string
	voiceCancel            context.CancelFunc
	voiceAbort             context.CancelFunc
	voiceGeneration        uint64
	streamRedrawPending    bool
	streamRedrawGeneration uint64
	lastViewportRedraw     time.Time
	// Ctrl+B only hides the active stream. /bg detach separately records that
	// live turn and routes new prompts to independent durable queue sessions.
	streamHidden     bool
	parallelDetached bool
	detachedTaskID   string
	historyStart     int
	historyExpanded  bool
	historySessionID string
	historyBefore    int
	// historyRetain is the persisted index where the anchor page begins.
	// Pages prepended below it can be dropped on return to the bottom.
	// historyLoadFailed stops reads after a manifest revision conflict.
	historyRetain     int
	historyLoadFailed bool
	activeTurn        *retryTurn
	failedTurn        *retryTurn
	// /recover fills the composer only after the user asks for it. It never
	// starts a model turn or reissues a tool call by itself.
	recoveryDraft string
	// An ask_user prompt temporarily owns the composer. Preserve any draft that
	// the user was writing while the current turn streamed.
	heldDraft      string
	heldImages     []string
	heldDraftValid bool

	streamHandler adapter.StreamHandler
	cmdDeps       commandDeps
	ctx           context.Context
	taskEvents    <-chan tasks.TaskEvent
	taskRegistry  *tasks.TaskRegistry
	taskQueue     *taskqueue.Queue

	width  int
	height int
	ready  bool

	// Elapsed time tracking
	streamStart time.Time

	// Notification system
	notification string
	notifyLevel  string

	// Tab completion
	tabIndex   int
	tabPrefix  string
	tabMatches []string

	// Ask-user question/answer flow
	userPromptHandler    *tool.UserPromptHandler
	waitingForUserAnswer bool
	pendingQuestion      *tool.UserQuestion

	// Tool event display
	toolEvents []adapter.ToolEventInfo

	// sessionState holds runtime state mutated by slash commands
	// (/files, /add-dir). Shared with cmdDeps.sessionState.
	sessionState *cliSessionState

	// Tool approval modal flow.
	// approvalHandler is optional: when nil, the model does not listen
	// for approval requests and never shows the modal.
	approvalHandler   *BubbleteaApprovalHandler
	currentApproval   *approvalRequestMsg // nil when no pending approval
	approvalSelection int                 // 0=yes, 1=no, 2=always, 3=deny-always

	// dataDir is the polypod data directory used for persisting history,
	// sessions and pasted images.
	dataDir string

	// pastedImages tracks image file paths saved during the current
	// input composition. When the input is submitted, these are added
	// as an addendum so the AI can read them via file tools.
	pastedImages []string

	// fileIndex backs tab-completion for @-mentions. Optional: when
	// nil, @-completion is disabled and only slash-command completion
	// remains.
	fileIndex *mentions.FileIndex

	// tabMode tracks whether the current tab-completion cycle is for
	// slash commands or @-mentions, so subsequent Tab presses cycle
	// within the same set.
	tabMode tabCompletionMode

	// mentionAnchor is the byte offset of the '@' that started the
	// current mention-completion cycle within the textarea value. When
	// we accept a completion, we replace text[anchor:] with the new
	// mention.
	mentionAnchor int

	// statusBar is the rich, Claude-Code-style status bar rendered at
	// the very bottom of the screen. It tracks mode, tokens, cost,
	// session info, and the currently active tool with a spinner
	// animation.
	statusBar *StatusBar

	// sessionStart is when the TUI was launched; used by the status
	// bar to display an "uptime" duration.
	sessionStart time.Time

	// tickerRunning reports whether the status-bar spinner ticker is
	// currently scheduled. We only keep it running while a tool is
	// active to avoid a perpetual 100ms event loop.
	tickerRunning bool

	// configForm, when non-nil, is the active interactive /config
	// overlay. While set, it owns the keyboard.
	configForm *configFormModel

	// historySearch, when non-nil, is the active Ctrl+R fuzzy history
	// search overlay. While set, it owns the keyboard.
	historySearch *historySearchModel

	// tokenWarning is the most recently computed banner snapshot for
	// context-window usage. nil (or Level == WarningNone) means no
	// banner is currently displayed.
	tokenWarning *TokenWarning

	// progressLines tracks live progress for tools currently running
	// (or recently completed). It is rendered above the streaming
	// response while a turn is in flight.
	progressLines *ProgressLineModel

	// progressTickerRunning reports whether the per-tool spinner
	// ticker is currently scheduled. Mirrors tickerRunning but for the
	// progressLines display.
	progressTickerRunning bool

	// quickOpen, when non-nil, is the active Ctrl+P fuzzy file
	// picker overlay. While set, it owns the keyboard. When the user
	// confirms a selection, the chosen path is inserted into the
	// textarea as an "@path" mention so the AI can resolve it.
	quickOpen *quickOpenModel

	// modelPicker, when non-nil, is the active interactive model
	// picker overlay. While set, it owns the keyboard. When the user
	// confirms a selection, the deps.setModel callback is invoked to
	// swap the AI client's model in place.
	modelPicker *modelPickerModel

	// toolRenderers maps tool names to custom rendering functions.
	// Populated with DefaultRenderers() at construction time.
	toolRenderers *ToolRendererRegistry
}

// progressTickMsg fires roughly every 100ms while progressLines has
// active tools, advancing each per-tool spinner frame.
type progressTickMsg struct{}

// progressTickInterval controls how frequently progress-line spinners tick.
const progressTickInterval = 100 * time.Millisecond

// scheduleProgressTick returns a tea.Cmd that emits a progressTickMsg
// after progressTickInterval.
func scheduleProgressTick() tea.Cmd {
	return tea.Tick(progressTickInterval, func(time.Time) tea.Msg {
		return progressTickMsg{}
	})
}

// tabCompletionMode distinguishes between the different completion
// sources the Tab key can cycle through.
type tabCompletionMode int

const (
	tabModeNone tabCompletionMode = iota
	tabModeSlash
	tabModeMention
)

// chatEntry represents a single message in the chat display.
type chatEntry struct {
	role      string // "user", "assistant", "system", "error"
	content   string
	timestamp time.Time
	elapsed   time.Duration // time to generate (assistant only)
	// Entries are immutable after insertion. Cache their expensive Markdown
	// and wrapping at the current terminal width; paging preserves these fields.
	renderedWidth int
	renderedBody  string
	// loadedFromSession marks an immutable copy of one persisted transcript
	// message. persistedIndex is its zero-based index. Notices, unsaved
	// turns, streaming results, and ask_user text leave the flag false.
	loadedFromSession bool
	persistedIndex    int
}

func newModel(ctx context.Context, streamHandler adapter.StreamHandler, deps commandDeps, dataDir string, userPrompt *tool.UserPromptHandler, approvalHandler *BubbleteaApprovalHandler, fileIndex *mentions.FileIndex) model {
	ta := textarea.New()
	ta.Placeholder = "Mensagem..."
	ta.Prompt = ""
	ta.Focus()
	ta.CharLimit = 0 // Bubbles uses zero for no character limit.
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Base = lipgloss.NewStyle()
	ta.BlurredStyle.Base = lipgloss.NewStyle()
	ta.Cursor.Style = lipgloss.NewStyle().Foreground(colorCyan)

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = streamingSpinnerStyle

	// Session-local state for /files and /add-dir. Owned by the model
	// and referenced from commandDeps so handlers can mutate it.
	state := newCLISessionState()
	deps.sessionState = state

	// Rich status bar. Initial state is populated from whatever the
	// adapter can report right now (agent name, mode, etc.); the bar
	// is then refreshed after each AI turn and on tool events.
	sb := NewStatusBar()
	sessStart := time.Now()
	initState := StatusBarState{
		Visible:      true,
		Mode:         "edit",
		SessionStart: sessStart,
	}
	if deps.activeAgent != nil {
		initState.Agent = deps.activeAgent()
	}
	if deps.planCurrent != nil {
		if mode := deps.planCurrent(); mode != "" {
			initState.Mode = mode
		}
	}
	if deps.currentModel != nil {
		initState.Model = deps.currentModel()
	}
	if deps.sessionInfo != nil {
		if id, _, err := deps.sessionInfo(); err == nil {
			initState.SessionID = id
		}
	}
	if deps.contextTokens != nil {
		initState.TokensUsed, initState.TokensMax = deps.contextTokens()
	}
	if deps.sessionCost != nil {
		initState.Cost = deps.sessionCost()
	}
	sb.Update(initState)

	// Initial token warning derived from whatever the deps already
	// expose. ComputeTokenWarning returns WarningNone for zero/unknown
	// counts so the banner stays hidden until real data flows in.
	tw := ComputeTokenWarning(initState.TokensUsed, initState.TokensMax)

	return model{
		textarea:          ta,
		spinner:           sp,
		history:           newInputHistory(dataDir),
		streaming:         &strings.Builder{},
		streamHandler:     streamHandler,
		cmdDeps:           deps,
		ctx:               ctx,
		state:             stateIdle,
		tabIndex:          -1,
		tabMode:           tabModeNone,
		userPromptHandler: userPrompt,
		sessionState:      state,
		approvalHandler:   approvalHandler,
		dataDir:           dataDir,
		fileIndex:         fileIndex,
		statusBar:         sb,
		sessionStart:      sessStart,
		tokenWarning:      &tw,
		progressLines:     NewProgressLineModel(),
		toolRenderers:     DefaultRenderers(),
	}
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{textarea.Blink, m.spinner.Tick}
	if m.userPromptHandler != nil {
		cmds = append(cmds, waitForUserQuestion(m.userPromptHandler))
	}
	if m.approvalHandler != nil {
		cmds = append(cmds, waitForApprovalRequest(m.approvalHandler))
	}
	if m.taskEvents != nil {
		cmds = append(cmds, waitForTaskEvent(m.ctx, m.taskEvents))
	}
	return tea.Batch(cmds...)
}

func waitForTaskEvent(ctx context.Context, events <-chan tasks.TaskEvent) tea.Cmd {
	return func() tea.Msg {
		select {
		case event, ok := <-events:
			if !ok {
				return nil
			}
			return taskEventMsg{event: event}
		case <-ctx.Done():
			return nil
		}
	}
}

// waitForUserQuestion returns a tea.Cmd that blocks on the handler's
// QuestionCh and delivers the question as a userQuestionMsg.
func waitForUserQuestion(h *tool.UserPromptHandler) tea.Cmd {
	return func() tea.Msg {
		q, ok := <-h.QuestionCh
		if !ok {
			return nil
		}
		return userQuestionMsg{question: q}
	}
}

// waitForApprovalRequest returns a tea.Cmd that blocks on the approval
// handler's request channel and delivers the request as an
// approvalRequestMsg.
func waitForApprovalRequest(h *BubbleteaApprovalHandler) tea.Cmd {
	return func() tea.Msg {
		req, ok := <-h.RequestChannel()
		if !ok {
			return nil
		}
		return approvalRequestMsg{id: req.id, req: req.req}
	}
}

// ── Slash command completions ───────────────────────────────────────────────

var slashCommands = []string{
	"/help", "/clear", "/quit", "/exit", "/bg",
	"/agents", "/agent switch ",
	"/skills",
	"/memory list", "/memory search ",
	"/model", "/session", "/sessions", "/sessions search ",
	"/copy", "/voice", "/voice ", "/run ", "/file ", "/search ",
	"/git status", "/git log", "/git diff", "/git branch",
	"/project", "/export ", "/context", "/cost",
	"/init", "/init --force",
	"/commit", "/commit ",
	"/diff", "/diff --staged", "/diff ",
	"/compact", "/retry",
	"/doctor",
	"/mcp", "/mcp list", "/mcp status ", "/mcp tools", "/mcp tools ", "/mcp reconnect ",
	"/hooks", "/hooks list", "/hooks enable ", "/hooks disable ", "/hooks show ",
	"/files",
	"/add-dir ", "/add-dir list", "/add-dir remove ",
	"/config", "/config get ", "/config set ", "/config save", "/config reload",
	"/resume", "/resume ", "/recover",
	"/rewind", "/rewind ",
	"/permissions", "/permissions list",
	"/permissions allow ", "/permissions deny ", "/permissions ask ",
	"/permissions remove ", "/permissions reset",
	"/plan", "/plan on", "/plan off", "/plan show", "/plan execute",
	"/review", "/review --staged", "/review --pr", "/review ",
	"/security-review", "/security-review --staged", "/security-review --full", "/security-review ",
	"/effort", "/effort low", "/effort medium", "/effort high", "/effort max",
	"/branch", "/branch tree",
	"/branch new ", "/branch switch ", "/branch delete ",
	"/usage", "/usage ",
	"/init-verifiers",
}

func (m *model) resetTab() {
	m.tabIndex = -1
	m.tabPrefix = ""
	m.tabMatches = nil
	m.tabMode = tabModeNone
	m.mentionAnchor = 0
}

// tryTabComplete routes Tab presses to either slash-command completion
// (when the input starts with '/') or @-mention completion (when the
// input contains a @token). Returns true if a completion was applied.
func (m *model) tryTabComplete() bool {
	input := m.textarea.Value()

	// Continue an active cycle: advance to the next match.
	if m.tabIndex >= 0 && len(m.tabMatches) > 0 {
		m.tabIndex = (m.tabIndex + 1) % len(m.tabMatches)
		return m.applyTabMatch()
	}

	// Slash-command completion has priority when input starts with '/'.
	if strings.HasPrefix(input, "/") {
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
		m.tabMode = tabModeSlash
		return m.applyTabMatch()
	}

	// Otherwise try @-mention completion.
	return m.tryMentionTabComplete(input)
}

// tryMentionTabComplete attempts to complete a @-mention at the end of
// the input. It locates the nearest unclosed '@' token, extracts the
// typed prefix, queries the file index, and populates tabMatches.
func (m *model) tryMentionTabComplete(input string) bool {
	if m.fileIndex == nil || input == "" {
		return false
	}

	// Walk left from the end of the input to find a '@' that starts a
	// mention token. Stop at whitespace (which cannot appear inside a
	// path) or at the beginning of the string.
	anchor := -1
	end := len(input)
	for i := end - 1; i >= 0; i-- {
		c := input[i]
		if c == '@' {
			// Valid only at start of string or after a boundary char.
			if i == 0 || isMentionBoundary(input[i-1]) {
				anchor = i
			}
			break
		}
		if c == ' ' || c == '\t' || c == '\n' {
			return false
		}
	}
	if anchor < 0 {
		return false
	}

	prefix := input[anchor+1 : end]
	matches := m.fileIndex.FuzzyComplete(prefix)
	if len(matches) == 0 {
		return false
	}

	m.tabPrefix = prefix
	m.tabMatches = matches
	m.tabIndex = 0
	m.tabMode = tabModeMention
	m.mentionAnchor = anchor
	return m.applyTabMatch()
}

// applyTabMatch writes the current tab match back into the textarea,
// handling both slash-command and @-mention modes.
func (m *model) applyTabMatch() bool {
	if m.tabIndex < 0 || m.tabIndex >= len(m.tabMatches) {
		return false
	}
	match := m.tabMatches[m.tabIndex]

	switch m.tabMode {
	case tabModeSlash:
		m.textarea.SetValue(match)
		m.textarea.CursorEnd()
	case tabModeMention:
		input := m.textarea.Value()
		if m.mentionAnchor < 0 || m.mentionAnchor > len(input) {
			return false
		}
		// Replace from the '@' through the end of the current token
		// (stopping at whitespace) with "@" + match.
		endCol := m.mentionAnchor + 1
		for endCol < len(input) && input[endCol] != ' ' && input[endCol] != '\t' && input[endCol] != '\n' {
			endCol++
		}
		newVal := input[:m.mentionAnchor] + "@" + match + input[endCol:]
		m.textarea.SetValue(newVal)
		m.textarea.CursorEnd()
	default:
		return false
	}
	return true
}

// isMentionBoundary reports whether b is a character that can precede
// a '@' and still allow it to be treated as the start of a file
// mention (as opposed to part of a word like an email address).
func isMentionBoundary(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '(', '[', '{', '>', ',', ';':
		return true
	}
	return false
}

// ── Update ──────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	_, isKey := msg.(tea.KeyMsg)
	_, isWindowSize := msg.(tea.WindowSizeMsg)
	if !isKey && !isWindowSize {
		return m.update(msg)
	}
	modalWasOpen := m.currentApproval != nil || m.configForm != nil || m.historySearch != nil ||
		m.quickOpen != nil || m.modelPicker != nil
	preExpanded := m.ready && isKey &&
		m.currentApproval == nil && m.configForm == nil && m.historySearch == nil &&
		m.quickOpen == nil && m.modelPicker == nil &&
		(m.state == stateIdle || m.state == stateStreaming || m.waitingForUserAnswer)
	if preExpanded {
		// Give the textarea room before it handles the key. Otherwise its
		// private viewport scrolls first and keeps the draft's top hidden
		// even after the composer grows.
		m.textarea.SetHeight(m.maxComposerHeight())
	}
	next, cmd := m.update(msg)
	updated, ok := next.(model)
	if !ok {
		return next, cmd
	}
	if preExpanded || isWindowSize {
		updated.resizeComposer()
	}
	if modalWasOpen {
		return updated, tea.Batch(cmd, updated.startQueuedCmd())
	}
	return updated, cmd
}

func (m model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = max(1, msg.Width)
		m.height = max(1, msg.Height)
		m.textarea.SetWidth(max(1, m.width-4))

		viewportHeight := m.viewportHeight()

		if !m.ready {
			m.viewport = viewport.New(m.width, viewportHeight)
			m.ready = true
			m.viewport.SetContent(m.renderMessages())
			if len(m.messages) > 0 {
				m.viewport.GotoBottom()
			}
		} else {
			atBottom := m.viewport.AtBottom()
			m.viewport.Width = m.width
			m.viewport.Height = viewportHeight
			m.viewport.SetContent(m.renderMessages())
			if atBottom {
				m.viewport.GotoBottom()
			}
		}

		if m.progressLines != nil {
			m.progressLines.SetWidth(m.width)
		}
		return m, nil

	case tea.MouseMsg:
		// Pass mouse events (scroll wheel) to viewport
		wasAtTop := m.viewport.AtTop()
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		if wasAtTop && msg.Button == tea.MouseButtonWheelUp {
			m.prependHistoryPage()
		} else if msg.Button == tea.MouseButtonWheelDown && m.viewport.AtBottom() {
			m.historyExpanded = false
			m.updateViewport()
		}
		return m, cmd

	case tea.KeyMsg:
		// When a modal approval is active, the modal owns the keyboard.
		// Only Ctrl+C can escape (quits the program).
		if m.currentApproval != nil {
			if msg.Type == tea.KeyCtrlC {
				return m, tea.Quit
			}
			return m.handleApprovalKey(msg)
		}

		// When the /config form is open it owns the keyboard. Only
		// Ctrl+C can quit the program.
		if m.configForm != nil {
			if msg.Type == tea.KeyCtrlC {
				return m, tea.Quit
			}
			return m.handleConfigFormKey(msg)
		}

		// When the Ctrl+R history search is open it owns the keyboard
		// (except Ctrl+C which always quits).
		if m.historySearch != nil {
			if msg.Type == tea.KeyCtrlC {
				return m, tea.Quit
			}
			return m.handleHistorySearchKey(msg)
		}

		// When the Ctrl+P quick-open file picker is active it owns
		// the keyboard. Ctrl+C still always quits the program.
		if m.quickOpen != nil {
			if msg.Type == tea.KeyCtrlC {
				return m, tea.Quit
			}
			return m.handleQuickOpenKey(msg)
		}

		// When the model picker is active it owns the keyboard.
		// Ctrl+C still always quits the program.
		if m.modelPicker != nil {
			if msg.Type == tea.KeyCtrlC {
				return m, tea.Quit
			}
			return m.handleModelPickerKey(msg)
		}

		// ── Paste detection ──────────────────────────────────────────
		// Bracketed-paste bursts arrive as KeyRunes with Paste=true.
		// Non-bracketed terminals can still deliver large bursts as a
		// single KeyRunes event with many runes — treat that as a paste.
		if (m.state == stateIdle || m.state == stateStreaming) && msg.Type == tea.KeyRunes &&
			(msg.Paste || len(msg.Runes) >= pasteThreshold) {
			handled, pcmd := m.handlePasteBurst(string(msg.Runes))
			if handled {
				return m, pcmd
			}
		}

		// ── Explicit newline insertion ───────────────────────────────
		// Alt+Enter and Ctrl+J both insert a literal newline into the
		// textarea without submitting. This gives users a reliable way
		// to compose multi-line input even when Enter alone would
		// submit because the surrounding context is "complete".
		if (m.state == stateIdle || m.state == stateStreaming) && !m.waitingForUserAnswer {
			if (msg.Type == tea.KeyEnter && msg.Alt) || msg.Type == tea.KeyCtrlJ {
				m.resetTab()
				m.textarea.InsertString("\n")
				return m, nil
			}
		}

		switch {
		case msg.Type == tea.KeyCtrlC:
			return m, tea.Quit

		case msg.Type == tea.KeyCtrlG:
			return handleKeyCtrlG(&m)

		case msg.Type == tea.KeyCtrlB && m.state == stateStreaming && !m.waitingForUserAnswer:
			return handleKeyCtrlB(&m)

		case msg.Type == tea.KeyPgUp:
			wasAtTop := m.viewport.AtTop()
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			if wasAtTop {
				m.prependHistoryPage()
			}
			return m, cmd

		case msg.Type == tea.KeyPgDown:
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			if m.viewport.AtBottom() {
				m.historyExpanded = false
				m.updateViewport()
			}
			return m, cmd

		case msg.Type == tea.KeyCtrlR && m.state == stateIdle:
			// Open the fuzzy history search overlay.
			if m.history != nil {
				m.historySearch = newHistorySearch(m.history.entries)
				m.historySearch.SetSize(m.width, m.height)
			}
			return m, nil

		case msg.Type == tea.KeyCtrlP && m.state == stateIdle:
			// Open the Quick Open file picker overlay. Rooted at the
			// current working directory; falls back silently if the
			// process cwd cannot be resolved.
			m.openQuickOpen()
			return m, nil

		case msg.Type == tea.KeyCtrlY && m.state == stateIdle:
			// Open the model picker overlay. The /model slash command
			// can also reach this code path via the openModelPicker
			// flag on commandResult.
			m.openModelPicker()
			return m, nil

		case msg.Type == tea.KeyTab && (m.state == stateIdle || m.state == stateStreaming):
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

		case msg.Type == tea.KeyCtrlL && m.state == stateIdle:
			m.messages = nil
			m.resetHistoryPaging()
			m.updateViewport()
			return m, nil

		case msg.Type == tea.KeyEsc && m.state == stateIdle:
			m.textarea.SetValue("")
			m.textarea.Reset()
			m.resetTab()
			return m, nil

		case msg.Type == tea.KeyEnter && !msg.Alt && m.waitingForUserAnswer:
			// User is answering an ask_user question
			input := strings.TrimSpace(m.textarea.Value())
			if input == "" {
				return m, nil
			}
			m.textarea.SetValue("")
			m.textarea.Reset()

			// Display the user's answer in the chat
			m.messages = append(m.messages, chatEntry{
				role:      "user",
				content:   input,
				timestamp: time.Now(),
			})

			// Send the answer back to the blocking tool goroutine
			m.waitingForUserAnswer = false
			m.pendingQuestion = nil
			if m.userPromptHandler != nil {
				m.userPromptHandler.AnswerCh <- input
			}
			m.textarea.Placeholder = "Mensagem..."
			if m.heldDraftValid {
				m.textarea.SetValue(m.heldDraft)
				m.textarea.CursorEnd()
				m.pastedImages = m.heldImages
				m.heldDraft, m.heldImages, m.heldDraftValid = "", nil, false
			}
			m.updateViewport()
			// Re-listen for the next question
			var nextCmd tea.Cmd
			if m.userPromptHandler != nil {
				nextCmd = waitForUserQuestion(m.userPromptHandler)
			}
			return m, tea.Batch(nextCmd, m.startQueuedCmd())

		case msg.Type == tea.KeyEnter && !msg.Alt && m.state == stateStreaming:
			m.resetTab()
			raw := m.textarea.Value()
			if strings.TrimSpace(raw) == "" {
				return m, nil
			}
			if !ShouldSubmitOnEnter(raw) {
				m.textarea.InsertString("\n")
				return m, nil
			}
			if handled, cmd := m.submitBackgroundCommand(raw); handled {
				return m, cmd
			}
			if m.parallelDetached && !strings.HasPrefix(strings.TrimSpace(raw), "/") && !strings.HasPrefix(strings.TrimSpace(raw), "!") {
				if handled, cmd := m.submitParallelPrompt(raw); handled {
					return m, cmd
				}
			}
			if strings.TrimSpace(raw) == "/recover" {
				m.notification = "Aguarde o turno atual terminar para usar /recover; o comando permanece no campo."
				m.notifyLevel = "info"
				m.updateViewport()
				return m, clearNotifyAfter(6*time.Second, m.notification)
			}
			m.queued = append(m.queued, queuedInput{
				raw: raw, images: append([]string(nil), m.pastedImages...),
			})
			m.pastedImages = nil
			m.textarea.SetValue("")
			m.textarea.Reset()
			m.notification = "Mensagem aguardando este turno; /bg <objetivo> cria uma tarefa separada"
			m.notifyLevel = "info"
			m.updateViewport()
			return m, clearNotifyAfter(6*time.Second, m.notification)

		case msg.Type == tea.KeyEnter && !msg.Alt && m.state == stateIdle:
			m.resetTab()
			raw := m.textarea.Value()

			// Empty Enter is a no-op: don't submit, don't newline.
			if strings.TrimSpace(raw) == "" {
				return m, nil
			}

			// If the input is an incomplete code block / unclosed
			// bracket / trailing backslash, treat Enter as newline
			// insertion instead of submission. This lets users paste
			// or type multi-line snippets naturally.
			if !ShouldSubmitOnEnter(raw) {
				m.textarea.InsertString("\n")
				return m, nil
			}
			if handled, cmd := m.submitBackgroundCommand(raw); handled {
				return m, cmd
			}
			if isRetryCommand(raw) {
				return m.retryFailedTurn()
			}
			if strings.TrimSpace(raw) == "/recover" {
				m.history.Add("/recover")
				m.history.ResetCursor()
				if m.recoveryDraft == "" {
					m.textarea.SetValue("")
					m.messages = append(m.messages, chatEntry{role: "system", content: "Nenhum turno interrompido desta sessão está marcado para recuperação.", timestamp: time.Now()})
				} else {
					draft := m.recoveryDraft
					if m.heldDraftValid {
						// Preserve an ask_user draft if the modal handed control
						// back between the command and this recovery request.
						if strings.TrimSpace(m.heldDraft) != "" {
							draft = m.heldDraft + "\n\n" + draft
						}
						m.pastedImages = append(m.pastedImages, m.heldImages...)
						m.heldDraft, m.heldImages, m.heldDraftValid = "", nil, false
					}
					m.textarea.SetValue(draft)
					m.textarea.CursorEnd()
					m.messages = append(m.messages, chatEntry{role: "system", content: "Rascunho de continuidade no campo de mensagem. Revise antes de enviar; nada foi executado automaticamente.", timestamp: time.Now()})
				}
				m.updateViewport()
				return m, nil
			}

			input := strings.TrimSpace(raw)
			turnImages := append([]string(nil), m.pastedImages...)

			// Augment the submitted input with any pasted-image
			// references collected while composing. The reference is
			// a short pointer to the file path so the AI can open it
			// with read_file / file tools.
			if len(m.pastedImages) > 0 {
				var b strings.Builder
				b.WriteString(input)
				b.WriteString("\n\n")
				b.WriteString("[imagens coladas nesta mensagem]\n")
				for _, p := range m.pastedImages {
					b.WriteString("  - ")
					b.WriteString(p)
					b.WriteString("\n")
				}
				input = b.String()
				m.pastedImages = nil
			}

			m.textarea.SetValue("")
			m.textarea.Reset()
			m.history.Add(input)
			m.history.ResetCursor()

			// Check for local commands
			result := processCommand(input, m.cmdDeps, m.messages)
			modelInput := input
			if result.modelInput != "" {
				modelInput = result.modelInput
			}
			if result.quit {
				return m, tea.Quit
			}
			if result.handled {
				if result.voiceStart && m.voiceRecording {
					m.messages = append(m.messages, chatEntry{role: "system", content: "Uma gravação já está em andamento.", timestamp: time.Now()})
					m.updateViewport()
					return m, nil
				}
				if result.voiceStop {
					return m.stopVoiceCapture()
				}
				if result.clearDisplay {
					m.invalidateVoiceCapture()
					m.messages = nil
					m.recoveryDraft = ""
					m.resetHistoryPaging()
				}
				if result.replaceMessages != nil {
					m.invalidateVoiceCapture()
					m.messages = append([]chatEntry{}, result.replaceMessages...)
					m.recoveryDraft = result.recoveryDraft
					m.adoptHistoryPaging(result.resumeSessionID, result.historyBefore)
					m.viewport.GotoBottom()
				}
				if result.output != "" {
					m.messages = append(m.messages, chatEntry{
						role:      "system",
						content:   result.output,
						timestamp: time.Now(),
					})
				}
				if result.openConfigForm {
					m.openConfigForm()
				}
				if result.openModelPicker {
					m.openModelPicker()
				}
				if result.voiceStart {
					duration := time.Duration(result.voiceSeconds) * time.Second
					return m.beginVoiceCapture(duration, false)
				}
				m.updateViewport()
				return m, nil
			}

			// Send to AI with streaming. The chat view keeps the raw
			// input so the user sees their original @mentions, but
			// the AI receives the expanded version with file/dir
			// contents inlined as an appendix.
			m.messages = append(m.messages, chatEntry{
				role:      "user",
				content:   input,
				timestamp: time.Now(),
			})
			m.recoveryDraft = ""
			m.state = stateStreaming
			m.streamHidden = false
			m.activeTurn = &retryTurn{raw: raw, images: turnImages}
			m.streamStart = time.Now()
			m.streaming.Reset()
			m.toolEvents = nil // clear tool events for new query
			if m.progressLines != nil {
				m.progressLines.Reset()
			}
			m.updateViewport()

			expanded := m.expandMentionsForAI(modelInput)
			ch := m.launchStream(expanded)
			m.streamCh = ch
			return m, waitForStream(ch)

		default:
			// Any non-tab key resets tab completion
			if msg.Type != tea.KeyTab {
				m.resetTab()
			}
		}

	case voiceResultMsg:
		if !m.voiceRecording || msg.generation != m.voiceGeneration {
			return m, nil
		}
		m.voiceRecording = false
		m.voiceStopping = false
		m.voicePartialText = ""
		m.voicePartials = nil
		if m.voiceCancel != nil {
			m.voiceCancel()
			m.voiceCancel = nil
		}
		if m.voiceAbort != nil {
			m.voiceAbort()
			m.voiceAbort = nil
		}
		if msg.err != nil {
			m.messages = append(m.messages, chatEntry{role: "error", content: "Entrada por voz: " + msg.err.Error(), timestamp: time.Now()})
			m.updateViewport()
			return m, nil
		}
		transcript := strings.TrimSpace(msg.text)
		if transcript == "" {
			m.messages = append(m.messages, chatEntry{role: "system", content: "Nenhuma fala foi detectada.", timestamp: time.Now()})
			m.updateViewport()
			return m, nil
		}
		m.textarea.InsertString(transcript)
		m.resizeComposer()
		m.notification = "Transcrição inserida. Revise e pressione Enter para enviar."
		m.notifyLevel = "success"
		m.updateViewport()
		return m, clearNotifyAfter(6*time.Second, m.notification)

	case voicePartialMsg:
		if !msg.ok || !m.voiceRecording || msg.source != m.voicePartials || msg.generation != m.voiceGeneration {
			return m, nil
		}
		m.voicePartialText = strings.TrimSpace(msg.text)
		m.updateViewport()
		return m, waitForVoicePartial(m.voicePartials, m.voiceGeneration)

	case streamMsg:
		if msg.err != nil {
			m.finishDetachedTurn(tasks.StatusFailed, m.streaming.String(), msg.err.Error())
			m.state = stateIdle
			wasHidden := m.streamHidden
			m.streamHidden = false
			m.failedTurn = m.activeTurn
			m.activeTurn = nil
			m.messages = append(m.messages, chatEntry{
				role:      "error",
				content:   msg.err.Error() + retryFailureHint(msg.err),
				timestamp: time.Now(),
			})
			if wasHidden {
				m.notification = "turno minimizado falhou; use /retry após corrigir a causa"
				m.notifyLevel = "error"
			}
			m.streaming.Reset()
			m.streamCh = nil
			if m.statusBar != nil {
				m.statusBar.UpdateActiveTool("")
			}
			m.refreshStatusBar()
			m.refreshTokenWarning()
			m.updateViewport()
			if wasHidden {
				return m, tea.Batch(m.startQueuedCmd(), clearNotifyAfter(6*time.Second, m.notification))
			}
			return m, m.startQueuedCmd()
		}
		// Handle tool event carried by streamMsg
		if msg.toolEvent != nil {
			m.toolEvents = append(m.toolEvents, *msg.toolEvent)
			m.recordFileFromToolEvent(msg.toolEvent)
			m.feedProgressLine(msg.toolEvent)
			// Update the rich status bar's active-tool indicator and
			// start the spinner ticker if we were idle.
			tickCmd := m.refreshActiveToolIndicator()
			progCmd := m.refreshProgressTicker()
			m.updateViewport()
			cmds := []tea.Cmd{waitForStream(m.streamCh)}
			if tickCmd != nil {
				cmds = append(cmds, tickCmd)
			}
			if progCmd != nil {
				cmds = append(cmds, progCmd)
			}
			return m, tea.Batch(cmds...)
		}
		if msg.done {
			elapsed := time.Since(m.streamStart)
			content := m.streaming.String()
			m.finishDetachedTurn(tasks.StatusCompleted, content, "")
			m.state = stateIdle
			wasHidden := m.streamHidden
			m.streamHidden = false
			m.activeTurn = nil
			if content != "" {
				m.messages = append(m.messages, chatEntry{
					role:      "assistant",
					content:   content,
					timestamp: time.Now(),
					elapsed:   elapsed,
				})
			}
			m.streaming.Reset()
			if wasHidden {
				m.notification = "turno minimizado concluído"
				m.notifyLevel = "success"
			}
			m.streamCh = nil
			// The turn is done: clear any stale active-tool indicator
			// and refresh the status bar from the latest cost/token
			// counters.
			if m.statusBar != nil {
				m.statusBar.UpdateActiveTool("")
			}
			m.refreshStatusBar()
			m.refreshTokenWarning()
			m.updateViewport()
			if wasHidden {
				return m, tea.Batch(m.startQueuedCmd(), clearNotifyAfter(6*time.Second, m.notification))
			}
			return m, m.startQueuedCmd()
		}
		// Delta token
		m.streaming.WriteString(msg.delta)
		redraw := m.queueStreamRedraw()
		if redraw == nil {
			return m, waitForStream(m.streamCh)
		}
		return m, tea.Batch(waitForStream(m.streamCh), redraw)

	case streamRedrawMsg:
		if !m.streamRedrawPending || msg.generation != m.streamRedrawGeneration {
			return m, nil
		}
		m.updateViewport()
		return m, nil

	case toolEventMsg:
		m.toolEvents = append(m.toolEvents, msg.event)
		m.recordFileFromToolEvent(&msg.event)
		m.feedProgressLine(&msg.event)
		tickCmd := m.refreshActiveToolIndicator()
		progCmd := m.refreshProgressTicker()
		m.updateViewport()
		cmds := []tea.Cmd{}
		if tickCmd != nil {
			cmds = append(cmds, tickCmd)
		}
		if progCmd != nil {
			cmds = append(cmds, progCmd)
		}
		return m, tea.Batch(cmds...)

	case progressTickMsg:
		// Advance per-tool spinner frames. Stop rescheduling once no
		// tools are active anymore.
		if m.progressLines == nil {
			m.progressTickerRunning = false
			return m, nil
		}
		m.progressLines.Tick()
		m.progressLines.Cleanup()
		if m.progressLines.ActiveCount() == 0 {
			m.progressTickerRunning = false
			m.updateViewport()
			return m, nil
		}
		m.updateViewport()
		return m, scheduleProgressTick()

	case statusTickMsg:
		// Advance the status-bar spinner frame while a tool is active.
		// Stop rescheduling once the active tool clears so we do not
		// drive a perpetual 100ms event loop.
		if m.statusBar == nil {
			m.tickerRunning = false
			return m, nil
		}
		active := m.activeToolName()
		if active == "" {
			m.statusBar.UpdateActiveTool("")
			m.tickerRunning = false
			return m, nil
		}
		m.statusBar.UpdateActiveTool(active)
		m.statusBar.Tick()
		return m, scheduleStatusTick()

	case userQuestionMsg:
		// The AI's ask_user tool sent a question — display it and switch to answer mode
		q := msg.question
		if !m.waitingForUserAnswer && (m.textarea.Value() != "" || len(m.pastedImages) > 0) {
			m.heldDraft = m.textarea.Value()
			m.heldImages = append([]string(nil), m.pastedImages...)
			m.heldDraftValid = true
			m.textarea.SetValue("")
			m.textarea.Reset()
			m.pastedImages = nil
		}
		m.waitingForUserAnswer = true
		m.pendingQuestion = &q

		// Build the question display as a system message
		var qb strings.Builder
		if q.Header != "" {
			qb.WriteString(q.Header)
			qb.WriteString("\n\n")
		}
		qb.WriteString(q.Question)
		if len(q.Options) > 0 {
			qb.WriteString("\n")
			for i, opt := range q.Options {
				qb.WriteString(fmt.Sprintf("\n  %d. %s", i+1, opt.Label))
				if opt.Description != "" {
					qb.WriteString(fmt.Sprintf(" — %s", opt.Description))
				}
			}
		}

		m.messages = append(m.messages, chatEntry{
			role:      "system",
			content:   qb.String(),
			timestamp: time.Now(),
		})
		m.textarea.Placeholder = "[resposta] >"
		m.updateViewport()
		return m, nil

	case approvalRequestMsg:
		// New approval request arrived from the enforcer. Display the
		// modal overlay and take focus away from the textarea.
		req := msg
		m.currentApproval = &req
		m.approvalSelection = 0
		m.updateViewport()
		// Re-arm the listener so we pick up the next request as soon
		// as this one is resolved.
		var nextCmd tea.Cmd
		if m.approvalHandler != nil {
			nextCmd = waitForApprovalRequest(m.approvalHandler)
		}
		return m, nextCmd

	case clearNotifyMsg:
		if msg.text == m.notification {
			m.notification = ""
			m.notifyLevel = ""
		}
		return m, nil

	case notifyMsg:
		m.notification = msg.text
		m.notifyLevel = msg.level
		return m, clearNotifyAfter(6*time.Second, m.notification)

	case taskEventMsg:
		state := msg.event.State
		if state.Type == tasks.TaskTypeCLITurn {
			// The live stream already renders this terminal result in the chat.
			return m, waitForTaskEvent(m.ctx, m.taskEvents)
		}
		level := "warn"
		verb := "interrompida"
		switch state.Status {
		case tasks.StatusCompleted:
			level, verb = "success", "concluída"
		case tasks.StatusFailed:
			level, verb = "error", "falhou"
		}
		message := fmt.Sprintf("Tarefa %s %s: %s", state.ID, verb, state.Description)
		if state.Status == tasks.StatusFailed && state.Error != "" {
			message += fmt.Sprintf(" (%s)", state.Error)
		}
		m.messages = append(m.messages, chatEntry{
			role:      "system",
			content:   message + "\nDetalhes: /task-status " + state.ID,
			timestamp: time.Now(),
		})
		m.notification = message
		m.notifyLevel = level
		m.updateViewport()
		return m, tea.Batch(waitForTaskEvent(m.ctx, m.taskEvents), clearNotifyAfter(6*time.Second, m.notification))

	case backgroundWorkMsg:
		m.showBackgroundResult(msg)
		return m, clearNotifyAfter(6*time.Second, m.notification)

	case queuedInputMsg:
		return m.startQueuedInput()

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	// Update textarea while composing, including during streaming, but never
	// while a modal approval is taking keyboard focus.
	if m.currentApproval == nil && (m.state == stateIdle || m.state == stateStreaming || m.waitingForUserAnswer) {
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

// startQueuedCmd schedules a queued submission when the current turn is idle.
// It leaves the actual submission to Update so the usual command and stream
// handling stays in one place.
func (m model) startQueuedCmd() tea.Cmd {
	if m.state != stateIdle || m.waitingForUserAnswer || len(m.queued) == 0 ||
		m.currentApproval != nil || m.configForm != nil || m.historySearch != nil ||
		m.quickOpen != nil || m.modelPicker != nil {
		return nil
	}
	return func() tea.Msg { return queuedInputMsg{} }
}

func (m model) startQueuedInput() (tea.Model, tea.Cmd) {
	if m.startQueuedCmd() == nil {
		return m, nil
	}

	item := m.queued[0]
	m.queued = m.queued[1:]
	draft := m.textarea.Value()
	draftImages := append([]string(nil), m.pastedImages...)
	m.textarea.SetValue(item.raw)
	m.pastedImages = item.images

	// Reuse the normal Enter path for local commands, history and AI turns.
	next, cmd := m.update(tea.KeyMsg{Type: tea.KeyEnter})
	updated, ok := next.(model)
	if !ok {
		return next, cmd
	}
	if strings.TrimSpace(item.raw) == "/recover" && updated.recoveryDraft != "" {
		// Preserve a draft written while this queued command waited. The
		// command itself only filled the composer and must not lose its text
		// when the normal queued-input restoration runs below.
		recovered := updated.textarea.Value()
		if strings.TrimSpace(draft) == "" {
			draft = recovered
		} else if strings.TrimSpace(recovered) != "" {
			draft += "\n\n" + recovered
		}
	}
	updated.textarea.SetValue(draft)
	updated.textarea.CursorEnd()
	updated.pastedImages = draftImages
	updated.resizeComposer()
	return updated, tea.Batch(cmd, updated.startQueuedCmd())
}

// ── Stream launch ───────────────────────────────────────────────────────────

func (m *model) launchStream(input string) <-chan streamMsg {
	ch := make(chan streamMsg, 64)
	metadata := map[string]string{}
	if m.cmdDeps.activeSessionID != nil {
		metadata["polypod_session_id"] = m.cmdDeps.activeSessionID()
	}

	go func() {
		defer close(ch)

		msg := adapter.InMessage{
			ID:        fmt.Sprintf("cli-%d", time.Now().UnixNano()),
			Channel:   "cli",
			UserID:    "local",
			UserName:  "local",
			Text:      input,
			Timestamp: time.Now(),
			Metadata:  metadata,
		}

		chunks := make(chan adapter.StreamChunk, 64)
		go m.streamHandler(m.ctx, msg, chunks)

		for chunk := range chunks {
			if chunk.Error != nil {
				ch <- streamMsg{err: chunk.Error, done: true}
				return
			}
			if chunk.ToolEvent != nil {
				ch <- streamMsg{toolEvent: chunk.ToolEvent}
				continue
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
	// A forced render supersedes any pending token timer. Its later message is
	// ignored, including when a new turn has already started.
	m.streamRedrawPending = false
	m.streamRedrawGeneration++
	if !m.ready {
		return
	}
	// Keep the viewport small while the user follows the latest messages.
	// Scrolling up preserves the current page until PgUp requests older entries.
	atBottom := m.viewport.AtBottom()
	if atBottom && !m.historyExpanded {
		// Drop only the older persisted cache. The snap below still renders
		// the newest history page; local messages are not a bounded window.
		m.evictOldHistory()
		if len(m.messages) > historyPageSize {
			m.historyStart = len(m.messages) - historyPageSize
		}
	}
	m.viewport.Height = m.viewportHeight()
	content := m.renderMessages()
	m.viewport.SetContent(content)
	if atBottom {
		m.viewport.GotoBottom()
	}
	m.lastViewportRedraw = time.Now()
}

func (m *model) queueStreamRedraw() tea.Cmd {
	if !m.ready || m.streamRedrawPending {
		return nil
	}
	// New tokens cannot be seen while the user reads older lines. Keep the
	// current viewport stable and refresh the accumulated stream on return.
	if !m.viewport.AtBottom() {
		return nil
	}
	elapsed := time.Since(m.lastViewportRedraw)
	if m.lastViewportRedraw.IsZero() || elapsed >= streamRedrawInterval {
		m.updateViewport()
		return nil
	}
	m.streamRedrawPending = true
	m.streamRedrawGeneration++
	generation := m.streamRedrawGeneration
	return tea.Tick(streamRedrawInterval-elapsed, func(time.Time) tea.Msg {
		return streamRedrawMsg{generation: generation}
	})
}

// resizeComposer keeps the entire draft visible while there is room to grow.
// The conversation retains a small viewport when the draft fills the terminal.
func (m *model) resizeComposer() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	wanted := min(m.inputVisualRows(), m.maxComposerHeight())
	if wanted == m.textarea.Height() {
		return
	}
	m.textarea.SetHeight(wanted)
	m.updateViewport()
}

// inputVisualRows counts explicit newlines and lines wrapped at the usable
// textarea width. One spare cell accounts for its trailing cursor space.
func (m model) inputVisualRows() int {
	textWidth := max(1, m.textarea.Width())
	rows := 0
	for _, line := range strings.Split(m.textarea.Value(), "\n") {
		wrapped := ansi.Wrap(line, textWidth, "")
		rows += 1 + strings.Count(wrapped, "\n")
		lastRow := wrapped[strings.LastIndexByte(wrapped, '\n')+1:]
		if line != "" && lipgloss.Width(lastRow) >= textWidth-1 {
			// Bubbles reserves one trailing cursor cell. At the width
			// boundary it creates a final visual row for that cell.
			rows++
		}
	}
	return max(1, rows)
}

func (m model) maxComposerHeight() int {
	chromeHeight := 2 + 1 + 2 // header, hint line and input border
	if m.statusBar != nil && m.statusBar.IsVisible() {
		chromeHeight++
	}
	if m.tokenWarning != nil && m.tokenWarning.Level >= WarningCaution {
		if FormatWarningBanner(*m.tokenWarning, m.width) != "" {
			chromeHeight++
			if m.tokenWarning.Level >= WarningHigh {
				chromeHeight++
			}
		}
	}
	return max(1, m.height-chromeHeight-3)
}

// viewportHeight measures the visible chrome, including dynamic warning lines.
func (m model) viewportHeight() int {
	inputHeight := m.textarea.Height() + 2
	chromeHeight := 2 + 1 + inputHeight // header and the status line above input
	if m.statusBar != nil && m.statusBar.IsVisible() {
		chromeHeight++
	}
	if m.tokenWarning != nil && m.tokenWarning.Level >= WarningCaution {
		if FormatWarningBanner(*m.tokenWarning, m.width) != "" {
			chromeHeight++
			if m.tokenWarning.Level >= WarningHigh {
				chromeHeight++
			}
		}
	}
	return max(1, m.height-chromeHeight)
}

// ── Render messages ─────────────────────────────────────────────────────────

func (m *model) renderMessages() string {
	var b strings.Builder
	width := m.width
	if width <= 0 {
		width = 80
	}

	if len(m.messages) == 0 && m.state != stateStreaming {
		b.WriteString(m.renderWelcome())
		return b.String()
	}

	start := min(max(0, m.historyStart), len(m.messages))
	older := start + m.historyBefore
	if older > 0 {
		b.WriteString(subtleStyle.Render(fmt.Sprintf("  ↑ %d mensagens anteriores; PgUp para carregar", older)))
		b.WriteString("\n\n")
	}
	for i := start; i < len(m.messages); i++ {
		entry := &m.messages[i]
		if entry.renderedWidth != width {
			entry.renderedBody = renderChatEntry(*entry, width)
			entry.renderedWidth = width
		}
		b.WriteString(entry.renderedBody)

		// Subtle separator between message pairs
		if i < len(m.messages)-1 && entry.role == "assistant" {
			sep := separatorStyle.Render(strings.Repeat("─", width/2))
			b.WriteString("  ")
			b.WriteString(sep)
			b.WriteString("\n\n")
		}
	}
	if m.voiceRecording && m.voicePartialText != "" {
		preview := []rune(m.voicePartialText)
		if len(preview) > 400 {
			preview = append([]rune("…"), preview[len(preview)-400:]...)
		}
		writeWrappedMessage(&b, "Prévia de voz (não enviada): "+string(preview), "  ", "  ", width, notifyInfoStyle)
		b.WriteString("\n")
	}

	if m.state == stateStreaming && m.streamHidden {
		if m.parallelDetached {
			b.WriteString(notifyInfoStyle.Render("  Turno destacado " + m.detachedTaskID + " em execução · Ctrl+B para visualizar · novas mensagens criam tarefas"))
		} else {
			b.WriteString(notifyInfoStyle.Render("  Saída do turno minimizada · Ctrl+B para visualizar · /bg detach libera novas mensagens"))
		}
		b.WriteString("\n")
		return b.String()
	}

	// Show streaming content with cursor
	if m.state == stateStreaming {
		// Show tool events in real-time, grouped for cleaner display.
		if len(m.toolEvents) > 0 {
			b.WriteString("\n")
			b.WriteString(m.renderToolEvents())
			b.WriteString("\n")
		}

		// Live progress lines for tools currently running. These sit
		// just above the streaming response so the user always sees
		// what the agent is doing right now.
		if m.progressLines != nil {
			if pl := m.progressLines.Render(); pl != "" {
				b.WriteString(pl)
			}
		}

		if m.streaming.Len() > 0 {
			writeWrappedMessage(&b, m.streaming.String(), "  ", "  ", width, contentStyle)
		}
		b.WriteString("  ")
		b.WriteString(streamingCursorStyle.Render("▍"))
		b.WriteString("\n")
	}

	return b.String()
}

func renderChatEntry(entry chatEntry, width int) string {
	var b strings.Builder
	switch entry.role {
	case "user":
		writeWrappedMessage(&b, entry.content, userStyle.Render("  > "), "    ", width, contentStyle)
		b.WriteString("\n")
	case "assistant":
		rendered := RenderMarkdown(entry.content, max(1, width-4))
		for _, line := range strings.Split(rendered, "\n") {
			b.WriteString("  ")
			b.WriteString(line)
			b.WriteString("\n")
		}
		if entry.elapsed > 0 {
			b.WriteString("  ")
			b.WriteString(subtleStyle.Render(formatDuration(entry.elapsed)))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	case "system":
		writeWrappedMessage(&b, entry.content, "  ", "  ", width, lipgloss.NewStyle())
		b.WriteString("\n")
	case "error":
		writeWrappedMessage(&b, entry.content, "  "+errorStyle.Render("erro "), "       ", width, contentStyle)
		b.WriteString("\n")
	}
	return b.String()
}

func writeWrappedMessage(b *strings.Builder, content, firstPrefix, nextPrefix string, width int, style lipgloss.Style) {
	contentWidth := max(1, width-max(lipgloss.Width(firstPrefix), lipgloss.Width(nextPrefix)))
	for i, line := range strings.Split(ansi.Wrap(content, contentWidth, ""), "\n") {
		if i == 0 {
			b.WriteString(firstPrefix)
		} else {
			b.WriteString(nextPrefix)
		}
		b.WriteString(style.Render(line))
		b.WriteByte('\n')
	}
}

// ── Welcome screen ──────────────────────────────────────────────────────────

func (m model) renderWelcome() string {
	var b strings.Builder
	if m.viewport.Height < 3 {
		return welcomeTitleStyle.Render("Polypod")
	}
	b.WriteString("\n")
	title := "  Pronto para trabalhar"
	if m.width < 24 {
		title = "  Polypod"
	}
	b.WriteString(welcomeTitleStyle.Render(title))
	b.WriteString("\n")
	if m.viewport.Height < 5 {
		return b.String()
	}

	intro := "  Descreva uma tarefa para comecar."
	if m.width < 38 {
		intro = "  Digite uma tarefa."
	}
	b.WriteString(welcomeSubStyle.Render(intro))
	b.WriteString("\n")

	if m.viewport.Height >= 8 && m.width >= 28 {
		b.WriteString("\n")
		b.WriteString(welcomeHintStyle.Render("  Exemplos:"))
		b.WriteString("\n")
		examples := []string{
			"Explique este projeto",
			"Encontre e corrija um bug",
			"/help para ver comandos",
		}
		count := 1
		if m.viewport.Height >= 10 && m.width >= 34 {
			count = len(examples)
		}
		for _, example := range examples[:count] {
			b.WriteString("    ")
			b.WriteString(welcomeKeyStyle.Render("> "))
			b.WriteString(welcomeDescStyle.Render(example))
			b.WriteString("\n")
		}
	}

	return b.String()
}

// ── View ────────────────────────────────────────────────────────────────────

func (m model) View() string {
	if !m.ready {
		return ""
	}

	// When a modal approval is pending, the modal owns the entire
	// screen. We draw it centered over a blank background so the user
	// cannot confuse it with the normal chat view.
	if m.currentApproval != nil {
		return m.overlayApprovalModal("")
	}

	// /config form overlay (owns the entire screen).
	if m.configForm != nil {
		return m.configForm.Overlay(m.width, m.height)
	}

	// Ctrl+R history search overlay (owns the entire screen).
	if m.historySearch != nil {
		return m.historySearch.Overlay(m.width, m.height)
	}

	// Ctrl+P quick-open file picker overlay (owns the entire screen).
	if m.quickOpen != nil {
		return m.quickOpen.Overlay(m.width, m.height)
	}

	// Model picker overlay (Ctrl+Y or /model). Owns the entire screen.
	if m.modelPicker != nil {
		return m.modelPicker.Overlay(m.width, m.height)
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
	if lipgloss.Width(header) > m.width {
		header = sliceansi.Truncate(header, m.width, "…") + ansiReset
	}
	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString(separatorStyle.Render(strings.Repeat("─", m.width)))
	b.WriteString("\n")

	// ── Messages viewport ──
	b.WriteString(m.viewport.View())
	b.WriteString("\n")

	// ── Status line ──
	var status strings.Builder
	if m.waitingForUserAnswer {
		status.WriteString(notifyInfoStyle.Render(" Aguardando sua resposta...  Enter para enviar"))
	} else if m.parallelDetached {
		elapsed := time.Since(m.streamStart)
		status.WriteString(notifyInfoStyle.Render(fmt.Sprintf(" Turno destacado %s · %s · mensagens novas em tarefas separadas", m.detachedTaskID, formatDuration(elapsed))))
	} else if m.streamHidden {
		elapsed := time.Since(m.streamStart)
		status.WriteString(notifyInfoStyle.Render(fmt.Sprintf(" Turno minimizado %s · Ctrl+B visualizar", formatDuration(elapsed))))
	} else if m.notification != "" {
		switch m.notifyLevel {
		case "success":
			status.WriteString(notifySuccessStyle.Render(" " + m.notification))
		case "warn":
			status.WriteString(notifyWarnStyle.Render(" " + m.notification))
		case "error":
			status.WriteString(notifyErrorStyle.Render(" " + m.notification))
		default:
			status.WriteString(notifyInfoStyle.Render(" " + m.notification))
		}
	} else if m.state == stateStreaming {
		elapsed := time.Since(m.streamStart)
		status.WriteString(" ")
		status.WriteString(m.spinner.View())
		// Show active tool name or "thinking" in the status line
		activeToolName := m.activeToolName()
		if activeToolName != "" {
			status.WriteString(streamingDimStyle.Render(fmt.Sprintf(" %s %s", activeToolName, formatDuration(elapsed))))
		} else {
			status.WriteString(streamingDimStyle.Render(fmt.Sprintf(" pensando... %s", formatDuration(elapsed))))
		}
		if m.width >= 80 {
			status.WriteString(statusBarStyle.Render("  Ctrl+B minimizar saída"))
		}
	} else {
		// Show tab completion hints or default status
		if len(m.tabMatches) > 0 && m.tabIndex >= 0 {
			status.WriteString(statusBarStyle.Render(fmt.Sprintf(" Tab: %s (%d/%d)",
				m.tabMatches[m.tabIndex], m.tabIndex+1, len(m.tabMatches))))
		} else {
			// Count wrapped rows as well as explicit newlines so the user
			// can tell when a draft exceeds the space available on screen.
			if rows := m.inputVisualRows(); rows > m.textarea.Height() {
				hint := fmt.Sprintf(" Entrada: %d linhas, %d visiveis | setas para navegar", rows, m.textarea.Height())
				status.WriteString(statusBarStyle.Render(hint))
			} else if rows > 1 {
				hint := fmt.Sprintf(" [%d linhas | Alt+Enter=quebra | Enter=enviar]", rows)
				if IsInUnfinishedCodeBlock(m.textarea.Value()) {
					hint = fmt.Sprintf(" [%d linhas | bloco ``` aberto | Alt+Enter=quebra]", rows)
				}
				status.WriteString(statusBarStyle.Render(hint))
			} else {
				hint := " Enter enviar  Tab completar  /help comandos  Ctrl+C sair"
				if m.width < 62 {
					hint = " Enter enviar  /help comandos"
				}
				status.WriteString(statusBarStyle.Render(hint))
			}
		}
	}
	if len(m.queued) > 0 {
		status.WriteString(statusBarStyle.Render(fmt.Sprintf("  fila: %d", len(m.queued))))
	}
	b.WriteString(sliceansi.Truncate(status.String(), m.width, "…"))
	b.WriteString(ansiReset)
	b.WriteString("\n")

	// ── Token warning banner (above input, when severity is high) ──
	if m.tokenWarning != nil && m.tokenWarning.Level >= WarningCaution {
		banner := FormatWarningBanner(*m.tokenWarning, m.width)
		if banner != "" {
			b.WriteString(banner)
			b.WriteString("\n")
			// At WarningHigh+ also surface the /context tip so the
			// user knows where to look for a detailed breakdown.
			if m.tokenWarning.Level >= WarningHigh {
				tip := " Tip: /context para detalhes"
				b.WriteString(notifyWarnStyle.Render(tip))
				b.WriteString("\n")
			}
		}
	}

	// ── Input with border ──
	inputContent := m.textarea.View()
	borderStyle := inputBorderActiveStyle
	if m.waitingForUserAnswer {
		borderStyle = inputBorderQuestionStyle
	}
	bordered := borderStyle.Width(max(1, m.width-2)).Render(inputContent)
	b.WriteString(bordered)

	// ── Rich status bar (last line of the screen) ──
	if m.statusBar != nil && m.statusBar.IsVisible() {
		b.WriteString("\n")
		b.WriteString(m.statusBar.Render(m.width))
	}

	return b.String()
}

// ── Helpers ─────────────────────────────────────────────────────────────────

// renderToolEvents renders the current m.toolEvents slice, using
// messages.GroupToolEvents to collapse consecutive similar tool calls
// into summary lines. Single (ungrouped) tool events are rendered with
// the full rich display (FormatToolCallDisplay / FormatToolResultDisplay
// / FormatFileEditResult) so the user still sees the detailed output
// for one-off calls.
func (m model) renderToolEvents() string {
	var b strings.Builder
	groups := messages.GroupToolEvents(m.toolEvents)

	// Track which tool IDs belong to a batched (collapsed) group so we
	// can skip their individual Start events in the single-event path.
	collapsedIDs := make(map[string]bool)
	for _, g := range groups {
		if g.Kind == messages.GroupSingle {
			continue
		}
		for _, t := range g.Tools {
			if t.ToolID != "" {
				collapsedIDs[t.ToolID] = true
			}
		}
	}

	// Render each group in order. For GroupSingle we preserve the
	// existing rich layout by walking the raw events between this
	// group's terminal event and the previous boundary.
	//
	// Simpler approach: iterate raw events; when we hit a Start event
	// whose ToolID is in a collapsed group, skip it; when we hit a
	// terminal event in a collapsed group, print the summary once (the
	// first time we see any event from that group) and mark it printed.
	printed := make(map[int]bool) // group index -> already printed
	eventGroup := make(map[string]int, len(m.toolEvents))
	for i, g := range groups {
		if g.Kind == messages.GroupSingle {
			continue
		}
		for _, t := range g.Tools {
			if t.ToolID != "" {
				eventGroup[t.ToolID] = i
			}
		}
	}

	for _, evt := range m.toolEvents {
		// Is this event part of a collapsed batch?
		if gi, ok := eventGroup[evt.ToolID]; ok && collapsedIDs[evt.ToolID] {
			if !printed[gi] {
				b.WriteString(messages.FormatGroup(groups[gi], false))
				printed[gi] = true
			}
			continue
		}

		// Fall back to the original rich per-event rendering for
		// ungrouped / singleton events.
		switch evt.Type {
		case adapter.ToolEventStart:
			rendered, ok := m.toolRenderers.RenderUse(evt.ToolName, evt.Args)
			if !ok {
				// RenderUse already falls back to FormatToolCallDisplay, so
				// rendered is always valid — just write it directly.
			}
			b.WriteString(rendered)
		case adapter.ToolEventComplete:
			switch {
			case IsTodoToolName(evt.ToolName) && evt.Result != "":
				b.WriteString("  ")
				b.WriteString(toolResultHeaderStyle.Render("✓ " + evt.ToolName))
				b.WriteString(" ")
				b.WriteString(toolTimingStyle.Render("(" + formatDuration(evt.Elapsed) + ")"))
				b.WriteString("\n")
				items, err := ParseTodoFromToolResult(evt.Result)
				if err != nil || len(items) == 0 {
					b.WriteString(renderPlainToolResult(evt.Result))
				} else {
					rendered := FormatTodoList(items, "TODO LIST")
					for _, line := range strings.Split(rendered, "\n") {
						b.WriteString("  ")
						b.WriteString(line)
						b.WriteString("\n")
					}
				}
			case (evt.ToolName == "compact" || IsCompactResultContent(evt.Result)) && evt.Result != "":
				b.WriteString("  ")
				b.WriteString(toolResultHeaderStyle.Render("✓ " + evt.ToolName))
				b.WriteString(" ")
				b.WriteString(toolTimingStyle.Render("(" + formatDuration(evt.Elapsed) + ")"))
				b.WriteString("\n")
				cr := ParseCompactResult(evt.Result)
				width := m.width - 4
				if width <= 0 {
					width = 76
				}
				rendered := FormatCompactResult(cr, width)
				for _, line := range strings.Split(rendered, "\n") {
					b.WriteString("  ")
					b.WriteString(line)
					b.WriteString("\n")
				}
			case isFileEditTool(evt.ToolName) && evt.Result != "":
				b.WriteString("  ")
				b.WriteString(toolResultHeaderStyle.Render("✓ " + evt.ToolName))
				b.WriteString(" ")
				b.WriteString(toolTimingStyle.Render("(" + formatDuration(evt.Elapsed) + ")"))
				b.WriteString("\n")
				b.WriteString(FormatFileEditResult(evt.ToolName, evt.Result))
			default:
				if rendered, ok := m.toolRenderers.RenderResult(evt.ToolName, evt.Args, evt.Result, false); ok {
					b.WriteString(rendered)
				} else {
					b.WriteString(FormatToolResultDisplay(evt.ToolName, evt.Result, evt.Elapsed))
				}
			}
		case adapter.ToolEventError:
			b.WriteString("  ")
			b.WriteString(errorStyle.Render("✗ " + evt.ToolName))
			b.WriteString(" ")
			b.WriteString(toolTimingStyle.Render("(" + formatDuration(evt.Elapsed) + ")"))
			b.WriteString("\n")
			b.WriteString("    ")
			b.WriteString(errorStyle.Render(evt.Error))
			b.WriteString("\n")
		}
	}

	return b.String()
}

// renderToolOutput renders a tool result string using the registry's custom
// renderer for toolName when one is registered, falling back to the raw output
// otherwise. Safe to call when m.toolRenderers is nil.
func (m model) renderToolOutput(toolName, output string) string {
	if m.toolRenderers != nil {
		rendered, ok := m.toolRenderers.RenderResult(toolName, nil, output, false)
		if ok {
			return rendered
		}
	}
	return output
}

// activeToolName returns the name of the currently executing tool (i.e. a tool
// that has a Start event but no corresponding Complete or Error event yet).
// Returns "" if no tool is currently running.
func (m model) activeToolName() string {
	started := make(map[string]bool)
	for _, evt := range m.toolEvents {
		switch evt.Type {
		case adapter.ToolEventStart:
			started[evt.ToolID] = true
		case adapter.ToolEventComplete, adapter.ToolEventError:
			delete(started, evt.ToolID)
		}
	}
	// Return the last one that's still running
	for i := len(m.toolEvents) - 1; i >= 0; i-- {
		evt := m.toolEvents[i]
		if evt.Type == adapter.ToolEventStart && started[evt.ToolID] {
			return evt.ToolName
		}
	}
	return ""
}

// recordFileFromToolEvent inspects a tool event for file path arguments and
// records read/edit activity in the session state. Used by /files to show
// which files have been touched in the current conversation.
func (m *model) recordFileFromToolEvent(evt *adapter.ToolEventInfo) {
	if evt == nil || m.sessionState == nil {
		return
	}
	// Only care about Complete events so we know the op actually happened.
	if evt.Type != adapter.ToolEventComplete {
		return
	}
	path := extractPathFromArgs(evt.Args)
	if path == "" {
		return
	}
	toolName := strings.ToLower(evt.ToolName)
	switch {
	case strings.Contains(toolName, "read") || strings.Contains(toolName, "view") || strings.Contains(toolName, "cat"):
		m.sessionState.recordFileRead(path)
	case strings.Contains(toolName, "edit") || strings.Contains(toolName, "write") || strings.Contains(toolName, "patch") || strings.Contains(toolName, "str_replace"):
		m.sessionState.recordFileEdit(path)
	}
}

// extractPathFromArgs pulls a file path out of a tool args map, trying
// several common key names.
func extractPathFromArgs(args map[string]any) string {
	if args == nil {
		return ""
	}
	keys := []string{"path", "file_path", "filepath", "filename", "file"}
	for _, k := range keys {
		if v, ok := args[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// expandMentionsForAI runs inputprocessing.ExpandMentions on the user
// input before handing it off to the AI layer. The working directory
// comes from os.Getwd() (the process cwd is the project root for all
// CLI-mode invocations) and extraDirs is sourced from the session
// state populated by /add-dir.
//
// If expansion finds no mentions, the original input is returned
// unchanged so the AI receives exactly what the user typed. Errors
// during file resolution are folded into the appendix as
// "(file not found: ...)" markers by the inputprocessing package;
// this function never fails.
func (m *model) expandMentionsForAI(input string) string {
	workingDir, err := os.Getwd()
	if err != nil {
		workingDir = "."
	}
	var extraDirs []string
	if m.sessionState != nil {
		for _, d := range m.sessionState.snapshotDirs() {
			extraDirs = append(extraDirs, d.path)
		}
	}
	expanded, _ := inputprocessing.ExpandMentions(input, workingDir, extraDirs)
	return expanded
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// ── Status bar refresh helpers ─────────────────────────────────────────────

// refreshActiveToolIndicator inspects the current tool events to find a
// running tool (if any), pushes that into the rich status bar, and kicks
// off the spinner ticker when appropriate. Returns a tea.Cmd that should
// be scheduled by the caller (nil when nothing needs scheduling).
//
// The ticker is only armed once: subsequent calls while the ticker is
// already running return nil, so we never end up with multiple concurrent
// ticker chains.
func (m *model) refreshActiveToolIndicator() tea.Cmd {
	if m.statusBar == nil {
		return nil
	}
	active := m.activeToolName()
	m.statusBar.UpdateActiveTool(active)
	if active == "" {
		m.tickerRunning = false
		return nil
	}
	if m.tickerRunning {
		return nil
	}
	m.tickerRunning = true
	return scheduleStatusTick()
}

// refreshStatusBar re-reads the mode, agent, model, token counts and cost
// through the command-deps callbacks (all optional) and pushes a fresh
// snapshot into the rich status bar. Called after every AI turn completes.
func (m *model) refreshStatusBar() {
	if m.statusBar == nil {
		return
	}
	// Start from whatever the bar already knows so we preserve values
	// for fields the deps do not expose.
	state := StatusBarState{
		Visible:      true,
		Mode:         "edit",
		SessionStart: m.sessionStart,
	}
	if m.cmdDeps.planCurrent != nil {
		if mode := m.cmdDeps.planCurrent(); mode != "" {
			state.Mode = mode
		}
	}
	if m.cmdDeps.activeAgent != nil {
		state.Agent = m.cmdDeps.activeAgent()
	}
	if m.cmdDeps.currentModel != nil {
		state.Model = m.cmdDeps.currentModel()
	}
	if m.cmdDeps.sessionInfo != nil {
		if id, _, err := m.cmdDeps.sessionInfo(); err == nil {
			state.SessionID = id
		}
	}
	if m.cmdDeps.contextTokens != nil {
		state.TokensUsed, state.TokensMax = m.cmdDeps.contextTokens()
	}
	if m.cmdDeps.sessionCost != nil {
		state.Cost = m.cmdDeps.sessionCost()
	}
	// Preserve the currently-active tool so a turn that spans multiple
	// tool calls does not blink the indicator off between calls.
	active := m.activeToolName()
	state.ActiveTool = active
	m.statusBar.Update(state)
}

// refreshTokenWarning recomputes the prominent above-input warning
// banner from the current contextTokens dependency. Called after each
// AI turn so the banner reacts to the latest token counts. When the
// dependency is missing the warning falls back to WarningNone.
func (m *model) refreshTokenWarning() {
	if m.cmdDeps.contextTokens == nil {
		w := TokenWarning{Level: WarningNone}
		m.tokenWarning = &w
		return
	}
	used, max := m.cmdDeps.contextTokens()
	w := ComputeTokenWarning(used, max)
	m.tokenWarning = &w
}

// feedProgressLine relays a tool lifecycle event into the
// progressLines display. Start/Complete/Error all map cleanly; Progress
// events are translated into AppendLine when they carry a Result string,
// or UpdateTool otherwise.
func (m *model) feedProgressLine(evt *adapter.ToolEventInfo) {
	if evt == nil || m.progressLines == nil {
		return
	}
	switch evt.Type {
	case adapter.ToolEventStart:
		m.progressLines.StartTool(evt.ToolID, evt.ToolName)
	case adapter.ToolEventProgress:
		if evt.Result != "" {
			// Treat the result string as one or more recent stdout
			// lines. Splitting on '\n' lets a single Progress event
			// carry a small batch.
			for _, line := range strings.Split(evt.Result, "\n") {
				m.progressLines.AppendLine(evt.ToolID, line)
			}
		} else if evt.Error != "" {
			m.progressLines.UpdateTool(evt.ToolID, evt.Error)
		}
	case adapter.ToolEventComplete:
		// If there is a single-line result, surface it as the final
		// message before marking the tool complete.
		if evt.Result != "" {
			first := evt.Result
			if i := strings.IndexByte(first, '\n'); i >= 0 {
				first = first[:i]
			}
			if first != "" {
				m.progressLines.UpdateTool(evt.ToolID, first)
			}
		}
		m.progressLines.CompleteTool(evt.ToolID)
	case adapter.ToolEventError:
		m.progressLines.ErrorTool(evt.ToolID, evt.Error)
	}
}

// refreshProgressTicker arms the progress-line spinner ticker when at
// least one tool is currently running. Returns nil if the ticker is
// already running or no tools are active.
func (m *model) refreshProgressTicker() tea.Cmd {
	if m.progressLines == nil {
		m.progressTickerRunning = false
		return nil
	}
	if m.progressLines.ActiveCount() == 0 {
		m.progressTickerRunning = false
		return nil
	}
	if m.progressTickerRunning {
		return nil
	}
	m.progressTickerRunning = true
	return scheduleProgressTick()
}

// ── Paste handling ──────────────────────────────────────────────────────────

// handlePasteBurst is called when a large runes burst arrives (either a
// bracketed-paste event or a single KeyRunes with many characters). It
// classifies the paste, inserts the formatted text into the textarea,
// records image attachments, and emits a status notification describing
// what was detected.
//
// Returns (true, cmd) when the paste was handled and the caller should
// return immediately; (false, nil) when the burst should fall through to
// the normal key dispatch (e.g. it did not actually look like a paste).
func (m *model) handlePasteBurst(content string) (bool, tea.Cmd) {
	if content == "" {
		return false, nil
	}

	pasteType := DetectPasteType(content)

	switch pasteType {
	case PasteImage:
		// Save the data URL to disk and insert a short reference so
		// the AI can later open it with a file tool. If saving fails
		// fall back to inserting the raw data URL so the user does
		// not silently lose data.
		path, err := SaveImageFromDataURL(content, m.dataDir)
		if err != nil {
			m.notification = fmt.Sprintf("falha ao salvar imagem: %v", err)
			m.notifyLevel = "error"
			m.textarea.InsertString(content)
			return true, clearNotifyAfter(3*time.Second, m.notification)
		}
		base := path
		if i := strings.LastIndex(path, "/"); i >= 0 {
			base = path[i+1:]
		}
		m.pastedImages = append(m.pastedImages, path)
		m.textarea.InsertString(fmt.Sprintf("[image: %s inserida]", base))
		m.notification = fmt.Sprintf("imagem salva: %s", path)
		m.notifyLevel = "success"
		return true, clearNotifyAfter(4*time.Second, m.notification)

	case PasteCode, PasteJSON, PasteFilePath, PasteURL:
		formatted := FormatPaste(content, pasteType)
		m.textarea.InsertString(formatted)
		m.notification = fmt.Sprintf("colado: %s (%d caracteres)",
			pasteType.String(), len(content))
		m.notifyLevel = "info"
		return true, clearNotifyAfter(2500*time.Millisecond, m.notification)

	default:
		// Plain text: just insert as-is. Note the notification so the
		// user knows the paste was recognised without any formatting
		// transformation.
		m.textarea.InsertString(content)
		if len(content) >= pasteThreshold {
			m.notification = fmt.Sprintf("colado: texto (%d caracteres)", len(content))
			m.notifyLevel = "info"
			return true, clearNotifyAfter(2*time.Second, m.notification)
		}
		return true, nil
	}
}

// clearNotifyAfter returns a tea.Cmd that emits a clearNotifyMsg after the
// given delay, used to auto-dismiss paste notifications.
func clearNotifyAfter(d time.Duration, notification string) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg {
		return clearNotifyMsg{text: notification}
	})
}
