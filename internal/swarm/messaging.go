package swarm

import (
	"container/heap"
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// MessageType categorizes inter-agent messages.
type MessageType int

const (
	// MessageChat is a plain text message between agents.
	MessageChat MessageType = iota
	// MessageTaskAssignment is a task delegation from leader to worker.
	MessageTaskAssignment
	// MessageStatusUpdate reports progress/idle/completion.
	MessageStatusUpdate
	// MessagePermissionRequest is a worker asking leader for tool permission.
	MessagePermissionRequest
	// MessagePermissionResponse is the leader's decision on a permission request.
	MessagePermissionResponse
	// MessageShutdownRequest asks an agent to shut down gracefully.
	MessageShutdownRequest
	// MessageShutdownApproved confirms the agent will shut down.
	MessageShutdownApproved
	// MessageShutdownRejected declines the shutdown request.
	MessageShutdownRejected
	// MessageBroadcast is a team-wide message.
	MessageBroadcast
)

// String returns a human-readable message type name.
func (mt MessageType) String() string {
	switch mt {
	case MessageChat:
		return "chat"
	case MessageTaskAssignment:
		return "task_assignment"
	case MessageStatusUpdate:
		return "status_update"
	case MessagePermissionRequest:
		return "permission_request"
	case MessagePermissionResponse:
		return "permission_response"
	case MessageShutdownRequest:
		return "shutdown_request"
	case MessageShutdownApproved:
		return "shutdown_approved"
	case MessageShutdownRejected:
		return "shutdown_rejected"
	case MessageBroadcast:
		return "broadcast"
	default:
		return "unknown"
	}
}

// Priority returns the scheduling priority for a message type.
// Lower values = higher priority. Shutdown > Permission > Task > Status > Chat.
func (mt MessageType) Priority() int {
	switch mt {
	case MessageShutdownRequest, MessageShutdownApproved, MessageShutdownRejected:
		return 0
	case MessagePermissionRequest, MessagePermissionResponse:
		return 1
	case MessageTaskAssignment:
		return 2
	case MessageStatusUpdate:
		return 3
	case MessageChat, MessageBroadcast:
		return 4
	default:
		return 5
	}
}

// Message is the unit of communication between agents in a swarm.
type Message struct {
	ID        string            `json:"id"`
	Type      MessageType       `json:"type"`
	Sender    string            `json:"sender"`
	Target    string            `json:"target"` // empty for broadcast
	Content   string            `json:"content"`
	Summary   string            `json:"summary,omitempty"`
	Color     string            `json:"color,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
	Read      bool              `json:"read"`

	// For priority queue ordering.
	priority int
	seqNo    uint64
	index    int // heap index
}

var globalSeqNo uint64

// NewMessage creates a new message with a generated ID and timestamp.
func NewMessage(msgType MessageType, sender, target, content string) *Message {
	return &Message{
		ID:        generateMessageID(),
		Type:      msgType,
		Sender:    sender,
		Target:    target,
		Content:   content,
		Timestamp: time.Now(),
		priority:  msgType.Priority(),
		seqNo:     atomic.AddUint64(&globalSeqNo, 1),
	}
}

func generateMessageID() string {
	return fmt.Sprintf("msg-%d-%d", time.Now().UnixNano(), atomic.AddUint64(&globalSeqNo, 1))
}

// --- Priority Queue (min-heap: lower priority number = dequeue first) ---

type messageHeap []*Message

func (h messageHeap) Len() int { return len(h) }

func (h messageHeap) Less(i, j int) bool {
	if h[i].priority != h[j].priority {
		return h[i].priority < h[j].priority
	}
	return h[i].seqNo < h[j].seqNo // FIFO within same priority
}

func (h messageHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *messageHeap) Push(x any) {
	msg := x.(*Message)
	msg.index = len(*h)
	*h = append(*h, msg)
}

func (h *messageHeap) Pop() any {
	old := *h
	n := len(old)
	msg := old[n-1]
	old[n-1] = nil
	msg.index = -1
	*h = old[:n-1]
	return msg
}

// Mailbox is a per-agent message inbox with priority ordering and
// blocking/non-blocking receive. Each agent in a team gets one Mailbox.
type Mailbox struct {
	mu      sync.Mutex
	owner   string
	queue   messageHeap
	notify  chan struct{} // signaled on new message; capacity 1
	closed  int32         // atomic flag
	logger  *slog.Logger
	history []*Message // bounded history for leader inspection
	maxHist int
}

const (
	defaultMaxHistory  = 200
	maxPendingMessages = 1024
)

// NewMailbox creates a mailbox for the given agent name.
func NewMailbox(owner string, logger *slog.Logger) *Mailbox {
	m := &Mailbox{
		owner:   owner,
		notify:  make(chan struct{}, 1),
		logger:  logger,
		maxHist: defaultMaxHistory,
	}
	heap.Init(&m.queue)
	return m
}

// Owner returns the agent name that owns this mailbox.
func (m *Mailbox) Owner() string { return m.owner }

// Deliver enqueues a message. Thread-safe.
// Returns ErrMailboxClosed if the mailbox has been closed.
func (m *Mailbox) Deliver(msg *Message) error {
	m.mu.Lock()
	if atomic.LoadInt32(&m.closed) != 0 {
		m.mu.Unlock()
		return ErrMailboxClosed
	}
	if m.queue.Len() >= maxPendingMessages {
		m.mu.Unlock()
		return ErrMailboxFull
	}
	msg.priority = msg.Type.Priority()
	msg.seqNo = atomic.AddUint64(&globalSeqNo, 1)
	heap.Push(&m.queue, msg)

	// Append to bounded history.
	if len(m.history) >= m.maxHist {
		m.history = m.history[1:]
	}
	m.history = append(m.history, msg)
	m.mu.Unlock()

	// Non-blocking signal: if the channel already has a token, skip.
	select {
	case m.notify <- struct{}{}:
	default:
	}
	return nil
}

// Receive returns the highest-priority message, blocking until one arrives
// or ctx is cancelled.
func (m *Mailbox) Receive(ctx context.Context) (*Message, error) {
	for {
		if msg := m.TryReceive(); msg != nil {
			return msg, nil
		}
		if atomic.LoadInt32(&m.closed) != 0 {
			return nil, ErrMailboxClosed
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-m.notify:
			// Woken up — loop to dequeue.
		}
	}
}

// TryReceive returns the highest-priority message without blocking.
// Returns nil if the inbox is empty.
func (m *Mailbox) TryReceive() *Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.queue.Len() == 0 {
		return nil
	}
	msg := heap.Pop(&m.queue).(*Message)
	msg.Read = true
	return msg
}

// Pending returns the number of unread messages.
func (m *Mailbox) Pending() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.queue.Len()
}

// Snapshot returns pending messages without acknowledging them. The caller
// should acknowledge IDs only after it has safely consumed the result.
func (m *Mailbox) Snapshot(limit int) []*Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	msgs := make([]*Message, 0, m.queue.Len())
	for _, queued := range m.queue {
		copy := *queued
		msgs = append(msgs, &copy)
	}
	sort.Slice(msgs, func(i, j int) bool {
		if msgs[i].priority != msgs[j].priority {
			return msgs[i].priority < msgs[j].priority
		}
		return msgs[i].seqNo < msgs[j].seqNo
	})
	if limit > 0 && len(msgs) > limit {
		msgs = msgs[:limit]
	}
	return msgs
}

// Acknowledge removes only the listed message IDs from the pending mailbox.
func (m *Mailbox) Acknowledge(ids []string) int {
	return len(m.AcknowledgeMatched(ids))
}

// AcknowledgeMatched returns precisely the IDs removed under the mailbox lock.
// Callers can persist this list without accidentally acknowledging absent IDs.
func (m *Mailbox) AcknowledgeMatched(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	selected := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		selected[id] = struct{}{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	retained := make(messageHeap, 0, m.queue.Len())
	var removed []string
	for _, msg := range m.queue {
		if _, ok := selected[msg.ID]; ok {
			msg.Read = true
			removed = append(removed, msg.ID)
			continue
		}
		retained = append(retained, msg)
	}
	m.queue = retained
	heap.Init(&m.queue)
	return removed
}

// History returns a copy of the bounded message history.
func (m *Mailbox) History() []*Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Message, len(m.history))
	copy(out, m.history)
	return out
}

// Drain returns all queued messages in priority order without blocking.
func (m *Mailbox) Drain() []*Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	var msgs []*Message
	for m.queue.Len() > 0 {
		msg := heap.Pop(&m.queue).(*Message)
		msg.Read = true
		msgs = append(msgs, msg)
	}
	return msgs
}

// Close marks the mailbox as closed. Subsequent Deliver calls return
// ErrMailboxClosed. Pending Receive calls unblock with ErrMailboxClosed.
func (m *Mailbox) Close() {
	m.mu.Lock()
	closed := atomic.CompareAndSwapInt32(&m.closed, 0, 1)
	m.mu.Unlock()
	if closed {
		// Wake up any blocking receiver.
		select {
		case m.notify <- struct{}{}:
		default:
		}
	}
}

// IsClosed reports whether the mailbox has been closed.
func (m *Mailbox) IsClosed() bool {
	return atomic.LoadInt32(&m.closed) != 0
}

// --- Message Router ---

// MessageRouter manages mailboxes for all agents in a team and routes
// messages between them. It is the single source of truth for inter-agent
// communication within a swarm.
type MessageRouter struct {
	mu       sync.RWMutex
	boxes    map[string]*Mailbox // agentName → mailbox
	teamName string
	logger   *slog.Logger
	journal  *TeamJournal
	runID    string
}

// NewMessageRouter creates a router for a team.
func NewMessageRouter(teamName string, logger *slog.Logger) *MessageRouter {
	return &MessageRouter{
		boxes:    make(map[string]*Mailbox),
		teamName: teamName,
		logger:   logger,
	}
}

// Register creates a mailbox for an agent. No-op if already registered.
func (r *MessageRouter) Register(agentName string) *Mailbox {
	r.mu.Lock()
	defer r.mu.Unlock()
	if mb, ok := r.boxes[agentName]; ok {
		return mb
	}
	mb := NewMailbox(agentName, r.logger)
	r.boxes[agentName] = mb
	r.logger.Debug("mailbox registered", "agent", agentName, "team", r.teamName)
	return mb
}

// Unregister closes and removes an agent's mailbox.
func (r *MessageRouter) Unregister(agentName string) {
	r.mu.Lock()
	mb, ok := r.boxes[agentName]
	if ok {
		delete(r.boxes, agentName)
	}
	r.mu.Unlock()
	if ok {
		mb.Close()
		r.logger.Debug("mailbox unregistered", "agent", agentName, "team", r.teamName)
	}
}

// GetMailbox returns the mailbox for an agent, or nil if not registered.
func (r *MessageRouter) GetMailbox(agentName string) *Mailbox {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.boxes[agentName]
}

// SendMessage routes a message to the target agent's mailbox.
// Returns ErrAgentNotFound if the target has no mailbox.
func (r *MessageRouter) SendMessage(msg *Message) error {
	r.mu.RLock()
	mb, ok := r.boxes[msg.Target]
	r.mu.RUnlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrAgentNotFound, msg.Target)
	}
	if err := mb.Deliver(msg); err != nil {
		return fmt.Errorf("deliver to %s: %w", msg.Target, err)
	}
	if r.journal != nil {
		if err := r.journal.append(journalEvent{Kind: "message", Team: r.teamName,
			RunID: r.runID, Message: msg}); err != nil {
			r.logger.Warn("team message journal write failed", "team", r.teamName, "error", err)
		}
	}
	r.logger.Debug("message routed",
		"type", msg.Type.String(),
		"from", msg.Sender,
		"to", msg.Target,
		"team", r.teamName,
	)
	return nil
}

// BroadcastMessage sends a message to every registered agent except the sender.
// Returns the list of recipients that received the message.
func (r *MessageRouter) BroadcastMessage(msg *Message) []string {
	r.mu.RLock()
	targets := make([]*Mailbox, 0, len(r.boxes))
	names := make([]string, 0, len(r.boxes))
	for name, mb := range r.boxes {
		if name != msg.Sender {
			targets = append(targets, mb)
			names = append(names, name)
		}
	}
	r.mu.RUnlock()

	var delivered []string
	for i, mb := range targets {
		msgCopy := *msg
		msgCopy.Target = names[i]
		msgCopy.seqNo = atomic.AddUint64(&globalSeqNo, 1)
		if err := mb.Deliver(&msgCopy); err == nil {
			delivered = append(delivered, names[i])
			if r.journal != nil {
				if err := r.journal.append(journalEvent{Kind: "message", Team: r.teamName,
					RunID: r.runID, Message: &msgCopy}); err != nil {
					r.logger.Warn("team broadcast journal write failed", "team", r.teamName, "error", err)
				}
			}
		}
	}
	r.logger.Debug("broadcast",
		"from", msg.Sender,
		"recipients", len(delivered),
		"team", r.teamName,
	)
	return delivered
}

// ListAgents returns the names of all agents with registered mailboxes.
func (r *MessageRouter) ListAgents() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.boxes))
	for name := range r.boxes {
		names = append(names, name)
	}
	return names
}

// CloseAll closes every mailbox and clears the router.
func (r *MessageRouter) CloseAll() {
	r.mu.Lock()
	boxes := r.boxes
	r.boxes = make(map[string]*Mailbox)
	r.mu.Unlock()

	for _, mb := range boxes {
		mb.Close()
	}
	r.logger.Info("all mailboxes closed", "team", r.teamName)
}
