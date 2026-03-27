package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// RegisterFileSkills registers file editing skills (edit, create, delete, patch).
func RegisterFileSkills(reg *skill.Registry) {
	reg.Register(&skill.Skill{
		Name:        "edit_file",
		Description: "Editar um arquivo substituindo texto exato (cirurgico, sem reescrever o arquivo inteiro)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path":        {Type: jsonschema.String, Description: "Caminho do arquivo"},
				"old_text":    {Type: jsonschema.String, Description: "Texto exato a encontrar"},
				"new_text":    {Type: jsonschema.String, Description: "Texto de substituicao"},
				"replace_all": {Type: jsonschema.String, Description: "'true' para substituir todas as ocorrencias"},
			},
			Required: []string{"path", "old_text", "new_text"},
		},
		Execute: func(args map[string]string) (string, error) {
			path := args["path"]
			oldText := args["old_text"]
			newText := args["new_text"]

			data, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("lendo arquivo: %w", err)
			}
			content := string(data)

			count := strings.Count(content, oldText)
			if count == 0 {
				return "", fmt.Errorf("texto nao encontrado no arquivo %s", path)
			}
			if count > 1 && args["replace_all"] != "true" {
				return "", fmt.Errorf("texto encontrado %d vezes em %s. Use replace_all='true' ou forneça um trecho mais especifico", count, path)
			}

			if args["replace_all"] == "true" {
				content = strings.ReplaceAll(content, oldText, newText)
			} else {
				content = strings.Replace(content, oldText, newText, 1)
			}

			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				return "", fmt.Errorf("escrevendo arquivo: %w", err)
			}

			return fmt.Sprintf("Arquivo %s editado com sucesso (%d substituicao(es)).", path, count), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "create_file",
		Description: "Criar um novo arquivo com conteudo",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path":    {Type: jsonschema.String, Description: "Caminho do arquivo a criar"},
				"content": {Type: jsonschema.String, Description: "Conteudo do arquivo"},
			},
			Required: []string{"path", "content"},
		},
		Execute: func(args map[string]string) (string, error) {
			path := args["path"]
			dir := filepath.Dir(path)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return "", fmt.Errorf("criando diretorio: %w", err)
			}
			if err := os.WriteFile(path, []byte(args["content"]), 0644); err != nil {
				return "", fmt.Errorf("criando arquivo: %w", err)
			}
			return fmt.Sprintf("Arquivo %s criado com sucesso (%d bytes).", path, len(args["content"])), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "delete_file",
		Description: "Excluir um arquivo",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path": {Type: jsonschema.String, Description: "Caminho do arquivo a excluir"},
			},
			Required: []string{"path"},
		},
		Execute: func(args map[string]string) (string, error) {
			path := args["path"]
			info, err := os.Stat(path)
			if err != nil {
				return "", fmt.Errorf("arquivo nao encontrado: %s", path)
			}
			if info.IsDir() {
				return "", fmt.Errorf("%s e um diretorio, use run_command para remover", path)
			}
			if err := os.Remove(path); err != nil {
				return "", fmt.Errorf("excluindo arquivo: %w", err)
			}
			return fmt.Sprintf("Arquivo %s excluido.", path), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "patch_file",
		Description: "Aplicar um patch unified diff em um arquivo",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path":  {Type: jsonschema.String, Description: "Caminho do arquivo"},
				"patch": {Type: jsonschema.String, Description: "Conteudo do patch (unified diff)"},
			},
			Required: []string{"path", "patch"},
		},
		Execute: func(args map[string]string) (string, error) {
			tmpFile, err := os.CreateTemp("", "polypod-patch-*.diff")
			if err != nil {
				return "", err
			}
			defer os.Remove(tmpFile.Name())

			if _, err := tmpFile.WriteString(args["patch"]); err != nil {
				tmpFile.Close()
				return "", err
			}
			tmpFile.Close()

			return run("patch", args["path"], tmpFile.Name())
		},
	})
}
