package remote

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrSessionNotFound  = errors.New("sessão remota não encontrada")
	ErrNotParticipant   = errors.New("participante não pertence à sessão")
	ErrMessageConflict  = errors.New("ID de mensagem já usado com outro conteúdo")
	ErrInvalidCursor    = errors.New("cursor não pertence à sessão")
	ErrForwardLeaseLost = errors.New("posse do encaminhamento remoto foi perdida")
)

type sessionStore struct{ db *sql.DB }

func openSessionStore(path string) (*sessionStore, error) {
	if path == "" {
		return nil, fmt.Errorf("caminho do banco de sessões é obrigatório")
	}
	if path != ":memory:" {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("criando diretório das sessões remotas: %w", err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return nil, fmt.Errorf("restringindo diretório das sessões remotas: %w", err)
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, fmt.Errorf("criando banco privado de sessões remotas: %w", err)
		}
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("fechando banco de sessões remotas: %w", err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, fmt.Errorf("restringindo banco de sessões remotas: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("abrindo banco de sessões remotas: %w", err)
	}
	// One connection keeps connection-local SQLite pragmas effective. SQLite WAL
	// coordinates separate server processes sharing this database file.
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=FULL",
		"CREATE TABLE IF NOT EXISTS remote_sessions (id TEXT PRIMARY KEY, created_at TEXT NOT NULL, owner TEXT NOT NULL, status TEXT NOT NULL)",
		"CREATE TABLE IF NOT EXISTS remote_participants (session_id TEXT NOT NULL REFERENCES remote_sessions(id) ON DELETE CASCADE, participant TEXT NOT NULL, PRIMARY KEY(session_id, participant))",
		"CREATE TABLE IF NOT EXISTS remote_messages (sequence INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL REFERENCES remote_sessions(id) ON DELETE CASCADE, id TEXT NOT NULL, type TEXT NOT NULL, sender TEXT NOT NULL, content TEXT NOT NULL, timestamp TEXT NOT NULL, UNIQUE(session_id, id))",
		"CREATE INDEX IF NOT EXISTS remote_messages_session_sequence ON remote_messages(session_id, sequence)",
		"CREATE TABLE IF NOT EXISTS remote_cursors (session_id TEXT NOT NULL, participant TEXT NOT NULL, sequence INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(session_id, participant), FOREIGN KEY(session_id, participant) REFERENCES remote_participants(session_id, participant) ON DELETE CASCADE)",
		"CREATE TABLE IF NOT EXISTS remote_forwards (sequence INTEGER PRIMARY KEY REFERENCES remote_messages(sequence) ON DELETE CASCADE, forwarded_at INTEGER, claim_owner TEXT, claim_until INTEGER)",
		"CREATE INDEX IF NOT EXISTS remote_forwards_pending ON remote_forwards(forwarded_at, claim_until, sequence)",
		"CREATE TABLE IF NOT EXISTS remote_questions (id TEXT PRIMARY KEY, source_key TEXT NOT NULL, source_message_id TEXT NOT NULL, session_id TEXT NOT NULL REFERENCES remote_sessions(id) ON DELETE CASCADE, participant TEXT NOT NULL, prompt TEXT NOT NULL, answer TEXT NOT NULL DEFAULT '', answer_message_id TEXT NOT NULL DEFAULT '', expires_at INTEGER NOT NULL, created_at INTEGER NOT NULL)",
		"CREATE INDEX IF NOT EXISTS remote_questions_source ON remote_questions(source_key, created_at DESC)",
		"CREATE TABLE IF NOT EXISTS remote_question_aliases (alias TEXT PRIMARY KEY, question_id TEXT NOT NULL REFERENCES remote_questions(id) ON DELETE CASCADE)",
		"INSERT OR IGNORE INTO remote_forwards(sequence) SELECT sequence FROM remote_messages WHERE type='user'",
	} {
		if _, err := db.Exec(statement); err != nil {
			if closeErr := db.Close(); closeErr != nil {
				return nil, fmt.Errorf("inicializando sessões remotas: %w; fechando banco: %v", err, closeErr)
			}
			return nil, fmt.Errorf("inicializando sessões remotas: %w", err)
		}
	}
	if path != ":memory:" {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Chmod(path+suffix, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
				if closeErr := db.Close(); closeErr != nil {
					return nil, fmt.Errorf("restringindo arquivo SQLite: %w; fechando banco: %v", err, closeErr)
				}
				return nil, fmt.Errorf("restringindo arquivo SQLite: %w", err)
			}
		}
	}
	return &sessionStore{db: db}, nil
}

func newID(prefix string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("gerando ID remoto: %w", err)
	}
	return prefix + hex.EncodeToString(raw[:]), nil
}

