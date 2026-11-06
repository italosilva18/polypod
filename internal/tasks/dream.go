package tasks

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Dream task migrated from Claude Code TypeScript source.
// Source: tasks/DreamTask/DreamTask.ts
//
// DreamTask is a background consolidation task that reviews past sessions
// and extracts reusable insights into memory files. It runs during idle
// periods and is visible in the footer pill.

// ---------------------------------------------------------------------------
// DreamPhase — lifecycle phases for the dream task
// ---------------------------------------------------------------------------

// DreamPhase represents the current phase of a dream task.
type DreamPhase string

const (
	// DreamPhaseStarting is the initial phase before any writes occur.
	DreamPhaseStarting DreamPhase = "starting"
	// DreamPhaseUpdating is set when the first Edit/Write lands.
	DreamPhaseUpdating DreamPhase = "updating"
)

// ---------------------------------------------------------------------------
// DreamTurn — a single assistant turn from the dream agent
// ---------------------------------------------------------------------------

// DreamTurn represents one assistant response from the dream agent,
// with tool uses collapsed to a count.
type DreamTurn struct {
	Text         string `json:"text"`
	ToolUseCount int    `json:"tool_use_count"`
}

// MaxDreamTurns is the number of recent turns to keep for live display.
const MaxDreamTurns = 30

// ---------------------------------------------------------------------------
// DreamState — dream-specific task state
// ---------------------------------------------------------------------------

// DreamState holds the extra state for a dream task.
type DreamState struct {
	Phase             DreamPhase  `json:"phase"`
	SessionsReviewing int         `json:"sessions_reviewing"`
	FilesTouched      []string    `json:"files_touched"`
	Turns             []DreamTurn `json:"turns"`
	PriorMtime        int64       `json:"prior_mtime"`
	mu                sync.Mutex
}

var (
	dreamStates   = make(map[string]*DreamState)
	dreamStatesMu sync.Mutex
)

func setDreamState(taskID string, state *DreamState) {
	dreamStatesMu.Lock()
	defer dreamStatesMu.Unlock()
	dreamStates[taskID] = state
}

func getDreamState(taskID string) *DreamState {
	dreamStatesMu.Lock()
	defer dreamStatesMu.Unlock()
	return dreamStates[taskID]
}

func removeDreamState(taskID string) {
	dreamStatesMu.Lock()
	defer dreamStatesMu.Unlock()
	delete(dreamStates, taskID)
}

// ---------------------------------------------------------------------------
// DreamTaskKiller
// ---------------------------------------------------------------------------

// DreamTaskKiller implements TaskKiller for dream tasks.
type DreamTaskKiller struct {
	logger *slog.Logger
}

// NewDreamTaskKiller creates a killer for dream tasks.
func NewDreamTaskKiller(logger *slog.Logger) *DreamTaskKiller {
	return &DreamTaskKiller{logger: logger}
}

func (k *DreamTaskKiller) Name() string   { return "DreamTask" }
func (k *DreamTaskKiller) Type() TaskType { return TaskTypeDream }

// Kill stops a dream task and rolls back the consolidation lock.
func (k *DreamTaskKiller) Kill(taskID string, registry *TaskRegistry) error {
	state, ok := registry.Get(taskID)
	if !ok {
		return fmt.Errorf("dream task %s not found", taskID)
	}
	if state.Status != StatusRunning {
		return nil
	}

	dreamState := getDreamState(taskID)

	now := time.Now()
	registry.Update(taskID, func(t *TaskState) {
		t.Status = StatusKilled
		t.EndTime = &now
		t.Notified = true
	})

	// Rollback the consolidation lock mtime so the next session can retry.
	if dreamState != nil && dreamState.PriorMtime > 0 {
		rollbackConsolidationLock(dreamState.PriorMtime)
	}

	k.logger.Info("dream task killed", "task_id", taskID)
	removeDreamState(taskID)
	return nil
}

// ---------------------------------------------------------------------------
// RegisterDreamTask — register a new dream task
// ---------------------------------------------------------------------------

// RegisterDreamTask creates and registers a new dream task.
func RegisterDreamTask(
	registry *TaskRegistry,
	sessionsReviewing int,
	priorMtime int64,
	logger *slog.Logger,
) string {
	state := NewTaskState(TaskTypeDream, "dreaming")
	state.Status = StatusRunning

	dreamState := &DreamState{
		Phase:             DreamPhaseStarting,
		SessionsReviewing: sessionsReviewing,
		PriorMtime:        priorMtime,
	}
	setDreamState(state.ID, dreamState)

	registry.Register(state)

	logger.Info("dream task registered",
		"task_id", state.ID,
		"sessions_reviewing", sessionsReviewing,
	)
	return state.ID
}

