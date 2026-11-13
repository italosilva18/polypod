package projectchecks

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ImpactHeader marks automatic post-edit caller/contract impact summaries.
const ImpactHeader = "[RAIO DE IMPACTO DA EDIÇÃO]"

type exportedSymbol struct {
	Key       string // e.g. "Func:Run", "Method:Client.Complete", "Type:Config"
	SearchSym string // identifier to grep across callers, e.g. "Run", "Complete", "Config"
	Signature string
}

// AnalyzeEditImpact compares oldContent and newContent of path, detects changed
// or removed exported symbols/contracts, locates external callers in the repo,
// and highlights companion test files that should be verified or updated.
func AnalyzeEditImpact(ctx context.Context, path string, oldContent, newContent []byte) string {
	if len(oldContent) == 0 || len(newContent) == 0 || bytes.Equal(oldContent, newContent) {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}

	ext := strings.ToLower(filepath.Ext(path))
	// Skip test files themselves to avoid noisy self-referential notices.
	base := filepath.Base(path)
	if strings.HasSuffix(base, "_test.go") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") || strings.HasPrefix(base, "test_") {
		return ""
	}

	var changed []exportedSymbol
	switch ext {
	case ".go":
		changed = diffGoExportedSymbols(oldContent, newContent)
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".py", ".rs":
		changed = diffScriptExportedSymbols(ext, oldContent, newContent)
	}
	if len(changed) == 0 {
		return ""
	}

	repoRoot := findRepoOrModuleRoot(filepath.Dir(path))
	if repoRoot == "" {
		repoRoot = filepath.Dir(path)
	}

	impactCtx, cancel := context.WithTimeout(ctx, 900*time.Millisecond)
	defer cancel()

	absPath, _ := filepath.Abs(path)
	var b strings.Builder
	b.WriteString(ImpactHeader + "\n")
	b.WriteString("Assinaturas/contratos exportados alterados:\n")

	limit := len(changed)
	if limit > 5 {
		limit = 5
	}

	totalCallers := 0
	seenCallerLines := make(map[string]bool)
	var callerLines []string

	for i := 0; i < limit; i++ {
		sym := changed[i]
		fmt.Fprintf(&b, "  - %s\n", sym.Signature)
		if len(sym.SearchSym) < 3 || impactCtx.Err() != nil {
			continue
		}
		refs := findExternalReferences(impactCtx, repoRoot, sym.SearchSym, absPath, 5)
		for _, ref := range refs {
			if !seenCallerLines[ref] {
				seenCallerLines[ref] = true
				callerLines = append(callerLines, ref)
				totalCallers++
			}
		}
	}
	if len(changed) > limit {
		fmt.Fprintf(&b, "  - ... (+%d símbolos exportados alterados)\n", len(changed)-limit)
	}

	if len(callerLines) > 0 {
		if len(callerLines) > 8 {
			callerLines = append(callerLines[:8], fmt.Sprintf("  ... (+%d referências externas)", totalCallers-8))
		}
		b.WriteString("Chamadores/dependentes externos detectados (verifique compatibilidade):\n")
		for _, line := range callerLines {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}

	if companion := findCompanionTestFile(path); companion != "" {
		relCompanion := companion
		if rel, err := filepath.Rel(repoRoot, companion); err == nil && !strings.HasPrefix(rel, "..") {
			relCompanion = rel
		}
		fmt.Fprintf(&b, "Teste correlato detectado: %s (execute/atualize antes de concluir)", relCompanion)
	}

	// Only emit impact notice when there are external callers or a companion test,
	// or when an exported symbol was deleted outright.
	hasRemoved := false
	for _, sym := range changed {
		if strings.HasPrefix(sym.Signature, "[REMOVIDO]") {
			hasRemoved = true
			break
		}
	}
	if len(callerLines) == 0 && findCompanionTestFile(path) == "" && !hasRemoved {
		return ""
	}
	return strings.TrimSpace(b.String())
}