func (s *sessionStore) createSession(ctx context.Context, owner string) (RemoteSession, error) {
	id, err := newID("session_")
	if err != nil {
		return RemoteSession{}, err
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RemoteSession{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "INSERT INTO remote_sessions(id,created_at,owner,status) VALUES(?,?,?,?)", id, now.Format(time.RFC3339Nano), owner, "active"); err != nil {
		return RemoteSession{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO remote_participants(session_id,participant) VALUES(?,?)", id, owner); err != nil {
		return RemoteSession{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO remote_cursors(session_id,participant,sequence) VALUES(?,?,0)", id, owner); err != nil {
		return RemoteSession{}, err
	}
	if err := tx.Commit(); err != nil {
		return RemoteSession{}, err
	}
	return RemoteSession{ID: id, CreatedAt: now, Owner: owner, Participants: []string{owner}, Status: "active"}, nil
}

func (s *sessionStore) getSession(ctx context.Context, id string) (RemoteSession, error) {
	var session RemoteSession
	var created string
	err := s.db.QueryRowContext(ctx, "SELECT id,created_at,owner,status FROM remote_sessions WHERE id=?", id).
		Scan(&session.ID, &created, &session.Owner, &session.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return RemoteSession{}, ErrSessionNotFound
	}
	if err != nil {
		return RemoteSession{}, err
	}
	session.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return RemoteSession{}, fmt.Errorf("data de sessão inválida: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, "SELECT participant FROM remote_participants WHERE session_id=? ORDER BY participant", id)
	if err != nil {
		return RemoteSession{}, err
	}
	defer rows.Close()
	session.Participants = make([]string, 0)
	for rows.Next() {
		var participant string
		if err := rows.Scan(&participant); err != nil {
			return RemoteSession{}, err
		}
		session.Participants = append(session.Participants, participant)
	}
	if err := rows.Err(); err != nil {
		return RemoteSession{}, err
	}
	return session, nil
}

func (s *sessionStore) listSessions(ctx context.Context) ([]RemoteSession, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,created_at,owner,status FROM remote_sessions ORDER BY created_at DESC, id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sessions := make([]RemoteSession, 0)
	for rows.Next() {
		var session RemoteSession
		var created string
		if err := rows.Scan(&session.ID, &created, &session.Owner, &session.Status); err != nil {
			return nil, err
		}
		session.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, fmt.Errorf("data de sessão inválida: %w", err)
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Release the only DB connection before fetching participant lists.
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range sessions {
		participants, err := s.participants(ctx, sessions[i].ID)
		if err != nil {
			return nil, err
		}
		sessions[i].Participants = participants
	}
	return sessions, nil
}

func (s *sessionStore) participants(ctx context.Context, id string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT participant FROM remote_participants WHERE session_id=? ORDER BY participant", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var participant string
		if err := rows.Scan(&participant); err != nil {
			return nil, err
		}
		result = append(result, participant)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *sessionStore) joinSession(ctx context.Context, id, participant string) (RemoteSession, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RemoteSession{}, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT 1 FROM remote_sessions WHERE id=?", id).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RemoteSession{}, ErrSessionNotFound
		}
		return RemoteSession{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO remote_participants(session_id,participant) VALUES(?,?)", id, participant); err != nil {
		return RemoteSession{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO remote_cursors(session_id,participant,sequence) VALUES(?,?,0)", id, participant); err != nil {
		return RemoteSession{}, err
	}
	if err := tx.Commit(); err != nil {
		return RemoteSession{}, err
	}
	return s.getSession(ctx, id)
}

func (s *sessionStore) cursor(ctx context.Context, id, participant string) (int64, error) {
	var cursor int64
	err := s.db.QueryRowContext(ctx, "SELECT sequence FROM remote_cursors WHERE session_id=? AND participant=?", id, participant).Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotParticipant
	}
	return cursor, err
}

func (s *sessionStore) messagesAfter(ctx context.Context, id string, after int64, limit int) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT sequence,id,type,sender,content,timestamp FROM remote_messages WHERE session_id=? AND sequence>? ORDER BY sequence ASC LIMIT ?",
		id, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	msgs := make([]Message, 0)
	contentBytes := 0
	for rows.Next() {
		var msg Message
		var timestamp string
		if err := rows.Scan(&msg.Sequence, &msg.ID, &msg.Type, &msg.Sender, &msg.Content, &timestamp); err != nil {
			return nil, err
		}
		msg.Timestamp, err = time.Parse(time.RFC3339Nano, timestamp)
		if err != nil {
			return nil, fmt.Errorf("data de mensagem inválida: %w", err)
		}
		if len(msgs) > 0 && contentBytes+len(msg.Content) > 1<<20 {
			break
		}
		contentBytes += len(msg.Content)
		msgs = append(msgs, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return msgs, nil
}

func (s *sessionStore) insertMessage(ctx context.Context, sessionID, sender string, msg Message) (Message, error) {
	if msg.ID == "" {
		id, err := newID("message_")
		if err != nil {
			return Message{}, err
		}
		msg.ID = id
	}
	msg.Sender = sender
	msg.Timestamp = time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT 1 FROM remote_cursors WHERE session_id=? AND participant=?", sessionID, sender).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Message{}, ErrNotParticipant
		}
		return Message{}, err
	}
	result, err := tx.ExecContext(ctx,
		"INSERT OR IGNORE INTO remote_messages(session_id,id,type,sender,content,timestamp) VALUES(?,?,?,?,?,?)",
		sessionID, msg.ID, msg.Type, msg.Sender, msg.Content, msg.Timestamp.Format(time.RFC3339Nano))
	if err != nil {
		return Message{}, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return Message{}, err
	}
	var stored Message
	var timestamp string
	if err := tx.QueryRowContext(ctx,
		"SELECT sequence,id,type,sender,content,timestamp FROM remote_messages WHERE session_id=? AND id=?", sessionID, msg.ID).
		Scan(&stored.Sequence, &stored.ID, &stored.Type, &stored.Sender, &stored.Content, &timestamp); err != nil {
		return Message{}, err
	}
	if inserted == 0 && (stored.Type != msg.Type || stored.Sender != msg.Sender || stored.Content != msg.Content) {
		return Message{}, ErrMessageConflict
	}
	if stored.Type == "user" {
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO remote_forwards(sequence) VALUES(?)", stored.Sequence); err != nil {
			return Message{}, err
		}
	}
	stored.Timestamp, err = time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	return stored, nil
}

func (s *sessionStore) acknowledge(ctx context.Context, sessionID, participant string, sequence int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if sequence != 0 {
		var exists int
		err := tx.QueryRowContext(ctx, "SELECT 1 FROM remote_messages WHERE session_id=? AND sequence=?", sessionID, sequence).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidCursor
		}
		if err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx,
		"UPDATE remote_cursors SET sequence=MAX(sequence,?) WHERE session_id=? AND participant=?",
		sequence, sessionID, participant)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotParticipant
	}
	return tx.Commit()
}

