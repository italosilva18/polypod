package codemap

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// FileInfo holds info about a file and its symbols.
type FileInfo struct {
	Path     string
	Language string
	Size     int64
	Symbols  []Symbol
}

// Symbol is a named code entity (function, type, class, etc.).
type Symbol struct {
	Name string
	Kind string // "function", "type", "method", "interface", "class", "const", "var"
	Line int
}

// Mapper scans a codebase and extracts structure.
type Mapper struct {
	root   string
	logger *slog.Logger
}

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "__pycache__": true,
	".venv": true, "venv": true, "dist": true, "build": true, ".next": true,
	"target": true, ".idea": true, ".vscode": true,
}

var langExtensions = map[string]string{
	".go": "go", ".py": "python", ".js": "javascript", ".ts": "typescript",
	".jsx": "jsx", ".tsx": "tsx", ".java": "java", ".rs": "rust",
	".rb": "ruby", ".php": "php", ".c": "c", ".cpp": "cpp", ".h": "c",
	".cs": "csharp", ".swift": "swift", ".kt": "kotlin", ".sh": "bash",
	".yaml": "yaml", ".yml": "yaml", ".json": "json", ".md": "markdown",
	".sql": "sql", ".html": "html", ".css": "css", ".dart": "dart",
}

// Go patterns
var goFuncRe = regexp.MustCompile(`(?m)^func\s+(?:\(.*?\)\s+)?(\w+)`)
var goTypeRe = regexp.MustCompile(`(?m)^type\s+(\w+)\s+(struct|interface)`)

// Python patterns
var pyDefRe = regexp.MustCompile(`(?m)^(?:class|def)\s+(\w+)`)

// JS/TS patterns
var jsFuncRe = regexp.MustCompile(`(?m)(?:export\s+)?(?:function|class|const|let|var)\s+(\w+)`)

// NewMapper creates a codebase mapper.
func NewMapper(root string, logger *slog.Logger) *Mapper {
	return &Mapper{root: root, logger: logger}
}

