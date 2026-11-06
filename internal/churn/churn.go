// Package churn computes per-file git churn and identifies hotspots.
package churn

import (
	"bufio"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Hotspot represents a file's churn metrics.
type Hotspot struct {
	Path    string
	Commits int
	Authors int
	Adds    int
	Dels    int
	LOC     int
	Score   float64
}

// Config controls the churn analysis.
type Config struct {
	RepoDir     string
	SinceDays   int // 0 = all history
	IncludeGlob []string
	ExcludeGlob []string
	MinCommits  int // filter out files with fewer commits
}

// DefaultConfig returns sensible defaults.
func DefaultConfig(repoDir string) Config {
	return Config{
		RepoDir:    repoDir,
		SinceDays:  90,
		MinCommits: 1,
		ExcludeGlob: []string{
			"vendor/", "node_modules/", ".git/", "dist/", "build/",
		},
	}
}

// Compute analyzes churn for files in a git repo.
func Compute(cfg Config) ([]Hotspot, error) {
	// Get commits with stats via git log
	commits, err := getCommits(cfg)
	if err != nil {
		return nil, err
	}

	// Aggregate per file
	filesMap := make(map[string]*fileAggregate)
	for _, commit := range commits {
		for _, change := range commit.Changes {
			if shouldExclude(change.Path, cfg) {
				continue
			}

			agg, ok := filesMap[change.Path]
			if !ok {
				agg = &fileAggregate{
					Path:    change.Path,
					Authors: make(map[string]bool),
				}
				filesMap[change.Path] = agg
			}
			agg.Commits++
			agg.Adds += change.Adds
			agg.Dels += change.Dels
			agg.Authors[commit.Author] = true
		}
	}

	// Get current LOC for each file
	var hotspots []Hotspot
	for _, agg := range filesMap {
		if agg.Commits < cfg.MinCommits {
			continue
		}
		loc := getCurrentLOC(cfg.RepoDir, agg.Path)
		score := computeScore(agg.Commits, len(agg.Authors), agg.Adds+agg.Dels, loc)
		hotspots = append(hotspots, Hotspot{
			Path:    agg.Path,
			Commits: agg.Commits,
			Authors: len(agg.Authors),
			Adds:    agg.Adds,
			Dels:    agg.Dels,
			LOC:     loc,
			Score:   score,
		})
	}

	// Sort by score desc
	sort.Slice(hotspots, func(i, j int) bool {
		return hotspots[i].Score > hotspots[j].Score
	})

	return hotspots, nil
}

// TopN returns the top N hotspots from a list.
func TopN(hotspots []Hotspot, n int) []Hotspot {
	if len(hotspots) <= n {
		return hotspots
	}
	return hotspots[:n]
}

// ByPath filters hotspots matching a path substring.
func ByPath(hotspots []Hotspot, pathSubstring string) []Hotspot {
	var result []Hotspot
	for _, h := range hotspots {
		if strings.Contains(h.Path, pathSubstring) {
			result = append(result, h)
		}
	}
	return result
}

type commit struct {
	Hash    string
	Author  string
	Date    time.Time
	Changes []fileChange
}

type fileChange struct {
	Path string
	Adds int
	Dels int
}

type fileAggregate struct {
	Path    string
	Commits int
	Adds    int
	Dels    int
	Authors map[string]bool
}

// getCommits runs git log and parses commit data with file stats.
func getCommits(cfg Config) ([]commit, error) {
	args := []string{
		"-C", cfg.RepoDir,
		"log",
		"--numstat",
		"--format=COMMIT|%H|%an|%at",
		"--no-merges",
	}

	if cfg.SinceDays > 0 {
		since := fmt.Sprintf("%d days ago", cfg.SinceDays)
		args = append(args, "--since="+since)
	}

	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git log failed: %w", err)
	}

	var commits []commit
	var current *commit

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "COMMIT|") {
			if current != nil {
				commits = append(commits, *current)
			}
			parts := strings.SplitN(line, "|", 4)
			if len(parts) < 4 {
				continue
			}
			ts, _ := strconv.ParseInt(parts[3], 10, 64)
			current = &commit{
				Hash:   parts[1],
				Author: parts[2],
				Date:   time.Unix(ts, 0),
			}
			continue
		}

		if current == nil {
			continue
		}

		// numstat: "adds\tdels\tpath"
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		adds, _ := strconv.Atoi(parts[0])
		dels, _ := strconv.Atoi(parts[1])
		current.Changes = append(current.Changes, fileChange{
			Path: parts[2],
			Adds: adds,
			Dels: dels,
		})
	}

	if current != nil {
		commits = append(commits, *current)
	}

	return commits, nil
}

