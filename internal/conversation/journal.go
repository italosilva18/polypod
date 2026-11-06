package conversation

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
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

// loadJournal runs under journalMu. Corruption is reported, never silently skipped.
func (s *Store) loadJournal(ctx context.Context) error {
	if s.journalLoaded {
		return nil
	}
	var entries []JournalEntry
	if s.sqliteDB != nil {
		if _, err := s.sqliteDB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS conversation_journal (sequence INTEGER PRIMARY KEY, message_id TEXT NOT NULL UNIQUE, entry TEXT NOT NULL)`); err != nil {
			return err
		}
		rows, err := s.sqliteDB.QueryContext(ctx, `SELECT entry FROM conversation_journal ORDER BY sequence`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw string
			var e JournalEntry
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(raw), &e); err != nil {
				return err
			}
			entries = append(entries, e)
		}
		if err := rows.Err(); err != nil {
			return err
		}
	} else if s.dataDir != "" {
		raw, err := os.ReadFile(filepath.Join(s.dataDir, "journal.jsonl"))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		// Require complete records. A failed append must be repaired explicitly rather
		// than acknowledging a cursor beyond evidence which was never persisted.
		for len(raw) > 0 {
			var e JournalEntry
			n := 0
			for n < len(raw) && raw[n] != '\n' {
				n++
			}
			if n == len(raw) {
				return fmt.Errorf("conversation journal: incomplete final record")
			}
			if err := json.Unmarshal(raw[:n], &e); err != nil {
				return err
			}
			entries = append(entries, e)
			raw = raw[n+1:]
		}
	}
	ids := make(map[string]bool, len(entries))
	var previous uint64
	for _, e := range entries {
		if e.Sequence <= previous || e.Message.ID == "" || ids[e.Message.ID] {
			return fmt.Errorf("conversation journal: invalid sequence or duplicate ID")
		}
		previous = e.Sequence
		ids[e.Message.ID] = true
	}
	s.journal = entries
	s.journalIDs = ids
	s.journalLoaded = true
	return nil
}

func (s *Store) appendJournal(ctx context.Context, sess *Session) error {
	s.journalMu.Lock()
	defer s.journalMu.Unlock()
	if err := s.loadJournal(ctx); err != nil {
		return fmt.Errorf("loading conversation journal: %w", err)
	}
	messages := append(append([]Message(nil), sess.pending...), sess.Messages...)
	for _, msg := range messages {
		if err := ctx.Err(); err != nil {
			return err
		}
		if msg.ID == "" { // Stable migration identity for pre-journal sessions.
			raw, err := json.Marshal(msg)
			if err != nil {
				return err
			}
			msg.ID = fmt.Sprintf("legacy-%x", sha256.Sum256(append([]byte(sess.ID), raw...)))
		}
		if s.journalIDs[msg.ID] {
			continue
		}
		seq := uint64(1)
		if len(s.journal) > 0 {
			seq = s.journal[len(s.journal)-1].Sequence + 1
		}
		e := JournalEntry{Sequence: seq, SessionID: sess.ID, Channel: sess.Channel, Message: msg}
		raw, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if s.sqliteDB != nil {
			if _, err = s.sqliteDB.ExecContext(ctx, `INSERT INTO conversation_journal(sequence,message_id,entry) VALUES(?,?,?)`, seq, msg.ID, string(raw)); err != nil {
				return fmt.Errorf("writing conversation journal: %w", err)
			}
		} else if s.dataDir != "" {
			if err = os.MkdirAll(s.dataDir, 0700); err != nil {
				return err
			}
			f, err := os.OpenFile(filepath.Join(s.dataDir, "journal.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
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
		s.journal = append(s.journal, e)
		s.journalIDs[msg.ID] = true
	}
	sess.pending = nil
	return nil
}

// JournalAfter returns an oldest-first batch. Existing session snapshots are
// imported once by stable IDs, allowing upgrades without losing retained context.
func (s *Store) JournalAfter(ctx context.Context, sequence uint64, limit int) ([]JournalEntry, error) {
	if s.journalInitErr != nil {
		return nil, s.journalInitErr
	}
	s.journalMu.Lock()
	defer s.journalMu.Unlock()
	if err := s.loadJournal(ctx); err != nil {
		return nil, err
	}
	start := sort.Search(len(s.journal), func(i int) bool { return s.journal[i].Sequence > sequence })
	end := len(s.journal)
	if limit > 0 && end-start > limit {
		end = start + limit
	}
	return append([]JournalEntry(nil), s.journal[start:end]...), nil
}
