package evaluation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	maxCommandOutput = 16 << 20
	maxVerifiedFile  = 128 << 20
)

// CommandCase describes an independently prepared evaluation workspace. The
// command must be an argv array, not a shell script, and contain {prompt} in
// exactly one argument. The expected digest is deliberately kept outside the
// prompt sent to the agent.
type CommandCase struct {
	TaskID         string   `json:"task_id"`
	WorkingDir     string   `json:"working_dir"`
	Command        []string `json:"command"`
	ExpectedFile   string   `json:"expected_file"`
	ExpectedSHA256 string   `json:"expected_sha256"`
	VerifyCommand  []string `json:"verify_command,omitempty"`
}

type CommandPlan struct {
	Cases []CommandCase `json:"cases"`
}

// CommandRunner executes manifest tasks through real subprocesses in
// caller-prepared workspaces. It does not itself provide a container/VM
// boundary; use one when evaluating agents that can run arbitrary tools.
type CommandRunner struct {
	cases map[string]CommandCase
}

func NewCommandRunner(manifest Manifest, plan CommandPlan) (*CommandRunner, error) {
	if len(plan.Cases) != len(manifest.Tasks) {
		return nil, errors.New("o plano deve ter exatamente um caso por tarefa")
	}
	tasks := make(map[string]bool, len(manifest.Tasks))
	for _, task := range manifest.Tasks {
		tasks[task.ID] = true
	}
	runner := &CommandRunner{cases: make(map[string]CommandCase, len(plan.Cases))}
	for _, c := range plan.Cases {
		if !tasks[c.TaskID] || runner.cases[c.TaskID].TaskID != "" {
			return nil, fmt.Errorf("caso desconhecido ou duplicado: %q", c.TaskID)
		}
		if !filepath.IsAbs(c.WorkingDir) {
			return nil, fmt.Errorf("diretório de %q deve ser absoluto", c.TaskID)
		}
		info, err := os.Stat(c.WorkingDir)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("diretório de %q indisponível: %v", c.TaskID, err)
		}
		if len(c.Command) == 0 || strings.TrimSpace(c.Command[0]) == "" {
			return nil, fmt.Errorf("comando ausente em %q", c.TaskID)
		}
		prompts := 0
		for _, arg := range c.Command {
			prompts += strings.Count(arg, "{prompt}")
		}
		if prompts != 1 {
			return nil, fmt.Errorf("comando de %q deve conter um {prompt}", c.TaskID)
		}
		if c.ExpectedFile != "" {
			if filepath.IsAbs(c.ExpectedFile) || !safeRelativePath(c.ExpectedFile) {
				return nil, fmt.Errorf("arquivo esperado de %q deve ficar dentro do workspace", c.TaskID)
			}
			if len(c.ExpectedSHA256) != 64 {
				return nil, fmt.Errorf("SHA-256 esperado inválido em %q", c.TaskID)
			}
			if _, err := hex.DecodeString(c.ExpectedSHA256); err != nil {
				return nil, fmt.Errorf("SHA-256 esperado inválido em %q: %w", c.TaskID, err)
			}
		} else if c.ExpectedSHA256 != "" {
			return nil, fmt.Errorf("SHA-256 sem arquivo em %q", c.TaskID)
		}
		if len(c.VerifyCommand) > 0 {
			if !filepath.IsAbs(c.VerifyCommand[0]) {
				return nil, fmt.Errorf("verificador de %q deve usar executável absoluto", c.TaskID)
			}
			for _, arg := range c.VerifyCommand {
				if strings.TrimSpace(arg) == "" || strings.Contains(arg, "{prompt}") {
					return nil, fmt.Errorf("argumento inválido no verificador de %q", c.TaskID)
				}
			}
		}
		runner.cases[c.TaskID] = c
	}
	return runner, nil
}

func safeRelativePath(path string) bool {
	clean := filepath.Clean(path)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

type limitWriter struct {
	buf   bytes.Buffer
	limit int
}

func (w *limitWriter) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > w.limit {
		return 0, errors.New("saída do agente excedeu o limite")
	}
	return w.buf.Write(p)
}

