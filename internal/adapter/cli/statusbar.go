package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// StatusBarState holds all the data the status bar displays. It is a pure
// value type that can be refreshed from outside the bar (e.g. after each AI
// response) without exposing the bar's rendering internals to callers.
type StatusBarState struct {
	// Core identity
	Model        string
	Agent        string
	Mode         string // "edit", "plan", "ask", "auto"
	SessionID    string
	SessionStart time.Time

	// Token usage
	TokensUsed int
	TokensMax  int

	// Cost in USD
	Cost float64

	// Active tool name (empty when idle).
	ActiveTool string

	// Visibility
	Visible bool
}

// StatusBar renders a rich, Claude-Code-style status bar for the CLI. It has
// three layout modes (compact / normal / detailed) selected based on the
// terminal width at render time.
type StatusBar struct {
	state        StatusBarState
	width        int
	spinnerFrame int
}

// spinnerFrames is the braille spinner used for the active-tool indicator.
var spinnerFrames = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

// NewStatusBar creates a status bar visible by default and in edit mode.
func NewStatusBar() *StatusBar {
	return &StatusBar{
		state: StatusBarState{
			Visible:      true,
			Mode:         "edit",
			SessionStart: time.Now(),
		},
	}
}

// Update replaces the entire state. Zero-valued fields are taken as-is, so
// callers should construct a full StatusBarState each time (or use the
// field-level helpers below).
func (sb *StatusBar) Update(state StatusBarState) {
	// Preserve session start if caller forgot to set it.
	if state.SessionStart.IsZero() {
		state.SessionStart = sb.state.SessionStart
	}
	sb.state = state
}

// UpdateActiveTool sets the currently running tool. Pass "" to clear.
func (sb *StatusBar) UpdateActiveTool(toolName string) {
	sb.state.ActiveTool = toolName
	if toolName == "" {
		sb.spinnerFrame = 0
	}
}

// UpdateTokens updates token counts.
func (sb *StatusBar) UpdateTokens(used, max int) {
	sb.state.TokensUsed = used
	sb.state.TokensMax = max
}

// UpdateCost updates the cost display.
func (sb *StatusBar) UpdateCost(cost float64) {
	sb.state.Cost = cost
}

// UpdateMode updates the mode indicator.
func (sb *StatusBar) UpdateMode(mode string) {
	if mode == "" {
		return
	}
	sb.state.Mode = mode
}

// UpdateModel sets the model name.
func (sb *StatusBar) UpdateModel(model string) {
	sb.state.Model = model
}

// UpdateAgent sets the agent name.
func (sb *StatusBar) UpdateAgent(agent string) {
	sb.state.Agent = agent
}

// SetSession sets the session ID and start time if not already set.
func (sb *StatusBar) SetSession(id string) {
	sb.state.SessionID = id
	if sb.state.SessionStart.IsZero() {
		sb.state.SessionStart = time.Now()
	}
}

// Toggle flips visibility.
func (sb *StatusBar) Toggle() {
	sb.state.Visible = !sb.state.Visible
}

// Visible reports whether the bar is currently rendered.
func (sb *StatusBar) IsVisible() bool {
	return sb.state.Visible
}

// Tick advances the spinner frame. Callers should arrange to call this
// roughly every 100ms while a tool is active.
func (sb *StatusBar) Tick() {
	sb.spinnerFrame = (sb.spinnerFrame + 1) % len(spinnerFrames)
}

// Render returns the styled status bar for the given terminal width.
// Returns "" when the bar is hidden.
func (sb *StatusBar) Render(width int) string {
	if !sb.state.Visible {
		return ""
	}
	if width <= 0 {
		width = 80
	}
	sb.width = width

	switch {
	case width < 60:
		return sb.renderCompact(width)
	case width < 100:
		return sb.renderNormal(width)
	default:
		return sb.renderDetailed(width)
	}
}

// ── Layout: compact ─────────────────────────────────────────────────────────

