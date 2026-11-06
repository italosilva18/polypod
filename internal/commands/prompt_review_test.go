package commands

import (
	"errors"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
)

// withGitDiffFn swaps the package-level gitDiffFn hook for the duration
// of a test and restores the previous value on cleanup.
func withGitDiffFn(t *testing.T, fn func(cwd string, ref string) (string, error)) {
	t.Helper()
	prev := gitDiffFn
	gitDiffFn = fn
	t.Cleanup(func() { gitDiffFn = prev })
}

func TestRegisterPromptReview_RegistersCanonicalNameAndAliases(t *testing.T) {
	reg := command.NewPromptRegistry()
	RegisterPromptReview(reg)

	got, ok := reg.Get("review")
	if !ok || got == nil {
		t.Fatalf("expected /review to be registered")
	}
	if got.Name != "review" {
		t.Errorf("expected Name=review, got %q", got.Name)
	}
	if got.Description == "" {
		t.Errorf("expected non-empty Description")
	}
	if !got.InjectAsUser {
		t.Errorf("expected InjectAsUser=true")
	}

	wantAliases := []string{"rev", "pr-review"}
	for _, a := range wantAliases {
		aliased, ok := reg.Get(a)
		if !ok || aliased == nil {
			t.Errorf("expected alias %q to resolve", a)
			continue
		}
		if aliased.Name != "review" {
			t.Errorf("alias %q resolved to %q, want review", a, aliased.Name)
		}
	}
}

func TestRegisterPromptReview_NilRegistryIsNoOp(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RegisterPromptReview(nil) panicked: %v", r)
		}
	}()
	RegisterPromptReview(nil)
}

func TestReviewPromptCommand_EmptyArgsUsesHEADAndBuildsPrompt(t *testing.T) {
	var gotCWD, gotRef string
	withGitDiffFn(t, func(cwd string, ref string) (string, error) {
		gotCWD = cwd
		gotRef = ref
		return "diff --git a/foo b/foo\n+hello\n", nil
	})

	cmd := reviewPromptCommand()
	res, err := cmd.Expand(command.PromptContext{CWD: "/tmp/fake", Args: ""})
	if err != nil {
		t.Fatalf("Expand returned error: %v", err)
	}
	if res == nil {
		t.Fatalf("Expand returned nil result")
	}
	if gotCWD != "/tmp/fake" {
		t.Errorf("gitDiffFn received cwd=%q, want /tmp/fake", gotCWD)
	}
	if gotRef != "HEAD" {
		t.Errorf("gitDiffFn received ref=%q, want HEAD", gotRef)
	}
	if !strings.Contains(res.PromptText, "You are reviewing code changes") {
		t.Errorf("prompt missing system directive: %q", res.PromptText)
	}
	if !strings.Contains(res.PromptText, "diff --git a/foo") {
		t.Errorf("prompt missing diff body: %q", res.PromptText)
	}
	for _, keyword := range []string{"Correctness", "Security", "Style", "Tests"} {
		if !strings.Contains(res.PromptText, keyword) {
			t.Errorf("prompt missing checklist item %q", keyword)
		}
	}
	if !res.InjectAsUser {
		t.Errorf("expected InjectAsUser=true")
	}
	if res.Display == "" {
		t.Errorf("expected non-empty Display")
	}
}

func TestReviewPromptCommand_RefRangeArgs(t *testing.T) {
	withGitDiffFn(t, func(cwd string, ref string) (string, error) {
		if ref != "main..feature" {
			t.Errorf("unexpected ref %q", ref)
		}
		return "diff body", nil
	})

	cmd := reviewPromptCommand()
	res, err := cmd.Expand(command.PromptContext{Args: "main..feature"})
	if err != nil {
		t.Fatalf("Expand returned error: %v", err)
	}
	if !strings.Contains(res.PromptText, "main..feature") {
		t.Errorf("prompt should mention ref label: %q", res.PromptText)
	}
	if !strings.Contains(res.PromptText, "diff body") {
		t.Errorf("prompt should include diff body")
	}
}

