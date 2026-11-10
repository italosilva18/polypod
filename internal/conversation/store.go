package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"
)

type snapshotSource uint8

const (
	sourceUnknown snapshotSource = iota
	sourceMemory
	sourceSQLite
	sourceDisk
	sourcePostgres
)

// sessionSlot is one session handle. Message bodies are loaded on demand so
// startup does not retain every transcript.
type sessionSlot struct {
	mu     sync.Mutex
	sess   *Session
	loaded bool
	source snapshotSource
	// diskName is the YAML base name in dataDir. Empty uses the canonical path.
	diskName string
	// alsoJournal records a backend whose body was replaced in the index.
	// Startup still journals that body before any later save overwrites it.
	alsoJournal []snapshotSource
	// Superseded YAML copies may contain unique turns, including stale .yml.
	alsoJournalDiskNames []string
}

// sessionHeader lets startup identify YAML sessions without constructing a
// second copy of their message bodies. Migration still reads every body once.
type sessionHeader struct {
	ID        string    `yaml:"id"`
	Channel   string    `yaml:"channel"`
	UserID    string    `yaml:"user_id"`
	CreatedAt time.Time `yaml:"created_at"`
	UpdatedAt time.Time `yaml:"updated_at"`
}

// Store persists sessions. Supports PostgreSQL, SQLite, YAML file, or in-memory only.
type Store struct {
	journalInitErr error
	journalMu      sync.Mutex
	journalEntries []JournalEntry         // bodies for memory or YAML-only journals
	journalIDs     map[string]bool        // YAML or memory stores only; SQLite uses its unique index
	journalForks   map[string]journalFork // branch ancestry for YAML or memory stores
	journalLast    uint64
	journalLoaded  bool
	mu             sync.RWMutex
	sessions       map[string]*sessionSlot
	pool           *pgxpool.Pool // nil = no postgres
	sqliteDB       *sql.DB       // nil = no sqlite
	dataDir        string        // non-empty = YAML file persistence
}

// NewStore creates a new session store.
// - pool != nil → PostgreSQL backend
// - sqliteDB != nil → SQLite backend
// - dataDir != "" → YAML file persistence
// - all nil/empty → in-memory only
func NewStore(pool *pgxpool.Pool, sqliteDB *sql.DB, dataDir string) *Store {
	s := &Store{
		sessions: make(map[string]*sessionSlot),
		pool:     pool,
		sqliteDB: sqliteDB,
		dataDir:  dataDir,
	}

	// Index sessions without retaining message bodies. Postgres stays
	// on demand, matching the previous startup path.
	if sqliteDB != nil {
		if err := s.loadIndexFromSQLite(); err != nil {
			s.journalInitErr = fmt.Errorf("indexing SQLite sessions: %w", err)
			return s
		}
	}
	if dataDir != "" {
		if err := s.loadIndexFromDisk(); err != nil {
			s.journalInitErr = fmt.Errorf("indexing YAML sessions: %w", err)
			return s
		}
	}
	s.migrateSnapshots(context.Background())
	return s
}

func sessionKey(channel, userID string) string {
	return channel + ":" + userID
}

// Get retrieves or creates a session. The same channel and user keep one pointer.
func (s *Store) Get(ctx context.Context, channel, userID string) (*Session, error) {
	if s.journalInitErr != nil {
		return nil, s.journalInitErr
	}
	key := sessionKey(channel, userID)

	s.mu.Lock()
	sl, ok := s.sessions[key]
	if !ok {
		sl = &sessionSlot{sess: &Session{ID: key, Channel: channel, UserID: userID}}
		s.sessions[key] = sl
	}
	s.mu.Unlock()
	return s.materialize(ctx, sl)
}