// renderCompact is used for narrow terminals. Format:
//
//	polypod | edit | 12k/128k | $0.02
func (sb *StatusBar) renderCompact(width int) string {
	brand := sbBrandStyle.Render("polypod")
	mode := sb.renderModeBadge(false)
	tokens := sb.renderTokensCompact()
	cost := sb.renderCost()

	parts := []string{brand, mode}
	if tokens != "" {
		parts = append(parts, tokens)
	}
	if cost != "" {
		parts = append(parts, cost)
	}

	sep := sbSepStyle.Render(" | ")
	content := strings.Join(parts, sep)
	return sb.padToWidth(content, width)
}

// ── Layout: normal ──────────────────────────────────────────────────────────

// renderNormal is used for medium terminals. Format:
//
//	[polypod] mode:edit | agent:coder | ctx:12.3k/128k (9%) | $0.0234 | session:abc12345
func (sb *StatusBar) renderNormal(width int) string {
	parts := []string{sbBrandStyle.Render("[polypod]")}

	parts = append(parts, sbLabelStyle.Render("mode:")+sb.renderModeBadge(false))

	if sb.state.Agent != "" {
		parts = append(parts, sbLabelStyle.Render("agent:")+sbValueStyle.Render(
			ansi.Truncate(sb.state.Agent, max(12, width/5), "…")))
	}

	if tok := sb.renderTokensDetailed(); tok != "" {
		parts = append(parts, sbLabelStyle.Render("ctx:")+tok)
	}

	if cost := sb.renderCost(); cost != "" {
		parts = append(parts, cost)
	}

	if sb.state.SessionID != "" {
		parts = append(parts, sbLabelStyle.Render("session:")+sbValueStyle.Render(shortSessionID(sb.state.SessionID)))
	}

	sep := sbSepStyle.Render(" | ")
	content := strings.Join(parts, sep)

	// Active tool goes on the right.
	right := sb.renderActiveTool()
	return sb.layoutWithRight(content, right, width)
}

// ── Layout: detailed ────────────────────────────────────────────────────────

// renderDetailed is used for wide terminals and includes everything.
func (sb *StatusBar) renderDetailed(width int) string {
	parts := []string{sbBrandStyle.Render("[polypod]")}

	parts = append(parts, sbLabelStyle.Render("mode:")+sb.renderModeBadge(true))

	if sb.state.Model != "" {
		parts = append(parts, sbLabelStyle.Render("model:")+sbValueStyle.Render(
			ansi.Truncate(sb.state.Model, max(16, width/4), "…")))
	}
	if sb.state.Agent != "" {
		parts = append(parts, sbLabelStyle.Render("agent:")+sbValueStyle.Render(
			ansi.Truncate(sb.state.Agent, max(12, width/5), "…")))
	}

	if tok := sb.renderTokensDetailed(); tok != "" {
		parts = append(parts, sbLabelStyle.Render("ctx:")+tok)
	}

	if cost := sb.renderCost(); cost != "" {
		parts = append(parts, cost)
	}

	if sb.state.SessionID != "" {
		parts = append(parts, sbLabelStyle.Render("session:")+sbValueStyle.Render(shortSessionID(sb.state.SessionID)))
	}

	if !sb.state.SessionStart.IsZero() {
		dur := formatSessionDuration(time.Since(sb.state.SessionStart))
		parts = append(parts, sbLabelStyle.Render("time:")+sbValueStyle.Render(dur))
	}

	sep := sbSepStyle.Render(" | ")
	content := strings.Join(parts, sep)

	right := sb.renderActiveTool()
	return sb.layoutWithRight(content, right, width)
}

// ── Component renderers ─────────────────────────────────────────────────────

