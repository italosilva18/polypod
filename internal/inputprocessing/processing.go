// Package inputprocessing handles the user input pipeline: slash command parsing,
// mention expansion, prompt submission, and completion caching.
// Migrated from Claude Code's handlePromptSubmit.ts, slashCommandParsing.ts,
// processUserInput/, completionCache.ts, mailbox.ts, activityManager.ts, advisor.ts.
package inputprocessing

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ParsedSlashCommand represents a parsed slash command from user input.
type ParsedSlashCommand struct {
	// CommandName is the command identifier (without leading slash).
	CommandName string

	// Args is everything after the command name.
	Args string

	// IsMCP indicates this is an MCP-provided command.
	IsMCP bool
}

// ParseSlashCommand parses a slash command input string into its component parts.
// Returns nil if the input is not a valid slash command.
//
// Examples:
//
//	"/search foo bar" -> {CommandName: "search", Args: "foo bar", IsMCP: false}
//	"/mcp:tool (MCP) arg1" -> {CommandName: "mcp:tool (MCP)", Args: "arg1", IsMCP: true}
func ParseSlashCommand(input string) *ParsedSlashCommand {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return nil
	}

	withoutSlash := trimmed[1:]
	words := strings.Fields(withoutSlash)
	if len(words) == 0 {
		return nil
	}

	commandName := words[0]
	isMCP := false
	argsStartIndex := 1

	// Check for MCP commands (second word is "(MCP)").
	if len(words) > 1 && words[1] == "(MCP)" {
		commandName = commandName + " (MCP)"
		isMCP = true
		argsStartIndex = 2
	}

	args := strings.Join(words[argsStartIndex:], " ")

	return &ParsedSlashCommand{
		CommandName: commandName,
		Args:        args,
		IsMCP:       isMCP,
	}
}

// InputMode represents the type of user input being processed.
type InputMode string

const (
	InputModePrompt           InputMode = "prompt"
	InputModeBash             InputMode = "bash"
	InputModeTaskNotification InputMode = "task-notification"
)

// ProcessedInput is the result of processing user input through the pipeline.
type ProcessedInput struct {
	// Messages are the conversation messages produced by processing.
	Messages []InputMessage

	// ShouldQuery indicates if the AI should be queried with these messages.
	ShouldQuery bool

	// AllowedTools lists additional tools allowed for this query.
	AllowedTools []string

	// Model overrides the default model for this query.
	Model string

	// NextInput is text to inject as the next input (e.g., from /discover activation).
	NextInput string

	// SubmitNextInput indicates NextInput should be submitted automatically.
	SubmitNextInput bool
}

// InputMessage represents a message produced during input processing.
type InputMessage struct {
	Role      string // "user", "assistant", "system"
	Content   string
	UUID      string
	Timestamp time.Time
	IsMeta    bool
}

// ProcessUserInput is the main input processing pipeline.
// It handles slash commands, mention expansion, and message creation.
func ProcessUserInput(input string, mode InputMode, skipSlashCommands bool) *ProcessedInput {
	result := &ProcessedInput{
		Messages:    make([]InputMessage, 0, 1),
		ShouldQuery: true,
	}

	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		result.ShouldQuery = false
		return result
	}

	// Handle exit commands.
	if isExitCommand(trimmed) {
		result.ShouldQuery = false
		return result
	}

	// Handle slash commands.
	if !skipSlashCommands && strings.HasPrefix(trimmed, "/") {
		parsed := ParseSlashCommand(trimmed)
		if parsed != nil {
			return processSlashCommandInput(parsed)
		}
	}

	// Standard prompt: create a user message. Mention expansion is
	// performed by the adapter layer (which has access to the working
	// directory and extraDirs). At this pipeline stage the mention
	// text is preserved verbatim.
	expanded := trimmed

	msg := InputMessage{
		Role:      "user",
		Content:   expanded,
		UUID:      generateMessageID(),
		Timestamp: time.Now(),
	}
	result.Messages = append(result.Messages, msg)

	return result
}

func processSlashCommandInput(cmd *ParsedSlashCommand) *ProcessedInput {
	result := &ProcessedInput{
		Messages:    make([]InputMessage, 0, 1),
		ShouldQuery: true,
	}

	// Create a user message with the slash command as content.
	msg := InputMessage{
		Role:      "user",
		Content:   fmt.Sprintf("/%s %s", cmd.CommandName, cmd.Args),
		UUID:      generateMessageID(),
		Timestamp: time.Now(),
	}
	result.Messages = append(result.Messages, msg)

	return result
}