// QuerySymbolImpact inspects external callers, declarations, and test coverage
// references for symbol across workDir. Used by the read-only `code_impact` tool.
func QuerySymbolImpact(ctx context.Context, workDir, symbol, excludeFile string) string {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return "Informe um símbolo não vazio para análise de impacto."
	}
	if ctx == nil {
		ctx = context.Background()
	}
	root := findRepoOrModuleRoot(workDir)
	if root == "" {
		if abs, err := filepath.Abs(workDir); err == nil {
			root = abs
		} else {
			root = "."
		}
	}

	var absExclude string
	if strings.TrimSpace(excludeFile) != "" {
		p := excludeFile
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		absExclude, _ = filepath.Abs(p)
	}

	qCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()

	refs := findExternalReferences(qCtx, root, symbol, absExclude, 30)
	if len(refs) == 0 {
		return fmt.Sprintf("Nenhuma referência externa encontrada para %q em %s.", symbol, root)
	}

	var prodRefs, testRefs []string
	for _, r := range refs {
		colon := strings.IndexByte(r, ':')
		filePart := r
		if colon > 0 {
			filePart = r[:colon]
		}
		if isTestFilename(filePart) {
			testRefs = append(testRefs, r)
		} else {
			prodRefs = append(prodRefs, r)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Raio de impacto para %q (%d referências em %s):\n", symbol, len(refs), root)
	if len(prodRefs) > 0 {
		fmt.Fprintf(&b, "\nCódigo de produção (%d):\n", len(prodRefs))
		for _, r := range prodRefs {
			fmt.Fprintf(&b, "  %s\n", r)
		}
	}
	if len(testRefs) > 0 {
		fmt.Fprintf(&b, "\nTestes correlatos (%d):\n", len(testRefs))
		for _, r := range testRefs {
			fmt.Fprintf(&b, "  %s\n", r)
		}
	} else {
		b.WriteString("\nTestes correlatos: nenhuma referência direta encontrada em arquivos de teste.\n")
	}
	return strings.TrimSpace(b.String())
}

func diffGoExportedSymbols(oldSrc, newSrc []byte) []exportedSymbol {
	oldMap, okOld := extractGoExportedSymbols(oldSrc)
	newMap, okNew := extractGoExportedSymbols(newSrc)
	if !okOld || !okNew {
		return nil
	}

	var changed []exportedSymbol
	for key, oldSym := range oldMap {
		newSym, exists := newMap[key]
		if !exists {
			changed = append(changed, exportedSymbol{
				Key:       key,
				SearchSym: oldSym.SearchSym,
				Signature: "[REMOVIDO] " + oldSym.Signature,
			})
			continue
		}
		if oldSym.Signature != newSym.Signature {
			changed = append(changed, exportedSymbol{
				Key:       key,
				SearchSym: newSym.SearchSym,
				Signature: oldSym.Signature + "  ==>  " + newSym.Signature,
			})
		}
	}
	sort.Slice(changed, func(i, j int) bool { return changed[i].Key < changed[j].Key })
	return changed
}

func extractGoExportedSymbols(src []byte) (map[string]exportedSymbol, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		return nil, false
	}
	out := make(map[string]exportedSymbol)
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name == nil || !d.Name.IsExported() {
				continue
			}
			recv := ""
			if d.Recv != nil && len(d.Recv.List) > 0 {
				recvType := renderASTNode(fset, d.Recv.List[0].Type)
				recvBase := strings.TrimPrefix(recvType, "*")
				if idx := strings.IndexByte(recvBase, '['); idx > 0 {
					recvBase = recvBase[:idx]
				}
				if recvBase != "" && !ast.IsExported(recvBase) {
					// Exported method on an unexported type can still satisfy interfaces,
					// so keep tracking it under receiver.Method.
				}
				recv = recvType
			}
			sigCopy := &ast.FuncDecl{
				Recv: d.Recv,
				Name: d.Name,
				Type: d.Type,
			}
			sig := renderASTNode(fset, sigCopy)
			key := "Func:" + d.Name.Name
			if recv != "" {
				key = "Method:" + recv + "." + d.Name.Name
			}
			out[key] = exportedSymbol{
				Key:       key,
				SearchSym: d.Name.Name,
				Signature: sig,
			}
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name == nil || !ts.Name.IsExported() {
					continue
				}
				rendered := "type " + renderASTNode(fset, ts)
				// Collapse multi-line struct/interface definitions to compact normalized single line.
				rendered = strings.Join(strings.Fields(rendered), " ")
				if len(rendered) > 140 {
					rendered = rendered[:140] + "..."
				}
				key := "Type:" + ts.Name.Name
				out[key] = exportedSymbol{
					Key:       key,
					SearchSym: ts.Name.Name,
					Signature: rendered,
				}
			}
		}
	}
	return out, true
}

func renderASTNode(fset *token.FileSet, node any) string {
	var buf bytes.Buffer
	cfg := printer.Config{Mode: printer.UseSpaces, Tabwidth: 4}
	if err := cfg.Fprint(&buf, fset, node); err != nil {
		return ""
	}
	return strings.TrimSpace(buf.String())
}

var (
	reJSExport = regexp.MustCompile(`(?m)^export\s+(?:default\s+)?(?:async\s+)?(?:function|class|interface|type)\s+([A-Za-z_][A-Za-z0-9_]*)\s*([^\{\n]*)`)
	rePyDef    = regexp.MustCompile(`(?m)^(def|class)\s+([A-Za-z][A-Za-z0-9_]*)\s*(\([^\n:]*\))?`)
	reRustPub  = regexp.MustCompile(`(?m)^pub\s+(?:async\s+)?(?:fn|struct|enum|trait)\s+([A-Za-z_][A-Za-z0-9_]*)\s*([^\{\n]*)`)
)

