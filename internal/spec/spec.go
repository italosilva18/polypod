package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Spec holds a feature specification with requirements, design, and tasks.
type Spec struct {
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	CreatedAt    time.Time `json:"created_at"`
	Requirements string    `json:"requirements"`
	Design       string    `json:"design"`
	Tasks        []Task    `json:"tasks"`
	Status       string    `json:"status"` // "draft", "planning", "in_progress", "done"
}

// Task is a single implementation step.
type Task struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"` // "pending", "in_progress", "done"
	Files       []string `json:"files"` // files to create/modify
}

// GenerateRequirementsPrompt creates an AI prompt to generate requirements from a description.
func GenerateRequirementsPrompt(description string) string {
	return fmt.Sprintf(`Voce e um analista de requisitos. A partir da descricao abaixo, gere requisitos detalhados.

## Descricao da feature
%s

## Gere os seguintes itens:

### Requisitos Funcionais
- Liste cada requisito como "RF01: descricao"
- Inclua criterios de aceitacao para cada um

### Requisitos Nao-Funcionais
- Performance, seguranca, usabilidade

### Casos de Uso
- Fluxo principal
- Fluxos alternativos
- Tratamento de erros

### Restricoes
- Limitacoes tecnicas, dependencias

Formate em Markdown.`, description)
}

// GenerateDesignPrompt creates an AI prompt to generate a design doc from requirements.
func GenerateDesignPrompt(requirements string) string {
	return fmt.Sprintf(`Voce e um arquiteto de software. A partir dos requisitos abaixo, crie um design doc.

## Requisitos
%s

## Gere o design com:

### Arquitetura
- Componentes e suas responsabilidades
- Fluxo de dados
- Diagrama de sequencia (em texto)

### Estrutura de Arquivos
- Quais arquivos criar/modificar
- Onde cada componente vive

### Interfaces/APIs
- Funcoes publicas, seus parametros e retornos
- Tipos de dados

### Decisoes de Design
- Por que cada decisao foi tomada
- Alternativas consideradas

Formate em Markdown.`, requirements)
}

// GenerateTasksPrompt creates an AI prompt to break design into tasks.
func GenerateTasksPrompt(design string) string {
	return fmt.Sprintf(`Voce e um tech lead. Quebre o design abaixo em tarefas de implementacao.

## Design
%s

## Gere uma lista de tarefas:

Cada tarefa deve ter:
1. Titulo curto e acao clara (ex: "Criar struct UserService em internal/user/service.go")
2. Descricao do que fazer
3. Arquivos envolvidos
4. Dependencias (quais tarefas devem ser feitas antes)
5. Estimativa: P (pequena), M (media), G (grande)

Ordene pela sequencia ideal de implementacao.
Formate como lista numerada em Markdown.`, design)
}

// SaveSpec saves a spec to disk.
func SaveSpec(dir string, spec *Spec) error {
	specDir := filepath.Join(dir, ".polypod", "specs", sanitize(spec.Name))
	os.MkdirAll(specDir, 0755)

	// requirements.md
	if spec.Requirements != "" {
		os.WriteFile(filepath.Join(specDir, "requirements.md"), []byte(spec.Requirements), 0644)
	}

	// design.md
	if spec.Design != "" {
		os.WriteFile(filepath.Join(specDir, "design.md"), []byte(spec.Design), 0644)
	}

	// tasks.md
	if len(spec.Tasks) > 0 {
		var sb strings.Builder
		sb.WriteString("# Tarefas\n\n")
		for _, t := range spec.Tasks {
			status := "[ ]"
			if t.Status == "done" {
				status = "[x]"
			} else if t.Status == "in_progress" {
				status = "[~]"
			}
			fmt.Fprintf(&sb, "%s **%d. %s**\n", status, t.ID, t.Title)
			if t.Description != "" {
				fmt.Fprintf(&sb, "   %s\n", t.Description)
			}
			if len(t.Files) > 0 {
				fmt.Fprintf(&sb, "   Arquivos: %s\n", strings.Join(t.Files, ", "))
			}
			sb.WriteByte('\n')
		}
		os.WriteFile(filepath.Join(specDir, "tasks.md"), []byte(sb.String()), 0644)
	}

	return nil
}

// LoadSpec loads a spec from disk.
func LoadSpec(dir, name string) (*Spec, error) {
	specDir := filepath.Join(dir, ".polypod", "specs", sanitize(name))
	if _, err := os.Stat(specDir); err != nil {
		return nil, fmt.Errorf("spec '%s' nao encontrada", name)
	}

	spec := &Spec{Name: name, Status: "draft"}

	if data, err := os.ReadFile(filepath.Join(specDir, "requirements.md")); err == nil {
		spec.Requirements = string(data)
		spec.Status = "planning"
	}
	if data, err := os.ReadFile(filepath.Join(specDir, "design.md")); err == nil {
		spec.Design = string(data)
		spec.Status = "planning"
	}

	return spec, nil
}

// ListSpecs returns all specs in the project.
func ListSpecs(dir string) []string {
	specDir := filepath.Join(dir, ".polypod", "specs")
	entries, err := os.ReadDir(specDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

func sanitize(name string) string {
	r := strings.NewReplacer(" ", "-", "/", "-", "\\", "-", ":", "-")
	return strings.ToLower(r.Replace(name))
}
