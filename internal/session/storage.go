package session

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// SessionStorage is the interface for persisting and retrieving sessions.
type SessionStorage interface {
	// Save persists a session.
	Save(sess *RestoredSession) error
	// Load retrieves a session by ID.
	Load(sessionID string) (*RestoredSession, error)
	// Delete removes a session by ID.
	Delete(sessionID string) error
	// List returns session summaries, ordered by most recently updated.
	List(filter SessionFilter) ([]SessionSummary, error)
	// Search finds sessions whose content matches the query string.
	Search(query string) ([]SessionSummary, error)
	// Export writes a session to the given path in the specified format.
	Export(sessionID, destPath, format string) error
}

// SessionFilter constrains which sessions List returns.
type SessionFilter struct {
	Channel  string
	Tag      string
	Since    time.Time
	MaxCount int
}

// SessionSummary is a lightweight view of a session for listing.
type SessionSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Tag       string    `json:"tag,omitempty"`
	Channel   string    `json:"channel,omitempty"`
	Messages  int       `json:"messages"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	GitBranch string    `json:"git_branch,omitempty"`
	Preview   string    `json:"preview,omitempty"`
}

// FileSystemStorage implements SessionStorage using JSON files.
type FileSystemStorage struct {
	mu     sync.RWMutex
	dir    string
	logger *slog.Logger
}

// NewFileSystemStorage creates a storage backed by JSON files in the given
// directory (e.g. ~/.config/polypod/sessions/).
func NewFileSystemStorage(dir string, logger *slog.Logger) *FileSystemStorage {
	if logger == nil {
		logger = slog.Default()
	}
	os.MkdirAll(dir, 0700)
	return &FileSystemStorage{dir: dir, logger: logger}
}

func (fs *FileSystemStorage) sessionPath(id string) string {
	return filepath.Join(fs.dir, id+".json")
}

// Save persists a session to disk.
func (fs *FileSystemStorage) Save(sess *RestoredSession) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if err := os.MkdirAll(fs.dir, 0700); err != nil {
		return fmt.Errorf("creating sessions dir: %w", err)
	}

	sess.Version = CurrentSessionVersion
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling session: %w", err)
	}

	path := fs.sessionPath(sess.ID)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing session file: %w", err)
	}
	return nil
}

// Load reads a session from disk by ID. Returns nil, nil if not found.
func (fs *FileSystemStorage) Load(sessionID string) (*RestoredSession, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	path := fs.sessionPath(sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading session: %w", err)
	}

	var sess RestoredSession
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("parsing session: %w", err)
	}
	return &sess, nil
}

// Delete removes a session file.
func (fs *FileSystemStorage) Delete(sessionID string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	path := fs.sessionPath(sessionID)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("deleting session: %w", err)
	}
	fs.logger.Info("deleted session", "id", sessionID)
	return nil
}

// List returns summaries of sessions matching the filter, newest first.
func (fs *FileSystemStorage) List(filter SessionFilter) ([]SessionSummary, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	entries, err := os.ReadDir(fs.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading sessions dir: %w", err)
	}

	var summaries []SessionSummary
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		path := filepath.Join(fs.dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var sess RestoredSession
		if err := json.Unmarshal(data, &sess); err != nil {
			continue
		}

		// Apply filters.
		if filter.Channel != "" && sess.Metadata.Tag != filter.Channel {
			// Check if we have channel info — for file-based storage we match on tag.
		}
		if filter.Tag != "" && sess.Metadata.Tag != filter.Tag {
			continue
		}
		if !filter.Since.IsZero() && sess.UpdatedAt.Before(filter.Since) {
			continue
		}

		preview := ""
		if len(sess.Messages) > 0 {
			first := sess.Messages[0].Content
			if len(first) > 100 {
				first = first[:100] + "..."
			}
			preview = first
		}

		summaries = append(summaries, SessionSummary{
			ID:        sess.ID,
			Title:     sess.Title,
			Tag:       sess.Metadata.Tag,
			Messages:  len(sess.Messages),
			CreatedAt: sess.CreatedAt,
			UpdatedAt: sess.UpdatedAt,
			GitBranch: sess.Metadata.GitBranch,
			Preview:   preview,
		})
	}

	// Sort by UpdatedAt descending.
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt)
	})

	if filter.MaxCount > 0 && len(summaries) > filter.MaxCount {
		summaries = summaries[:filter.MaxCount]
	}

	return summaries, nil
}

// Search finds sessions whose messages, title, tag, or metadata match the
// query string (case-insensitive substring match).
func (fs *FileSystemStorage) Search(query string) ([]SessionSummary, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	queryLower := strings.ToLower(query)

	entries, err := os.ReadDir(fs.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading sessions dir: %w", err)
	}

	var results []SessionSummary
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		path := filepath.Join(fs.dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var sess RestoredSession
		if err := json.Unmarshal(data, &sess); err != nil {
			continue
		}

		if matchesQuery(queryLower, &sess) {
			preview := extractSearchPreview(queryLower, &sess)
			results = append(results, SessionSummary{
				ID:        sess.ID,
				Title:     sess.Title,
				Tag:       sess.Metadata.Tag,
				Messages:  len(sess.Messages),
				CreatedAt: sess.CreatedAt,
				UpdatedAt: sess.UpdatedAt,
				GitBranch: sess.Metadata.GitBranch,
				Preview:   preview,
			})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].UpdatedAt.After(results[j].UpdatedAt)
	})

	return results, nil
}

// Export writes a session to destPath in the specified format ("json" or "markdown").
func (fs *FileSystemStorage) Export(sessionID, destPath, format string) error {
	sess, err := fs.Load(sessionID)
	if err != nil {
		return err
	}
	if sess == nil {
		return fmt.Errorf("session %s not found", sessionID)
	}

	switch format {
	case "json", "":
		data, err := json.MarshalIndent(sess, "", "  ")
		if err != nil {
			return fmt.Errorf("marshaling session: %w", err)
		}
		return os.WriteFile(destPath, data, 0644)

	case "markdown", "md":
		return exportMarkdown(sess, destPath)

	default:
		return fmt.Errorf("unsupported export format: %s", format)
	}
}

func exportMarkdown(sess *RestoredSession, destPath string) error {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\n\n", sess.Title)
	fmt.Fprintf(&sb, "**Session ID:** %s  \n", sess.ID)
	fmt.Fprintf(&sb, "**Created:** %s  \n", sess.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(&sb, "**Updated:** %s  \n\n", sess.UpdatedAt.Format(time.RFC3339))

	if sess.Metadata.Tag != "" {
		fmt.Fprintf(&sb, "**Tag:** %s  \n", sess.Metadata.Tag)
	}
	if sess.Metadata.GitBranch != "" {
		fmt.Fprintf(&sb, "**Branch:** %s  \n", sess.Metadata.GitBranch)
	}
	sb.WriteString("\n---\n\n")

	for _, msg := range sess.Messages {
		role := strings.Title(msg.Role)
		fmt.Fprintf(&sb, "### %s\n\n%s\n\n", role, msg.Content)
	}

	return os.WriteFile(destPath, []byte(sb.String()), 0644)
}

func matchesQuery(queryLower string, sess *RestoredSession) bool {
	if strings.Contains(strings.ToLower(sess.Title), queryLower) {
		return true
	}
	if strings.Contains(strings.ToLower(sess.Metadata.CustomTitle), queryLower) {
		return true
	}
	if strings.Contains(strings.ToLower(sess.Metadata.Tag), queryLower) {
		return true
	}
	if strings.Contains(strings.ToLower(sess.Metadata.GitBranch), queryLower) {
		return true
	}
	if strings.Contains(strings.ToLower(sess.Metadata.LastPrompt), queryLower) {
		return true
	}
	// Search message content.
	for _, msg := range sess.Messages {
		if msg.Role != "user" && msg.Role != "assistant" {
			continue
		}
		if strings.Contains(strings.ToLower(msg.Content), queryLower) {
			return true
		}
	}
	return false
}

func extractSearchPreview(queryLower string, sess *RestoredSession) string {
	// Find the first message that contains the query.
	for _, msg := range sess.Messages {
		content := strings.ToLower(msg.Content)
		idx := strings.Index(content, queryLower)
		if idx >= 0 {
			// Extract surrounding context.
			start := idx - 40
			if start < 0 {
				start = 0
			}
			end := idx + len(queryLower) + 60
			if end > len(msg.Content) {
				end = len(msg.Content)
			}
			preview := msg.Content[start:end]
			if start > 0 {
				preview = "..." + preview
			}
			if end < len(msg.Content) {
				preview = preview + "..."
			}
			return preview
		}
	}
	// Fallback to first message.
	if len(sess.Messages) > 0 {
		first := sess.Messages[0].Content
		if len(first) > 100 {
			first = first[:100] + "..."
		}
		return first
	}
	return ""
}
