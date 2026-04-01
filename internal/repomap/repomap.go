package repomap

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Symbol represents a code symbol (function, type, class, etc.)
type Symbol struct {
	Name     string
	Kind     string // "function", "method", "type", "interface", "class", "const", "var"
	File     string
	Line     int
	Exported bool
}

// Reference represents a reference from one file to a symbol.
type Reference struct {
	FromFile string
	ToSymbol string
	ToFile   string
}

// RepoMap builds a ranked map of a codebase using AST parsing + PageRank.
type RepoMap struct {
	root    string
	symbols []Symbol
	refs    []Reference
	graph   map[string][]string // file -> files it references
	logger  *slog.Logger
}

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "__pycache__": true,
	".venv": true, "dist": true, "build": true, ".next": true, "target": true,
}

// NewRepoMap creates a repo mapper.
func NewRepoMap(root string, logger *slog.Logger) *RepoMap {
	return &RepoMap{
		root:   root,
		graph:  make(map[string][]string),
		logger: logger,
	}
}

// Build parses the codebase, extracts symbols and references, builds the graph.
func (rm *RepoMap) Build() error {
	err := filepath.Walk(rm.root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() && skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Size() > 1024*1024 { // skip >1MB
			return nil
		}

		relPath, _ := filepath.Rel(rm.root, path)
		ext := filepath.Ext(path)

		switch ext {
		case ".go":
			rm.parseGo(path, relPath)
		case ".py":
			rm.parseRegex(path, relPath, "python")
		case ".js", ".jsx", ".ts", ".tsx":
			rm.parseRegex(path, relPath, "javascript")
		case ".rs":
			rm.parseRegex(path, relPath, "rust")
		case ".java":
			rm.parseRegex(path, relPath, "java")
		case ".rb":
			rm.parseRegex(path, relPath, "ruby")
		}

		return nil
	})

	// Build reference graph
	rm.buildReferenceGraph()

	return err
}

// parseGo uses Go's native AST parser for accurate symbol extraction.
func (rm *RepoMap) parseGo(path, relPath string) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return
	}

	ast.Inspect(node, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.FuncDecl:
			sym := Symbol{
				Name:     v.Name.Name,
				Kind:     "function",
				File:     relPath,
				Line:     fset.Position(v.Pos()).Line,
				Exported: v.Name.IsExported(),
			}
			if v.Recv != nil {
				sym.Kind = "method"
			}
			rm.symbols = append(rm.symbols, sym)

		case *ast.TypeSpec:
			kind := "type"
			switch v.Type.(type) {
			case *ast.InterfaceType:
				kind = "interface"
			case *ast.StructType:
				kind = "struct"
			}
			rm.symbols = append(rm.symbols, Symbol{
				Name:     v.Name.Name,
				Kind:     kind,
				File:     relPath,
				Line:     fset.Position(v.Pos()).Line,
				Exported: v.Name.IsExported(),
			})

		case *ast.ValueSpec:
			for _, name := range v.Names {
				rm.symbols = append(rm.symbols, Symbol{
					Name:     name.Name,
					Kind:     "var",
					File:     relPath,
					Line:     fset.Position(name.Pos()).Line,
					Exported: name.IsExported(),
				})
			}
		}
		return true
	})

	// Extract imports for reference graph
	for _, imp := range node.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		// Map imports to local files
		if strings.Contains(importPath, "/internal/") || strings.Contains(importPath, "/pkg/") {
			parts := strings.Split(importPath, "/")
			if len(parts) > 0 {
				lastPart := parts[len(parts)-1]
				rm.refs = append(rm.refs, Reference{
					FromFile: relPath,
					ToSymbol: lastPart,
				})
			}
		}
	}
}

// Regex patterns for non-Go languages
var (
	pyDefRe    = regexp.MustCompile(`(?m)^(?:class|def)\s+(\w+)`)
	jsDefRe    = regexp.MustCompile(`(?m)(?:export\s+)?(?:function|class|const|let|var|interface|type|enum)\s+(\w+)`)
	rustDefRe  = regexp.MustCompile(`(?m)(?:pub\s+)?(?:fn|struct|enum|trait|impl|type|const|static)\s+(\w+)`)
	javaDefRe  = regexp.MustCompile(`(?m)(?:public|private|protected)?\s*(?:static\s+)?(?:class|interface|enum|void|int|String|boolean)\s+(\w+)`)
	rubyDefRe  = regexp.MustCompile(`(?m)(?:class|module|def)\s+(\w+)`)
	importGoRe = regexp.MustCompile(`(?m)import\s+\(([^)]+)\)`)
	importPyRe = regexp.MustCompile(`(?m)(?:from|import)\s+([\w.]+)`)
	importJsRe = regexp.MustCompile(`(?m)(?:import|require)\s*\(?.*?['"]([\w./@-]+)['"]`)
)

func (rm *RepoMap) parseRegex(path, relPath, lang string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	content := string(data)

	var re *regexp.Regexp
	switch lang {
	case "python":
		re = pyDefRe
	case "javascript":
		re = jsDefRe
	case "rust":
		re = rustDefRe
	case "java":
		re = javaDefRe
	case "ruby":
		re = rubyDefRe
	default:
		return
	}

	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if matches := re.FindStringSubmatch(line); len(matches) > 1 {
			kind := "function"
			lower := strings.ToLower(line)
			if strings.Contains(lower, "class ") {
				kind = "class"
			} else if strings.Contains(lower, "interface ") || strings.Contains(lower, "trait ") {
				kind = "interface"
			} else if strings.Contains(lower, "struct ") || strings.Contains(lower, "enum ") {
				kind = "type"
			}

			rm.symbols = append(rm.symbols, Symbol{
				Name:     matches[1],
				Kind:     kind,
				File:     relPath,
				Line:     i + 1,
				Exported: len(matches[1]) > 0 && matches[1][0] >= 'A' && matches[1][0] <= 'Z',
			})
		}
	}

	// Extract references (imports)
	var importRe *regexp.Regexp
	switch lang {
	case "python":
		importRe = importPyRe
	case "javascript":
		importRe = importJsRe
	}
	if importRe != nil {
		for _, m := range importRe.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				rm.refs = append(rm.refs, Reference{FromFile: relPath, ToSymbol: m[1]})
			}
		}
	}
}

