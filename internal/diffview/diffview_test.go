package diffview

import (
	"strings"
	"testing"
)

func TestFormatColorDiff_Headers(t *testing.T) {
	out := FormatColorDiff("a\n", "b\n", "file.txt")

	if !strings.Contains(out, "--- a/file.txt") {
		t.Errorf("missing old header, got: %q", out)
	}
	if !strings.Contains(out, "+++ b/file.txt") {
		t.Errorf("missing new header, got: %q", out)
	}
}

func TestFormatColorDiff_Identical(t *testing.T) {
	content := "line1\nline2\nline3"
	out := FormatColorDiff(content, content, "same.go")

	// No red or green lines should appear (no additions/removals)
	if strings.Contains(out, "\033[31m- ") {
		t.Errorf("unexpected removal in identical diff: %q", out)
	}
	if strings.Contains(out, "\033[32m+ ") {
		t.Errorf("unexpected addition in identical diff: %q", out)
	}
	// Unchanged lines should be present with double-space prefix
	if !strings.Contains(out, "  line1\n") {
		t.Errorf("expected unchanged 'line1', got: %q", out)
	}
	if !strings.Contains(out, "  line2\n") {
		t.Errorf("expected unchanged 'line2', got: %q", out)
	}
}

func TestFormatColorDiff_Addition(t *testing.T) {
	out := FormatColorDiff("hello", "hello\nworld", "add.txt")

	if !strings.Contains(out, "\033[32m+ world\033[0m") {
		t.Errorf("expected green addition of 'world', got: %q", out)
	}
	if !strings.Contains(out, "  hello\n") {
		t.Errorf("expected unchanged 'hello', got: %q", out)
	}
}

func TestFormatColorDiff_Removal(t *testing.T) {
	out := FormatColorDiff("keep\ndrop", "keep", "rm.txt")

	if !strings.Contains(out, "\033[31m- drop\033[0m") {
		t.Errorf("expected red removal of 'drop', got: %q", out)
	}
	if !strings.Contains(out, "  keep\n") {
		t.Errorf("expected unchanged 'keep', got: %q", out)
	}
}

func TestFormatColorDiff_Replacement(t *testing.T) {
	out := FormatColorDiff("foo", "bar", "swap.txt")

	if !strings.Contains(out, "\033[31m- foo\033[0m") {
		t.Errorf("expected '- foo' in diff, got: %q", out)
	}
	if !strings.Contains(out, "\033[32m+ bar\033[0m") {
		t.Errorf("expected '+ bar' in diff, got: %q", out)
	}
}

func TestFormatPreview_ContainsPathAndLines(t *testing.T) {
	out := FormatPreview("cmd/main.go", "old line", "new line")

	if !strings.Contains(out, "cmd/main.go") {
		t.Errorf("expected path in preview, got: %q", out)
	}
	if !strings.Contains(out, "\033[31m- old line\033[0m") {
		t.Errorf("expected red old line, got: %q", out)
	}
	if !strings.Contains(out, "\033[32m+ new line\033[0m") {
		t.Errorf("expected green new line, got: %q", out)
	}
	if !strings.Contains(out, "Alteracao proposta") {
		t.Errorf("expected header text, got: %q", out)
	}
}

func TestFormatPreview_MultilineText(t *testing.T) {
	out := FormatPreview("a.txt", "one\ntwo", "uno\ndos\ntres")

	if !strings.Contains(out, "- one") {
		t.Errorf("expected '- one', got: %q", out)
	}
	if !strings.Contains(out, "- two") {
		t.Errorf("expected '- two', got: %q", out)
	}
	if !strings.Contains(out, "+ uno") {
		t.Errorf("expected '+ uno', got: %q", out)
	}
	if !strings.Contains(out, "+ dos") {
		t.Errorf("expected '+ dos', got: %q", out)
	}
	if !strings.Contains(out, "+ tres") {
		t.Errorf("expected '+ tres', got: %q", out)
	}
}

func TestFormatPreview_HasSeparators(t *testing.T) {
	out := FormatPreview("x.go", "a", "b")

	// Should contain the horizontal separator line twice (top and bottom)
	sep := "────────────────────────────────────────"
	count := strings.Count(out, sep)
	if count != 2 {
		t.Errorf("expected 2 separator lines, got %d in: %q", count, out)
	}
}

