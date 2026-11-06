package session

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// RestoredSession holds the data loaded from a persisted session file.
type RestoredSession struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Messages  []Message       `json:"messages"`
	Metadata  SessionMetadata `json:"metadata"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	Version   int             `json:"version"`
}

// SessionMetadata stores auxiliary session data that's persisted alongside
// the conversation transcript.
type SessionMetadata struct {
	AgentName    string `json:"agent_name,omitempty"`
	AgentColor   string `json:"agent_color,omitempty"`
	AgentSetting string `json:"agent_setting,omitempty"`
	CustomTitle  string `json:"custom_title,omitempty"`
	Tag          string `json:"tag,omitempty"`
	Mode         string `json:"mode,omitempty"`
	GitBranch    string `json:"git_branch,omitempty"`
	ProjectRoot  string `json:"project_root,omitempty"`
	LastPrompt   string `json:"last_prompt,omitempty"`
}

// CurrentSessionVersion is bumped when the on-disk format changes.
const CurrentSessionVersion = 2

// SessionRestorer handles loading, validating, migrating, and cleaning up
// persisted sessions.
type SessionRestorer struct {
	sessionsDir string
	logger      *slog.Logger
}

// NewSessionRestorer creates a restorer that works with the given sessions
// directory (e.g. ~/.config/polypod/sessions/).
func NewSessionRestorer(sessionsDir string, logger *slog.Logger) *SessionRestorer {
	if logger == nil {
		logger = slog.Default()
	}
	return &SessionRestorer{
		sessionsDir: sessionsDir,
		logger:      logger,
	}
}

// RestoreSession loads a session from its persisted JSON file. Returns nil
// if the session file does not exist.
func (r *SessionRestorer) RestoreSession(sessionID string) (*RestoredSession, error) {
	path := filepath.Join(r.sessionsDir, sessionID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading session %s: %w", sessionID, err)
	}

	var sess RestoredSession
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("parsing session %s: %w", sessionID, err)
	}

	// Migrate if needed.
	if sess.Version < CurrentSessionVersion {
		if err := r.MigrateSessionFormat(&sess); err != nil {
			r.logger.Warn("migration failed, returning raw session",
				"session", sessionID, "version", sess.Version, "error", err)
		}
	}

	return &sess, nil
}

// ValidateRestoredSession checks a restored session for stale data.
// Returns a list of warnings (empty = everything is fine).
func (r *SessionRestorer) ValidateRestoredSession(sess *RestoredSession) []string {
	var warnings []string

	if sess.ID == "" {
		warnings = append(warnings, "session has no ID")
	}
	if len(sess.Messages) == 0 {
		warnings = append(warnings, "session has no messages")
	}

	// Check for stale session (older than 7 days of inactivity).
	if !sess.UpdatedAt.IsZero() && time.Since(sess.UpdatedAt) > 7*24*time.Hour {
		warnings = append(warnings, fmt.Sprintf(
			"session last updated %s ago — data may be stale",
			time.Since(sess.UpdatedAt).Truncate(time.Hour),
		))
	}

	// Validate that project root still exists.
	if sess.Metadata.ProjectRoot != "" {
		if _, err := os.Stat(sess.Metadata.ProjectRoot); err != nil {
			warnings = append(warnings, fmt.Sprintf(
				"project root no longer exists: %s", sess.Metadata.ProjectRoot))
		}
	}

	// Validate message order.
	for i := 1; i < len(sess.Messages); i++ {
		// Consecutive assistant messages without a user message in between is suspect.
		if sess.Messages[i].Role == "assistant" && sess.Messages[i-1].Role == "assistant" {
			warnings = append(warnings, fmt.Sprintf(
				"consecutive assistant messages at index %d-%d", i-1, i))
			break
		}
	}

	// Check version mismatch.
	if sess.Version > CurrentSessionVersion {
		warnings = append(warnings, fmt.Sprintf(
			"session version %d is newer than supported %d",
			sess.Version, CurrentSessionVersion))
	}

	return warnings
}

// MigrateSessionFormat updates a session from an older format version to the
// current version. Modifies the session in place.
func (r *SessionRestorer) MigrateSessionFormat(sess *RestoredSession) error {
	if sess.Version >= CurrentSessionVersion {
		return nil
	}

	r.logger.Info("migrating session format",
		"session", sess.ID,
		"from_version", sess.Version,
		"to_version", CurrentSessionVersion,
	)

	// Version 0 → 1: Add metadata struct.
	if sess.Version < 1 {
		if sess.Metadata.CustomTitle == "" && sess.Title != "" {
			sess.Metadata.CustomTitle = sess.Title
		}
		sess.Version = 1
	}

	// Version 1 → 2: Normalize message roles and add timestamps.
	if sess.Version < 2 {
		for i := range sess.Messages {
			role := sess.Messages[i].Role
			switch role {
			case "user", "assistant", "system":
				// valid — no change
			default:
				r.logger.Warn("unknown message role during migration",
					"session", sess.ID, "index", i, "role", role)
				sess.Messages[i].Role = "system"
			}
		}
		sess.Version = 2
	}

	return nil
}

// CleanupStaleSessions removes sessions that haven't been updated within
// maxAge. Returns the number of sessions removed.
func (r *SessionRestorer) CleanupStaleSessions(maxAge time.Duration) (int, error) {
	entries, err := os.ReadDir(r.sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("reading sessions dir: %w", err)
	}

	removed := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		path := filepath.Join(r.sessionsDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}

		if time.Since(info.ModTime()) <= maxAge {
			continue
		}

		// Double-check by reading the file's UpdatedAt.
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var sess struct {
			UpdatedAt time.Time `json:"updated_at"`
		}
		if err := json.Unmarshal(data, &sess); err == nil && !sess.UpdatedAt.IsZero() {
			if time.Since(sess.UpdatedAt) <= maxAge {
				continue
			}
		}

		if err := os.Remove(path); err != nil {
			r.logger.Warn("failed to remove stale session", "path", path, "error", err)
			continue
		}
		r.logger.Info("removed stale session", "file", entry.Name(),
			"age", time.Since(info.ModTime()).Truncate(time.Hour))
		removed++
	}

	return removed, nil
}

// SaveSession persists a session to its JSON file.
func (r *SessionRestorer) SaveSession(sess *RestoredSession) error {
	if err := os.MkdirAll(r.sessionsDir, 0700); err != nil {
		return fmt.Errorf("creating sessions dir: %w", err)
	}

	sess.Version = CurrentSessionVersion
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling session %s: %w", sess.ID, err)
	}

	path := filepath.Join(r.sessionsDir, sess.ID+".json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing session %s: %w", sess.ID, err)
	}
	return nil
}

// ListRestorable returns the IDs of all sessions in the sessions directory.
func (r *SessionRestorer) ListRestorable() ([]string, error) {
	entries, err := os.ReadDir(r.sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading sessions dir: %w", err)
	}

	var ids []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := entry.Name()[:len(entry.Name())-5] // strip .json
		ids = append(ids, id)
	}
	return ids, nil
}