// buildReferenceGraph creates the file→file dependency graph.
func (rm *RepoMap) buildReferenceGraph() {
	// Map symbols to their files
	symbolFile := make(map[string]string)
	for _, s := range rm.symbols {
		symbolFile[s.Name] = s.File
	}

	// Build edges: file A references symbol in file B → edge A→B
	for _, ref := range rm.refs {
		if targetFile, ok := symbolFile[ref.ToSymbol]; ok && targetFile != ref.FromFile {
			rm.graph[ref.FromFile] = append(rm.graph[ref.FromFile], targetFile)
		}
	}
}

// PageRank computes personalized PageRank scores for files.
// chatFiles are files mentioned in the current conversation (get higher weight).
func (rm *RepoMap) PageRank(chatFiles []string, iterations int, damping float64) map[string]float64 {
	if iterations == 0 {
		iterations = 20
	}
	if damping == 0 {
		damping = 0.85
	}

	// Collect all files
	allFiles := make(map[string]bool)
	for _, s := range rm.symbols {
		allFiles[s.File] = true
	}
	for f := range rm.graph {
		allFiles[f] = true
	}

	n := len(allFiles)
	if n == 0 {
		return nil
	}

	// Initialize scores
	scores := make(map[string]float64)
	for f := range allFiles {
		scores[f] = 1.0 / float64(n)
	}

	// Personalization vector (bias toward chat files)
	personal := make(map[string]float64)
	if len(chatFiles) > 0 {
		for _, f := range chatFiles {
			personal[f] = 1.0 / float64(len(chatFiles))
		}
	} else {
		for f := range allFiles {
			personal[f] = 1.0 / float64(n)
		}
	}

	// Iterate PageRank
	for iter := 0; iter < iterations; iter++ {
		newScores := make(map[string]float64)

		for f := range allFiles {
			// Teleportation
			newScores[f] = (1 - damping) * personal[f]
		}

		for f, neighbors := range rm.graph {
			if len(neighbors) == 0 {
				continue
			}
			share := damping * scores[f] / float64(len(neighbors))
			for _, neighbor := range neighbors {
				newScores[neighbor] += share
			}
		}

		scores = newScores
	}

	return scores
}

// RankedSymbols returns symbols ranked by PageRank score, limited to tokenBudget.
func (rm *RepoMap) RankedSymbols(chatFiles []string, tokenBudget int) []Symbol {
	if tokenBudget == 0 {
		tokenBudget = 4000
	}

	scores := rm.PageRank(chatFiles, 20, 0.85)

	// Score each symbol by its file's PageRank
	type scoredSymbol struct {
		Symbol
		Score float64
	}

	var scored []scoredSymbol
	for _, s := range rm.symbols {
		if !s.Exported && s.Kind != "class" {
			continue // skip unexported non-class symbols
		}
		score := scores[s.File]
		// Boost exported symbols
		if s.Exported {
			score *= 1.5
		}
		// Boost functions/methods over vars
		if s.Kind == "function" || s.Kind == "method" {
			score *= 1.2
		}
		scored = append(scored, scoredSymbol{s, score})
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	// Select symbols within token budget (~10 tokens per symbol line)
	var result []Symbol
	tokensUsed := 0
	for _, s := range scored {
		lineTokens := 10 + len(s.Name)/2
		if tokensUsed+lineTokens > tokenBudget {
			break
		}
		result = append(result, s.Symbol)
		tokensUsed += lineTokens
	}

	return result
}

// FormatMap renders the ranked repo map as a string for context injection.
func (rm *RepoMap) FormatMap(chatFiles []string, tokenBudget int) string {
	symbols := rm.RankedSymbols(chatFiles, tokenBudget)
	if len(symbols) == 0 {
		return ""
	}

	// Group by file
	byFile := make(map[string][]Symbol)
	fileOrder := []string{}
	seen := make(map[string]bool)
	for _, s := range symbols {
		if !seen[s.File] {
			fileOrder = append(fileOrder, s.File)
			seen[s.File] = true
		}
		byFile[s.File] = append(byFile[s.File], s)
	}

	var sb strings.Builder
	for _, file := range fileOrder {
		fmt.Fprintf(&sb, "%s\n", file)
		for _, s := range byFile[file] {
			fmt.Fprintf(&sb, "  %s %s (line %d)\n", s.Kind, s.Name, s.Line)
		}
	}

	return sb.String()
}

// Stats returns statistics about the parsed codebase.
func (rm *RepoMap) Stats() string {
	files := make(map[string]bool)
	for _, s := range rm.symbols {
		files[s.File] = true
	}

	entropy := 0.0
	scores := rm.PageRank(nil, 20, 0.85)
	for _, s := range scores {
		if s > 0 {
			entropy -= s * math.Log2(s)
		}
	}

	return fmt.Sprintf("Arquivos: %d, Simbolos: %d, Referencias: %d, Entropia: %.2f",
		len(files), len(rm.symbols), len(rm.refs), entropy)
}
