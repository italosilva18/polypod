package statusline

import (
	"strings"
	"testing"
)

func TestRenderSegments_Empty(t *testing.T) {
	got := RenderSegments(nil, " | ", 80)
	if got != "" {
		t.Errorf("RenderSegments(nil) = %q; want empty", got)
	}

	got2 := RenderSegments([]Segment{}, " | ", 80)
	if got2 != "" {
		t.Errorf("RenderSegments([]) = %q; want empty", got2)
	}
}

func TestRenderSegments_Single(t *testing.T) {
	segments := []Segment{
		{Text: "hello", Min: 0, Max: 0, Align: AlignLeft},
	}
	got := RenderSegments(segments, " | ", 80)
	if got == "" {
		t.Fatal("single segment rendered empty")
	}

	plain := Strip(got)
	if !strings.Contains(plain, "hello") {
		t.Errorf("output missing 'hello': %q", plain)
	}
}

func TestRenderSegments_Multiple_Joined(t *testing.T) {
	segments := []Segment{
		{Text: "foo", Align: AlignLeft},
		{Text: "bar", Align: AlignLeft},
		{Text: "baz", Align: AlignLeft},
	}
	got := RenderSegments(segments, " | ", 0)
	plain := Strip(got)

	for _, want := range []string{"foo", "bar", "baz", " | "} {
		if !strings.Contains(plain, want) {
			t.Errorf("output missing %q: %q", want, plain)
		}
	}

	// Expect both separators
	if strings.Count(plain, " | ") != 2 {
		t.Errorf("expected 2 separators in %q", plain)
	}
}

func TestRenderSegments_WidthTruncate(t *testing.T) {
	segments := []Segment{
		{Text: "this is a fairly long piece of text", Align: AlignLeft},
		{Text: "another long chunk", Align: AlignLeft},
	}
	got := RenderSegments(segments, " | ", 20)
	if VisibleLength(got) != 20 {
		t.Errorf("VisibleLength = %d; want 20", VisibleLength(got))
	}
}

func TestRenderSegments_WidthPad(t *testing.T) {
	segments := []Segment{
		{Text: "hi", Align: AlignLeft},
	}
	got := RenderSegments(segments, " | ", 40)
	if VisibleLength(got) != 40 {
		t.Errorf("VisibleLength = %d; want 40", VisibleLength(got))
	}
}

func TestRenderSegments_SegmentMaxTruncates(t *testing.T) {
	segments := []Segment{
		{Text: "verylongtext", Max: 5, Align: AlignLeft},
	}
	got := RenderSegments(segments, "", 0)
	if VisibleLength(got) > 5 {
		t.Errorf("segment visible width = %d; want <= 5", VisibleLength(got))
	}
}

func TestRenderSegments_SegmentMinPads(t *testing.T) {
	segments := []Segment{
		{Text: "hi", Min: 10, Align: AlignLeft},
	}
	got := RenderSegments(segments, "", 0)
	if VisibleLength(got) != 10 {
		t.Errorf("segment visible width = %d; want 10", VisibleLength(got))
	}
}

func TestPadToWidth_Left(t *testing.T) {
	got := PadToWidth("hi", 10, AlignLeft)
	if VisibleLength(got) != 10 {
		t.Errorf("VisibleLength = %d; want 10", VisibleLength(got))
	}
	if !strings.HasPrefix(got, "hi") {
		t.Errorf("AlignLeft: got %q; want prefix 'hi'", got)
	}
}

func TestPadToWidth_Right(t *testing.T) {
	got := PadToWidth("hi", 10, AlignRight)
	if VisibleLength(got) != 10 {
		t.Errorf("VisibleLength = %d; want 10", VisibleLength(got))
	}
	if !strings.HasSuffix(got, "hi") {
		t.Errorf("AlignRight: got %q; want suffix 'hi'", got)
	}
}

func TestPadToWidth_Center(t *testing.T) {
	got := PadToWidth("hi", 10, AlignCenter)
	if VisibleLength(got) != 10 {
		t.Errorf("VisibleLength = %d; want 10", VisibleLength(got))
	}
	if !strings.Contains(got, "hi") {
		t.Errorf("AlignCenter: missing 'hi' in %q", got)
	}
	// Should have padding on both sides
	if !strings.HasPrefix(got, " ") || !strings.HasSuffix(got, " ") {
		t.Errorf("AlignCenter: expected space on both sides: %q", got)
	}
}

func TestPadToWidth_ExactWidth(t *testing.T) {
	// String already at target width - should be unchanged (visible-length-wise)
	got := PadToWidth("hello", 5, AlignLeft)
	if VisibleLength(got) != 5 {
		t.Errorf("VisibleLength = %d; want 5", VisibleLength(got))
	}
	// Plain "hello" stripped should still be hello
	if Strip(got) != "hello" {
		t.Errorf("got %q; want 'hello'", Strip(got))
	}
}

