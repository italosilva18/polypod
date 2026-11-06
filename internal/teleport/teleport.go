package teleport

import (
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Checkpoint represents a saved conversation state at a specific point.
type Checkpoint struct {
	ID           string    `json:"id"`
	TurnNumber   int       `json:"turn_number"`
	MessageCount int       `json:"message_count"`
	Timestamp    time.Time `json:"timestamp"`
	Label        string    `json:"label"`
	TokensUsed   int       `json:"tokens_used"`
}

// TeleportManager manages conversation checkpoints for session teleportation.
// It allows saving, listing, retrieving, and jumping between conversation states.
type TeleportManager struct {
	mu           sync.Mutex
	checkpoints  []*Checkpoint
	index        map[string]*Checkpoint // id -> checkpoint
	current      *Checkpoint            // active teleport target
	autoInterval int                    // auto-save every N turns (0 = disabled)
	lastAutoTurn int                    // last turn that triggered auto-checkpoint
}

// NewTeleportManager creates a new TeleportManager with no checkpoints.
func NewTeleportManager() *TeleportManager {
	return &TeleportManager{
		checkpoints: make([]*Checkpoint, 0),
		index:       make(map[string]*Checkpoint),
	}
}

// SaveCheckpoint creates and stores a new checkpoint at the given turn with the
// provided message count and label. It returns a pointer to the saved checkpoint.
func (tm *TeleportManager) SaveCheckpoint(turn int, msgCount int, label string) *Checkpoint {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	cp := &Checkpoint{
		ID:           uuid.New().String(),
		TurnNumber:   turn,
		MessageCount: msgCount,
		Timestamp:    time.Now(),
		Label:        label,
	}

	tm.checkpoints = append(tm.checkpoints, cp)
	tm.index[cp.ID] = cp
	return cp
}

// ListCheckpoints returns all stored checkpoints in insertion order.
func (tm *TeleportManager) ListCheckpoints() []*Checkpoint {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	out := make([]*Checkpoint, len(tm.checkpoints))
	copy(out, tm.checkpoints)
	return out
}

// GetCheckpoint retrieves a checkpoint by ID. The second return value indicates
// whether the checkpoint was found.
func (tm *TeleportManager) GetCheckpoint(id string) (*Checkpoint, bool) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	cp, ok := tm.index[id]
	return cp, ok
}

// FindByTurn finds the checkpoint whose TurnNumber is nearest to the given turn.
// Returns nil and false if no checkpoints exist.
func (tm *TeleportManager) FindByTurn(turn int) (*Checkpoint, bool) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if len(tm.checkpoints) == 0 {
		return nil, false
	}

	var best *Checkpoint
	bestDist := math.MaxInt

	for _, cp := range tm.checkpoints {
		dist := turn - cp.TurnNumber
		if dist < 0 {
			dist = -dist
		}
		if dist < bestDist {
			bestDist = dist
			best = cp
		}
	}

	return best, true
}

// TeleportTo marks the checkpoint with the given ID as the active teleport target.
// Returns the checkpoint and nil on success, or an error if the ID is not found.
func (tm *TeleportManager) TeleportTo(id string) (*Checkpoint, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	cp, ok := tm.index[id]
	if !ok {
		return nil, fmt.Errorf("teleport: checkpoint %q not found", id)
	}

	tm.current = cp
	return cp, nil
}

// Current returns the currently active checkpoint, or nil if none is set.
func (tm *TeleportManager) Current() *Checkpoint {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	return tm.current
}

// AutoCheckpoint configures automatic checkpoint saving every interval turns.
// A value of 0 disables auto-checkpointing. When called, it records the current
// auto-save interval so that external callers can invoke SaveCheckpoint at the
// appropriate cadence by checking ShouldAutoSave.
func (tm *TeleportManager) AutoCheckpoint(interval int) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.autoInterval = interval
	tm.lastAutoTurn = 0
}

// ShouldAutoSave returns true if the given turn number should trigger an
// automatic checkpoint save based on the configured interval.
// It updates the last auto-save turn when returning true.
func (tm *TeleportManager) ShouldAutoSave(turn int) bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if tm.autoInterval <= 0 {
		return false
	}

	if turn-tm.lastAutoTurn >= tm.autoInterval {
		tm.lastAutoTurn = turn
		return true
	}
	return false
}

// teleportManagerJSON is the serialization envelope for TeleportManager.
type teleportManagerJSON struct {
	Checkpoints  []*Checkpoint `json:"checkpoints"`
	CurrentID    string        `json:"current_id,omitempty"`
	AutoInterval int           `json:"auto_interval"`
	LastAutoTurn int           `json:"last_auto_turn"`
}

// Serialize encodes the TeleportManager state as JSON.
func (tm *TeleportManager) Serialize() ([]byte, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	envelope := teleportManagerJSON{
		Checkpoints:  tm.checkpoints,
		AutoInterval: tm.autoInterval,
		LastAutoTurn: tm.lastAutoTurn,
	}
	if tm.current != nil {
		envelope.CurrentID = tm.current.ID
	}

	return json.MarshalIndent(envelope, "", "  ")
}

// Deserialize reconstructs a TeleportManager from JSON data previously
// produced by Serialize.
func Deserialize(data []byte) (*TeleportManager, error) {
	var envelope teleportManagerJSON
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("teleport: deserialize: %w", err)
	}

	tm := &TeleportManager{
		checkpoints:  envelope.Checkpoints,
		index:        make(map[string]*Checkpoint, len(envelope.Checkpoints)),
		autoInterval: envelope.AutoInterval,
		lastAutoTurn: envelope.LastAutoTurn,
	}

	if tm.checkpoints == nil {
		tm.checkpoints = make([]*Checkpoint, 0)
	}

	for _, cp := range tm.checkpoints {
		tm.index[cp.ID] = cp
	}

	if envelope.CurrentID != "" {
		if cp, ok := tm.index[envelope.CurrentID]; ok {
			tm.current = cp
		}
	}

	return tm, nil
}
