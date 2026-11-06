// Package ansirender converts ANSI-colored terminal output to SVG.
package ansirender

import (
	"fmt"
	"os"
	"strings"
)

// RenderConfig holds the configuration for SVG rendering.
type RenderConfig struct {
	FontFamily string  // CSS font family (default "monospace")
	FontSize   int     // font size in pixels (default 14)
	LineHeight float64 // line height multiplier (default 1.2)
	Padding    int     // padding in pixels (default 10)
	Background string  // background color (default "#1e1e1e")
	Width      int     // width in columns (default 80)
}

// DefaultConfig returns a RenderConfig with sensible defaults.
func DefaultConfig() RenderConfig {
	return RenderConfig{
		FontFamily: "monospace",
		FontSize:   14,
		LineHeight: 1.2,
		Padding:    10,
		Background: "#1e1e1e",
		Width:      80,
	}
}

// ansiColors maps standard ANSI foreground color codes (30-37) to hex values.
var ansiColors = map[int]string{
	30: "#000",
	31: "#c00",
	32: "#0c0",
	33: "#cc0",
	34: "#00c",
	35: "#c0c",
	36: "#0cc",
	37: "#ccc",
}

// ansiBgColors maps standard ANSI background color codes (40-47) to hex values.
var ansiBgColors = map[int]string{
	40: "#000",
	41: "#c00",
	42: "#0c0",
	43: "#cc0",
	44: "#00c",
	45: "#c0c",
	46: "#0cc",
	47: "#ccc",
}

// span represents a styled fragment of text within a single line.
type span struct {
	text string
	fg   string // foreground hex color, empty means default
	bg   string // background hex color, empty means none
	bold bool
}

// parseANSI splits ANSI-escaped text into lines of styled spans.
func parseANSI(input string) [][]span {
	var (
		lines   [][]span
		current []span
		buf     strings.Builder
		fg      string
		bg      string
		bold    bool
	)

	flush := func() {
		if buf.Len() > 0 {
			current = append(current, span{
				text: buf.String(),
				fg:   fg,
				bg:   bg,
				bold: bold,
			})
			buf.Reset()
		}
	}

	runes := []rune(input)
	i := 0
	for i < len(runes) {
		ch := runes[i]

		// Detect ESC [ ... m sequences.
		if ch == '\x1b' && i+1 < len(runes) && runes[i+1] == '[' {
			flush()
			// Find the terminating 'm'.
			j := i + 2
			for j < len(runes) && runes[j] != 'm' {
				j++
			}
			if j < len(runes) {
				seq := string(runes[i+2 : j])
				codes := parseSGRCodes(seq)
				for _, code := range codes {
					switch {
					case code == 0:
						fg = ""
						bg = ""
						bold = false
					case code == 1:
						bold = true
					case code >= 30 && code <= 37:
						if c, ok := ansiColors[code]; ok {
							fg = c
						}
					case code >= 40 && code <= 47:
						if c, ok := ansiBgColors[code]; ok {
							bg = c
						}
					}
				}
				i = j + 1
				continue
			}
		}

		if ch == '\n' {
			flush()
			lines = append(lines, current)
			current = nil
			i++
			continue
		}

		buf.WriteRune(ch)
		i++
	}

	flush()
	lines = append(lines, current)
	return lines
}

// parseSGRCodes splits a semicolon-separated SGR parameter string into ints.
func parseSGRCodes(seq string) []int {
	if seq == "" {
		return []int{0}
	}
	parts := strings.Split(seq, ";")
	codes := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			codes = append(codes, 0)
			continue
		}
		var v int
		for _, r := range p {
			if r >= '0' && r <= '9' {
				v = v*10 + int(r-'0')
			}
		}
		codes = append(codes, v)
	}
	return codes
}

// escapeXML replaces characters that must be escaped in SVG/XML text content.
func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

// ToSVG converts ANSI-escaped text to an SVG string.
func ToSVG(ansiText string, cfg RenderConfig) string {
	if cfg.FontFamily == "" {
		cfg.FontFamily = "monospace"
	}
	if cfg.FontSize == 0 {
		cfg.FontSize = 14
	}
	if cfg.LineHeight == 0 {
		cfg.LineHeight = 1.2
	}
	if cfg.Padding == 0 {
		cfg.Padding = 10
	}
	if cfg.Background == "" {
		cfg.Background = "#1e1e1e"
	}
	if cfg.Width == 0 {
		cfg.Width = 80
	}

	lines := parseANSI(ansiText)
	lineHeightPx := float64(cfg.FontSize) * cfg.LineHeight

	// Approximate character width for a monospace font (~0.6 * fontSize).
	charWidth := float64(cfg.FontSize) * 0.6
	svgWidth := 2*cfg.Padding + int(charWidth*float64(cfg.Width))
	svgHeight := 2*cfg.Padding + int(lineHeightPx*float64(len(lines)))

	var sb strings.Builder

	sb.WriteString(fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d">`+"\n",
		svgWidth, svgHeight,
	))
	sb.WriteString(fmt.Sprintf(
		`<rect width="100%%" height="100%%" fill="%s"/>`+"\n",
		cfg.Background,
	))

	defaultFG := "#ccc"

	for lineIdx, spans := range lines {
		y := float64(cfg.Padding) + lineHeightPx*float64(lineIdx+1) - (lineHeightPx-float64(cfg.FontSize))/2

		// Render background rectangles for spans that have a bg color.
		xOffset := float64(cfg.Padding)
		for _, sp := range spans {
			w := charWidth * float64(len([]rune(sp.text)))
			if sp.bg != "" {
				rectY := float64(cfg.Padding) + lineHeightPx*float64(lineIdx)
				sb.WriteString(fmt.Sprintf(
					`<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="%s"/>`,
					xOffset, rectY, w, lineHeightPx, sp.bg,
				))
				sb.WriteByte('\n')
			}
			xOffset += w
		}

		// Build the <text> element with <tspan> children for each styled run.
		sb.WriteString(fmt.Sprintf(
			`<text x="%d" y="%.1f" font-family="%s" font-size="%d" fill="%s">`,
			cfg.Padding, y, cfg.FontFamily, cfg.FontSize, defaultFG,
		))

		for _, sp := range spans {
			color := sp.fg
			if color == "" {
				color = defaultFG
			}
			weight := "normal"
			if sp.bold {
				weight = "bold"
			}
			sb.WriteString(fmt.Sprintf(
				`<tspan fill="%s" font-weight="%s">%s</tspan>`,
				color, weight, escapeXML(sp.text),
			))
		}

		sb.WriteString("</text>\n")
	}

	sb.WriteString("</svg>\n")
	return sb.String()
}

// ToSVGFile renders ANSI text to SVG and writes it to the specified file path.
func ToSVGFile(ansiText, path string, cfg RenderConfig) error {
	svg := ToSVG(ansiText, cfg)
	return os.WriteFile(path, []byte(svg), 0o644)
}
