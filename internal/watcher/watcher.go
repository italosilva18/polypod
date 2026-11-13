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
	"sync"
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
	Paths      []string      // directories to watch (default: ["."])
	Extensions []string      // file extensions to watch (default: common code files)
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
	mu           sync.Mutex
	config       WatchConfig
	states       map[string]FileState
	seenTriggers map[string]map[string]int
	onTrigger    func(Trigger)
	onChange     func(path string)
	onEvent      func(FileChangeEvent)
	logger       *slog.Logger
}

// New creates a file watcher.
func New(cfg WatchConfig, logger *slog.Logger) *Watcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Watcher{
		config:       cfg,
		states:       make(map[string]FileState),
		seenTriggers: make(map[string]map[string]int),
		logger:       logger,
	}
}

// OnTrigger sets the callback for AI comment triggers.
func (w *Watcher) OnTrigger(fn func(Trigger)) { w.onTrigger = fn }

// OnChange sets the callback for any file change.
func (w *Watcher) OnChange(fn func(string)) { w.onChange = fn }

// OnEvent sets the rich callback receiving Added, Modified, and Removed file events.
func (w *Watcher) OnEvent(fn func(FileChangeEvent)) { w.onEvent = fn }

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
	w.mu.Lock()
	defer w.mu.Unlock()
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
	seenInPoll := make(map[string]bool)
	var events []FileChangeEvent
	var changedPaths []string

	w.mu.Lock()
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
			seenInPoll[path] = true

			prev, known := w.states[path]
			if !known || info.ModTime().After(prev.ModTime) {
				w.states[path] = FileState{ModTime: info.ModTime()}
				op := FileOpModified
				if !known {
					op = FileOpAdded
				}
				events = append(events, FileChangeEvent{
					Path:      path,
					Op:        op,
					Timestamp: time.Now(),
				})
				changedPaths = append(changedPaths, path)
			}
			return nil
		})
	}

	for trackedPath := range w.states {
		if !seenInPoll[trackedPath] {
			delete(w.states, trackedPath)
			delete(w.seenTriggers, trackedPath)
			events = append(events, FileChangeEvent{
				Path:      trackedPath,
				Op:        FileOpRemoved,
				Timestamp: time.Now(),
			})
		}
	}
	w.mu.Unlock()

	for _, ev := range events {
		if w.onEvent != nil {
			w.onEvent(ev)
		}
	}
	for _, path := range changedPaths {
		if w.onChange != nil {
			w.onChange(path)
		}
		// A file created after startup can also contain a new AI! action.
		w.checkTriggers(path)
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
	current := make(map[string]int)
	previous := w.seenTriggers[path]
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		matches := AICommentPattern.FindStringSubmatch(line)
		if len(matches) < 3 {
			continue
		}
		key := matches[1] + ":" + strings.TrimSpace(matches[2])
		current[key]++
		if current[key] <= previous[key] {
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
	w.seenTriggers[path] = current
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
