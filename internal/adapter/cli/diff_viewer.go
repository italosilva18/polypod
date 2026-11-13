package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/costa/polypod/internal/turncheckpoint"
)

type diffFile struct {
	path    string
	patch   string
	warning string
}

type diffViewer struct {
	root      string
	scope     string
	files     []diffFile
	selected  int
	scroll    int
	width     int
	height    int
	err       string
	truncated bool
	turnDiff  func() ([]turncheckpoint.FileDiff, error)
}

func newDiffViewer(root string, turnDiff func() ([]turncheckpoint.FileDiff, error)) *diffViewer {
	v := &diffViewer{root: root, turnDiff: turnDiff}
	v.load("working")
	return v
}

func (v *diffViewer) load(scope string) {
	v.scope, v.selected, v.scroll, v.err, v.truncated = scope, 0, 0, "", false
	v.files = nil
	if scope == "turn" {
		if v.turnDiff == nil {
			v.err = "Histórico de alterações por turno indisponível."
			return
		}
		files, err := v.turnDiff()
		if err != nil {
			v.err = err.Error()
			return
		}
		for _, file := range files {
			name := file.Path
			if rel, err := filepath.Rel(v.root, file.Path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				name = rel
			}
			patch := strings.ReplaceAll(file.Patch, file.Path, name)
			v.files = append(v.files, diffFile{path: name, patch: patch, warning: file.Warning})
		}
		return
	}
	var args []string
	switch scope {
	case "working":
		args = []string{"diff", "--no-ext-diff", "--no-color", "HEAD"}
	case "staged":
		args = []string{"diff", "--no-ext-diff", "--no-color", "--cached"}
	case "branch":
		base := v.branchBase()
		if base == "" {
			v.err = "Não foi possível encontrar o ramo base (origin/HEAD, main ou master)."
			return
		}
		args = []string{"diff", "--no-ext-diff", "--no-color", base + "...HEAD"}
	}
	output, truncated, err := captureDiff(v.root, args...)
	if err != nil && scope == "working" {
		output, truncated, err = captureDiff(v.root, "diff", "--no-ext-diff", "--no-color")
	}
	if err != nil {
		v.err = err.Error() + ": " + strings.TrimSpace(output)
		return
	}
	v.truncated = truncated
	v.files = splitDiffFiles(output)
	if scope == "working" {
		v.addUntracked()
	}
}

const maxDiffBytes = 2 * 1024 * 1024

type boundedDiffOutput struct {
	mu sync.Mutex
	bytes.Buffer
	truncated bool
}

func (b *boundedDiffOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if b.Len()+len(p) > maxDiffBytes {
		b.truncated = true
	}
	if b.Len() < maxDiffBytes {
		keep := min(len(p), maxDiffBytes-b.Len())
		if _, err := b.Buffer.Write(p[:keep]); err != nil {
			return 0, err
		}
	}
	return n, nil
}

func captureDiff(dir string, args ...string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.WaitDelay = 2 * time.Second
	var out boundedDiffOutput
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return out.String(), out.truncated, err
}

func (v *diffViewer) branchBase() string {
	if base, err := runGitCapture(v.root, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if base = strings.TrimSpace(base); base != "" {
			return base
		}
	}
	for _, base := range []string{"main", "master"} {
		if _, err := runGitCapture(v.root, "rev-parse", "--verify", "--quiet", base); err == nil {
			return base
		}
	}
	return ""
}

