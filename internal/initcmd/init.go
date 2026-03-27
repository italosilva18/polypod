package initcmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DetectedStack holds info about the detected project stack.
type DetectedStack struct {
	Language   string
	Framework  string
	BuildTool  string
	TestCmd    string
	LintCmd    string
	HasGit     bool
	HasDocker  bool
	HasCI      bool
}

// Detect analyzes the current directory to detect project stack.
func Detect(dir string) DetectedStack {
	s := DetectedStack{}

	// Language detection
	checks := []struct {
		file string
		lang string
		fw   string
		test string
		lint string
	}{
		{"go.mod", "Go", "", "go test ./...", "go vet ./..."},
		{"package.json", "JavaScript/TypeScript", "", "npm test", "npx eslint ."},
		{"pyproject.toml", "Python", "", "python3 -m pytest", "ruff check ."},
		{"requirements.txt", "Python", "", "python3 -m pytest", "ruff check ."},
		{"Cargo.toml", "Rust", "", "cargo test", "cargo clippy"},
		{"Gemfile", "Ruby", "Rails", "bundle exec rspec", "bundle exec rubocop"},
		{"pubspec.yaml", "Dart", "Flutter", "flutter test", "flutter analyze"},
		{"pom.xml", "Java", "Maven", "mvn test", ""},
		{"build.gradle", "Java/Kotlin", "Gradle", "./gradlew test", ""},
	}
	for _, c := range checks {
		if fileExists(filepath.Join(dir, c.file)) {
			s.Language = c.lang
			s.Framework = c.fw
			s.TestCmd = c.test
			s.LintCmd = c.lint
			break
		}
	}

	// Framework detection refinements
	if s.Language == "JavaScript/TypeScript" {
		if fileExists(filepath.Join(dir, "next.config.js")) || fileExists(filepath.Join(dir, "next.config.ts")) {
			s.Framework = "Next.js"
		} else if fileExists(filepath.Join(dir, "vite.config.ts")) || fileExists(filepath.Join(dir, "vite.config.js")) {
			s.Framework = "Vite"
		} else if fileExists(filepath.Join(dir, "nuxt.config.ts")) {
			s.Framework = "Nuxt"
		}
	}
	if s.Language == "Go" {
		if dirContains(dir, "gin") {
			s.Framework = "Gin"
		} else if dirContains(dir, "fiber") {
			s.Framework = "Fiber"
		} else if dirContains(dir, "chi") {
			s.Framework = "Chi"
		}
	}
	if s.Language == "Python" {
		if fileExists(filepath.Join(dir, "manage.py")) {
			s.Framework = "Django"
		} else if dirContains(dir, "fastapi") {
			s.Framework = "FastAPI"
		} else if dirContains(dir, "flask") {
			s.Framework = "Flask"
		}
	}

	// Build tools
	if fileExists(filepath.Join(dir, "Makefile")) {
		s.BuildTool = "Make"
	} else if fileExists(filepath.Join(dir, "Taskfile.yml")) {
		s.BuildTool = "Task"
	} else if fileExists(filepath.Join(dir, "justfile")) {
		s.BuildTool = "Just"
	}

	// Git, Docker, CI
	s.HasGit = fileExists(filepath.Join(dir, ".git"))
	s.HasDocker = fileExists(filepath.Join(dir, "Dockerfile")) || fileExists(filepath.Join(dir, "docker-compose.yml"))
	s.HasCI = fileExists(filepath.Join(dir, ".github")) || fileExists(filepath.Join(dir, ".gitlab-ci.yml"))

	return s
}