func TestFormatFilesDiff_Summary(t *testing.T) {
	diffs := []FileDiff{
		{Path: "a.go", Added: 10, Removed: 2},
		{Path: "b.go", Added: 3, Removed: 5},
	}
	out := FormatFilesDiff(diffs)

	if !strings.Contains(out, "a.go") {
		t.Errorf("expected 'a.go' in output, got: %q", out)
	}
	if !strings.Contains(out, "b.go") {
		t.Errorf("expected 'b.go' in output, got: %q", out)
	}
	if !strings.Contains(out, "+10 -2") {
		t.Errorf("expected '+10 -2' for a.go, got: %q", out)
	}
	if !strings.Contains(out, "+3 -5") {
		t.Errorf("expected '+3 -5' for b.go, got: %q", out)
	}
	// Total: 13 added, 7 removed, 2 files
	if !strings.Contains(out, "2 arquivo(s)") {
		t.Errorf("expected '2 arquivo(s)' in total, got: %q", out)
	}
	if !strings.Contains(out, "+13") {
		t.Errorf("expected total '+13', got: %q", out)
	}
	if !strings.Contains(out, "-7") {
		t.Errorf("expected total '-7', got: %q", out)
	}
}

func TestFormatFilesDiff_Empty(t *testing.T) {
	out := FormatFilesDiff([]FileDiff{})

	if !strings.Contains(out, "0 arquivo(s)") {
		t.Errorf("expected '0 arquivo(s)' for empty diff, got: %q", out)
	}
	if !strings.Contains(out, "Arquivos alterados") {
		t.Errorf("expected header, got: %q", out)
	}
}

func TestMakeBar_ZeroTotal(t *testing.T) {
	bar := makeBar(0, 0, 10)
	expected := strings.Repeat("░", 10)
	if bar != expected {
		t.Errorf("zero total: expected %q, got %q", expected, bar)
	}
}

func TestMakeBar_OnlyAdditions(t *testing.T) {
	bar := makeBar(5, 0, 10)
	// All green, no red
	if !strings.Contains(bar, "\033[32m") {
		t.Errorf("expected green ansi code, got: %q", bar)
	}
	greenBlocks := strings.Count(bar, "█")
	if greenBlocks != 10 {
		t.Errorf("expected 10 blocks, got %d in: %q", greenBlocks, bar)
	}
}

func TestMakeBar_OnlyRemovals(t *testing.T) {
	bar := makeBar(0, 5, 10)
	if !strings.Contains(bar, "\033[31m") {
		t.Errorf("expected red ansi code, got: %q", bar)
	}
	blocks := strings.Count(bar, "█")
	if blocks != 10 {
		t.Errorf("expected 10 blocks total, got %d in: %q", blocks, bar)
	}
}

func TestMakeBar_MixedWidth(t *testing.T) {
	bar := makeBar(5, 5, 20)
	// Must have both colors and reset
	if !strings.Contains(bar, "\033[32m") {
		t.Errorf("expected green code, got: %q", bar)
	}
	if !strings.Contains(bar, "\033[31m") {
		t.Errorf("expected red code, got: %q", bar)
	}
	if !strings.Contains(bar, "\033[0m") {
		t.Errorf("expected reset code, got: %q", bar)
	}
	blocks := strings.Count(bar, "█")
	if blocks != 20 {
		t.Errorf("expected 20 blocks total, got %d in: %q", blocks, bar)
	}
}

func TestContainsAfter_Found(t *testing.T) {
	lines := []string{"a", "b", "c", "d"}
	if !containsAfter(lines, 0, "c") {
		t.Error("expected to find 'c' starting from index 0")
	}
	if !containsAfter(lines, 2, "c") {
		t.Error("expected to find 'c' starting from index 2")
	}
}

func TestContainsAfter_NotFound(t *testing.T) {
	lines := []string{"a", "b", "c"}
	if containsAfter(lines, 0, "z") {
		t.Error("did not expect to find 'z'")
	}
	// Searching past the target
	if containsAfter(lines, 2, "a") {
		t.Error("did not expect to find 'a' starting from index 2")
	}
}

func TestContainsAfter_LookaheadLimit(t *testing.T) {
	// Function only looks up to 5 ahead
	lines := []string{"x", "1", "2", "3", "4", "5", "target"}
	// From index 0, target is at offset 6 — beyond the 5-line lookahead
	if containsAfter(lines, 0, "target") {
		t.Error("expected lookahead limit to exclude 'target' 6 positions away")
	}
	// From index 2, target is at offset 4 — within the lookahead
	if !containsAfter(lines, 2, "target") {
		t.Error("expected to find 'target' within lookahead window")
	}
}