// Execute sends only the prompt to a headless agent command. Response content
// remains in memory for the verifier and is never copied to Results.
func (r *CommandRunner) Execute(ctx context.Context, task Task) (Execution, error) {
	c, ok := r.cases[task.ID]
	if !ok {
		return Execution{}, fmt.Errorf("caso não configurado: %q", task.ID)
	}
	if c.ExpectedFile != "" {
		initial, err := observedFileSHA256(ctx, c.WorkingDir, c.ExpectedFile)
		if err != nil {
			return Execution{}, fmt.Errorf("estado inicial de %q: %w", task.ID, err)
		}
		if strings.EqualFold(initial, c.ExpectedSHA256) {
			return Execution{}, fmt.Errorf("resultado de %q já existia antes da execução", task.ID)
		}
	}
	argv := append([]string(nil), c.Command...)
	for i := range argv {
		argv[i] = strings.ReplaceAll(argv[i], "{prompt}", task.Prompt)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = c.WorkingDir
	stdout := &limitWriter{limit: maxCommandOutput}
	stderr := &limitWriter{limit: maxCommandOutput}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Execution{}, ctx.Err()
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && (c.ExpectedFile != "" || len(c.VerifyCommand) > 0) {
			// A coding patch can be correct even when the agent fails to produce a
			// final answer. Let the external verifier judge the workspace.
			code := exitErr.ExitCode()
			return Execution{AgentExitCode: &code}, nil
		}
		// stderr can contain private material, so do not return it in errors.
		return Execution{}, fmt.Errorf("agente de %q falhou: %w", task.ID, err)
	}
	var result struct {
		Content      string  `json:"content"`
		CostMeasured bool    `json:"cost_measured"`
		CostUSD      float64 `json:"cost_usd"`
		Steps        *int    `json:"steps"`
		ToolFailures *int    `json:"tool_failures"`
		Tokens       struct {
			Total int `json:"total"`
		} `json:"tokens"`
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.buf.Bytes()))
	if err := decoder.Decode(&result); err != nil {
		return Execution{}, fmt.Errorf("saída JSON de %q: %w", task.ID, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Execution{}, fmt.Errorf("saída JSON adicional de %q", task.ID)
	}
	execution := Execution{Response: result.Content, Steps: result.Steps, ToolFailures: result.ToolFailures}
	if result.CostMeasured {
		execution.CostUSD = &result.CostUSD
	}
	if result.Tokens.Total > 0 {
		execution.Tokens = &result.Tokens.Total
	}
	return execution, nil
}

// Verify checks an externally observed file and/or a fixed verifier command.
// Cases without a verifier stay skipped, not successful.
func (r *CommandRunner) Verify(ctx context.Context, task Task, execution Execution) (Verification, error) {
	c, ok := r.cases[task.ID]
	if !ok {
		return Verification{}, fmt.Errorf("caso não configurado: %q", task.ID)
	}
	if c.ExpectedFile == "" && len(c.VerifyCommand) == 0 {
		return Verification{Status: "skipped"}, nil
	}
	if err := ctx.Err(); err != nil {
		return Verification{}, err
	}
	var evidence []string
	var verifiedBy []string
	if execution.AgentExitCode != nil {
		evidence = append(evidence, fmt.Sprintf("agent_exit_code:%d", *execution.AgentExitCode))
	}
	if c.ExpectedFile != "" {
		actual, err := observedFileSHA256(ctx, c.WorkingDir, c.ExpectedFile)
		if err != nil {
			return Verification{}, err
		}
		if actual == "missing" {
			return Verification{Status: "failure", VerifiedBy: "file_sha256", Evidence: strings.Join(append(evidence, "arquivo ausente"), ";")}, nil
		}
		if !strings.EqualFold(actual, c.ExpectedSHA256) {
			return Verification{Status: "failure", VerifiedBy: "file_sha256", Evidence: strings.Join(append(evidence, "sha256:"+actual), ";")}, nil
		}
		evidence = append(evidence, "sha256:"+actual)
		verifiedBy = append(verifiedBy, "file_sha256")
	}
	if len(c.VerifyCommand) > 0 {
		cmd := exec.CommandContext(ctx, c.VerifyCommand[0], c.VerifyCommand[1:]...)
		cmd.Dir = c.WorkingDir
		stdout := &limitWriter{limit: maxCommandOutput}
		stderr := &limitWriter{limit: maxCommandOutput}
		cmd.Stdout, cmd.Stderr = stdout, stderr
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				return Verification{}, ctx.Err()
			}
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				return Verification{Status: "failure", VerifiedBy: "external_command", Evidence: strings.Join(append(evidence, fmt.Sprintf("exit_code:%d", exitErr.ExitCode())), ";")}, nil
			}
			return Verification{}, fmt.Errorf("verificador de %q: %w", task.ID, err)
		}
		evidence = append(evidence, "exit_code:0")
		verifiedBy = append(verifiedBy, "external_command")
	}
	return Verification{Status: "success", VerifiedBy: strings.Join(verifiedBy, "+"), Evidence: strings.Join(evidence, ";")}, nil
}

func observedFileSHA256(ctx context.Context, root, relative string) (string, error) {
	// Refuse symlink traversal so the verifier reads only its case workspace.
	if err := ensureNoSymlinks(root, relative); errors.Is(err, os.ErrNotExist) {
		return "missing", nil
	} else if err != nil {
		return "", err
	}
	confined, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer confined.Close()
	// O_NONBLOCK prevents an attacker-controlled FIFO from freezing the runner
	// after the path check. O_NOFOLLOW rejects a swapped final symlink.
	f, err := confined.OpenFile(relative, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return "missing", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("não é arquivo regular")
	}
	if info.Size() > maxVerifiedFile {
		return "", errors.New("arquivo verificado excede 128 MiB")
	}
	h := sha256.New()
	buf := make([]byte, 64<<10)
	total := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, readErr := f.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > maxVerifiedFile {
				return "", errors.New("arquivo verificado excede 128 MiB")
			}
			if _, err := h.Write(buf[:n]); err != nil {
				return "", err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func ensureNoSymlinks(root, relative string) error {
	current := root
	for _, part := range strings.Split(filepath.Clean(relative), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlink")
		}
	}
	return nil
}
