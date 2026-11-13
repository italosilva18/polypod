package arena

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGitForTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func TestRunWorktreeArena_SelectsVerifiedWinnerAndCleansWorktrees(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()

	runGitForTest(t, repo, "init")
	runGitForTest(t, repo, "config", "user.email", "poly@test.local")
	runGitForTest(t, repo, "config", "user.name", "Poly Test")

	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/arenatest\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Buggy initial implementation: Add subtracts instead of adding.
	if err := os.WriteFile(filepath.Join(repo, "calc.go"), []byte("package arenatest\n\nfunc Add(a, b int) int {\n\treturn a - b\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "calc_test.go"), []byte("package arenatest\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif got := Add(2, 3); got != 5 {\n\t\tt.Fatalf(\"got %d, want 5\", got)\n\t}\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "initial buggy calc")

	outcome, err := RunWorktreeArena(ctx, WorktreeArenaConfig{
		RepoDir:     repo,
		Task:        "Corrigir função Add em calc.go",
		Strategies:  []string{"Candidato 1 (quebra teste)", "Candidato 2 (corrige bug)"},
		VerifierCmd: "/usr/local/go/bin/go test ./...",
		ApplyWinner: true,
		Runner: func(_ context.Context, index int, worktreeDir string, _ string) (string, error) {
			target := filepath.Join(worktreeDir, "calc.go")
			if index == 0 {
				// Candidate 0 still fails verification.
				err := os.WriteFile(target, []byte("package arenatest\n\nfunc Add(a, b int) int {\n\treturn a * b\n}\n"), 0644)
				return "multiplicou valores", err
			}
			// Candidate 1 fixes the bug cleanly.
			err := os.WriteFile(target, []byte("package arenatest\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n"), 0644)
			return "corrigiu soma a + b", err
		},
	})
	if err != nil {
		t.Fatalf("RunWorktreeArena failed: %v", err)
	}
	if len(outcome.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(outcome.Candidates))
	}
	if outcome.Candidates[0].VerifierPassed {
		t.Fatalf("expected candidate 0 verifier to fail")
	}
	if !outcome.Candidates[1].VerifierPassed {
		t.Fatalf("expected candidate 1 verifier to pass, output:\n%s", outcome.Candidates[1].VerifierOutput)
	}
	if outcome.WinnerIndex != 1 || !outcome.WinnerApplied {
		t.Fatalf("expected WinnerIndex=1 and WinnerApplied=true, got winner=%d applied=%v", outcome.WinnerIndex, outcome.WinnerApplied)
	}

	// Verify main repo received Candidate 1's verified patch.
	updated, err := os.ReadFile(filepath.Join(repo, "calc.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "return a + b") {
		t.Fatalf("expected main repo calc.go updated with winning patch, got:\n%s", string(updated))
	}

	// Verify detached temporary worktrees were pruned cleanly.
	wtCmd := exec.Command("git", "worktree", "list")
	wtCmd.Dir = repo
	wtOut, err := wtCmd.CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(wtOut)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected only main worktree remaining after arena cleanup, got %d:\n%s", len(lines), string(wtOut))
	}

	formatted := FormatWorktreeOutcome(outcome)
	if !strings.Contains(formatted, "VENCEDOR (aplicado)") {
		t.Fatalf("expected formatted summary to highlight applied winner:\n%s", formatted)
	}
}