func isExitCommand(input string) bool {
	exitCommands := map[string]bool{
		"exit": true, "quit": true, ":q": true,
		":q!": true, ":wq": true, ":wq!": true,
	}
	return exitCommands[input]
}

// HandlePromptSubmit processes a submitted prompt, handling queuing,
// slash commands, reference expansion, and abort logic.
type HandlePromptSubmit struct {
	mu           sync.Mutex
	isProcessing bool
	queue        []QueuedCommand
	abortChan    chan struct{}
}

// QueuedCommand represents a command waiting to be executed.
type QueuedCommand struct {
	Value             string
	PreExpansionValue string
	Mode              InputMode
	SkipSlashCommands bool
	UUID              string
	Workload          string
}

// NewHandlePromptSubmit creates a new prompt submission handler.
func NewHandlePromptSubmit() *HandlePromptSubmit {
	return &HandlePromptSubmit{
		queue: make([]QueuedCommand, 0),
	}
}

// Submit processes a user prompt submission.
// If a query is already active, the input is queued.
func (h *HandlePromptSubmit) Submit(input string, mode InputMode) *ProcessedInput {
	h.mu.Lock()
	if h.isProcessing {
		h.queue = append(h.queue, QueuedCommand{
			Value: strings.TrimSpace(input),
			Mode:  mode,
			UUID:  generateMessageID(),
		})
		h.mu.Unlock()
		return nil
	}
	h.isProcessing = true
	h.abortChan = make(chan struct{})
	h.mu.Unlock()

	return ProcessUserInput(input, mode, false)
}

// Dequeue returns the next queued command, or nil if the queue is empty.
func (h *HandlePromptSubmit) Dequeue() *QueuedCommand {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.queue) == 0 {
		return nil
	}
	cmd := h.queue[0]
	h.queue = h.queue[1:]
	return &cmd
}

// Done marks the current query as complete.
func (h *HandlePromptSubmit) Done() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.isProcessing = false
}

// Abort cancels the current query.
func (h *HandlePromptSubmit) Abort() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.abortChan != nil {
		select {
		case <-h.abortChan:
			// Already closed.
		default:
			close(h.abortChan)
		}
	}
}

// QueueLength returns the number of queued commands.
func (h *HandlePromptSubmit) QueueLength() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.queue)
}

// CompletionCache caches input completions for faster autocomplete.
type CompletionCache struct {
	mu      sync.RWMutex
	entries map[string][]string
	maxSize int
}

// NewCompletionCache creates a new completion cache.
func NewCompletionCache(maxSize int) *CompletionCache {
	if maxSize <= 0 {
		maxSize = 100
	}
	return &CompletionCache{
		entries: make(map[string][]string),
		maxSize: maxSize,
	}
}

// Get retrieves completions for a prefix.
func (cc *CompletionCache) Get(prefix string) []string {
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	if completions, ok := cc.entries[prefix]; ok {
		result := make([]string, len(completions))
		copy(result, completions)
		return result
	}
	return nil
}

// Set stores completions for a prefix.
func (cc *CompletionCache) Set(prefix string, completions []string) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	// Evict oldest entries if at capacity.
	if len(cc.entries) >= cc.maxSize {
		// Simple eviction: clear half the cache.
		i := 0
		for k := range cc.entries {
			if i >= cc.maxSize/2 {
				break
			}
			delete(cc.entries, k)
			i++
		}
	}

	stored := make([]string, len(completions))
	copy(stored, completions)
	cc.entries[prefix] = stored
}

// Clear removes all cached completions.
func (cc *CompletionCache) Clear() {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.entries = make(map[string][]string)
}

// MessageSource identifies where a mailbox message came from.
type MessageSource string

const (
	MessageSourceUser     MessageSource = "user"
	MessageSourceTeammate MessageSource = "teammate"
	MessageSourceSystem   MessageSource = "system"
	MessageSourceTick     MessageSource = "tick"
	MessageSourceTask     MessageSource = "task"
)

// MailboxMessage represents a message in the inter-agent mailbox.
type MailboxMessage struct {
	ID        string
	Source    MessageSource
	Content   string
	From      string
	Color     string
	Timestamp time.Time
}

// Mailbox provides an inter-agent message queue with polling and blocking receive.
type Mailbox struct {
	mu       sync.Mutex
	queue    []MailboxMessage
	waiters  []mailboxWaiter
	revision int
}

type mailboxWaiter struct {
	filter func(MailboxMessage) bool
	ch     chan MailboxMessage
}

// NewMailbox creates a new empty mailbox.
func NewMailbox() *Mailbox {
	return &Mailbox{
		queue: make([]MailboxMessage, 0),
	}
}

