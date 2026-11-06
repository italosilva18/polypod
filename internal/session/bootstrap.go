package session

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// BootstrapState holds session-level state initialized at startup.
// It is the Go equivalent of the TS bootstrap/state.ts global STATE.
type BootstrapState struct {
	mu sync.RWMutex

	sessionID     string
	originalCwd   string
	projectRoot   string
	cwd           string
	startTime     time.Time
	isInteractive bool

	// Counters — use atomics for hot-path increments.
	totalCostUSD        float64
	totalAPIDuration    int64 // nanoseconds
	totalToolDuration   int64 // nanoseconds
	totalLinesAdded     int64
	totalLinesRemoved   int64
	turnToolCount       int64
	turnHookCount       int64
	lastInteractionTime atomic.Int64 // unix millis

	// Metadata.
	clientType        string
	mainLoopModel     string
	modelOverride     string
	mainThreadAgent   string
	parentSessionID   string
	sessionProjectDir string
}

// NewBootstrapState creates a BootstrapState with a fresh session ID and
// the current working directory.
func NewBootstrapState() *BootstrapState {
	cwd, _ := os.Getwd()
	// Resolve symlinks.
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}

	bs := &BootstrapState{
		sessionID:   uuid.New().String(),
		originalCwd: cwd,
		projectRoot: cwd,
		cwd:         cwd,
		startTime:   time.Now(),
		clientType:  "cli",
	}
	bs.lastInteractionTime.Store(time.Now().UnixMilli())
	return bs
}

// InitBootstrap initializes the bootstrap state. This is typically called
// once at application startup.
func InitBootstrap() *BootstrapState {
	return NewBootstrapState()
}

// GetSessionID returns the current session's unique identifier.
func (bs *BootstrapState) GetSessionID() string {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	return bs.sessionID
}

// RegenerateSessionID creates a new session ID, optionally preserving the
// current one as the parent session ID.
func (bs *BootstrapState) RegenerateSessionID(setCurrentAsParent bool) string {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if setCurrentAsParent {
		bs.parentSessionID = bs.sessionID
	}
	bs.sessionID = uuid.New().String()
	bs.sessionProjectDir = ""
	return bs.sessionID
}

// SwitchSession atomically updates the session ID and its project directory.
func (bs *BootstrapState) SwitchSession(sessionID string, projectDir string) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.sessionID = sessionID
	bs.sessionProjectDir = projectDir
}

// GetParentSessionID returns the parent session ID (set during regeneration).
func (bs *BootstrapState) GetParentSessionID() string {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	return bs.parentSessionID
}

// GetProjectRoot returns the stable project root directory.
func (bs *BootstrapState) GetProjectRoot() string {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	return bs.projectRoot
}

// SetProjectRoot sets the project root (only at startup or --worktree).
func (bs *BootstrapState) SetProjectRoot(root string) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.projectRoot = root
}

// GetOriginalCwd returns the cwd at startup.
func (bs *BootstrapState) GetOriginalCwd() string {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	return bs.originalCwd
}

// SetOriginalCwd updates the original cwd.
func (bs *BootstrapState) SetOriginalCwd(cwd string) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.originalCwd = cwd
}

// GetCwd returns the current working directory.
func (bs *BootstrapState) GetCwd() string {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	return bs.cwd
}

// SetCwd updates the cwd.
func (bs *BootstrapState) SetCwd(cwd string) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.cwd = cwd
}

// GetSessionProjectDir returns the directory containing the session's JSONL.
func (bs *BootstrapState) GetSessionProjectDir() string {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	return bs.sessionProjectDir
}

// GetStartTime returns when the session started.
func (bs *BootstrapState) GetStartTime() time.Time {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	return bs.startTime
}

// GetTotalDuration returns wall-clock time since session start.
func (bs *BootstrapState) GetTotalDuration() time.Duration {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	return time.Since(bs.startTime)
}

// IsInteractive returns true if this is an interactive session.
func (bs *BootstrapState) IsInteractive() bool {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	return bs.isInteractive
}

// SetInteractive marks the session as interactive or non-interactive.
func (bs *BootstrapState) SetInteractive(v bool) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.isInteractive = v
}

// GetClientType returns the client type string (e.g. "cli", "sdk", "vscode").
func (bs *BootstrapState) GetClientType() string {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	return bs.clientType
}

// SetClientType sets the client type.
func (bs *BootstrapState) SetClientType(ct string) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.clientType = ct
}

// GetMainLoopModel returns the current main loop model.
func (bs *BootstrapState) GetMainLoopModel() string {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	return bs.mainLoopModel
}

// SetMainLoopModel sets the main loop model.
func (bs *BootstrapState) SetMainLoopModel(model string) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.mainLoopModel = model
}

// GetModelOverride returns the model override (from --model flag).
func (bs *BootstrapState) GetModelOverride() string {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	return bs.modelOverride
}

