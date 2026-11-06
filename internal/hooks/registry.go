package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// --- Session Hook Registry ---
// Manages session-scoped hooks (both command hooks and function hooks).
// Thread-safe. Hooks are grouped by session ID, event, and matcher.

// FunctionHookCallback is a Go function hook callback.
// Returns true if the check passes, false to block.
type FunctionHookCallback func(ctx context.Context) (bool, error)

// FunctionHook is a session-scoped function hook with an embedded callback.
// Cannot be persisted to settings; session-lifetime only.
type FunctionHook struct {
	ID            string
	Timeout       time.Duration
	Callback      FunctionHookCallback
	ErrorMessage  string
	StatusMessage string
}

// OnHookSuccess is called after a hook executes successfully.
type OnHookSuccess func(hook interface{}, result *AggregatedHookResult)

// SessionHookEntry wraps a hook with its optional success callback.
type SessionHookEntry struct {
	CommandHook  *HookCommand
	FunctionHook *FunctionHook
	OnSuccess    OnHookSuccess
}

// IsFunction returns true if this entry wraps a function hook.
func (e *SessionHookEntry) IsFunction() bool {
	return e.FunctionHook != nil
}

// SessionHookMatcher groups hooks under a matcher pattern with an optional skill root.
type SessionHookMatcher struct {
	Matcher   string
	SkillRoot string
	Entries   []SessionHookEntry
}

// SessionStore holds all hooks for a single session.
type SessionStore struct {
	Hooks map[HookEvent][]SessionHookMatcher
}

// SessionHookRegistry manages session-scoped hooks across multiple sessions.
// Thread-safe via sync.RWMutex. Uses a map (not copy-on-write) for O(1) updates.
type SessionHookRegistry struct {
	mu       sync.RWMutex
	sessions map[string]*SessionStore
	logger   *slog.Logger
}

// NewSessionHookRegistry creates a new session hook registry.
func NewSessionHookRegistry(logger *slog.Logger) *SessionHookRegistry {
	return &SessionHookRegistry{
		sessions: make(map[string]*SessionStore),
		logger:   logger,
	}
}

// AddSessionHook registers a command hook for a session.
func (r *SessionHookRegistry) AddSessionHook(
	sessionID string,
	event HookEvent,
	matcher string,
	hook HookCommand,
	onSuccess OnHookSuccess,
	skillRoot string,
) {
	r.mu.Lock()
	defer r.mu.Unlock()

	store := r.getOrCreateStore(sessionID)
	entry := SessionHookEntry{
		CommandHook: &hook,
		OnSuccess:   onSuccess,
	}
	r.addEntryToStore(store, event, matcher, skillRoot, entry)

	r.logger.Debug("added session hook",
		"session_id", sessionID,
		"event", event,
		"matcher", matcher,
	)
}