func (v *diffViewer) addUntracked() {
	cmd := exec.Command("git", "ls-files", "--others", "--exclude-standard", "-z")
	cmd.Dir = v.root
	out, err := cmd.Output()
	if err != nil {
		return
	}
	for _, name := range strings.Split(string(out), "\x00") {
		if len(v.files) >= 200 {
			v.truncated = true
			break
		}
		if name == "" || filepath.IsAbs(name) || strings.Contains(name, "..") {
			continue
		}
		path := filepath.Join(v.root, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 128*1024 {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil || strings.ContainsRune(string(data), '\x00') {
			continue
		}
		var patch strings.Builder
		fmt.Fprintf(&patch, "--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", name, len(strings.Split(string(data), "\n")))
		for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
			patch.WriteByte('+')
			patch.WriteString(line)
			patch.WriteByte('\n')
		}
		v.files = append(v.files, diffFile{path: name, patch: patch.String()})
	}
}

func splitDiffFiles(diff string) []diffFile {
	var files []diffFile
	diff = strings.TrimPrefix(diff, "diff --git ")
	for _, part := range strings.Split(diff, "\ndiff --git ") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		patch := "diff --git " + part
		path := "arquivo"
		for _, line := range strings.Split(patch, "\n") {
			if strings.HasPrefix(line, "+++ b/") {
				path = strings.TrimPrefix(line, "+++ b/")
				break
			}
		}
		if path == "arquivo" {
			if header := strings.SplitN(part, "\n", 2)[0]; strings.Contains(header, " b/") {
				path = strings.TrimPrefix(header[strings.LastIndex(header, " b/")+1:], "b/")
			}
		}
		files = append(files, diffFile{path: path, patch: patch})
	}
	return files
}

func (v *diffViewer) key(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyEsc:
		return true
	case tea.KeyUp:
		v.selectFile(-1)
	case tea.KeyDown:
		v.selectFile(1)
	case tea.KeyPgUp:
		v.scroll = max(0, v.scroll-max(1, v.height-8))
	case tea.KeyPgDown:
		v.scroll += max(1, v.height-8)
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "q":
			return true
		case "n":
			v.selectFile(1)
		case "p":
			v.selectFile(-1)
		case "j":
			v.scroll++
		case "k":
			v.scroll = max(0, v.scroll-1)
		case "1":
			v.load("working")
		case "2":
			v.load("staged")
		case "3":
			v.load("branch")
		case "4":
			v.load("turn")
		}
	}
	return false
}

func (v *diffViewer) selectFile(delta int) {
	if len(v.files) == 0 {
		return
	}
	v.selected = min(max(0, v.selected+delta), len(v.files)-1)
	v.scroll = 0
}

func (v *diffViewer) View(width, height int) string {
	if width < 1 || height < 1 {
		return ""
	}
	title := "Diferenças · 1 trabalho  2 preparado  3 ramo  4 último turno · n/p arquivo  j/k rolar  Esc sair"
	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render(ansi.Truncate(title, width, "…")))
	b.WriteByte('\n')
	scopeLabel := map[string]string{"working": "trabalho", "staged": "preparado", "branch": "ramo", "turn": "último turno"}[v.scope]
	b.WriteString(subtleStyle.Render(fmt.Sprintf("Escopo: %s · %d arquivo(s)", scopeLabel, len(v.files))))
	b.WriteByte('\n')
	if v.truncated {
		b.WriteString(notifyWarnStyle.Render("Diferenças extensas: visualização limitada."))
		b.WriteByte('\n')
	}
	if v.err != "" {
		b.WriteString(errorStyle.Render(ansi.Truncate(v.err, width, "…")))
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Top, b.String())
	}
	if len(v.files) == 0 {
		b.WriteString("\nNenhuma alteração neste escopo.")
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Top, b.String())
	}
	file := v.files[v.selected]
	b.WriteString(cmdBadgeStyle.Render(fmt.Sprintf("%d/%d %s", v.selected+1, len(v.files), file.path)))
	b.WriteByte('\n')
	if file.warning != "" {
		b.WriteString(notifyWarnStyle.Render(ansi.Truncate(file.warning, width, "…")))
		b.WriteByte('\n')
	}
	lines := strings.Split(strings.TrimSuffix(file.patch, "\n"), "\n")
	available := max(1, height-5)
	if v.truncated {
		available = max(1, available-1)
	}
	v.scroll = min(v.scroll, max(0, len(lines)-available))
	for _, line := range lines[v.scroll:min(len(lines), v.scroll+available)] {
		line = ansi.Truncate(line, max(1, width-2), "…")
		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			b.WriteString(diffAddStyle.Render(line))
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			b.WriteString(diffDelStyle.Render(line))
		case strings.HasPrefix(line, "@@"):
			b.WriteString(cmdBadgeStyle.Render(line))
		default:
			b.WriteString(line)
		}
		b.WriteByte('\n')
	}
	return lipgloss.Place(width, height, lipgloss.Left, lipgloss.Top, b.String())
}
