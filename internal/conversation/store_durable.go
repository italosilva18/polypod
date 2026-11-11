package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type durabilityUnknownError struct{ cause error }

func (e *durabilityUnknownError) Error() string {
	return "persistência da mensagem não confirmada: " + e.cause.Error()
}
func (e *durabilityUnknownError) Unwrap() error { return e.cause }

// saveMessageDurable publishes a new CLI message only when its session
// snapshot is durable. The runtime's SQLite-only backend commits the snapshot
// and journal in one transaction. Other backends save the snapshot first; a
// journal failure there is reported as a warning so the caller never rolls
// back a message that already exists in the durable session.
func (s *Store) saveMessageDurable(ctx context.Context, sess *Session) (journalComplete bool, err error) {
	if s.journalInitErr != nil {
		return false, s.journalInitErr
	}
	if sess == nil {
		return false, errors.New("saving durable message: session is nil")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := s.ensureLoaded(ctx, sess); err != nil {
		return false, err
	}
	if err := s.journalSuperseded(ctx, sess); err != nil {
		return false, err
	}
	if s.sqliteDB != nil && s.pool == nil && s.dataDir == "" {
		return true, s.saveSQLiteMessageTransaction(ctx, sess)
	}
	// The fallback cannot atomically commit independent backends. Publish the
	// snapshot first, then journal it. A later save repairs a missing journal
	// entry; no journal-only user turn can be created by a snapshot failure.
	published := false
	if s.pool != nil {
		if err := s.saveToDB(ctx, sess); err != nil {
			return false, err
		}
		published = true
	}
	if s.sqliteDB != nil {
		if err := s.saveToSQLite(ctx, sess); err != nil {
			if published {
				return false, &durabilityUnknownError{err}
			}
			return false, err
		}
		published = true
	}
	if s.dataDir != "" {
		if err := s.saveToDiskAtomic(sess); err != nil {
			if published {
				return false, &durabilityUnknownError{err}
			}
			return false, err
		}
		published = true
	}
	if err := s.appendJournal(ctx, sess); err != nil {
		return false, &durabilityUnknownError{fmt.Errorf("snapshot salvo, journal pendente: %w", err)}
	}
	return true, nil
}

func (s *Store) saveSQLiteMessageTransaction(ctx context.Context, sess *Session) error {
	s.journalMu.Lock()
	defer s.journalMu.Unlock()
	if err := s.loadJournal(ctx); err != nil {
		return fmt.Errorf("opening conversation journal: %w", err)
	}
	scope, err := journalScopeForEntry(ctx, sess)
	if err != nil {
		return err
	}
	tx, err := s.sqliteDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var last int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM conversation_journal`).Scan(&last); err != nil {
		return err
	}
	if last < 0 || last == math.MaxInt64 {
		return errors.New("conversation journal sequence exhausted")
	}
	appendOne := func(msg *Message) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := assignStoredID(sess.ID, msg); err != nil {
			return err
		}
		var raw string
		err := tx.QueryRowContext(ctx, `SELECT entry FROM conversation_journal WHERE message_id=?`, msg.ID).Scan(&raw)
		if err == nil {
			var prior JournalEntry
			if err := json.Unmarshal([]byte(raw), &prior); err != nil {
				return err
			}
			return sameSessionJournalID(sess.ID, prior)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if last == math.MaxInt64 {
			return errors.New("conversation journal sequence exhausted")
		}
		last++
		entry := JournalEntry{Sequence: uint64(last), SessionID: sess.ID, Channel: sess.Channel,
			BranchID: scope.BranchID, AgentName: scope.AgentName, Message: *msg}
		data, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO conversation_journal(sequence,message_id,entry) VALUES(?,?,?)`, last, msg.ID, string(data))
		return err
	}
	for i := range sess.pending {
		if err := appendOne(&sess.pending[i]); err != nil {
			return err
		}
	}
	for i := range sess.Messages {
		if err := appendOne(&sess.Messages[i]); err != nil {
			return err
		}
	}
	msgs, err := json.Marshal(sess.Messages)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sessions (id, channel, user_id, messages, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET messages=excluded.messages, updated_at=excluded.updated_at
	`, sess.ID, sess.Channel, sess.UserID, string(msgs), sess.CreatedAt, sess.UpdatedAt); err != nil {
		return fmt.Errorf("saving session and journal transaction: %w", err)
	}
	if err := tx.Commit(); err != nil {
		lastMessageID := ""
		if len(sess.Messages) > 0 {
			lastMessageID = sess.Messages[len(sess.Messages)-1].ID
		}
		journal, snapshot, verifyErr := s.verifySQLitePublication(sess.ID, lastMessageID)
		if verifyErr == nil && journal && snapshot {
			s.journalLast = uint64(last)
			sess.pending = nil
			return nil
		}
		if verifyErr != nil || journal != snapshot {
			return &durabilityUnknownError{fmt.Errorf("commit retornou erro e estado não pôde ser confirmado: %w (verificação: %v)", err, verifyErr)}
		}
		return fmt.Errorf("committing session and journal transaction: %w", err)
	}
	s.journalLast = uint64(last)
	sess.pending = nil
	return nil
}

func (s *Store) verifySQLitePublication(sessionID, messageID string) (journal, snapshot bool, err error) {
	if messageID == "" {
		return false, false, errors.New("message ID unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var raw string
	err = s.sqliteDB.QueryRowContext(ctx, `SELECT entry FROM conversation_journal WHERE message_id=?`, messageID).Scan(&raw)
	if err == nil {
		var entry JournalEntry
		if err = json.Unmarshal([]byte(raw), &entry); err != nil {
			return false, false, err
		}
		journal = entry.SessionID == sessionID && entry.Message.ID == messageID
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, false, err
	}
	err = s.sqliteDB.QueryRowContext(ctx, `SELECT messages FROM sessions WHERE id=?`, sessionID).Scan(&raw)
	if err == nil {
		var messages []Message
		if err = json.Unmarshal([]byte(raw), &messages); err != nil {
			return journal, false, err
		}
		for _, message := range messages {
			if message.ID == messageID {
				snapshot = true
				break
			}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return journal, false, err
	}
	return journal, snapshot, nil
}

func (s *Store) saveToDiskAtomic(sess *Session) error {
	if err := os.MkdirAll(s.dataDir, 0700); err != nil {
		return err
	}
	data, err := yaml.Marshal(sess)
	if err != nil {
		return err
	}
	path := s.sessionFilePath(sess)
	tmp, err := os.CreateTemp(s.dataDir, ".conversation-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return &durabilityUnknownError{err}
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return &durabilityUnknownError{err}
	}
	return nil
}
