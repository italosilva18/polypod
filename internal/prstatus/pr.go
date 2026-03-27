package prstatus

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// PRInfo holds pull request information.
type PRInfo struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	State          string `json:"state"`
	ReviewDecision string `json:"reviewDecision"`
	URL            string `json:"url"`
	IsDraft        bool   `json:"isDraft"`
}

// GetCurrentPR checks if the current branch has an open PR using `gh` CLI.
func GetCurrentPR() (*PRInfo, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, nil // gh not installed, silent
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "gh", "pr", "view", "--json",
		"number,title,state,reviewDecision,url,isDraft")
	out, err := cmd.Output()
	if err != nil {
		return nil, nil // no PR for current branch
	}

	var pr PRInfo
	if err := json.Unmarshal(out, &pr); err != nil {
		return nil, nil
	}
	if pr.Number == 0 {
		return nil, nil
	}

	return &pr, nil
}

// FormatFooter formats PR info for the status bar.
func FormatFooter(pr *PRInfo) string {
	if pr == nil {
		return ""
	}

	color := "\033[90m" // gray (draft)
	switch {
	case pr.ReviewDecision == "APPROVED":
		color = "\033[32m" // green
	case pr.ReviewDecision == "CHANGES_REQUESTED":
		color = "\033[31m" // red
	case pr.ReviewDecision == "REVIEW_REQUIRED":
		color = "\033[33m" // yellow
	case pr.State == "MERGED":
		color = "\033[35m" // purple
	case pr.IsDraft:
		color = "\033[90m" // gray
	}

	return fmt.Sprintf("%sPR #%d\033[0m", color, pr.Number)
}

// FormatDetailed shows full PR info.
func FormatDetailed(pr *PRInfo) string {
	if pr == nil {
		return "Nenhum PR aberto para a branch atual."
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## PR #%d\n\n", pr.Number)
	fmt.Fprintf(&sb, "**Titulo**: %s\n", pr.Title)
	fmt.Fprintf(&sb, "**Estado**: %s\n", pr.State)
	fmt.Fprintf(&sb, "**Review**: %s\n", pr.ReviewDecision)
	if pr.IsDraft {
		sb.WriteString("**Draft**: sim\n")
	}
	fmt.Fprintf(&sb, "**URL**: %s\n", pr.URL)

	return sb.String()
}
