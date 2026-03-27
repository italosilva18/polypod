package git

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

const execTimeout = 30 * time.Second

// RegisterSkills registers all git-related skills.
func RegisterSkills(reg *skill.Registry) {
	reg.Register(&skill.Skill{
		Name:        "git_status",
		Description: "Mostrar o status do repositorio git (arquivos modificados, staged, untracked)",
		Parameters:  jsonschema.Definition{Type: jsonschema.Object, Properties: map[string]jsonschema.Definition{}},
		Execute: func(args map[string]string) (string, error) {
			return run("git", "status", "--short")
		},
	})

	reg.Register(&skill.Skill{
		Name:        "git_diff",
		Description: "Mostrar diff de alteracoes no repositorio",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path":   {Type: jsonschema.String, Description: "Arquivo especifico (opcional)"},
				"staged": {Type: jsonschema.String, Description: "'true' para ver apenas alteracoes staged"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			cmdArgs := []string{"diff"}
			if args["staged"] == "true" {
				cmdArgs = append(cmdArgs, "--cached")
			}
			if p := args["path"]; p != "" {
				cmdArgs = append(cmdArgs, "--", p)
			}
			return run("git", cmdArgs...)
		},
	})

	reg.Register(&skill.Skill{
		Name:        "git_log",
		Description: "Mostrar historico de commits recentes",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"count":   {Type: jsonschema.String, Description: "Numero de commits (default: 10)"},
				"oneline": {Type: jsonschema.String, Description: "'true' para formato resumido"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			count := args["count"]
			if count == "" {
				count = "10"
			}
			if args["oneline"] == "true" {
				return run("git", "log", "--oneline", "-"+count)
			}
			return run("git", "log", "-"+count, "--format=%h %ad %an: %s", "--date=short")
		},
	})

	reg.Register(&skill.Skill{
		Name:        "git_commit",
		Description: "Criar um commit git",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"message": {Type: jsonschema.String, Description: "Mensagem do commit"},
				"files":   {Type: jsonschema.String, Description: "Arquivos a adicionar (separados por virgula, vazio = todos modificados)"},
			},
			Required: []string{"message"},
		},
		Execute: func(args map[string]string) (string, error) {
			if files := args["files"]; files != "" {
				for _, f := range strings.Split(files, ",") {
					f = strings.TrimSpace(f)
					if _, err := run("git", "add", f); err != nil {
						return "", fmt.Errorf("git add %s: %w", f, err)
					}
				}
			} else {
				if _, err := run("git", "add", "-A"); err != nil {
					return "", err
				}
			}
			return run("git", "commit", "-m", args["message"])
		},
	})

	reg.Register(&skill.Skill{
		Name:        "git_branch",
		Description: "Gerenciar branches git",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"action": {Type: jsonschema.String, Description: "Acao: list, create, delete, switch"},
				"name":   {Type: jsonschema.String, Description: "Nome da branch (para create/delete/switch)"},
			},
			Required: []string{"action"},
		},
		Execute: func(args map[string]string) (string, error) {
			switch args["action"] {
			case "list":
				return run("git", "branch", "-a")
			case "create":
				return run("git", "checkout", "-b", args["name"])
			case "delete":
				return run("git", "branch", "-d", args["name"])
			case "switch":
				return run("git", "checkout", args["name"])
			default:
				return "", fmt.Errorf("acao desconhecida: %s (use: list, create, delete, switch)", args["action"])
			}
		},
	})

	reg.Register(&skill.Skill{
		Name:        "git_stash",
		Description: "Gerenciar git stash",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"action": {Type: jsonschema.String, Description: "Acao: push, pop, list, drop"},
			},
			Required: []string{"action"},
		},
		Execute: func(args map[string]string) (string, error) {
			switch args["action"] {
			case "push":
				return run("git", "stash", "push")
			case "pop":
				return run("git", "stash", "pop")
			case "list":
				return run("git", "stash", "list")
			case "drop":
				return run("git", "stash", "drop")
			default:
				return "", fmt.Errorf("acao desconhecida: %s", args["action"])
			}
		},
	})

	reg.Register(&skill.Skill{
		Name:        "git_blame",
		Description: "Mostrar blame (autoria) de um arquivo",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path":       {Type: jsonschema.String, Description: "Caminho do arquivo"},
				"line_start": {Type: jsonschema.String, Description: "Linha inicial (opcional)"},
				"line_end":   {Type: jsonschema.String, Description: "Linha final (opcional)"},
			},
			Required: []string{"path"},
		},
		Execute: func(args map[string]string) (string, error) {
			cmdArgs := []string{"blame"}
			if s, e := args["line_start"], args["line_end"]; s != "" && e != "" {
				cmdArgs = append(cmdArgs, fmt.Sprintf("-L%s,%s", s, e))
			}
			cmdArgs = append(cmdArgs, args["path"])
			return run("git", cmdArgs...)
		},
	})

	reg.Register(&skill.Skill{
		Name:        "git_show",
		Description: "Mostrar detalhes de um commit",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"ref": {Type: jsonschema.String, Description: "Commit hash ou referencia"},
			},
			Required: []string{"ref"},
		},
		Execute: func(args map[string]string) (string, error) {
			return run("git", "show", "--stat", args["ref"])
		},
	})

	reg.Register(&skill.Skill{
		Name:        "git_pull",
		Description: "Puxar alteracoes do repositorio remoto",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"remote": {Type: jsonschema.String, Description: "Remoto (default: origin)"},
				"branch": {Type: jsonschema.String, Description: "Branch (opcional)"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			remote := args["remote"]
			if remote == "" {
				remote = "origin"
			}
			cmdArgs := []string{"pull", remote}
			if b := args["branch"]; b != "" {
				cmdArgs = append(cmdArgs, b)
			}
			return run("git", cmdArgs...)
		},
	})

	reg.Register(&skill.Skill{
		Name:        "git_push",
		Description: "Enviar commits para o repositorio remoto",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"remote": {Type: jsonschema.String, Description: "Remoto (default: origin)"},
				"branch": {Type: jsonschema.String, Description: "Branch (opcional)"},
				"force":  {Type: jsonschema.String, Description: "'true' para force push"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			remote := args["remote"]
			if remote == "" {
				remote = "origin"
			}
			cmdArgs := []string{"push", remote}
			if b := args["branch"]; b != "" {
				cmdArgs = append(cmdArgs, b)
			}
			if args["force"] == "true" {
				cmdArgs = append(cmdArgs, "--force")
			}
			return run("git", cmdArgs...)
		},
	})

	reg.Register(&skill.Skill{
		Name:        "git_tag",
		Description: "Gerenciar tags git",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"action":  {Type: jsonschema.String, Description: "Acao: list, create, delete"},
				"name":    {Type: jsonschema.String, Description: "Nome da tag"},
				"message": {Type: jsonschema.String, Description: "Mensagem (para tag anotada)"},
			},
			Required: []string{"action"},
		},
		Execute: func(args map[string]string) (string, error) {
			switch args["action"] {
			case "list":
				return run("git", "tag", "-l")
			case "create":
				if msg := args["message"]; msg != "" {
					return run("git", "tag", "-a", args["name"], "-m", msg)
				}
				return run("git", "tag", args["name"])
			case "delete":
				return run("git", "tag", "-d", args["name"])
			default:
				return "", fmt.Errorf("acao desconhecida: %s", args["action"])
			}
		},
	})

	reg.Register(&skill.Skill{
		Name:        "git_clone",
		Description: "Clonar um repositorio git",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"url":  {Type: jsonschema.String, Description: "URL do repositorio"},
				"path": {Type: jsonschema.String, Description: "Diretorio destino (opcional)"},
			},
			Required: []string{"url"},
		},
		Execute: func(args map[string]string) (string, error) {
			cmdArgs := []string{"clone", args["url"]}
			if p := args["path"]; p != "" {
				cmdArgs = append(cmdArgs, p)
			}
			return run("git", cmdArgs...)
		},
	})

	reg.Register(&skill.Skill{
		Name:        "git_remote",
		Description: "Mostrar informacoes dos remotos",
		Parameters:  jsonschema.Definition{Type: jsonschema.Object, Properties: map[string]jsonschema.Definition{}},
		Execute: func(args map[string]string) (string, error) {
			return run("git", "remote", "-v")
		},
	})

	reg.Register(&skill.Skill{
		Name:        "git_init",
		Description: "Inicializar um novo repositorio git",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path": {Type: jsonschema.String, Description: "Diretorio (default: .)"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			p := args["path"]
			if p == "" {
				p = "."
			}
			return run("git", "init", p)
		},
	})

	reg.Register(&skill.Skill{
		Name:        "git_merge",
		Description: "Fazer merge de uma branch",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"branch": {Type: jsonschema.String, Description: "Branch para merge"},
				"no_ff":  {Type: jsonschema.String, Description: "'true' para no-fast-forward"},
			},
			Required: []string{"branch"},
		},
		Execute: func(args map[string]string) (string, error) {
			cmdArgs := []string{"merge"}
			if args["no_ff"] == "true" {
				cmdArgs = append(cmdArgs, "--no-ff")
			}
			cmdArgs = append(cmdArgs, args["branch"])
			return run("git", cmdArgs...)
		},
	})

	reg.Register(&skill.Skill{
		Name:        "git_cherry_pick",
		Description: "Cherry-pick um commit",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"ref": {Type: jsonschema.String, Description: "Hash do commit"},
			},
			Required: []string{"ref"},
		},
		Execute: func(args map[string]string) (string, error) {
			return run("git", "cherry-pick", args["ref"])
		},
	})
}

func run(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), execTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(out))
	if ctx.Err() == context.DeadlineExceeded {
		return result + "\n[timeout: 30s]", nil
	}
	if err != nil && result == "" {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return result, nil
}
