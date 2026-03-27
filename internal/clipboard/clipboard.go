package clipboard

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Copy copies text to the system clipboard.
func Copy(text string) error {
	cmd := copyCommand()
	if cmd == nil {
		return fmt.Errorf("nenhuma ferramenta de clipboard encontrada (instale xclip, xsel ou wl-copy)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	c.Stdin = strings.NewReader(text)
	return c.Run()
}

// Paste reads text from the system clipboard.
func Paste() (string, error) {
	cmd := pasteCommand()
	if cmd == nil {
		return "", fmt.Errorf("nenhuma ferramenta de clipboard encontrada")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	out, err := c.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// CopyCodeBlocks extracts and copies only code blocks from markdown text.
func CopyCodeBlocks(text string) (string, error) {
	var blocks []string
	inBlock := false
	var current strings.Builder

	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "```") {
			if inBlock {
				blocks = append(blocks, current.String())
				current.Reset()
				inBlock = false
			} else {
				inBlock = true
			}
			continue
		}
		if inBlock {
			if current.Len() > 0 {
				current.WriteByte('\n')
			}
			current.WriteString(line)
		}
	}

	if len(blocks) == 0 {
		return "", fmt.Errorf("nenhum bloco de codigo encontrado")
	}

	combined := strings.Join(blocks, "\n\n")
	if err := Copy(combined); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d bloco(s) de codigo copiado(s) (%d bytes)", len(blocks), len(combined)), nil
}

// IsAvailable checks if clipboard tools are installed.
func IsAvailable() bool {
	cmd := copyCommand()
	return cmd != nil
}

// ToolName returns the name of the available clipboard tool.
func ToolName() string {
	switch runtime.GOOS {
	case "darwin":
		return "pbcopy/pbpaste"
	case "linux":
		for _, tool := range []string{"wl-copy", "xclip", "xsel"} {
			if _, err := exec.LookPath(tool); err == nil {
				return tool
			}
		}
	case "windows":
		return "clip.exe"
	}
	return "nenhum"
}

func copyCommand() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"pbcopy"}
	case "linux":
		if _, err := exec.LookPath("wl-copy"); err == nil {
			return []string{"wl-copy"}
		}
		if _, err := exec.LookPath("xclip"); err == nil {
			return []string{"xclip", "-selection", "clipboard"}
		}
		if _, err := exec.LookPath("xsel"); err == nil {
			return []string{"xsel", "--clipboard", "--input"}
		}
	case "windows":
		return []string{"clip.exe"}
	}
	return nil
}

func pasteCommand() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"pbpaste"}
	case "linux":
		if _, err := exec.LookPath("wl-paste"); err == nil {
			return []string{"wl-paste"}
		}
		if _, err := exec.LookPath("xclip"); err == nil {
			return []string{"xclip", "-selection", "clipboard", "-o"}
		}
		if _, err := exec.LookPath("xsel"); err == nil {
			return []string{"xsel", "--clipboard", "--output"}
		}
	case "windows":
		return []string{"powershell.exe", "-command", "Get-Clipboard"}
	}
	return nil
}
