package conversation

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
)

// JournalEntry is immutable evidence, independent of the bounded model context.
// Sequence is a durable consolidation cursor; Message.ID identifies provenance.
type JournalEntry struct {
	Sequence  uint64  `json:"sequence"`
	SessionID string  `json:"session_id"`
	Channel   string  `json:"channel"`
	Message   Message `json:"message"`
}

func (s *Store) journalPath() string {
	return filepath.Join(s.dataDir, "journal.jsonl")
}

// loadJournal runs under journalMu. SQLite rows are validated when they are
// read or deduplicated; a startup probe does not decode every historical row.
// SQLite's unique index owns message IDs and its primary key owns sequence.
// YAML-only stores retain journal bodies in memory so cursor pages do not
// rescan the JSONL file.
func (s *Store) loadJournal(ctx context.Context) error {
	if s.journalLoaded {
		return nil
	}
	if s.sqliteDB != nil {
		if _, err := s.sqliteDB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS conversation_journal (sequence INTEGER PRIMARY KEY, message_id TEXT NOT NULL UNIQUE, entry TEXT NOT NULL)`); err != nil {
			return err
		}
		if err := s.validateSQLiteFirstSequence(ctx); err != nil {
			return err
		}
		last, err := s.sqliteJournalLast(ctx)
		if err != nil {
			return err
		}
		s.journalLast = uint64(last)
		s.journalLoaded = true
		return nil
	}
	ids := make(map[string]bool)
	var last uint64
	var memory []JournalEntry
	err := s.scanJournal(ctx, func(e JournalEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.Sequence <= last || e.Message.ID == "" || ids[e.Message.ID] {
			return fmt.Errorf("conversation journal: invalid sequence or duplicate ID")
		}
		last = e.Sequence
		ids[e.Message.ID] = true
		memory = append(memory, e)
		return nil
	})
	if err != nil {
		return err
	}
	s.journalEntries = memory
	s.journalIDs = ids
	s.journalLast = last
	s.journalLoaded = true
	return nil
}

func (s *Store) scanJournal(ctx context.Context, fn func(JournalEntry) error) error {
	if s.dataDir != "" {
		return s.scanJournalFile(fn)
	}
	return nil
}

func (s *Store) validateSQLiteFirstSequence(ctx context.Context) error {
	var first sql.NullInt64
	if err := s.sqliteDB.QueryRowContext(ctx, `SELECT MIN(sequence) FROM conversation_journal`).Scan(&first); err != nil {
		return fmt.Errorf("reading conversation journal first sequence: %w", err)
	}
	if first.Valid && first.Int64 <= 0 {
		return fmt.Errorf("conversation journal first sequence is invalid: %d", first.Int64)
	}
	return nil
}

func (s *Store) sqliteJournalLast(ctx context.Context) (int64, error) {
	var last int64
	if err := s.sqliteDB.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) FROM conversation_journal`).Scan(&last); err != nil {
		return 0, fmt.Errorf("reading conversation journal sequence: %w", err)
	}
	if last < 0 || last == math.MaxInt64 {
		return 0, fmt.Errorf("conversation journal sequence is invalid: %d", last)
	}
	return last, nil
}

// scanJournalFile requires a newline after every record. A torn final append
// is an error so callers do not advance a cursor past evidence that was never
// persisted. fn is not invoked for that torn line.
func (s *Store) scanJournalFile(fn func(JournalEntry) error) error {
	f, err := os.Open(s.journalPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) == 0 && err == io.EOF {
			return nil
		}
		if err != nil && err != io.EOF {
			return err
		}
		if len(line) == 0 || line[len(line)-1] != '\n' {
			return fmt.Errorf("conversation journal: incomplete final record")
		}
		line = line[:len(line)-1]
		var e JournalEntry
		if uerr := json.Unmarshal(line, &e); uerr != nil {
			return uerr
		}
		if ferr := fn(e); ferr != nil {
			return ferr
		}
	}
}

