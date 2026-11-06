// Package humanloop provides the durable inbox used to wake the full agent
// from background routines and remote relays without coupling them to the TUI.
package humanloop

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Direction identifies which side of the human loop owns a message.
type Direction string

const (
	DirectionInbound  Direction = "inbound"
	DirectionOutbound Direction = "outbound"
)

// Status is the durable lifecycle state of a message.
type Status string

const (
	StatusPending    Status = "pending"
	StatusProcessing Status = "processing"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
)

// Message is one durable inbox or outbox item.
type Message struct {
	ID            string     `json:"id"`
	Direction     Direction  `json:"direction"`
	Status        Status     `json:"status"`
	Channel       string     `json:"channel"`
	UserID        string     `json:"user_id"`
	Text          string     `json:"text"`
	ReplyTo       string     `json:"reply_to,omitempty"`
	Response      string     `json:"response,omitempty"`
	Error         string     `json:"error,omitempty"`
	Attempts      int        `json:"attempts"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	ProcessingAt  *time.Time `json:"processing_at,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	NextAttemptAt time.Time  `json:"next_attempt_at,omitempty"`
}

// State is the persisted heartbeat metadata associated with an inbox.
type State struct {
	LastHeartbeat time.Time `json:"last_heartbeat,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
}

type diskState struct {
	Version  int       `json:"version"`
	State    State     `json:"state"`
	Messages []Message `json:"messages"`
}

// Store owns the durable inbox journal. All mutations are persisted before
// the method returns. Finished history is bounded; active work is never
// discarded by history trimming.
type Store struct {
	mu         sync.Mutex
	path       string
	maxHistory int
	state      State
	messages   []Message
}

// NewStore loads or creates a durable inbox. Any item left in processing by a
// crashed process is recovered to pending so it can be attempted again.
func NewStore(path string, maxHistory int) (*Store, error) {
	if path == "" {
		return nil, errors.New("humanloop: caminho da caixa de entrada e obrigatorio")
	}
	if maxHistory <= 0 {
		maxHistory = 500
	}
	s := &Store{path: path, maxHistory: maxHistory}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("humanloop: lendo caixa de entrada: %w", err)
	}
	var disk diskState
	if err := json.Unmarshal(raw, &disk); err != nil {
		return fmt.Errorf("humanloop: decodificando caixa de entrada: %w", err)
	}
	s.state = disk.State
	s.messages = disk.Messages

	now := time.Now()
	recovered := false
	for i := range s.messages {
		if s.messages[i].Status != StatusProcessing {
			continue
		}
		s.messages[i].Status = StatusPending
		s.messages[i].ProcessingAt = nil
		s.messages[i].NextAttemptAt = now
		s.messages[i].UpdatedAt = now
		s.messages[i].Error = "execucao interrompida; recuperada apos reinicio"
		recovered = true
	}
	if recovered {
		return s.saveLocked()
	}
	return nil
}

// EnqueueInbound records a message that must wake the agent.
func (s *Store) EnqueueInbound(channel, userID, text string) (Message, error) {
	return s.enqueue(DirectionInbound, channel, userID, text, "")
}

// EnqueueOutbound records a response or notification that must be delivered.
func (s *Store) EnqueueOutbound(channel, userID, text, replyTo string) (Message, error) {
	return s.enqueue(DirectionOutbound, channel, userID, text, replyTo)
}

func (s *Store) enqueue(direction Direction, channel, userID, text, replyTo string) (Message, error) {
	if direction != DirectionInbound && direction != DirectionOutbound {
		return Message{}, fmt.Errorf("humanloop: direcao invalida: %s", direction)
	}
	if text == "" {
		return Message{}, errors.New("humanloop: mensagem vazia")
	}
	now := time.Now()
	msg := Message{
		ID:            newID(),
		Direction:     direction,
		Status:        StatusPending,
		Channel:       channel,
		UserID:        userID,
		Text:          text,
		ReplyTo:       replyTo,
		CreatedAt:     now,
		UpdatedAt:     now,
		NextAttemptAt: now,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msg)
	if err := s.saveLocked(); err != nil {
		return Message{}, err
	}
	return msg, nil
}

// ClaimNextDue atomically moves the oldest due pending item to processing.
// The attempts counter is incremented before returning it to the worker.
func (s *Store) ClaimNextDue(now time.Time) (Message, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i := range s.messages {
		msg := &s.messages[i]
		if msg.Status != StatusPending || msg.NextAttemptAt.After(now) {
			continue
		}
		if idx == -1 || msg.CreatedAt.Before(s.messages[idx].CreatedAt) {
			idx = i
		}
	}
	if idx == -1 {
		return Message{}, false, nil
	}

	msg := &s.messages[idx]
	msg.Status = StatusProcessing
	msg.Attempts++
	msg.UpdatedAt = now
	msg.ProcessingAt = timePtr(now)
	if err := s.saveLocked(); err != nil {
		return Message{}, false, err
	}
	return *msg, true, nil
}

// Complete marks an item successful and records any generated response.
func (s *Store) Complete(id, response string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, err := s.findLocked(id)
	if err != nil {
		return err
	}
	msg.Status = StatusCompleted
	msg.Response = response
	msg.Error = ""
	msg.UpdatedAt = now
	msg.CompletedAt = timePtr(now)
	msg.ProcessingAt = nil
	s.trimLocked()
	return s.saveLocked()
}

// Retry records a failed attempt. The item is returned to pending until
// maxAttempts is reached; at that point it becomes terminally failed.
func (s *Store) Retry(id string, cause error, next time.Time, maxAttempts int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, err := s.findLocked(id)
	if err != nil {
		return err
	}
	msg.Error = cause.Error()
	msg.UpdatedAt = time.Now()
	msg.ProcessingAt = nil
	if maxAttempts > 0 && msg.Attempts >= maxAttempts {
		msg.Status = StatusFailed
		msg.CompletedAt = timePtr(msg.UpdatedAt)
		s.trimLocked()
	} else {
		msg.Status = StatusPending
		msg.NextAttemptAt = next
	}
	return s.saveLocked()
}

// RecordHeartbeat persists liveness and the latest cycle error.
func (s *Store) RecordHeartbeat(at time.Time, cycleErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.LastHeartbeat = at
	s.state.LastError = ""
	if cycleErr != nil {
		s.state.LastError = cycleErr.Error()
	}
	return s.saveLocked()
}

// Snapshot returns copies suitable for status and audit views.
func (s *Store) Snapshot() (State, []Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	messages := append([]Message(nil), s.messages...)
	sort.SliceStable(messages, func(i, j int) bool {
		return messages[i].CreatedAt.Before(messages[j].CreatedAt)
	})
	return s.state, messages
}

func (s *Store) findLocked(id string) (*Message, error) {
	for i := range s.messages {
		if s.messages[i].ID == id {
			return &s.messages[i], nil
		}
	}
	return nil, fmt.Errorf("humanloop: mensagem %s nao encontrada", id)
}

func (s *Store) trimLocked() {
	terminal := 0
	for _, msg := range s.messages {
		if msg.Status == StatusCompleted || msg.Status == StatusFailed {
			terminal++
		}
	}
	remove := terminal - s.maxHistory
	if remove <= 0 {
		return
	}
	kept := make([]Message, 0, len(s.messages)-remove)
	for _, msg := range s.messages {
		isTerminal := msg.Status == StatusCompleted || msg.Status == StatusFailed
		if isTerminal && remove > 0 {
			remove--
			continue
		}
		kept = append(kept, msg)
	}
	s.messages = kept
}

func (s *Store) saveLocked() error {
	disk := diskState{Version: 1, State: s.state, Messages: s.messages}
	raw, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return fmt.Errorf("humanloop: codificando caixa de entrada: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("humanloop: criando diretorio: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("humanloop: gravando caixa temporaria: %w", err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return fmt.Errorf("humanloop: protegendo caixa temporaria: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("humanloop: publicando caixa de entrada: %w", err)
	}
	return nil
}

func newID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err == nil {
		return "msg_" + hex.EncodeToString(buf)
	}
	return fmt.Sprintf("msg_%d", time.Now().UnixNano())
}

func timePtr(t time.Time) *time.Time { return &t }
