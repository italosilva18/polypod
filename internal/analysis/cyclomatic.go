// Package analysis provides Go code analysis utilities.
package analysis

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FuncComplexity represents cyclomatic complexity for a single function.
type FuncComplexity struct {
	File     string
	Func     string
	Receiver string // empty for non-methods
	Line     int
	Score    int
}

// Cyclomatic computes complexity for all functions in a Go file.
func Cyclomatic(path string) ([]FuncComplexity, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var results []FuncComplexity
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}

		score := computeComplexity(fn)
		result := FuncComplexity{
			File:  path,
			Func:  fn.Name.Name,
			Line:  fset.Position(fn.Pos()).Line,
			Score: score,
		}
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			result.Receiver = receiverTypeName(fn.Recv.List[0].Type)
		}
		results = append(results, result)
		return true
	})

	return results, nil
}

// CyclomaticDir computes complexity for all .go files in a directory (non-recursive).
func CyclomaticDir(dir string) ([]FuncComplexity, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var all []FuncComplexity
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		if strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		results, err := Cyclomatic(path)
		if err != nil {
			continue // skip bad files
		}
		all = append(all, results...)
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].Score > all[j].Score
	})
	return all, nil
}

// CyclomaticRecursive walks a directory tree and computes complexity for all .go files.
func CyclomaticRecursive(root string) ([]FuncComplexity, error) {
	var all []FuncComplexity

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() {
			// Skip vendor, node_modules, .git
			base := info.Name()
			if base == "vendor" || base == "node_modules" || base == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}

		results, err := Cyclomatic(path)
		if err != nil {
			return nil // skip bad files
		}
		all = append(all, results...)
		return nil
	})

	if err != nil {
		return nil, err
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].Score > all[j].Score
	})
	return all, nil
}

// computeComplexity computes cyclomatic complexity for a function.
// Base complexity is 1, +1 for each decision point.
func computeComplexity(fn *ast.FuncDecl) int {
	score := 1 // base

	if fn.Body == nil {
		return score
	}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.IfStmt:
			score++
		case *ast.ForStmt:
			score++
		case *ast.RangeStmt:
			score++
		case *ast.CaseClause:
			// Each case (except default) adds 1
			if len(node.List) > 0 {
				score++
			}
		case *ast.CommClause:
			// select case
			if node.Comm != nil {
				score++
			}
		case *ast.BinaryExpr:
			// && and || add a decision point each
			if node.Op == token.LAND || node.Op == token.LOR {
				score++
			}
		case *ast.FuncLit:
			// Don't recurse into nested function literals (they have their own score)
			return false
		}
		return true
	})

	return score
}

// receiverTypeName extracts the type name from a method receiver.
func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return "*" + id.Name
		}
	}
	return ""
}

// ComplexityCategory returns a human-friendly category for a score.
func ComplexityCategory(score int) string {
	switch {
	case score <= 5:
		return "simple"
	case score <= 10:
		return "moderate"
	case score <= 20:
		return "complex"
	case score <= 50:
		return "very complex"
	default:
		return "untestable"
	}
}

// FormatReport returns a human-friendly report of complexities.
func FormatReport(complexities []FuncComplexity, top int) string {
	if len(complexities) == 0 {
		return "No functions found."
	}

	if top > 0 && top < len(complexities) {
		complexities = complexities[:top]
	}

	var sb strings.Builder
	sb.WriteString("CYCLOMATIC COMPLEXITY REPORT\n")
	sb.WriteString("============================\n\n")

	for _, fc := range complexities {
		cat := ComplexityCategory(fc.Score)
		funcName := fc.Func
		if fc.Receiver != "" {
			funcName = fc.Receiver + "." + fc.Func
		}
		sb.WriteString(strings.Repeat(" ", 0))
		sb.WriteString(filepath.Base(fc.File))
		sb.WriteString(":")
		sb.WriteString(itoa(fc.Line))
		sb.WriteString("\t")
		sb.WriteString(funcName)
		sb.WriteString("\t")
		sb.WriteString(itoa(fc.Score))
		sb.WriteString(" (")
		sb.WriteString(cat)
		sb.WriteString(")\n")
	}

	return sb.String()
}

// Stats returns aggregate statistics.
type Stats struct {
	TotalFunctions int
	TotalScore     int
	AvgScore       float64
	MaxScore       int
	MinScore       int
	Distribution   map[string]int // category -> count
}

// ComputeStats returns aggregate statistics for a list of complexities.
func ComputeStats(complexities []FuncComplexity) Stats {
	if len(complexities) == 0 {
		return Stats{Distribution: make(map[string]int)}
	}

	stats := Stats{
		TotalFunctions: len(complexities),
		Distribution:   make(map[string]int),
		MinScore:       complexities[0].Score,
		MaxScore:       complexities[0].Score,
	}

	for _, fc := range complexities {
		stats.TotalScore += fc.Score
		if fc.Score > stats.MaxScore {
			stats.MaxScore = fc.Score
		}
		if fc.Score < stats.MinScore {
			stats.MinScore = fc.Score
		}
		cat := ComplexityCategory(fc.Score)
		stats.Distribution[cat]++
	}

	stats.AvgScore = float64(stats.TotalScore) / float64(len(complexities))
	return stats
}

// itoa is a small helper to avoid pulling in strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}
