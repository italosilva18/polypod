// Package deadcode detects unreferenced exported symbols in a Go module.
package deadcode

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// DeadSymbol represents an unreferenced exported symbol.
type DeadSymbol struct {
	Package    string
	Name       string
	Kind       string // "func", "type", "var", "const", "method"
	File       string
	Line       int
	Receiver   string  // for methods
	Confidence float64 // 0-1, confidence this is truly dead
	Reason     string  // why we think it might not be dead
}

// SymbolKind represents the kind of symbol.
type SymbolKind string

const (
	KindFunc   SymbolKind = "func"
	KindType   SymbolKind = "type"
	KindVar    SymbolKind = "var"
	KindConst  SymbolKind = "const"
	KindMethod SymbolKind = "method"
)

// Config controls the dead code scan.
type Config struct {
	// SkipTest skips _test.go files when computing dead code (default true).
	SkipTest bool
	// SkipInternal skips internal/ packages (default false).
	SkipInternal bool
	// IncludeUnexported scans unexported symbols too (default false).
	IncludeUnexported bool
	// SkipDirs contains directories to skip (e.g. vendor, node_modules).
	SkipDirs []string
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		SkipTest: true,
		SkipDirs: []string{"vendor", "node_modules", ".git", "testdata"},
	}
}

// Symbol is an exported symbol declared in a file.
type Symbol struct {
	Package  string
	Name     string
	Kind     string
	File     string
	Line     int
	Receiver string
}

// Scan scans a Go module and returns probable dead symbols.
func Scan(modRoot string) ([]DeadSymbol, error) {
	return ScanWithConfig(modRoot, DefaultConfig())
}

// ScanWithConfig scans with explicit configuration.
func ScanWithConfig(modRoot string, cfg Config) ([]DeadSymbol, error) {
	// Step 1: Collect all symbols and all files
	symbols, files, err := collectSymbols(modRoot, cfg)
	if err != nil {
		return nil, err
	}

	// Step 2: Build reference index by scanning all files for identifier uses
	refs, err := collectReferences(files, cfg)
	if err != nil {
		return nil, err
	}

	// Step 3: Find symbols with no references (excluding self-reference)
	var deadSymbols []DeadSymbol
	for _, sym := range symbols {
		if isEntryPoint(sym) {
			continue
		}

		refCount := countReferences(sym, refs)
		if refCount == 0 {
			confidence := computeConfidence(sym)
			deadSymbols = append(deadSymbols, DeadSymbol{
				Package:    sym.Package,
				Name:       sym.Name,
				Kind:       sym.Kind,
				File:       sym.File,
				Line:       sym.Line,
				Receiver:   sym.Receiver,
				Confidence: confidence,
				Reason:     dynamicHint(sym),
			})
		}
	}

	return deadSymbols, nil
}

// collectSymbols walks the tree and collects all top-level exported symbols.
func collectSymbols(root string, cfg Config) ([]Symbol, []string, error) {
	var symbols []Symbol
	var files []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
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

		files = append(files, path)

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil
		}

		pkgName := file.Name.Name
		if pkgName == "main" {
			return nil // skip main package entirely
		}

		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				name := d.Name.Name
				if !cfg.IncludeUnexported && !isExported(name) {
					continue
				}
				kind := "func"
				receiver := ""
				if d.Recv != nil && len(d.Recv.List) > 0 {
					kind = "method"
					receiver = exprString(d.Recv.List[0].Type)
				}
				symbols = append(symbols, Symbol{
					Package:  pkgName,
					Name:     name,
					Kind:     kind,
					File:     path,
					Line:     fset.Position(d.Pos()).Line,
					Receiver: receiver,
				})
			case *ast.GenDecl:
				var kind string
				switch d.Tok {
				case token.TYPE:
					kind = "type"
				case token.VAR:
					kind = "var"
				case token.CONST:
					kind = "const"
				default:
					continue
				}
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if !cfg.IncludeUnexported && !isExported(s.Name.Name) {
							continue
						}
						symbols = append(symbols, Symbol{
							Package: pkgName,
							Name:    s.Name.Name,
							Kind:    kind,
							File:    path,
							Line:    fset.Position(s.Pos()).Line,
						})
					case *ast.ValueSpec:
						for _, name := range s.Names {
							if !cfg.IncludeUnexported && !isExported(name.Name) {
								continue
							}
							symbols = append(symbols, Symbol{
								Package: pkgName,
								Name:    name.Name,
								Kind:    kind,
								File:    path,
								Line:    fset.Position(name.Pos()).Line,
							})
						}
					}
				}
			}
		}
		return nil
	})

	if err != nil {
		return nil, nil, err
	}
	return symbols, files, nil
}

