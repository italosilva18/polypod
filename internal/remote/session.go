// Package remote manages remote/shared sessions via WebSocket (or HTTP
// long-polling fallback when no WebSocket library is available in go.mod).
//
// Ported from the TypeScript remote/ directory (RemoteSessionManager,
// SessionsWebSocket, sdkMessageAdapter).
//
// Because neither nhooyr.io/websocket nor gorilla/websocket are present in the
// module, the implementation uses stdlib net/http for HTTP-based long-polling as
// a transport layer, with the same reconnect/backoff semantics as the TS
// WebSocket client.
package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

// SessionConfig holds the parameters needed to connect to the remote session
// server.
type SessionConfig struct {
	// ServerURL is the base URL of the session server (e.g.
	// "https://api.anthropic.com").
	ServerURL string

	// SessionID is the identifier of an existing session to join, or empty
	// when creating a new session.
	SessionID string

	// AuthToken is sent as a Bearer token in the Authorization header.
	AuthToken string

	// Logger receives structured log messages.  If nil a no-op logger is
	// used.
	Logger *slog.Logger
}

// ---------------------------------------------------------------------------
// Domain types
// ---------------------------------------------------------------------------

// RemoteSession describes a remote/shared session returned by the server.
type RemoteSession struct {
	ID           string    `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	Owner        string    `json:"owner"`
	Participants []string  `json:"participants"`
	Status       string    `json:"status"`
}

// Message is a single message exchanged with a remote session.
type Message struct {
	Type      string    `json:"type"`
	Sender    string    `json:"sender"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// ---------------------------------------------------------------------------
// Connection state
// ---------------------------------------------------------------------------

type connState int

const (
	stateDisconnected connState = iota
	stateConnecting
	stateConnected
)

// ---------------------------------------------------------------------------
// Constants (mirrors TS SessionsWebSocket)
// ---------------------------------------------------------------------------

const (
	reconnectDelay     = 2 * time.Second
	maxReconnect       = 5
	pollInterval       = 2 * time.Second
	defaultHTTPTimeout = 30 * time.Second
)

// ---------------------------------------------------------------------------
// SessionManager
// ---------------------------------------------------------------------------

// SessionManager manages the connection to a remote session server using
// HTTP long-polling.  It provides the same logical API as the TS
// RemoteSessionManager + SessionsWebSocket pair.
type SessionManager struct {
	cfg    SessionConfig
	log    *slog.Logger
	client *http.Client

	mu       sync.Mutex
	state    connState
	handlers []func(Message)

	// polling
	pollCancel context.CancelFunc
	pollDone   chan struct{}
}

// NewSessionManager creates a new SessionManager with the given config.  The
// manager is created in a disconnected state; call Connect to start
// receiving messages.
func NewSessionManager(cfg SessionConfig) *SessionManager {
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &SessionManager{
		cfg: cfg,
		log: log,
		client: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
		state: stateDisconnected,
	}
}

// ---------------------------------------------------------------------------
// Connection lifecycle
// ---------------------------------------------------------------------------

// Connect establishes the connection to the session server.  It starts a
// background goroutine that long-polls for incoming messages and dispatches
// them to registered handlers.
//
// The method returns an error only if the initial handshake (an HTTP GET to
// verify reachability) fails.
func (sm *SessionManager) Connect(ctx context.Context) error {
	sm.mu.Lock()
	if sm.state != stateDisconnected {
		sm.mu.Unlock()
		return nil
	}
	sm.state = stateConnecting
	sm.mu.Unlock()

	sm.log.Info("connecting to remote session",
		"server", sm.cfg.ServerURL,
		"session", sm.cfg.SessionID,
	)

	// Verify that the server is reachable.
	u := fmt.Sprintf("%s/v1/sessions/%s", sm.cfg.ServerURL, sm.cfg.SessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		sm.setState(stateDisconnected)
		return fmt.Errorf("remote: build request: %w", err)
	}
	sm.setAuth(req)

	resp, err := sm.client.Do(req)
	if err != nil {
		sm.setState(stateDisconnected)
		return fmt.Errorf("remote: connect handshake: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		sm.setState(stateDisconnected)
		return fmt.Errorf("remote: connect handshake: HTTP %d", resp.StatusCode)
	}

	sm.setState(stateConnected)
	sm.log.Info("connected to remote session", "session", sm.cfg.SessionID)

	// Start long-polling loop.
	pollCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	sm.mu.Lock()
	sm.pollCancel = cancel
	sm.pollDone = done
	sm.mu.Unlock()

	go sm.pollLoop(pollCtx, done)

	return nil
}

// Disconnect closes the connection and stops the polling loop.
func (sm *SessionManager) Disconnect() error {
	sm.mu.Lock()
	cancel := sm.pollCancel
	done := sm.pollDone
	sm.pollCancel = nil
	sm.pollDone = nil
	sm.state = stateDisconnected
	sm.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}

	sm.log.Info("disconnected from remote session", "session", sm.cfg.SessionID)
	return nil
}

// ---------------------------------------------------------------------------
// Messaging
// ---------------------------------------------------------------------------

// SendMessage sends a message to the remote session via HTTP POST.
func (sm *SessionManager) SendMessage(ctx context.Context, msg Message) error {
	if !sm.isConnected() {
		return errors.New("remote: not connected")
	}

	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("remote: marshal message: %w", err)
	}

	u := fmt.Sprintf("%s/v1/sessions/%s/messages", sm.cfg.ServerURL, sm.cfg.SessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("remote: build request: %w", err)
	}
	sm.setAuth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := sm.client.Do(req)
	if err != nil {
		return fmt.Errorf("remote: send message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("remote: send message: HTTP %d", resp.StatusCode)
	}

	sm.log.Debug("message sent", "type", msg.Type, "session", sm.cfg.SessionID)
	return nil
}

// OnMessage registers a handler that is called for every incoming message.
// Multiple handlers can be registered; they are called in order.
func (sm *SessionManager) OnMessage(handler func(Message)) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.handlers = append(sm.handlers, handler)
}