// renderModeBadge returns the mode string styled with its color. If withIcon
// is true, plan mode gets a leading eye icon.
func (sb *StatusBar) renderModeBadge(withIcon bool) string {
	mode := sb.state.Mode
	if mode == "" {
		mode = "edit"
	}

	var fg lipgloss.Color
	switch mode {
	case "edit":
		fg = lipgloss.Color("#00FF00")
	case "plan":
		fg = lipgloss.Color("#FFCC00")
	case "ask":
		fg = lipgloss.Color("#00CCFF")
	case "auto":
		fg = lipgloss.Color("#FF00FF")
	default:
		fg = lipgloss.Color("#FFFFFF")
	}

	label := strings.ToUpper(mode)
	if withIcon && mode == "plan" {
		label = "👁 " + label
	}

	return lipgloss.NewStyle().
		Background(sbBgColor).
		Foreground(fg).
		Bold(true).
		Render(label)
}

// renderTokensCompact returns "12k/128k".
func (sb *StatusBar) renderTokensCompact() string {
	if sb.state.TokensMax == 0 {
		return ""
	}
	used := formatTokenShort(sb.state.TokensUsed)
	max := formatTokenShort(sb.state.TokensMax)
	txt := used + "/" + max
	return lipgloss.NewStyle().
		Background(sbBgColor).
		Foreground(tokenColor(sb.state.TokensUsed, sb.state.TokensMax)).
		Render(txt)
}

// renderTokensDetailed returns "12.3k/128k (9%)" with warning color.
func (sb *StatusBar) renderTokensDetailed() string {
	if sb.state.TokensMax == 0 && sb.state.TokensUsed == 0 {
		return ""
	}
	used := formatTokenShort(sb.state.TokensUsed)
	var txt string
	if sb.state.TokensMax > 0 {
		max := formatTokenShort(sb.state.TokensMax)
		pct := int(float64(sb.state.TokensUsed) / float64(sb.state.TokensMax) * 100)
		txt = fmt.Sprintf("%s/%s (%d%%)", used, max, pct)
	} else {
		txt = used
	}

	color := tokenColor(sb.state.TokensUsed, sb.state.TokensMax)
	style := lipgloss.NewStyle().
		Background(sbBgColor).
		Foreground(color).
		Bold(true)

	// Critical: blink when > 95%.
	if sb.state.TokensMax > 0 {
		pct := float64(sb.state.TokensUsed) / float64(sb.state.TokensMax)
		if pct > 0.95 {
			style = style.Blink(true)
		}
	}

	return style.Render(txt)
}

// renderCost returns the colored cost string, e.g. "$0.0234".
func (sb *StatusBar) renderCost() string {
	if sb.state.Cost <= 0 {
		return ""
	}
	return lipgloss.NewStyle().
		Background(sbBgColor).
		Foreground(costColor(sb.state.Cost)).
		Bold(true).
		Render(formatCostDisplay(sb.state.Cost))
}

// renderActiveTool returns the right-aligned active tool indicator, or "".
func (sb *StatusBar) renderActiveTool() string {
	if sb.state.ActiveTool == "" {
		return ""
	}
	spinner := string(spinnerFrames[sb.spinnerFrame%len(spinnerFrames)])
	txt := fmt.Sprintf("%s running: %s", spinner, sb.state.ActiveTool)
	return lipgloss.NewStyle().
		Background(sbBgColor).
		Foreground(lipgloss.Color("#FFCC00")).
		Bold(true).
		Render(txt)
}

// ── Layout helpers ──────────────────────────────────────────────────────────

// layoutWithRight places `left` at the start and `right` at the end of the
// bar, padded with the status-bar background color. When both sides do not
// fit, shorten them so the active tool remains visible without wrapping.
func (sb *StatusBar) layoutWithRight(left, right string, width int) string {
	if width <= 0 {
		return ""
	}
	leftLen := visibleRuneLen(left)
	rightLen := visibleRuneLen(right)

	if right == "" {
		return sb.padToWidth(left, width)
	}
	if leftLen+rightLen+2 > width {
		rightBudget := min(rightLen, max(12, width/3))
		if rightBudget >= width {
			rightBudget = width / 2
		}
		left = ansi.Truncate(left, width-rightBudget-1, "…")
		right = ansi.Truncate(right, rightBudget, "…")
		leftLen = visibleRuneLen(left)
		rightLen = visibleRuneLen(right)
	}

	gap := width - leftLen - rightLen
	if gap < 1 {
		gap = 1
	}
	padding := sbBarStyle.Render(strings.Repeat(" ", gap))
	return left + padding + right
}