// collectReferences builds a set of all identifier names used across files.
func collectReferences(files []string, cfg Config) (map[string]int, error) {
	refs := make(map[string]int)

	for _, path := range files {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			continue
		}

		ast.Inspect(file, func(n ast.Node) bool {
			if ident, ok := n.(*ast.Ident); ok {
				refs[ident.Name]++
			}
			// Also count selector expressions (pkg.Name)
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if sel.Sel != nil {
					refs[sel.Sel.Name]++
				}
			}
			return true
		})
	}
	return refs, nil
}

// countReferences returns the number of times a symbol is referenced (excluding its declaration).
func countReferences(sym Symbol, refs map[string]int) int {
	count := refs[sym.Name]
	// Subtract 1 for the declaration itself
	if count > 0 {
		count--
	}
	return count
}

// isEntryPoint returns true for functions that shouldn't be flagged as dead.
func isEntryPoint(sym Symbol) bool {
	if sym.Kind != "func" && sym.Kind != "method" {
		return false
	}
	// main, init, TestXxx, BenchmarkXxx, ExampleXxx
	if sym.Name == "main" || sym.Name == "init" {
		return true
	}
	if strings.HasPrefix(sym.Name, "Test") ||
		strings.HasPrefix(sym.Name, "Benchmark") ||
		strings.HasPrefix(sym.Name, "Example") ||
		strings.HasPrefix(sym.Name, "Fuzz") {
		return true
	}
	// Interface method implementations (heuristic: methods with receivers)
	if sym.Kind == "method" {
		// Common interface method names
		switch sym.Name {
		case "String", "Error", "MarshalJSON", "UnmarshalJSON",
			"Read", "Write", "Close", "Len", "Less", "Swap", "Error()",
			"ServeHTTP", "Scan", "Value":
			return true
		}
	}
	return false
}

// computeConfidence returns 0-1 confidence that this is truly dead.
func computeConfidence(sym Symbol) float64 {
	base := 0.7

	// Methods are less confidently dead (might implement interface)
	if sym.Kind == "method" {
		base -= 0.3
	}

	// Exported types are commonly used via reflection
	if sym.Kind == "type" && isExported(sym.Name) {
		base -= 0.1
	}

	if base < 0 {
		return 0
	}
	return base
}

// dynamicHint returns a human-readable reason for uncertainty.
func dynamicHint(sym Symbol) string {
	switch sym.Kind {
	case "method":
		return "May implement an interface; verify by checking type implementations"
	case "type":
		return "May be used via reflection or external packages"
	case "func":
		if strings.HasSuffix(sym.Name, "Handler") || strings.HasSuffix(sym.Name, "Factory") {
			return "Name pattern suggests it may be registered at runtime"
		}
	case "var":
		return "May be used via init() side effects or external packages"
	}
	return "No static references found"
}

func isExported(name string) bool {
	if name == "" {
		return false
	}
	r := []rune(name)[0]
	return unicode.IsUpper(r)
}

func exprString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + exprString(t.X)
	case *ast.SelectorExpr:
		return exprString(t.X) + "." + t.Sel.Name
	}
	return ""
}

// FormatReport returns a human-friendly report.
func FormatReport(dead []DeadSymbol, top int) string {
	if len(dead) == 0 {
		return "No dead code found."
	}

	if top > 0 && len(dead) > top {
		dead = dead[:top]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("DEAD CODE REPORT (%d symbols)\n", len(dead)))
	sb.WriteString("=================\n\n")

	for _, d := range dead {
		name := d.Name
		if d.Receiver != "" {
			name = d.Receiver + "." + name
		}
		sb.WriteString(fmt.Sprintf("[%s] %s:%d %s %s (confidence=%.0f%%)\n",
			d.Kind, filepath.Base(d.File), d.Line, d.Package, name, d.Confidence*100))
		if d.Reason != "" {
			sb.WriteString("  Reason: " + d.Reason + "\n")
		}
	}
	return sb.String()
}
