package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"
)

// Store persists sessions. Supports PostgreSQL, YAML file, or in-memory only.
type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	pool     *pgxpool.Pool // nil = no postgres
	dataDir  string        // non-empty = YAML file persistence
}

// NewStore creates a new session store.
// - pool != nil → PostgreSQL backend
// - dataDir != "" → YAML file persistence
// - both nil/empty → in-memory only
func NewStore(pool *pgxpool.Pool, dataDir string) *Store {
	s := &Store{
		sessions: make(map[string]*Session),
		pool:     pool,
		dataDir:  dataDir,
	}

	// Load existing sessions from YAML files on startup
	if dataDir != "" {
		s.loadAllFromDisk()
	}

	return s
}

func sessionKey(channel, userID string) string {
	return channel + ":" + userID
}

// Get retrieves or creates a session.
func (s *Store) Get(ctx context.Context, channel, userID string) (*Session, error) {
	key := sessionKey(channel, userID)

	s.mu.RLock()
	sess, ok := s.sessions[key]
	s.mu.RUnlock()
	if ok {
		return sess, nil
	}

	// Try loading from DB
	if s.pool != nil {
		sess, err := s.loadFromDB(ctx, key, channel, userID)
		if err != nil {
			return nil, err
		}
		if sess != nil {
			s.mu.Lock()
			s.sessions[key] = sess
			s.mu.Unlock()
			return sess, nil
		}
	}

	// Create new
	sess = NewSession(key, channel, userID)
	s.mu.Lock()
	s.sessions[key] = sess
	s.mu.Unlock()
	return sess, nil
}

// Save persists a session (to DB and/or YAML file).
func (s *Store) Save(ctx context.Context, sess *Session) error {
	if s.pool != nil {
		if err := s.saveToDB(ctx, sess); err != nil {
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

// ListSessions returns all active session IDs.
func (s *Store) ListSessions() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	return ids
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

	if err != nil {
		return nil, nil // not found
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
	if err := os.MkdirAll(s.dataDir, 0755); err != nil {
		return fmt.Errorf("creating data dir: %w", err)
	}

	data, err := yaml.Marshal(sess)
	if err != nil {
		return fmt.Errorf("marshaling session: %w", err)
	}

	path := s.sessionFilePath(sess)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing session file: %w", err)
	}

	return nil
}

func (s *Store) loadAllFromDisk() {
	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		return // dir doesn't exist yet
	}

	for _, entry := range entries {
		ext := filepath.Ext(entry.Name())
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(s.dataDir, entry.Name()))
		if err != nil {
			continue
		}

		var sess Session
		if err := yaml.Unmarshal(data, &sess); err != nil {
			continue
		}

		s.sessions[sess.ID] = &sess
	}
}
