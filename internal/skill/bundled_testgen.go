package skill

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/sashabaranov/go-openai/jsonschema"
)

// newTestGenSkill returns the bundled test-generator skill.
//
// The skill parses a Go source file (or raw source string) and emits a stub
// table-driven test file containing one test function per exported function
// or method declaration. It does not execute or compile anything; the caller
// is expected to write the returned text to a *_test.go file and fill in the
// blanks.
//
// Inputs (all optional, but exactly one of source/file_path is required):
//
//	source    - raw Go source code to parse
//	file_path - path to a .go file to read and parse
//	package   - override package name (auto-detected from source if omitted)
func newTestGenSkill() *Skill {
	return &Skill{
		Name:        "test-generator",
		Description: "Generate table-driven stub tests for exported functions in a Go file.",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"file_path": {
					Type:        jsonschema.String,
					Description: "Path to the .go file to generate tests for",
				},
				"source": {
					Type:        jsonschema.String,
					Description: "Alternative: raw Go source code",
				},
				"package": {
					Type:        jsonschema.String,
					Description: "Package name (auto-detected from source if omitted)",
				},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			var source []byte
			filePath := args["file_path"]

			if v := args["source"]; v != "" {
				source = []byte(v)
			} else if filePath != "" {
				data, err := os.ReadFile(filePath)
				if err != nil {
					return "", fmt.Errorf("reading %s: %w", filePath, err)
				}
				source = data
			} else {
				return "", fmt.Errorf("either source or file_path is required")
			}

			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, filePath, source, parser.ParseComments)
			if err != nil {
				// Can't parse; return a minimal template so the caller still
				// gets something useful.
				pkgName := args["package"]
				if pkgName == "" {
					pkgName = "pkg"
				}
				return buildEmptyTestFile(pkgName), nil
			}

			pkgName := file.Name.Name
			if p := args["package"]; p != "" {
				pkgName = p
			}

			funcs := extractTestableFuncs(file)
			return buildTestFile(pkgName, filePath, funcs), nil
		},
	}
}

type testableFunc struct {
	Name     string
	Receiver string
	Params   []fieldInfo
	Results  []fieldInfo
}

type fieldInfo struct {
	Name string
	Type string
}

func extractTestableFuncs(file *ast.File) []testableFunc {
	var funcs []testableFunc
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		// Only exported funcs.
		if !ast.IsExported(fn.Name.Name) {
			continue
		}
		// Skip Test/Benchmark/Example helpers — these are already tests.
		if strings.HasPrefix(fn.Name.Name, "Test") ||
			strings.HasPrefix(fn.Name.Name, "Benchmark") ||
			strings.HasPrefix(fn.Name.Name, "Example") {
			continue
		}

		tf := testableFunc{Name: fn.Name.Name}
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			tf.Receiver = exprToString(fn.Recv.List[0].Type)
		}
		if fn.Type.Params != nil {
			for _, p := range fn.Type.Params.List {
				typeName := exprToString(p.Type)
				if len(p.Names) == 0 {
					tf.Params = append(tf.Params, fieldInfo{Name: "arg", Type: typeName})
				} else {
					for _, n := range p.Names {
						tf.Params = append(tf.Params, fieldInfo{Name: n.Name, Type: typeName})
					}
				}
			}
		}
		if fn.Type.Results != nil {
			for _, r := range fn.Type.Results.List {
				typeName := exprToString(r.Type)
				if len(r.Names) == 0 {
					tf.Results = append(tf.Results, fieldInfo{Type: typeName})
				} else {
					for _, n := range r.Names {
						tf.Results = append(tf.Results, fieldInfo{Name: n.Name, Type: typeName})
					}
				}
			}
		}
		funcs = append(funcs, tf)
	}
	return funcs
}

func exprToString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + exprToString(t.X)
	case *ast.SelectorExpr:
		return exprToString(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		return "[]" + exprToString(t.Elt)
	case *ast.MapType:
		return "map[" + exprToString(t.Key) + "]" + exprToString(t.Value)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.Ellipsis:
		return "..." + exprToString(t.Elt)
	}
	return "any"
}

func buildTestFile(pkgName, filePath string, funcs []testableFunc) string {
	var sb strings.Builder
	sb.WriteString("package " + pkgName + "\n\n")
	sb.WriteString("import (\n")
	sb.WriteString("\t\"testing\"\n")
	sb.WriteString(")\n\n")

	if filePath != "" {
		sb.WriteString("// Generated stub tests for " + filepath.Base(filePath) + "\n")
		sb.WriteString("// TODO: fill in test cases and implementations\n\n")
	}

	if len(funcs) == 0 {
		sb.WriteString("// No exported functions found to test.\n")
		return sb.String()
	}

	for _, fn := range funcs {
		testName := "Test" + fn.Name
		if fn.Receiver != "" {
			testName = "Test" + strings.TrimPrefix(fn.Receiver, "*") + "_" + fn.Name
		}

		sb.WriteString(fmt.Sprintf("func %s(t *testing.T) {\n", testName))
		sb.WriteString("\ttests := []struct {\n")
		sb.WriteString("\t\tname string\n")

		for _, p := range fn.Params {
			name := p.Name
			if name == "" {
				name = "arg"
			}
			sb.WriteString(fmt.Sprintf("\t\t%s %s\n", toLowerFirst(name), p.Type))
		}

		for i, r := range fn.Results {
			if r.Type == "error" {
				sb.WriteString("\t\twantErr bool\n")
				continue
			}
			name := r.Name
			if name == "" {
				name = fmt.Sprintf("want%d", i+1)
			} else {
				name = "want" + toUpperFirst(name)
			}
			sb.WriteString(fmt.Sprintf("\t\t%s %s\n", name, r.Type))
		}

		sb.WriteString("\t}{\n")
		sb.WriteString("\t\t// TODO: Add test cases\n")
		sb.WriteString("\t}\n\n")
		sb.WriteString("\tfor _, tt := range tests {\n")
		sb.WriteString("\t\tt.Run(tt.name, func(t *testing.T) {\n")
		sb.WriteString("\t\t\t// TODO: call " + fn.Name + " and verify\n")
		sb.WriteString("\t\t\t_ = tt\n")
		sb.WriteString("\t\t})\n")
		sb.WriteString("\t}\n")
		sb.WriteString("}\n\n")
	}

	return sb.String()
}

func buildEmptyTestFile(pkgName string) string {
	return "package " + pkgName + "\n\nimport \"testing\"\n\n// TODO: add tests\nfunc TestPlaceholder(t *testing.T) {\n\tt.Skip(\"not implemented\")\n}\n"
}

func toLowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

func toUpperFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