func TestReviewPromptCommand_PRURLSkipsDiff(t *testing.T) {
	called := false
	withGitDiffFn(t, func(cwd string, ref string) (string, error) {
		called = true
		return "should not be called", nil
	})

	cmd := reviewPromptCommand()
	url := "https://github.com/acme/widget/pull/42"
	res, err := cmd.Expand(command.PromptContext{Args: url})
	if err != nil {
		t.Fatalf("Expand returned error: %v", err)
	}
	if called {
		t.Errorf("gitDiffFn should not be called when args is a PR URL")
	}
	if !strings.Contains(res.PromptText, url) {
		t.Errorf("prompt should include PR URL %q, got: %q", url, res.PromptText)
	}
	if strings.Contains(res.PromptText, "```diff") {
		t.Errorf("prompt should not include a diff block for PR URL path")
	}
}

func TestReviewPromptCommand_DiffTruncation(t *testing.T) {
	// Build an oversized diff (> maxReviewDiffChars).
	big := strings.Repeat("a", maxReviewDiffChars+500)
	withGitDiffFn(t, func(cwd string, ref string) (string, error) {
		return big, nil
	})

	cmd := reviewPromptCommand()
	res, err := cmd.Expand(command.PromptContext{})
	if err != nil {
		t.Fatalf("Expand returned error: %v", err)
	}
	if !strings.Contains(res.PromptText, "[diff truncated]") {
		t.Errorf("expected truncation marker in prompt")
	}
	// The truncated diff section must not contain the full original length.
	if strings.Count(res.PromptText, "a") >= len(big) {
		t.Errorf("diff was not actually truncated")
	}
}

func TestReviewPromptCommand_GitErrorPropagates(t *testing.T) {
	withGitDiffFn(t, func(cwd string, ref string) (string, error) {
		return "", errors.New("not a git repository")
	})

	cmd := reviewPromptCommand()
	_, err := cmd.Expand(command.PromptContext{})
	if err == nil {
		t.Fatalf("expected error when gitDiffFn fails")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("expected error to wrap git error, got: %v", err)
	}
}

func TestReviewPromptCommand_EmptyDiffRendersNoChanges(t *testing.T) {
	withGitDiffFn(t, func(cwd string, ref string) (string, error) {
		return "", nil
	})

	cmd := reviewPromptCommand()
	res, err := cmd.Expand(command.PromptContext{})
	if err != nil {
		t.Fatalf("Expand returned error: %v", err)
	}
	if !strings.Contains(res.PromptText, "(no changes)") {
		t.Errorf("expected '(no changes)' marker for empty diff, got: %q", res.PromptText)
	}
}

func TestDefaultGitDiffFn_InNonRepoTempDir(t *testing.T) {
	// Use a fresh temp dir with no .git. If git is not installed the
	// function should return a LookPath error; if git is installed it
	// should return an error because the directory is not a repository.
	// Either way we expect (diff == "", err != nil).
	tmp := t.TempDir()
	out, err := defaultGitDiffFn(tmp, "HEAD")
	if err == nil {
		t.Fatalf("expected error for non-git temp dir, got nil (out=%q)", out)
	}
	if out != "" {
		t.Errorf("expected empty diff on error, got %q", out)
	}
}

func TestLooksLikePRURL(t *testing.T) {
	cases := map[string]bool{
		"":                                       false,
		"main..feature":                          false,
		"HEAD~3":                                 false,
		"https://github.com/acme/widget/pull/42": true,
		"http://github.com/acme/widget/pull/7":   true,
		"https://gitlab.com/acme/w/-/merge_requests/3": true,
		"https://example.com/some/other/page":          false,
	}
	for in, want := range cases {
		if got := looksLikePRURL(in); got != want {
			t.Errorf("looksLikePRURL(%q)=%v, want %v", in, got, want)
		}
	}
}
