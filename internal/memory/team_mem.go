package memory

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TeamMemConfig holds configuration for team-shared memory directories.
type TeamMemConfig struct {
	TeamDir  string // Base directory for all team data
	TeamID   string // Team identifier (e.g., org slug)
	MemberID string // Individual member identifier
}

// TeamMemoryPaths returns the directories where team memory files are stored:
// a shared directory for the whole team and a per-member directory.
//
// Paths returned:
//   - <TeamDir>/<TeamID>/shared/
//   - <TeamDir>/<TeamID>/members/<MemberID>/
func TeamMemoryPaths(cfg TeamMemConfig) []string {
	base := filepath.Join(cfg.TeamDir, cfg.TeamID)
	paths := []string{
		filepath.Join(base, "shared") + string(os.PathSeparator),
	}
	if cfg.MemberID != "" {
		paths = append(paths,
			filepath.Join(base, "members", cfg.MemberID)+string(os.PathSeparator),
		)
	}
	return paths
}

// TeamPromptSection formats a slice of team memory files into a system prompt
// section with markdown headers, suitable for injection into the conversation
// context.
func TeamPromptSection(memories []MemoryFile) string {
	if len(memories) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("# TEAM MEMORY\n\n")

	for i, m := range memories {
		if i > 0 {
			sb.WriteString("\n---\n\n")
		}
		label := filepath.Base(m.Path)
		if m.Source != "" {
			fmt.Fprintf(&sb, "## %s (%s)\n\n", label, m.Source)
		} else {
			fmt.Fprintf(&sb, "## %s\n\n", label)
		}
		sb.WriteString(strings.TrimSpace(m.Content))
		sb.WriteString("\n")
	}
	return sb.String()
}

// MergeTeamMemories combines personal and team memory files, deduplicating
// entries that have identical content (compared by SHA-256 hash). When a
// duplicate is found the personal copy is kept and the team copy is dropped.
func MergeTeamMemories(personal, team []MemoryFile) []MemoryFile {
	seen := make(map[string]bool, len(personal))
	merged := make([]MemoryFile, 0, len(personal)+len(team))

	for _, m := range personal {
		h := contentHash(m.Content)
		seen[h] = true
		merged = append(merged, m)
	}

	for _, m := range team {
		h := contentHash(m.Content)
		if seen[h] {
			continue
		}
		seen[h] = true
		merged = append(merged, m)
	}

	return merged
}

// contentHash returns a hex-encoded SHA-256 of the trimmed content, used for
// deduplication in MergeTeamMemories.
func contentHash(content string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(content)))
	return fmt.Sprintf("%x", h)
}

// WriteTeamMemory writes a memory file into the team shared directory.
// The file is stored as <TeamDir>/<TeamID>/shared/<key>.md.
// Intermediate directories are created automatically.
func WriteTeamMemory(cfg TeamMemConfig, key, value string) error {
	if key == "" {
		return fmt.Errorf("team memory key must not be empty")
	}
	if strings.Contains(key, "..") || strings.ContainsAny(key, "/\\") {
		return fmt.Errorf("team memory key contains invalid characters: %q", key)
	}

	dir := filepath.Join(cfg.TeamDir, cfg.TeamID, "shared")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create team memory dir: %w", err)
	}

	path := filepath.Join(dir, key+".md")
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		return fmt.Errorf("write team memory %s: %w", key, err)
	}
	return nil
}

// ReadTeamMemories reads all .md memory files from the team shared directory.
// Returns an empty slice (not an error) if the directory does not exist.
func ReadTeamMemories(cfg TeamMemConfig) ([]MemoryFile, error) {
	dir := filepath.Join(cfg.TeamDir, cfg.TeamID, "shared")

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read team memory dir: %w", err)
	}

	var files []MemoryFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}

		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue // skip unreadable files
		}

		info, _ := entry.Info()
		var size int64
		if info != nil {
			size = info.Size()
		}

		files = append(files, MemoryFile{
			Path:    path,
			Content: string(data),
			Type:    MemoryGeneric,
			Source:  MemoryFileSource("team_shared"),
			Size:    size,
		})
	}
	return files, nil
}