// AddFunctionHook registers a function hook for a session. Returns the hook ID.
func (r *SessionHookRegistry) AddFunctionHook(
	sessionID string,
	event HookEvent,
	matcher string,
	callback FunctionHookCallback,
	errorMessage string,
	timeout time.Duration,
	id string,
) string {
	if id == "" {
		id = fmt.Sprintf("function-hook-%d", time.Now().UnixNano())
	}
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	hook := &FunctionHook{
		ID:           id,
		Timeout:      timeout,
		Callback:     callback,
		ErrorMessage: errorMessage,
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	store := r.getOrCreateStore(sessionID)
	entry := SessionHookEntry{
		FunctionHook: hook,
	}
	r.addEntryToStore(store, event, matcher, "", entry)

	r.logger.Debug("added function hook",
		"session_id", sessionID,
		"event", event,
		"hook_id", id,
	)
	return id
}

// RemoveFunctionHook removes a function hook by ID from a session.
func (r *SessionHookRegistry) RemoveFunctionHook(sessionID string, event HookEvent, hookID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	store := r.sessions[sessionID]
	if store == nil {
		return
	}

	matchers := store.Hooks[event]
	updatedMatchers := make([]SessionHookMatcher, 0, len(matchers))
	for _, m := range matchers {
		updatedEntries := make([]SessionHookEntry, 0, len(m.Entries))
		for _, e := range m.Entries {
			if e.FunctionHook != nil && e.FunctionHook.ID == hookID {
				continue
			}
			updatedEntries = append(updatedEntries, e)
		}
		if len(updatedEntries) > 0 {
			m.Entries = updatedEntries
			updatedMatchers = append(updatedMatchers, m)
		}
	}

	if len(updatedMatchers) > 0 {
		store.Hooks[event] = updatedMatchers
	} else {
		delete(store.Hooks, event)
	}

	r.logger.Debug("removed function hook",
		"session_id", sessionID,
		"event", event,
		"hook_id", hookID,
	)
}

// RemoveSessionHook removes a specific command hook from a session.
func (r *SessionHookRegistry) RemoveSessionHook(sessionID string, event HookEvent, hook HookCommand) {
	r.mu.Lock()
	defer r.mu.Unlock()

	store := r.sessions[sessionID]
	if store == nil {
		return
	}

	matchers := store.Hooks[event]
	updatedMatchers := make([]SessionHookMatcher, 0, len(matchers))
	for _, m := range matchers {
		updatedEntries := make([]SessionHookEntry, 0, len(m.Entries))
		for _, e := range m.Entries {
			if e.CommandHook != nil && isHookEqual(*e.CommandHook, hook) {
				continue
			}
			updatedEntries = append(updatedEntries, e)
		}
		if len(updatedEntries) > 0 {
			m.Entries = updatedEntries
			updatedMatchers = append(updatedMatchers, m)
		}
	}

	if len(updatedMatchers) > 0 {
		store.Hooks[event] = updatedMatchers
	} else {
		delete(store.Hooks, event)
	}

	r.logger.Debug("removed session hook",
		"session_id", sessionID,
		"event", event,
	)
}

// GetSessionHooks returns all command hooks (not function hooks) for a session and event.
func (r *SessionHookRegistry) GetSessionHooks(sessionID string, event HookEvent) []SessionDerivedHookMatcher {
	r.mu.RLock()
	defer r.mu.RUnlock()

	store := r.sessions[sessionID]
	if store == nil {
		return nil
	}

	matchers := store.Hooks[event]
	result := make([]SessionDerivedHookMatcher, 0, len(matchers))
	for _, m := range matchers {
		var hooks []HookCommand
		for _, e := range m.Entries {
			if e.CommandHook != nil {
				hooks = append(hooks, *e.CommandHook)
			}
		}
		if len(hooks) > 0 {
			result = append(result, SessionDerivedHookMatcher{
				Matcher:   m.Matcher,
				SkillRoot: m.SkillRoot,
				Hooks:     hooks,
			})
		}
	}
	return result
}

// GetAllSessionHooks returns command hooks for all events in a session.
func (r *SessionHookRegistry) GetAllSessionHooks(sessionID string) map[HookEvent][]SessionDerivedHookMatcher {
	r.mu.RLock()
	defer r.mu.RUnlock()

	store := r.sessions[sessionID]
	if store == nil {
		return nil
	}

	result := make(map[HookEvent][]SessionDerivedHookMatcher)
	for event, matchers := range store.Hooks {
		for _, m := range matchers {
			var hooks []HookCommand
			for _, e := range m.Entries {
				if e.CommandHook != nil {
					hooks = append(hooks, *e.CommandHook)
				}
			}
			if len(hooks) > 0 {
				result[event] = append(result[event], SessionDerivedHookMatcher{
					Matcher:   m.Matcher,
					SkillRoot: m.SkillRoot,
					Hooks:     hooks,
				})
			}
		}
	}
	return result
}

// GetFunctionHooks returns all function hooks for a session and event.
func (r *SessionHookRegistry) GetFunctionHooks(sessionID string, event HookEvent) []FunctionHookMatcher {
	r.mu.RLock()
	defer r.mu.RUnlock()

	store := r.sessions[sessionID]
	if store == nil {
		return nil
	}

	matchers := store.Hooks[event]
	result := make([]FunctionHookMatcher, 0)
	for _, m := range matchers {
		var hooks []*FunctionHook
		for _, e := range m.Entries {
			if e.FunctionHook != nil {
				hooks = append(hooks, e.FunctionHook)
			}
		}
		if len(hooks) > 0 {
			result = append(result, FunctionHookMatcher{
				Matcher: m.Matcher,
				Hooks:   hooks,
			})
		}
	}
	return result
}

// GetHookCallback retrieves the full hook entry including callbacks.
func (r *SessionHookRegistry) GetHookCallback(
	sessionID string,
	event HookEvent,
	matcher string,
	hook HookCommand,
) *SessionHookEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	store := r.sessions[sessionID]
	if store == nil {
		return nil
	}

	for _, m := range store.Hooks[event] {
		if m.Matcher == matcher || matcher == "" {
			for i, e := range m.Entries {
				if e.CommandHook != nil && isHookEqual(*e.CommandHook, hook) {
					return &m.Entries[i]
				}
			}
		}
	}
	return nil
}

