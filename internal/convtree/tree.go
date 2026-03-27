package convtree

import (
	"fmt"
	"strings"
	"time"
)

// Node is a message in the conversation tree.
type Node struct {
	ID       string
	ParentID string
	Role     string
	Content  string
	Time     time.Time
	Children []*Node
}

// Tree manages a branching conversation.
type Tree struct {
	Root    *Node
	Nodes   map[string]*Node
	Current *Node
	nextID  int
}

// NewTree creates an empty conversation tree.
func NewTree() *Tree {
	root := &Node{ID: "root", Role: "system", Content: "root"}
	t := &Tree{
		Root:    root,
		Nodes:   map[string]*Node{"root": root},
		Current: root,
	}
	return t
}

// AddMessage adds a message as a child of the current node.
func (t *Tree) AddMessage(role, content string) *Node {
	t.nextID++
	node := &Node{
		ID:       fmt.Sprintf("msg_%d", t.nextID),
		ParentID: t.Current.ID,
		Role:     role,
		Content:  content,
		Time:     time.Now(),
	}
	t.Current.Children = append(t.Current.Children, node)
	t.Nodes[node.ID] = node
	t.Current = node
	return node
}

// ForkAt creates a new branch at the given node ID.
func (t *Tree) ForkAt(nodeID string) (*Node, error) {
	node, ok := t.Nodes[nodeID]
	if !ok {
		return nil, fmt.Errorf("no '%s' nao encontrado", nodeID)
	}
	t.Current = node
	return node, nil
}

// GoTo moves the current pointer to a specific node.
func (t *Tree) GoTo(nodeID string) error {
	node, ok := t.Nodes[nodeID]
	if !ok {
		return fmt.Errorf("no '%s' nao encontrado", nodeID)
	}
	t.Current = node
	return nil
}

// PathToRoot returns all nodes from current to root (for context building).
func (t *Tree) PathToRoot() []*Node {
	var path []*Node
	node := t.Current
	for node != nil && node.ID != "root" {
		path = append([]*Node{node}, path...)
		parent, ok := t.Nodes[node.ParentID]
		if !ok {
			break
		}
		node = parent
	}
	return path
}

// Branches returns the number of branches (nodes with >1 child).
func (t *Tree) Branches() int {
	count := 0
	for _, n := range t.Nodes {
		if len(n.Children) > 1 {
			count++
		}
	}
	return count
}

// FormatTree renders the tree as ASCII art.
func (t *Tree) FormatTree() string {
	var sb strings.Builder
	sb.WriteString("## Arvore de conversa\n\n")
	t.renderNode(&sb, t.Root, "", true, t.Current.ID)
	sb.WriteString(fmt.Sprintf("\n%d mensagens, %d branch(es)\n", len(t.Nodes)-1, t.Branches()))
	return sb.String()
}

func (t *Tree) renderNode(sb *strings.Builder, node *Node, prefix string, isLast bool, currentID string) {
	if node.ID == "root" {
		sb.WriteString("*\n")
	} else {
		connector := "|-- "
		if isLast {
			connector = "\\-- "
		}
		marker := "  "
		if node.ID == currentID {
			marker = "> "
		}
		snippet := node.Content
		if len(snippet) > 50 {
			snippet = snippet[:50] + "..."
		}
		snippet = strings.ReplaceAll(snippet, "\n", " ")
		fmt.Fprintf(sb, "%s%s%s[%s] %s\n", prefix, connector, marker, node.Role, snippet)
	}

	childPrefix := prefix
	if node.ID != "root" {
		if isLast {
			childPrefix += "    "
		} else {
			childPrefix += "|   "
		}
	}

	for i, child := range node.Children {
		isChildLast := i == len(node.Children)-1
		t.renderNode(sb, child, childPrefix, isChildLast, currentID)
	}
}

// Siblings returns sibling branches at the current node's parent.
func (t *Tree) Siblings() []*Node {
	if t.Current.ParentID == "" {
		return nil
	}
	parent, ok := t.Nodes[t.Current.ParentID]
	if !ok {
		return nil
	}
	return parent.Children
}

// NextSibling moves to the next sibling branch.
func (t *Tree) NextSibling() error {
	siblings := t.Siblings()
	for i, s := range siblings {
		if s.ID == t.Current.ID && i+1 < len(siblings) {
			t.Current = siblings[i+1]
			return nil
		}
	}
	return fmt.Errorf("nenhum sibling disponivel")
}

// PrevSibling moves to the previous sibling branch.
func (t *Tree) PrevSibling() error {
	siblings := t.Siblings()
	for i, s := range siblings {
		if s.ID == t.Current.ID && i > 0 {
			t.Current = siblings[i-1]
			return nil
		}
	}
	return fmt.Errorf("nenhum sibling anterior")
}
