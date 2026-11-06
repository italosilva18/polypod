package importgraph

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestBuild_Empty(t *testing.T) {
	dir := setupModule(t)
	g, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if g.PackageCount() != 0 {
		t.Errorf("empty module PackageCount = %d; want 0", g.PackageCount())
	}
	if g.EdgeCount() != 0 {
		t.Errorf("empty module EdgeCount = %d; want 0", g.EdgeCount())
	}
	if g.ModPath != "example.com/test" {
		t.Errorf("ModPath = %q; want example.com/test", g.ModPath)
	}
}

func TestBuild_SinglePackage(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")

	g, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if g.PackageCount() != 1 {
		t.Errorf("PackageCount = %d; want 1", g.PackageCount())
	}
	if g.EdgeCount() != 0 {
		t.Errorf("EdgeCount = %d; want 0", g.EdgeCount())
	}
}

func TestBuild_TwoPackages(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "main.go", `package main

import "example.com/test/util"

func main() {
	util.Foo()
}
`)
	writeFile(t, dir, "util/util.go", "package util\n\nfunc Foo() {}\n")

	g, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if g.PackageCount() < 2 {
		t.Errorf("PackageCount = %d; want >= 2", g.PackageCount())
	}
	if g.EdgeCount() < 1 {
		t.Errorf("EdgeCount = %d; want >= 1", g.EdgeCount())
	}

	// Verify edge direction: main -> util
	mainNode, ok := g.Nodes["example.com/test"]
	if !ok {
		t.Fatal("missing main node")
	}
	if mainNode.FanOut != 1 {
		t.Errorf("main FanOut = %d; want 1", mainNode.FanOut)
	}
	utilNode, ok := g.Nodes["example.com/test/util"]
	if !ok {
		t.Fatal("missing util node")
	}
	if utilNode.FanIn != 1 {
		t.Errorf("util FanIn = %d; want 1", utilNode.FanIn)
	}
}

func TestBuild_LinearChain(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "a/a.go", `package a

import "example.com/test/b"

func F() { b.F() }
`)
	writeFile(t, dir, "b/b.go", `package b

import "example.com/test/c"

func F() { c.F() }
`)
	writeFile(t, dir, "c/c.go", "package c\n\nfunc F() {}\n")

	g, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if g.PackageCount() != 3 {
		t.Errorf("PackageCount = %d; want 3", g.PackageCount())
	}
	if g.EdgeCount() != 2 {
		t.Errorf("EdgeCount = %d; want 2", g.EdgeCount())
	}

	c := g.Nodes["example.com/test/c"]
	if c == nil || c.FanOut != 0 {
		t.Errorf("c should be a leaf with FanOut=0")
	}
	a := g.Nodes["example.com/test/a"]
	if a == nil || a.FanIn != 0 {
		t.Errorf("a should have FanIn=0")
	}
}

func TestBuild_Diamond(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "a/a.go", `package a

import (
	"example.com/test/b"
	"example.com/test/c"
)

func F() { b.F(); c.F() }
`)
	writeFile(t, dir, "b/b.go", `package b

import "example.com/test/d"

func F() { d.F() }
`)
	writeFile(t, dir, "c/c.go", `package c

import "example.com/test/d"

func F() { d.F() }
`)
	writeFile(t, dir, "d/d.go", "package d\n\nfunc F() {}\n")

	g, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if g.PackageCount() != 4 {
		t.Errorf("PackageCount = %d; want 4", g.PackageCount())
	}
	if g.EdgeCount() != 4 {
		t.Errorf("EdgeCount = %d; want 4", g.EdgeCount())
	}

	d := g.Nodes["example.com/test/d"]
	if d == nil {
		t.Fatal("missing d")
	}
	if d.FanIn != 2 {
		t.Errorf("d FanIn = %d; want 2", d.FanIn)
	}
	a := g.Nodes["example.com/test/a"]
	if a.FanOut != 2 {
		t.Errorf("a FanOut = %d; want 2", a.FanOut)
	}
}

func TestBuild_SkipsStdlib(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "main.go", `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	fmt.Println(os.Args)
	_ = strings.ToUpper("x")
}
`)

	g, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for path := range g.Nodes {
		if path == "fmt" || path == "os" || path == "strings" {
			t.Errorf("stdlib %q should not be in graph", path)
		}
	}
	if g.PackageCount() != 1 {
		t.Errorf("PackageCount = %d; want 1", g.PackageCount())
	}
	if g.EdgeCount() != 0 {
		t.Errorf("EdgeCount = %d; want 0", g.EdgeCount())
	}
}

func TestGraph_Cycles(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "a/a.go", `package a

import "example.com/test/b"

func F() { b.F() }
`)
	writeFile(t, dir, "b/b.go", `package b

import "example.com/test/a"

func F() { a.F() }
`)

	g, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	cycles := g.Cycles()
	if len(cycles) == 0 {
		t.Errorf("expected cycles, got none")
	}
}

func TestGraph_NoCycles(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "a/a.go", `package a

import "example.com/test/b"

func F() { b.F() }
`)
	writeFile(t, dir, "b/b.go", "package b\n\nfunc F() {}\n")

	g, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	cycles := g.Cycles()
	if len(cycles) != 0 {
		t.Errorf("expected 0 cycles, got %d", len(cycles))
	}
}

