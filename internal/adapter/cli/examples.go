package cli

import (
	"math/rand"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// ExampleCommand describes one example prompt the user can try from the
// welcome screen. Description is shown as a short bullet, Command is the
// actual text the user can copy/paste, and Category groups examples by
// intent so the welcome screen can show a balanced selection.
type ExampleCommand struct {
	Description string
	Command     string
	Category    string // "code", "git", "search", "explain", "refactor"
}

// examples is the canonical list of built-in suggestions. The Portuguese
// phrasing matches the rest of the TUI copy.
var examples = []ExampleCommand{
	// ── Code understanding (explain) ──
	{Description: "Explicar a estrutura do projeto", Command: "Explique a arquitetura do projeto e os principais componentes", Category: "explain"},
	{Description: "Encontrar bugs", Command: "Analise main.go e procure por possiveis bugs", Category: "explain"},
	{Description: "Resumir um pacote", Command: "Resuma o que faz o pacote internal/auth e como ele se conecta com o resto", Category: "explain"},
	{Description: "Listar pontos de entrada", Command: "Liste os pontos de entrada principais e o fluxo de inicializacao", Category: "explain"},
	{Description: "Mapear dependencias", Command: "Mostre quais pacotes dependem de internal/conversation", Category: "explain"},

	// ── Code (write/test/doc) ──
	{Description: "Documentar funcoes", Command: "Adicione comentarios doc para todas as funcoes em internal/auth/", Category: "code"},
	{Description: "Implementar funcao", Command: "Implemente uma funcao para validar emails seguindo RFC 5322", Category: "code"},
	{Description: "Adicionar testes", Command: "Adicione testes unitarios para internal/parser.go cobrindo casos de borda", Category: "code"},
	{Description: "Build e fix", Command: "Execute go build ./... e corrija qualquer erro de compilacao", Category: "code"},
	{Description: "Run tests", Command: "Execute go test ./... e analise os resultados", Category: "code"},
	{Description: "Lint", Command: "Execute go vet ./... e corrija os avisos", Category: "code"},

	// ── Git ──
	{Description: "Resumir mudancas", Command: "/diff", Category: "git"},
	{Description: "Criar commit", Command: "/commit", Category: "git"},
	{Description: "Ver historico", Command: "/git log", Category: "git"},
	{Description: "Status do repo", Command: "/git status", Category: "git"},
	{Description: "Review de mudancas", Command: "/review --staged", Category: "git"},

	// ── Search ──
	{Description: "Buscar funcao", Command: "Procure por todas as funcoes que validam input do usuario", Category: "search"},
	{Description: "Encontrar TODOs", Command: "Liste todos os TODOs e FIXMEs no codigo", Category: "search"},
	{Description: "Caca a logs", Command: "Encontre uses de fmt.Println que deveriam ser logs estruturados", Category: "search"},
	{Description: "Tipos exportados", Command: "Liste os tipos exportados em internal/adapter", Category: "search"},

	// ── Refactor ──
	{Description: "Extrair funcao", Command: "Refatore a funcao processData em main.go para extrair a logica de validacao", Category: "refactor"},
	{Description: "Renomear", Command: "Renomeie a variavel cfg para config em todos os arquivos do pacote atual", Category: "refactor"},
	{Description: "Quebrar arquivo grande", Command: "O arquivo handler.go esta gigante — sugira como quebrar em arquivos menores", Category: "refactor"},
	{Description: "Simplificar logica", Command: "Simplifique a funcao mais longa em main.go mantendo o comportamento", Category: "refactor"},
}

// projectExamples returns examples that are relevant to the given project
// type. The keys are normalised lowercase language names as returned by
// codemap (ProjectIndex.PrimaryLanguage). Unknown project types fall back
// to a generic mix.
var projectExamples = map[string][]ExampleCommand{
	"go": {
		{Description: "Build e fix", Command: "Execute go build ./... e corrija qualquer erro de compilacao", Category: "code"},
		{Description: "Run tests", Command: "Execute go test ./... e analise os resultados", Category: "code"},
		{Description: "Vet", Command: "Execute go vet ./... e corrija os avisos", Category: "code"},
		{Description: "Modulos", Command: "Verifique se ha dependencias nao usadas em go.mod", Category: "explain"},
	},
	"javascript": {
		{Description: "Install", Command: "Execute npm install e mostre se ha vulnerabilidades", Category: "code"},
		{Description: "Run tests", Command: "Execute npm test e analise os resultados", Category: "code"},
		{Description: "Lint", Command: "Execute eslint e corrija os erros mais simples", Category: "code"},
	},
	"typescript": {
		{Description: "Type check", Command: "Execute tsc --noEmit e corrija os erros de tipo", Category: "code"},
		{Description: "Run tests", Command: "Execute npm test e analise os resultados", Category: "code"},
		{Description: "Refatorar tipos", Command: "Procure por usos de any e sugira tipos mais especificos", Category: "refactor"},
	},
	"python": {
		{Description: "Run tests", Command: "Execute pytest e analise os resultados", Category: "code"},
		{Description: "Lint", Command: "Execute ruff check . e corrija os avisos", Category: "code"},
		{Description: "Type check", Command: "Execute mypy . e corrija os erros de tipo", Category: "code"},
	},
	"rust": {
		{Description: "Build", Command: "Execute cargo build e corrija qualquer erro", Category: "code"},
		{Description: "Run tests", Command: "Execute cargo test e analise os resultados", Category: "code"},
		{Description: "Clippy", Command: "Execute cargo clippy e corrija os avisos", Category: "code"},
	},
	"java": {
		{Description: "Build", Command: "Execute mvn compile e corrija qualquer erro", Category: "code"},
		{Description: "Run tests", Command: "Execute mvn test e analise os resultados", Category: "code"},
	},
	"ruby": {
		{Description: "Run tests", Command: "Execute bundle exec rspec e analise os resultados", Category: "code"},
		{Description: "Rubocop", Command: "Execute rubocop e corrija os avisos mais simples", Category: "code"},
	},
}

// AllExamples returns the full built-in list of example commands.
func AllExamples() []ExampleCommand {
	out := make([]ExampleCommand, len(examples))
	copy(out, examples)
	return out
}

// ExamplesForProject returns examples relevant to the detected project
// type, in addition to a small generic baseline. projectType uses the
// same lowercase identifiers as codemap.ProjectIndex.PrimaryLanguage
// (e.g. "go", "javascript", "python"). Unknown values yield only the
// generic baseline.
func ExamplesForProject(projectType string) []ExampleCommand {
	key := strings.ToLower(strings.TrimSpace(projectType))

	// Generic baseline that applies regardless of language: a couple of
	// explain/git/search prompts so even unknown project types get a
	// useful list.
	baseline := []ExampleCommand{
		{Description: "Explicar a estrutura do projeto", Command: "Explique a arquitetura do projeto e os principais componentes", Category: "explain"},
		{Description: "Encontrar TODOs", Command: "Liste todos os TODOs e FIXMEs no codigo", Category: "search"},
		{Description: "Resumir mudancas", Command: "/diff", Category: "git"},
		{Description: "Criar commit", Command: "/commit", Category: "git"},
	}

	specific, ok := projectExamples[key]
	if !ok {
		return baseline
	}
	out := make([]ExampleCommand, 0, len(baseline)+len(specific))
	out = append(out, specific...)
	out = append(out, baseline...)
	return out
}

// RandomExamples returns up to n random examples. When category is
// non-empty, only examples in that category are eligible. The same
// example is never returned twice in a single call. If n exceeds the
// number of available examples, all of them are returned (shuffled).
func RandomExamples(n int, category string) []ExampleCommand {
	if n <= 0 {
		return nil
	}
	pool := make([]ExampleCommand, 0, len(examples))
	cat := strings.ToLower(strings.TrimSpace(category))
	for _, e := range examples {
		if cat == "" || strings.EqualFold(e.Category, cat) {
			pool = append(pool, e)
		}
	}
	if len(pool) == 0 {
		return nil
	}

	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	r.Shuffle(len(pool), func(i, j int) {
		pool[i], pool[j] = pool[j], pool[i]
	})
	if n > len(pool) {
		n = len(pool)
	}
	return pool[:n]
}

// ── Formatting ──────────────────────────────────────────────────────────────

var (
	exampleHeaderStyle = lipgloss.NewStyle().
				Foreground(colorMagenta).
				Bold(true)

	exampleCategoryStyle = lipgloss.NewStyle().
				Foreground(colorCyan).
				Bold(true)

	exampleBulletStyle = lipgloss.NewStyle().
				Foreground(colorGreen)

	exampleDescStyle = lipgloss.NewStyle().
				Foreground(colorWhiteDim)

	exampleCmdStyle = lipgloss.NewStyle().
			Foreground(colorBlue)

	exampleHintStyle = lipgloss.NewStyle().
				Foreground(colorSubtle).
				Italic(true)
)

// categoryLabels maps a category id to a human-readable label and an
// emoji-free icon prefix that fits the rest of the TUI copy.
var categoryLabels = []struct {
	key   string
	label string
}{
	{"explain", "Entender o codigo"},
	{"code", "Codificar"},
	{"search", "Buscar e analisar"},
	{"refactor", "Refatorar"},
	{"git", "Comandos rapidos"},
}

// FormatExamples renders the given examples as a styled multi-line
// block, grouped by category. The output is intended to be embedded
// inside the welcome screen.
func FormatExamples(examplesIn []ExampleCommand) string {
	if len(examplesIn) == 0 {
		return ""
	}

	// Group by category preserving the canonical order from
	// categoryLabels. Examples whose category is not in that list are
	// appended at the end under "Outros".
	grouped := make(map[string][]ExampleCommand, len(categoryLabels))
	for _, e := range examplesIn {
		grouped[strings.ToLower(e.Category)] = append(grouped[strings.ToLower(e.Category)], e)
	}

	var b strings.Builder
	b.WriteString(exampleHeaderStyle.Render("EXEMPLOS PARA COMECAR"))
	b.WriteString("\n\n")

	rendered := 0
	for _, cat := range categoryLabels {
		items, ok := grouped[cat.key]
		if !ok || len(items) == 0 {
			continue
		}
		b.WriteString("  ")
		b.WriteString(exampleCategoryStyle.Render(cat.label + ":"))
		b.WriteString("\n")
		for _, item := range items {
			b.WriteString("    ")
			b.WriteString(exampleBulletStyle.Render("> "))
			if cat.key == "git" {
				// For git/quick commands the Command itself is the
				// short form (e.g. "/diff"), so render command + desc.
				b.WriteString(exampleCmdStyle.Render(padRight(item.Command, 12)))
				b.WriteString("  ")
				b.WriteString(exampleDescStyle.Render(item.Description))
			} else {
				b.WriteString(exampleDescStyle.Render(item.Description))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
		rendered++
		delete(grouped, cat.key)
	}

	// Append any leftover categories under "Outros".
	if len(grouped) > 0 {
		b.WriteString("  ")
		b.WriteString(exampleCategoryStyle.Render("Outros:"))
		b.WriteString("\n")
		for _, items := range grouped {
			for _, item := range items {
				b.WriteString("    ")
				b.WriteString(exampleBulletStyle.Render("> "))
				b.WriteString(exampleDescStyle.Render(item.Description))
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")
		rendered++
	}

	if rendered == 0 {
		return ""
	}

	b.WriteString("  ")
	b.WriteString(exampleHintStyle.Render("Digite sua mensagem ou copie um exemplo acima."))
	b.WriteString("\n")
	return b.String()
}

// padRight pads s with spaces on the right until it reaches width
// runes. It never truncates.
func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
