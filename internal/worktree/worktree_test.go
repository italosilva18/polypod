package worktree

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setupRepo initializes a bare-minimum git repo inside t.TempDir(), chdir's into
// it, and creates an initial commit so that worktree operations have something
// to branch from. Returns the absolute path of the repo.
func setupRepo(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git nao encontrado no PATH; pulando testes de worktree")
	}

	repo := t.TempDir()

	// git init
	runGit(t, repo, "init", "-q", "-b", "main")
	// Minimal identity so commits work in CI-like environments.
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")
	runGit(t, repo, "config", "commit.gpgsign", "false")

	// Create an initial commit so HEAD exists and worktree add can branch off.
	readme := filepath.Join(repo, "README.md")
	if err := os.WriteFile(readme, []byte("# test\n"), 0644); err != nil {
		t.Fatalf("escrever README: %v", err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-q", "-m", "initial")

	// Change into the repo for the duration of the test. All Manager methods
	// run git in the process cwd, so this is required. t.Cleanup restores the
	// previous cwd to avoid cross-test contamination.
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir para repo: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prev)
	})

	return repo
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s falhou: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

// newTestManager builds a Manager whose baseDir lives under t.TempDir() but
// still contains the literal "polypod-worktrees" so that Manager.List's path
// filter matches.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	base := filepath.Join(t.TempDir(), "polypod-worktrees")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewManager(base, logger)
}

func TestNewManager_DefaultBaseDir(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := NewManager("", logger)
	if m == nil {
		t.Fatal("NewManager retornou nil")
	}
	wantSuffix := "polypod-worktrees"
	if !strings.HasSuffix(m.baseDir, wantSuffix) {
		t.Errorf("baseDir padrao = %q, esperado terminar em %q", m.baseDir, wantSuffix)
	}
	if _, err := os.Stat(m.baseDir); err != nil {
		t.Errorf("NewManager deveria criar baseDir: %v", err)
	}
}

func TestNewManager_CustomBaseDir(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "my-worktrees")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := NewManager(custom, logger)
	if m.baseDir != custom {
		t.Errorf("baseDir = %q, esperado %q", m.baseDir, custom)
	}
	if _, err := os.Stat(custom); err != nil {
		t.Errorf("baseDir custom deveria existir: %v", err)
	}
}

func TestManagerCreate_Success(t *testing.T) {
	setupRepo(t)
	m := newTestManager(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	wt, err := m.Create(ctx, "feature-a")
	if err != nil {
		t.Fatalf("Create falhou: %v", err)
	}
	if wt == nil {
		t.Fatal("worktree retornada e nil")
	}
	if wt.Name != "feature-a" {
		t.Errorf("Name = %q, esperado %q", wt.Name, "feature-a")
	}
	if wt.Branch != "polypod/feature-a" {
		t.Errorf("Branch = %q, esperado %q", wt.Branch, "polypod/feature-a")
	}
	if wt.Path == "" {
		t.Error("Path vazio")
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Errorf("path do worktree nao existe: %v", err)
	}
	// O README do commit inicial deve estar presente no worktree.
	if _, err := os.Stat(filepath.Join(wt.Path, "README.md")); err != nil {
		t.Errorf("worktree nao contem arquivo do commit inicial: %v", err)
	}
	if wt.Created.IsZero() {
		t.Error("Created nao foi setado")
	}
}

func TestManagerCreate_NotAGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git nao encontrado")
	}

	// Diretorio temporario SEM git init.
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	m := newTestManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wt, err := m.Create(ctx, "x")
	if err == nil {
		t.Fatalf("esperado erro 'nao e um repositorio git', obtido wt=%+v", wt)
	}
	if !strings.Contains(err.Error(), "repositorio git") {
		t.Errorf("mensagem de erro inesperada: %v", err)
	}
}

