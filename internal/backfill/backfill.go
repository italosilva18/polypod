// Package backfill discovers orphaned session files on disk and imports them
// into the main session index so they become visible to the rest of the app.
package backfill

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BackfillConfig holds the parameters needed by a Scanner.
type BackfillConfig struct {
	// DataDir is the root data directory (e.g. ~/.config/polypod).
	DataDir string
	// SessionsDir is the directory that contains session .json files.
	SessionsDir string
	// Logger is used for diagnostics. Falls back to slog.Default() if nil.
	Logger *slog.Logger
}

// OrphanedSession describes a session file found on disk that is not
// registered in the main session index.
type OrphanedSession struct {
	Path         string    `json:"path"`
	ID           string    `json:"id"`
	ModTime      time.Time `json:"mod_time"`
	Size         int64     `json:"size"`
	MessageCount int       `json:"message_count"`
}

// indexEntry mirrors the on-disk shape of an entry in index.json.
type indexEntry struct {
	ID        string    `json:"id"`
	Channel   string    `json:"channel"`
	UserID    string    `json:"user_id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Messages  int       `json:"messages"`
	Tokens    int       `json:"tokens"`
}

// sessionFile is the subset of a persisted session we need for import.
type sessionFile struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	Messages  []rawMessage   `json:"messages"`
	Metadata  map[string]any `json:"metadata"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	Version   int            `json:"version"`
}

type rawMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Scanner walks the sessions directory looking for orphaned files and can
// import them back into the session index.
type Scanner struct {
	cfg    BackfillConfig
	logger *slog.Logger
}

// NewScanner creates a Scanner from the given config.
func NewScanner(cfg BackfillConfig) *Scanner {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Scanner{cfg: cfg, logger: logger}
}

// FindOrphaned walks SessionsDir and returns every .json session file whose
// ID is not present in the main index (DataDir/sessions/index.json).
func (s *Scanner) FindOrphaned() ([]OrphanedSession, error) {
	tracked, err := s.loadTrackedIDs()
	if err != nil {
		return nil, fmt.Errorf("loading session index: %w", err)
	}

	var orphans []OrphanedSession

	err = filepath.WalkDir(s.cfg.SessionsDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			s.logger.Warn("caminho inacessivel", "path", path, "err", walkErr)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.ToLower(filepath.Ext(d.Name())) != ".json" {
			return nil
		}
		// Skip the index file itself.
		if d.Name() == "index.json" {
			return nil
		}

		id := strings.TrimSuffix(d.Name(), ".json")
		if tracked[id] {
			return nil
		}

		info, infoErr := d.Info()
		if infoErr != nil {
			s.logger.Warn("stat falhou", "path", path, "err", infoErr)
			return nil
		}

		orphans = append(orphans, OrphanedSession{
			Path:         path,
			ID:           id,
			ModTime:      info.ModTime(),
			Size:         info.Size(),
			MessageCount: CountMessages(path),
		})
		return nil
	})

	if err != nil {
		return orphans, fmt.Errorf("percorrendo diretorio de sessoes: %w", err)
	}
	return orphans, nil
}

// Import reads an orphaned session file and registers it in the session
// index so it becomes visible to the rest of the application.
func (s *Scanner) Import(session OrphanedSession) error {
	data, err := os.ReadFile(session.Path)
	if err != nil {
		return fmt.Errorf("lendo arquivo de sessao %s: %w", session.Path, err)
	}

	var sf sessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return fmt.Errorf("parseando sessao %s: %w", session.ID, err)
	}

	// Use the ID from the file content if present, otherwise fall back to the filename-derived ID.
	id := sf.ID
	if id == "" {
		id = session.ID
	}

	title := sf.Title
	if title == "" {
		title = id
	}

	entry := indexEntry{
		ID:        id,
		Title:     title,
		Messages:  len(sf.Messages),
		CreatedAt: sf.CreatedAt,
		UpdatedAt: sf.UpdatedAt,
	}

	// Fill in zero times from file metadata.
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = session.ModTime
	}
	if entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = session.ModTime
	}

	if err := s.appendToIndex(entry); err != nil {
		return fmt.Errorf("registrando sessao %s no indice: %w", id, err)
	}

	s.logger.Info("sessao importada", "id", id, "messages", entry.Messages, "path", session.Path)
	return nil
}

