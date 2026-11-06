// Package mergedtools combines tools, commands, and MCP clients from
// multiple sources (built-in, plugin, MCP) with deduplication and
// priority ordering.
//
// Migrated from TypeScript: hooks/{useMergedTools,useMergedCommands,useMergedClients,
// useCommandQueue,useQueueProcessor}.ts
package mergedtools

import (
	"sort"
	"sync"
)

// ---------------------------------------------------------------------------
// Priority levels
// ---------------------------------------------------------------------------

// Priority determines which source wins in a name collision.
type Priority int

const (
	PriorityBuiltin Priority = iota // highest
	PriorityPlugin
	PriorityMCP // lowest
)

// ---------------------------------------------------------------------------
// Tool
// ---------------------------------------------------------------------------

// Tool is a generic tool descriptor (built-in, MCP, or plugin).
type Tool struct {
	Name        string
	Description string
	Source      Priority
	Schema      map[string]any // JSON schema for the tool input
	// Execute is the optional handler. Nil means the tool is descriptive-only
	// (e.g. from an MCP server).
	Execute func(input map[string]any) (any, error)
}

// MergeTools combines multiple tool slices with deduplication.
// When names collide, the tool with the higher priority (lower numeric
// value) wins. Within the same priority, earlier sources win.
func MergeTools(sources ...[]Tool) []Tool {
	byName := make(map[string]Tool)
	for _, src := range sources {
		for _, t := range src {
			existing, ok := byName[t.Name]
			if !ok || t.Source < existing.Source {
				byName[t.Name] = t
			}
		}
	}

	merged := make([]Tool, 0, len(byName))
	for _, t := range byName {
		merged = append(merged, t)
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].Source != merged[j].Source {
			return merged[i].Source < merged[j].Source
		}
		return merged[i].Name < merged[j].Name
	})
	return merged
}

// DeduplicateToolsByName returns a copy with duplicate names removed.
// First occurrence wins.
func DeduplicateToolsByName(tools []Tool) []Tool {
	seen := make(map[string]bool, len(tools))
	out := make([]Tool, 0, len(tools))
	for _, t := range tools {
		if seen[t.Name] {
			continue
		}
		seen[t.Name] = true
		out = append(out, t)
	}
	return out
}

