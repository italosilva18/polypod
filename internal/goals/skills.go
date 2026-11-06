package goals

import (
	"encoding/json"
	"fmt"

	"github.com/sashabaranov/go-openai/jsonschema"

	"github.com/costa/polypod/internal/skill"
)

// RegisterSkills exposes planning state only. Execution still uses the agent's permission path.
func RegisterSkills(reg *skill.Registry, store *Store) {
	register := func(name, description string, fields map[string]string, required []string, run func(map[string]string) (string, error)) {
		props := map[string]jsonschema.Definition{}
		for key, description := range fields {
			props[key] = jsonschema.Definition{Type: jsonschema.String, Description: description}
		}
		reg.Register(&skill.Skill{Name: name, Description: description, Parameters: jsonschema.Definition{Type: jsonschema.Object, Properties: props, Required: required}, Execute: run})
	}
	register("goal_create", "Criar meta ou subtarefa duravel somente para trabalho autorizado pelo usuario", map[string]string{
		"title": "Objetivo concreto", "success_criteria": "Criterio verificavel de sucesso", "parent_id": "ID opcional da meta superior", "dependencies": "Array JSON de IDs de dependencias, como string", "next_action": "Proxima acao concreta",
	}, []string{"title", "success_criteria", "next_action"}, func(args map[string]string) (string, error) {
		deps, err := parseDependencies(args["dependencies"])
		if err != nil {
			return "", err
		}
		g, err := store.Create(CreateInput{Title: args["title"], SuccessCriteria: args["success_criteria"], ParentID: args["parent_id"], Dependencies: deps, NextAction: args["next_action"]})
		if err != nil {
			return "", err
		}
		return encode(g)
	})
	register("goal_list", "Listar metas persistidas e historico de transicoes", map[string]string{"status": "Filtro opcional de estado", "parent_id": "Filtro opcional de meta superior"}, nil, func(args map[string]string) (string, error) {
		selected := []Goal{}
		for _, g := range store.List() {
			if args["status"] != "" && string(g.Status) != args["status"] {
				continue
			}
			if args["parent_id"] != "" && g.ParentID != args["parent_id"] {
				continue
			}
			selected = append(selected, g)
		}
		return encode(selected)
	})
	register("goal_update", "Atualizar estado ou plano; concluir exige evidencia e dependencias/subtarefas resolvidas", map[string]string{
		"id": "ID da meta", "status": "pending, active, blocked, paused, completed ou cancelled", "next_action": "Nova proxima acao (vazio para limpar)", "evidence": "Evidencia verificavel da conclusao", "dependencies": "Array JSON de IDs, como string", "note": "Motivo ou resultado da atualizacao para auditoria",
	}, []string{"id", "note"}, func(args map[string]string) (string, error) {
		in := UpdateInput{Note: args["note"]}
		if v, ok := args["status"]; ok {
			status := Status(v)
			in.Status = &status
		}
		if v, ok := args["next_action"]; ok {
			in.NextAction = &v
		}
		if v, ok := args["evidence"]; ok {
			in.Evidence = &v
		}
		if v, ok := args["dependencies"]; ok {
			deps, err := parseDependencies(v)
			if err != nil {
				return "", err
			}
			in.Dependencies = &deps
		}
		g, err := store.Update(args["id"], in)
		if err != nil {
			return "", err
		}
		return encode(g)
	})
	register("goal_review", "Revisar metas, proximas acoes prontas e bloqueios; nao executa nem agenda trabalho", nil, nil, func(map[string]string) (string, error) { return encode(store.Review()) })
}
func parseDependencies(raw string) ([]string, error) {
	out := []string{}
	if raw == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("dependencies deve ser uma string contendo array JSON: %w", err)
	}
	return out, nil
}
func encode(value any) (string, error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	return string(raw), err
}
