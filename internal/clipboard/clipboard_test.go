package clipboard

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// hasClipboardTool returns true if at least one clipboard-related binary
// is available on the host. This is used to decide whether to attempt a
// real write to the system clipboard during tests.
func hasClipboardTool() bool {
	switch runtime.GOOS {
	case "darwin", "windows":
		return true
	case "linux":
		for _, tool := range []string{"wl-copy", "xclip", "xsel"} {
			if _, err := exec.LookPath(tool); err == nil {
				return true
			}
		}
	}
	return false
}

func TestIsAvailableMatchesCopyCommand(t *testing.T) {
	got := IsAvailable()
	want := copyCommand() != nil
	if got != want {
		t.Fatalf("IsAvailable() = %v, want %v", got, want)
	}
}

func TestToolNameNonEmpty(t *testing.T) {
	name := ToolName()
	if name == "" {
		t.Fatal("ToolName() returned empty string")
	}

	switch runtime.GOOS {
	case "darwin":
		if name != "pbcopy/pbpaste" {
			t.Errorf("darwin ToolName() = %q, want pbcopy/pbpaste", name)
		}
	case "windows":
		if name != "clip.exe" {
			t.Errorf("windows ToolName() = %q, want clip.exe", name)
		}
	case "linux":
		// On linux it must be one of the known tools or "nenhum".
		valid := map[string]bool{
			"wl-copy": true,
			"xclip":   true,
			"xsel":    true,
			"nenhum":  true,
		}
		if !valid[name] {
			t.Errorf("linux ToolName() = %q, not in known set", name)
		}
	}
}

func TestCopyCommandConsistency(t *testing.T) {
	// copyCommand should return a non-empty slice whenever it returns non-nil.
	cmd := copyCommand()
	if cmd != nil && len(cmd) == 0 {
		t.Fatal("copyCommand() returned non-nil but empty slice")
	}
	// The availability check must agree with the command presence.
	if (cmd != nil) != IsAvailable() {
		t.Fatal("IsAvailable() disagrees with copyCommand() presence")
	}
}

func TestPasteCommandShape(t *testing.T) {
	cmd := pasteCommand()
	if cmd == nil {
		// Acceptable: no paste tool on this host.
		return
	}
	if len(cmd) == 0 {
		t.Fatal("pasteCommand() returned non-nil but empty slice")
	}
	if cmd[0] == "" {
		t.Fatal("pasteCommand() first element is empty string")
	}
}

func TestCopyCodeBlocksNoBlocks(t *testing.T) {
	input := "Just plain text with no code blocks at all.\nAnother line."
	out, err := CopyCodeBlocks(input)
	if err == nil {
		t.Fatalf("expected error for input without code blocks, got output %q", out)
	}
	if !strings.Contains(err.Error(), "nenhum bloco") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestCopyCodeBlocksEmptyInput(t *testing.T) {
	_, err := CopyCodeBlocks("")
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestCopyCodeBlocksUnclosedBlock(t *testing.T) {
	// An opening fence with no closing fence should yield zero completed blocks
	// because the parser only appends on the closing fence.
	input := "```go\nfmt.Println(\"hi\")\n"
	_, err := CopyCodeBlocks(input)
	if err == nil {
		t.Fatal("expected error when code block is not closed")
	}
}

func TestCopyCodeBlocksWithBlocks(t *testing.T) {
	if !hasClipboardTool() {
		t.Skip("no clipboard tool available; skipping real clipboard write")
	}
	// The actual write may still fail if there is no display/session (e.g. CI
	// without X/Wayland), so we only assert behavior shape here.
	input := "intro\n```\nhello\n```\nmiddle\n```py\nprint(1)\n```\nend"
	msg, err := CopyCodeBlocks(input)
	if err != nil {
		t.Skipf("clipboard write failed (likely no display): %v", err)
	}
	if !strings.Contains(msg, "2 bloco(s)") {
		t.Errorf("expected message to mention 2 blocks, got %q", msg)
	}
}

func TestCopyRequiresTool(t *testing.T) {
	// If no clipboard tool exists, Copy should return the well-known error.
	if copyCommand() != nil {
		t.Skip("clipboard tool present; cannot verify missing-tool error")
	}
	err := Copy("anything")
	if err == nil {
		t.Fatal("expected error when no clipboard tool is available")
	}
	if !strings.Contains(err.Error(), "nenhuma ferramenta") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestPasteRequiresTool(t *testing.T) {
	if pasteCommand() != nil {
		t.Skip("paste tool present; cannot verify missing-tool error")
	}
	_, err := Paste()
	if err == nil {
		t.Fatal("expected error when no paste tool is available")
	}
}
