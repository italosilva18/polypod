package state

import (
	"sync"
	"time"
)

// AppState represents the main application state.
// Migrated from Claude Code's state/AppStateStore.ts
type AppState struct {
	// Session information
	SessionID      string
	WorkingDir     string
	IsInitialized  bool
	IsShuttingDown bool

	// User and system context
	UserContext   map[string]string
	SystemContext map[string]string

	// Application settings
	Settings    map[string]interface{}
	Model       string
	Temperature float32
	MaxTokens   int

	// Conversation state
	Messages   []map[string]interface{}
	TokenCount int
	TurnCount  int

	// Agent and task management
	ActiveAgent     string
	AvailableAgents []string
	AgentContexts   map[string]interface{}
	Tasks           []TaskState
	BackgroundTasks []BackgroundTaskState

	// Tool and permission state
	AvailableTools    []string
	EnabledTools      []string
	PermissionMode    string
	PermissionContext map[string]interface{}

	// MCP and LSP connections
	MCPConnections []MCPConnection
	LSPConnections []LSPConnection

	// Plugin state
	LoadedPlugins []PluginInfo
	PluginErrors  []PluginError

	// Notifications and alerts
	Notifications []Notification
	Alerts        []Alert

	// Telemetry and metrics
	Telemetry TelemetryState
	Usage     UsageState
	Cost      CostState

	// UI state
	UIDeprecated bool
	StatusLine   string
	Theme        string
	Keybindings  map[string]string

	// Timestamps
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LastActivity time.Time
}

// TaskState represents a task in the task list.
type TaskState struct {
	ID        string
	Content   string
	Status    string // pending, in_progress, completed, cancelled
	Priority  string // high, medium, low
	CreatedAt time.Time
	UpdatedAt time.Time
}

