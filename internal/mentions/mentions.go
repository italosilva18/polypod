package mentions

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// MentionPattern matches @path/to/file, @dir/, and optional #start-end lines.
var MentionPattern = regexp.MustCompile(`@([\w./_-]+(?:\.\w+|/))(?:#([0-9]+)(?:-([0-9]+))?)?`)

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
	rootPath, err := filepath.EvalSymlinks(root)
	if err != nil {
		return text, nil
	}

	var expanded []string
	result := text

	for _, match := range matches {
		path := match[1]
		if strings.Contains(path, "..") || filepath.IsAbs(path) {
			continue
		}
		fullPath := filepath.Join(root, path)
		resolved, err := filepath.EvalSymlinks(fullPath)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(rootPath, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}

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
			result = strings.Replace(result, match[0], replacement, 1)
			expanded = append(expanded, path)
		} else {
			// Read file content
			data, err := os.ReadFile(fullPath)
			if err != nil {
				continue
			}
			content := string(data)
			label := path
			if match[2] != "" {
				start, startErr := strconv.Atoi(match[2])
				end := start
				var endErr error
				if match[3] != "" {
					end, endErr = strconv.Atoi(match[3])
				}
				lines := strings.Split(content, "\n")
				if startErr != nil || endErr != nil || start < 1 || end < start || start > len(lines) || end > len(lines) || end-start > 499 {
					result = strings.Replace(result, match[0], fmt.Sprintf("[Intervalo de linhas inválido: %s]", match[0]), 1)
					continue
				}
				content = strings.Join(lines[start-1:end], "\n")
				label = strings.TrimPrefix(match[0], "@")
			}
			if len(content) > 10000 {
				content = content[:10000] + "\n... [truncado]"
			}
			replacement := fmt.Sprintf("[Arquivo %s]\n```\n%s\n```", label, content)
			result = strings.Replace(result, match[0], replacement, 1)
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