// FilterByMode removes tools that are disallowed in the given permission mode.
// denySet contains tool names that should be excluded.
func FilterByMode(tools []Tool, denySet map[string]bool) []Tool {
	if len(denySet) == 0 {
		return tools
	}
	out := make([]Tool, 0, len(tools))
	for _, t := range tools {
		if !denySet[t.Name] {
			out = append(out, t)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Command
// ---------------------------------------------------------------------------

// Command represents a slash command (e.g. /memory, /theme).
type Command struct {
	Name        string
	Description string
	Source      Priority
	Hidden      bool
	Execute     func(args string) error
}

// MergeCommands combines commands from multiple sources.
// Deduplication is by name; first occurrence at highest priority wins.
func MergeCommands(sources ...[]Command) []Command {
	byName := make(map[string]Command)
	for _, src := range sources {
		for _, c := range src {
			existing, ok := byName[c.Name]
			if !ok || c.Source < existing.Source {
				byName[c.Name] = c
			}
		}
	}
	merged := make([]Command, 0, len(byName))
	for _, c := range byName {
		merged = append(merged, c)
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Name < merged[j].Name
	})
	return merged
}

// DeduplicateCommandsByName removes duplicate commands by name.
func DeduplicateCommandsByName(commands []Command) []Command {
	seen := make(map[string]bool, len(commands))
	out := make([]Command, 0, len(commands))
	for _, c := range commands {
		if seen[c.Name] {
			continue
		}
		seen[c.Name] = true
		out = append(out, c)
	}
	return out
}

// ---------------------------------------------------------------------------
// Client (MCP server connections)
// ---------------------------------------------------------------------------

// MCPClient represents a connection to an MCP server.
type MCPClient struct {
	Name      string
	Transport string // "stdio", "sse", etc.
	Status    string // "connected", "disconnected", "error"
	Tools     []Tool
	Commands  []Command
}

// MergeClients combines MCP client lists, deduplicating by name.
// First occurrence wins.
func MergeClients(initial, dynamic []MCPClient) []MCPClient {
	if len(dynamic) == 0 {
		return initial
	}
	seen := make(map[string]bool, len(initial)+len(dynamic))
	merged := make([]MCPClient, 0, len(initial)+len(dynamic))
	for _, c := range initial {
		if !seen[c.Name] {
			seen[c.Name] = true
			merged = append(merged, c)
		}
	}
	for _, c := range dynamic {
		if !seen[c.Name] {
			seen[c.Name] = true
			merged = append(merged, c)
		}
	}
	return merged
}

// ---------------------------------------------------------------------------
// Command queue
// ---------------------------------------------------------------------------

// QueuePriority determines processing order.
type QueuePriority int

const (
	PriorityNow   QueuePriority = iota // immediate (interrupts)
	PriorityNext                       // user input
	PriorityLater                      // task notifications
)

// QueuedCommand is a command waiting to be processed.
type QueuedCommand struct {
	Text     string
	Priority QueuePriority
	Source   string
}

// CommandQueue is a thread-safe priority queue for commands.
type CommandQueue struct {
	mu          sync.Mutex
	items       []QueuedCommand
	subscribers []func()
}

// NewCommandQueue creates an empty queue.
func NewCommandQueue() *CommandQueue {
	return &CommandQueue{}
}

// Enqueue adds a command to the queue and notifies subscribers.
func (q *CommandQueue) Enqueue(cmd QueuedCommand) {
	q.mu.Lock()
	q.items = append(q.items, cmd)
	q.mu.Unlock()
	q.notify()
}

// Dequeue removes and returns the highest-priority command.
// Returns false if the queue is empty.
func (q *CommandQueue) Dequeue() (QueuedCommand, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.items) == 0 {
		return QueuedCommand{}, false
	}

	// Find highest priority (lowest numeric value).
	bestIdx := 0
	for i, item := range q.items {
		if item.Priority < q.items[bestIdx].Priority {
			bestIdx = i
		}
	}

	cmd := q.items[bestIdx]
	q.items = append(q.items[:bestIdx], q.items[bestIdx+1:]...)
	return cmd, true
}

// DequeueAll removes and returns all commands, ordered by priority.
func (q *CommandQueue) DequeueAll() []QueuedCommand {
	q.mu.Lock()
	items := q.items
	q.items = nil
	q.mu.Unlock()

	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Priority < items[j].Priority
	})
	return items
}

// Len returns the current queue length.
func (q *CommandQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// Snapshot returns a copy of the current queue contents.
func (q *CommandQueue) Snapshot() []QueuedCommand {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]QueuedCommand, len(q.items))
	copy(out, q.items)
	return out
}

// Subscribe registers a callback invoked on queue mutations.
func (q *CommandQueue) Subscribe(fn func()) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.subscribers = append(q.subscribers, fn)
}

func (q *CommandQueue) notify() {
	q.mu.Lock()
	subs := make([]func(), len(q.subscribers))
	copy(subs, q.subscribers)
	q.mu.Unlock()
	for _, fn := range subs {
		fn()
	}
}

// ---------------------------------------------------------------------------
// Queue processor
// ---------------------------------------------------------------------------

// QueueProcessor processes commands when conditions are met.
type QueueProcessor struct {
	Queue         *CommandQueue
	IsQueryActive func() bool
	HasActiveUI   func() bool
	ExecuteInput  func(cmds []QueuedCommand) error
}

// ProcessIfReady drains the queue and executes commands if no query
// is active and no UI is blocking.
func (p *QueueProcessor) ProcessIfReady() error {
	if p.IsQueryActive() {
		return nil
	}
	if p.HasActiveUI() {
		return nil
	}
	if p.Queue.Len() == 0 {
		return nil
	}
	cmds := p.Queue.DequeueAll()
	if len(cmds) == 0 {
		return nil
	}
	return p.ExecuteInput(cmds)
}
