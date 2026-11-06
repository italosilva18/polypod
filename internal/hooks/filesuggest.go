// Package hooks — file suggestion hook.
//
// Suggests relevant files based on conversation context. Ported from the
// TypeScript hooks/fileSuggestions.ts, adapted to the Go hooks architecture
// and PromptHookHandler interface.
package hooks

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// defaultMaxSuggestions is the maximum number of file suggestions returned.
const defaultMaxSuggestions = 15

// FileSuggestion represents a single suggested file with relevance metadata.
type FileSuggestion struct {
	// Path is the file path relative to the root directory.
	Path string
	// Reason explains why this file was suggested.
	Reason string
	// Score indicates relevance (higher is more relevant).
	Score float64
	// Preview contains the first two lines of the file.
	Preview string
}

// FileSuggestHook finds and suggests relevant files based on query text.
// It implements PromptHookHandler to inject file context into prompts.
type FileSuggestHook struct {
	rootDir        string
	maxSuggestions int
	logger         *slog.Logger

	// mu protects the cached file index.
	mu        sync.RWMutex
	fileIndex []string // cached relative paths
	indexOnce sync.Once
}

// NewFileSuggestHook creates a FileSuggestHook rooted at rootDir.
func NewFileSuggestHook(rootDir string, logger *slog.Logger) *FileSuggestHook {
	if logger == nil {
		logger = slog.Default()
	}
	return &FileSuggestHook{
		rootDir:        rootDir,
		maxSuggestions: defaultMaxSuggestions,
		logger:         logger,
	}
}

// Suggest finds files relevant to the given query string. It searches by
// filename matching against query keywords and directory structure, then
// scores and ranks results. Returns at most maxSuggestions items with file
// previews.
func (h *FileSuggestHook) Suggest(ctx context.Context, query string) []FileSuggestion {
	if query == "" {
		return nil
	}

	keywords := extractKeywords(query)
	if len(keywords) == 0 {
		return nil
	}

	files := h.getFileIndex(ctx)
	if len(files) == 0 {
		return nil
	}

	type scored struct {
		path   string
		score  float64
		reason string
	}

	var candidates []scored

	for _, relPath := range files {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		s, reason := scoreFile(relPath, keywords)
		if s > 0 {
			candidates = append(candidates, scored{path: relPath, score: s, reason: reason})
		}
	}

	// Sort descending by score, then alphabetically for stability.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].path < candidates[j].path
	})

	limit := h.maxSuggestions
	if len(candidates) < limit {
		limit = len(candidates)
	}
	candidates = candidates[:limit]

	suggestions := make([]FileSuggestion, 0, len(candidates))
	for _, c := range candidates {
		preview := readPreview(filepath.Join(h.rootDir, c.path))
		suggestions = append(suggestions, FileSuggestion{
			Path:    c.path,
			Reason:  c.reason,
			Score:   c.score,
			Preview: preview,
		})
	}

	h.logger.Debug("file suggestions generated",
		"query", query,
		"keywords", keywords,
		"candidates", len(suggestions),
	)

	return suggestions
}

// OnPrompt implements PromptHookHandler. During the "pre" phase it appends
// file suggestions derived from the prompt text as additional context.
func (h *FileSuggestHook) OnPrompt(ctx context.Context, event PromptHookEvent) PromptHookResult {
	if event.Phase != "pre" {
		return PromptHookResult{}
	}

	suggestions := h.Suggest(ctx, event.PromptText)
	if len(suggestions) == 0 {
		return PromptHookResult{}
	}

	var sb strings.Builder
	sb.WriteString("\n\n<!-- Suggested relevant files -->\n")
	for _, s := range suggestions {
		sb.WriteString(fmt.Sprintf("- %s (score: %.1f) — %s", s.Path, s.Score, s.Reason))
		if s.Preview != "" {
			sb.WriteString(fmt.Sprintf("\n  Preview: %s", s.Preview))
		}
		sb.WriteString("\n")
	}

	return PromptHookResult{
		Modified:      true,
		NewPromptText: event.PromptText + sb.String(),
	}
}

// getFileIndex returns the cached list of relative file paths under rootDir.
// The index is built once (lazily) and reused across calls.
func (h *FileSuggestHook) getFileIndex(ctx context.Context) []string {
	h.indexOnce.Do(func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.fileIndex = h.buildFileIndex(ctx)
	})

	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.fileIndex
}

