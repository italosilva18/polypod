package treeify

import (
	"strings"
	"testing"
)

func TestRender_Empty(t *testing.T) {
	if got := Render(nil, DefaultOptions()); got != "" {
		t.Errorf("Render(nil) = %q; want empty", got)
	}
}

func TestRender_SingleNode(t *testing.T) {
	root := &Node{Label: "root"}
	got := Render(root, DefaultOptions())

	if !strings.Contains(got, "root") {
		t.Errorf("Render = %q; should contain 'root'", got)
	}
}

func TestRender_WithChildren(t *testing.T) {
	root := &Node{
		Label: "root",
		Children: []*Node{
			{Label: "child1"},
			{Label: "child2"},
			{Label: "child3"},
		},
	}

	got := Render(root, DefaultOptions())

	for _, label := range []string{"root", "child1", "child2", "child3"} {
		if !strings.Contains(got, label) {
			t.Errorf("Render missing %q in:\n%s", label, got)
		}
	}

	// Should use box drawing chars
	if !strings.Contains(got, "├") && !strings.Contains(got, "└") {
		t.Errorf("Render should use box chars; got:\n%s", got)
	}
}

func TestRender_Nested(t *testing.T) {
	root := &Node{
		Label: "root",
		Children: []*Node{
			{
				Label: "branch1",
				Children: []*Node{
					{Label: "leaf1"},
					{Label: "leaf2"},
				},
			},
			{Label: "branch2"},
		},
	}

	got := Render(root, DefaultOptions())

	for _, label := range []string{"root", "branch1", "leaf1", "leaf2", "branch2"} {
		if !strings.Contains(got, label) {
			t.Errorf("Render missing %q", label)
		}
	}
}

func TestNode_AddChild(t *testing.T) {
	root := &Node{Label: "root"}
	child := root.AddChild("child")

	if len(root.Children) != 1 {
		t.Errorf("Children len = %d; want 1", len(root.Children))
	}
	if child.Label != "child" {
		t.Errorf("child.Label = %s; want child", child.Label)
	}
}

func TestNode_AddChildWithMeta(t *testing.T) {
	root := &Node{Label: "root"}
	child := root.AddChildWithMeta("child", "metadata")

	if child.Meta != "metadata" {
		t.Errorf("Meta = %v; want metadata", child.Meta)
	}
}

func TestNode_Count(t *testing.T) {
	root := &Node{
		Label: "root",
		Children: []*Node{
			{Label: "a"},
			{
				Label: "b",
				Children: []*Node{
					{Label: "b1"},
					{Label: "b2"},
				},
			},
		},
	}

	if got := root.Count(); got != 5 {
		t.Errorf("Count = %d; want 5", got)
	}
}

func TestNode_Depth(t *testing.T) {
	root := &Node{
		Label: "root",
		Children: []*Node{
			{Label: "a"},
			{
				Label: "b",
				Children: []*Node{
					{Label: "b1"},
				},
			},
		},
	}

	if got := root.Depth(); got != 2 {
		t.Errorf("Depth = %d; want 2", got)
	}
}

func TestNode_Walk(t *testing.T) {
	root := &Node{
		Label: "root",
		Children: []*Node{
			{Label: "a"},
			{Label: "b"},
		},
	}

	var labels []string
	root.Walk(func(n *Node, depth int) {
		labels = append(labels, n.Label)
	})

	if len(labels) != 3 {
		t.Errorf("Walk visited %d nodes; want 3", len(labels))
	}
}

func TestNode_Find(t *testing.T) {
	root := &Node{
		Label: "root",
		Children: []*Node{
			{Label: "a"},
			{Label: "b"},
			{
				Label: "c",
				Children: []*Node{
					{Label: "target"},
				},
			},
		},
	}

	found := root.Find("target")
	if found == nil {
		t.Error("Find should locate target")
	}
	if found != nil && found.Label != "target" {
		t.Errorf("Find Label = %s; want target", found.Label)
	}

	if root.Find("missing") != nil {
		t.Error("Find missing should return nil")
	}
}

func TestFromFiles(t *testing.T) {
	paths := []string{
		"src/main.go",
		"src/util/helper.go",
		"src/util/strings.go",
		"README.md",
	}

	root := FromFiles(paths)

	if root.Find("README.md") == nil {
		t.Error("should find README.md")
	}
	if root.Find("main.go") == nil {
		t.Error("should find main.go")
	}
	if root.Find("util") == nil {
		t.Error("should find util")
	}
	if root.Find("helper.go") == nil {
		t.Error("should find helper.go")
	}
}

func TestFromMap(t *testing.T) {
	data := map[string]any{
		"name":    "polypod",
		"version": "0.4",
		"config": map[string]any{
			"host": "localhost",
			"port": 8080,
		},
	}

	root := FromMap("Project", data)

	if root.Label != "Project" {
		t.Errorf("Label = %s; want Project", root.Label)
	}
	if len(root.Children) != 3 {
		t.Errorf("Children len = %d; want 3", len(root.Children))
	}
}

func TestRender_NoChildren(t *testing.T) {
	root := &Node{Label: "lonely"}
	got := Render(root, DefaultOptions())

	if !strings.Contains(got, "lonely") {
		t.Errorf("Render = %q; should contain 'lonely'", got)
	}
	if strings.Contains(got, "├") || strings.Contains(got, "└") {
		t.Errorf("single node should not have branch chars; got:\n%s", got)
	}
}

func TestRender_HideRoot(t *testing.T) {
	root := &Node{
		Label: "secret-root",
		Children: []*Node{
			{Label: "visible-child"},
		},
	}

	opts := DefaultOptions()
	opts.ShowRoot = false
	got := Render(root, opts)

	if strings.Contains(got, "secret-root") {
		t.Errorf("hidden root should not appear; got:\n%s", got)
	}
	if !strings.Contains(got, "visible-child") {
		t.Errorf("child should appear; got:\n%s", got)
	}
}