// ---------------------------------------------------------------------------
// Session CRUD
// ---------------------------------------------------------------------------

// ListSessions returns all sessions available on the server.
func (sm *SessionManager) ListSessions(ctx context.Context) ([]RemoteSession, error) {
	u := fmt.Sprintf("%s/v1/sessions", sm.cfg.ServerURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("remote: build request: %w", err)
	}
	sm.setAuth(req)

	resp, err := sm.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remote: list sessions: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("remote: list sessions: HTTP %d", resp.StatusCode)
	}

	var sessions []RemoteSession
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		return nil, fmt.Errorf("remote: decode sessions: %w", err)
	}
	return sessions, nil
}

// CreateSession creates a new remote session on the server and returns it.
func (sm *SessionManager) CreateSession(ctx context.Context) (*RemoteSession, error) {
	u := fmt.Sprintf("%s/v1/sessions", sm.cfg.ServerURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return nil, fmt.Errorf("remote: build request: %w", err)
	}
	sm.setAuth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := sm.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remote: create session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("remote: create session: HTTP %d", resp.StatusCode)
	}

	var session RemoteSession
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, fmt.Errorf("remote: decode session: %w", err)
	}
	return &session, nil
}

// JoinSession joins an existing remote session identified by sessionID.  It
// sends a POST to the join endpoint and updates the manager's session ID on
// success.
func (sm *SessionManager) JoinSession(ctx context.Context, sessionID string) error {
	u := fmt.Sprintf("%s/v1/sessions/%s/join", sm.cfg.ServerURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return fmt.Errorf("remote: build request: %w", err)
	}
	sm.setAuth(req)

	resp, err := sm.client.Do(req)
	if err != nil {
		return fmt.Errorf("remote: join session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("remote: join session: HTTP %d", resp.StatusCode)
	}

	sm.mu.Lock()
	sm.cfg.SessionID = sessionID
	sm.mu.Unlock()

	sm.log.Info("joined remote session", "session", sessionID)
	return nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (sm *SessionManager) setAuth(req *http.Request) {
	if sm.cfg.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+sm.cfg.AuthToken)
	}
}

func (sm *SessionManager) setState(s connState) {
	sm.mu.Lock()
	sm.state = s
	sm.mu.Unlock()
}

func (sm *SessionManager) isConnected() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.state == stateConnected
}

func (sm *SessionManager) dispatch(msg Message) {
	sm.mu.Lock()
	handlers := make([]func(Message), len(sm.handlers))
	copy(handlers, sm.handlers)
	sm.mu.Unlock()

	for _, h := range handlers {
		h(msg)
	}
}

// pollLoop long-polls the server for new messages.  It implements
// exponential back-off on transient errors, mirroring the reconnect
// behaviour of the TS WebSocket client.
func (sm *SessionManager) pollLoop(ctx context.Context, done chan struct{}) {
	defer close(done)

	failures := 0

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msgs, err := sm.poll(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			failures++
			if failures > maxReconnect {
				sm.log.Error("polling stopped: too many failures",
					"failures", failures,
					"session", sm.cfg.SessionID,
				)
				sm.setState(stateDisconnected)
				return
			}
			backoff := reconnectDelay * time.Duration(failures)
			sm.log.Warn("poll error, backing off",
				"err", err,
				"backoff", backoff,
				"attempt", failures,
			)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			continue
		}

		failures = 0
		for _, m := range msgs {
			sm.dispatch(m)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(pollInterval):
		}
	}
}

// poll performs a single long-poll request and returns any new messages.
func (sm *SessionManager) poll(ctx context.Context) ([]Message, error) {
	u := fmt.Sprintf("%s/v1/sessions/%s/messages/poll", sm.cfg.ServerURL, sm.cfg.SessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("remote: build poll request: %w", err)
	}
	sm.setAuth(req)

	resp, err := sm.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remote: poll: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified {
		// No new messages.
		return nil, nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("remote: poll: HTTP %d", resp.StatusCode)
	}

	var msgs []Message
	if err := json.NewDecoder(resp.Body).Decode(&msgs); err != nil {
		return nil, fmt.Errorf("remote: decode poll response: %w", err)
	}
	return msgs, nil
}
