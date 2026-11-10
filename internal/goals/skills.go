package goals

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/sashabaranov/go-openai/jsonschema"

	"github.com/costa/polypod/internal/evolution"
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
	register("goal_verification_plan", "Registrar antes da primeira tentativa um teste objetivo imutavel da meta (arquivo, HTTP ou SQLite)", map[string]string{
		"id": "ID da meta", "kind": "file_sha256, http_status, http_json ou sqlite_scalar",
		"path":            "Caminho absoluto do arquivo/SQLite ou URL HTTP(S)",
		"selector":        "Ponteiro JSON ou SELECT escalar, conforme o tipo",
		"expected_sha256": "SHA-256 esperado para file_sha256",
		"expected_json":   "Valor JSON esperado para http_json ou sqlite_scalar; somente o hash sera salvo",
		"expected_status": "Status HTTP esperado para http_status/http_json",
		"pattern_key":     "Categoria estavel opcional para agregar resultados",
	}, []string{"id", "kind", "path"}, func(args map[string]string) (string, error) {
		status := 0
		if raw := args["expected_status"]; raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil {
				return "", fmt.Errorf("expected_status invalido: %w", err)
			}
			status = value
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		g, err := store.RegisterVerification(ctx, args["id"], evolution.VerificationPlanInput{
			PatternKey: args["pattern_key"], Kind: args["kind"], Path: args["path"],
			Selector: args["selector"], ExpectedSHA256: args["expected_sha256"],
			ExpectedJSON: args["expected_json"], ExpectedStatus: status,
		})
		if err != nil {
			return "", err
		}
		return encode(g)
	})
	register("goal_list", "Listar metas persistidas e historico de transicoes", map[string]string{"status": "Filtro opcional de estado", "parent_id": "Filtro opcional de meta superior"}, nil, func(args map[string]string) (string, error) {
		if err := store.Reload(); err != nil {
			return "", err
		}
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
	register("goal_update", "Atualizar estado ou plano; concluir meta com plano objetivo exige ultima tentativa verificada e revisada", map[string]string{
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
	register("goal_attempt_start", "Registrar uma tentativa antes de executar a proxima acao de uma meta pronta", map[string]string{
		"id": "ID da meta", "action": "Acao concreta; vazio usa next_action da meta",
	}, []string{"id"}, func(args map[string]string) (string, error) {
		g, err := store.StartAttempt(args["id"], args["action"])
		if err != nil {
			return "", err
		}
		return encode(g)
	})
	register("goal_attempt_finish", "Registrar resultado; para reconciliar in_flight antigo, confira a execucao e o efeito antes de usar interrupted", map[string]string{
		"id": "ID da meta", "attempt_id": "ID retornado por goal_attempt_start",
		"status": "reported_success, failed ou interrupted", "result": "O que aconteceu de fato",
		"evidence": "Referencia verificavel obrigatoria para reported_success", "next_action": "Nova proxima acao, se necessaria",
	}, []string{"id", "attempt_id", "status", "result"}, func(args map[string]string) (string, error) {
		var nextAction *string
		if value, ok := args["next_action"]; ok {
			nextAction = &value
		}
		g, err := store.FinishAttempt(args["id"], args["attempt_id"], AttemptStatus(args["status"]),
			args["result"], args["evidence"], nextAction)
		if err != nil {
			return "", err
		}
		return encode(g)
	})
	register("goal_attempt_verify", "Observar o alvo configurado e persistir um resultado independente da narrativa do agente", map[string]string{
		"id": "ID da meta", "attempt_id": "ID da ultima tentativa encerrada",
	}, []string{"id", "attempt_id"}, func(args map[string]string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		g, err := store.VerifyAttempt(ctx, args["id"], args["attempt_id"])
		if err != nil {
			return "", err
		}
		return encode(g)
	})
	register("goal_attempt_review", "Revisar a ultima tentativa; accepted exige verificacao objetiva positiva quando houver plano", map[string]string{
		"id": "ID da meta", "attempt_id": "ID da ultima tentativa",
		"decision": "accepted se a evidencia confirma o efeito; rejected caso contrario",
		"note":     "Evidencia observada ou motivo concreto da rejeicao", "next_action": "Nova proxima acao se a revisao rejeitar o efeito",
	}, []string{"id", "attempt_id", "decision", "note"}, func(args map[string]string) (string, error) {
		var nextAction *string
		if value, ok := args["next_action"]; ok {
			nextAction = &value
		}
		g, err := store.ReviewAttempt(args["id"], args["attempt_id"], args["decision"], args["note"], nextAction)
		if err != nil {
			return "", err
		}
		return encode(g)
	})
	register("goal_review", "Revisar metas prontas, tentativas em andamento e efeitos pendentes de verificacao; nao executa trabalho", nil, nil,
		func(map[string]string) (string, error) {
			if err := store.Reload(); err != nil {
				return "", err
			}
			return encode(store.Review())
		})
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
