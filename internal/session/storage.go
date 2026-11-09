package session

import (
	"crypto/sha256"
	"encoding/hex"
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
	if err := os.MkdirAll(dir, 0700); err != nil {
		logger.Warn("cannot create sessions directory", "dir", dir, "error", err)
	} else if err := inheritSessionDirOwner(dir); err != nil {
		logger.Warn("cannot set sessions directory owner", "dir", dir, "error", err)
	}
	return &FileSystemStorage{dir: dir, logger: logger}
}

func (fs *FileSystemStorage) sessionPath(id string) string {
	return filepath.Join(fs.dir, id+".json")
}

// Save persists a session to disk.
func (fs *FileSystemStorage) Save(sess *RestoredSession) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if sess == nil || !validSessionID(sess.ID) {
		return fmt.Errorf("invalid session")
	}

	if err := os.MkdirAll(fs.dir, 0700); err != nil {
		return fmt.Errorf("creating sessions dir: %w", err)
	}
	if err := inheritSessionDirOwner(fs.dir); err != nil {
		return fmt.Errorf("setting sessions dir owner: %w", err)
	}
	previous, _, err := fs.readDocument(sess.ID)
	if err != nil {
		fs.logger.Warn("cannot read previous session manifest", "session", sess.ID, "error", err)
	}

	sess.Version = CurrentSessionVersion
	pageDir := fs.pageDir(sess.ID)
	if err := os.MkdirAll(pageDir, 0700); err != nil {
		return fmt.Errorf("creating session pages dir: %w", err)
	}
	if err := inheritSessionDirOwner(pageDir); err != nil {
		return fmt.Errorf("setting session pages dir owner: %w", err)
	}
	manifest := sessionManifest{
		Format: pagedFormat, ID: sess.ID, Title: sess.Title,
		Metadata: sess.Metadata, CreatedAt: sess.CreatedAt,
		UpdatedAt: sess.UpdatedAt, Version: sess.Version,
		MessageCount: len(sess.Messages),
	}
	if len(sess.Messages) > 0 {
		manifest.Preview = previewContent(sess.Messages[0].Content)
	}
	for start := 0; start < len(sess.Messages); start += messagePageSize {
		end := min(start+messagePageSize, len(sess.Messages))
		data, err := json.Marshal(sess.Messages[start:end])
		if err != nil {
			return fmt.Errorf("marshaling session page: %w", err)
		}
		digest := sha256.Sum256(data)
		name := hex.EncodeToString(digest[:]) + ".page"
		path := filepath.Join(pageDir, name)
		info, err := os.Stat(path)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("checking existing session page: %w", err)
		}
		if os.IsNotExist(err) || info.Size() != int64(len(data)) {
			if err := atomicWriteFile(path, data); err != nil {
				return fmt.Errorf("writing session page: %w", err)
			}
		}
		manifest.Pages = append(manifest.Pages, messagePageRef{File: name, Count: end - start})
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling session manifest: %w", err)
	}
	if err := atomicWriteFile(fs.sessionPath(sess.ID), data); err != nil {
		return fmt.Errorf("writing session manifest: %w", err)
	}
	fs.retireOldPages(sess.ID, previous, manifest.Pages)
	fs.pruneUnreferencedPages(sess.ID, manifest.Pages)
	return nil
}

// Load reads a session from disk by ID. Returns nil, nil if not found.
func (fs *FileSystemStorage) Load(sessionID string) (*RestoredSession, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	manifest, legacy, err := fs.readDocument(sessionID)
	if err != nil || (manifest == nil && legacy == nil) {
		return nil, err
	}
	if legacy != nil {
		return legacy, nil
	}
	sess := manifest.session()
	sess.Messages = make([]Message, 0, manifest.MessageCount)
	for _, ref := range manifest.Pages {
		messages, err := fs.readPage(sessionID, ref)
		if err != nil {
			return nil, err
		}
		sess.Messages = append(sess.Messages, messages...)
	}
	return sess, nil
}

// Delete removes a session file.
func (fs *FileSystemStorage) Delete(sessionID string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if !validSessionID(sessionID) {
		return fmt.Errorf("invalid session ID %q", sessionID)
	}

	path := fs.sessionPath(sessionID)
	if err := os.Remove(path); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("deleting session: %w", err)
		}
	}
	if err := os.RemoveAll(fs.pageDir(sessionID)); err != nil {
		return fmt.Errorf("deleting session pages: %w", err)
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
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" || entry.Name() == "index.json" {
			continue
		}

		id := strings.TrimSuffix(entry.Name(), ".json")
		manifest, legacy, err := fs.readDocument(id)
		if err != nil || (manifest == nil && legacy == nil) {
			continue
		}
		var header *SessionHeader
		if legacy != nil {
			header = headerFromLegacy(legacy)
		} else {
			header = manifest.header()
		}

		// Apply filters.
		if filter.Channel != "" && header.Metadata.Tag != "" && header.Metadata.Tag != filter.Channel {
			continue
		}
		if filter.Tag != "" && header.Metadata.Tag != filter.Tag {
			continue
		}
		if !filter.Since.IsZero() && header.UpdatedAt.Before(filter.Since) {
			continue
		}

		summaries = append(summaries, SessionSummary{
			ID:        header.ID,
			Title:     header.Title,
			Tag:       header.Metadata.Tag,
			Messages:  header.MessageCount,
			CreatedAt: header.CreatedAt,
			UpdatedAt: header.UpdatedAt,
			GitBranch: header.Metadata.GitBranch,
			Preview:   header.Preview,
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
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" || entry.Name() == "index.json" {
			continue
		}

		id := strings.TrimSuffix(entry.Name(), ".json")
		manifest, legacy, err := fs.readDocument(id)
		if err != nil || (manifest == nil && legacy == nil) {
			continue
		}
		var header *SessionHeader
		preview := ""
		matched := false
		if legacy != nil {
			header = headerFromLegacy(legacy)
			matched = matchesQuery(queryLower, legacy)
			if matched {
				preview = extractSearchPreview(queryLower, legacy)
			}
		} else {
			header = manifest.header()
			metadata := manifest.session()
			matched = matchesQuery(queryLower, metadata)
			if matched {
				preview = manifest.Preview
			} else {
				for _, ref := range manifest.Pages {
					messages, err := fs.readPage(id, ref)
					if err != nil {
						break
					}
					page := &RestoredSession{Messages: messages}
					if matchesQuery(queryLower, page) {
						matched = true
						preview = extractSearchPreview(queryLower, page)
						break
					}
				}
			}
		}
		if matched {
			results = append(results, SessionSummary{
				ID:        header.ID,
				Title:     header.Title,
				Tag:       header.Metadata.Tag,
				Messages:  header.MessageCount,
				CreatedAt: header.CreatedAt,
				UpdatedAt: header.UpdatedAt,
				GitBranch: header.Metadata.GitBranch,
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
