package git

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// FileStatus represents the status of a single file in the working tree.
type FileStatus struct {
	Path   string
	Status string // "M", "A", "D", "R", "C", "??"
	Staged bool
}

// Commit represents a single git commit.
type Commit struct {
	Hash    string
	Subject string
	Author  string
	Date    time.Time
}

// GetCurrentBranch returns the name of the current git branch.
func GetCurrentBranch(dir ...string) (string, error) {
	return runGitFor(dir, "rev-parse", "--abbrev-ref", "HEAD")
}

// GetStatus returns parsed git status output for all files.
func GetStatus(dir ...string) ([]FileStatus, error) {
	out, err := runGitFor(dir, "status", "--porcelain", "-uall")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}

	var statuses []FileStatus
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 4 {
			continue
		}

		// Porcelain format: XY PATH
		// X = index status, Y = worktree status
		indexStatus := line[0]
		worktreeStatus := line[1]
		path := strings.TrimSpace(line[3:])

		// Handle renames: "R  old -> new"
		if strings.Contains(path, " -> ") {
			parts := strings.SplitN(path, " -> ", 2)
			path = parts[1]
		}

		if indexStatus != ' ' && indexStatus != '?' {
			statuses = append(statuses, FileStatus{
				Path:   path,
				Status: string(indexStatus),
				Staged: true,
			})
		}

		if worktreeStatus != ' ' && worktreeStatus != '?' {
			statuses = append(statuses, FileStatus{
				Path:   path,
				Status: string(worktreeStatus),
				Staged: false,
			})
		}

		// Untracked files
		if indexStatus == '?' && worktreeStatus == '?' {
			statuses = append(statuses, FileStatus{
				Path:   path,
				Status: "??",
				Staged: false,
			})
		}
	}

	return statuses, nil
}

// GetDiff returns the diff output. If staged is true, returns only staged changes.
func GetDiff(staged bool, dir ...string) (string, error) {
	if staged {
		return runGitFor(dir, "diff", "--cached")
	}
	return runGitFor(dir, "diff")
}

// GetDiffStat returns a short stat summary of the diff.
func GetDiffStat(staged bool, dir ...string) (string, error) {
	if staged {
		return runGitFor(dir, "diff", "--cached", "--stat")
	}
	return runGitFor(dir, "diff", "--stat")
}

// GetRecentCommits returns the n most recent commits with structured data.
func GetRecentCommits(n int, dir ...string) ([]Commit, error) {
	if n <= 0 {
		n = 10
	}

	// Use a unique separator to parse fields reliably
	const sep = "<<|>>"
	format := fmt.Sprintf("%%H%s%%s%s%%an%s%%aI", sep, sep, sep)
	out, err := runGitFor(dir, "log", fmt.Sprintf("-%d", n), fmt.Sprintf("--format=%s", format))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}

	var commits []Commit
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, sep, 4)
		if len(parts) != 4 {
			continue
		}

		date, _ := time.Parse(time.RFC3339, parts[3])
		commits = append(commits, Commit{
			Hash:    parts[0],
			Subject: parts[1],
			Author:  parts[2],
			Date:    date,
		})
	}

	return commits, nil
}

// IsInsideRepo returns true if the current directory is inside a git repository.
func IsInsideRepo() bool {
	_, err := runGit("rev-parse", "--is-inside-work-tree")
	return err == nil
}

// IsInsideRepoAt returns true if the given directory is inside a git repository.
func IsInsideRepoAt(dir string) bool {
	_, err := runGitAt(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil
}

// GetRepoRoot returns the absolute path to the repository root.
func GetRepoRoot() (string, error) {
	return runGit("rev-parse", "--show-toplevel")
}

// GetRepoRootAt returns the absolute path to the repository root from the given directory.
func GetRepoRootAt(dir string) (string, error) {
	return runGitAt(dir, "rev-parse", "--show-toplevel")
}

// GetRemoteURL returns the URL of the origin remote.
func GetRemoteURL() (string, error) {
	out, err := runGit("remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("no remote 'origin' configured: %w", err)
	}
	return out, nil
}

// StageFiles adds the specified file paths to the git staging area.
func StageFiles(paths []string, dir ...string) error {
	if len(paths) == 0 {
		return fmt.Errorf("no files specified to stage")
	}
	args := append([]string{"add", "--"}, paths...)
	_, err := runGitFor(dir, args...)
	return err
}

// StageAll stages all changes (tracked and untracked).
func StageAll(dir ...string) error {
	_, err := runGitFor(dir, "add", "-A")
	return err
}

// CreateCommit creates a new commit with the given message.
// Files must be staged before calling this.
func CreateCommit(message string, dir ...string) error {
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("commit message cannot be empty")
	}
	_, err := runGitFor(dir, "commit", "-m", message)
	return err
}

