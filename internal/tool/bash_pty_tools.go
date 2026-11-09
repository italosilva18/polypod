package tool

import (
	"fmt"
	"strings"
	"time"

	"github.com/costa/polypod/internal/bash"
	"github.com/costa/polypod/internal/types"
)

// RegisterBashPTYTools exposes persistent interactive terminal sessions.
func RegisterBashPTYTools(reg *Registry, manager *bash.PTYManager, bt *bash.BashTool, workingDir string) {
	reg.mu.Lock()
	reg.pty = manager
	reg.mu.Unlock()

	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "bash_pty_start" },
		DescriptionFn: func() string {
			return "Iniciar comando em terminal interativo persistente (PTY). Retorna ID para bash_pty_write/read/close."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command":     map[string]any{"type": "string", "description": "Comando interativo a executar"},
					"working_dir": map[string]any{"type": "string", "description": "Diretorio de trabalho opcional"},
					"rows":        map[string]any{"type": "integer", "description": "Linhas do terminal (padrao 24)"},
					"cols":        map[string]any{"type": "integer", "description": "Colunas do terminal (padrao 100)"},
				},
				"required": []string{"command"},
			}
		},
		IsDestructiveFn: func(map[string]any) bool { return true },
		CheckPermFn: func(args map[string]any, ctx types.ToolContext) types.PermissionResult {
			command, _ := args["command"].(string)
			return bt.CheckPermissions(command)
		},
		ValidateFn: func(args map[string]any) types.ValidationResult {
			if cmd, ok := args["command"].(string); !ok || strings.TrimSpace(cmd) == "" {
				return types.ValidationResult{Valid: false, Message: "command e obrigatorio"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			command, _ := args["command"].(string)
			wd := ptyWorkingDir(workingDir, ctx, args)
			rows := argInt(args, "rows", 24)
			cols := argInt(args, "cols", 100)
			if rows <= 0 || cols <= 0 || rows > 65535 || cols > 65535 {
				return errResult("rows e cols devem ficar entre 1 e 65535"), nil
			}
			return startPTYResult(manager, command, wd, commandEnvOverrides(), uint16(rows), uint16(cols))
		},
	})

	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return "bash_pty_write" },
		DescriptionFn: func() string { return "Enviar texto ou teclas de controle a uma sessao PTY." },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":             map[string]any{"type": "string", "description": "ID da sessao PTY"},
					"input":          map[string]any{"type": "string", "description": "Texto ou sequencia de teclas"},
					"append_newline": map[string]any{"type": "boolean", "description": "Enviar Enter apos o texto (padrao true)"},
				},
				"required": []string{"id", "input"},
			}
		},
		IsDestructiveFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			if argString(args, "id") == "" {
				return types.ValidationResult{Valid: false, Message: "id e obrigatorio"}
			}
			if _, ok := args["input"].(string); !ok {
				return types.ValidationResult{Valid: false, Message: "input e obrigatorio"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			id, _ := args["id"].(string)
			input, _ := args["input"].(string)
			appendNewline := true
			if value, ok := args["append_newline"].(bool); ok {
				appendNewline = value
			}
			if appendNewline && !strings.HasSuffix(input, "\n") {
				input += "\n"
			}
			n, err := manager.Write(ctx.Context, id, input)
			if err != nil {
				return errResult("Erro ao enviar entrada PTY: %v", err), nil
			}
			return okResult("Enviados %d bytes para PTY %s. Use bash_pty_read para acompanhar.", n, id), nil
		},
	})

	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return "bash_pty_read" },
		DescriptionFn: func() string { return "Ler saida paginada e estado de uma sessao PTY." },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":     map[string]any{"type": "string", "description": "ID da sessao PTY"},
					"offset": map[string]any{"type": "integer", "description": "Posicao em bytes (padrao 0)"},
					"limit":  map[string]any{"type": "integer", "description": "Maximo de bytes (ate 65536)"},
				},
				"required": []string{"id"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			if argString(args, "id") == "" {
				return types.ValidationResult{Valid: false, Message: "id e obrigatorio"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			id, _ := args["id"].(string)
			offset := int64(argInt(args, "offset", 0))
			limit := int64(argInt(args, "limit", 8192))
			chunk, status, err := manager.Read(id, offset, limit)
			if err != nil {
				return errResult("Erro ao ler PTY: %v", err), nil
			}
			return types.ToolResult{Content: formatPTYRead(status, offset, chunk)}, nil
		},
	})

	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return "bash_pty_close" },
		DescriptionFn: func() string { return "Encerrar processo de uma sessao PTY, preservando a saida para leitura." },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type":       "object",
				"properties": map[string]any{"id": map[string]any{"type": "string"}},
				"required":   []string{"id"},
			}
		},
		IsDestructiveFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			if argString(args, "id") == "" {
				return types.ValidationResult{Valid: false, Message: "id e obrigatorio"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			id, _ := args["id"].(string)
			if err := manager.Close(id); err != nil {
				return errResult("Erro ao encerrar PTY: %v", err), nil
			}
			return okResult("Sessao PTY %s encerrada. A saida pode ser lida com bash_pty_read.", id), nil
		},
	})

	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return "bash_pty_resize" },
		DescriptionFn: func() string { return "Ajustar linhas e colunas de uma sessao PTY." },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":   map[string]any{"type": "string"},
					"rows": map[string]any{"type": "integer"},
					"cols": map[string]any{"type": "integer"},
				},
				"required": []string{"id", "rows", "cols"},
			}
		},
		IsDestructiveFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			if argString(args, "id") == "" {
				return types.ValidationResult{Valid: false, Message: "id e obrigatorio"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			id, _ := args["id"].(string)
			rows, cols := argInt(args, "rows", 0), argInt(args, "cols", 0)
			if rows <= 0 || cols <= 0 || rows > 65535 || cols > 65535 {
				return errResult("rows e cols devem ficar entre 1 e 65535"), nil
			}
			if err := manager.Resize(id, uint16(rows), uint16(cols)); err != nil {
				return errResult("Erro ao redimensionar PTY: %v", err), nil
			}
			return okResult("PTY %s ajustado para %d linhas e %d colunas.", id, rows, cols), nil
		},
	})
}