func TestPadToWidth_Truncates(t *testing.T) {
	got := PadToWidth("hello world", 5, AlignLeft)
	if VisibleLength(got) != 5 {
		t.Errorf("VisibleLength = %d; want 5", VisibleLength(got))
	}
}

func TestVisibleLength_Plain(t *testing.T) {
	if got := VisibleLength("hello"); got != 5 {
		t.Errorf("VisibleLength('hello') = %d; want 5", got)
	}
	if got := VisibleLength(""); got != 0 {
		t.Errorf("VisibleLength('') = %d; want 0", got)
	}
}

func TestVisibleLength_IgnoresANSI(t *testing.T) {
	withAnsi := "\x1b[31mred\x1b[0m"
	if got := VisibleLength(withAnsi); got != 3 {
		t.Errorf("VisibleLength(colored 'red') = %d; want 3", got)
	}

	complex := "\x1b[1;32mhello\x1b[0m \x1b[33mworld\x1b[0m"
	if got := VisibleLength(complex); got != 11 {
		t.Errorf("VisibleLength = %d; want 11", got)
	}
}

func TestStrip_RemovesANSI(t *testing.T) {
	withAnsi := "\x1b[31mred\x1b[0m"
	if got := Strip(withAnsi); got != "red" {
		t.Errorf("Strip = %q; want 'red'", got)
	}

	multi := "\x1b[1;32mhello\x1b[0m \x1b[33mworld\x1b[0m"
	if got := Strip(multi); got != "hello world" {
		t.Errorf("Strip = %q; want 'hello world'", got)
	}
}

func TestStrip_Plain(t *testing.T) {
	if got := Strip("plain text"); got != "plain text" {
		t.Errorf("Strip('plain text') = %q; want unchanged", got)
	}
	if got := Strip(""); got != "" {
		t.Errorf("Strip('') = %q; want empty", got)
	}
}

func TestFitToWidth_Exact(t *testing.T) {
	got := FitToWidth("hello", 5)
	if VisibleLength(got) != 5 {
		t.Errorf("VisibleLength = %d; want 5", VisibleLength(got))
	}
	if Strip(got) != "hello" {
		t.Errorf("got %q; want 'hello'", Strip(got))
	}
}

func TestFitToWidth_Truncate(t *testing.T) {
	got := FitToWidth("hello world", 5)
	if VisibleLength(got) != 5 {
		t.Errorf("VisibleLength = %d; want 5", VisibleLength(got))
	}
}

func TestFitToWidth_Pad(t *testing.T) {
	got := FitToWidth("hi", 10)
	if VisibleLength(got) != 10 {
		t.Errorf("VisibleLength = %d; want 10", VisibleLength(got))
	}
	if !strings.HasPrefix(got, "hi") {
		t.Errorf("expected 'hi' prefix; got %q", got)
	}
}

func TestWrapToWidth_LongText(t *testing.T) {
	text := "the quick brown fox jumps over the lazy dog"
	lines := WrapToWidth(text, 15)
	if len(lines) < 2 {
		t.Errorf("lines = %d; want >= 2", len(lines))
	}
	for _, line := range lines {
		if VisibleLength(line) > 15 {
			t.Errorf("line %q visible width %d > 15", line, VisibleLength(line))
		}
	}
}

func TestWrapToWidth_ShortText(t *testing.T) {
	text := "short"
	lines := WrapToWidth(text, 80)
	if len(lines) != 1 {
		t.Errorf("lines = %d; want 1", len(lines))
	}
	if len(lines) > 0 && !strings.Contains(lines[0], "short") {
		t.Errorf("line = %q; want contains 'short'", lines[0])
	}
}

func TestAlignmentConstants_Unique(t *testing.T) {
	// Ensure the three alignment values are distinct.
	vals := map[Alignment]string{
		AlignLeft:   "left",
		AlignRight:  "right",
		AlignCenter: "center",
	}
	if len(vals) != 3 {
		t.Errorf("alignment constants collide: %v", vals)
	}
}

func TestJoinHorizontal(t *testing.T) {
	got := JoinHorizontal([]string{"a", "b", "c"})
	if got != "abc" {
		t.Errorf("JoinHorizontal = %q; want 'abc'", got)
	}

	empty := JoinHorizontal(nil)
	if empty != "" {
		t.Errorf("JoinHorizontal(nil) = %q; want empty", empty)
	}
}

func TestTruncate(t *testing.T) {
	got := Truncate("hello world", 5)
	if VisibleLength(got) > 5 {
		t.Errorf("Truncate visible width = %d; want <= 5", VisibleLength(got))
	}
}
