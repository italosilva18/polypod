package skill

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FSSkillLoader walks one or more "skill root" directories and parses every
// SKILL.md (or *.skill.md) file it finds.
//
// It is the Go equivalent of the TS loadSkillsDir.ts scanner — much smaller
// because we only care about the subset of metadata expressed in Frontmatter.
type FSSkillLoader struct {
	mu           sync.Mutex
	Roots        []string
	PollInterval time.Duration
}

// NewFSSkillLoader builds a loader seeded with an optional list of roots.
// Pass no arguments to start empty and add roots later via AddRoot, or use
// DefaultSkillDirs() to populate the common locations.
func NewFSSkillLoader(roots ...string) *FSSkillLoader {
	cp := make([]string, 0, len(roots))
	cp = append(cp, roots...)
	return &FSSkillLoader{Roots: cp, PollInterval: time.Second}
}

// AddRoot appends a directory to the scan list. Safe for concurrent use.
func (l *FSSkillLoader) AddRoot(root string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Roots = append(l.Roots, root)
}

// DefaultSkillDirs returns typical skill locations, in priority order:
//  1. ~/.claude/skills — per-user global skills
//  2. ./claude/skills — project-local skills (legacy layout)
//  3. ./.polypod/skills — Polypod-specific project skills
//
// Missing directories are fine; LoadAll silently ignores roots that do not
// exist so callers can always pass the full set.
func DefaultSkillDirs() []string {
	dirs := make([]string, 0, 3)
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs, filepath.Join(home, ".claude", "skills"))
	}
	dirs = append(dirs, filepath.Join("claude", "skills"))
	dirs = append(dirs, filepath.Join(".polypod", "skills"))
	return dirs
}

// LoadAll walks every configured root and returns every successfully parsed
// skill. Per-file errors are accumulated with errors.Join — the walk never
// bails on the first bad file, so one malformed frontmatter can't hide the
// rest of the user's skills.
//
// The context is checked between roots and between files so large trees can
// be cancelled promptly.
func (l *FSSkillLoader) LoadAll(ctx context.Context) ([]*ParsedSkill, error) {
	l.mu.Lock()
	roots := make([]string, len(l.Roots))
	copy(roots, l.Roots)
	l.mu.Unlock()

	var (
		out  []*ParsedSkill
		errs []error
		seen = make(map[string]struct{})
	)

	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		info, err := os.Stat(root)
		if err != nil {
			// Non-existent roots are not an error: the default list
			// intentionally includes paths that may never be created.
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			errs = append(errs, fmt.Errorf("stat skill root %s: %w", root, err))
			continue
		}
		if !info.IsDir() {
			errs = append(errs, fmt.Errorf("skill root %s is not a directory", root))
			continue
		}

		walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				errs = append(errs, fmt.Errorf("walk %s: %w", path, walkErr))
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !isSkillFile(d.Name()) {
				return nil
			}
			abs, absErr := filepath.Abs(path)
			if absErr != nil {
				abs = path
			}
			if _, dup := seen[abs]; dup {
				return nil
			}
			seen[abs] = struct{}{}

			content, readErr := os.ReadFile(path)
			if readErr != nil {
				errs = append(errs, fmt.Errorf("read skill %s: %w", path, readErr))
				return nil
			}
			parsed, parseErr := ParseSkillFile(abs, content)
			if parseErr != nil {
				errs = append(errs, parseErr)
				return nil
			}
			out = append(out, parsed)
			return nil
		})
		if walkErr != nil && !errors.Is(walkErr, context.Canceled) && !errors.Is(walkErr, context.DeadlineExceeded) {
			errs = append(errs, fmt.Errorf("walk root %s: %w", root, walkErr))
		}
		if walkErr != nil && (errors.Is(walkErr, context.Canceled) || errors.Is(walkErr, context.DeadlineExceeded)) {
			return out, walkErr
		}
	}

	return out, errors.Join(errs...)
}

// Watch polls all configured SKILL.md files and calls onChange whenever a
// skill is added or its content changes. It blocks until ctx is cancelled.
func (l *FSSkillLoader) Watch(ctx context.Context, onChange func(*ParsedSkill)) error {
	if onChange == nil {
		return errors.New("skill: onChange callback is required")
	}
	interval := l.PollInterval
	if interval <= 0 {
		interval = time.Second
	}

	known := make(map[string]fileWatchState)
	scan := func(notify bool) error {
		l.mu.Lock()
		roots := append([]string(nil), l.Roots...)
		l.mu.Unlock()
		for _, root := range roots {
			err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					if errors.Is(walkErr, fs.ErrNotExist) {
						return nil
					}
					return walkErr
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if entry.IsDir() || !isSkillFile(entry.Name()) {
					return nil
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				state := fileWatchState{modTime: info.ModTime(), size: info.Size()}
				if previous, ok := known[path]; ok && previous == state {
					return nil
				}
				known[path] = state
				if !notify {
					return nil
				}
				content, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				parsed, err := ParseSkillFile(path, content)
				if err != nil {
					return err
				}
				onChange(parsed)
				return nil
			})
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("watch skill root %s: %w", root, err)
			}
		}
		return nil
	}

	if err := scan(false); err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := scan(true); err != nil {
				return err
			}
		}
	}
}

type fileWatchState struct {
	modTime time.Time
	size    int64
}

// isSkillFile returns true for file names that the loader treats as skill
// manifests: the canonical SKILL.md, or any *.skill.md sidecar.
func isSkillFile(name string) bool {
	if name == "SKILL.md" {
		return true
	}
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".skill.md") {
		return true
	}
	return false
}
