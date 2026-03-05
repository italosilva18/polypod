package cli

import "github.com/charmbracelet/lipgloss"

var (
	userStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6")). // cyan
			Bold(true)

	assistantStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2")). // green
			Bold(true)

	systemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("3")). // yellow
			Bold(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1")). // red
			Bold(true)

	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("5")). // magenta
			Bold(true).
			Padding(0, 1)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("7")).  // white
			Background(lipgloss.Color("8")). // dark gray
			Padding(0, 1)

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")) // dim gray
)
