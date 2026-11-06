package sliceansi

import "testing"

const (
	red   = "\x1b[31m"
	reset = "\x1b[0m"
	bold  = "\x1b[1m"
)

func TestVisibleWidth(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"hello", 5},
		{red + "hello" + reset, 5},
		{bold + red + "hello" + reset, 5},
		{"a" + red + "b" + reset + "c", 3},
		{"日本語", 6}, // CJK chars are width 2 each
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := VisibleWidth(tt.input); got != tt.want {
				t.Errorf("VisibleWidth(%q) = %d; want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestStripANSI(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{red + "hello" + reset, "hello"},
		{bold + red + "world" + reset, "world"},
		{"\x1b[31mred\x1b[0m text", "red text"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := StripANSI(tt.input); got != tt.want {
				t.Errorf("StripANSI(%q) = %q; want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSlice(t *testing.T) {
	tests := []struct {
		input      string
		start, end int
		want       string
	}{
		{"hello", 0, 5, "hello"},
		{"hello", 0, 3, "hel"},
		{"hello", 2, 5, "llo"},
		{"hello", 1, 4, "ell"},
		{"", 0, 5, ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := Slice(tt.input, tt.start, tt.end)
			if got != tt.want {
				t.Errorf("Slice(%q, %d, %d) = %q; want %q",
					tt.input, tt.start, tt.end, got, tt.want)
			}
		})
	}
}

func TestPadRight(t *testing.T) {
	tests := []struct {
		input string
		width int
		want  string
	}{
		{"hi", 5, "hi   "},
		{"hello", 5, "hello"},
		{"hello world", 5, "hello world"}, // already over
		{"", 3, "   "},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := PadRight(tt.input, tt.width)
			if got != tt.want {
				t.Errorf("PadRight(%q, %d) = %q; want %q",
					tt.input, tt.width, got, tt.want)
			}
		})
	}
}

func TestPadLeft(t *testing.T) {
	tests := []struct {
		input string
		width int
		want  string
	}{
		{"hi", 5, "   hi"},
		{"hello", 5, "hello"},
		{"", 3, "   "},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := PadLeft(tt.input, tt.width)
			if got != tt.want {
				t.Errorf("PadLeft(%q, %d) = %q; want %q",
					tt.input, tt.width, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		width    int
		ellipsis string
	}{
		{"hello world", 20, "..."},
		{"hello world", 8, "..."},
		{"hello", 5, "..."},
		{"hello", 3, ".."}, // ellipsis >= width
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := Truncate(tt.input, tt.width, tt.ellipsis)
			// Verify visible width is at most the limit
			if VisibleWidth(got) > tt.width {
				t.Errorf("Truncate(%q, %d) = %q (width %d); exceeds limit",
					tt.input, tt.width, got, VisibleWidth(got))
			}
		})
	}
}

func TestWrap(t *testing.T) {
	text := "the quick brown fox jumps over the lazy dog"
	lines := Wrap(text, 15)

	if len(lines) < 2 {
		t.Errorf("Wrap should produce multiple lines, got %d", len(lines))
	}

	for _, line := range lines {
		if VisibleWidth(line) > 15 {
			t.Errorf("line %q exceeds width 15", line)
		}
	}
}

func TestSliceWithANSI(t *testing.T) {
	// The slice should preserve ANSI escapes
	input := red + "hello" + reset
	got := Slice(input, 0, 3)

	// Visible result should be just "hel" but the escape sequences should be there
	visible := StripANSI(got)
	if visible != "hel" {
		t.Errorf("Slice visible content = %q; want hel", visible)
	}
}

func TestVisibleWidthCombining(t *testing.T) {
	// Combining diacritical mark (U+0301) has width 0
	text := "a\u0301" // 'a' with acute accent
	if got := VisibleWidth(text); got != 1 {
		t.Errorf("VisibleWidth combining = %d; want 1", got)
	}
}