// getCurrentLOC returns the current line count of a file (0 if missing).
func getCurrentLOC(repoDir, path string) int {
	cmd := exec.Command("git", "-C", repoDir, "show", "HEAD:"+path)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	return strings.Count(string(out), "\n")
}

// shouldExclude returns true if the path should be skipped.
func shouldExclude(path string, cfg Config) bool {
	for _, ex := range cfg.ExcludeGlob {
		if strings.Contains(path, ex) {
			return true
		}
	}
	if len(cfg.IncludeGlob) > 0 {
		for _, inc := range cfg.IncludeGlob {
			if strings.Contains(path, inc) {
				return false
			}
		}
		return true
	}
	return false
}

// computeScore combines churn metrics into a single hotspot score.
func computeScore(commits, authors, churn, loc int) float64 {
	if commits == 0 {
		return 0
	}
	// Base: commit count
	score := float64(commits)
	// Bonus for author diversity (many authors = more risk)
	score *= (1.0 + float64(authors)*0.1)
	// Bonus for churn magnitude
	score *= (1.0 + float64(churn)/1000.0)
	// Normalize by LOC (bigger files can handle more churn)
	if loc > 0 {
		score /= float64(loc) / 100.0
	}
	return score
}

// FormatReport returns a human-friendly report of hotspots.
func FormatReport(hotspots []Hotspot, top int) string {
	if len(hotspots) == 0 {
		return "No hotspots found."
	}

	if top > 0 && len(hotspots) > top {
		hotspots = hotspots[:top]
	}

	var sb strings.Builder
	sb.WriteString("HOTSPOT REPORT\n")
	sb.WriteString("==============\n\n")
	sb.WriteString(fmt.Sprintf("%-50s %8s %8s %8s %8s %8s\n",
		"FILE", "COMMITS", "AUTHORS", "ADDS", "DELS", "SCORE"))
	sb.WriteString(strings.Repeat("-", 96) + "\n")

	for _, h := range hotspots {
		path := h.Path
		if len(path) > 50 {
			path = "..." + path[len(path)-47:]
		}
		sb.WriteString(fmt.Sprintf("%-50s %8d %8d %8d %8d %8.1f\n",
			path, h.Commits, h.Authors, h.Adds, h.Dels, h.Score))
	}
	return sb.String()
}

// Stats returns aggregate statistics.
type Stats struct {
	TotalFiles   int
	TotalCommits int
	TotalAdds    int
	TotalDels    int
	AvgCommits   float64
	MaxCommits   int
}

// ComputeStats returns aggregate statistics for hotspots.
func ComputeStats(hotspots []Hotspot) Stats {
	if len(hotspots) == 0 {
		return Stats{}
	}

	stats := Stats{
		TotalFiles: len(hotspots),
	}
	for _, h := range hotspots {
		stats.TotalCommits += h.Commits
		stats.TotalAdds += h.Adds
		stats.TotalDels += h.Dels
		if h.Commits > stats.MaxCommits {
			stats.MaxCommits = h.Commits
		}
	}
	stats.AvgCommits = float64(stats.TotalCommits) / float64(len(hotspots))
	return stats
}
