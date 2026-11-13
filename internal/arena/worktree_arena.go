package arena

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// CandidateRunner executes a candidate strategy inside an isolated git worktree directory.
type CandidateRunner func(ctx context.Context, index int, worktreeDir string, strategy string) (summary string, err error)

// WorktreeArenaConfig configures a parallel Best-of-N execution across isolated git worktrees.
type WorktreeArenaConfig struct {
	RepoDir         string
	Task            string
	Strategies      []string        // defaults to 2 complementary engineering strategies when empty
	VerifierCmd     string          // shell command run in each worktree; auto-detected when empty
	Runner          CandidateRunner // executes edits inside worktreeDir
	ApplyWinner     bool            // apply verified winning patch onto RepoDir
	PerCandidateTTL time.Duration   // defaults to 4 minutes
}

// WorktreeCandidateResult captures one isolated worktree contender's outcome.
type WorktreeCandidateResult struct {
	Index          int
	Strategy       string
	Summary        string
	Patch          string
	ChangedFiles   []string
	DiffLines      int
	VerifierCmd    string
	VerifierPassed bool
	VerifierOutput string
	Duration       time.Duration
	RunnerErr      error
}

// WorktreeArenaOutcome holds all candidate results and the verified winner (if any).
type WorktreeArenaOutcome struct {
	Task          string
	Candidates    []WorktreeCandidateResult
	WinnerIndex   int // -1 when no candidate produced a verified patch
	WinnerApplied bool
	SavedPatches  []string
}

var defaultStrategies = []string{
	"Estratégia A (Cirúrgica Mínima): faça a menor alteração possível focada estritamente na causa-raiz, preservando 100% das assinaturas existentes.",
	"Estratégia B (Orientada a Contratos & Casos de Borda): trate explicitamente invariantes, nil/erros de fronteira e adicione/ajuste testes unitários cobrindo a mudança.",
}

// RunWorktreeArena spawns N isolated detached git worktrees in parallel, runs
// Runner in each worktree, verifies every produced patch with VerifierCmd,
// ranks candidates by deterministic verification + diff minimality, and
// optionally applies only a verified winner onto cfg.RepoDir.
func RunWorktreeArena(ctx context.Context, cfg WorktreeArenaConfig) (*WorktreeArenaOutcome, error) {
	if cfg.Runner == nil {
		return nil, fmt.Errorf("CandidateRunner obrigatório para WorktreeArena")
	}
	repoRoot, err := resolveGitTopLevel(ctx, cfg.RepoDir)
	if err != nil {
		return nil, fmt.Errorf("repositório git necessário para Best-of-N em worktrees: %w", err)
	}

	strategies := cfg.Strategies
	if len(strategies) == 0 {
		strategies = append([]string(nil), defaultStrategies...)
	}
	if len(strategies) > 4 {
		strategies = strategies[:4]
	}

	ttl := cfg.PerCandidateTTL
	if ttl <= 0 {
		ttl = 4 * time.Minute
	}

	verifier := strings.TrimSpace(cfg.VerifierCmd)
	if verifier == "" {
		verifier = autoDetectVerifierCmd(repoRoot)
	}

	baseTmp, err := os.MkdirTemp("", "polypod-worktree-arena-*")
	if err != nil {
		return nil, fmt.Errorf("criando diretório temporário da arena: %w", err)
	}
	defer func() {
		_ = pruneWorktrees(repoRoot)
		_ = os.RemoveAll(baseTmp)
	}()

	worktreeDirs := make([]string, len(strategies))
	for i := range strategies {
		wtDir := filepath.Join(baseTmp, fmt.Sprintf("candidate-%d", i+1))
		if addErr := gitRun(ctx, repoRoot, "worktree", "add", "--detach", wtDir, "HEAD"); addErr != nil {
			// Clean up any already-created worktrees before returning.
			for j := 0; j < i; j++ {
				_ = gitRun(context.Background(), repoRoot, "worktree", "remove", "--force", worktreeDirs[j])
			}
			return nil, fmt.Errorf("criando git worktree %d: %w", i+1, addErr)
		}
		worktreeDirs[i] = wtDir
	}
	defer func() {
		for _, wtDir := range worktreeDirs {
			if wtDir != "" {
				_ = gitRun(context.Background(), repoRoot, "worktree", "remove", "--force", wtDir)
			}
		}
	}()

	results := make([]WorktreeCandidateResult, len(strategies))
	var wg sync.WaitGroup
	for i, strat := range strategies {
		wg.Add(1)
		go func(idx int, strategy, wtDir string) {
			defer wg.Done()
			cCtx, cancel := context.WithTimeout(ctx, ttl)
			defer cancel()

			start := time.Now()
			summary, runErr := cfg.Runner(cCtx, idx, wtDir, strategy)
			res := WorktreeCandidateResult{
				Index:       idx,
				Strategy:    strategy,
				Summary:     strings.TrimSpace(summary),
				VerifierCmd: verifier,
				RunnerErr:   runErr,
			}

			patch, files, diffLines := captureWorktreePatch(cCtx, wtDir)
			res.Patch = patch
			res.ChangedFiles = files
			res.DiffLines = diffLines

			if runErr == nil && strings.TrimSpace(patch) != "" {
				if verifier == "" {
					res.VerifierPassed = true
					res.VerifierOutput = "nenhum comando verificador configurado; diff capturado com sucesso"
				} else {
					passed, out := runShellVerifier(cCtx, wtDir, verifier)
					res.VerifierPassed = passed
					res.VerifierOutput = out
				}
			}
			res.Duration = time.Since(start)
			results[idx] = res
		}(i, strat, worktreeDirs[i])
	}
	wg.Wait()

	outcome := &WorktreeArenaOutcome{
		Task:        cfg.Task,
		Candidates:  results,
		WinnerIndex: selectWinningCandidate(results),
	}

	if outcome.WinnerIndex >= 0 && cfg.ApplyWinner {
		winner := results[outcome.WinnerIndex]
		if err := applyPatchToRepo(ctx, repoRoot, winner.Patch); err == nil {
			outcome.WinnerApplied = true
		} else {
			// Archive winner patch so work is never lost if working tree diverged.
			if saved := archiveCandidatePatches(repoRoot, results); len(saved) > 0 {
				outcome.SavedPatches = saved
			}
		}
	} else if outcome.WinnerIndex < 0 {
		outcome.SavedPatches = archiveCandidatePatches(repoRoot, results)
	}

	return outcome, nil
}

