package skill

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sashabaranov/go-openai/jsonschema"
)

// NewDocSummarySkill returns a skill that generates package-level GoDoc summaries
// for a Go package directory. It extracts exported (and optionally unexported)
// types, functions, constants, and variables and renders them as markdown.
func NewDocSummarySkill() *Skill {
	return &Skill{
		Name:        "doc_summary",
		Description: "Gerar um resumo em markdown dos tipos, funcoes e visao geral de um pacote Go (GoDoc).",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"dir": {
					Type:        jsonschema.String,
					Description: "Diretorio contendo o pacote Go a ser resumido",
				},
				"include_unexported": {
					Type:        jsonschema.String,
					Description: "Incluir simbolos nao-exportados (\"true\" ou \"false\", padrao \"false\")",
				},
			},
			Required: []string{"dir"},
		},
		Execute: func(args map[string]string) (string, error) {
			dir := args["dir"]
			if dir == "" {
				return "", fmt.Errorf("dir is required")
			}
			includeUnexported := false
			if v, ok := args["include_unexported"]; ok {
				switch strings.ToLower(strings.TrimSpace(v)) {
				case "true", "1", "yes", "y":
					includeUnexported = true
				}
			}
			return summarizePackage(dir, includeUnexported)
		},
	}
}

type pkgDoc struct {
	Name      string
	PkgDoc    string
	Types     []symDoc
	Funcs     []symDoc
	Constants []symDoc
	Variables []symDoc
}

type symDoc struct {
	Name      string
	Kind      string
	Signature string
	Doc       string
	Exported  bool
}

func summarizePackage(dir string, includeUnexported bool) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read dir: %w", err)
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}

	if len(files) == 0 {
		return fmt.Sprintf("# Package (empty)\n\nNo Go files found in %s.\n", dir), nil
	}

	pkg := &pkgDoc{}
	fset := token.NewFileSet()

	for _, path := range files {
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			continue
		}

		if pkg.Name == "" {
			pkg.Name = file.Name.Name
		}

		if file.Doc != nil && pkg.PkgDoc == "" {
			pkg.PkgDoc = strings.TrimSpace(file.Doc.Text())
		}

		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				exported := ast.IsExported(d.Name.Name)
				if !exported && !includeUnexported {
					continue
				}
				sig := buildFuncSignature(d)
				doc := ""
				if d.Doc != nil {
					doc = strings.TrimSpace(d.Doc.Text())
				}
				pkg.Funcs = append(pkg.Funcs, symDoc{
					Name:      d.Name.Name,
					Kind:      "func",
					Signature: sig,
					Doc:       doc,
					Exported:  exported,
				})
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch sp := spec.(type) {
					case *ast.TypeSpec:
						exported := ast.IsExported(sp.Name.Name)
						if !exported && !includeUnexported {
							continue
						}
						doc := ""
						if d.Doc != nil {
							doc = strings.TrimSpace(d.Doc.Text())
						} else if sp.Doc != nil {
							doc = strings.TrimSpace(sp.Doc.Text())
						}
						kind := "type"
						switch sp.Type.(type) {
						case *ast.InterfaceType:
							kind = "interface"
						case *ast.StructType:
							kind = "struct"
						}
						pkg.Types = append(pkg.Types, symDoc{
							Name:      sp.Name.Name,
							Kind:      kind,
							Signature: "type " + sp.Name.Name,
							Doc:       doc,
							Exported:  exported,
						})
					case *ast.ValueSpec:
						kind := "var"
						if d.Tok == token.CONST {
							kind = "const"
						}
						for _, n := range sp.Names {
							exported := ast.IsExported(n.Name)
							if !exported && !includeUnexported {
								continue
							}
							doc := ""
							if d.Doc != nil {
								doc = strings.TrimSpace(d.Doc.Text())
							}
							sd := symDoc{
								Name:     n.Name,
								Kind:     kind,
								Doc:      doc,
								Exported: exported,
							}
							if kind == "const" {
								pkg.Constants = append(pkg.Constants, sd)
							} else {
								pkg.Variables = append(pkg.Variables, sd)
							}
						}
					}
				}
			}
		}
	}

	return renderPackageDoc(pkg), nil
}

