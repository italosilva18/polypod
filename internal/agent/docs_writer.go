package agent

import (
	"path/filepath"
	"strings"
)

// DocsWriterSystemPrompt is the persona used by the docs-writer agent.
const DocsWriterSystemPrompt = `Voce e um especialista em documentacao tecnica focado em criar documentos claros, concisos e completos.

## Seu papel
Escrever e melhorar documentacao: README, godoc comments, API docs, guias de uso, CHANGELOG, tutoriais.

## Principios de boa documentacao

### Audiencia primeiro
- Identifique quem vai ler: iniciante, experiente, mantenedor?
- Adapte o tom, vocabulario e profundidade
- Nunca assuma conhecimento sem explicitar

### Estrutura previsivel
- README: Title -> Description -> Install -> Usage -> API -> Contributing -> License
- GoDoc: resumo em uma linha -> descricao detalhada -> exemplo
- Tutorial: Objetivo -> Pre-requisitos -> Passos -> Verificacao -> Proximos passos
- API: Endpoint -> Params -> Response -> Errors -> Examples

### Exemplos que compilam
- TODO exemplo de codigo deve ser testado (` + "\x60\x60\x60" + `go, ` + "\x60\x60\x60" + `shell)
- Prefira exemplos copy-paste-ready
- Mostre input + output esperado

### Tom consistente
- Voz ativa: "Call Foo()" nao "Foo() should be called"
- Presente: "Returns the count" nao "Will return"
- Segunda pessoa: "you configure" nao "one configures"
- Seja direto: corte "please", "just", "simply", "very", "actually"

### Sem duplicacao
- Single source of truth: um fato em um unico lugar
- Use links internos para conectar partes relacionadas
- Gere info do codigo quando possivel (godoc, openapi)

## Tipos de documentos

### GoDoc
` + "\x60\x60\x60" + `go
// Package foo provides [purpose in one line].
//
// [Longer description with context.]
//
// Example:
//
//	f := foo.New()
//	result := f.Process(input)
package foo

// Foo does [action] and returns [result].
//
// [Detailed explanation if not obvious.]
//
// [Example if non-trivial usage.]
func Foo(arg string) (string, error) { ... }
` + "\x60\x60\x60" + `

### README
- Badge row
- Heading + one-line description
- Longer description (what problem it solves)
- Quick start (30 seconds to first success)
- Full installation
- Usage examples
- API reference (or link to it)
- Configuration
- Contributing
- License

### CHANGELOG
- Keep a Changelog format
- [Unreleased] section for next
- Group by: Added, Changed, Deprecated, Removed, Fixed, Security
- Link to issues/PRs
- Reverse chronological

## Ferramentas disponiveis
- read_file: ler codigo fonte e docs existentes
- write_file: criar novos docs
- edit_file: atualizar docs existentes
- file_edit: edicoes precisas em docs
- glob: encontrar arquivos doc (*.md, **/doc.go, **/README*)
- grep: buscar referencias e inconsistencias

## Restricoes importantes
Voce SOMENTE edita arquivos de documentacao:
- Arquivos *.md, *.mdx, *.rst, *.txt
- Diretorios docs/**
- Arquivos doc.go
- Comentarios em codigo fonte (mas nao a logica)

NUNCA modifique:
- Logica de funcoes em arquivos .go, .py, .js, etc.
- Configuracao (yaml, toml, json) exceto metadata de docs
- Testes
- Scripts de build

Se encontrar um bug ou melhoria no codigo, DOCUMENTE em um arquivo TODO.md ou sugira ao usuario, mas NAO conserte.

## Formato das respostas
Quando criar/editar um doc, sempre mostre:
1. O que foi feito (lista de arquivos tocados)
2. Por que (justificativa)
3. Preview das mudancas principais
4. Sugestoes de proximos passos (se houver)`

// DocsAllowedExtensions lists file extensions the docs-writer may modify.
var DocsAllowedExtensions = []string{
	".md", ".mdx", ".rst", ".txt", ".adoc",
}

// DocsAllowedDirs lists directory prefixes the docs-writer may modify
// (in addition to doc extensions).
var DocsAllowedDirs = []string{
	"docs/", "doc/", "documentation/", "guides/", "tutorials/",
}

// DocsAllowedFilenames lists exact filenames the docs-writer may always modify.
var DocsAllowedFilenames = []string{
	"README.md", "CHANGELOG.md", "LICENSE", "CONTRIBUTING.md",
	"CODE_OF_CONDUCT.md", "SECURITY.md", "AUTHORS.md", "NOTICE",
	"doc.go",
}

// DocPathAllowed returns true if the given path is one the docs-writer
// may modify. The check matches by exact filename, file extension, or
// directory prefix.
func DocPathAllowed(p string) bool {
	p = filepath.ToSlash(p)

	// Exact filename match
	base := filepath.Base(p)
	for _, name := range DocsAllowedFilenames {
		if base == name {
			return true
		}
	}

	// Extension match
	ext := strings.ToLower(filepath.Ext(p))
	for _, e := range DocsAllowedExtensions {
		if ext == e {
			return true
		}
	}

	// Directory prefix match
	for _, dir := range DocsAllowedDirs {
		if strings.HasPrefix(p, dir) || strings.Contains(p, "/"+dir) {
			return true
		}
	}

	return false
}

// DocsWriterAgent returns the docs writer agent definition.
func DocsWriterAgent() *Agent {
	return &Agent{
		Name:        "docs-writer",
		Description: "Especialista em documentacao. Escreve docs claros e completos (docs/*.md, README, godoc).",
		Aliases:     []string{"docs", "writer", "documentation"},
		IsBuiltIn:   true,
		MaxTokens:   8192,
		Persona:     strings.TrimSpace(DocsWriterSystemPrompt),
		Skills: []string{
			"read_file",
			"write_file",
			"edit_file",
			"list_directory",
			"glob",
			"grep",
			"search_files",
		},
	}
}

// DocsWriterPromptForREADME returns a prompt to write/update a README.
func DocsWriterPromptForREADME() string {
	return "Analise o projeto e crie/atualize o README.md seguindo as boas praticas. " +
		"Inclua: titulo, descricao, instalacao, uso basico, api, contribuicao, licenca."
}

// DocsWriterPromptForGodoc returns a prompt to add godoc comments.
func DocsWriterPromptForGodoc(pkgPath string) string {
	return "Adicione comentarios godoc de qualidade para o pacote " + pkgPath +
		". Comece pelo doc.go (ou crie um), depois adicione comentarios para todos os tipos e funcoes exportadas " +
		"que nao tem, seguindo o formato Go standard."
}

// DocsWriterPromptForChangelog returns a prompt to update the CHANGELOG.
func DocsWriterPromptForChangelog(version string) string {
	return "Atualize o CHANGELOG.md para a versao " + version + ". Use git log para identificar " +
		"as mudancas relevantes e organize-as em Added, Changed, Deprecated, Removed, Fixed, Security."
}
