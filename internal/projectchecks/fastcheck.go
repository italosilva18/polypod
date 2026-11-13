package projectchecks

import (
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// AutoDiagnosticHeader marks deterministic post-edit syntax/type diagnostics
// so turn-level verification gates can detect unresolved broken edits.
const AutoDiagnosticHeader = "[AUTO-DIAGNÓSTICO PÓS-EDIÇÃO — CORRIJA ANTES DE CONCLUIR]"

// FastDiagnoseFile runs sub-second syntax and static type checks appropriate
// for path right after a file write/edit, even when .polypod/verifiers.yaml is
// not configured. It returns "" when clean or when no checker applies.
func FastDiagnoseFile(ctx context.Context, path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	src, err := os.ReadFile(path)
	if err != nil || len(src) == 0 {
		return ""
	}

	ext := strings.ToLower(filepath.Ext(path))
	var diag string
	switch ext {
	case ".go":
		diag = diagnoseGoFile(ctx, path, src)
	case ".json":
		diag = diagnoseJSONFile(path, src)
	case ".yaml", ".yml":
		diag = diagnoseYAMLFile(path, src)
	case ".py":
		diag = diagnosePythonFile(ctx, path)
	case ".js", ".mjs", ".cjs":
		diag = diagnoseNodeFile(ctx, path)
	case ".sh", ".bash":
		diag = diagnoseShellFile(ctx, path)
	}
	diag = strings.TrimSpace(diag)
	if diag == "" {
		return ""
	}
	return AutoDiagnosticHeader + "\n" + diag
}

func diagnoseGoFile(ctx context.Context, path string, src []byte) string {
	fset := token.NewFileSet()
	_, err := parser.ParseFile(fset, path, src, parser.AllErrors)
	if err != nil {
		if list, ok := err.(scanner.ErrorList); ok && len(list) > 0 {
			maxErrs := len(list)
			if maxErrs > 8 {
				maxErrs = 8
			}
			lines := make([]string, 0, maxErrs+1)
			for i := 0; i < maxErrs; i++ {
				lines = append(lines, list[i].Error())
			}
			if len(list) > maxErrs {
				lines = append(lines, fmt.Sprintf("... (+%d erros adicionais de sintaxe Go)", len(list)-maxErrs))
			}
			return strings.Join(lines, "\n")
		}
		return err.Error()
	}

	// Only run scoped `go vet` when inside a Go module and a Go toolchain exists.
	modRoot := findGoModRoot(filepath.Dir(path))
	if modRoot == "" {
		return ""
	}
	goBin := resolveGoBinary()
	if goBin == "" {
		return ""
	}

	pkgDir := filepath.Dir(path)
	relPkg, relErr := filepath.Rel(modRoot, pkgDir)
	if relErr != nil || strings.HasPrefix(relPkg, "..") {
		return ""
	}
	target := "."
	if relPkg != "." && relPkg != "" {
		target = "./" + filepath.ToSlash(relPkg)
	}

	vetCtx, cancel := context.WithTimeout(ctx, 3500*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(vetCtx, goBin, "vet", target)
	cmd.Dir = modRoot
	cmd.WaitDelay = 500 * time.Millisecond
	var out limitedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr := cmd.Run()
	if vetCtx.Err() != nil {
		// Timed out on cold cache — never block or fail the edit for latency.
		return ""
	}
	if runErr == nil {
		return ""
	}
	raw := strings.TrimSpace(out.String())
	if raw == "" {
		return ""
	}
	return trimDiagnosticLines(raw, 14)
}

func diagnoseJSONFile(path string, src []byte) string {
	if json.Valid(src) {
		return ""
	}
	var v any
	if err := json.Unmarshal(src, &v); err != nil {
		return fmt.Sprintf("%s: JSON inválido: %v", filepath.Base(path), err)
	}
	return fmt.Sprintf("%s: JSON inválido", filepath.Base(path))
}

func diagnoseYAMLFile(path string, src []byte) string {
	var v any
	if err := yaml.Unmarshal(src, &v); err != nil {
		return fmt.Sprintf("%s: %v", filepath.Base(path), err)
	}
	return ""
}

func diagnosePythonFile(ctx context.Context, path string) string {
	pyBin, err := exec.LookPath("python3")
	if err != nil {
		return ""
	}
	pyCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	cacheDir := filepath.Join(os.TempDir(), "polypod-pycache")
	cmd := exec.CommandContext(pyCtx, pyBin, "-m", "py_compile", path)
	cmd.Env = append(os.Environ(), "PYTHONPYCACHEPREFIX="+cacheDir)
	cmd.WaitDelay = 300 * time.Millisecond
	var out limitedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil && pyCtx.Err() == nil {
		return trimDiagnosticLines(out.String(), 10)
	}
	return ""
}

func diagnoseNodeFile(ctx context.Context, path string) string {
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		return ""
	}
	nodeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(nodeCtx, nodeBin, "--check", path)
	cmd.WaitDelay = 300 * time.Millisecond
	var out limitedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil && nodeCtx.Err() == nil {
		return trimDiagnosticLines(out.String(), 10)
	}
	return ""
}

func diagnoseShellFile(ctx context.Context, path string) string {
	bashBin, err := exec.LookPath("bash")
	if err != nil {
		return ""
	}
	shCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(shCtx, bashBin, "-n", path)
	cmd.WaitDelay = 300 * time.Millisecond
	var out limitedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil && shCtx.Err() == nil {
		return trimDiagnosticLines(out.String(), 8)
	}
	return ""
}

func findGoModRoot(startDir string) string {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return ""
	}
	for {
		if info, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil && !info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func resolveGoBinary() string {
	if p, err := exec.LookPath("go"); err == nil {
		return p
	}
	for _, candidate := range []string{"/usr/local/go/bin/go", "/usr/bin/go"} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func trimDiagnosticLines(raw string, maxLines int) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	lines := strings.Split(raw, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "# ") {
			continue
		}
		filtered = append(filtered, line)
	}
	if len(filtered) == 0 {
		filtered = lines
	}
	if len(filtered) > maxLines {
		extra := len(filtered) - maxLines
		filtered = append(filtered[:maxLines], fmt.Sprintf("... (+%d linhas de diagnóstico)", extra))
	}
	return strings.Join(filtered, "\n")
}