// BackgroundTaskState represents a background task.
type BackgroundTaskState struct {
	ID          string
	Type        string
	Status      string
	Description string
	Result      string
	Error       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// MCPConnection represents an MCP server connection.
type MCPConnection struct {
	Name      string
	URL       string
	Status    string // connected, disconnected, error
	Tools     []string
	Resources []string
	CreatedAt time.Time
}

// LSPConnection represents an LSP server connection.
type LSPConnection struct {
	Name         string
	Command      []string
	Status       string // running, stopped, error
	Capabilities map[string]interface{}
	CreatedAt    time.Time
}

// PluginInfo represents information about a loaded plugin.
type PluginInfo struct {
	Name        string
	Version     string
	Description string
	Commands    []string
	Agents      []string
	Skills      []string
	LoadedAt    time.Time
}

// PluginError represents an error loading or running a plugin.
type PluginError struct {
	PluginName string
	Error      string
	Timestamp  time.Time
}

// Notification represents a user notification.
type Notification struct {
	ID        string
	Title     string
	Message   string
	Type      string // info, warning, error, success
	Timestamp time.Time
}

// Alert represents a system alert.
type Alert struct {
	ID        string
	Title     string
	Message   string
	Severity  string // low, medium, high, critical
	Timestamp time.Time
}

// TelemetryState tracks telemetry and metrics.
type TelemetryState struct {
	EventsSent    int
	EventsFailed  int
	LastEventSent time.Time
	SessionID     string
	DistinctID    string
	FeatureFlags  map[string]bool
}

// UsageState tracks API usage.
type UsageState struct {
	InputTokens   int
	OutputTokens  int
	TotalTokens   int
	TotalCostUSD  float64
	RequestsCount int
	LastRequest   time.Time
}

// CostState tracks cost information.
type CostState struct {
	TotalUSD       float64
	DailyUSD       float64
	MonthlyUSD     float64
	BudgetLimitUSD float64
	IsOverBudget   bool
	LastReset      time.Time
}

// AppStateStore manages the application state with reactive updates.
// Migrated from Claude Code's state/AppStateStore.ts
type AppStateStore struct {
	mu          sync.RWMutex
	state       AppState
	subscribers []func(AppState, AppState) // oldState, newState
}

// NewAppStateStore creates a new application state store.
func NewAppStateStore() *AppStateStore {
	return &AppStateStore{
		state: AppState{
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
			LastActivity:      time.Now(),
			Settings:          make(map[string]interface{}),
			UserContext:       make(map[string]string),
			SystemContext:     make(map[string]string),
			AgentContexts:     make(map[string]interface{}),
			Tasks:             make([]TaskState, 0),
			BackgroundTasks:   make([]BackgroundTaskState, 0),
			AvailableTools:    make([]string, 0),
			EnabledTools:      make([]string, 0),
			PermissionContext: make(map[string]interface{}),
			MCPConnections:    make([]MCPConnection, 0),
			LSPConnections:    make([]LSPConnection, 0),
			LoadedPlugins:     make([]PluginInfo, 0),
			PluginErrors:      make([]PluginError, 0),
			Notifications:     make([]Notification, 0),
			Alerts:            make([]Alert, 0),
			Telemetry:         TelemetryState{FeatureFlags: make(map[string]bool)},
			Usage:             UsageState{},
			Cost:              CostState{},
			Keybindings:       make(map[string]string),
		},
		subscribers: make([]func(AppState, AppState), 0),
	}
}

// Get returns the current application state.
func (s *AppStateStore) Get() AppState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// Set updates the application state and notifies subscribers.
func (s *AppStateStore) Set(newState AppState) {
	s.mu.Lock()
	oldState := s.state
	s.state = newState
	s.state.UpdatedAt = time.Now()
	s.mu.Unlock()

	s.mu.RLock()
	subscribers := s.subscribers
	s.mu.RUnlock()

	for _, sub := range subscribers {
		sub(oldState, s.state)
	}
}

// Subscribe registers a function to be called whenever the state changes.
// Returns an unsubscribe function.
func (s *AppStateStore) Subscribe(subscriber func(AppState, AppState)) func() {
	s.mu.Lock()
	id := len(s.subscribers)
	s.subscribers = append(s.subscribers, subscriber)
	s.mu.Unlock()

	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if id < len(s.subscribers) {
			s.subscribers = append(s.subscribers[:id], s.subscribers[id+1:]...)
		}
	}
}

// Update applies an updater function to the current state.
func (s *AppStateStore) Update(updater func(AppState) AppState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	oldState := s.state
	newState := updater(s.state)
	newState.UpdatedAt = time.Now()
	s.state = newState
	s.mu.Unlock()

	s.mu.RLock()
	subscribers := s.subscribers
	s.mu.RUnlock()

	for _, sub := range subscribers {
		sub(oldState, newState)
	}
}

// GetSessionID returns the current session ID.
func (s *AppStateStore) GetSessionID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.SessionID
}

// SetSessionID sets the session ID.
func (s *AppStateStore) SetSessionID(id string) {
	s.Update(func(state AppState) AppState {
		state.SessionID = id
		return state
	})
}

// GetWorkingDir returns the current working directory.
func (s *AppStateStore) GetWorkingDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.WorkingDir
}

// SetWorkingDir sets the working directory.
func (s *AppStateStore) SetWorkingDir(dir string) {
	s.Update(func(state AppState) AppState {
		state.WorkingDir = dir
		return state
	})
}

// GetModel returns the current model.
func (s *AppStateStore) GetModel() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Model
}

// SetModel sets the model.
func (s *AppStateStore) SetModel(model string) {
	s.Update(func(state AppState) AppState {
		state.Model = model
		return state
	})
}

// GetMessages returns the conversation messages.
func (s *AppStateStore) GetMessages() []map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]map[string]interface{}, len(s.state.Messages))
	copy(result, s.state.Messages)
	return result
}

// AddMessage adds a message to the conversation.
func (s *AppStateStore) AddMessage(message map[string]interface{}) {
	s.Update(func(state AppState) AppState {
		state.Messages = append(state.Messages, message)
		state.LastActivity = time.Now()
		return state
	})
}