func ptyWorkingDir(defaultDir string, ctx types.ToolContext, args map[string]any) string {
	wd := defaultDir
	if ctx.WorkingDir != "" {
		wd = ctx.WorkingDir
	}
	if override, ok := args["working_dir"].(string); ok && override != "" {
		wd = override
	}
	return wd
}

func startPTYResult(manager *bash.PTYManager, command, wd string, env map[string]string, rows, cols uint16) (types.ToolResult, error) {
	id, err := manager.Start(command, wd, env, rows, cols)
	if err != nil {
		return errResult("Erro ao iniciar PTY: %v", err), nil
	}
	time.Sleep(150 * time.Millisecond)
	chunk, status, err := manager.Read(id, 0, 8192)
	if err != nil {
		return okResult("Sessao PTY iniciada: %s. Use bash_pty_read para acompanhar. Aviso: %v", id, err), nil
	}
	return types.ToolResult{Content: "Sessao PTY iniciada. Use bash_pty_write/read/close com o ID abaixo.\n" +
		formatPTYRead(status, 0, chunk)}, nil
}

func formatPTYRead(status bash.PTYStatus, offset int64, chunk []byte) string {
	end := offset + int64(len(chunk))
	text := fmt.Sprintf("ID: %s | estado: %s | exit_code: %d | bytes: %d-%d de %d | proximo offset: %d",
		status.ID, status.State, status.ExitCode, offset, end, status.OutputBytes, end)
	if status.Error != "" {
		text += "\nAviso: " + status.Error
	}
	if len(chunk) > 0 {
		text += "\n" + strings.ToValidUTF8(string(chunk), "�")
	}
	return text
}

// ClosePTY stops all persistent terminal processes owned by this registry.
func (reg *Registry) ClosePTY() {
	reg.mu.RLock()
	manager := reg.pty
	reg.mu.RUnlock()
	if manager != nil {
		manager.CloseAll()
	}
}
