package project

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/costa/polypod/internal/treeify"
)

// TreeViewOptions controls tree rendering.
type TreeViewOptions struct {
	MaxDepth     int      // 0 = unlimited
	ShowHidden   bool     // show dot-files
	MaxEntries   int      // max total entries (truncate deep trees)
	IgnoreGlobs  []string // paths to skip
	ShowFileSize bool
}

// DefaultTreeViewOptions returns sensible defaults.
func DefaultTreeViewOptions() TreeViewOptions {
	return TreeViewOptions{
		MaxDepth:   5,
		ShowHidden: false,
		MaxEntries: 500,
		IgnoreGlobs: []string{
			"node_modules", "vendor", ".git", "dist", "build",
			"target", "__pycache__", ".venv", ".next", ".cache",
		},
	}
}

// RenderDirectoryTree walks a directory and produces a treeified string.
func RenderDirectoryTree(rootDir string, opts TreeViewOptions) (string, error) {
	paths, err := walkDirectory(rootDir, opts)
	if err != nil {
		return "", err
	}

	if len(paths) == 0 {
		return "(empty)", nil
	}

	root := treeify.FromFiles(paths)

	treeOpts := treeify.DefaultOptions()
	treeOpts.ShowRoot = true
	if opts.MaxDepth > 0 {
		treeOpts.MaxDepth = opts.MaxDepth
	}

	return treeify.Render(root, treeOpts), nil
}

// RenderPackageTree renders a tree of relative file paths.
func RenderPackageTree(basePath string, relativePaths []string) string {
	if len(relativePaths) == 0 {
		return "(empty)"
	}

	sorted := make([]string, len(relativePaths))
	copy(sorted, relativePaths)
	sort.Strings(sorted)

	root := treeify.FromFiles(sorted)
	root.Label = filepath.Base(basePath)

	return treeify.Render(root, treeify.DefaultOptions())
}

// RenderFileGroups renders files grouped by category (e.g. source, tests, docs).
func RenderFileGroups(groups map[string][]string) string {
	root := &treeify.Node{Label: "Project"}

	// Sort category keys for stable output
	categories := make([]string, 0, len(groups))
	for k := range groups {
		categories = append(categories, k)
	}
	sort.Strings(categories)

	for _, cat := range categories {
		files := groups[cat]
		sort.Strings(files)
		catNode := root.AddChild(cat + " (" + countLabel(len(files)) + ")")
		for _, f := range files {
			catNode.AddChild(f)
		}
	}

	return treeify.Render(root, treeify.DefaultOptions())
}

// walkDirectory collects paths respecting the options.
func walkDirectory(rootDir string, opts TreeViewOptions) ([]string, error) {
	var paths []string
	count := 0

	ignoreSet := make(map[string]bool, len(opts.IgnoreGlobs))
	for _, g := range opts.IgnoreGlobs {
		ignoreSet[g] = true
	}

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		name := info.Name()

		// Skip ignored dirs
		if info.IsDir() {
			if ignoreSet[name] {
				return filepath.SkipDir
			}
			if !opts.ShowHidden && strings.HasPrefix(name, ".") && path != rootDir {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip hidden files
		if !opts.ShowHidden && strings.HasPrefix(name, ".") {
			return nil
		}

		// Count limit
		if opts.MaxEntries > 0 && count >= opts.MaxEntries {
			return filepath.SkipDir
		}

		rel, err := filepath.Rel(rootDir, path)
		if err != nil {
			rel = path
		}

		paths = append(paths, rel)
		count++
		return nil
	})

	if err != nil {
		return nil, err
	}
	return paths, nil
}

func countLabel(n int) string {
	if n == 1 {
		return "1 file"
	}
	return itoaSimple(n) + " files"
}

func itoaSimple(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}
