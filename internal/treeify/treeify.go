// Package treeify provides ASCII tree rendering.
package treeify

import (
	"fmt"
	"sort"
	"strings"
)

// Node represents a tree node.
type Node struct {
	Label    string
	Children []*Node
	Meta     any
}

// Options controls rendering.
type Options struct {
	Indent   string                // Characters used per indent level (default: "  ")
	ColorFn  func(n *Node) string  // Optional per-node color function
	MaxDepth int                   // 0 = unlimited
	ShowRoot bool                  // If true, render the root label; otherwise just children
	SortFn   func(a, b *Node) bool // Optional sort function for children
}

// DefaultOptions returns sane defaults.
func DefaultOptions() Options {
	return Options{
		Indent:   "  ",
		ShowRoot: true,
	}
}

// Render renders the tree as a multi-line string using box-drawing characters.
func Render(root *Node, opts Options) string {
	if root == nil {
		return ""
	}
	if opts.Indent == "" {
		opts.Indent = "  "
	}
	var sb strings.Builder
	if opts.ShowRoot {
		writeNodeLabel(&sb, root, opts)
		sb.WriteByte('\n')
	}
	renderChildren(&sb, root.Children, "", opts, 0)
	return strings.TrimRight(sb.String(), "\n")
}

// RenderSimple is a convenience wrapper with default options.
func RenderSimple(root *Node) string {
	return Render(root, DefaultOptions())
}

func renderChildren(sb *strings.Builder, children []*Node, prefix string, opts Options, depth int) {
	if opts.MaxDepth > 0 && depth >= opts.MaxDepth {
		return
	}

	// Sort if requested
	if opts.SortFn != nil {
		sorted := make([]*Node, len(children))
		copy(sorted, children)
		sort.Slice(sorted, func(i, j int) bool {
			return opts.SortFn(sorted[i], sorted[j])
		})
		children = sorted
	}

	for i, child := range children {
		isLast := i == len(children)-1

		// Branch prefix
		var branch, childPrefix string
		if isLast {
			branch = "└── "
			childPrefix = "    "
		} else {
			branch = "├── "
			childPrefix = "│   "
		}

		sb.WriteString(prefix)
		sb.WriteString(branch)
		writeNodeLabel(sb, child, opts)
		sb.WriteByte('\n')

		if len(child.Children) > 0 {
			renderChildren(sb, child.Children, prefix+childPrefix, opts, depth+1)
		}
	}
}

func writeNodeLabel(sb *strings.Builder, n *Node, opts Options) {
	label := n.Label
	if opts.ColorFn != nil {
		color := opts.ColorFn(n)
		if color != "" {
			sb.WriteString(color)
			sb.WriteString(label)
			sb.WriteString("\x1b[0m") // reset
			return
		}
	}
	sb.WriteString(label)
}

// AddChild appends a child node and returns it.
func (n *Node) AddChild(label string) *Node {
	child := &Node{Label: label}
	n.Children = append(n.Children, child)
	return child
}

// AddChildWithMeta appends a child with metadata and returns it.
func (n *Node) AddChildWithMeta(label string, meta any) *Node {
	child := &Node{Label: label, Meta: meta}
	n.Children = append(n.Children, child)
	return child
}

// Count returns the total number of nodes in the tree (including root).
func (n *Node) Count() int {
	if n == nil {
		return 0
	}
	total := 1
	for _, c := range n.Children {
		total += c.Count()
	}
	return total
}

// Depth returns the maximum depth of the tree (0 = just root).
func (n *Node) Depth() int {
	if n == nil || len(n.Children) == 0 {
		return 0
	}
	max := 0
	for _, c := range n.Children {
		d := c.Depth() + 1
		if d > max {
			max = d
		}
	}
	return max
}

// Walk calls fn for each node in DFS order.
func (n *Node) Walk(fn func(node *Node, depth int)) {
	n.walk(fn, 0)
}

func (n *Node) walk(fn func(*Node, int), depth int) {
	if n == nil {
		return
	}
	fn(n, depth)
	for _, c := range n.Children {
		c.walk(fn, depth+1)
	}
}

// Find searches for a node with the given label (first match, DFS).
func (n *Node) Find(label string) *Node {
	if n == nil {
		return nil
	}
	if n.Label == label {
		return n
	}
	for _, c := range n.Children {
		if found := c.Find(label); found != nil {
			return found
		}
	}
	return nil
}

// FromMap builds a tree from a nested map[string]any.
func FromMap(title string, m map[string]any) *Node {
	root := &Node{Label: title}
	buildFromMap(root, m)
	return root
}

func buildFromMap(parent *Node, m map[string]any) {
	// Sort keys for stable output
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v := m[k]
		switch vv := v.(type) {
		case map[string]any:
			child := parent.AddChild(k)
			buildFromMap(child, vv)
		case []any:
			child := parent.AddChild(fmt.Sprintf("%s (%d)", k, len(vv)))
			for i, item := range vv {
				if sub, ok := item.(map[string]any); ok {
					subChild := child.AddChild(fmt.Sprintf("[%d]", i))
					buildFromMap(subChild, sub)
				} else {
					child.AddChild(fmt.Sprintf("%v", item))
				}
			}
		default:
			parent.AddChild(fmt.Sprintf("%s: %v", k, v))
		}
	}
}

// FromFiles builds a directory tree from a list of file paths.
func FromFiles(paths []string) *Node {
	root := &Node{Label: "."}

	for _, path := range paths {
		parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
		current := root
		for _, part := range parts {
			if part == "" {
				continue
			}
			// Find or create child
			var existing *Node
			for _, c := range current.Children {
				if c.Label == part {
					existing = c
					break
				}
			}
			if existing == nil {
				existing = current.AddChild(part)
			}
			current = existing
		}
	}
	return root
}
