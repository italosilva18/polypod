package conversation

import (
	"time"
)

const MaxMessages = 20

// Role represents who sent the message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

// Message is a single entry in a conversation.
type Message struct {
	Role      Role      `json:"role" yaml:"role"`
	Content   string    `json:"content" yaml:"content"`
	Timestamp time.Time `json:"timestamp" yaml:"timestamp"`
}

// Session holds conversation state as a ring buffer of messages.
type Session struct {
	ID        string    `json:"id" yaml:"id"`
	Channel   string    `json:"channel" yaml:"channel"`
	UserID    string    `json:"user_id" yaml:"user_id"`
	Messages  []Message `json:"messages" yaml:"messages"`
	CreatedAt time.Time `json:"created_at" yaml:"created_at"`
	UpdatedAt time.Time `json:"updated_at" yaml:"updated_at"`
}

// NewSession creates a new empty session.
func NewSession(id, channel, userID string) *Session {
	now := time.Now()
	return &Session{
		ID:        id,
		Channel:   channel,
		UserID:    userID,
		Messages:  make([]Message, 0, MaxMessages),
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// AddMessage appends a message, evicting the oldest if at capacity.
func (s *Session) AddMessage(role Role, content string) {
	msg := Message{
		Role:      role,
		Content:   content,
		Timestamp: time.Now(),
	}

	if len(s.Messages) >= MaxMessages {
		// Keep system messages, evict oldest non-system
		s.Messages = append(s.Messages[1:], msg)
	} else {
		s.Messages = append(s.Messages, msg)
	}
	s.UpdatedAt = time.Now()
}

// History returns the messages suitable for the AI API.
func (s *Session) History() []Message {
	return s.Messages
}
