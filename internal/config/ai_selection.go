package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SaveAISelection updates the selected backend and clears the previous API key.
// Other YAML values are preserved.
func SaveAISelection(path, provider, model, baseURL string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("ler configuração: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("ler YAML: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("configuração YAML inválida")
	}
	root := doc.Content[0]
	ai := mappingValue(root, "ai")
	if ai == nil {
		ai = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "ai"}, ai)
	}
	if ai.Kind != yaml.MappingNode {
		return fmt.Errorf("seção ai inválida")
	}
	setMappingValue(ai, "provider", provider)
	setMappingValue(ai, "model", model)
	setMappingValue(ai, "base_url", baseURL)
	setMappingValue(ai, "api_key", "")
	var encoded []byte
	encoded, err = yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("gerar YAML: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("verificar configuração: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".polypod-ai-*")
	if err != nil {
		return fmt.Errorf("criar configuração temporária: %w", err)
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(info.Mode().Perm()); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(encoded); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("salvar configuração: %w", err)
	}
	return nil
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func setMappingValue(node *yaml.Node, key, value string) {
	if existing := mappingValue(node, key); existing != nil {
		existing.Kind, existing.Tag, existing.Value = yaml.ScalarNode, "!!str", value
		return
	}
	node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}
