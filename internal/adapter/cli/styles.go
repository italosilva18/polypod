package cli

import "github.com/charmbracelet/lipgloss"

// ── Color palette ───────────────────────────────────────────────────────────

const (
	colorWhite      = lipgloss.Color("#FFFFFF")
	colorWhiteDim   = lipgloss.Color("#CCCCCC")
	colorCyan       = lipgloss.Color("#57E5E5")
	colorGreen      = lipgloss.Color("#5AF78E")
	colorYellow     = lipgloss.Color("#F3F99D")
	colorRed        = lipgloss.Color("#FF5C57")
	colorMagenta    = lipgloss.Color("#FF6AC1")
	colorBlue       = lipgloss.Color("#57C7FF")
	colorDim        = lipgloss.Color("#888888")
	colorSubtle     = lipgloss.Color("#555555")
	colorBorder     = lipgloss.Color("#444444")
	colorBorderDim  = lipgloss.Color("#333333")
	colorBg         = lipgloss.Color("#1A1A2E")
	colorBgAccent   = lipgloss.Color("#16213E")
	colorAccent     = lipgloss.Color("#0F3460")
	colorBrandStart = lipgloss.Color("#57C7FF")
	colorBrandEnd   = lipgloss.Color("#5AF78E")
)

// ── Content styles ──────────────────────────────────────────────────────────

var (
	contentStyle = lipgloss.NewStyle().
			Foreground(colorWhite)

	contentDimStyle = lipgloss.NewStyle().
			Foreground(colorWhiteDim)
)

// ── Role prefix styles ──────────────────────────────────────────────────────

var (
	userStyle = lipgloss.NewStyle().
			Foreground(colorCyan).
			Bold(true)

	assistantStyle = lipgloss.NewStyle().
			Foreground(colorGreen).
			Bold(true)

	systemStyle = lipgloss.NewStyle().
			Foreground(colorYellow).
			Bold(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(colorRed).
			Bold(true)
)

// ── Header ──────────────────────────────────────────────────────────────────

var (
	logoStyle = lipgloss.NewStyle().
			Foreground(colorCyan).
			Bold(true)

	headerAgentStyle = lipgloss.NewStyle().
				Foreground(colorGreen)

	headerVersionStyle = lipgloss.NewStyle().
				Foreground(colorSubtle)
)

// ── Layout ──────────────────────────────────────────────────────────────────

var (
	separatorStyle = lipgloss.NewStyle().
			Foreground(colorBorderDim)

	dimStyle = lipgloss.NewStyle().
			Foreground(colorDim)

	subtleStyle = lipgloss.NewStyle().
			Foreground(colorSubtle)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(colorSubtle)
)

// ── Input box ───────────────────────────────────────────────────────────────

var (
	inputBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorBorder).
				Padding(0, 1)

	inputBorderActiveStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorCyan).
				Padding(0, 1)
)

// ── Messages ────────────────────────────────────────────────────────────────

var (
	userMsgStyle = lipgloss.NewStyle().
			PaddingLeft(2)

	assistantMsgStyle = lipgloss.NewStyle().
				PaddingLeft(2)

	systemMsgStyle = lipgloss.NewStyle().
			Foreground(colorYellow).
			PaddingLeft(2)

	errorMsgStyle = lipgloss.NewStyle().
			Foreground(colorRed).
			PaddingLeft(2)

	timestampStyle = lipgloss.NewStyle().
			Foreground(colorSubtle)
)

// ── Welcome screen ──────────────────────────────────────────────────────────

var (
	welcomeTitleStyle = lipgloss.NewStyle().
				Foreground(colorCyan).
				Bold(true)

	welcomeSubStyle = lipgloss.NewStyle().
			Foreground(colorDim)

	welcomeHintStyle = lipgloss.NewStyle().
				Foreground(colorSubtle)

	welcomeKeyStyle = lipgloss.NewStyle().
			Foreground(colorWhiteDim)

	welcomeDescStyle = lipgloss.NewStyle().
				Foreground(colorDim)
)

// ── Command output ──────────────────────────────────────────────────────────

var (
	cmdTitleStyle = lipgloss.NewStyle().
			Foreground(colorCyan).
			Bold(true)

	cmdLabelStyle = lipgloss.NewStyle().
			Foreground(colorDim)

	cmdValueStyle = lipgloss.NewStyle().
			Foreground(colorWhite)

	cmdActiveStyle = lipgloss.NewStyle().
			Foreground(colorGreen).
			Bold(true)

	cmdInactiveStyle = lipgloss.NewStyle().
				Foreground(colorWhiteDim)

	cmdBadgeStyle = lipgloss.NewStyle().
			Foreground(colorCyan)

	cmdSepStyle = lipgloss.NewStyle().
			Foreground(colorBorderDim)

	cmdTopicStyle = lipgloss.NewStyle().
			Foreground(colorMagenta).
			Bold(true)

	cmdPreviewStyle = lipgloss.NewStyle().
			Foreground(colorWhiteDim)
)

// ── Streaming ───────────────────────────────────────────────────────────────

var (
	streamingCursorStyle = lipgloss.NewStyle().
				Foreground(colorGreen)

	streamingDimStyle = lipgloss.NewStyle().
				Foreground(colorSubtle)

	streamingSpinnerStyle = lipgloss.NewStyle().
				Foreground(colorCyan)
)

// ── Notification ────────────────────────────────────────────────────────────

var (
	notifySuccessStyle = lipgloss.NewStyle().
				Foreground(colorGreen)

	notifyInfoStyle = lipgloss.NewStyle().
			Foreground(colorCyan)

	notifyWarnStyle = lipgloss.NewStyle().
			Foreground(colorYellow)

	notifyErrorStyle = lipgloss.NewStyle().
				Foreground(colorRed)
)
