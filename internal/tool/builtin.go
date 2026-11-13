package tool

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/costa/polypod/internal/constants"
	"github.com/costa/polypod/internal/types"
	"github.com/costa/polypod/internal/web"
)

// RegisterBuiltinTools registers all built-in tools with the registry.
func RegisterBuiltinTools(reg *Registry, workingDir string) {
	RegisterReadFileTool(reg, workingDir)
	reg.RegisterDef(newWriteFileTool(workingDir, reg))
	reg.RegisterDef(newEditFileTool(workingDir, reg))
	reg.RegisterDef(newListDirTool(workingDir))
	RegisterBashTool(reg, workingDir)
	reg.RegisterDef(newGlobTool(workingDir))
	reg.RegisterDef(newGrepTool(workingDir))
	reg.RegisterDef(newWebSearchTool())
	reg.RegisterDef(newWebFetchTool())
}

// --- write_file ---
func newWriteFileTool(wd string, regs ...*Registry) types.ToolDef {
	var reg *Registry
	if len(regs) > 0 {
		reg = regs[0]
	}
	return types.ToolDef{
		NameFn:        func() string { return constants.ToolWriteFile },
		DescriptionFn: func() string { return "Escrever conteudo em um arquivo (cria ou sobrescreve)" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string", "description": "Caminho do arquivo"},
					"content": map[string]any{"type": "string", "description": "Conteudo a escrever"},
				},
				"required": []string{"path", "content"},
			}
		},
		IsDestructiveFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			if args["path"] == "" {
				return types.ValidationResult{Valid: false, Message: "path e obrigatorio"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			path := resolvePath(effectiveWorkingDir(ctx, wd), args["path"])
			content, _ := args["content"].(string)
			orig, readErr := os.ReadFile(path)
			existed := readErr == nil
			if readErr != nil && !os.IsNotExist(readErr) {
				return types.ToolResult{Content: fmt.Sprintf("Erro ao ler %s: %v", path, readErr), IsError: true}, nil
			}
			mode := os.FileMode(0644)
			if existed {
				if st, stErr := os.Stat(path); stErr == nil {
					mode = st.Mode()
				}
			}
			if reg != nil {
				if err := reg.runBeforeFileWrite(ctx, path, orig, existed); err != nil {
					return beforeFileWriteError(err), nil
				}
			}
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Erro: %v", err), IsError: true}, nil
			}
			if err := os.WriteFile(path, []byte(content), mode); err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Erro: %v", err), IsError: true}, nil
			}
			res := types.ToolResult{Content: fmt.Sprintf("Arquivo escrito: %s (%d bytes)", path, len(content))}
			if reg != nil {
				return reg.afterFileWrite(ctx, path, content, res), nil
			}
			return res, nil
		},
	}
}

// --- edit_file ---
func newEditFileTool(wd string, regs ...*Registry) types.ToolDef {
	var reg *Registry
	if len(regs) > 0 {
		reg = regs[0]
	}
	return types.ToolDef{
		NameFn:        func() string { return constants.ToolEditFile },
		DescriptionFn: func() string { return "Substituir texto em um arquivo existente" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":     map[string]any{"type": "string", "description": "Caminho de arquivo existente, como math.go; nunca use um diretório como ."},
					"old_text": map[string]any{"type": "string", "description": "Texto a encontrar"},
					"new_text": map[string]any{"type": "string", "description": "Texto substituto"},
				},
				"required": []string{"path", "old_text", "new_text"},
			}
		},
		IsDestructiveFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			path, _ := args["path"].(string)
			if strings.TrimSpace(path) == "" || path == "." {
				return types.ValidationResult{Valid: false, Message: "edit_file: path deve indicar um arquivo existente, por exemplo math.go"}
			}
			oldText, _ := args["old_text"].(string)
			if oldText == "" {
				return types.ValidationResult{Valid: false, Message: "edit_file: old_text deve conter o trecho exato a substituir"}
			}
			if _, ok := args["new_text"].(string); !ok {
				return types.ValidationResult{Valid: false, Message: "edit_file: new_text deve ser texto"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			path := resolvePath(effectiveWorkingDir(ctx, wd), args["path"])
			data, err := os.ReadFile(path)
			if err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Erro ao ler %s: %v. Use o caminho de um arquivo existente, como math.go.", path, err), IsError: true}, nil
			}
			oldText, _ := args["old_text"].(string)
			newText, _ := args["new_text"].(string)
			content := string(data)
			if !strings.Contains(content, oldText) {
				return types.ToolResult{Content: "Texto nao encontrado no arquivo", IsError: true}, nil
			}
			newContent := strings.Replace(content, oldText, newText, 1)
			mode := os.FileMode(0644)
			if st, stErr := os.Stat(path); stErr == nil {
				mode = st.Mode()
			}
			if reg != nil {
				if err := reg.runBeforeFileWrite(ctx, path, data, true); err != nil {
					return beforeFileWriteError(err), nil
				}
			}
			if err := os.WriteFile(path, []byte(newContent), mode); err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Erro: %v", err), IsError: true}, nil
			}
			res := types.ToolResult{Content: fmt.Sprintf("Arquivo editado: %s", path)}
			if reg != nil {
				return reg.afterFileWrite(ctx, path, newContent, res), nil
			}
			return res, nil
		},
	}
}