// Save persists a session (to DB, SQLite, and/or YAML file).
func (s *Store) Save(ctx context.Context, sess *Session) error {
	if s.journalInitErr != nil {
		return s.journalInitErr
	}
	if sess == nil {
		return fmt.Errorf("saving session: session is nil")
	}
	if err := s.ensureLoaded(ctx, sess); err != nil {
		return err
	}
	// Journal a backend that lost the working copy before this save replaces
	// its row. A failed read must not continue into the snapshot write.
	if err := s.journalSuperseded(ctx, sess); err != nil {
		return err
	}
	if err := s.appendJournal(ctx, sess); err != nil {
		return err
	}
	if s.pool != nil {
		if err := s.saveToDB(ctx, sess); err != nil {
			return err
		}
	}

	if s.sqliteDB != nil {
		if err := s.saveToSQLite(ctx, sess); err != nil {
			return err
		}
	}

	if s.dataDir != "" {
		if err := s.saveToDisk(sess); err != nil {
			return err
		}
	}

	return nil
}

// ListSessions returns all active session IDs without loading message bodies.
func (s *Store) ListSessions() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	return ids
}

// GetAllSessions returns every session, loading each body. ListSessions is the
// path that stays cheap when only identifiers are needed.
func (s *Store) GetAllSessions() []*Session {
	s.mu.RLock()
	slots := make([]*sessionSlot, 0, len(s.sessions))
	for _, sl := range s.sessions {
		slots = append(slots, sl)
	}
	s.mu.RUnlock()
	sessions := make([]*Session, 0, len(slots))
	for _, sl := range slots {
		sess, err := s.materialize(context.Background(), sl)
		if err != nil || sess == nil {
			continue
		}
		sessions = append(sessions, sess)
	}
	return sessions
}

func (s *Store) ensureLoaded(ctx context.Context, sess *Session) error {
	s.mu.RLock()
	sl := s.sessions[sess.ID]
	s.mu.RUnlock()
	if sl == nil {
		return nil
	}
	sl.mu.Lock()
	same := sl.sess == sess
	loaded := sl.loaded
	sl.mu.Unlock()
	if !same || loaded {
		return nil
	}
	_, err := s.materialize(ctx, sl)
	return err
}

