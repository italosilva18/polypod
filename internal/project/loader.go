package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	projectFileName = ".polypod.md"
	maxSearchDepth  = 5
)

// ProjectConfig represents a loaded project instruction file.
type ProjectConfig struct {
	Content  string
	FilePath string
}

// LoadProjectFile searches for .polypod.md in current and parent directories.
func LoadProjectFile() (*ProjectConfig, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	for i := 0; i < maxSearchDepth; i++ {
		path := filepath.Join(dir, projectFileName)
		if data, err := os.ReadFile(path); err == nil {
			return &ProjectConfig{Content: string(data), FilePath: path}, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil, nil
}

// LoadUserFile loads the global user config from ~/.polypod/config.md.
func LoadUserFile() (*ProjectConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, ".polypod", "config.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &ProjectConfig{Content: string(data), FilePath: path}, nil
}

// InjectProjectContext builds a combined context from project + user files.
func InjectProjectContext() string {
	var parts []string

	if proj, err := LoadProjectFile(); err == nil && proj != nil {
		parts = append(parts, fmt.Sprintf("[Instrucoes do projeto (%s)]\n%s", proj.FilePath, proj.Content))
	}
	if user, err := LoadUserFile(); err == nil && user != nil {
		parts = append(parts, fmt.Sprintf("[Instrucoes do usuario (%s)]\n%s", user.FilePath, user.Content))
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n")
}

// CreateDefaultProjectFile creates a template .polypod.md in the given path.
func CreateDefaultProjectFile(dir string) error {
	path := filepath.Join(dir, projectFileName)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("arquivo %s ja existe", path)
	}

	template := `# Polypod - Instrucoes do Projeto

## Sobre este projeto
Descreva seu projeto aqui. O Polypod usara essas instrucoes como contexto.

## Regras
- Responda em portugues
- Seja conciso e direto
- Use as ferramentas disponiveis para investigar antes de responder

## Contexto
Informacoes importantes que o assistente deve saber sobre este projeto.

## Stack
- Linguagem:
- Framework:
- Banco de dados:
`
	return os.WriteFile(path, []byte(template), 0644)
}
