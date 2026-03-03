package skill

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai/jsonschema"
)

// RegisterDynamicManagement registers meta-skills for creating, listing, and deleting custom skills.
func RegisterDynamicManagement(reg *Registry, skillsDir string) {
	reg.Register(&Skill{
		Name:        "create_skill",
		Description: "Criar uma nova skill customizada baseada em script bash. A skill fica disponivel imediatamente e persiste entre reinicializacoes.",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"name":        {Type: jsonschema.String, Description: "Nome da skill (apenas letras, numeros e underscore)"},
				"description": {Type: jsonschema.String, Description: "Descricao do que a skill faz"},
				"parameters":  {Type: jsonschema.String, Description: "JSON com parametros: {\"nome\": \"descricao\", ...}"},
				"script":      {Type: jsonschema.String, Description: "Script bash a executar. Use {{.nome_param}} para substituir parametros."},
			},
			Required: []string{"name", "description", "script"},
		},
		Execute: func(args map[string]string) (string, error) {
			name := args["name"]
			if name == "" {
				return "", fmt.Errorf("name e obrigatorio")
			}
			if !validName.MatchString(name) {
				return "", fmt.Errorf("nome invalido: use apenas letras, numeros e underscore, comecando com letra")
			}
			if len(args["script"]) > maxScriptBytes {
				return "", fmt.Errorf("script excede limite de %d bytes", maxScriptBytes)
			}

			// Parse parameters if provided
			params := make(map[string]string)
			if args["parameters"] != "" {
				if err := json.Unmarshal([]byte(args["parameters"]), &params); err != nil {
					return "", fmt.Errorf("parsing parameters JSON: %w", err)
				}
			}

			def := &CustomSkillDef{
				Name:        name,
				Description: args["description"],
				Parameters:  params,
				Script:      args["script"],
				CreatedAt:   time.Now(),
			}

			// Save to disk
			if err := SaveCustomSkill(skillsDir, def); err != nil {
				return "", err
			}

			// Register in runtime
			RegisterCustomSkill(reg, def)

			return fmt.Sprintf("Skill '%s' criada com sucesso. Disponivel imediatamente.", name), nil
		},
	})

	reg.Register(&Skill{
		Name:        "list_custom_skills",
		Description: "Listar todas as skills customizadas criadas.",
		Parameters: jsonschema.Definition{
			Type:       jsonschema.Object,
			Properties: map[string]jsonschema.Definition{},
		},
		Execute: func(args map[string]string) (string, error) {
			defs, err := ListCustomSkills(skillsDir)
			if err != nil {
				return "", err
			}
			if len(defs) == 0 {
				return "Nenhuma skill customizada criada.", nil
			}
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("Total: %d skills customizadas\n\n", len(defs)))
			for _, d := range defs {
				sb.WriteString(fmt.Sprintf("- %s: %s\n", d.Name, d.Description))
				if len(d.Parameters) > 0 {
					for p, desc := range d.Parameters {
						sb.WriteString(fmt.Sprintf("  param: %s (%s)\n", p, desc))
					}
				}
			}
			return sb.String(), nil
		},
	})

	reg.Register(&Skill{
		Name:        "delete_custom_skill",
		Description: "Deletar uma skill customizada por nome.",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"name": {Type: jsonschema.String, Description: "Nome da skill a deletar"},
			},
			Required: []string{"name"},
		},
		Execute: func(args map[string]string) (string, error) {
			name := args["name"]
			if name == "" {
				return "", fmt.Errorf("name e obrigatorio")
			}

			if err := DeleteCustomSkill(skillsDir, name); err != nil {
				return "", err
			}

			// Unregister from runtime
			reg.Unregister(name)

			return fmt.Sprintf("Skill '%s' deletada com sucesso.", name), nil
		},
	})
}
