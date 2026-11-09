package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Instruction is the content of one AGENTS.md file, identified by its path.
type Instruction struct {
	Path    string
	Content string
}

type instructionCacheEntry struct {
	exists   bool
	size     int64
	modified time.Time
	content  string
}

// InstructionCollector loads AGENTS.md files for the current working tree.
// A stat on every lookup invalidates changed and deleted files immediately.
type InstructionCollector struct {
	root  string
	mu    sync.Mutex
	cache map[string]instructionCacheEntry
}

// NewInstructionCollector anchors relative tool paths to root.
func NewInstructionCollector(root string) (*InstructionCollector, error) {
	if root == "" {
		root = "."
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve instruction root: %w", err)
	}
	return &InstructionCollector{root: filepath.Clean(absolute), cache: make(map[string]instructionCacheEntry)}, nil
}

// Root returns the directory used to resolve relative paths.
func (c *InstructionCollector) Root() string { return c.root }

// ForPath returns applicable AGENTS.md files from the outer directory to the
// nearest directory. A file path inherits instructions from its parent.
func (c *InstructionCollector) ForPath(path string) ([]Instruction, error) {
	if path == "" {
		path = c.root
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.root, path)
	}
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	dir := path
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		dir = filepath.Dir(path)
	}

	// Paths under the working root inherit only the root and descendants.
	// An absolute path outside the root uses its own ancestor chain.
	stop := c.root
	if relative, err := filepath.Rel(c.root, dir); err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		stop = filepath.VolumeName(dir) + string(filepath.Separator)
	}
	var directories []string
	for current := dir; ; current = filepath.Dir(current) {
		directories = append(directories, current)
		if current == stop || filepath.Dir(current) == current {
			break
		}
	}

	var instructions []Instruction
	var readErrors []error
	for index := len(directories) - 1; index >= 0; index-- {
		file := filepath.Join(directories[index], "AGENTS.md")
		content, exists, err := c.read(file)
		if err != nil {
			readErrors = append(readErrors, err)
			continue
		}
		if exists && strings.TrimSpace(content) != "" {
			instructions = append(instructions, Instruction{Path: file, Content: content})
		}
	}
	return instructions, errors.Join(readErrors...)
}

func (c *InstructionCollector) read(path string) (string, bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		c.mu.Lock()
		c.cache[path] = instructionCacheEntry{}
		c.mu.Unlock()
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		return "", false, nil
	}
	c.mu.Lock()
	cached, ok := c.cache[path]
	if ok && cached.exists && cached.size == info.Size() && cached.modified.Equal(info.ModTime()) {
		c.mu.Unlock()
		return cached.content, true, nil
	}
	c.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", path, err)
	}
	// Cache only under the same identity observed before reading. If an edit
	// races with this read, the next lookup will see the newer identity.
	if updated, statErr := os.Stat(path); statErr == nil &&
		updated.Size() == info.Size() && updated.ModTime().Equal(info.ModTime()) {
		info = updated
	}
	content := string(data)
	c.mu.Lock()
	c.cache[path] = instructionCacheEntry{exists: true, size: info.Size(), modified: info.ModTime(), content: content}
	c.mu.Unlock()
	return content, true, nil
}

type instructionScopeKey struct{}

type instructionScope struct {
	collector *InstructionCollector
	mu        sync.Mutex
	seen      map[string]string
}

// WithInstructionScope starts one turn of instruction delivery. The same
// context must be passed to the model and its tool calls.
func WithInstructionScope(ctx context.Context, collector *InstructionCollector) context.Context {
	if collector == nil {
		return ctx
	}
	return context.WithValue(ctx, instructionScopeKey{}, &instructionScope{
		collector: collector,
		seen:      make(map[string]string),
	})
}

// RootInstructions returns root AGENTS.md content and marks it as delivered.
func RootInstructions(ctx context.Context) (string, error) {
	scope, _ := ctx.Value(instructionScopeKey{}).(*instructionScope)
	if scope == nil {
		return "", nil
	}
	items, err := scope.collector.ForPath(scope.collector.root)
	return scope.formatNew(items), err
}

func (s *instructionScope) formatNew(items []Instruction) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out strings.Builder
	for _, item := range items {
		if s.seen[item.Path] == item.Content {
			continue
		}
		s.seen[item.Path] = item.Content
		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		fmt.Fprintf(&out, "Instruções de %s:\n%s", item.Path, item.Content)
	}
	return out.String()
}

// InstructionsAfterTool supplies newly discovered directory instructions in
// the next tool observation. It never gates execution of the tool itself.
func InstructionsAfterTool(ctx context.Context, name string, args map[string]any) (string, error) {
	scope, _ := ctx.Value(instructionScopeKey{}).(*instructionScope)
	if scope == nil {
		return "", nil
	}
	paths := instructionPathsForTool(name, args)
	var out strings.Builder
	var readErrors []error
	for _, path := range paths {
		items, err := scope.collector.ForPath(path)
		if err != nil {
			readErrors = append(readErrors, err)
		}
		newContent := scope.formatNew(items)
		if newContent != "" {
			if out.Len() > 0 {
				out.WriteString("\n\n")
			}
			out.WriteString(newContent)
		}
	}
	return out.String(), errors.Join(readErrors...)
}

func instructionPathsForTool(name string, args map[string]any) []string {
	if args == nil {
		return nil
	}
	name = strings.ToLower(name)
	local := strings.Contains(name, "file") || strings.Contains(name, "edit") ||
		strings.Contains(name, "read") || strings.Contains(name, "write") ||
		strings.Contains(name, "glob") || strings.Contains(name, "grep") ||
		strings.Contains(name, "search") || strings.Contains(name, "notebook") ||
		strings.Contains(name, "document") || strings.Contains(name, "vision") ||
		strings.Contains(name, "lsp") ||
		strings.Contains(name, "bash") || strings.Contains(name, "powershell") ||
		strings.Contains(name, "git")
	if !local {
		return nil
	}
	var paths []string
	seen := make(map[string]bool)
	var walk func(map[string]any, int)
	walk = func(values map[string]any, depth int) {
		if depth > 3 {
			return
		}
		for key, raw := range values {
			switch key {
			case "file_path", "notebook_path", "path", "directory", "working_dir", "cwd":
				path, _ := raw.(string)
				if path != "" && !strings.Contains(path, "://") && !seen[path] {
					seen[path] = true
					paths = append(paths, path)
				}
			case "files", "edits", "paths":
				if list, ok := raw.([]any); ok {
					for _, entry := range list {
						if nested, ok := entry.(map[string]any); ok {
							walk(nested, depth+1)
						} else if path, ok := entry.(string); ok && path != "" && !seen[path] {
							seen[path] = true
							paths = append(paths, path)
						}
					}
				}
			}
		}
	}
	walk(args, 0)
	sort.Strings(paths)
	return paths
}
