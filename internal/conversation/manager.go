package conversation

import (
	"context"
	"log/slog"
	"time"
)

// Manager handles the lifecycle of conversation sessions.
type Manager struct {
	store  *Store
	logger *slog.Logger
}

// NewManager creates a new conversation manager.
func NewManager(store *Store, logger *slog.Logger) *Manager {
	return &Manager{
		store:  store,
		logger: logger,
	}
}

// GetSession retrieves or creates a session for a user on a channel.
func (m *Manager) GetSession(ctx context.Context, channel, userID string) (*Session, error) {
	sess, err := m.store.Get(ctx, channel, userID)
	if err != nil {
		return nil, err
	}
	sess.unbounded = true
	return sess, nil
}

// AddUserMessage adds a user message to the session.
func (m *Manager) AddUserMessage(ctx context.Context, sess *Session, content string) Message {
	msg := sess.AddMessage(RoleUser, content)
	if err := m.store.Save(ctx, sess); err != nil {
		m.logger.Warn("failed to persist user message", "session", sess.ID, "error", err)
	}
	return msg
}

// AddAssistantMessage adds an assistant message and persists the session.
func (m *Manager) AddAssistantMessage(ctx context.Context, sess *Session, content string) error {
	sess.AddMessage(RoleAssistant, content)
	if err := m.store.Save(ctx, sess); err != nil {
		m.logger.Warn("failed to persist session", "session", sess.ID, "error", err)
		return err
	}
	return nil
}

// GetHistory returns the stored transcript. Callers that persist compaction
// use this list so a summary is produced from every turn it must cover.
func (m *Manager) GetHistory(sess *Session) []Message {
	return sess.History()
}

// ListSessions returns all active sessions.
func (m *Manager) ListSessions() []*Session {
	return m.store.GetAllSessions()
}

// ClearSession resets all messages in a session and persists the change.
func (m *Manager) ClearSession(ctx context.Context, sess *Session) error {
	sess.Messages = sess.Messages[:0]
	if err := m.store.Save(ctx, sess); err != nil {
		m.logger.Warn("failed to persist cleared session", "session", sess.ID, "error", err)
		return err
	}
	return nil
}

// TruncateMessages removes the last n messages from the session and persists.
// Returns the messages that were removed (in original order). If n exceeds the
// session length, all messages are removed.
func (m *Manager) TruncateMessages(ctx context.Context, sess *Session, n int) ([]Message, error) {
	if n <= 0 || len(sess.Messages) == 0 {
		return nil, nil
	}
	if n > len(sess.Messages) {
		n = len(sess.Messages)
	}
	cut := len(sess.Messages) - n
	removed := make([]Message, n)
	copy(removed, sess.Messages[cut:])
	sess.Messages = sess.Messages[:cut]
	if err := m.store.Save(ctx, sess); err != nil {
		m.logger.Warn("failed to persist truncated session", "session", sess.ID, "error", err)
		return removed, err
	}
	return removed, nil
}

// ReplaceMessages replaces the session messages wholesale and persists.
// Used by /resume to load a persisted session into the active conversation.
func (m *Manager) ReplaceMessages(ctx context.Context, sess *Session, msgs []Message) error {
	previous := append([]Message(nil), sess.Messages...)
	previousUpdatedAt := sess.UpdatedAt
	// Stamp ids on the caller slice before copying so a compacted history that
	// is sent to the model names the same journal rows Save is about to write.
	for i := range msgs {
		if err := assignStoredID(sess.ID, &msgs[i]); err != nil {
			return err
		}
	}
	sess.Messages = append(sess.Messages[:0], msgs...)
	sess.UpdatedAt = time.Now()
	if err := m.store.Save(ctx, sess); err != nil {
		sess.Messages = previous
		sess.UpdatedAt = previousUpdatedAt
		m.logger.Warn("failed to persist replaced session", "session", sess.ID, "error", err)
		return err
	}
	return nil
}