func selectWinningCandidate(results []WorktreeCandidateResult) int {
	type ranked struct {
		idx int
		r   WorktreeCandidateResult
	}
	var verified []ranked
	for i, r := range results {
		if r.RunnerErr == nil && r.VerifierPassed && strings.TrimSpace(r.Patch) != "" {
			verified = append(verified, ranked{idx: i, r: r})
		}
	}
	if len(verified) == 0 {
		return -1
	}
	sort.Slice(verified, func(i, j int) bool {
		// Prefer smaller focused diffs when both candidates pass full verification.
		if verified[i].r.DiffLines != verified[j].r.DiffLines {
			return verified[i].r.DiffLines < verified[j].r.DiffLines
		}
		if len(verified[i].r.ChangedFiles) != len(verified[j].r.ChangedFiles) {
			return len(verified[i].r.ChangedFiles) < len(verified[j].r.ChangedFiles)
		}
		return verified[i].r.Duration < verified[j].r.Duration
	})
	return verified[0].idx
}

// FormatWorktreeOutcome produces a clear markdown table + verification digest.
func FormatWorktreeOutcome(o *WorktreeArenaOutcome) string {
	if o == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Arena Best-of-%d em Worktrees Isoladas\n\n", len(o.Candidates))
	b.WriteString("| # | Verificador | Arquivos | Linhas Diff | Tempo | Status |\n")
	b.WriteString("|---|-------------|----------|-------------|-------|--------|\n")
	for i, c := range o.Candidates {
		ver := "—"
		if strings.TrimSpace(c.Patch) != "" {
			if c.VerifierPassed {
				ver = "PASSOU"
			} else {
				ver = "FALHOU"
			}
		}
		status := "sem diff"
		if c.RunnerErr != nil {
			status = "erro"
		} else if i == o.WinnerIndex {
			if o.WinnerApplied {
				status = "VENCEDOR (aplicado)"
			} else {
				status = "VENCEDOR"
			}
		} else if strings.TrimSpace(c.Patch) != "" {
			status = "descartado"
		}
		fmt.Fprintf(&b, "| %d | %s | %d | %d | %s | %s |\n",
			i+1, ver, len(c.ChangedFiles), c.DiffLines, c.Duration.Round(10*time.Millisecond), status)
	}

	if o.WinnerIndex >= 0 {
		w := o.Candidates[o.WinnerIndex]
		fmt.Fprintf(&b, "\n**Candidato vencedor**: #%d (%d arquivo(s): %s)\n",
			o.WinnerIndex+1, len(w.ChangedFiles), strings.Join(w.ChangedFiles, ", "))
		if w.Summary != "" {
			fmt.Fprintf(&b, "\n%s\n", truncate(w.Summary, 800))
		}
	} else {
		b.WriteString("\nNenhum candidato passou simultaneamente na edição e no verificador; a árvore principal permaneceu intacta.\n")
		for i, c := range o.Candidates {
			if c.VerifierOutput != "" && !c.VerifierPassed {
				fmt.Fprintf(&b, "\n### Falha do Candidato #%d\n```\n%s\n```\n", i+1, truncate(c.VerifierOutput, 600))
			}
		}
	}
	if len(o.SavedPatches) > 0 {
		fmt.Fprintf(&b, "\nPatches arquivados para inspeção: %s\n", strings.Join(o.SavedPatches, ", "))
	}
	return strings.TrimSpace(b.String())
}

