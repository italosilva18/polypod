package prstatus

import (
	"os"
	"strings"
	"testing"
)

func TestFormatFooter_Nil(t *testing.T) {
	if got := FormatFooter(nil); got != "" {
		t.Errorf("FormatFooter(nil) = %q, want empty string", got)
	}
}

func TestFormatFooter_Approved(t *testing.T) {
	pr := &PRInfo{Number: 42, ReviewDecision: "APPROVED", State: "OPEN"}
	got := FormatFooter(pr)

	if !strings.Contains(got, "PR #42") {
		t.Errorf("FormatFooter() = %q, want to contain 'PR #42'", got)
	}
	if !strings.Contains(got, "\033[32m") {
		t.Errorf("FormatFooter() = %q, want to contain green ANSI color \\033[32m", got)
	}
	if !strings.HasSuffix(got, "\033[0m") {
		t.Errorf("FormatFooter() = %q, want to end with reset sequence \\033[0m", got)
	}
}

func TestFormatFooter_ChangesRequested(t *testing.T) {
	pr := &PRInfo{Number: 7, ReviewDecision: "CHANGES_REQUESTED", State: "OPEN"}
	got := FormatFooter(pr)

	if !strings.Contains(got, "\033[31m") {
		t.Errorf("FormatFooter() = %q, want red ANSI color \\033[31m", got)
	}
	if !strings.Contains(got, "PR #7") {
		t.Errorf("FormatFooter() = %q, want to contain 'PR #7'", got)
	}
}

func TestFormatFooter_ReviewRequired(t *testing.T) {
	pr := &PRInfo{Number: 11, ReviewDecision: "REVIEW_REQUIRED", State: "OPEN"}
	got := FormatFooter(pr)

	if !strings.Contains(got, "\033[33m") {
		t.Errorf("FormatFooter() = %q, want yellow ANSI color \\033[33m", got)
	}
}

func TestFormatFooter_Merged(t *testing.T) {
	pr := &PRInfo{Number: 100, State: "MERGED"}
	got := FormatFooter(pr)

	if !strings.Contains(got, "\033[35m") {
		t.Errorf("FormatFooter() = %q, want purple ANSI color \\033[35m for merged PR", got)
	}
	if !strings.Contains(got, "PR #100") {
		t.Errorf("FormatFooter() = %q, want to contain 'PR #100'", got)
	}
}

func TestFormatFooter_Draft(t *testing.T) {
	pr := &PRInfo{Number: 55, State: "OPEN", IsDraft: true}
	got := FormatFooter(pr)

	if !strings.Contains(got, "\033[90m") {
		t.Errorf("FormatFooter() = %q, want gray ANSI color \\033[90m for draft PR", got)
	}
}

func TestFormatFooter_ApprovedBeatsMerged(t *testing.T) {
	// APPROVED review decision should take precedence over MERGED state
	// because of switch-case ordering in FormatFooter.
	pr := &PRInfo{Number: 8, ReviewDecision: "APPROVED", State: "MERGED"}
	got := FormatFooter(pr)

	if !strings.Contains(got, "\033[32m") {
		t.Errorf("FormatFooter() = %q, want green (APPROVED precedence over MERGED)", got)
	}
	if strings.Contains(got, "\033[35m") {
		t.Errorf("FormatFooter() = %q, must not contain purple when APPROVED wins", got)
	}
}

func TestFormatFooter_DefaultGrayWhenUnknown(t *testing.T) {
	pr := &PRInfo{Number: 1, State: "OPEN", ReviewDecision: ""}
	got := FormatFooter(pr)

	if !strings.Contains(got, "\033[90m") {
		t.Errorf("FormatFooter() = %q, want default gray for unknown state", got)
	}
}

func TestFormatDetailed_Nil(t *testing.T) {
	got := FormatDetailed(nil)
	want := "Nenhum PR aberto para a branch atual."
	if got != want {
		t.Errorf("FormatDetailed(nil) = %q, want %q", got, want)
	}
}

func TestFormatDetailed_FullPR(t *testing.T) {
	pr := &PRInfo{
		Number:         123,
		Title:          "Add new feature",
		State:          "OPEN",
		ReviewDecision: "APPROVED",
		URL:            "https://github.com/example/repo/pull/123",
		IsDraft:        false,
	}
	got := FormatDetailed(pr)

	wantSubstrings := []string{
		"## PR #123",
		"**Titulo**: Add new feature",
		"**Estado**: OPEN",
		"**Review**: APPROVED",
		"**URL**: https://github.com/example/repo/pull/123",
	}
	for _, s := range wantSubstrings {
		if !strings.Contains(got, s) {
			t.Errorf("FormatDetailed() output missing %q.\nFull output:\n%s", s, got)
		}
	}

	if strings.Contains(got, "**Draft**: sim") {
		t.Errorf("FormatDetailed() should not include Draft line when IsDraft is false, got:\n%s", got)
	}
}

func TestFormatDetailed_DraftPR(t *testing.T) {
	pr := &PRInfo{
		Number:         9,
		Title:          "WIP: refactor",
		State:          "OPEN",
		ReviewDecision: "REVIEW_REQUIRED",
		URL:            "https://example.com/pr/9",
		IsDraft:        true,
	}
	got := FormatDetailed(pr)

	if !strings.Contains(got, "**Draft**: sim") {
		t.Errorf("FormatDetailed() should include Draft marker for draft PR, got:\n%s", got)
	}
	if !strings.Contains(got, "## PR #9") {
		t.Errorf("FormatDetailed() should include PR number header, got:\n%s", got)
	}
}

func TestGetCurrentPR_NoGhCLI(t *testing.T) {
	// Save and restore PATH so we can simulate gh not being installed.
	origPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", origPath) })

	if err := os.Setenv("PATH", ""); err != nil {
		t.Fatalf("failed to set empty PATH: %v", err)
	}

	pr, err := GetCurrentPR()
	if err != nil {
		t.Errorf("GetCurrentPR() should return nil error when gh is missing, got %v", err)
	}
	if pr != nil {
		t.Errorf("GetCurrentPR() should return nil PR when gh is missing, got %+v", pr)
	}
}
