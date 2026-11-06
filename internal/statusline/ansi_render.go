package statusline

import (
	"strings"

	"github.com/costa/polypod/internal/sliceansi"
)

// Segment is a single styled piece of a status line.
type Segment struct {
	Text  string // may contain ANSI escapes
	Min   int    // minimum visible width (pads if smaller)
	Max   int    // maximum visible width (truncates if larger)
	Align Alignment
}

// Alignment specifies how a segment is padded to reach its minimum width.
type Alignment int

const (
	AlignLeft Alignment = iota
	AlignRight
	AlignCenter
)

// RenderSegments concatenates segments with a separator, respecting
// visible width (ignoring ANSI escape sequences).
func RenderSegments(segments []Segment, separator string, totalWidth int) string {
	if len(segments) == 0 {
		return ""
	}

	pieces := make([]string, 0, len(segments))
	for _, seg := range segments {
		pieces = append(pieces, renderSegment(seg))
	}

	joined := strings.Join(pieces, separator)

	// Fit to total width
	if totalWidth > 0 {
		visible := sliceansi.VisibleWidth(joined)
		if visible > totalWidth {
			joined = sliceansi.Truncate(joined, totalWidth, "…")
		} else if visible < totalWidth {
			joined = sliceansi.PadRight(joined, totalWidth)
		}
	}

	return joined
}

// renderSegment applies min/max width to a single segment.
func renderSegment(seg Segment) string {
	text := seg.Text
	visible := sliceansi.VisibleWidth(text)

	// Truncate if over max
	if seg.Max > 0 && visible > seg.Max {
		text = sliceansi.Truncate(text, seg.Max, "…")
		visible = sliceansi.VisibleWidth(text)
	}

	// Pad if under min
	if seg.Min > 0 && visible < seg.Min {
		switch seg.Align {
		case AlignLeft:
			text = sliceansi.PadRight(text, seg.Min)
		case AlignRight:
			text = sliceansi.PadLeft(text, seg.Min)
		case AlignCenter:
			leftPad := (seg.Min - visible) / 2
			rightPad := seg.Min - visible - leftPad
			text = strings.Repeat(" ", leftPad) + text + strings.Repeat(" ", rightPad)
		}
	}

	return text
}

// VisibleLength returns the visible length of a styled string.
func VisibleLength(s string) int {
	return sliceansi.VisibleWidth(s)
}

// Strip removes all ANSI escape sequences from a string.
func Strip(s string) string {
	return sliceansi.StripANSI(s)
}

// Truncate cuts a string to the specified visible width with an ellipsis.
func Truncate(s string, width int) string {
	return sliceansi.Truncate(s, width, "…")
}

// PadToWidth pads a string to the exact visible width.
func PadToWidth(s string, width int, align Alignment) string {
	visible := sliceansi.VisibleWidth(s)
	if visible >= width {
		return sliceansi.Truncate(s, width, "…")
	}

	switch align {
	case AlignLeft:
		return sliceansi.PadRight(s, width)
	case AlignRight:
		return sliceansi.PadLeft(s, width)
	case AlignCenter:
		leftPad := (width - visible) / 2
		rightPad := width - visible - leftPad
		return strings.Repeat(" ", leftPad) + s + strings.Repeat(" ", rightPad)
	}
	return s
}

// WrapToWidth wraps a string into multiple lines of at most maxWidth visible chars.
func WrapToWidth(s string, maxWidth int) []string {
	return sliceansi.Wrap(s, maxWidth)
}

// JoinHorizontal concatenates segments side-by-side without a separator.
func JoinHorizontal(segments []string) string {
	var sb strings.Builder
	for _, s := range segments {
		sb.WriteString(s)
	}
	return sb.String()
}

// FitToWidth ensures a string is exactly the given visible width.
// Truncates with ellipsis or pads with spaces on the right.
func FitToWidth(s string, width int) string {
	visible := sliceansi.VisibleWidth(s)
	if visible > width {
		return sliceansi.Truncate(s, width, "…")
	}
	return sliceansi.PadRight(s, width)
}
