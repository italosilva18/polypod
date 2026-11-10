package tool

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/costa/polypod/internal/types"
)

// RegisterGrokTool delegates to the official CLI, which owns account login and
// token refresh. It is deliberately not an implementation of provider.Provider:
// the CLI runs its own agent and permission loop.
func RegisterGrokTool(reg *Registry) {
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "grok_delegate" },
		DescriptionFn: func() string {
			return "Delegar uma tarefa ao Grok Build usando a sessão local da conta (grok login). Pode executar ferramentas e alterar arquivos; respeita as permissões do Grok."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt":        map[string]any{"type": "string", "description": "Tarefa completa e contexto para o Grok"},
					"model":         map[string]any{"type": "string", "description": "Modelo opcional; vazio usa o padrão do Grok"},
					"approve_tools": map[string]any{"type": "boolean", "default": false, "description": "Autorizar as ferramentas internas do Grok para esta tarefa delegada; use somente com autorização do usuário"},
				},
				"required": []string{"prompt"},
			}
		},
		ValidateFn: func(args map[string]any) types.ValidationResult {
			prompt, _ := args["prompt"].(string)
			if strings.TrimSpace(prompt) == "" || len(prompt) > 128*1024 {
				return types.ValidationResult{Message: "Informe uma tarefa com até 128 KiB."}
			}
			return types.ValidationResult{Valid: true}
		},
		IsDestructiveFn: func(map[string]any) bool { return true },
		ExecuteFn:       executeGrok,
	})
}

func executeGrok(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
	prompt, _ := args["prompt"].(string)
	if strings.TrimSpace(prompt) == "" || len(prompt) > 128*1024 {
		return types.ToolResult{IsError: true, Content: "Informe uma tarefa com até 128 KiB."}, nil
	}
	path, err := grokExecutable()
	if err != nil {
		return types.ToolResult{IsError: true, Content: "Grok Build não instalado. Instale o CLI oficial e execute grok login antes de delegar."}, nil
	}
	parent := tc.Context
	if parent == nil {
		parent = context.Background()
	}
	// Repository work often takes longer than a short chat delegation. Keep a
	// finite ceiling while allowing the parent task or abort signal to cancel.
	ctx, cancel := context.WithTimeout(parent, time.Hour)
	defer cancel()
	if tc.AbortChan != nil {
		go func() {
			select {
			case <-tc.AbortChan:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	argv := []string{"--no-auto-update", "--output-format", "plain"}
	if approve, _ := args["approve_tools"].(bool); approve {
		argv = append(argv, "--always-approve")
	} else {
		argv = append(argv, "--permission-mode", "dontAsk")
	}
	if model, _ := args["model"].(string); model != "" {
		argv = append(argv, "--model", model)
	}
	cmd := exec.CommandContext(ctx, path, argv...)
	if err := configureGrokProcess(cmd); err != nil {
		return types.ToolResult{IsError: true, Content: "Não foi possível preparar o usuário de execução do Grok. Verifique POLYPOD_DESKTOP_USER no serviço."}, nil
	}
	promptFile, err := os.CreateTemp("", "polypod-grok-prompt-*")
	if err != nil {
		return types.ToolResult{IsError: true, Content: "Não foi possível preparar a tarefa do Grok."}, nil
	}
	defer os.Remove(promptFile.Name())
	_, writeErr := promptFile.WriteString(prompt)
	closeErr := promptFile.Close()
	if writeErr != nil || closeErr != nil || setGrokPromptOwner(cmd, promptFile.Name()) != nil {
		return types.ToolResult{IsError: true, Content: "Não foi possível preparar a tarefa do Grok."}, nil
	}
	cmd.Args = append(cmd.Args, "--prompt-file", promptFile.Name())
	cmd.Dir = tc.WorkingDir
	cmd.WaitDelay = time.Second
	// stdin is closed, so login/permission prompts cannot block unattended work.
	// --always-approve requires an explicit per-call option visible to the owner.
	var output grokOutput
	cmd.Stdout = &output
	cmd.Stderr = io.Discard // CLI diagnostics can contain authentication material.
	err = cmd.Run()
	if ctx.Err() != nil {
		return types.ToolResult{IsError: true, Content: "Delegação ao Grok cancelada ou excedeu o limite de 1 hora."}, nil
	}
	if err != nil {
		// Do not return raw subprocess errors/output from failed auth flows.
		return types.ToolResult{IsError: true, Content: "Grok não concluiu a tarefa. Verifique grok login e as permissões no CLI oficial; aprovações interativas não estão disponíveis nesta delegação."}, nil
	}
	content := output.String()
	if output.truncated {
		content += "\n[Saída do Grok truncada em 64 KiB.]"
	}
	if strings.TrimSpace(content) == "" {
		return types.ToolResult{IsError: true, Content: "Grok terminou sem resposta."}, nil
	}
	return types.ToolResult{Content: content}, nil
}

func grokExecutable() (string, error) {
	if path := os.Getenv("POLYPOD_GROK_BIN"); path != "" {
		if !filepath.IsAbs(path) {
			return "", fmt.Errorf("POLYPOD_GROK_BIN must be absolute")
		}
		return exec.LookPath(path)
	}
	if home := os.Getenv("POLYPOD_DESKTOP_HOME"); home != "" && filepath.IsAbs(home) {
		if path, err := exec.LookPath(filepath.Join(home, ".local", "bin", "grok")); err == nil {
			return path, nil
		}
	}
	return exec.LookPath("grok")
}

// grokOutput drains stdout while bounding memory consumption.
type grokOutput struct {
	strings.Builder
	truncated bool
}

func (b *grokOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 64*1024 - b.Len()
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	if _, err := b.Builder.Write(p); err != nil {
		return 0, fmt.Errorf("capturing Grok output: %w", err)
	}
	return n, nil
}
