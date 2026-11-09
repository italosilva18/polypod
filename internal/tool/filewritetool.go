package tool

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/costa/polypod/internal/types"
)

// ---------------------------------------------------------------------------
// file_write — standalone tool for creating / overwriting files
// ---------------------------------------------------------------------------

// NewFileWriteTool returns a types.Tool that creates new files or completely
// overwrites existing ones. It writes atomically via a temporary file +
// os.Rename and optionally creates parent directories.
func NewFileWriteTool() types.Tool {
	return types.BuildTool(types.ToolDef{
		NameFn: func() string { return "file_write" },
		DescriptionFn: func() string {
			return "Create a new file or completely overwrite an existing file. " +
				"Writes content atomically (temp file + rename). " +
				"Optionally creates parent directories. " +
				"Use file_edit for surgical, targeted edits instead."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Absolute or relative path to the file to create or overwrite",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "The full content to write to the file",
					},
					"create_dirs": map[string]any{
						"type":        "boolean",
						"description": "Create parent directories if they don't exist (default: true)",
					},
				},
				"required": []string{"path", "content"},
			}
		},
		IsDestructiveFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			p, _ := args["path"].(string)
			if p == "" {
				return types.ValidationResult{Valid: false, Message: "path is required"}
			}
			if _, ok := args["content"].(string); !ok {
				return types.ValidationResult{Valid: false, Message: "content is required"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			return executeFileWrite(ctx, args)
		},
	})
}

// executeFileWrite implements the core write logic.
func executeFileWrite(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
	rawPath, _ := args["path"].(string)
	content, _ := args["content"].(string)

	createDirs := true
	if cd, ok := args["create_dirs"].(bool); ok {
		createDirs = cd
	}

	// Resolve to absolute path.
	absPath := rawPath
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(ctx.WorkingDir, absPath)
	}
	absPath = filepath.Clean(absPath)

	// Keep the version seen at the start of the operation for a final check.
	info, statErr := os.Stat(absPath)
	existed := statErr == nil
	var original []byte
	if existed {
		var readErr error
		original, readErr = os.ReadFile(absPath)
		if readErr != nil {
			return types.ToolResult{Content: fmt.Sprintf("Erro ao ler arquivo: %v", readErr), IsError: true}, nil
		}
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return types.ToolResult{Content: fmt.Sprintf("Erro ao verificar arquivo: %v", statErr), IsError: true}, nil
	}

	// Optionally create parent directories.
	dir := filepath.Dir(absPath)
	if createDirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return types.ToolResult{
				Content: fmt.Sprintf("Erro ao criar diretorios: %v", err),
				IsError: true,
			}, nil
		}
	} else {
		// Verify parent exists.
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return types.ToolResult{
				Content: fmt.Sprintf("Diretorio pai nao existe: %s (use create_dirs=true para criar automaticamente)", dir),
				IsError: true,
			}, nil
		}
	}

	if !existed {
		if err := writeNewFileExclusive(absPath, []byte(content), 0644); err != nil {
			return types.ToolResult{Content: fmt.Sprintf("Erro ao criar arquivo: %v", err), IsError: true}, nil
		}
		return types.ToolResult{Content: fmt.Sprintf("Arquivo criado: %s (%d bytes)", absPath, len(content))}, nil
	}

	// Atomic replacement with a unique temporary file in the same directory.
	tmp, err := os.CreateTemp(dir, ".polypod-write-*")
	if err != nil {
		return types.ToolResult{Content: fmt.Sprintf("Erro ao criar arquivo temporario: %v", err), IsError: true}, nil
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(info.Mode()); err != nil {
		tmp.Close()
		return types.ToolResult{Content: fmt.Sprintf("Erro ao preservar permissoes: %v", err), IsError: true}, nil
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return types.ToolResult{
			Content: fmt.Sprintf("Erro ao escrever arquivo temporario: %v", err),
			IsError: true,
		}, nil
	}
	if err := tmp.Close(); err != nil {
		return types.ToolResult{Content: fmt.Sprintf("Erro ao fechar arquivo temporario: %v", err), IsError: true}, nil
	}
	matches, err := fileStillMatches(absPath, true, original)
	if err != nil {
		return types.ToolResult{Content: fmt.Sprintf("Erro ao verificar arquivo: %v", err), IsError: true}, nil
	}
	if !matches {
		return staleFileResult(absPath), nil
	}
	if err := os.Rename(tmpPath, absPath); err != nil {
		return types.ToolResult{
			Content: fmt.Sprintf("Erro ao renomear arquivo temporario: %v", err),
			IsError: true,
		}, nil
	}

	size := len(content)

	if existed {
		return types.ToolResult{
			Content: fmt.Sprintf("Arquivo sobrescrito: %s (%d bytes)", absPath, size),
		}, nil
	}

	return types.ToolResult{
		Content: fmt.Sprintf("Arquivo criado: %s (%d bytes)", absPath, size),
	}, nil
}