// SetModelOverride sets the model override.
func (bs *BootstrapState) SetModelOverride(model string) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.modelOverride = model
}

// GetMainThreadAgent returns the main thread agent type.
func (bs *BootstrapState) GetMainThreadAgent() string {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	return bs.mainThreadAgent
}

// SetMainThreadAgent sets the main thread agent type.
func (bs *BootstrapState) SetMainThreadAgent(agentType string) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.mainThreadAgent = agentType
}

// --- Counter operations ---

// IncrementCounter adds to a named counter. Supported names:
// "cost", "api_duration", "tool_duration", "lines_added", "lines_removed",
// "turn_tools", "turn_hooks".
func (bs *BootstrapState) IncrementCounter(name string, value int64) {
	switch name {
	case "cost":
		bs.mu.Lock()
		bs.totalCostUSD += float64(value) / 1_000_000 // microdollars
		bs.mu.Unlock()
	case "api_duration":
		atomic.AddInt64(&bs.totalAPIDuration, value)
	case "tool_duration":
		atomic.AddInt64(&bs.totalToolDuration, value)
	case "lines_added":
		atomic.AddInt64(&bs.totalLinesAdded, value)
	case "lines_removed":
		atomic.AddInt64(&bs.totalLinesRemoved, value)
	case "turn_tools":
		atomic.AddInt64(&bs.turnToolCount, value)
	case "turn_hooks":
		atomic.AddInt64(&bs.turnHookCount, value)
	}
}

// AddCost adds to the total cost in USD.
func (bs *BootstrapState) AddCost(usd float64) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.totalCostUSD += usd
}

// GetTotalCostUSD returns the cumulative cost.
func (bs *BootstrapState) GetTotalCostUSD() float64 {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	return bs.totalCostUSD
}

// AddAPIDuration adds to the total API duration.
func (bs *BootstrapState) AddAPIDuration(d time.Duration) {
	atomic.AddInt64(&bs.totalAPIDuration, d.Nanoseconds())
}

// GetTotalAPIDuration returns the cumulative API duration.
func (bs *BootstrapState) GetTotalAPIDuration() time.Duration {
	return time.Duration(atomic.LoadInt64(&bs.totalAPIDuration))
}

// AddToolDuration adds to the total tool execution duration.
func (bs *BootstrapState) AddToolDuration(d time.Duration) {
	atomic.AddInt64(&bs.totalToolDuration, d.Nanoseconds())
}

// GetTotalToolDuration returns the cumulative tool duration.
func (bs *BootstrapState) GetTotalToolDuration() time.Duration {
	return time.Duration(atomic.LoadInt64(&bs.totalToolDuration))
}

// AddLinesChanged records lines added and removed.
func (bs *BootstrapState) AddLinesChanged(added, removed int64) {
	atomic.AddInt64(&bs.totalLinesAdded, added)
	atomic.AddInt64(&bs.totalLinesRemoved, removed)
}

// GetTotalLinesAdded returns cumulative lines added.
func (bs *BootstrapState) GetTotalLinesAdded() int64 {
	return atomic.LoadInt64(&bs.totalLinesAdded)
}

// GetTotalLinesRemoved returns cumulative lines removed.
func (bs *BootstrapState) GetTotalLinesRemoved() int64 {
	return atomic.LoadInt64(&bs.totalLinesRemoved)
}

// UpdateLastInteractionTime records the current time as the last interaction.
func (bs *BootstrapState) UpdateLastInteractionTime() {
	bs.lastInteractionTime.Store(time.Now().UnixMilli())
}

// GetLastInteractionTime returns the last interaction time.
func (bs *BootstrapState) GetLastInteractionTime() time.Time {
	ms := bs.lastInteractionTime.Load()
	return time.UnixMilli(ms)
}

// ResetTurnCounters resets per-turn counters to zero.
func (bs *BootstrapState) ResetTurnCounters() {
	atomic.StoreInt64(&bs.turnToolCount, 0)
	atomic.StoreInt64(&bs.turnHookCount, 0)
}

// GetTurnToolCount returns the number of tools used in the current turn.
func (bs *BootstrapState) GetTurnToolCount() int64 {
	return atomic.LoadInt64(&bs.turnToolCount)
}

// GetTurnHookCount returns the number of hooks fired in the current turn.
func (bs *BootstrapState) GetTurnHookCount() int64 {
	return atomic.LoadInt64(&bs.turnHookCount)
}

// ResetCostState resets all cost and duration counters (used on /clear).
func (bs *BootstrapState) ResetCostState() {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.totalCostUSD = 0
	bs.startTime = time.Now()
	atomic.StoreInt64(&bs.totalAPIDuration, 0)
	atomic.StoreInt64(&bs.totalToolDuration, 0)
	atomic.StoreInt64(&bs.totalLinesAdded, 0)
	atomic.StoreInt64(&bs.totalLinesRemoved, 0)
}