// Map scans the codebase and returns all files with their symbols.
func (m *Mapper) Map() ([]FileInfo, error) {
	var files []FileInfo

	err := filepath.Walk(m.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		ext := filepath.Ext(path)
		lang, ok := langExtensions[ext]
		if !ok {
			return nil
		}

		relPath, _ := filepath.Rel(m.root, path)
		fi := FileInfo{
			Path:     relPath,
			Language: lang,
			Size:     info.Size(),
		}

		// Only parse symbols for code files < 1MB
		if info.Size() < 1024*1024 {
			data, err := os.ReadFile(path)
			if err == nil {
				fi.Symbols = extractSymbols(lang, string(data))
			}
		}

		files = append(files, fi)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func extractSymbols(lang, content string) []Symbol {
	lines := strings.Split(content, "\n")
	var symbols []Symbol

	switch lang {
	case "go":
		for i, line := range lines {
			if m := goFuncRe.FindStringSubmatch(line); m != nil {
				kind := "function"
				if strings.Contains(line, ") ") && strings.Index(line, ")") < strings.Index(line, m[1]) {
					kind = "method"
				}
				symbols = append(symbols, Symbol{Name: m[1], Kind: kind, Line: i + 1})
			}
			if m := goTypeRe.FindStringSubmatch(line); m != nil {
				symbols = append(symbols, Symbol{Name: m[1], Kind: m[2], Line: i + 1})
			}
		}
	case "python":
		for i, line := range lines {
			if m := pyDefRe.FindStringSubmatch(line); m != nil {
				kind := "function"
				if strings.HasPrefix(strings.TrimSpace(line), "class") {
					kind = "class"
				}
				symbols = append(symbols, Symbol{Name: m[1], Kind: kind, Line: i + 1})
			}
		}
	case "javascript", "typescript", "jsx", "tsx":
		for i, line := range lines {
			if m := jsFuncRe.FindStringSubmatch(line); m != nil {
				kind := "function"
				if strings.Contains(line, "class ") {
					kind = "class"
				}
				symbols = append(symbols, Symbol{Name: m[1], Kind: kind, Line: i + 1})
			}
		}
	}

	return symbols
}

// FormatRepoMap formats the file list as a tree string.
func FormatRepoMap(files []FileInfo) string {
	var sb strings.Builder
	for _, f := range files {
		fmt.Fprintf(&sb, "%s  [%s, %d bytes]\n", f.Path, f.Language, f.Size)
		for _, s := range f.Symbols {
			fmt.Fprintf(&sb, "  %s %s (line %d)\n", s.Kind, s.Name, s.Line)
		}
	}
	return sb.String()
}

// RegisterSkills registers codebase mapping skills.
func RegisterSkills(reg *skill.Registry, root string, logger *slog.Logger) {
	mapper := NewMapper(root, logger)

	reg.Register(&skill.Skill{
		Name:        "repo_map",
		Description: "Gerar mapa da estrutura do codebase (arquivos, funcoes, tipos, classes)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path": {Type: jsonschema.String, Description: "Diretorio raiz (default: diretorio atual)"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			m := mapper
			if p := args["path"]; p != "" {
				m = NewMapper(p, logger)
			}
			files, err := m.Map()
			if err != nil {
				return "", err
			}
			if len(files) == 0 {
				return "Nenhum arquivo de codigo encontrado.", nil
			}
			result := FormatRepoMap(files)
			if len(result) > 10000 {
				result = result[:10000] + "\n... [truncado]"
			}
			return result, nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "find_symbol",
		Description: "Encontrar onde um simbolo (funcao, tipo, classe) e definido no codebase",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"name": {Type: jsonschema.String, Description: "Nome do simbolo a buscar"},
			},
			Required: []string{"name"},
		},
		Execute: func(args map[string]string) (string, error) {
			files, err := mapper.Map()
			if err != nil {
				return "", err
			}
			name := args["name"]
			var found []string
			for _, f := range files {
				for _, s := range f.Symbols {
					if strings.EqualFold(s.Name, name) {
						found = append(found, fmt.Sprintf("%s:%d  %s %s", f.Path, s.Line, s.Kind, s.Name))
					}
				}
			}
			if len(found) == 0 {
				return fmt.Sprintf("Simbolo '%s' nao encontrado.", name), nil
			}
			return strings.Join(found, "\n"), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "project_info",
		Description: "Mostrar informacoes sobre o projeto (linguagem, estrutura, dependencias)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path": {Type: jsonschema.String, Description: "Diretorio do projeto (default: .)"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			path := args["path"]
			if path == "" {
				path = "."
			}
			var sb strings.Builder

			// Detect project type
			checks := []struct {
				file string
				desc string
			}{
				{"go.mod", "Go (go.mod)"},
				{"package.json", "Node.js (package.json)"},
				{"Cargo.toml", "Rust (Cargo.toml)"},
				{"pyproject.toml", "Python (pyproject.toml)"},
				{"requirements.txt", "Python (requirements.txt)"},
				{"pom.xml", "Java/Maven (pom.xml)"},
				{"build.gradle", "Java/Gradle (build.gradle)"},
				{"Gemfile", "Ruby (Gemfile)"},
				{"pubspec.yaml", "Dart/Flutter (pubspec.yaml)"},
				{"docker-compose.yml", "Docker Compose"},
				{"Dockerfile", "Docker"},
				{"Makefile", "Makefile"},
			}

			sb.WriteString("## Projeto\n\n")
			for _, c := range checks {
				if _, err := os.Stat(filepath.Join(path, c.file)); err == nil {
					fmt.Fprintf(&sb, "- %s\n", c.desc)
				}
			}

			// Count files by language
			m := NewMapper(path, logger)
			files, err := m.Map()
			if err == nil && len(files) > 0 {
				langCount := make(map[string]int)
				for _, f := range files {
					langCount[f.Language]++
				}
				sb.WriteString("\n## Arquivos por linguagem\n\n")
				for lang, count := range langCount {
					fmt.Fprintf(&sb, "- %s: %d arquivos\n", lang, count)
				}
				fmt.Fprintf(&sb, "\nTotal: %d arquivos de codigo\n", len(files))
			}

			return sb.String(), nil
		},
	})
}
