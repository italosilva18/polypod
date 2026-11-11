package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrSessionStale = errors.New("histórico da conversa alterado por outra instância")

// CheckCurrentSnapshot detects a second CLI process whose in-memory session
// predates a durable turn written by the first. Call it while holding the CLI
// profile lease and before submitting a model request or executing a tool.
func (m *Manager) CheckCurrentSnapshot(ctx context.Context, sess *Session) error {
	if m == nil || m.store == nil || sess == nil {
		return nil
	}
	return m.store.checkCurrentSnapshot(ctx, sess)
}

func (s *Store) checkCurrentSnapshot(ctx context.Context, sess *Session) error {
	if s.pool != nil {
		stored, err := s.loadFromDB(ctx, sess.ID, sess.Channel, sess.UserID)
		if err != nil {
			return err
		}
		if !sameStoredMessages(sess, stored) {
			return ErrSessionStale
		}
	}
	if s.sqliteDB != nil {
		var raw string
		err := s.sqliteDB.QueryRowContext(ctx, `SELECT messages FROM sessions WHERE id=?`, sess.ID).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			if len(sess.Messages) != 0 {
				return ErrSessionStale
			}
		} else if err != nil {
			return fmt.Errorf("verificando histórico atual: %w", err)
		} else {
			var stored Session
			if err := json.Unmarshal([]byte(raw), &stored.Messages); err != nil {
				return fmt.Errorf("lendo histórico atual: %w", err)
			}
			if !sameStoredMessages(sess, &stored) {
				return ErrSessionStale
			}
		}
	}
	if s.dataDir != "" {
		stored, err := s.loadSnapshotFromDisk(sess, "")
		if err != nil {
			return err
		}
		if !sameStoredMessages(sess, stored) {
			return ErrSessionStale
		}
	}
	return nil
}

func sameStoredMessages(current, stored *Session) bool {
	if stored == nil {
		return len(current.Messages) == 0
	}
	if len(stored.Messages) != len(current.Messages) {
		return false
	}
	for i, message := range current.Messages {
		other := stored.Messages[i]
		if message.ID != other.ID || message.Role != other.Role || message.Content != other.Content ||
			message.Origin != other.Origin || !message.Timestamp.Equal(other.Timestamp) {
			return false
		}
	}
	return true
}