func (s *Store) appendJournal(ctx context.Context, sess *Session) error {
	s.journalMu.Lock()
	defer s.journalMu.Unlock()
	if err := s.loadJournal(ctx); err != nil {
		return fmt.Errorf("loading conversation journal: %w", err)
	}
	// pending retains turns evicted from the bounded snapshot. Both slices are
	// journaled in that order. IDs are written onto the stored messages.
	if err := s.journalSlice(ctx, sess, sess.pending); err != nil {
		return err
	}
	if err := s.journalSlice(ctx, sess, sess.Messages); err != nil {
		return err
	}
	sess.pending = nil
	return nil
}

func (s *Store) journalSlice(ctx context.Context, sess *Session, msgs []Message) error {
	for i := range msgs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := assignStoredID(sess.ID, &msgs[i]); err != nil {
			return err
		}
		msg := msgs[i]
		if s.sqliteDB != nil {
			if err := s.appendSQLiteJournalMessage(ctx, sess, msg); err != nil {
				return err
			}
			continue
		}
		if s.journalIDs[msg.ID] {
			continue
		}
		seq := s.journalLast + 1
		e := JournalEntry{Sequence: seq, SessionID: sess.ID, Channel: sess.Channel, Message: msg}
		raw, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if s.dataDir != "" {
			if err = os.MkdirAll(s.dataDir, 0700); err != nil {
				return err
			}
			f, err := os.OpenFile(s.journalPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			_, writeErr := f.Write(append(raw, '\n'))
			syncErr := f.Sync()
			closeErr := f.Close()
			if writeErr != nil {
				return writeErr
			}
			if syncErr != nil {
				return syncErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
		s.journalEntries = append(s.journalEntries, e)
		s.journalIDs[msg.ID] = true
		s.journalLast = seq
	}
	return nil
}

// appendSQLiteJournalMessage asks SQLite for the next sequence on every write,
// so another Polypod process cannot leave this store's cached sequence stale.
// A competing insert is retried only when the primary key advanced; a row
// with the same message ID is checked before it is treated as already saved.
func (s *Store) appendSQLiteJournalMessage(ctx context.Context, sess *Session, msg Message) error {
	prior, found, err := s.sqliteEntryByID(ctx, msg.ID)
	if err != nil {
		return err
	}
	if found {
		return sameSessionJournalID(sess.ID, prior)
	}
	for attempt := 0; attempt < 3; attempt++ {
		last, err := s.sqliteJournalLast(ctx)
		if err != nil {
			return err
		}
		seq := uint64(last) + 1
		e := JournalEntry{Sequence: seq, SessionID: sess.ID, Channel: sess.Channel, Message: msg}
		raw, err := json.Marshal(e)
		if err != nil {
			return err
		}
		_, err = s.sqliteDB.ExecContext(ctx,
			`INSERT INTO conversation_journal(sequence,message_id,entry) VALUES(?,?,?)`, seq, msg.ID, string(raw))
		if err == nil {
			s.journalLast = seq
			return nil
		}
		prior, found, lookupErr := s.sqliteEntryByID(ctx, msg.ID)
		if lookupErr != nil {
			return fmt.Errorf("writing conversation journal: %w; checking existing ID: %w", err, lookupErr)
		}
		if found {
			return sameSessionJournalID(sess.ID, prior)
		}
		advanced, queryErr := s.sqliteJournalLast(ctx)
		if queryErr != nil {
			return fmt.Errorf("writing conversation journal: %w; checking sequence: %w", err, queryErr)
		}
		if advanced <= last {
			return fmt.Errorf("writing conversation journal: %w", err)
		}
	}
	return fmt.Errorf("writing conversation journal: sequence changed during three attempts")
}

func sameSessionJournalID(sessionID string, entry JournalEntry) error {
	if entry.SessionID != sessionID {
		return fmt.Errorf("conversation journal: message ID belongs to session %q instead of %q", entry.SessionID, sessionID)
	}
	return nil
}

func (s *Store) sqliteEntryByID(ctx context.Context, messageID string) (JournalEntry, bool, error) {
	var rowSequence int64
	var raw string
	err := s.sqliteDB.QueryRowContext(ctx,
		`SELECT sequence, entry FROM conversation_journal WHERE message_id = ?`, messageID).Scan(&rowSequence, &raw)
	if err == sql.ErrNoRows {
		return JournalEntry{}, false, nil
	}
	if err != nil {
		return JournalEntry{}, false, fmt.Errorf("reading conversation journal message: %w", err)
	}
	e, err := decodeSQLiteEntry(rowSequence, messageID, raw)
	if err != nil {
		return JournalEntry{}, false, err
	}
	return e, true, nil
}

func decodeSQLiteEntry(rowSequence int64, messageID, raw string) (JournalEntry, error) {
	var e JournalEntry
	if rowSequence <= 0 || messageID == "" {
		return JournalEntry{}, fmt.Errorf("conversation journal: invalid SQLite row metadata")
	}
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		return JournalEntry{}, fmt.Errorf("decoding conversation journal: %w", err)
	}
	if e.Sequence != uint64(rowSequence) || e.Message.ID != messageID || e.SessionID == "" {
		return JournalEntry{}, fmt.Errorf("conversation journal: SQLite row does not match its entry")
	}
	return e, nil
}

func stampSessionIDs(sess *Session) error {
	if sess == nil {
		return nil
	}
	for i := range sess.Messages {
		if err := assignStoredID(sess.ID, &sess.Messages[i]); err != nil {
			return err
		}
	}
	return nil
}

// assignStoredID gives an id-less message a stable journal id. The hash is
// taken before the id is written, so an existing journal row still matches.
func assignStoredID(sessionID string, msg *Message) error {
	if msg == nil || msg.ID != "" {
		return nil
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	msg.ID = fmt.Sprintf("legacy-%x", sha256.Sum256(append([]byte(sessionID), raw...)))
	return nil
}

// JournalAfter returns an oldest-first batch. Existing session snapshots are
// imported once by stable IDs, allowing upgrades without losing retained context.
// A non-positive limit returns every entry after sequence. SQLite pages from
// the DB; YAML-only stores page from their in-memory journal.
func (s *Store) JournalAfter(ctx context.Context, sequence uint64, limit int) ([]JournalEntry, error) {
	if s.journalInitErr != nil {
		return nil, s.journalInitErr
	}
	s.journalMu.Lock()
	defer s.journalMu.Unlock()
	if err := s.loadJournal(ctx); err != nil {
		return nil, err
	}
	if s.sqliteDB == nil {
		start := sort.Search(len(s.journalEntries), func(i int) bool { return s.journalEntries[i].Sequence > sequence })
		end := len(s.journalEntries)
		if limit > 0 && end-start > limit {
			end = start + limit
		}
		return append([]JournalEntry(nil), s.journalEntries[start:end]...), nil
	}
	return s.journalAfterSQLite(ctx, sequence, limit)
}

func (s *Store) journalAfterSQLite(ctx context.Context, sequence uint64, limit int) ([]JournalEntry, error) {
	query := `SELECT sequence, message_id, entry FROM conversation_journal WHERE sequence > ? ORDER BY sequence`
	args := []any{sequence}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.sqliteDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JournalEntry
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var raw string
		var messageID string
		var rowSequence int64
		if err := rows.Scan(&rowSequence, &messageID, &raw); err != nil {
			return nil, err
		}
		e, err := decodeSQLiteEntry(rowSequence, messageID, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ReadJournalMessage loads one journaled message by its stable ID. The body
// is read from SQLite or the in-memory journal.
// ok is false when the id is unknown. A journal that failed to open returns
// the initialization or validation error.
func (s *Store) ReadJournalMessage(ctx context.Context, messageID string) (Message, bool, error) {
	if messageID == "" {
		return Message{}, false, nil
	}
	if s.journalInitErr != nil {
		return Message{}, false, s.journalInitErr
	}
	s.journalMu.Lock()
	defer s.journalMu.Unlock()
	if err := s.loadJournal(ctx); err != nil {
		return Message{}, false, err
	}
	if s.sqliteDB != nil {
		e, found, err := s.sqliteEntryByID(ctx, messageID)
		if err != nil || !found {
			return Message{}, found, err
		}
		return e.Message, true, nil
	}
	if !s.journalIDs[messageID] {
		return Message{}, false, nil
	}
	for _, e := range s.journalEntries {
		if e.Message.ID == messageID {
			return e.Message, true, nil
		}
	}
	return Message{}, false, nil
}