// ---------------------------------------------------------------------------
// AddDreamTurn — record an assistant turn
// ---------------------------------------------------------------------------

// AddDreamTurn adds a turn to the dream task and updates touched paths.
func AddDreamTurn(taskID string, turn DreamTurn, touchedPaths []string) {
	dreamState := getDreamState(taskID)
	if dreamState == nil {
		return
	}

	dreamState.mu.Lock()
	defer dreamState.mu.Unlock()

	// Track newly touched files
	seen := make(map[string]bool, len(dreamState.FilesTouched))
	for _, p := range dreamState.FilesTouched {
		seen[p] = true
	}
	var newPaths []string
	for _, p := range touchedPaths {
		if !seen[p] {
			seen[p] = true
			newPaths = append(newPaths, p)
		}
	}

	// Skip empty no-op updates
	if turn.Text == "" && turn.ToolUseCount == 0 && len(newPaths) == 0 {
		return
	}

	if len(newPaths) > 0 {
		dreamState.Phase = DreamPhaseUpdating
		dreamState.FilesTouched = append(dreamState.FilesTouched, newPaths...)
	}

	// Keep only MaxDreamTurns recent turns
	if len(dreamState.Turns) >= MaxDreamTurns {
		dreamState.Turns = dreamState.Turns[1:]
	}
	dreamState.Turns = append(dreamState.Turns, turn)
}

// ---------------------------------------------------------------------------
// CompleteDreamTask
// ---------------------------------------------------------------------------

// CompleteDreamTask marks a dream task as completed.
func CompleteDreamTask(registry *TaskRegistry, taskID string) {
	now := time.Now()
	registry.Update(taskID, func(t *TaskState) {
		t.Status = StatusCompleted
		t.EndTime = &now
		t.Notified = true
	})
	removeDreamState(taskID)
}

// FailDreamTask marks a dream task as failed.
func FailDreamTask(registry *TaskRegistry, taskID string) {
	now := time.Now()
	registry.Update(taskID, func(t *TaskState) {
		t.Status = StatusFailed
		t.EndTime = &now
		t.Notified = true
	})
	removeDreamState(taskID)
}

// ---------------------------------------------------------------------------
// ExtractInsights — extract insights from conversation
// ---------------------------------------------------------------------------

// Insight is a learned fact extracted from conversation.
type Insight struct {
	Category string `json:"category"` // "pattern", "preference", "convention", "fact"
	Content  string `json:"content"`
	Source   string `json:"source"` // session ID or description
}

// ExtractInsights parses conversation content and extracts reusable insights.
// This is a simplified version — in production this would use the LLM to extract.
func ExtractInsights(conversationText string) []Insight {
	var insights []Insight

	// Look for common patterns in the conversation
	lines := strings.Split(conversationText, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Pattern: "always use X" or "prefer X over Y"
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "always use") ||
			strings.Contains(lower, "prefer ") ||
			strings.Contains(lower, "convention:") ||
			strings.Contains(lower, "rule:") ||
			strings.Contains(lower, "pattern:") {
			insights = append(insights, Insight{
				Category: "convention",
				Content:  trimmed,
			})
		}

		// Pattern: user corrections
		if strings.Contains(lower, "actually,") ||
			strings.Contains(lower, "instead,") ||
			strings.Contains(lower, "no, use") ||
			strings.Contains(lower, "don't do that") {
			insights = append(insights, Insight{
				Category: "preference",
				Content:  trimmed,
			})
		}
	}

	return insights
}

// ---------------------------------------------------------------------------
// SaveToMemory — persist learned facts
// ---------------------------------------------------------------------------

// SaveToMemory persists insights to the auto-memory directory.
// Each insight is saved as a JSON entry in the memory file.
func SaveToMemory(memoryDir string, insights []Insight) error {
	if len(insights) == 0 {
		return nil
	}

	if err := os.MkdirAll(memoryDir, 0o700); err != nil {
		return fmt.Errorf("creating memory dir: %w", err)
	}

	filename := fmt.Sprintf("dream_%s.json", time.Now().Format("20060102_150405"))
	path := filepath.Join(memoryDir, filename)

	data, err := json.MarshalIndent(insights, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling insights: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing memory file: %w", err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Consolidation lock helpers
// ---------------------------------------------------------------------------

// rollbackConsolidationLock restores the lock file mtime so the next session
// can retry dream consolidation.
func rollbackConsolidationLock(priorMtime int64) {
	// The lock file path would be determined by the session/config system.
	// This is a stub that maintains the interface contract.
	_ = priorMtime
}
