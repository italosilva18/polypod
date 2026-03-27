package mentions

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// MentionPattern matches @path/to/file or @dir/ in text.
var MentionPattern = regexp.MustCompile(`@([\w./_-]+(?:\.\w+|/))`)

// FileIndex is a pre-built index of files for autocomplete.
type FileIndex struct {
	root  string
	files []string
}

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true,
	"__pycache__": true, ".venv": true, "dist": true,
	"build": true, ".next": true, "target": true,
}

// NewFileIndex builds an index of files in the given root.
func NewFileIndex(root string) *FileIndex {
	idx := &FileIndex{root: root}
	idx.Rebuild()
	return idx
}

// Rebuild re-scans the filesystem.
func (idx *FileIndex) Rebuild() {
	idx.files = nil
	filepath.Walk(idx.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(idx.root, path)
		idx.files = append(idx.files, rel)
		return nil
	})
	sort.Strings(idx.files)
}

// Complete returns file paths matching the given prefix (for tab-completion).
func (idx *FileIndex) Complete(prefix string) []string {
	prefix = strings.TrimPrefix(prefix, "@")
	var matches []string
	for _, f := range idx.files {
		if strings.HasPrefix(f, prefix) || strings.Contains(f, prefix) {
			matches = append(matches, f)
			if len(matches) >= 20 {
				break
			}
		}
	}
	return matches
}

// FuzzyComplete returns fuzzy-matched file paths.
func (idx *FileIndex) FuzzyComplete(query string) []string {
	query = strings.ToLower(strings.TrimPrefix(query, "@"))
	type scored struct {
		path  string
		score int
	}
	var results []scored

	for _, f := range idx.files {
		lower := strings.ToLower(f)
		score := 0

		// Exact prefix match
		if strings.HasPrefix(lower, query) {
			score += 100
		}
		// Contains
		if strings.Contains(lower, query) {
			score += 50
		}
		// Basename match
		if strings.Contains(strings.ToLower(filepath.Base(f)), query) {
			score += 75
		}
		// Fuzzy subsequence
		if score == 0 && fuzzyMatch(query, lower) {
			score += 25
		}

		if score > 0 {
			results = append(results, scored{f, score})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	var matches []string
	for i, r := range results {
		if i >= 15 {
			break
		}
		matches = append(matches, r.path)
	}
	return matches
}

// ExpandMentions replaces @path references in text with file contents.
func ExpandMentions(text string, root string) (string, []string) {
	matches := MentionPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return text, nil
	}

	var expanded []string
	result := text

	for _, match := range matches {
		path := match[1]
		fullPath := filepath.Join(root, path)

		// Check if it's a directory
		info, err := os.Stat(fullPath)
		if err != nil {
			continue
		}

		if info.IsDir() {
			// List directory contents
			entries, err := os.ReadDir(fullPath)
			if err != nil {
				continue
			}
			var listing []string
			for _, e := range entries {
				listing = append(listing, e.Name())
			}
			replacement := fmt.Sprintf("[Diretorio %s]\n%s", path, strings.Join(listing, "\n"))
			result = strings.Replace(result, "@"+path, replacement, 1)
			expanded = append(expanded, path)
		} else {
			// Read file content
			data, err := os.ReadFile(fullPath)
			if err != nil {
				continue
			}
			content := string(data)
			if len(content) > 10000 {
				content = content[:10000] + "\n... [truncado]"
			}
			replacement := fmt.Sprintf("[Arquivo %s]\n```\n%s\n```", path, content)
			result = strings.Replace(result, "@"+path, replacement, 1)
			expanded = append(expanded, path)
		}
	}

	return result, expanded
}

func fuzzyMatch(query, target string) bool {
	qi := 0
	for _, c := range target {
		if qi < len(query) && byte(c) == query[qi] {
			qi++
		}
	}
	return qi == len(query)
}
