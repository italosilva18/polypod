package watcher

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// AICommentPattern matches "// AI: ...", "// AI! ...", "# AI: ...", "# AI! ..."
var AICommentPattern = regexp.MustCompile(`(?m)(?://|#)\s*AI([!?:])\s*(.+)$`)

// Trigger represents an AI action triggered by a file change.
type Trigger struct {
	File    string
	Line    int
	Type    string // "!" = action, "?" = question, ":" = instruction
	Prompt  string
	Content string // surrounding code context
}

// WatchConfig configures the file watcher.
type WatchConfig struct {
	Paths      []string // directories to watch (default: ["."])
	Extensions []string // file extensions to watch (default: common code files)
	Interval   time.Duration // poll interval (default: 2s)
	IgnoreDirs []string
}

// DefaultConfig returns sensible defaults for watching.
func DefaultConfig() WatchConfig {
	return WatchConfig{
		Paths:      []string{"."},
		Extensions: []string{".go", ".py", ".js", ".ts", ".jsx", ".tsx", ".rs", ".rb", ".java", ".c", ".cpp", ".h"},
		Interval:   2 * time.Second,
		IgnoreDirs: []string{".git", "node_modules", "vendor", "__pycache__", ".venv", "dist", "build"},
	}
}

// FileState tracks modification times.
type FileState struct {
	ModTime time.Time
	Hash    string
}

// Watcher monitors files for changes and AI comment triggers.
type Watcher struct {
	config    WatchConfig
	states    map[string]FileState
	onTrigger func(Trigger)
	onChange  func(path string)
	logger    *slog.Logger
}

// New creates a file watcher.
func New(cfg WatchConfig, logger *slog.Logger) *Watcher {
	return &Watcher{
		config: cfg,
		states: make(map[string]FileState),
		logger: logger,
	}
}

// OnTrigger sets the callback for AI comment triggers.
func (w *Watcher) OnTrigger(fn func(Trigger)) { w.onTrigger = fn }

// OnChange sets the callback for any file change.
func (w *Watcher) OnChange(fn func(string)) { w.onChange = fn }

// Watch starts the polling loop. Blocks until ctx is done.
func (w *Watcher) Watch(ctx context.Context) error {
	// Initial scan
	w.scan()

	w.logger.Info("watch mode ativado", "paths", w.config.Paths, "interval", w.config.Interval)

	ticker := time.NewTicker(w.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			w.poll()
		}
	}
}

func (w *Watcher) scan() {
	for _, root := range w.config.Paths {
		filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				for _, ignore := range w.config.IgnoreDirs {
					if info.Name() == ignore {
						return filepath.SkipDir
					}
				}
				return nil
			}
			if !w.matchesExtension(path) {
				return nil
			}
			w.states[path] = FileState{ModTime: info.ModTime()}
			return nil
		})
	}
}

func (w *Watcher) poll() {
	for _, root := range w.config.Paths {
		filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				for _, ignore := range w.config.IgnoreDirs {
					if info.Name() == ignore {
						return filepath.SkipDir
					}
				}
				return nil
			}
			if !w.matchesExtension(path) {
				return nil
			}

			prev, known := w.states[path]
			if !known || info.ModTime().After(prev.ModTime) {
				w.states[path] = FileState{ModTime: info.ModTime()}

				if known {
					// File changed
					if w.onChange != nil {
						w.onChange(path)
					}
					w.checkTriggers(path)
				}
			}
			return nil
		})
	}
}

func (w *Watcher) checkTriggers(path string) {
	if w.onTrigger == nil {
		return
	}

	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		matches := AICommentPattern.FindStringSubmatch(line)
		if len(matches) < 3 {
			continue
		}

		trigger := Trigger{
			File:   path,
			Line:   lineNum,
			Type:   matches[1],
			Prompt: strings.TrimSpace(matches[2]),
		}

		// Read surrounding context (5 lines before and after)
		trigger.Content = readContext(path, lineNum, 5)

		w.logger.Info("AI trigger detectado",
			"file", path,
			"line", lineNum,
			"type", trigger.Type,
			"prompt", trigger.Prompt,
		)

		w.onTrigger(trigger)
	}
}

func (w *Watcher) matchesExtension(path string) bool {
	ext := filepath.Ext(path)
	for _, e := range w.config.Extensions {
		if ext == e {
			return true
		}
	}
	return false
}

func readContext(path string, line, radius int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")

	start := line - radius - 1
	if start < 0 {
		start = 0
	}
	end := line + radius
	if end > len(lines) {
		end = len(lines)
	}

	var sb strings.Builder
	for i := start; i < end; i++ {
		fmt.Fprintf(&sb, "%d: %s\n", i+1, lines[i])
	}
	return sb.String()
}
