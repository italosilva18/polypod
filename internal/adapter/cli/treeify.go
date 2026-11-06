package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// TreeNode is a single node in a renderable tree. Children are drawn
// in slice order. A nil Style means the label is rendered without any
// extra ANSI styling.
type TreeNode struct {
	Label    string
	Children []*TreeNode
	Style    *TreeNodeStyle
}

// TreeNodeStyle controls how a node's label is rendered. Color is an
// ANSI hex/name string compatible with lipgloss.Color().
type TreeNodeStyle struct {
	Color string // e.g. "#57E5E5" or "cyan"
	Bold  bool
}

// Box-drawing prefixes used by the tree renderer.
const (
	treeBranchMid  = "├── "
	treeBranchLast = "└── "
	treeIndentMid  = "│   "
	treeIndentLast = "    "
)

// Treeify renders the given tree as a multi-line string. The root node
// itself is printed on the first line and its children follow with the
// usual box-drawing prefixes.
//
// A nil root yields an empty string. The renderer is non-recursive in
// its public API but uses a recursive helper internally; depth is
// effectively bounded only by available stack.
func Treeify(root *TreeNode) string {
	if root == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(styleNodeLabel(root))
	b.WriteString("\n")
	writeChildren(&b, root.Children, "")
	return b.String()
}

// writeChildren is the recursive helper that draws each child of the
// current level using the current accumulated prefix.
func writeChildren(b *strings.Builder, children []*TreeNode, prefix string) {
	for i, child := range children {
		isLast := i == len(children)-1
		branch := treeBranchMid
		nextIndent := treeIndentMid
		if isLast {
			branch = treeBranchLast
			nextIndent = treeIndentLast
		}
		b.WriteString(prefix)
		b.WriteString(branch)
		b.WriteString(styleNodeLabel(child))
		b.WriteString("\n")
		if len(child.Children) > 0 {
			writeChildren(b, child.Children, prefix+nextIndent)
		}
	}
}

// styleNodeLabel applies the optional Style on a node's label. The
// returned string contains ANSI escapes when Style is non-nil; an empty
// label is returned unchanged.
func styleNodeLabel(n *TreeNode) string {
	if n == nil {
		return ""
	}
	if n.Style == nil {
		return n.Label
	}
	st := lipgloss.NewStyle()
	if n.Style.Color != "" {
		st = st.Foreground(lipgloss.Color(n.Style.Color))
	}
	if n.Style.Bold {
		st = st.Bold(true)
	}
	return st.Render(n.Label)
}

// TreeifyMap renders a map[string]any as a tree. Nested maps become
// subtrees; slices are rendered as a sequence of indexed children;
// scalar values are stringified using fmt.Sprint. Keys are sorted so
// the output is stable across runs.
//
// The title becomes the root label. Pass an empty string to get a
// root-less output (the children are rendered without any header).
func TreeifyMap(m map[string]any, title string) string {
	root := &TreeNode{Label: title}
	addMapChildren(root, m)
	if title == "" {
		// Render the children directly without a root header so the
		// caller can embed the tree under its own title.
		var b strings.Builder
		writeChildren(&b, root.Children, "")
		return b.String()
	}
	return Treeify(root)
}

// addMapChildren walks a map and appends a child node for each key in
// sorted order, recursing into nested maps and slices.
func addMapChildren(parent *TreeNode, m map[string]any) {
	if len(m) == 0 {
		return
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v := m[k]
		child := &TreeNode{Label: k}
		switch typed := v.(type) {
		case map[string]any:
			addMapChildren(child, typed)
		case []any:
			for i, item := range typed {
				idxNode := &TreeNode{Label: fmt.Sprintf("[%d]", i)}
				addAnyChild(idxNode, item)
				child.Children = append(child.Children, idxNode)
			}
		default:
			child.Label = fmt.Sprintf("%s: %v", k, v)
		}
		parent.Children = append(parent.Children, child)
	}
}

// addAnyChild handles a single slice element, recursing for nested
// maps/slices and stringifying scalars onto the parent label.
func addAnyChild(parent *TreeNode, v any) {
	switch typed := v.(type) {
	case map[string]any:
		addMapChildren(parent, typed)
	case []any:
		for i, item := range typed {
			idxNode := &TreeNode{Label: fmt.Sprintf("[%d]", i)}
			addAnyChild(idxNode, item)
			parent.Children = append(parent.Children, idxNode)
		}
	default:
		parent.Label = fmt.Sprintf("%s = %v", parent.Label, v)
	}
}

// TreeifyFiles renders a list of file paths as a directory tree. Paths
// are interpreted with '/' as the separator (POSIX style) and may use
// either relative or absolute notation. The output uses box-drawing
// characters compatible with the rest of the TUI.
//
// Example input:
//
//	["src/main.go", "src/utils/helpers.go", "README.md"]
//
// Output (formatted):
//
//	├── src/
//	│   ├── main.go
//	│   └── utils/
//	│       └── helpers.go
//	└── README.md
//
// Empty input yields an empty string. Duplicate paths are de-duplicated.
func TreeifyFiles(paths []string) string {
	if len(paths) == 0 {
		return ""
	}

	// Build a nested map of name -> children. Files have a nil
	// children map; directories have a non-nil one.
	type dirEntry struct {
		name     string
		children map[string]*dirEntry
		isDir    bool
	}

	root := &dirEntry{children: map[string]*dirEntry{}, isDir: true}

	insert := func(parts []string) {
		cur := root
		for i, part := range parts {
			if part == "" {
				continue
			}
			isDir := i < len(parts)-1
			child, ok := cur.children[part]
			if !ok {
				child = &dirEntry{
					name:  part,
					isDir: isDir,
				}
				if isDir {
					child.children = map[string]*dirEntry{}
				}
				cur.children[part] = child
			}
			// If a name appears as both file and dir (rare), upgrade
			// to dir so the deeper insertion has somewhere to land.
			if isDir && !child.isDir {
				child.isDir = true
				if child.children == nil {
					child.children = map[string]*dirEntry{}
				}
			}
			cur = child
		}
	}

	for _, p := range paths {
		// Normalise: trim leading "./", convert backslashes, drop
		// any trailing slash so the splitter does not produce an
		// empty trailing element.
		clean := strings.ReplaceAll(p, "\\", "/")
		clean = strings.TrimPrefix(clean, "./")
		clean = strings.TrimSuffix(clean, "/")
		if clean == "" {
			continue
		}
		parts := strings.Split(clean, "/")
		insert(parts)
	}

	// Recursive renderer over the dirEntry tree.
	var b strings.Builder
	var render func(node *dirEntry, prefix string)
	render = func(node *dirEntry, prefix string) {
		// Sort children: directories first, then files, both
		// alphabetically.
		names := make([]string, 0, len(node.children))
		for name := range node.children {
			names = append(names, name)
		}
		sort.Slice(names, func(i, j int) bool {
			a := node.children[names[i]]
			c := node.children[names[j]]
			if a.isDir != c.isDir {
				return a.isDir // dirs come first
			}
			return names[i] < names[j]
		})

		for i, name := range names {
			child := node.children[name]
			isLast := i == len(names)-1
			branch := treeBranchMid
			nextIndent := treeIndentMid
			if isLast {
				branch = treeBranchLast
				nextIndent = treeIndentLast
			}
			b.WriteString(prefix)
			b.WriteString(branch)
			label := name
			if child.isDir {
				label = name + "/"
			}
			b.WriteString(label)
			b.WriteString("\n")
			if child.isDir && len(child.children) > 0 {
				render(child, prefix+nextIndent)
			}
		}
	}

	render(root, "")
	return b.String()
}
