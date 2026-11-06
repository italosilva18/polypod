// Package importgraph builds directed dependency graphs of Go packages.
package importgraph

import (
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Node represents a package in the graph.
type Node struct {
	Name    string
	Path    string  // relative path from module root
	Imports []*Node // direct dependencies
	FanIn   int     // incoming edges
	FanOut  int     // outgoing edges
	Files   int     // .go files in package
}

// Graph is a directed graph of package dependencies.
type Graph struct {
	Nodes   map[string]*Node
	ModPath string
	RootDir string
}

// Config controls graph building.
type Config struct {
	// SkipTest skips _test.go files (default true).
	SkipTest bool
	// SkipExternal excludes packages outside the current module (default true).
	SkipExternal bool
	// SkipDirs lists directory names to skip.
	SkipDirs []string
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		SkipTest:     true,
		SkipExternal: true,
		SkipDirs:     []string{"vendor", "node_modules", ".git", "testdata"},
	}
}

// Build walks a Go module and constructs the import graph.
func Build(modRoot string) (*Graph, error) {
	return BuildWithConfig(modRoot, DefaultConfig())
}

// BuildWithConfig builds with explicit configuration.
func BuildWithConfig(modRoot string, cfg Config) (*Graph, error) {
	// Parse module name from go.mod
	modPath, err := readModulePath(filepath.Join(modRoot, "go.mod"))
	if err != nil {
		return nil, err
	}

	graph := &Graph{
		Nodes:   make(map[string]*Node),
		ModPath: modPath,
		RootDir: modRoot,
	}

	// First pass: collect packages and their imports
	err = filepath.Walk(modRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			for _, skip := range cfg.SkipDirs {
				if name == skip {
					return filepath.SkipDir
				}
			}
			return nil
		}

		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if cfg.SkipTest && strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return nil
		}

		pkgDir := filepath.Dir(path)
		relPkgDir, _ := filepath.Rel(modRoot, pkgDir)
		pkgImportPath := filepath.ToSlash(filepath.Join(modPath, relPkgDir))
		if relPkgDir == "." {
			pkgImportPath = modPath
		}

		node, ok := graph.Nodes[pkgImportPath]
		if !ok {
			node = &Node{
				Name: file.Name.Name,
				Path: filepath.ToSlash(relPkgDir),
			}
			graph.Nodes[pkgImportPath] = node
		}
		node.Files++

		// Parse imports
		importSet := make(map[string]bool)
		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)

			// Skip external if configured
			if cfg.SkipExternal && !strings.HasPrefix(importPath, modPath) {
				continue
			}

			// Skip self
			if importPath == pkgImportPath {
				continue
			}

			importSet[importPath] = true
		}

		// Store imports as strings; resolve to nodes in second pass
		for imp := range importSet {
			if existing := graph.Nodes[imp]; existing != nil {
				found := false
				for _, e := range node.Imports {
					if e == existing {
						found = true
						break
					}
				}
				if !found {
					node.Imports = append(node.Imports, existing)
				}
			} else {
				// Create placeholder
				placeholder := &Node{
					Name: filepath.Base(imp),
					Path: strings.TrimPrefix(imp, modPath+"/"),
				}
				graph.Nodes[imp] = placeholder
				node.Imports = append(node.Imports, placeholder)
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Compute fan-in/fan-out
	for _, node := range graph.Nodes {
		node.FanOut = len(node.Imports)
		for _, imp := range node.Imports {
			imp.FanIn++
		}
	}

	return graph, nil
}

// readModulePath reads the module path from go.mod.
func readModulePath(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module")), nil
		}
	}
	return "", fmt.Errorf("module directive not found")
}

// Cycles returns all dependency cycles.
// Each cycle is a list of package import paths.
func (g *Graph) Cycles() [][]string {
	var cycles [][]string
	visited := make(map[string]bool)
	stack := make(map[string]bool)
	path := []string{}

	var dfs func(pkgPath string) bool
	dfs = func(pkgPath string) bool {
		if stack[pkgPath] {
			// Found cycle - extract it from the path
			for i, p := range path {
				if p == pkgPath {
					cycle := append([]string(nil), path[i:]...)
					cycle = append(cycle, pkgPath)
					cycles = append(cycles, cycle)
					return true
				}
			}
			return true
		}
		if visited[pkgPath] {
			return false
		}

		visited[pkgPath] = true
		stack[pkgPath] = true
		path = append(path, pkgPath)

		node, ok := g.Nodes[pkgPath]
		if !ok {
			stack[pkgPath] = false
			path = path[:len(path)-1]
			return false
		}

		for _, imp := range node.Imports {
			impPath := g.pathForNode(imp)
			dfs(impPath)
		}

		stack[pkgPath] = false
		path = path[:len(path)-1]
		return false
	}

	for pkgPath := range g.Nodes {
		if !visited[pkgPath] {
			dfs(pkgPath)
		}
	}

	return cycles
}