func TestGraph_TopFanIn(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "a/a.go", `package a

import "example.com/test/shared"

func F() { shared.F() }
`)
	writeFile(t, dir, "b/b.go", `package b

import "example.com/test/shared"

func F() { shared.F() }
`)
	writeFile(t, dir, "c/c.go", `package c

import "example.com/test/shared"

func F() { shared.F() }
`)
	writeFile(t, dir, "shared/shared.go", "package shared\n\nfunc F() {}\n")

	g, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	top := g.TopFanIn(1)
	if len(top) != 1 {
		t.Fatalf("TopFanIn(1) len = %d; want 1", len(top))
	}
	if top[0].Path != "shared" {
		t.Errorf("top fan-in = %q; want shared", top[0].Path)
	}
	if top[0].FanIn != 3 {
		t.Errorf("shared FanIn = %d; want 3", top[0].FanIn)
	}
}

func TestGraph_TopFanOut(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "hub/hub.go", `package hub

import (
	"example.com/test/a"
	"example.com/test/b"
	"example.com/test/c"
)

func F() { a.F(); b.F(); c.F() }
`)
	writeFile(t, dir, "a/a.go", "package a\n\nfunc F() {}\n")
	writeFile(t, dir, "b/b.go", "package b\n\nfunc F() {}\n")
	writeFile(t, dir, "c/c.go", "package c\n\nfunc F() {}\n")

	g, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	top := g.TopFanOut(1)
	if len(top) != 1 {
		t.Fatalf("TopFanOut(1) len = %d; want 1", len(top))
	}
	if top[0].Path != "hub" {
		t.Errorf("top fan-out = %q; want hub", top[0].Path)
	}
	if top[0].FanOut != 3 {
		t.Errorf("hub FanOut = %d; want 3", top[0].FanOut)
	}
}

func TestGraph_Orphans(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "root/root.go", `package root

import "example.com/test/dep"

func F() { dep.F() }
`)
	writeFile(t, dir, "dep/dep.go", "package dep\n\nfunc F() {}\n")

	g, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	orphans := g.Orphans()
	foundRoot := false
	for _, o := range orphans {
		if o.Path == "root" {
			foundRoot = true
		}
		if o.Path == "dep" {
			t.Errorf("dep has incoming edges, should not be orphan")
		}
	}
	if !foundRoot {
		t.Errorf("root should be in Orphans")
	}
}

func TestGraph_Leaves(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "root/root.go", `package root

import "example.com/test/leaf"

func F() { leaf.F() }
`)
	writeFile(t, dir, "leaf/leaf.go", "package leaf\n\nfunc F() {}\n")

	g, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	leaves := g.Leaves()
	foundLeaf := false
	for _, l := range leaves {
		if l.Path == "leaf" {
			foundLeaf = true
		}
		if l.Path == "root" {
			t.Errorf("root has outgoing edges, should not be leaf")
		}
	}
	if !foundLeaf {
		t.Errorf("leaf should be in Leaves")
	}
}

func TestGraph_ToDOT(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "main.go", `package main

import "example.com/test/util"

func main() { util.Foo() }
`)
	writeFile(t, dir, "util/util.go", "package util\n\nfunc Foo() {}\n")

	g, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var buf bytes.Buffer
	if err := g.ToDOT(&buf); err != nil {
		t.Fatalf("ToDOT: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "digraph") {
		t.Errorf("ToDOT missing 'digraph': %q", out)
	}
	if !strings.Contains(out, "util") {
		t.Errorf("ToDOT missing 'util': %q", out)
	}
	if !strings.Contains(out, "->") {
		t.Errorf("ToDOT missing edge arrow: %q", out)
	}
	if !strings.Contains(out, "}") {
		t.Errorf("ToDOT missing closing brace")
	}
}

func TestGraph_FormatReport(t *testing.T) {
	dir := setupModule(t)
	writeFile(t, dir, "main.go", `package main

import "example.com/test/util"

func main() { util.Foo() }
`)
	writeFile(t, dir, "util/util.go", "package util\n\nfunc Foo() {}\n")

	g, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	report := g.FormatReport()
	if report == "" {
		t.Fatal("FormatReport empty")
	}
	if !strings.Contains(report, "example.com/test") {
		t.Errorf("report missing module name: %q", report)
	}
	if !strings.Contains(report, "Total packages") {
		t.Errorf("report missing 'Total packages'")
	}
	if !strings.Contains(report, "Total edges") {
		t.Errorf("report missing 'Total edges'")
	}
}

func TestBuild_MissingGoMod(t *testing.T) {
	dir := t.TempDir()
	_, err := Build(dir)
	if err == nil {
		t.Error("expected error for missing go.mod")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.SkipTest {
		t.Error("SkipTest should default to true")
	}
	if !cfg.SkipExternal {
		t.Error("SkipExternal should default to true")
	}
	if len(cfg.SkipDirs) == 0 {
		t.Error("SkipDirs should have defaults")
	}
}