// --- list_directory ---
func newListDirTool(wd string) types.ToolDef {
	return types.ToolDef{
		NameFn:        func() string { return constants.ToolReadFile + "_dir" },
		DescriptionFn: func() string { return "Listar arquivos e pastas em um diretorio" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string", "description": "Caminho do diretorio"},
				},
				"required": []string{"path"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			path := resolvePath(effectiveWorkingDir(ctx, wd), args["path"])
			entries, err := os.ReadDir(path)
			if err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Erro: %v", err), IsError: true}, nil
			}
			var sb strings.Builder
			for _, e := range entries {
				info, err := e.Info()
				if err != nil {
					continue
				}
				t := "-"
				if e.IsDir() {
					t = "d"
				}
				sb.WriteString(fmt.Sprintf("%s %8d %s %s\n",
					t, info.Size(), info.ModTime().Format("2006-01-02 15:04"), e.Name()))
			}
			return types.ToolResult{Content: sb.String()}, nil
		},
	}
}

// --- glob ---
func newGlobTool(wd string) types.ToolDef {
	return types.ToolDef{
		NameFn:        func() string { return constants.ToolGlobTool },
		DescriptionFn: func() string { return "Buscar arquivos por padrao glob (ex: **/*.go, *.md)" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{"type": "string", "description": "Padrao glob"},
					"path":    map[string]any{"type": "string", "description": "Diretorio base (default: .)"},
				},
				"required": []string{"pattern"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			pattern, _ := args["pattern"].(string)
			base := resolvePath(effectiveWorkingDir(ctx, wd), args["path"])
			if base == "" {
				base = "."
			}

			var matches []string
			filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				if len(matches) >= constants.MaxGlobResults {
					return filepath.SkipDir
				}
				matched, _ := filepath.Match(pattern, info.Name())
				if matched {
					matches = append(matches, path)
				}
				return nil
			})
			if len(matches) == 0 {
				return types.ToolResult{Content: "Nenhum arquivo encontrado."}, nil
			}
			result := strings.Join(matches, "\n")
			if len(matches) >= constants.MaxGlobResults {
				result += fmt.Sprintf("\n... [primeiros %d resultados]", constants.MaxGlobResults)
			}
			return types.ToolResult{Content: result}, nil
		},
	}
}

// --- grep ---
func newGrepTool(wd string) types.ToolDef {
	return types.ToolDef{
		NameFn:        func() string { return constants.ToolGrepTool },
		DescriptionFn: func() string { return "Buscar texto em arquivos por expressao regular" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{"type": "string", "description": "Expressao regular"},
					"path":    map[string]any{"type": "string", "description": "Diretorio base (default: .)"},
					"include": map[string]any{"type": "string", "description": "Filtro de extensao (ex: *.go)"},
				},
				"required": []string{"pattern"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			pattern, _ := args["pattern"].(string)
			searchPath := resolvePath(effectiveWorkingDir(ctx, wd), args["path"])
			if searchPath == "" {
				searchPath = "."
			}

			grepArgs := []string{"-r", "-n", "--include=*", pattern, searchPath}
			if inc, ok := args["include"].(string); ok && inc != "" {
				grepArgs[1] = "-r"
				grepArgs = []string{"-r", "-n", "--include=" + inc, pattern, searchPath}
			}

			cmd := exec.Command("grep", grepArgs...)
			out, _ := cmd.CombinedOutput()
			output := string(out)
			if len(output) > constants.MaxOutputBytes {
				output = output[:constants.MaxOutputBytes] + "\n... [saida truncada]"
			}
			if output == "" {
				return types.ToolResult{Content: "Nenhuma correspondencia encontrada."}, nil
			}
			return types.ToolResult{Content: output}, nil
		},
	}
}

// --- web_search ---
func newWebSearchTool() types.ToolDef {
	return types.ToolDef{
		NameFn:        func() string { return constants.ToolWebSearch },
		DescriptionFn: func() string { return "Buscar informacoes na web usando DuckDuckGo" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Termo de busca"},
					"count": map[string]any{"type": "integer", "description": "Numero de resultados (default: 5)"},
				},
				"required": []string{"query"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			q, _ := args["query"].(string)
			if q == "" {
				return types.ValidationResult{Valid: false, Message: "query e obrigatorio"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			query, _ := args["query"].(string)
			maxResults := 5
			if c, ok := args["count"].(float64); ok && c > 0 {
				maxResults = int(c)
			}
			if maxResults > 20 {
				maxResults = 20
			}

			results, err := web.Search(ctx.Context, query, maxResults)
			if err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Erro na busca: %v", err), IsError: true}, nil
			}
			if len(results) == 0 {
				return types.ToolResult{Content: "Nenhum resultado encontrado para: " + query}, nil
			}

			var sb strings.Builder
			for i, r := range results {
				sb.WriteString(fmt.Sprintf("%d. %s\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Snippet))
			}
			return types.ToolResult{Content: sb.String()}, nil
		},
	}
}

// --- web_fetch ---
func newWebFetchTool() types.ToolDef {
	return types.ToolDef{
		NameFn:        func() string { return constants.ToolWebFetch },
		DescriptionFn: func() string { return "Buscar e extrair conteudo de texto de uma URL" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url":    map[string]any{"type": "string", "description": "URL para buscar"},
					"format": map[string]any{"type": "string", "description": "Formato: text, markdown, html (default: text)"},
				},
				"required": []string{"url"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			u, _ := args["url"].(string)
			if u == "" {
				return types.ValidationResult{Valid: false, Message: "url e obrigatoria"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			u, _ := args["url"].(string)
			if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
				u = "https://" + u
			}
			content, err := web.FetchURL(u)
			if err != nil {
				return types.ToolResult{Content: fmt.Sprintf("Erro: %v", err), IsError: true}, nil
			}
			if len(content) > constants.MaxOutputBytes {
				content = content[:constants.MaxOutputBytes] + "\n... [conteudo truncado]"
			}
			return types.ToolResult{Content: content}, nil
		},
	}
}

func resolvePath(base string, path any) string {
	p, ok := path.(string)
	if !ok || p == "" {
		return base
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}
