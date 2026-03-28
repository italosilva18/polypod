package convtree

import "testing"

func TestTreeBasic(t *testing.T) {
	tree := NewTree()

	// Add messages
	tree.AddMessage("user", "hello")
	tree.AddMessage("assistant", "hi there")

	if len(tree.Nodes) != 3 { // root + 2 messages
		t.Fatalf("expected 3 nodes, got %d", len(tree.Nodes))
	}

	path := tree.PathToRoot()
	if len(path) != 2 {
		t.Fatalf("expected path length 2, got %d", len(path))
	}
}

func TestTreeBranching(t *testing.T) {
	tree := NewTree()

	msg1 := tree.AddMessage("user", "question 1")
	tree.AddMessage("assistant", "answer 1")

	// Fork at msg1
	tree.ForkAt(msg1.ID)
	tree.AddMessage("user", "question 1 - alternative")
	tree.AddMessage("assistant", "answer 1 - alternative")

	if tree.Branches() != 1 {
		t.Errorf("expected 1 branch point, got %d", tree.Branches())
	}
}

func TestTreeSiblings(t *testing.T) {
	tree := NewTree()

	tree.AddMessage("user", "msg1")
	msg1 := tree.Current

	// Go back to root and create sibling
	tree.ForkAt("root")
	tree.AddMessage("user", "msg2")

	siblings := tree.Siblings()
	if len(siblings) != 2 {
		t.Fatalf("expected 2 siblings, got %d", len(siblings))
	}

	_ = msg1
}

func TestFormatTree(t *testing.T) {
	tree := NewTree()
	tree.AddMessage("user", "hello world")
	tree.AddMessage("assistant", "hi")

	output := tree.FormatTree()
	if output == "" {
		t.Fatal("FormatTree should not be empty")
	}
}