// GetTasks returns the task list.
func (s *AppStateStore) GetTasks() []TaskState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]TaskState, len(s.state.Tasks))
	copy(result, s.state.Tasks)
	return result
}

// AddTask adds a new task.
func (s *AppStateStore) AddTask(task TaskState) {
	s.Update(func(state AppState) AppState {
		state.Tasks = append(state.Tasks, task)
		return state
	})
}

// UpdateTask updates an existing task.
func (s *AppStateStore) UpdateTask(id string, updater func(TaskState) TaskState) {
	s.Update(func(state AppState) AppState {
		for i := range state.Tasks {
			if state.Tasks[i].ID == id {
				state.Tasks[i] = updater(state.Tasks[i])
				break
			}
		}
		return state
	})
}

// RemoveTask removes a task by ID.
func (s *AppStateStore) RemoveTask(id string) {
	s.Update(func(state AppState) AppState {
		for i := range state.Tasks {
			if state.Tasks[i].ID == id {
				state.Tasks = append(state.Tasks[:i], state.Tasks[i+1:]...)
				break
			}
		}
		return state
	})
}

// ClearTasks removes all tasks.
func (s *AppStateStore) ClearTasks() {
	s.Update(func(state AppState) AppState {
		state.Tasks = make([]TaskState, 0)
		return state
	})
}

// GetAvailableTools returns the list of available tools.
func (s *AppStateStore) GetAvailableTools() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]string, len(s.state.AvailableTools))
	copy(result, s.state.AvailableTools)
	return result
}

// SetAvailableTools sets the list of available tools.
func (s *AppStateStore) SetAvailableTools(tools []string) {
	s.Update(func(state AppState) AppState {
		state.AvailableTools = tools
		return state
	})
}

// GetEnabledTools returns the list of enabled tools.
func (s *AppStateStore) GetEnabledTools() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]string, len(s.state.EnabledTools))
	copy(result, s.state.EnabledTools)
	return result
}

// SetEnabledTools sets the list of enabled tools.
func (s *AppStateStore) SetEnabledTools(tools []string) {
	s.Update(func(state AppState) AppState {
		state.EnabledTools = tools
		return state
	})
}

// GetPermissionMode returns the current permission mode.
func (s *AppStateStore) GetPermissionMode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.PermissionMode
}

// SetPermissionMode sets the permission mode.
func (s *AppStateStore) SetPermissionMode(mode string) {
	s.Update(func(state AppState) AppState {
		state.PermissionMode = mode
		return state
	})
}

// GetNotifications returns the notifications.
func (s *AppStateStore) GetNotifications() []Notification {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Notification, len(s.state.Notifications))
	copy(result, s.state.Notifications)
	return result
}

// RemoveNotification removes a notification by ID.
func (s *AppStateStore) RemoveNotification(id string) {
	s.Update(func(state AppState) AppState {
		for i := range state.Notifications {
			if state.Notifications[i].ID == id {
				state.Notifications = append(state.Notifications[:i], state.Notifications[i+1:]...)
				break
			}
		}
		return state
	})
}

// ClearNotifications removes all notifications.
func (s *AppStateStore) ClearNotifications() {
	s.Update(func(state AppState) AppState {
		state.Notifications = make([]Notification, 0)
		return state
	})
}

// GetTelemetry returns the telemetry state.
func (s *AppStateStore) GetTelemetry() TelemetryState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Telemetry
}

// UpdateTelemetry updates the telemetry state.
func (s *AppStateStore) UpdateTelemetry(updater func(TelemetryState) TelemetryState) {
	s.Update(func(state AppState) AppState {
		state.Telemetry = updater(state.Telemetry)
		return state
	})
}

// GetUsage returns the usage state.
func (s *AppStateStore) GetUsage() UsageState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Usage
}