// Send adds a message to the mailbox. If a waiter matches, delivers directly.
func (m *Mailbox) Send(msg MailboxMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revision++

	// Check if any waiter accepts this message.
	for i, w := range m.waiters {
		if w.filter(msg) {
			m.waiters = append(m.waiters[:i], m.waiters[i+1:]...)
			w.ch <- msg
			return
		}
	}
	m.queue = append(m.queue, msg)
}

// Poll returns the first message matching the filter, or nil if none match.
func (m *Mailbox) Poll(filter func(MailboxMessage) bool) *MailboxMessage {
	if filter == nil {
		filter = func(MailboxMessage) bool { return true }
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for i, msg := range m.queue {
		if filter(msg) {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			return &msg
		}
	}
	return nil
}

// Receive blocks until a message matching the filter arrives.
func (m *Mailbox) Receive(filter func(MailboxMessage) bool) MailboxMessage {
	if filter == nil {
		filter = func(MailboxMessage) bool { return true }
	}

	m.mu.Lock()

	// Check queue first.
	for i, msg := range m.queue {
		if filter(msg) {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			m.mu.Unlock()
			return msg
		}
	}

	// Register as waiter.
	ch := make(chan MailboxMessage, 1)
	m.waiters = append(m.waiters, mailboxWaiter{filter: filter, ch: ch})
	m.mu.Unlock()

	return <-ch
}

// Length returns the number of queued messages.
func (m *Mailbox) Length() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.queue)
}

// Revision returns the monotonically increasing message counter.
func (m *Mailbox) Revision() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.revision
}

// ActivityManager tracks user and CLI activity time.
// It deduplicates overlapping activities and provides separate metrics.
type ActivityManager struct {
	mu sync.Mutex

	activeOperations map[string]bool
	lastUserActivity time.Time
	lastCLIRecorded  time.Time
	isCLIActive      bool

	// UserActivityTimeout is the window for considering user "active" (default 5s).
	UserActivityTimeout time.Duration

	// Metrics accumulator.
	userActiveSeconds float64
	cliActiveSeconds  float64
}

// NewActivityManager creates a new activity manager.
func NewActivityManager() *ActivityManager {
	return &ActivityManager{
		activeOperations:    make(map[string]bool),
		lastCLIRecorded:     time.Now(),
		UserActivityTimeout: 5 * time.Second,
	}
}

// RecordUserActivity records a user interaction event.
func (am *ActivityManager) RecordUserActivity() {
	am.mu.Lock()
	defer am.mu.Unlock()

	now := time.Now()
	if !am.isCLIActive && !am.lastUserActivity.IsZero() {
		elapsed := now.Sub(am.lastUserActivity).Seconds()
		if elapsed > 0 && elapsed < am.UserActivityTimeout.Seconds() {
			am.userActiveSeconds += elapsed
		}
	}
	am.lastUserActivity = now
}

// StartCLIActivity begins tracking a CLI operation.
func (am *ActivityManager) StartCLIActivity(operationID string) {
	am.mu.Lock()
	defer am.mu.Unlock()

	// Cleanup if already exists.
	if am.activeOperations[operationID] {
		am.endCLIActivityLocked(operationID)
	}

	wasEmpty := len(am.activeOperations) == 0
	am.activeOperations[operationID] = true

	if wasEmpty {
		am.isCLIActive = true
		am.lastCLIRecorded = time.Now()
	}
}

// EndCLIActivity stops tracking a CLI operation.
func (am *ActivityManager) EndCLIActivity(operationID string) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.endCLIActivityLocked(operationID)
}

func (am *ActivityManager) endCLIActivityLocked(operationID string) {
	delete(am.activeOperations, operationID)

	if len(am.activeOperations) == 0 {
		now := time.Now()
		elapsed := now.Sub(am.lastCLIRecorded).Seconds()
		if elapsed > 0 {
			am.cliActiveSeconds += elapsed
		}
		am.lastCLIRecorded = now
		am.isCLIActive = false
	}
}

// GetActivityStates returns current activity state snapshot.
func (am *ActivityManager) GetActivityStates() (isUserActive, isCLIActive bool, activeOps int) {
	am.mu.Lock()
	defer am.mu.Unlock()

	elapsed := time.Since(am.lastUserActivity).Seconds()
	isUserActive = elapsed < am.UserActivityTimeout.Seconds()

	return isUserActive, am.isCLIActive, len(am.activeOperations)
}

// GetMetrics returns accumulated active time metrics.
func (am *ActivityManager) GetMetrics() (userSeconds, cliSeconds float64) {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.userActiveSeconds, am.cliActiveSeconds
}

func generateMessageID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