// GeneratePolypodMD creates a .polypod.md from detected stack.
func GeneratePolypodMD(stack DetectedStack) string {
	var sb strings.Builder

	sb.WriteString("# Polypod - Instrucoes do Projeto\n\n")

	sb.WriteString("## Stack\n")
	if stack.Language != "" {
		fmt.Fprintf(&sb, "- Linguagem: %s\n", stack.Language)
	}
	if stack.Framework != "" {
		fmt.Fprintf(&sb, "- Framework: %s\n", stack.Framework)
	}
	if stack.BuildTool != "" {
		fmt.Fprintf(&sb, "- Build: %s\n", stack.BuildTool)
	}
	if stack.HasDocker {
		sb.WriteString("- Deploy: Docker\n")
	}
	if stack.HasCI {
		sb.WriteString("- CI/CD: Configurado\n")
	}

	sb.WriteString("\n## Regras\n")
	sb.WriteString("- Responda no idioma do usuario\n")
	sb.WriteString("- Seja conciso e direto\n")
	sb.WriteString("- USE as ferramentas para investigar antes de responder\n")

	if stack.TestCmd != "" {
		fmt.Fprintf(&sb, "- Antes de commitar, rode: `%s`\n", stack.TestCmd)
	}
	if stack.LintCmd != "" {
		fmt.Fprintf(&sb, "- Verifique com: `%s`\n", stack.LintCmd)
	}

	sb.WriteString("\n## Contexto\n")
	sb.WriteString("Adicione aqui informacoes importantes sobre o projeto.\n")

	return sb.String()
}

// GenerateHooksYAML creates initial hooks based on detected stack.
func GenerateHooksYAML(stack DetectedStack) string {
	var sb strings.Builder
	sb.WriteString("hooks:\n")

	if stack.LintCmd != "" {
		fmt.Fprintf(&sb, `  - name: auto-lint
    event: PostToolUse
    type: shell
    matcher: "edit_file"
    command: "%s"
    enabled: false
`, stack.LintCmd)
	}

	sb.WriteString(`  - name: block-force-push
    event: PreToolUse
    type: shell
    matcher: "git_push"
    command: |
      INPUT=$(cat)
      if echo "$INPUT" | grep -q '"force":"true"'; then
        echo '{"decision":"deny","message":"Force push bloqueado por hook"}'
      else
        echo '{"decision":"allow"}'
      fi
    enabled: true
`)
	return sb.String()
}

// WriteInit writes .polypod.md and .polypod/ directory structure.
func WriteInit(dir string, stack DetectedStack) ([]string, error) {
	var created []string

	// .polypod.md
	mdPath := filepath.Join(dir, ".polypod.md")
	if !fileExists(mdPath) {
		content := GeneratePolypodMD(stack)
		if err := os.WriteFile(mdPath, []byte(content), 0644); err != nil {
			return nil, err
		}
		created = append(created, mdPath)
	}

	// .polypod/ directory
	polypodDir := filepath.Join(dir, ".polypod")
	os.MkdirAll(filepath.Join(polypodDir, "commands"), 0755)
	os.MkdirAll(filepath.Join(polypodDir, "recipes"), 0755)

	// Example command
	cmdPath := filepath.Join(polypodDir, "commands", "test.md")
	if !fileExists(cmdPath) {
		testCmd := stack.TestCmd
		if testCmd == "" {
			testCmd = "echo 'nenhum teste configurado'"
		}
		content := fmt.Sprintf("---\nname: test\ndescription: Executar testes do projeto\n---\n\nExecute os testes: `%s`\n\nSe algum falhar, analise o erro e sugira correção.\n", testCmd)
		os.WriteFile(cmdPath, []byte(content), 0644)
		created = append(created, cmdPath)
	}

	// settings.json
	settingsPath := filepath.Join(polypodDir, "settings.json")
	if !fileExists(settingsPath) {
		os.WriteFile(settingsPath, []byte(`{
  "denied_tools": [],
  "ask_tools": [
    {"pattern": "git_push", "decision": "ask"},
    {"pattern": "delete_file", "decision": "ask"}
  ],
  "allowed_tools": [
    {"pattern": "read_*", "decision": "allow"},
    {"pattern": "git_status", "decision": "allow"},
    {"pattern": "git_diff", "decision": "allow"},
    {"pattern": "git_log", "decision": "allow"}
  ]
}
`), 0644)
		created = append(created, settingsPath)
	}

	return created, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func dirContains(dir, pattern string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		data, err = os.ReadFile(filepath.Join(dir, "package.json"))
		if err != nil {
			data, err = os.ReadFile(filepath.Join(dir, "requirements.txt"))
			if err != nil {
				return false
			}
		}
	}
	return strings.Contains(strings.ToLower(string(data)), strings.ToLower(pattern))
}