func TestManagerCreate_DuplicateName(t *testing.T) {
	setupRepo(t)
	m := newTestManager(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if _, err := m.Create(ctx, "dup"); err != nil {
		t.Fatalf("primeiro Create falhou: %v", err)
	}
	if _, err := m.Create(ctx, "dup"); err == nil {
		t.Fatal("segundo Create com nome duplicado deveria falhar")
	}
}

func TestManagerList_ReturnsCreated(t *testing.T) {
	setupRepo(t)
	m := newTestManager(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	names := []string{"alpha", "beta"}
	for _, n := range names {
		if _, err := m.Create(ctx, n); err != nil {
			t.Fatalf("Create(%s): %v", n, err)
		}
	}

	list, err := m.List(ctx)
	if err != nil {
		t.Fatalf("List falhou: %v", err)
	}
	if len(list) < 2 {
		t.Fatalf("List retornou %d worktrees, esperado >= 2: %+v", len(list), list)
	}

	seen := map[string]bool{}
	for _, w := range list {
		seen[w.Name] = true
		if !strings.HasPrefix(w.Branch, "polypod/") {
			t.Errorf("branch %q nao tem prefixo polypod/", w.Branch)
		}
	}
	for _, n := range names {
		if !seen[n] {
			t.Errorf("worktree %q nao aparece na lista: %+v", n, list)
		}
	}
}

func TestManagerRemove(t *testing.T) {
	setupRepo(t)
	m := newTestManager(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	wt, err := m.Create(ctx, "temp")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := m.Remove(ctx, "temp"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	// O path do worktree deve ter sido removido pelo git.
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Errorf("path do worktree ainda existe apos Remove: err=%v", err)
	}

	// O branch polypod/temp deve ter sido deletado.
	cmd := exec.Command("git", "branch", "--list", "polypod/temp")
	out, _ := cmd.CombinedOutput()
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("branch ainda existe apos Remove: %q", string(out))
	}

	// List nao deve mais conter 'temp'.
	list, err := m.List(ctx)
	if err != nil {
		t.Fatalf("List pos-Remove: %v", err)
	}
	for _, w := range list {
		if w.Name == "temp" {
			t.Errorf("worktree 'temp' ainda presente em List: %+v", w)
		}
	}
}

func TestManagerRemove_Idempotent(t *testing.T) {
	setupRepo(t)
	m := newTestManager(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Remove de um nome que nunca foi criado nao deve retornar erro
	// (a implementacao ignora falhas do git propositalmente).
	if err := m.Remove(ctx, "nao-existe"); err != nil {
		t.Errorf("Remove de worktree inexistente deveria ser no-op, obtido: %v", err)
	}
}

func TestManagerMergeBack(t *testing.T) {
	repo := setupRepo(t)
	m := newTestManager(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	wt, err := m.Create(ctx, "merge-me")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Faz um commit no worktree para ter algo para fazer merge.
	extra := filepath.Join(wt.Path, "extra.txt")
	if err := os.WriteFile(extra, []byte("hello\n"), 0644); err != nil {
		t.Fatalf("escrever extra.txt: %v", err)
	}
	runGit(t, wt.Path, "add", "extra.txt")
	runGit(t, wt.Path, "commit", "-q", "-m", "add extra")

	// Merge de volta na main (cwd e o repo principal, ja em main).
	if err := m.MergeBack(ctx, "merge-me", "main"); err != nil {
		t.Fatalf("MergeBack: %v", err)
	}

	// Apos o merge, extra.txt deve existir no repo principal.
	if _, err := os.Stat(filepath.Join(repo, "extra.txt")); err != nil {
		t.Errorf("arquivo do worktree nao foi merge-ado para main: %v", err)
	}
}

func TestIsGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git nao encontrado")
	}

	prev, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(prev) })

	// Em um diretorio sem git, deve retornar false.
	plain := t.TempDir()
	if err := os.Chdir(plain); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if isGitRepo() {
		t.Error("isGitRepo() = true em diretorio sem git, esperado false")
	}

	// Apos git init, deve retornar true.
	runGit(t, plain, "init", "-q")
	if !isGitRepo() {
		t.Error("isGitRepo() = false em diretorio com git init, esperado true")
	}
}