func resolveGitTopLevel(ctx context.Context, dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v (%s)", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func gitRun(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func pruneWorktrees(repoRoot string) error {
	return gitRun(context.Background(), repoRoot, "worktree", "prune")
}

func captureWorktreePatch(ctx context.Context, wtDir string) (patch string, files []string, diffLines int) {
	// Mark untracked files with intent-to-add so `git diff HEAD` captures newly created files too!
	_ = gitRun(ctx, wtDir, "add", "-N", ".")

	nameCmd := exec.CommandContext(ctx, "git", "diff", "--name-only", "HEAD")
	nameCmd.Dir = wtDir
	if nameOut, err := nameCmd.Output(); err == nil {
		for _, f := range strings.Split(strings.TrimSpace(string(nameOut)), "\n") {
			f = strings.TrimSpace(f)
			if f != "" {
				files = append(files, f)
			}
		}
	}

	diffCmd := exec.CommandContext(ctx, "git", "diff", "--binary", "HEAD")
	diffCmd.Dir = wtDir
	out, err := diffCmd.Output()
	if err != nil || len(out) == 0 {
		return "", files, 0
	}
	patch = string(out)
	for _, line := range strings.Split(patch, "\n") {
		if (strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++")) ||
			(strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---")) {
			diffLines++
		}
	}
	return patch, files, diffLines
}

func runShellVerifier(ctx context.Context, wtDir, command string) (bool, string) {
	vCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// Ensure /usr/local/go/bin is in PATH for Go repositories on Linux hosts.
	env := os.Environ()
	for i, kv := range env {
		if strings.HasPrefix(kv, "PATH=") && !strings.Contains(kv, "/usr/local/go/bin") {
			env[i] = kv + ":/usr/local/go/bin"
			break
		}
	}

	cmd := exec.CommandContext(vCtx, "sh", "-c", command)
	cmd.Dir = wtDir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))
	if len(output) > 4096 {
		output = output[:2048] + "\n...\n" + output[len(output)-2048:]
	}
	return err == nil && vCtx.Err() == nil, output
}

func applyPatchToRepo(ctx context.Context, repoRoot, patch string) error {
	cmd := exec.CommandContext(ctx, "git", "apply", "--3way", "--whitespace=nowarn", "-")
	cmd.Dir = repoRoot
	cmd.Stdin = strings.NewReader(patch)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		// Fallback to plain `git apply` when 3-way index blobs are absent.
		fallback := exec.CommandContext(ctx, "git", "apply", "--whitespace=nowarn", "-")
		fallback.Dir = repoRoot
		fallback.Stdin = strings.NewReader(patch)
		if fbOut, fbErr := fallback.CombinedOutput(); fbErr != nil {
			return fmt.Errorf("%v (%s)", fbErr, strings.TrimSpace(string(fbOut)))
		}
	}
	return nil
}

func archiveCandidatePatches(repoRoot string, results []WorktreeCandidateResult) []string {
	dir := filepath.Join(repoRoot, ".polypod", "arena-candidates")
	var saved []string
	stamp := time.Now().UTC().Format("20060102-150405")
	for i, r := range results {
		if strings.TrimSpace(r.Patch) == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			return saved
		}
		p := filepath.Join(dir, fmt.Sprintf("%s-candidate-%d.patch", stamp, i+1))
		if err := os.WriteFile(p, []byte(r.Patch), 0600); err == nil {
			if rel, relErr := filepath.Rel(repoRoot, p); relErr == nil {
				saved = append(saved, rel)
			} else {
				saved = append(saved, p)
			}
		}
	}
	return saved
}

func autoDetectVerifierCmd(repoRoot string) string {
	if _, err := os.Stat(filepath.Join(repoRoot, "go.mod")); err == nil {
		return "go vet ./... && go test -short ./..."
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "Cargo.toml")); err == nil {
		return "cargo check && cargo test --quiet"
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "pyproject.toml")); err == nil {
		return "python3 -m pytest -q"
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "package.json")); err == nil {
		return "npm test --if-present"
	}
	return ""
}
