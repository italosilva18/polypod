package theme

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Theme defines visual styling for the CLI.
type Theme struct {
	Name       string
	Background string
	Foreground string
	Accent     string
	Dim        string
	Border     string
	Success    string
	Error      string
	Warning    string
	UserBubble string
	AIBubble   string
}

// Builtin themes.
var Themes = map[string]Theme{
	"dark": {
		Name:       "dark",
		Background: "#0a0a0a",
		Foreground: "#e0e0e0",
		Accent:     "#4a9fd4",
		Dim:        "#666666",
		Border:     "#333333",
		Success:    "#4caf50",
		Error:      "#f44336",
		Warning:    "#ff9800",
		UserBubble: "#1a3a5c",
		AIBubble:   "#1a1a2e",
	},
	"light": {
		Name:       "light",
		Background: "#ffffff",
		Foreground: "#1a1a1a",
		Accent:     "#0066cc",
		Dim:        "#999999",
		Border:     "#dddddd",
		Success:    "#2e7d32",
		Error:      "#c62828",
		Warning:    "#e65100",
		UserBubble: "#e3f2fd",
		AIBubble:   "#f5f5f5",
	},
	"monokai": {
		Name:       "monokai",
		Background: "#272822",
		Foreground: "#f8f8f2",
		Accent:     "#66d9ef",
		Dim:        "#75715e",
		Border:     "#49483e",
		Success:    "#a6e22e",
		Error:      "#f92672",
		Warning:    "#fd971f",
		UserBubble: "#3e3d32",
		AIBubble:   "#1e1f1c",
	},
	"dracula": {
		Name:       "dracula",
		Background: "#282a36",
		Foreground: "#f8f8f2",
		Accent:     "#bd93f9",
		Dim:        "#6272a4",
		Border:     "#44475a",
		Success:    "#50fa7b",
		Error:      "#ff5555",
		Warning:    "#ffb86c",
		UserBubble: "#44475a",
		AIBubble:   "#21222c",
	},
	"solarized": {
		Name:       "solarized",
		Background: "#002b36",
		Foreground: "#839496",
		Accent:     "#268bd2",
		Dim:        "#586e75",
		Border:     "#073642",
		Success:    "#859900",
		Error:      "#dc322f",
		Warning:    "#b58900",
		UserBubble: "#073642",
		AIBubble:   "#002b36",
	},
	"nord": {
		Name:       "nord",
		Background: "#2e3440",
		Foreground: "#d8dee9",
		Accent:     "#88c0d0",
		Dim:        "#4c566a",
		Border:     "#3b4252",
		Success:    "#a3be8c",
		Error:      "#bf616a",
		Warning:    "#ebcb8b",
		UserBubble: "#3b4252",
		AIBubble:   "#2e3440",
	},
}

// SessionColors for /color command — simple per-session accent color.
var SessionColors = map[string]string{
	"default": "#4a9fd4",
	"gray":    "#888888",
	"red":     "#f44336",
	"green":   "#4caf50",
	"blue":    "#2196f3",
	"yellow":  "#ffeb3b",
	"purple":  "#9c27b0",
	"orange":  "#ff9800",
	"pink":    "#e91e63",
	"cyan":    "#00bcd4",
}

// Current holds the active theme state.
type Current struct {
	Theme        Theme
	SessionColor string // override accent for this session
}

// NewCurrent creates default theme state.
func NewCurrent() *Current {
	return &Current{
		Theme:        Themes["dark"],
		SessionColor: "",
	}
}

// SetTheme changes the active theme.
func (c *Current) SetTheme(name string) (string, error) {
	t, ok := Themes[strings.ToLower(name)]
	if !ok {
		var names []string
		for n := range Themes {
			names = append(names, n)
		}
		return "", fmt.Errorf("tema '%s' nao encontrado. Disponiveis: %s", name, strings.Join(names, ", "))
	}
	c.Theme = t
	return fmt.Sprintf("Tema alterado para **%s**.", t.Name), nil
}

// SetSessionColor changes the accent color for the current session.
func (c *Current) SetSessionColor(color string) (string, error) {
	if hex, ok := SessionColors[strings.ToLower(color)]; ok {
		c.SessionColor = hex
		return fmt.Sprintf("Cor da sessao: **%s**.", color), nil
	}
	// Allow raw hex
	if strings.HasPrefix(color, "#") && (len(color) == 7 || len(color) == 4) {
		c.SessionColor = color
		return fmt.Sprintf("Cor da sessao: **%s**.", color), nil
	}
	var names []string
	for n := range SessionColors {
		names = append(names, n)
	}
	return "", fmt.Errorf("cor '%s' desconhecida. Disponiveis: %s (ou hex #RRGGBB)", color, strings.Join(names, ", "))
}

// AccentColor returns the effective accent color.
func (c *Current) AccentColor() string {
	if c.SessionColor != "" {
		return c.SessionColor
	}
	return c.Theme.Accent
}

// AccentStyle returns a lipgloss style with the accent color.
func (c *Current) AccentStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(c.AccentColor()))
}

// ListThemes returns formatted list.
func ListThemes() string {
	var sb strings.Builder
	sb.WriteString("## Temas disponiveis\n\n")
	for name, t := range Themes {
		fmt.Fprintf(&sb, "- **%s**: accent=%s, bg=%s\n", name, t.Accent, t.Background)
	}
	sb.WriteString("\n## Cores de sessao\n\n")
	for name, hex := range SessionColors {
		fmt.Fprintf(&sb, "- **%s**: %s\n", name, hex)
	}
	return sb.String()
}