func (s *sessionStore) claimForward(ctx context.Context, owner string) (string, Message, bool, error) {
	now := time.Now().UnixMilli()
	var sequence int64
	err := s.db.QueryRowContext(ctx,
		"SELECT f.sequence FROM remote_forwards f JOIN remote_messages m ON m.sequence=f.sequence "+
			"WHERE f.forwarded_at IS NULL AND (f.claim_until IS NULL OR f.claim_until<=?) "+
			"AND NOT EXISTS (SELECT 1 FROM remote_forwards earlier JOIN remote_messages prior ON prior.sequence=earlier.sequence "+
			"WHERE prior.session_id=m.session_id AND earlier.sequence<f.sequence AND earlier.forwarded_at IS NULL) "+
			"ORDER BY f.sequence LIMIT 1", now).
		Scan(&sequence)
	if errors.Is(err, sql.ErrNoRows) {
		return "", Message{}, false, nil
	}
	if err != nil {
		return "", Message{}, false, err
	}
	result, err := s.db.ExecContext(ctx,
		"UPDATE remote_forwards SET claim_owner=?,claim_until=? WHERE sequence=? AND forwarded_at IS NULL AND (claim_until IS NULL OR claim_until<=?)",
		owner, now+60_000, sequence, now)
	if err != nil {
		return "", Message{}, false, err
	}
	claimed, err := result.RowsAffected()
	if err != nil {
		return "", Message{}, false, err
	}
	if claimed == 0 {
		return "", Message{}, false, nil // another process won the claim
	}
	var sessionID, timestamp string
	var msg Message
	err = s.db.QueryRowContext(ctx,
		"SELECT session_id,sequence,id,type,sender,content,timestamp FROM remote_messages WHERE sequence=?", sequence).
		Scan(&sessionID, &msg.Sequence, &msg.ID, &msg.Type, &msg.Sender, &msg.Content, &timestamp)
	if err != nil {
		return "", Message{}, false, err
	}
	msg.Timestamp, err = time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return "", Message{}, false, fmt.Errorf("data de mensagem inválida: %w", err)
	}
	return sessionID, msg, true, nil
}

func (s *sessionStore) finishForward(ctx context.Context, sequence int64, owner string, delivered bool) error {
	var result sql.Result
	var err error
	if delivered {
		result, err = s.db.ExecContext(ctx,
			"UPDATE remote_forwards SET forwarded_at=?,claim_owner=NULL,claim_until=NULL WHERE sequence=? AND claim_owner=?",
			time.Now().UnixMilli(), sequence, owner)
	} else {
		result, err = s.db.ExecContext(ctx,
			"UPDATE remote_forwards SET claim_owner=NULL,claim_until=? WHERE sequence=? AND claim_owner=?",
			time.Now().Add(5*time.Second).UnixMilli(), sequence, owner)
	}
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrForwardLeaseLost
	}
	return nil
}