// ImportAll discovers all orphaned sessions and imports each one.
// Returns the number of successfully imported sessions and any errors
// encountered along the way.
func (s *Scanner) ImportAll() (imported int, errors []error) {
	orphans, err := s.FindOrphaned()
	if err != nil {
		errors = append(errors, err)
		return 0, errors
	}

	for _, o := range orphans {
		if err := s.Import(o); err != nil {
			s.logger.Warn("falha ao importar", "path", o.Path, "err", err)
			errors = append(errors, err)
			continue
		}
		imported++
	}
	return imported, errors
}

// Preview returns a human-readable summary (in Portuguese) of orphaned
// sessions that would be imported.
func (s *Scanner) Preview() string {
	orphans, err := s.FindOrphaned()
	if err != nil {
		return fmt.Sprintf("Erro ao buscar sessoes orfas: %v", err)
	}

	if len(orphans) == 0 {
		return "Nenhuma sessao orfa encontrada."
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Encontrada(s) %d sessao(oes) orfa(s) para importar:\n\n", len(orphans))

	var totalSize int64
	var totalMsgs int
	for _, o := range orphans {
		fmt.Fprintf(&sb, "  - %s  (%d mensagens, %s, modificado em %s)\n",
			o.ID,
			o.MessageCount,
			formatSize(o.Size),
			o.ModTime.Format("02/01/2006 15:04"),
		)
		totalSize += o.Size
		totalMsgs += o.MessageCount
	}

	fmt.Fprintf(&sb, "\nTotal: %d sessao(oes), %d mensagens, %s",
		len(orphans), totalMsgs, formatSize(totalSize))

	return sb.String()
}

// CountMessages performs a quick count of message objects (entries with a
// "role" field) inside a session JSON file.
func CountMessages(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}

	// Try parsing as an object with a "messages" array (standard session format).
	var obj struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if json.Unmarshal(data, &obj) == nil && len(obj.Messages) > 0 {
		count := 0
		for _, raw := range obj.Messages {
			var m map[string]any
			if json.Unmarshal(raw, &m) == nil {
				if _, ok := m["role"]; ok {
					count++
				}
			}
		}
		return count
	}

	// Try parsing as a top-level array of messages.
	var arr []json.RawMessage
	if json.Unmarshal(data, &arr) == nil {
		count := 0
		for _, raw := range arr {
			var m map[string]any
			if json.Unmarshal(raw, &m) == nil {
				if _, ok := m["role"]; ok {
					count++
				}
			}
		}
		return count
	}

	return 0
}

// ---------------------------------------------------------------------------
// internal helpers
// ---------------------------------------------------------------------------

// loadTrackedIDs reads the session index and returns a set of known IDs.
func (s *Scanner) loadTrackedIDs() (map[string]bool, error) {
	indexPath := filepath.Join(s.cfg.DataDir, "sessions", "index.json")

	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]bool), nil
		}
		return nil, err
	}

	var entries []indexEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parseando index.json: %w", err)
	}

	ids := make(map[string]bool, len(entries))
	for _, e := range entries {
		ids[e.ID] = true
	}
	return ids, nil
}

// appendToIndex adds an entry to the session index file, creating it if
// necessary. If the ID already exists it is updated in place.
func (s *Scanner) appendToIndex(entry indexEntry) error {
	indexPath := filepath.Join(s.cfg.DataDir, "sessions", "index.json")

	if err := os.MkdirAll(filepath.Dir(indexPath), 0755); err != nil {
		return err
	}

	var entries []indexEntry
	data, err := os.ReadFile(indexPath)
	if err == nil {
		// best-effort parse; ignore corrupt index (start fresh).
		_ = json.Unmarshal(data, &entries)
	}

	// Upsert.
	found := false
	for i, e := range entries {
		if e.ID == entry.ID {
			entries[i] = entry
			found = true
			break
		}
	}
	if !found {
		entries = append(entries, entry)
	}

	out, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(indexPath, out, 0644)
}

// formatSize returns a human-friendly file size string.
func formatSize(bytes int64) string {
	switch {
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