func diffScriptExportedSymbols(ext string, oldSrc, newSrc []byte) []exportedSymbol {
	oldMap := extractScriptSymbols(ext, string(oldSrc))
	newMap := extractScriptSymbols(ext, string(newSrc))
	var changed []exportedSymbol
	for key, oldSym := range oldMap {
		newSym, ok := newMap[key]
		if !ok {
			changed = append(changed, exportedSymbol{
				Key:       key,
				SearchSym: oldSym.SearchSym,
				Signature: "[REMOVIDO] " + oldSym.Signature,
			})
			continue
		}
		if oldSym.Signature != newSym.Signature {
			changed = append(changed, exportedSymbol{
				Key:       key,
				SearchSym: newSym.SearchSym,
				Signature: oldSym.Signature + "  ==>  " + newSym.Signature,
			})
		}
	}
	sort.Slice(changed, func(i, j int) bool { return changed[i].Key < changed[j].Key })
	return changed
}

func extractScriptSymbols(ext, src string) map[string]exportedSymbol {
	out := make(map[string]exportedSymbol)
	switch ext {
	case ".py":
		for _, m := range rePyDef.FindAllStringSubmatch(src, -1) {
			name := m[2]
			if strings.HasPrefix(name, "_") {
				continue
			}
			sig := strings.TrimSpace(m[1] + " " + name + m[3])
			out[name] = exportedSymbol{Key: name, SearchSym: name, Signature: sig}
		}
	case ".rs":
		for _, m := range reRustPub.FindAllStringSubmatch(src, -1) {
			name := m[1]
			sig := strings.Join(strings.Fields(m[0]), " ")
			out[name] = exportedSymbol{Key: name, SearchSym: name, Signature: sig}
		}
	default:
		for _, m := range reJSExport.FindAllStringSubmatch(src, -1) {
			name := m[1]
			sig := strings.Join(strings.Fields(m[0]), " ")
			out[name] = exportedSymbol{Key: name, SearchSym: name, Signature: sig}
		}
	}
	return out
}

func findExternalReferences(ctx context.Context, root, symbol, absExclude string, maxResults int) []string {
	if maxResults <= 0 {
		maxResults = 10
	}
	// Prefer `git grep -n -w` when inside a Git repository for speed & .gitignore respect.
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		if gitBin, lookErr := exec.LookPath("git"); lookErr == nil {
			cmd := exec.CommandContext(ctx, gitBin, "grep", "-n", "-w", "--no-color", symbol)
			cmd.Dir = root
			cmd.WaitDelay = 200 * time.Millisecond
			var out limitedBuffer
			cmd.Stdout = &out
			_ = cmd.Run()
			if out.Len() > 0 {
				return filterRefLines(out.String(), root, absExclude, maxResults)
			}
		}
	}

	// Fallback to ripgrep or standard grep when outside a git repo.
	if rgBin, err := exec.LookPath("rg"); err == nil {
		cmd := exec.CommandContext(ctx, rgBin, "-n", "-w", "--no-heading", "--color", "never", symbol, ".")
		cmd.Dir = root
		cmd.WaitDelay = 200 * time.Millisecond
		var out limitedBuffer
		cmd.Stdout = &out
		_ = cmd.Run()
		if out.Len() > 0 {
			return filterRefLines(out.String(), root, absExclude, maxResults)
		}
	}
	if grepBin, err := exec.LookPath("grep"); err == nil {
		cmd := exec.CommandContext(ctx, grepBin, "-r", "-n", "-w", "--exclude-dir=.git", "--exclude-dir=node_modules", "--exclude-dir=vendor", symbol, ".")
		cmd.Dir = root
		cmd.WaitDelay = 200 * time.Millisecond
		var out limitedBuffer
		cmd.Stdout = &out
		_ = cmd.Run()
		if out.Len() > 0 {
			return filterRefLines(out.String(), root, absExclude, maxResults)
		}
	}
	return nil
}

func filterRefLines(raw, root, absExclude string, maxResults int) []string {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	results := make([]string, 0, maxResults)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimPrefix(line, "./")
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			continue
		}
		relFile := line[:colon]
		if absExclude != "" {
			candidateAbs, err := filepath.Abs(filepath.Join(root, relFile))
			if err == nil && candidateAbs == absExclude {
				continue
			}
		}
		if len(line) > 160 {
			line = line[:160] + "..."
		}
		results = append(results, line)
		if len(results) >= maxResults {
			break
		}
	}
	return results
}

func findCompanionTestFile(path string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)

	var candidates []string
	switch strings.ToLower(ext) {
	case ".go":
		candidates = []string{filepath.Join(dir, stem+"_test.go")}
	case ".py":
		candidates = []string{
			filepath.Join(dir, "test_"+base),
			filepath.Join(dir, stem+"_test.py"),
			filepath.Join(dir, "tests", "test_"+base),
		}
	case ".ts", ".tsx", ".js", ".jsx":
		candidates = []string{
			filepath.Join(dir, stem+".test"+ext),
			filepath.Join(dir, stem+".spec"+ext),
			filepath.Join(dir, "__tests__", stem+".test"+ext),
		}
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	return ""
}

func findRepoOrModuleRoot(startDir string) string {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func isTestFilename(path string) bool {
	base := filepath.Base(path)
	return strings.HasSuffix(base, "_test.go") ||
		strings.HasPrefix(base, "test_") ||
		strings.HasSuffix(base, "_test.py") ||
		strings.Contains(base, ".test.") ||
		strings.Contains(base, ".spec.")
}