func (s *Store) materialize(ctx context.Context, sl *sessionSlot) (*Session, error) {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	if sl.loaded {
		return sl.sess, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch sl.source {
	case sourceDisk, sourceSQLite, sourcePostgres:
		loaded, err := s.readSnapshot(ctx, sl.sess, sl.source, sl.diskName)
		if err != nil {
			return nil, err
		}
		if loaded == nil {
			return nil, fmt.Errorf("loading session %s: snapshot not found", sl.sess.ID)
		}
		sl.sess.Messages = loaded.Messages
		sl.sess.CreatedAt = loaded.CreatedAt
		sl.sess.UpdatedAt = loaded.UpdatedAt
		if loaded.Channel != "" {
			sl.sess.Channel = loaded.Channel
		}
		if loaded.UserID != "" {
			sl.sess.UserID = loaded.UserID
		}
		if err := stampSessionIDs(sl.sess); err != nil {
			return nil, err
		}
		sl.loaded = true
		return sl.sess, nil
	default:
		if s.pool != nil {
			loaded, err := s.loadFromDB(ctx, sl.sess.ID, sl.sess.Channel, sl.sess.UserID)
			if err != nil {
				return nil, err
			}
			if loaded != nil {
				if err := stampSessionIDs(loaded); err != nil {
					return nil, err
				}
				sl.sess = loaded
				sl.source = sourcePostgres
				sl.loaded = true
				return sl.sess, nil
			}
		}
		if s.sqliteDB != nil {
			loaded, err := s.loadFromSQLite(ctx, sl.sess.ID, sl.sess.Channel, sl.sess.UserID)
			if err != nil {
				return nil, err
			}
			if loaded != nil {
				if err := stampSessionIDs(loaded); err != nil {
					return nil, err
				}
				sl.sess = loaded
				sl.source = sourceSQLite
				sl.loaded = true
				return sl.sess, nil
			}
		}
		fresh := NewSession(sl.sess.ID, sl.sess.Channel, sl.sess.UserID)
		sl.sess = fresh
		sl.source = sourceMemory
		sl.loaded = true
		return sl.sess, nil
	}
}

// migrateSnapshots journals existing snapshots one at a time, then drops the
// temporary bodies. Legacy messages gain stable IDs inside the journal.
func (s *Store) migrateSnapshots(ctx context.Context) {
	s.mu.RLock()
	slots := make([]*sessionSlot, 0, len(s.sessions))
	for _, sl := range s.sessions {
		slots = append(slots, sl)
	}
	s.mu.RUnlock()
	for _, sl := range slots {
		if err := ctx.Err(); err != nil {
			s.journalInitErr = err
			return
		}
		sl.mu.Lock()
		header := sl.sess
		source := sl.source
		diskName := sl.diskName
		extra := append([]snapshotSource(nil), sl.alsoJournal...)
		extraDisk := append([]string(nil), sl.alsoJournalDiskNames...)
		sl.mu.Unlock()
		if header == nil {
			s.journalInitErr = fmt.Errorf("journaling indexed session: missing header")
			return
		}
		if len(extra) > 0 {
			if err := s.journalSources(ctx, header, extra, diskName); err != nil {
				s.journalInitErr = err
				return
			}
		}
		if err := s.journalDiskCopies(ctx, header, extraDisk); err != nil {
			s.journalInitErr = err
			return
		}
		sl.mu.Lock()
		sl.alsoJournal = nil
		sl.alsoJournalDiskNames = nil
		sl.mu.Unlock()
		snap, err := s.readSnapshot(ctx, header, source, diskName)
		if err != nil {
			s.journalInitErr = fmt.Errorf("journaling indexed session %s: %w", header.ID, err)
			return
		}
		if snap == nil {
			s.journalInitErr = fmt.Errorf("journaling indexed session %s: snapshot not found", header.ID)
			return
		}
		if err := s.appendJournal(ctx, snap); err != nil {
			s.journalInitErr = err
			return
		}
	}
}

// journalSuperseded journals snapshots that lost the index race. alsoJournal
// is cleared only after those bodies are in the journal.
func (s *Store) journalSuperseded(ctx context.Context, sess *Session) error {
	s.mu.RLock()
	sl := s.sessions[sess.ID]
	s.mu.RUnlock()
	if sl == nil {
		return nil
	}
	sl.mu.Lock()
	extra := append([]snapshotSource(nil), sl.alsoJournal...)
	extraDisk := append([]string(nil), sl.alsoJournalDiskNames...)
	header := sl.sess
	diskName := sl.diskName
	sl.mu.Unlock()
	if len(extra) == 0 && len(extraDisk) == 0 {
		return nil
	}
	if header == nil {
		header = sess
	}
	if err := s.journalSources(ctx, header, extra, diskName); err != nil {
		return err
	}
	if err := s.journalDiskCopies(ctx, header, extraDisk); err != nil {
		return err
	}
	sl.mu.Lock()
	sl.alsoJournal = nil
	sl.alsoJournalDiskNames = nil
	sl.mu.Unlock()
	return nil
}

func (s *Store) journalSources(ctx context.Context, header *Session, sources []snapshotSource, diskName string) error {
	seen := make(map[snapshotSource]bool, len(sources))
	for _, src := range sources {
		if seen[src] || src == sourceUnknown || src == sourceMemory {
			continue
		}
		seen[src] = true
		snap, err := s.readSnapshot(ctx, header, src, diskName)
		if err != nil {
			return fmt.Errorf("journaling superseded session %s: %w", header.ID, err)
		}
		if snap == nil {
			return fmt.Errorf("journaling superseded session %s: snapshot not found", header.ID)
		}
		if err := s.appendJournal(ctx, snap); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) journalDiskCopies(ctx context.Context, header *Session, diskNames []string) error {
	seen := make(map[string]bool, len(diskNames))
	for _, name := range diskNames {
		if seen[name] {
			continue
		}
		seen[name] = true
		snap, err := s.readSnapshot(ctx, header, sourceDisk, name)
		if err != nil {
			return fmt.Errorf("journaling superseded YAML session %s (%s): %w", header.ID, name, err)
		}
		if snap == nil {
			return fmt.Errorf("journaling superseded YAML session %s (%s): snapshot not found", header.ID, name)
		}
		if err := s.appendJournal(ctx, snap); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) readSnapshot(ctx context.Context, header *Session, source snapshotSource, diskName string) (*Session, error) {
	switch source {
	case sourceDisk:
		return s.loadSnapshotFromDisk(header, diskName)
	case sourceSQLite:
		return s.loadFromSQLite(ctx, header.ID, header.Channel, header.UserID)
	case sourcePostgres:
		return s.loadFromDB(ctx, header.ID, header.Channel, header.UserID)
	default:
		return nil, nil
	}
}

// --- PostgreSQL backend ---

func (s *Store) saveToDB(ctx context.Context, sess *Session) error {
	msgs, err := json.Marshal(sess.Messages)
	if err != nil {
		return fmt.Errorf("marshaling messages: %w", err)
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO sessions (id, channel, user_id, messages, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE SET
			messages = EXCLUDED.messages,
			updated_at = EXCLUDED.updated_at
	`, sess.ID, sess.Channel, sess.UserID, msgs, sess.CreatedAt, sess.UpdatedAt)

	if err != nil {
		return fmt.Errorf("saving session to db: %w", err)
	}
	return nil
}

func (s *Store) loadFromDB(ctx context.Context, key, channel, userID string) (*Session, error) {
	var sess Session
	var msgsJSON []byte

	err := s.pool.QueryRow(ctx,
		"SELECT id, channel, user_id, messages, created_at, updated_at FROM sessions WHERE id = $1",
		key,
	).Scan(&sess.ID, &sess.Channel, &sess.UserID, &msgsJSON, &sess.CreatedAt, &sess.UpdatedAt)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading session from db: %w", err)
	}

	if err := json.Unmarshal(msgsJSON, &sess.Messages); err != nil {
		return nil, fmt.Errorf("unmarshaling messages: %w", err)
	}

	return &sess, nil
}

// --- YAML file backend ---

func (s *Store) sessionFilePath(sess *Session) string {
	safe := strings.NewReplacer(":", "_", "/", "_", "\\", "_").Replace(sess.ID)
	return filepath.Join(s.dataDir, safe+".yaml")
}

func (s *Store) saveToDisk(sess *Session) error {
	if err := os.MkdirAll(s.dataDir, 0700); err != nil {
		return fmt.Errorf("creating data dir: %w", err)
	}

	data, err := yaml.Marshal(sess)
	if err != nil {
		return fmt.Errorf("marshaling session: %w", err)
	}

	path := s.sessionFilePath(sess)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing session file: %w", err)
	}

	return nil
}

// --- SQLite backend ---

func (s *Store) saveToSQLite(ctx context.Context, sess *Session) error {
	msgs, err := json.Marshal(sess.Messages)
	if err != nil {
		return fmt.Errorf("marshaling messages: %w", err)
	}

	_, err = s.sqliteDB.ExecContext(ctx, `
		INSERT INTO sessions (id, channel, user_id, messages, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			messages = excluded.messages,
			updated_at = excluded.updated_at
	`, sess.ID, sess.Channel, sess.UserID, string(msgs), sess.CreatedAt, sess.UpdatedAt)

	if err != nil {
		return fmt.Errorf("saving session to sqlite: %w", err)
	}
	return nil
}

func (s *Store) loadFromSQLite(ctx context.Context, key, channel, userID string) (*Session, error) {
	var sess Session
	var msgsJSON string

	err := s.sqliteDB.QueryRowContext(ctx,
		"SELECT id, channel, user_id, messages, created_at, updated_at FROM sessions WHERE id = ?",
		key,
	).Scan(&sess.ID, &sess.Channel, &sess.UserID, &msgsJSON, &sess.CreatedAt, &sess.UpdatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading session from sqlite: %w", err)
	}

	if err := json.Unmarshal([]byte(msgsJSON), &sess.Messages); err != nil {
		return nil, fmt.Errorf("unmarshaling messages: %w", err)
	}

	return &sess, nil
}

func (s *Store) loadIndexFromSQLite() error {
	// A journal-only SQLite store may not have a sessions table yet.
	var tableName string
	err := s.sqliteDB.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&tableName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	rows, err := s.sqliteDB.Query("SELECT id, channel, user_id, created_at, updated_at FROM sessions")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		sess := &Session{}
		if err := rows.Scan(&sess.ID, &sess.Channel, &sess.UserID, &sess.CreatedAt, &sess.UpdatedAt); err != nil {
			return err
		}
		if sess.ID == "" {
			return fmt.Errorf("SQLite session row has empty ID")
		}
		s.sessions[sess.ID] = &sessionSlot{sess: sess, source: sourceSQLite}
	}
	return rows.Err()
}

func (s *Store) loadIndexFromDisk() error {
	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		ext := filepath.Ext(entry.Name())
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dataDir, entry.Name()))
		if err != nil {
			return fmt.Errorf("reading session file %s: %w", entry.Name(), err)
		}

		// Decode the complete YAML document but materialize only header fields.
		// migrateSnapshots validates and journals every message body once.
		var metadata sessionHeader
		if err := yaml.Unmarshal(data, &metadata); err != nil {
			return fmt.Errorf("parsing session file %s: %w", entry.Name(), err)
		}
		if metadata.ID == "" {
			return fmt.Errorf("session file %s has empty ID", entry.Name())
		}
		header := &Session{
			ID: metadata.ID, Channel: metadata.Channel, UserID: metadata.UserID,
			CreatedAt: metadata.CreatedAt, UpdatedAt: metadata.UpdatedAt,
		}
		if prev := s.sessions[metadata.ID]; prev != nil {
			if prev.source == sourceDisk {
				canonical := filepath.Base(s.sessionFilePath(header))
				if !preferDiskSnapshot(prev.diskName, entry.Name(), canonical) {
					prev.alsoJournalDiskNames = append(prev.alsoJournalDiskNames, entry.Name())
					continue
				}
				prev.alsoJournalDiskNames = append(prev.alsoJournalDiskNames, prev.diskName)
			} else if prev.source != sourceUnknown && prev.source != sourceMemory {
				prev.alsoJournal = append(prev.alsoJournal, prev.source)
			}
			prev.sess = header
			prev.source = sourceDisk
			prev.diskName = entry.Name()
			prev.loaded = false
			continue
		}
		s.sessions[metadata.ID] = &sessionSlot{sess: header, source: sourceDisk, diskName: entry.Name()}
	}
	return nil
}

func preferDiskSnapshot(current, candidate, canonical string) bool {
	if current == canonical {
		return false
	}
	if candidate == canonical {
		return true
	}
	if filepath.Ext(current) != filepath.Ext(candidate) {
		return filepath.Ext(candidate) == ".yaml"
	}
	return candidate < current
}

func (s *Store) loadSnapshotFromDisk(header *Session, diskName string) (*Session, error) {
	path := s.sessionFilePath(header)
	if base := filepath.Base(diskName); base != "" && base != "." && base != string(filepath.Separator) {
		path = filepath.Join(s.dataDir, base)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading session file: %w", err)
	}
	var sess Session
	if err := yaml.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("parsing session file: %w", err)
	}
	if sess.ID == "" {
		sess.ID = header.ID
	} else if sess.ID != header.ID {
		return nil, fmt.Errorf("session file %s contains ID %q instead of %q", path, sess.ID, header.ID)
	}
	return &sess, nil
}