// HasUncommittedChanges returns true if there are any uncommitted changes
// (staged or unstaged, excluding untracked files).
func HasUncommittedChanges() bool {
	out, err := runGit("status", "--porcelain")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) != ""
}

// HasStagedChanges returns true if there are staged changes ready to commit.
func HasStagedChanges(dir ...string) bool {
	_, err := runGitFor(dir, "diff", "--cached", "--quiet")
	return err != nil // non-zero exit = there are staged changes
}

// GetBranches returns all local branch names.
func GetBranches() ([]string, error) {
	out, err := runGit("branch", "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	var branches []string
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		b := strings.TrimSpace(scanner.Text())
		if b != "" {
			branches = append(branches, b)
		}
	}
	return branches, nil
}

// GetFilesDiff returns the list of files changed between HEAD and working tree.
func GetFilesDiff(staged bool, dir ...string) ([]string, error) {
	args := []string{"diff", "--name-only"}
	if staged {
		args = append(args, "--cached")
	}
	out, err := runGitFor(dir, args...)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	var files []string
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		f := strings.TrimSpace(scanner.Text())
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// GetCommitCount returns the total number of commits in the current branch.
func GetCommitCount() (int, error) {
	out, err := runGit("rev-list", "--count", "HEAD")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// FormatStatus returns a human-readable formatted status string.
func FormatStatus(statuses []FileStatus) string {
	if len(statuses) == 0 {
		return "Working tree clean."
	}

	var staged, unstaged, untracked []FileStatus
	for _, s := range statuses {
		switch {
		case s.Status == "??":
			untracked = append(untracked, s)
		case s.Staged:
			staged = append(staged, s)
		default:
			unstaged = append(unstaged, s)
		}
	}

	var sb strings.Builder
	if len(staged) > 0 {
		sb.WriteString("Staged changes:\n")
		for _, s := range staged {
			sb.WriteString(fmt.Sprintf("  %s %s\n", statusLabel(s.Status), s.Path))
		}
	}
	if len(unstaged) > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("Unstaged changes:\n")
		for _, s := range unstaged {
			sb.WriteString(fmt.Sprintf("  %s %s\n", statusLabel(s.Status), s.Path))
		}
	}
	if len(untracked) > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("Untracked files:\n")
		for _, s := range untracked {
			sb.WriteString(fmt.Sprintf("  %s\n", s.Path))
		}
	}

	return sb.String()
}

// FormatCommits returns a human-readable formatted list of commits.
func FormatCommits(commits []Commit) string {
	if len(commits) == 0 {
		return "No commits found."
	}

	var sb strings.Builder
	for _, c := range commits {
		sb.WriteString(fmt.Sprintf("%s %s %s: %s\n",
			c.Hash[:minInt(7, len(c.Hash))],
			c.Date.Format("2006-01-02"),
			c.Author,
			c.Subject,
		))
	}
	return sb.String()
}

// statusLabel maps short status codes to descriptive labels.
func statusLabel(s string) string {
	switch s {
	case "M":
		return "modified:"
	case "A":
		return "added:   "
	case "D":
		return "deleted: "
	case "R":
		return "renamed: "
	case "C":
		return "copied:  "
	case "U":
		return "unmerged:"
	default:
		return s + ":       "
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// runGit executes a git command and returns trimmed stdout.
func runGit(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), execTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(out))
	if ctx.Err() == context.DeadlineExceeded {
		return result, fmt.Errorf("git command timed out after %v", execTimeout)
	}
	if err != nil {
		if result != "" {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), result)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return result, nil
}

// runGitAt executes a git command in the specified directory.
func runGitAt(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), execTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(out))
	if ctx.Err() == context.DeadlineExceeded {
		return result, fmt.Errorf("git command timed out after %v", execTimeout)
	}
	if err != nil {
		if result != "" {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), result)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return result, nil
}

// runGitFor preserves the process-cwd behavior for legacy callers while
// allowing a conversation to target its own Git worktree explicitly.
func runGitFor(dir []string, args ...string) (string, error) {
	if len(dir) > 0 && dir[0] != "" {
		return runGitAt(dir[0], args...)
	}
	return runGit(args...)
}