// ClearSession removes all hooks for a session.
func (r *SessionHookRegistry) ClearSession(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, sessionID)
	r.logger.Debug("cleared session hooks", "session_id", sessionID)
}

// SessionDerivedHookMatcher is the output format for session hooks
// (excluding function hooks which can't be serialized).
type SessionDerivedHookMatcher struct {
	Matcher   string
	SkillRoot string
	Hooks     []HookCommand
}

// FunctionHookMatcher groups function hooks under a matcher.
type FunctionHookMatcher struct {
	Matcher string
	Hooks   []*FunctionHook
}

// --- Internal helpers ---

func (r *SessionHookRegistry) getOrCreateStore(sessionID string) *SessionStore {
	store := r.sessions[sessionID]
	if store == nil {
		store = &SessionStore{
			Hooks: make(map[HookEvent][]SessionHookMatcher),
		}
		r.sessions[sessionID] = store
	}
	return store
}

func (r *SessionHookRegistry) addEntryToStore(
	store *SessionStore,
	event HookEvent,
	matcher string,
	skillRoot string,
	entry SessionHookEntry,
) {
	matchers := store.Hooks[event]

	// Find existing matcher with same pattern and skill root.
	for i, m := range matchers {
		if m.Matcher == matcher && m.SkillRoot == skillRoot {
			matchers[i].Entries = append(matchers[i].Entries, entry)
			return
		}
	}

	// Create new matcher.
	store.Hooks[event] = append(matchers, SessionHookMatcher{
		Matcher:   matcher,
		SkillRoot: skillRoot,
		Entries:   []SessionHookEntry{entry},
	})
}

// isHookEqual compares two HookCommands for equality.
func isHookEqual(a, b HookCommand) bool {
	if a.Type != b.Type {
		return false
	}
	switch a.Type {
	case HookTypeCommand:
		return a.Command == b.Command
	case HookTypePrompt, HookTypeAgent:
		return a.Prompt == b.Prompt
	case HookTypeHTTP:
		return a.URL == b.URL
	}
	return false
}

// --- Async Hook Registry ---
// Manages background (async) hooks that run without blocking the main loop.

// PendingAsyncHook tracks a background hook execution.
type PendingAsyncHook struct {
	ProcessID    string
	HookID       string
	HookName     string
	HookEvent    HookEvent
	ToolName     string
	PluginID     string
	StartTime    time.Time
	Timeout      time.Duration
	Command      string
	ResponseSent bool
	Cancel       context.CancelFunc
	StopProgress func()
	Stdout       string
	Stderr       string
	ExitCode     *int
	Done         bool
}

// AsyncHookRegistry manages pending async hooks.
type AsyncHookRegistry struct {
	mu      sync.Mutex
	pending map[string]*PendingAsyncHook
	logger  *slog.Logger
}

// NewAsyncHookRegistry creates a new async hook registry.
func NewAsyncHookRegistry(logger *slog.Logger) *AsyncHookRegistry {
	return &AsyncHookRegistry{
		pending: make(map[string]*PendingAsyncHook),
		logger:  logger,
	}
}