func buildFuncSignature(fn *ast.FuncDecl) string {
	var sb strings.Builder
	sb.WriteString("func ")
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		sb.WriteString("(")
		if len(fn.Recv.List[0].Names) > 0 {
			sb.WriteString(fn.Recv.List[0].Names[0].Name + " ")
		}
		sb.WriteString(exprString(fn.Recv.List[0].Type))
		sb.WriteString(") ")
	}
	sb.WriteString(fn.Name.Name)
	sb.WriteString("(")
	if fn.Type.Params != nil {
		first := true
		for _, p := range fn.Type.Params.List {
			if !first {
				sb.WriteString(", ")
			}
			first = false
			if len(p.Names) > 0 {
				var names []string
				for _, n := range p.Names {
					names = append(names, n.Name)
				}
				sb.WriteString(strings.Join(names, ", "))
				sb.WriteString(" ")
			}
			sb.WriteString(exprString(p.Type))
		}
	}
	sb.WriteString(")")
	if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 {
		sb.WriteString(" ")
		multi := len(fn.Type.Results.List) > 1 || len(fn.Type.Results.List[0].Names) > 0
		if multi {
			sb.WriteString("(")
		}
		first := true
		for _, r := range fn.Type.Results.List {
			if !first {
				sb.WriteString(", ")
			}
			first = false
			sb.WriteString(exprString(r.Type))
		}
		if multi {
			sb.WriteString(")")
		}
	}
	return sb.String()
}

func exprString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + exprString(t.X)
	case *ast.SelectorExpr:
		return exprString(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		return "[]" + exprString(t.Elt)
	case *ast.MapType:
		return "map[" + exprString(t.Key) + "]" + exprString(t.Value)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.FuncType:
		return "func(...)"
	case *ast.Ellipsis:
		return "..." + exprString(t.Elt)
	case *ast.ChanType:
		return "chan " + exprString(t.Value)
	}
	return "any"
}

func renderPackageDoc(pkg *pkgDoc) string {
	var sb strings.Builder

	sb.WriteString("# Package " + pkg.Name + "\n\n")

	if pkg.PkgDoc != "" {
		sb.WriteString(pkg.PkgDoc + "\n\n")
	}

	if len(pkg.Constants) > 0 {
		sb.WriteString("## Constants\n\n")
		sortSymDocs(pkg.Constants)
		for _, c := range pkg.Constants {
			sb.WriteString("- `" + c.Name + "`")
			if c.Doc != "" {
				sb.WriteString(" — " + firstLine(c.Doc))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	if len(pkg.Variables) > 0 {
		sb.WriteString("## Variables\n\n")
		sortSymDocs(pkg.Variables)
		for _, v := range pkg.Variables {
			sb.WriteString("- `" + v.Name + "`")
			if v.Doc != "" {
				sb.WriteString(" — " + firstLine(v.Doc))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	if len(pkg.Types) > 0 {
		sb.WriteString("## Types\n\n")
		sortSymDocs(pkg.Types)
		for _, t := range pkg.Types {
			sb.WriteString("### " + t.Name + " (" + t.Kind + ")\n\n")
			if t.Doc != "" {
				sb.WriteString(t.Doc + "\n\n")
			}
		}
	}

	if len(pkg.Funcs) > 0 {
		sb.WriteString("## Functions\n\n")
		sortSymDocs(pkg.Funcs)
		for _, f := range pkg.Funcs {
			sb.WriteString("### " + f.Name + "\n\n")
			sb.WriteString("```go\n" + f.Signature + "\n```\n\n")
			if f.Doc != "" {
				sb.WriteString(f.Doc + "\n\n")
			}
		}
	}

	return sb.String()
}

func sortSymDocs(s []symDoc) {
	sort.Slice(s, func(i, j int) bool {
		return s[i].Name < s[j].Name
	})
}

func firstLine(s string) string {
	if idx := strings.Index(s, "\n"); idx >= 0 {
		return s[:idx]
	}
	return s
}
