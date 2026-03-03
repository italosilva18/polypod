package memory

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// NewStoreFromDB creates the appropriate Store implementation.
// Uses SQLite if db is provided, otherwise falls back to file-based storage.
func NewStoreFromDB(sqlDB *sql.DB, dataDir string) Store {
	if sqlDB != nil {
		return NewSQLiteStore(sqlDB)
	}
	return NewFileStore(dataDir)
}

// RegisterSkills registers memory-related skills in the skill registry.
func RegisterSkills(reg *skill.Registry, store Store) {
	reg.Register(&skill.Skill{
		Name:        "save_memory",
		Description: "Salvar uma memoria persistente. Use para lembrar fatos, preferencias e informacoes importantes entre sessoes.",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"topic":   {Type: jsonschema.String, Description: "Topico/chave da memoria (ex: linguagem_favorita, nome_usuario)"},
				"content": {Type: jsonschema.String, Description: "Conteudo da memoria a salvar"},
			},
			Required: []string{"topic", "content"},
		},
		Execute: func(args map[string]string) (string, error) {
			topic := args["topic"]
			content := args["content"]
			if topic == "" || content == "" {
				return "", fmt.Errorf("topic e content sao obrigatorios")
			}
			if err := store.Save(context.Background(), topic, content); err != nil {
				return "", err
			}
			return fmt.Sprintf("Memoria salva com sucesso: [%s]", topic), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "recall_memory",
		Description: "Buscar memorias persistentes por palavra-chave. Pesquisa no topico e conteudo.",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"query": {Type: jsonschema.String, Description: "Texto para buscar nas memorias"},
			},
			Required: []string{"query"},
		},
		Execute: func(args map[string]string) (string, error) {
			query := args["query"]
			if query == "" {
				return "", fmt.Errorf("query e obrigatoria")
			}
			memories, err := store.Search(context.Background(), query)
			if err != nil {
				return "", err
			}
			if len(memories) == 0 {
				return "Nenhuma memoria encontrada para: " + query, nil
			}
			var sb strings.Builder
			for _, m := range memories {
				sb.WriteString(fmt.Sprintf("- [%s]: %s\n", m.Topic, m.Content))
			}
			return sb.String(), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "list_memories",
		Description: "Listar todas as memorias persistentes salvas.",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{},
		},
		Execute: func(args map[string]string) (string, error) {
			memories, err := store.List(context.Background())
			if err != nil {
				return "", err
			}
			if len(memories) == 0 {
				return "Nenhuma memoria salva.", nil
			}
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("Total: %d memorias\n\n", len(memories)))
			for _, m := range memories {
				sb.WriteString(fmt.Sprintf("- [%s]: %s\n", m.Topic, m.Content))
			}
			return sb.String(), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "delete_memory",
		Description: "Deletar uma memoria persistente por topico.",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"topic": {Type: jsonschema.String, Description: "Topico exato da memoria a deletar"},
			},
			Required: []string{"topic"},
		},
		Execute: func(args map[string]string) (string, error) {
			topic := args["topic"]
			if topic == "" {
				return "", fmt.Errorf("topic e obrigatorio")
			}
			if err := store.Delete(context.Background(), topic); err != nil {
				return "", err
			}
			return fmt.Sprintf("Memoria deletada: [%s]", topic), nil
		},
	})
}
