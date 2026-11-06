package memory

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Additional MemoryFileType constants for directory-based scanning.
const (
	MemoryClaudeMD  MemoryFileType = "claude_md"
	MemoryMemoryDir MemoryFileType = "memory_dir"
	MemoryAgentsMD  MemoryFileType = "agents_md"
)

// MemDirScanner scans for memory files across standard directory locations
// (CLAUDE.md, .claude/memory/, .polypod/memory/, AGENTS.md) and provides
// keyword-based relevance filtering and file-change polling.
type MemDirScanner struct {
	rootDir string
	logger  *slog.Logger
}

// NewMemDirScanner creates a MemDirScanner rooted at the given directory.
// If logger is nil a no-op logger is used.
func NewMemDirScanner(rootDir string, logger *slog.Logger) *MemDirScanner {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))
	}
	return &MemDirScanner{
		rootDir: rootDir,
		logger:  logger,
	}
}

// Scan discovers memory files in and around rootDir. It checks:
//   - CLAUDE.md in root and parent directories (up to the user's home)
//   - .claude/memory/ directory for .md files
//   - .polypod/memory/ directory for .md files
//   - AGENTS.md in root directory
//
// Files are returned in discovery order (project root first, then parents,
// then memory directories). Duplicates are deduplicated by absolute path.
func (s *MemDirScanner) Scan() ([]MemoryFile, error) {
	absRoot, err := filepath.Abs(s.rootDir)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var results []MemoryFile

	addFile := func(path string, mfType MemoryFileType, source MemoryFileSource) {
		abs, err := filepath.Abs(path)
		if err != nil || seen[abs] {
			return
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			return
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			s.logger.Debug("failed to read memory file", "path", abs, "err", err)
			return
		}
		seen[abs] = true
		results = append(results, MemoryFile{
			Path:    abs,
			Content: string(data),
			Type:    mfType,
			Source:  source,
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}

	// 1. CLAUDE.md in root directory
	addFile(filepath.Join(absRoot, "CLAUDE.md"), MemoryClaudeMD, SourceProjectRoot)

	// 2. AGENTS.md in root directory
	addFile(filepath.Join(absRoot, "AGENTS.md"), MemoryAgentsMD, SourceProjectRoot)

	// 3. .polypod.md in root directory
	addFile(filepath.Join(absRoot, ".polypod.md"), MemoryProject, SourceProjectRoot)

	// 4. CLAUDE.md in parent directories (up to home)
	home, _ := os.UserHomeDir()
	current := filepath.Dir(absRoot)
	for current != "" && current != "/" && current != filepath.Dir(current) {
		if home != "" && !strings.HasPrefix(current, home) && current != home {
			break
		}
		addFile(filepath.Join(current, "CLAUDE.md"), MemoryClaudeMD, SourceParentDir)
		if current == home {
			break
		}
		current = filepath.Dir(current)
	}

	// 5. .claude/memory/ directory
	s.scanMemoryDir(filepath.Join(absRoot, ".claude", "memory"), seen, &results)

	// 6. .polypod/memory/ directory
	s.scanMemoryDir(filepath.Join(absRoot, ".polypod", "memory"), seen, &results)

	s.logger.Debug("memdir scan complete", "root", absRoot, "files_found", len(results))
	return results, nil
}

// scanMemoryDir reads all .md files from a memory directory and appends them
// to results. Non-existent directories are silently skipped.
func (s *MemDirScanner) scanMemoryDir(dir string, seen map[string]bool, results *[]MemoryFile) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Directory doesn't exist or is unreadable — not an error.
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		abs, err := filepath.Abs(path)
		if err != nil || seen[abs] {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			s.logger.Debug("failed to read memory dir file", "path", abs, "err", err)
			continue
		}
		seen[abs] = true
		*results = append(*results, MemoryFile{
			Path:    abs,
			Content: string(data),
			Type:    MemoryMemoryDir,
			Source:  SourceProjectRoot,
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
}

// FindRelevant filters files by simple keyword matching against the query.
// It checks whether any word from the query (length > 2) appears in the
// file's path or content (case-insensitive). Files that match at least one
// keyword are returned, sorted by number of matches descending.
func (s *MemDirScanner) FindRelevant(query string, files []MemoryFile) []MemoryFile {
	if query == "" || len(files) == 0 {
		return nil
	}

	queryWords := strings.Fields(strings.ToLower(query))
	// Filter to meaningful words.
	var keywords []string
	for _, w := range queryWords {
		if len(w) > 2 {
			keywords = append(keywords, w)
		}
	}
	if len(keywords) == 0 {
		return nil
	}

	type scored struct {
		file  MemoryFile
		count int
	}
	var matched []scored

	for _, f := range files {
		contentLower := strings.ToLower(f.Content)
		pathLower := strings.ToLower(f.Path)
		count := 0
		for _, kw := range keywords {
			if strings.Contains(contentLower, kw) || strings.Contains(pathLower, kw) {
				count++
			}
		}
		if count > 0 {
			matched = append(matched, scored{file: f, count: count})
		}
	}

	// Sort by match count descending.
	for i := 0; i < len(matched); i++ {
		for j := i + 1; j < len(matched); j++ {
			if matched[j].count > matched[i].count {
				matched[i], matched[j] = matched[j], matched[i]
			}
		}
	}

	result := make([]MemoryFile, len(matched))
	for i, m := range matched {
		result[i] = m.file
	}
	return result
}

// WatchForChanges polls the filesystem every 10 seconds for changes to memory
// files. When a change is detected (new files, removed files, or modified
// files), onChange is called with the full current file list. The goroutine
// runs until ctx is cancelled.
func (s *MemDirScanner) WatchForChanges(ctx context.Context, onChange func([]MemoryFile)) {
	const pollInterval = 10 * time.Second

	// Build initial snapshot.
	prev := s.buildSnapshot()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			curr := s.buildSnapshot()
			if s.snapshotChanged(prev, curr) {
				files, err := s.Scan()
				if err != nil {
					s.logger.Debug("watch scan failed", "err", err)
					continue
				}
				onChange(files)
				prev = curr
			}
		}
	}
}

// fileSnapshot holds the size and mod time for change detection.
type fileSnapshot struct {
	size    int64
	modTime time.Time
}

// buildSnapshot stats all default memory paths and returns a map of
// path -> snapshot for change detection.
func (s *MemDirScanner) buildSnapshot() map[string]fileSnapshot {
	paths := DefaultMemoryPaths(s.rootDir)
	snap := make(map[string]fileSnapshot, len(paths))
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if info.IsDir() {
			// For directories, scan their entries.
			entries, err := os.ReadDir(p)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
					continue
				}
				ep := filepath.Join(p, e.Name())
				ei, err := e.Info()
				if err != nil {
					continue
				}
				snap[ep] = fileSnapshot{size: ei.Size(), modTime: ei.ModTime()}
			}
		} else {
			snap[p] = fileSnapshot{size: info.Size(), modTime: info.ModTime()}
		}
	}
	return snap
}

// snapshotChanged returns true if prev and curr differ in keys or values.
func (s *MemDirScanner) snapshotChanged(prev, curr map[string]fileSnapshot) bool {
	if len(prev) != len(curr) {
		return true
	}
	for k, v := range curr {
		pv, ok := prev[k]
		if !ok || pv.size != v.size || !pv.modTime.Equal(v.modTime) {
			return true
		}
	}
	return false
}

// DefaultMemoryPaths returns the standard paths to check for memory files
// relative to rootDir. These include individual files and memory directories.
func DefaultMemoryPaths(rootDir string) []string {
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		abs = rootDir
	}
	return []string{
		filepath.Join(abs, "CLAUDE.md"),
		filepath.Join(abs, "AGENTS.md"),
		filepath.Join(abs, ".polypod.md"),
		filepath.Join(abs, ".claude", "memory"),
		filepath.Join(abs, ".polypod", "memory"),
	}
}

// MemoryAge returns how long ago the memory file was last modified.
// Returns zero duration if ModTime is the zero value.
func MemoryAge(f MemoryFile) time.Duration {
	if f.ModTime.IsZero() {
		return 0
	}
	age := time.Since(f.ModTime)
	if age < 0 {
		return 0
	}
	return age
}
