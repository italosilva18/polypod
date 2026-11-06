package session

import (
	"log/slog"
	"sync"
	"time"
)

// SessionHooks provides integration hooks that connect session persistence
// to the AI pipeline. It tracks messages and tool calls during a session
// and persists them on session end.
type SessionHooks struct {
	mu      sync.Mutex
	store   SessionStorage
	session *RestoredSession
	logger  *slog.Logger
}

// NewSessionHooks creates hooks connected to the given storage and session ID.
// If sessionID is empty, a new session ID is generated.
func NewSessionHooks(store SessionStorage, sessionID string, logger *slog.Logger) *SessionHooks {
	if sessionID == "" {
		sessionID = GenerateSessionID()
	}
	if logger == nil {
		logger = slog.Default()
	}

	now := time.Now()
	return &SessionHooks{
		store: store,
		session: &RestoredSession{
			ID:        sessionID,
			Messages:  make([]Message, 0),
			CreatedAt: now,
			UpdatedAt: now,
			Version:   CurrentSessionVersion,
		},
		logger: logger,
	}
}

// SessionID returns the current session's identifier.
func (h *SessionHooks) SessionID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.session.ID
}

// OnMessageSent is called when a message is added to the conversation.
func (h *SessionHooks) OnMessageSent(role, content string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.session.Messages = append(h.session.Messages, Message{
		Role:    role,
		Content: content,
	})
	h.session.UpdatedAt = time.Now()

	// Auto-title from first user message
	if role == "user" && h.session.Title == "" {
		h.session.Title = TitleFromText(content, 60)
	}
}

// OnToolCall is called when a tool executes during the session.
// Tool calls are recorded as system messages for context.
func (h *SessionHooks) OnToolCall(name, args, result string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Record tool calls as a compact system message
	content := "[tool:" + name + "] " + truncateForHook(result, 500)
	h.session.Messages = append(h.session.Messages, Message{
		Role:    "system",
		Content: content,
	})
	h.session.UpdatedAt = time.Now()
}

// OnSessionEnd persists the session to disk. Should be called on shutdown.
func (h *SessionHooks) OnSessionEnd() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.session.Messages) == 0 {
		h.logger.Debug("skipping session save — no messages")
		return nil
	}

	if err := h.store.Save(h.session); err != nil {
		h.logger.Error("failed to save session on end", "id", h.session.ID, "error", err)
		return err
	}

	h.logger.Info("session saved", "id", h.session.ID, "messages", len(h.session.Messages))
	return nil
}

// RestoreHistory returns the messages from the underlying persisted session.
// This is used when restoring a session to inject history into the conversation.
func (h *SessionHooks) RestoreHistory() []Message {
	h.mu.Lock()
	defer h.mu.Unlock()

	out := make([]Message, len(h.session.Messages))
	copy(out, h.session.Messages)
	return out
}

// SetMetadata updates the session metadata.
func (h *SessionHooks) SetMetadata(fn func(*SessionMetadata)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fn(&h.session.Metadata)
}

// GetSession returns a copy of the underlying session for inspection.
func (h *SessionHooks) GetSession() RestoredSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	return *h.session
}

// LoadFrom replaces the current session with a restored one.
func (h *SessionHooks) LoadFrom(sess *RestoredSession) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.session = sess
}

func truncateForHook(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