// pathForNode finds the import path key for a given Node pointer.
func (g *Graph) pathForNode(n *Node) string {
	for path, node := range g.Nodes {
		if node == n {
			return path
		}
	}
	return ""
}

// TopFanIn returns packages sorted by incoming dependency count (highest first).
func (g *Graph) TopFanIn(limit int) []*Node {
	nodes := make([]*Node, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		nodes = append(nodes, n)
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].FanIn > nodes[j].FanIn
	})
	if limit > 0 && len(nodes) > limit {
		return nodes[:limit]
	}
	return nodes
}

// TopFanOut returns packages sorted by outgoing dependency count.
func (g *Graph) TopFanOut(limit int) []*Node {
	nodes := make([]*Node, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		nodes = append(nodes, n)
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].FanOut > nodes[j].FanOut
	})
	if limit > 0 && len(nodes) > limit {
		return nodes[:limit]
	}
	return nodes
}

// Orphans returns packages with no incoming dependencies (leaf consumers).
func (g *Graph) Orphans() []*Node {
	var orphans []*Node
	for _, n := range g.Nodes {
		if n.FanIn == 0 && n.Files > 0 {
			orphans = append(orphans, n)
		}
	}
	sort.Slice(orphans, func(i, j int) bool {
		return orphans[i].Path < orphans[j].Path
	})
	return orphans
}

// Leaves returns packages with no outgoing internal dependencies.
func (g *Graph) Leaves() []*Node {
	var leaves []*Node
	for _, n := range g.Nodes {
		if n.FanOut == 0 && n.Files > 0 {
			leaves = append(leaves, n)
		}
	}
	sort.Slice(leaves, func(i, j int) bool {
		return leaves[i].Path < leaves[j].Path
	})
	return leaves
}

// PackageCount returns the number of nodes in the graph.
func (g *Graph) PackageCount() int {
	return len(g.Nodes)
}

// EdgeCount returns the total number of dependency edges.
func (g *Graph) EdgeCount() int {
	count := 0
	for _, n := range g.Nodes {
		count += n.FanOut
	}
	return count
}

// ToDOT writes the graph in Graphviz DOT format.
func (g *Graph) ToDOT(w io.Writer) error {
	fmt.Fprintln(w, "digraph imports {")
	fmt.Fprintln(w, "  rankdir=LR;")
	fmt.Fprintln(w, "  node [shape=box];")

	// Sort for stable output
	paths := make([]string, 0, len(g.Nodes))
	for p := range g.Nodes {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		n := g.Nodes[p]
		label := n.Path
		if label == "" {
			label = n.Name
		}
		fmt.Fprintf(w, "  %q [label=%q];\n", p, label)
	}

	for _, p := range paths {
		n := g.Nodes[p]
		for _, imp := range n.Imports {
			impPath := g.pathForNode(imp)
			fmt.Fprintf(w, "  %q -> %q;\n", p, impPath)
		}
	}

	fmt.Fprintln(w, "}")
	return nil
}

// FormatReport returns a text summary of the graph.
func (g *Graph) FormatReport() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("IMPORT GRAPH REPORT: %s\n", g.ModPath))
	sb.WriteString("=====================\n\n")
	sb.WriteString(fmt.Sprintf("Total packages: %d\n", g.PackageCount()))
	sb.WriteString(fmt.Sprintf("Total edges:    %d\n", g.EdgeCount()))

	cycles := g.Cycles()
	sb.WriteString(fmt.Sprintf("Cycles:         %d\n\n", len(cycles)))

	if len(cycles) > 0 {
		sb.WriteString("CYCLES:\n")
		for i, cycle := range cycles {
			if i >= 5 {
				sb.WriteString(fmt.Sprintf("  ... and %d more\n", len(cycles)-5))
				break
			}
			sb.WriteString("  " + strings.Join(cycle, " -> ") + "\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("TOP FAN-IN (most depended on):\n")
	for i, n := range g.TopFanIn(10) {
		sb.WriteString(fmt.Sprintf("  %2d. %s (in=%d, out=%d)\n", i+1, n.Path, n.FanIn, n.FanOut))
	}
	sb.WriteString("\n")

	sb.WriteString("TOP FAN-OUT (most dependencies):\n")
	for i, n := range g.TopFanOut(10) {
		sb.WriteString(fmt.Sprintf("  %2d. %s (in=%d, out=%d)\n", i+1, n.Path, n.FanIn, n.FanOut))
	}

	return sb.String()
}
