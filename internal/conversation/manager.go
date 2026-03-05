package conversation

import (
	"context"
	"log/slog"
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
	return m.store.Get(ctx, channel, userID)
}

// AddUserMessage adds a user message to the session.
func (m *Manager) AddUserMessage(ctx context.Context, sess *Session, content string) {
	sess.AddMessage(RoleUser, content)
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

// GetHistory returns the conversation history for the AI API.
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