// Register adds a pending async hook.
func (r *AsyncHookRegistry) Register(hook *PendingAsyncHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if hook.Timeout == 0 {
		hook.Timeout = time.Duration(HookTimeouts.AsyncDefault) * time.Millisecond
	}
	r.pending[hook.ProcessID] = hook
	r.logger.Debug("registered async hook",
		"process_id", hook.ProcessID,
		"hook_name", hook.HookName,
		"timeout", hook.Timeout,
	)
}

// GetPending returns all hooks that haven't had their response delivered yet.
func (r *AsyncHookRegistry) GetPending() []*PendingAsyncHook {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]*PendingAsyncHook, 0)
	for _, h := range r.pending {
		if !h.ResponseSent {
			result = append(result, h)
		}
	}
	return result
}

// AsyncHookResponse is returned when checking for completed async hooks.
type AsyncHookResponse struct {
	ProcessID string
	Response  SyncHookJSONOutput
	HookName  string
	HookEvent HookEvent
	ToolName  string
	PluginID  string
	Stdout    string
	Stderr    string
	ExitCode  *int
}

// SyncHookJSONOutput is the JSON output from a synchronous hook.
type SyncHookJSONOutput struct {
	Continue           *bool                  `json:"continue,omitempty"`
	SuppressOutput     *bool                  `json:"suppressOutput,omitempty"`
	StopReason         string                 `json:"stopReason,omitempty"`
	Decision           string                 `json:"decision,omitempty"`
	Reason             string                 `json:"reason,omitempty"`
	SystemMessage      string                 `json:"systemMessage,omitempty"`
	HookSpecificOutput map[string]interface{} `json:"hookSpecificOutput,omitempty"`
}

// AsyncHookJSONOutput is the JSON output requesting async execution.
type AsyncHookJSONOutput struct {
	Async        bool `json:"async"`
	AsyncTimeout int  `json:"asyncTimeout,omitempty"` // milliseconds
}

// CheckForResponses checks all pending hooks for completed responses.
// Returns completed hook responses and removes them from the registry.
func (r *AsyncHookRegistry) CheckForResponses() []AsyncHookResponse {
	r.mu.Lock()
	defer r.mu.Unlock()

	var responses []AsyncHookResponse
	var toDelete []string

	for processID, hook := range r.pending {
		if !hook.Done {
			continue
		}

		if hook.ResponseSent || strings.TrimSpace(hook.Stdout) == "" {
			hook.StopProgress()
			toDelete = append(toDelete, processID)
			continue
		}

		// Parse JSON from stdout lines.
		var response SyncHookJSONOutput
		lines := strings.Split(hook.Stdout, "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "{") {
				var parsed map[string]interface{}
				if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
					if _, isAsync := parsed["async"]; !isAsync {
						data, _ := json.Marshal(parsed)
						json.Unmarshal(data, &response) //nolint:errcheck
						break
					}
				}
			}
		}

		hook.ResponseSent = true
		hook.StopProgress()

		responses = append(responses, AsyncHookResponse{
			ProcessID: processID,
			Response:  response,
			HookName:  hook.HookName,
			HookEvent: hook.HookEvent,
			ToolName:  hook.ToolName,
			PluginID:  hook.PluginID,
			Stdout:    hook.Stdout,
			Stderr:    hook.Stderr,
			ExitCode:  hook.ExitCode,
		})
		toDelete = append(toDelete, processID)
	}

	for _, id := range toDelete {
		delete(r.pending, id)
	}

	r.logger.Debug("checked async hook responses",
		"responses", len(responses),
		"remaining", len(r.pending),
	)
	return responses
}

// RemoveDelivered removes hooks that have been delivered to the caller.
func (r *AsyncHookRegistry) RemoveDelivered(processIDs []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range processIDs {
		if hook, ok := r.pending[id]; ok && hook.ResponseSent {
			hook.StopProgress()
			delete(r.pending, id)
		}
	}
}

