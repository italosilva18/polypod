package session

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// SessionStatus represents the lifecycle state of a session.
type SessionStatus string

const (
	StatusInitializing   SessionStatus = "initializing"
	StatusActive         SessionStatus = "active"
	StatusPaused         SessionStatus = "paused"
	StatusCompleted      SessionStatus = "completed"
	StatusError          SessionStatus = "error"
	StatusIdle           SessionStatus = "idle"
	StatusRunning        SessionStatus = "running"
	StatusRequiresAction SessionStatus = "requires_action"
)

// RequiresActionDetails carries context when a session transitions to
// requires_action, so downstream surfaces can show what is blocking.
type RequiresActionDetails struct {
	ToolName          string                 `json:"tool_name"`
	ActionDescription string                 `json:"action_description"`
	ToolUseID         string                 `json:"tool_use_id"`
	RequestID         string                 `json:"request_id"`
	Input             map[string]interface{} `json:"input,omitempty"`
}

// SessionState holds the complete state of an active session.
type SessionState struct {
	mu sync.RWMutex

	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Status    SessionStatus   `json:"status"`
	Messages  []Message       `json:"messages"`
	Tools     []string        `json:"tools,omitempty"`
	Agents    []string        `json:"agents,omitempty"`
	Metadata  SessionMetadata `json:"metadata"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	Version   int             `json:"version"`

	// State change listeners.
	stateListeners    []func(SessionStatus, *RequiresActionDetails)
	metadataListeners []func(map[string]interface{})

	// Snapshot history for recovery.
	snapshots []SessionSnapshot
	maxSnaps  int
}

// SessionSnapshot is a point-in-time capture of session state for recovery.
type SessionSnapshot struct {
	Timestamp time.Time       `json:"timestamp"`
	Status    SessionStatus   `json:"status"`
	Messages  []Message       `json:"messages"`
	Metadata  SessionMetadata `json:"metadata"`
}

// NewSessionState creates a new session state with the given ID.
func NewSessionState(id string) *SessionState {
	now := time.Now()
	return &SessionState{
		ID:        id,
		Status:    StatusInitializing,
		Messages:  make([]Message, 0),
		Tools:     make([]string, 0),
		Agents:    make([]string, 0),
		CreatedAt: now,
		UpdatedAt: now,
		Version:   CurrentSessionVersion,
		maxSnaps:  10,
	}
}

// SetStatus transitions the session to a new status.
func (s *SessionState) SetStatus(status SessionStatus, details *RequiresActionDetails) {
	s.mu.Lock()
	s.Status = status
	s.UpdatedAt = time.Now()
	listeners := make([]func(SessionStatus, *RequiresActionDetails), len(s.stateListeners))
	copy(listeners, s.stateListeners)
	s.mu.Unlock()

	for _, fn := range listeners {
		fn(status, details)
	}
}

// GetStatus returns the current session status.
func (s *SessionState) GetStatus() SessionStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Status
}

// OnStateChange registers a listener that fires when the session status changes.
func (s *SessionState) OnStateChange(fn func(SessionStatus, *RequiresActionDetails)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stateListeners = append(s.stateListeners, fn)
}

// OnMetadataChange registers a listener that fires when session metadata changes.
func (s *SessionState) OnMetadataChange(fn func(map[string]interface{})) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metadataListeners = append(s.metadataListeners, fn)
}

// NotifyMetadataChanged emits a metadata change to all registered listeners.
func (s *SessionState) NotifyMetadataChanged(meta map[string]interface{}) {
	s.mu.RLock()
	listeners := make([]func(map[string]interface{}), len(s.metadataListeners))
	copy(listeners, s.metadataListeners)
	s.mu.RUnlock()

	for _, fn := range listeners {
		fn(meta)
	}
}

// AddMessage appends a message to the session.
func (s *SessionState) AddMessage(msg Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Messages = append(s.Messages, msg)
	s.UpdatedAt = time.Now()
}

// GetMessages returns a copy of the session messages.
func (s *SessionState) GetMessages() []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Message, len(s.Messages))
	copy(out, s.Messages)
	return out
}

// MessageCount returns the number of messages.
func (s *SessionState) MessageCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.Messages)
}

// SetTitle sets the session title.
func (s *SessionState) SetTitle(title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Title = title
	s.UpdatedAt = time.Now()
}

// SetTools sets the list of available tools.
func (s *SessionState) SetTools(tools []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Tools = tools
	s.UpdatedAt = time.Now()
}

// SetAgents sets the list of available agents.
func (s *SessionState) SetAgents(agents []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Agents = agents
	s.UpdatedAt = time.Now()
}

// UpdateMetadata updates the session metadata fields.
func (s *SessionState) UpdateMetadata(fn func(*SessionMetadata)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.Metadata)
	s.UpdatedAt = time.Now()
}

// TakeSnapshot creates a point-in-time snapshot for recovery.
func (s *SessionState) TakeSnapshot() {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap := SessionSnapshot{
		Timestamp: time.Now(),
		Status:    s.Status,
		Messages:  make([]Message, len(s.Messages)),
		Metadata:  s.Metadata,
	}
	copy(snap.Messages, s.Messages)

	s.snapshots = append(s.snapshots, snap)
	if len(s.snapshots) > s.maxSnaps {
		s.snapshots = s.snapshots[1:]
	}
}

// LatestSnapshot returns the most recent snapshot, or nil.
func (s *SessionState) LatestSnapshot() *SessionSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.snapshots) == 0 {
		return nil
	}
	snap := s.snapshots[len(s.snapshots)-1]
	return &snap
}

// RestoreFromSnapshot rolls back to the given snapshot.
func (s *SessionState) RestoreFromSnapshot(snap *SessionSnapshot) {
	if snap == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = snap.Status
	s.Messages = make([]Message, len(snap.Messages))
	copy(s.Messages, snap.Messages)
	s.Metadata = snap.Metadata
	s.UpdatedAt = time.Now()
}

// Snapshots returns all recorded snapshots.
func (s *SessionState) Snapshots() []SessionSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SessionSnapshot, len(s.snapshots))
	copy(out, s.snapshots)
	return out
}

// Serialize converts the session state to JSON bytes.
func (s *SessionState) Serialize() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return json.Marshal(s)
}

// DeserializeSessionState loads a SessionState from JSON bytes.
func DeserializeSessionState(data []byte) (*SessionState, error) {
	var state SessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("deserializing session state: %w", err)
	}
	if state.Messages == nil {
		state.Messages = make([]Message, 0)
	}
	if state.Tools == nil {
		state.Tools = make([]string, 0)
	}
	if state.Agents == nil {
		state.Agents = make([]string, 0)
	}
	return &state, nil
}

// IsTerminal returns true if the status represents a terminal state.
func (s SessionStatus) IsTerminal() bool {
	return s == StatusCompleted || s == StatusError
}

// IsTransitionValid returns true if transitioning from→to is allowed.
func IsTransitionValid(from, to SessionStatus) bool {
	allowed := map[SessionStatus][]SessionStatus{
		StatusInitializing:   {StatusActive, StatusError},
		StatusActive:         {StatusPaused, StatusCompleted, StatusError, StatusIdle, StatusRunning, StatusRequiresAction},
		StatusPaused:         {StatusActive, StatusCompleted, StatusError},
		StatusIdle:           {StatusActive, StatusRunning, StatusCompleted, StatusError},
		StatusRunning:        {StatusActive, StatusIdle, StatusPaused, StatusCompleted, StatusError, StatusRequiresAction},
		StatusRequiresAction: {StatusActive, StatusRunning, StatusIdle, StatusCompleted, StatusError},
		StatusCompleted:      {},
		StatusError:          {StatusActive, StatusInitializing},
	}

	targets, ok := allowed[from]
	if !ok {
		return false
	}
	for _, t := range targets {
		if t == to {
			return true
		}
	}
	return false
}