// buildFileIndex walks rootDir and collects relative file paths, skipping
// common VCS and build directories.
func (h *FileSuggestHook) buildFileIndex(ctx context.Context) []string {
	var files []string

	skipDirs := map[string]bool{
		".git":         true,
		".svn":         true,
		".hg":          true,
		".bzr":         true,
		".jj":          true,
		".sl":          true,
		"node_modules": true,
		"vendor":       true,
		"__pycache__":  true,
		".cache":       true,
		".venv":        true,
	}

	_ = filepath.WalkDir(h.rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		rel, relErr := filepath.Rel(h.rootDir, path)
		if relErr != nil {
			return nil
		}
		files = append(files, rel)
		return nil
	})

	h.logger.Debug("file index built", "root", h.rootDir, "files", len(files))
	return files
}

// --- scoring helpers ---

// scoreFile computes a relevance score for relPath against the given keywords.
// Returns (score, reason). Score is 0 if no keyword matches.
func scoreFile(relPath string, keywords []string) (float64, string) {
	lowerPath := strings.ToLower(relPath)
	base := strings.ToLower(filepath.Base(relPath))
	nameNoExt := strings.TrimSuffix(base, filepath.Ext(base))
	dir := strings.ToLower(filepath.Dir(relPath))

	var totalScore float64
	var reasons []string

	for _, kw := range keywords {
		kwLower := strings.ToLower(kw)

		// Exact filename match (without extension).
		if nameNoExt == kwLower {
			totalScore += 10.0
			reasons = append(reasons, fmt.Sprintf("exact filename match: %s", kw))
			continue
		}

		// Exact basename match (with extension).
		if base == kwLower {
			totalScore += 9.0
			reasons = append(reasons, fmt.Sprintf("exact basename match: %s", kw))
			continue
		}

		// Filename contains keyword.
		if strings.Contains(base, kwLower) {
			totalScore += 5.0
			reasons = append(reasons, fmt.Sprintf("filename contains: %s", kw))
			continue
		}

		// Directory segment matches keyword.
		dirParts := strings.Split(dir, string(filepath.Separator))
		dirMatch := false
		for _, part := range dirParts {
			if part == kwLower {
				totalScore += 3.0
				reasons = append(reasons, fmt.Sprintf("directory match: %s", kw))
				dirMatch = true
				break
			}
		}
		if dirMatch {
			continue
		}

		// Full path contains keyword.
		if strings.Contains(lowerPath, kwLower) {
			totalScore += 1.0
			reasons = append(reasons, fmt.Sprintf("path contains: %s", kw))
		}
	}

	if totalScore == 0 {
		return 0, ""
	}

	// Penalise deeply nested files slightly.
	depth := float64(strings.Count(relPath, string(filepath.Separator)))
	penalty := math.Max(0, 1.0-depth*0.05)
	totalScore *= penalty

	return totalScore, strings.Join(reasons, "; ")
}

// extractKeywords splits a query string into meaningful search keywords,
// filtering out very short words and common stop words.
func extractKeywords(query string) []string {
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "is": true, "in": true,
		"to": true, "for": true, "of": true, "and": true, "or": true,
		"with": true, "this": true, "that": true, "it": true, "on": true,
		"at": true, "by": true, "from": true, "as": true, "be": true,
		"was": true, "are": true, "been": true, "being": true,
		"have": true, "has": true, "had": true, "do": true, "does": true,
		"did": true, "will": true, "would": true, "could": true,
		"should": true, "may": true, "might": true, "can": true,
		"file": true, "look": true, "find": true, "show": true,
		"me": true, "how": true, "what": true, "where": true,
	}

	// Split on whitespace and common delimiters.
	fields := strings.FieldsFunc(query, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', ',', ';', ':', '(', ')', '[', ']', '{', '}', '"', '\'', '`':
			return true
		}
		return false
	})

	var keywords []string
	seen := make(map[string]bool)
	for _, f := range fields {
		lower := strings.ToLower(f)
		if len(lower) < 2 {
			continue
		}
		if stopWords[lower] {
			continue
		}
		if seen[lower] {
			continue
		}
		seen[lower] = true
		keywords = append(keywords, f)
	}
	return keywords
}

// readPreview returns the first two lines of the file at absPath, joined
// by " | ". Returns an empty string on any error.
func readPreview(absPath string) string {
	f, err := os.Open(absPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var lines []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			lines = append(lines, line)
		}
		if len(lines) >= 2 {
			break
		}
	}

	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, " | ")
}
