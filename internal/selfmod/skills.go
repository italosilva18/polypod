package selfmod

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
	"gopkg.in/yaml.v3"
)

// coreSkills that cannot be removed via self-modification.
var coreSkills = map[string]bool{
	"read_file":   true,
	"run_command": true,
}

// RegisterSkills registers self-modification skills in the skill registry.
func RegisterSkills(reg *skill.Registry, agentReg *agent.Registry, agentsDir string) {
	reg.Register(&skill.Skill{
		Name:        "read_agent_config",
		Description: "Ler a configuracao YAML do agente atual ou de outro agente.",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"agent_name": {Type: jsonschema.String, Description: "Nome do agente (default: 'default')"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			name := args["agent_name"]
			if name == "" {
				name = "default"
			}
			path := filepath.Join(agentsDir, name+".yaml")
			data, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("lendo config do agente '%s': %w", name, err)
			}
			return string(data), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "update_persona",
		Description: "Atualizar a persona/personalidade do agente. A mudanca persiste no YAML e entra em vigor na proxima mensagem.",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"new_persona": {Type: jsonschema.String, Description: "Nova persona completa para o agente"},
				"agent_name":  {Type: jsonschema.String, Description: "Nome do agente (default: 'default')"},
			},
			Required: []string{"new_persona"},
		},
		Execute: func(args map[string]string) (string, error) {
			name := args["agent_name"]
			if name == "" {
				name = "default"
			}
			newPersona := args["new_persona"]
			if newPersona == "" {
				return "", fmt.Errorf("new_persona e obrigatoria")
			}

			ag, err := loadAgentYAML(agentsDir, name)
			if err != nil {
				return "", err
			}

			if err := backupAgent(agentsDir, name); err != nil {
				return "", err
			}

			ag.Persona = newPersona

			if err := saveAgentYAML(agentsDir, name, ag); err != nil {
				return "", err
			}

			agentReg.Register(ag)
			return fmt.Sprintf("Persona do agente '%s' atualizada com sucesso.", name), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "add_agent_skill",
		Description: "Adicionar uma skill a lista de skills do agente. Persiste no YAML.",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"skill_name": {Type: jsonschema.String, Description: "Nome da skill a adicionar"},
				"agent_name": {Type: jsonschema.String, Description: "Nome do agente (default: 'default')"},
			},
			Required: []string{"skill_name"},
		},
		Execute: func(args map[string]string) (string, error) {
			name := args["agent_name"]
			if name == "" {
				name = "default"
			}
			skillName := args["skill_name"]
			if skillName == "" {
				return "", fmt.Errorf("skill_name e obrigatorio")
			}

			ag, err := loadAgentYAML(agentsDir, name)
			if err != nil {
				return "", err
			}

			// Check if already present
			for _, s := range ag.Skills {
				if s == skillName {
					return fmt.Sprintf("Skill '%s' ja esta no agente '%s'.", skillName, name), nil
				}
			}

			if err := backupAgent(agentsDir, name); err != nil {
				return "", err
			}

			ag.Skills = append(ag.Skills, skillName)

			if err := saveAgentYAML(agentsDir, name, ag); err != nil {
				return "", err
			}

			agentReg.Register(ag)
			return fmt.Sprintf("Skill '%s' adicionada ao agente '%s'.", skillName, name), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "remove_agent_skill",
		Description: "Remover uma skill da lista do agente. Skills core (read_file, run_command) nao podem ser removidas.",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"skill_name": {Type: jsonschema.String, Description: "Nome da skill a remover"},
				"agent_name": {Type: jsonschema.String, Description: "Nome do agente (default: 'default')"},
			},
			Required: []string{"skill_name"},
		},
		Execute: func(args map[string]string) (string, error) {
			name := args["agent_name"]
			if name == "" {
				name = "default"
			}
			skillName := args["skill_name"]
			if skillName == "" {
				return "", fmt.Errorf("skill_name e obrigatorio")
			}

			if coreSkills[skillName] {
				return "", fmt.Errorf("skill '%s' e core e nao pode ser removida", skillName)
			}

			ag, err := loadAgentYAML(agentsDir, name)
			if err != nil {
				return "", err
			}

			found := false
			var newSkills []string
			for _, s := range ag.Skills {
				if s == skillName {
					found = true
					continue
				}
				newSkills = append(newSkills, s)
			}

			if !found {
				return fmt.Sprintf("Skill '%s' nao encontrada no agente '%s'.", skillName, name), nil
			}

			if err := backupAgent(agentsDir, name); err != nil {
				return "", err
			}

			ag.Skills = newSkills

			if err := saveAgentYAML(agentsDir, name, ag); err != nil {
				return "", err
			}

			agentReg.Register(ag)
			return fmt.Sprintf("Skill '%s' removida do agente '%s'.", skillName, name), nil
		},
	})
}

func loadAgentYAML(agentsDir, name string) (*agent.Agent, error) {
	path := filepath.Join(agentsDir, name+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("lendo agente '%s': %w", name, err)
	}
	var ag agent.Agent
	if err := yaml.Unmarshal(data, &ag); err != nil {
		return nil, fmt.Errorf("parsing agente '%s': %w", name, err)
	}
	if ag.Name == "" {
		ag.Name = name
	}
	return &ag, nil
}

func backupAgent(agentsDir, name string) error {
	src := filepath.Join(agentsDir, name+".yaml")
	dst := filepath.Join(agentsDir, name+".yaml.bak")
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("lendo agente para backup: %w", err)
	}
	return os.WriteFile(dst, data, 0644)
}

func saveAgentYAML(agentsDir, name string, ag *agent.Agent) error {
	data, err := yaml.Marshal(ag)
	if err != nil {
		return fmt.Errorf("serializando agente: %w", err)
	}

	// Clean up the YAML: gopkg.in/yaml.v3 outputs persona with proper block scalar
	path := filepath.Join(agentsDir, name+".yaml")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("salvando agente '%s': %w", name, err)
	}
	return nil
}

