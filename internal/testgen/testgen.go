package testgen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// GenerateTestPrompt creates a prompt for the AI to generate tests.
func GenerateTestPrompt(filePath, content, language string) string {
	var sb strings.Builder

	sb.WriteString("Gere testes unitarios completos para o seguinte codigo.\n\n")

	switch language {
	case "go":
		sb.WriteString("Regras para Go:\n")
		sb.WriteString("- Use table-driven tests (subtests com t.Run)\n")
		sb.WriteString("- Inclua casos: sucesso, erro, edge cases (nil, vazio, limites)\n")
		sb.WriteString("- Nome do arquivo: " + strings.TrimSuffix(filepath.Base(filePath), ".go") + "_test.go\n")
		sb.WriteString("- Package: mesmo package do arquivo original\n")
		sb.WriteString("- Use testify se necessario, senao stdlib\n")
	case "python":
		sb.WriteString("Regras para Python:\n")
		sb.WriteString("- Use pytest com parametrize\n")
		sb.WriteString("- Inclua fixtures quando necessario\n")
		sb.WriteString("- Nome: test_" + strings.TrimSuffix(filepath.Base(filePath), ".py") + ".py\n")
		sb.WriteString("- Teste edge cases: None, empty, invalid input\n")
	case "javascript", "typescript":
		sb.WriteString("Regras para JS/TS:\n")
		sb.WriteString("- Use describe/it blocks (vitest ou jest)\n")
		sb.WriteString("- Inclua beforeEach/afterEach quando necessario\n")
		sb.WriteString("- Nome: " + strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath)) + ".test" + filepath.Ext(filePath) + "\n")
		sb.WriteString("- Mock dependencias externas\n")
	case "rust":
		sb.WriteString("Regras para Rust:\n")
		sb.WriteString("- Use #[cfg(test)] mod tests\n")
		sb.WriteString("- Inclua #[test] para cada caso\n")
		sb.WriteString("- Teste com assert_eq!, assert!, should_panic\n")
	}

	sb.WriteString("\n## Codigo fonte\n")
	fmt.Fprintf(&sb, "Arquivo: %s\n\n```%s\n%s\n```\n\n", filePath, language, content)
	sb.WriteString("Retorne APENAS o codigo dos testes, sem explicacao adicional.\n")
	sb.WriteString("Inclua todos os imports necessarios.\n")

	return sb.String()
}

// TestFilePath returns the conventional test file path for a source file.
func TestFilePath(sourcePath, language string) string {
	dir := filepath.Dir(sourcePath)
	base := filepath.Base(sourcePath)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)

	switch language {
	case "go":
		return filepath.Join(dir, name+"_test.go")
	case "python":
		return filepath.Join(dir, "test_"+name+".py")
	case "javascript":
		return filepath.Join(dir, name+".test.js")
	case "typescript":
		return filepath.Join(dir, name+".test.ts")
	case "rust":
		return filepath.Join(dir, name+"_test.rs")
	default:
		return filepath.Join(dir, name+"_test"+ext)
	}
}

// DetectLanguage detects programming language from file extension.
func DetectLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	langs := map[string]string{
		".go": "go", ".py": "python", ".js": "javascript", ".ts": "typescript",
		".jsx": "javascript", ".tsx": "typescript", ".rs": "rust",
		".rb": "ruby", ".java": "java", ".cs": "csharp",
	}
	if l, ok := langs[ext]; ok {
		return l
	}
	return "unknown"
}

// RegisterSkills registers test generation skills.
func RegisterSkills(reg *skill.Registry) {
	reg.Register(&skill.Skill{
		Name:        "test_generate",
		Description: "Gerar testes unitarios para um arquivo (Go: table-driven, Python: pytest, JS: vitest)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path": {Type: jsonschema.String, Description: "Caminho do arquivo fonte"},
			},
			Required: []string{"path"},
		},
		Execute: func(args map[string]string) (string, error) {
			path := args["path"]
			data, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("lendo arquivo: %w", err)
			}

			lang := DetectLanguage(path)
			if lang == "unknown" {
				return "", fmt.Errorf("linguagem nao reconhecida para %s", path)
			}

			testPath := TestFilePath(path, lang)
			prompt := GenerateTestPrompt(path, string(data), lang)

			return fmt.Sprintf("[TEST_GENERATE]\nfonte: %s\ntestes: %s\nlinguagem: %s\n\n%s", path, testPath, lang, prompt), nil
		},
	})
}