// padToWidth pads `content` to `width` visible chars using the bar style.
func (sb *StatusBar) padToWidth(content string, width int) string {
	if width <= 0 {
		return ""
	}
	contentLen := visibleRuneLen(content)
	if contentLen > width {
		return ansi.Truncate(content, width, "…")
	}
	if contentLen == width {
		return content
	}
	padding := sbBarStyle.Render(strings.Repeat(" ", width-contentLen))
	return content + padding
}

// ── Styles ──────────────────────────────────────────────────────────────────

var (
	sbBgColor = lipgloss.Color("#1A1A2E")

	sbBarStyle = lipgloss.NewStyle().
			Background(sbBgColor).
			Foreground(lipgloss.Color("#888888"))

	sbBrandStyle = lipgloss.NewStyle().
			Background(sbBgColor).
			Foreground(lipgloss.Color("#57E5E5")).
			Bold(true)

	sbLabelStyle = lipgloss.NewStyle().
			Background(sbBgColor).
			Foreground(lipgloss.Color("#666666"))

	sbValueStyle = lipgloss.NewStyle().
			Background(sbBgColor).
			Foreground(lipgloss.Color("#CCCCCC"))

	sbSepStyle = lipgloss.NewStyle().
			Background(sbBgColor).
			Foreground(lipgloss.Color("#333333"))
)

// ── Color helpers ───────────────────────────────────────────────────────────

// tokenColor returns a color keyed to the percentage of the context window
// used. White is returned when the max is unknown.
func tokenColor(used, max int) lipgloss.Color {
	if max == 0 {
		return lipgloss.Color("#FFFFFF")
	}
	pct := float64(used) / float64(max)
	switch {
	case pct > 0.95:
		return lipgloss.Color("#FF0000") // red
	case pct > 0.80:
		return lipgloss.Color("#FF8800") // orange
	case pct > 0.50:
		return lipgloss.Color("#FFCC00") // yellow
	default:
		return lipgloss.Color("#00FF00") // green
	}
}

// costColor returns the color for a cost display.
func costColor(cost float64) lipgloss.Color {
	switch {
	case cost > 1.0:
		return lipgloss.Color("#FF0000")
	case cost > 0.1:
		return lipgloss.Color("#FFCC00")
	default:
		return lipgloss.Color("#00FF00")
	}
}

// ── Formatting helpers ──────────────────────────────────────────────────────

// formatTokenShort formats a token count as "234" / "12.3k" / "1.2M".
func formatTokenShort(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1000:
		// Drop trailing ".0" for whole thousands.
		v := float64(n) / 1000
		if v == float64(int(v)) {
			return fmt.Sprintf("%dk", int(v))
		}
		return fmt.Sprintf("%.1fk", v)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// formatCostDisplay formats a USD cost according to the brief.
func formatCostDisplay(cost float64) string {
	switch {
	case cost < 0.001:
		return "$<0.001"
	case cost < 1.0:
		return fmt.Sprintf("$%.3f", cost)
	default:
		return fmt.Sprintf("$%.2f", cost)
	}
}

// formatSessionDuration formats a session duration as "30s" / "5m" / "1h30m".
func formatSessionDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

// shortSessionID truncates a session ID to 8 chars.
func shortSessionID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// visibleRuneLen returns the terminal cell width, including wide characters
// and excluding ANSI escape sequences produced by lipgloss.
func visibleRuneLen(s string) int {
	return ansi.StringWidth(s)
}