// UpdateUsage updates the usage state.
func (s *AppStateStore) UpdateUsage(updater func(UsageState) UsageState) {
	s.Update(func(state AppState) AppState {
		state.Usage = updater(state.Usage)
		return state
	})
}

// GetCost returns the cost state.
func (s *AppStateStore) GetCost() CostState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Cost
}

// UpdateCost updates the cost state.
func (s *AppStateStore) UpdateCost(updater func(CostState) CostState) {
	s.Update(func(state AppState) AppState {
		state.Cost = updater(state.Cost)
		return state
	})
}

// IsInitialized returns whether the application is initialized.
func (s *AppStateStore) IsInitialized() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.IsInitialized
}

// SetInitialized sets whether the application is initialized.
func (s *AppStateStore) SetInitialized(initialized bool) {
	s.Update(func(state AppState) AppState {
		state.IsInitialized = initialized
		return state
	})
}

// IsShuttingDown returns whether the application is shutting down.
func (s *AppStateStore) IsShuttingDown() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.IsShuttingDown
}

// SetShuttingDown sets whether the application is shutting down.
func (s *AppStateStore) SetShuttingDown(shuttingDown bool) {
	s.Update(func(state AppState) AppState {
		state.IsShuttingDown = shuttingDown
		return state
	})
}

// Stats returns basic statistics about the state.
func (s *AppStateStore) Stats() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return map[string]interface{}{
		"session_id":            s.state.SessionID,
		"is_initialized":        s.state.IsInitialized,
		"is_shutting_down":      s.state.IsShuttingDown,
		"message_count":         len(s.state.Messages),
		"task_count":            len(s.state.Tasks),
		"background_task_count": len(s.state.BackgroundTasks),
		"available_tools_count": len(s.state.AvailableTools),
		"enabled_tools_count":   len(s.state.EnabledTools),
		"loaded_plugins_count":  len(s.state.LoadedPlugins),
		"plugin_errors_count":   len(s.state.PluginErrors),
		"notification_count":    len(s.state.Notifications),
		"alert_count":           len(s.state.Alerts),
		"telemetry_events_sent": s.state.Telemetry.EventsSent,
		"usage_total_tokens":    s.state.Usage.TotalTokens,
		"cost_total_usd":        s.state.Cost.TotalUSD,
		"last_activity":         s.state.LastActivity,
		"created_at":            s.state.CreatedAt,
		"updated_at":            s.state.UpdatedAt,
	}
}

// DefaultAppState returns a default application state.
func DefaultAppState() AppState {
	return AppState{
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
		LastActivity:      time.Now(),
		SessionID:         "",
		WorkingDir:        "",
		IsInitialized:     false,
		IsShuttingDown:    false,
		Settings:          make(map[string]interface{}),
		UserContext:       make(map[string]string),
		SystemContext:     make(map[string]string),
		Model:             "claude-3-opus-20240229",
		Temperature:       0.7,
		MaxTokens:         4096,
		Messages:          make([]map[string]interface{}, 0),
		TokenCount:        0,
		TurnCount:         0,
		ActiveAgent:       "",
		AvailableAgents:   make([]string, 0),
		AgentContexts:     make(map[string]interface{}),
		Tasks:             make([]TaskState, 0),
		BackgroundTasks:   make([]BackgroundTaskState, 0),
		AvailableTools:    make([]string, 0),
		EnabledTools:      make([]string, 0),
		PermissionMode:    "default",
		PermissionContext: make(map[string]interface{}),
		MCPConnections:    make([]MCPConnection, 0),
		LSPConnections:    make([]LSPConnection, 0),
		LoadedPlugins:     make([]PluginInfo, 0),
		PluginErrors:      make([]PluginError, 0),
		Notifications:     make([]Notification, 0),
		Alerts:            make([]Alert, 0),
		Telemetry:         TelemetryState{FeatureFlags: make(map[string]bool)},
		Usage:             UsageState{},
		Cost:              CostState{},
		Keybindings:       make(map[string]string),
	}
}
