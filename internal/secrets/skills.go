package secrets

import (
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// RegisterSkills exposes unrestricted secret management to the agent. Secret
// values intentionally appear in tool input and output so the agent can use
// them for logins, commands, APIs, and other authorized tasks.
func RegisterSkills(reg *skill.Registry, vault *Vault) {
	reg.Register(&skill.Skill{
		Name:        "save_secret",
		Description: "Salvar ou atualizar uma senha, token ou credencial no cofre persistente. O valor e recebido em texto e pode ser recuperado integralmente pelo agente.",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"name":     {Type: jsonschema.String, Description: "Nome unico para identificar o secret"},
				"value":    {Type: jsonschema.String, Description: "Senha, token ou valor integral, sem mascaramento"},
				"username": {Type: jsonschema.String, Description: "Usuario ou email associado, se houver"},
				"url":      {Type: jsonschema.String, Description: "Site ou servico associado, se houver"},
				"notes":    {Type: jsonschema.String, Description: "Informacoes adicionais"},
			},
			Required: []string{"name", "value"},
		},
		Execute: func(args map[string]string) (string, error) {
			record := Record{
				Name:     args["name"],
				Value:    args["value"],
				Username: args["username"],
				URL:      args["url"],
				Notes:    args["notes"],
			}
			if err := vault.Save(record); err != nil {
				return "", err
			}
			return fmt.Sprintf("Secret %q salvo. Valor disponivel integralmente para uso do agente.", record.Name), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "get_secret",
		Description: "Recuperar uma senha, token ou credencial do cofre em texto integral para usar em login, comando, API ou automacao.",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"name": {Type: jsonschema.String, Description: "Nome do secret"},
			},
			Required: []string{"name"},
		},
		Execute: func(args map[string]string) (string, error) {
			record, err := vault.Get(args["name"])
			if err != nil {
				return "", err
			}
			return formatRecord(record), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "list_secrets",
		Description: "Listar todos os secrets do cofre com seus valores integrais, usuarios, URLs e observacoes, sem mascaramento.",
		Parameters:  jsonschema.Definition{Type: jsonschema.Object, Properties: map[string]jsonschema.Definition{}},
		Execute: func(map[string]string) (string, error) {
			records, err := vault.List()
			if err != nil {
				return "", err
			}
			if len(records) == 0 {
				return "Nenhum secret salvo.", nil
			}
			var out strings.Builder
			for i, record := range records {
				if i > 0 {
					out.WriteString("\n---\n")
				}
				out.WriteString(formatRecord(record))
			}
			return out.String(), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "delete_secret",
		Description: "Excluir permanentemente uma senha, token ou credencial do cofre pelo nome.",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"name": {Type: jsonschema.String, Description: "Nome do secret a excluir"},
			},
			Required: []string{"name"},
		},
		Execute: func(args map[string]string) (string, error) {
			if _, err := vault.Get(args["name"]); err != nil {
				return "", err
			}
			if err := vault.Delete(args["name"]); err != nil {
				return "", err
			}
			return fmt.Sprintf("Secret %q excluido.", strings.TrimSpace(args["name"])), nil
		},
	})
}

func formatRecord(record Record) string {
	return fmt.Sprintf(
		"name: %s\nusername: %s\nvalue: %s\nurl: %s\nnotes: %s\nupdated_at: %s",
		record.Name,
		record.Username,
		record.Value,
		record.URL,
		record.Notes,
		record.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	)
}