// FinalizeAll cancels or collects all pending hooks. Called at session end.
func (r *AsyncHookRegistry) FinalizeAll() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, hook := range r.pending {
		hook.StopProgress()
		if hook.Cancel != nil && !hook.Done {
			hook.Cancel()
		}
	}
	r.pending = make(map[string]*PendingAsyncHook)
}

// Clear removes all async hooks (for testing).
func (r *AsyncHookRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, hook := range r.pending {
		hook.StopProgress()
	}
	r.pending = make(map[string]*PendingAsyncHook)
}

// --- Hook Result Types ---

// HookBlockingError describes a hook-originated block.
type HookBlockingError struct {
	BlockingError string `json:"blockingError"`
	Command       string `json:"command"`
}

// HookResult is the result of executing a single hook.
type HookResult struct {
	Hook                         *HookCommand
	Outcome                      HookResultOutcome
	Message                      string
	Stdout                       string
	Stderr                       string
	ExitCode                     *int
	BlockingError                *HookBlockingError
	PreventContinuation          bool
	StopReason                   string
	PermissionBehavior           string // "ask", "deny", "allow", "passthrough"
	HookPermissionDecisionReason string
	AdditionalContext            string
	InitialUserMessage           string
	UpdatedInput                 map[string]interface{}
	UpdatedMCPToolOutput         interface{}
	PermissionRequestResult      *PermissionRequestResult
	Retry                        bool
}

// HookResultOutcome describes how a hook execution completed.
type HookResultOutcome string

const (
	HookOutcomeSuccess          HookResultOutcome = "success"
	HookOutcomeBlocking         HookResultOutcome = "blocking"
	HookOutcomeNonBlockingError HookResultOutcome = "non_blocking_error"
	HookOutcomeCancelled        HookResultOutcome = "cancelled"
)

// PermissionRequestResult is the hook's decision for a PermissionRequest event.
type PermissionRequestResult struct {
	Behavior     string                 `json:"behavior"` // "allow" or "deny"
	UpdatedInput map[string]interface{} `json:"updatedInput,omitempty"`
	Message      string                 `json:"message,omitempty"`
	Interrupt    bool                   `json:"interrupt,omitempty"`
}

// AggregatedHookResult combines results from multiple hooks for a single event.
type AggregatedHookResult struct {
	Message                      string
	BlockingErrors               []HookBlockingError
	PreventContinuation          bool
	StopReason                   string
	HookPermissionDecisionReason string
	PermissionBehavior           string
	AdditionalContexts           []string
	InitialUserMessage           string
	UpdatedInput                 map[string]interface{}
	UpdatedMCPToolOutput         interface{}
	PermissionRequestResult      *PermissionRequestResult
	Retry                        bool
}

// Aggregate combines multiple HookResults into an AggregatedHookResult.
func Aggregate(results []HookResult) *AggregatedHookResult {
	agg := &AggregatedHookResult{}

	for _, r := range results {
		if r.BlockingError != nil {
			agg.BlockingErrors = append(agg.BlockingErrors, *r.BlockingError)
		}
		if r.PreventContinuation {
			agg.PreventContinuation = true
		}
		if r.StopReason != "" {
			agg.StopReason = r.StopReason
		}
		if r.PermissionBehavior != "" {
			agg.PermissionBehavior = r.PermissionBehavior
		}
		if r.HookPermissionDecisionReason != "" {
			agg.HookPermissionDecisionReason = r.HookPermissionDecisionReason
		}
		if r.AdditionalContext != "" {
			agg.AdditionalContexts = append(agg.AdditionalContexts, r.AdditionalContext)
		}
		if r.InitialUserMessage != "" {
			agg.InitialUserMessage = r.InitialUserMessage
		}
		if r.UpdatedInput != nil {
			agg.UpdatedInput = r.UpdatedInput
		}
		if r.UpdatedMCPToolOutput != nil {
			agg.UpdatedMCPToolOutput = r.UpdatedMCPToolOutput
		}
		if r.PermissionRequestResult != nil {
			agg.PermissionRequestResult = r.PermissionRequestResult
		}
		if r.Retry {
			agg.Retry = true
		}
	}
	return agg
}
