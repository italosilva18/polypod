package types

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
)

// SessionID uniquely identifies a conversation session.
// Branded type prevents accidental mixing with other string IDs.
type SessionID string

// AgentID uniquely identifies a subagent within a session.
// Format: "a" + optional label + "-" + 16 hex chars.
type AgentID string

// TaskID uniquely identifies a background task.
// Format: type prefix + 8 chars from base-36 alphabet.
type TaskID string

const (
	TaskPrefixBash     = "b"
	TaskPrefixAgent    = "a"
	TaskPrefixRemote   = "r"
	TaskPrefixTeammate = "t"
	TaskPrefixWorkflow = "w"
	TaskPrefixMonitor  = "m"
)

var agentIDPattern = regexp.MustCompile(`^a(?:.+-)?[0-9a-f]{16}$`)

// NewSessionID generates a new unique session identifier.
func NewSessionID() SessionID {
	b := make([]byte, 8)
	rand.Read(b)
	return SessionID("ses_" + hex.EncodeToString(b))
}

// NewAgentID generates a new unique agent identifier.
func NewAgentID(label string) AgentID {
	b := make([]byte, 8)
	rand.Read(b)
	if label != "" {
		return AgentID("a" + label + "-" + hex.EncodeToString(b))
	}
	return AgentID("a" + hex.EncodeToString(b))
}

// NewTaskID generates a new unique task identifier with a type prefix.
func NewTaskID(prefix string) TaskID {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 8)
	rand.Read(b)
	id := prefix
	for i := 0; i < 8; i++ {
		id += string(alphabet[b[i]%byte(len(alphabet))])
	}
	return TaskID(id)
}

// ValidateAgentID checks if a string matches the AgentID format.
func ValidateAgentID(s string) bool {
	return agentIDPattern.MatchString(s)
}

// String returns the raw string value of SessionID.
func (id SessionID) String() string { return string(id) }

// String returns the raw string value of AgentID.
func (id AgentID) String() string { return string(id) }

// String returns the raw string value of TaskID.
func (id TaskID) String() string { return string(id) }

// IsZero returns true if the SessionID is empty.
func (id SessionID) IsZero() bool { return id == "" }

// IsZero returns true if the AgentID is empty.
func (id AgentID) IsZero() bool { return id == "" }

// IsZero returns true if the TaskID is empty.
func (id TaskID) IsZero() bool { return id == "" }
