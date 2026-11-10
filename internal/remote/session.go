// Package remote provides Polypod-owned persistent shared sessions over HTTP
// long polling. The server stores ordered messages and delivery cursors in
// SQLite; clients acknowledge each message after dispatching it. The caller
// may supply a TLS listener when the host is reachable beyond loopback.
package remote

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

// SessionConfig holds the parameters needed to connect to the remote session
// server.
type SessionConfig struct {
	// ServerURL is the base URL of a Polypod remote server.
	ServerURL string

	// SessionID is the identifier of an existing session to join, or empty
	// when creating a new session.
	SessionID string

	// AuthToken is sent as a Bearer token in the Authorization header.
	AuthToken string

	// ParticipantID is a stable, unique identity for this subscriber. Clients
	// using the same ID share one acknowledged cursor across process restarts.
	ParticipantID string

	// StateFile optionally stores the auto-created SessionID for process
	// restarts. The acknowledged message cursor is stored on the server.
	StateFile string

	// RootCAs, when non-nil, is the only set of trust anchors for this client.
	// Nil keeps the default HTTP client and the process trust store.
	// Certificate verification stays enabled in both cases.
	RootCAs *x509.CertPool

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
	ID        string    `json:"id,omitempty"`
	Sequence  int64     `json:"sequence,omitempty"`
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
	defaultHTTPTimeout = 30 * time.Second
	maxResponseBytes   = 2 << 20
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
	cursor   int64
	epoch    uint64

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
		cfg:    cfg,
		log:    log,
		client: sessionHTTPClient(cfg.RootCAs),
		state:  stateDisconnected,
	}
}

// SessionTrustPoolFromPEM returns a pool that contains only CERTIFICATE blocks
// from pemBytes. System roots are not added. A private-key block, or a bundle
// with no certificate, is rejected. The result is intended for
// SessionConfig.RootCAs and does not disable verification.
func SessionTrustPoolFromPEM(pemBytes []byte) (*x509.CertPool, error) {
	if pemBundleHasPrivateKey(pemBytes) {
		return nil, fmt.Errorf("remote: o PEM contém chave privada")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("remote: PEM sem certificado")
	}
	return pool, nil
}

func pemBundleHasPrivateKey(pemBytes []byte) bool {
	rest := pemBytes
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return false
		}
		if strings.Contains(block.Type, "PRIVATE KEY") {
			return true
		}
	}
}

func sessionHTTPClient(rootCAs *x509.CertPool) *http.Client {
	if rootCAs == nil {
		return &http.Client{Timeout: defaultHTTPTimeout, CheckRedirect: rejectSessionRedirect}
	}
	var transport *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = base.Clone()
	} else {
		transport = &http.Transport{}
	}
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    rootCAs,
	}
	return &http.Client{Timeout: defaultHTTPTimeout, Transport: transport, CheckRedirect: rejectSessionRedirect}
}

// The session API does not use redirects. Refusing every redirect prevents
// the bearer token from being forwarded to another origin or downgraded to
// cleartext HTTP on the same host.
func rejectSessionRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// ---------------------------------------------------------------------------
// Connection lifecycle
// ---------------------------------------------------------------------------

// Connect establishes the connection to the session server.  It starts a
// background goroutine that long-polls for incoming messages and dispatches
// them to registered handlers.
//
// Connect creates or joins the configured session before polling. A stable
// ParticipantID lets the server resume from its persisted cursor on restart.
func (sm *SessionManager) Connect(ctx context.Context) error {
	if strings.TrimSpace(sm.cfg.ServerURL) == "" || sm.cfg.AuthToken == "" || !publicParticipant(sm.cfg.ParticipantID) {
		return fmt.Errorf("remote: URL, token e ParticipantID válido são obrigatórios")
	}
	if err := sm.restoreSessionID(); err != nil {
		return err
	}
	sm.mu.Lock()
	if sm.state == stateConnected {
		sm.mu.Unlock()
		return nil
	}
	if sm.state == stateConnecting {
		sm.mu.Unlock()
		return fmt.Errorf("remote: conexão já está em andamento")
	}
	sm.state = stateConnecting
	sm.epoch++
	epoch := sm.epoch
	sessionID := sm.cfg.SessionID
	sm.mu.Unlock()

	sm.log.Info("connecting to remote session",
		"server", sm.cfg.ServerURL,
		"session", sessionID,
	)
	var err error
	if sessionID == "" {
		_, err = sm.CreateSession(ctx)
	} else {
		err = sm.JoinSession(ctx, sessionID)
	}
	if err != nil {
		sm.abortConnect(epoch)
		return fmt.Errorf("remote: establishing session: %w", err)
	}

	// Start long-polling loop.
	pollCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	sm.mu.Lock()
	if sm.state != stateConnecting || sm.epoch != epoch {
		sm.mu.Unlock()
		cancel()
		return context.Canceled
	}
	sm.pollCancel = cancel
	sm.pollDone = done
	sm.state = stateConnected
	sessionID = sm.cfg.SessionID
	sm.mu.Unlock()

	go sm.pollLoop(pollCtx, done)
	sm.log.Info("connected to remote session", "session", sessionID)

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
	sm.epoch++
	sm.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}

	sm.log.Info("disconnected from remote session", "session", sm.sessionID())
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
	if strings.TrimSpace(msg.Type) != "user" {
		return fmt.Errorf("remote: cliente remoto só pode enviar mensagem type=user")
	}
	msg.Type = "user"
	if strings.TrimSpace(msg.Content) == "" || len(msg.Content) > 256<<10 || len(msg.ID) > 128 ||
		(msg.ID != "" && strings.TrimSpace(msg.ID) == "") {
		return fmt.Errorf("remote: conteúdo ou ID da mensagem inválido")
	}
	msg.Sender = ""
	msg.Sequence = 0
	msg.Timestamp = time.Time{}
	if msg.ID == "" {
		id, err := newID("message_")
		if err != nil {
			return err
		}
		msg.ID = id
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("remote: marshal message: %w", err)
	}
	if len(body) > 512<<10 {
		return fmt.Errorf("remote: mensagem excede 512 KiB")
	}
	u := sm.sessionURL("/messages")
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("remote: build request: %w", err)
		}
		sm.setAuth(req)
		req.Header.Set("Content-Type", "application/json")
		resp, err := sm.client.Do(req)
		if err == nil {
			status := resp.StatusCode
			closeErr := resp.Body.Close()
			if closeErr != nil {
				sm.log.Warn("falha ao fechar resposta remota", "error", closeErr)
			}
			if status >= 200 && status < 300 {
				sm.log.Debug("message sent", "type", msg.Type, "session", sm.sessionID(), "id", msg.ID)
				return nil
			}
			lastErr = fmt.Errorf("remote: send message: HTTP %d", status)
			if status < 500 {
				return lastErr
			}
		} else {
			lastErr = fmt.Errorf("remote: send message: %w", err)
		}
		if attempt < 2 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt+1) * reconnectDelay):
			}
		}
	}
	return lastErr
}

// OnMessage registers a handler that is called for every incoming message.
// Multiple handlers can be registered; they are called in order.
func (sm *SessionManager) OnMessage(handler func(Message)) {
	if handler == nil {
		return
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.handlers = append(sm.handlers, handler)
}

// ---------------------------------------------------------------------------
// Session CRUD
// ---------------------------------------------------------------------------

// ListSessions returns all sessions available on the server.
func (sm *SessionManager) ListSessions(ctx context.Context) ([]RemoteSession, error) {
	u := sm.baseURL() + "/v1/sessions"
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
	if err := decodeLimitedResponse(resp.Body, &sessions); err != nil {
		return nil, fmt.Errorf("remote: decode sessions: %w", err)
	}
	return sessions, nil
}

// CreateSession creates a new remote session on the server and returns it.
func (sm *SessionManager) CreateSession(ctx context.Context) (*RemoteSession, error) {
	sm.mu.Lock()
	connected := sm.state == stateConnected
	sm.mu.Unlock()
	if connected {
		return nil, fmt.Errorf("remote: desconecte antes de trocar de sessão")
	}
	u := sm.baseURL() + "/v1/sessions"
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
	if err := decodeLimitedResponse(resp.Body, &session); err != nil {
		return nil, fmt.Errorf("remote: decode session: %w", err)
	}
	sm.mu.Lock()
	sm.cfg.SessionID = session.ID
	sm.cursor = 0
	sm.mu.Unlock()
	if err := sm.persistSessionID(session.ID); err != nil {
		return nil, err
	}
	return &session, nil
}

// JoinSession joins an existing remote session identified by sessionID.  It
// sends a POST to the join endpoint and updates the manager's session ID on
// success.
func (sm *SessionManager) JoinSession(ctx context.Context, sessionID string) error {
	if !validSessionID(sessionID) {
		return fmt.Errorf("remote: ID de sessão inválido")
	}
	sm.mu.Lock()
	connected := sm.state == stateConnected
	sm.mu.Unlock()
	if connected {
		return fmt.Errorf("remote: desconecte antes de trocar de sessão")
	}
	u := sm.baseURL() + "/v1/sessions/" + url.PathEscape(sessionID) + "/join"
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
	sm.cursor = 0
	sm.mu.Unlock()
	if err := sm.persistSessionID(sessionID); err != nil {
		return err
	}

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
	req.Header.Set(participantHeader, sm.cfg.ParticipantID)
}

// SessionID returns the persisted session ID, including one created by Connect.
func (sm *SessionManager) SessionID() string { return sm.sessionID() }

func (sm *SessionManager) sessionID() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.cfg.SessionID
}

func (sm *SessionManager) baseURL() string { return strings.TrimRight(sm.cfg.ServerURL, "/") }

func (sm *SessionManager) sessionURL(suffix string) string {
	return sm.baseURL() + "/v1/sessions/" + url.PathEscape(sm.sessionID()) + suffix
}

func (sm *SessionManager) restoreSessionID() error {
	sm.mu.Lock()
	needed := sm.cfg.SessionID == "" && sm.cfg.StateFile != ""
	path := sm.cfg.StateFile
	sm.mu.Unlock()
	if !needed {
		return nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remote: reading session state: %w", err)
	}
	if len(raw) > 256 {
		return fmt.Errorf("remote: arquivo de sessão inválido")
	}
	id := strings.TrimSpace(string(raw))
	if !validSessionID(id) {
		return fmt.Errorf("remote: ID persistido de sessão inválido")
	}
	sm.mu.Lock()
	if sm.cfg.SessionID == "" {
		sm.cfg.SessionID = id
	}
	sm.mu.Unlock()
	return nil
}

func (sm *SessionManager) persistSessionID(id string) error {
	if sm.cfg.StateFile == "" {
		return nil
	}
	dir := filepath.Dir(sm.cfg.StateFile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("remote: creating session state directory: %w", err)
	}
	file, err := os.CreateTemp(dir, ".remote-session-*")
	if err != nil {
		return fmt.Errorf("remote: creating session state: %w", err)
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if _, err := file.WriteString(id + "\n"); err != nil {
		file.Close()
		return fmt.Errorf("remote: writing session state: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("remote: syncing session state: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("remote: closing session state: %w", err)
	}
	if err := os.Rename(tmp, sm.cfg.StateFile); err != nil {
		return fmt.Errorf("remote: publishing session state: %w", err)
	}
	return nil
}

func (sm *SessionManager) currentCursor() int64 {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.cursor
}

func (sm *SessionManager) advanceCursor(sequence int64) {
	sm.mu.Lock()
	if sequence > sm.cursor {
		sm.cursor = sequence
	}
	sm.mu.Unlock()
}

func decodeLimitedResponse(body io.Reader, target any) error {
	raw, err := io.ReadAll(io.LimitReader(body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxResponseBytes {
		return fmt.Errorf("resposta excede %d bytes", maxResponseBytes)
	}
	return json.Unmarshal(raw, target)
}

func (sm *SessionManager) abortConnect(epoch uint64) {
	sm.mu.Lock()
	if sm.epoch == epoch && sm.state == stateConnecting {
		sm.state = stateDisconnected
	}
	sm.mu.Unlock()
}

func (sm *SessionManager) isConnected() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.state == stateConnected
}

func (sm *SessionManager) dispatch(msg Message) error {
	sm.mu.Lock()
	handlers := make([]func(Message), len(sm.handlers))
	copy(handlers, sm.handlers)
	sm.mu.Unlock()
	if len(handlers) == 0 {
		return fmt.Errorf("remote: nenhum handler registrado para receber mensagens")
	}

	for _, h := range handlers {
		if err := invokeHandler(h, msg); err != nil {
			return err
		}
	}
	return nil
}

func invokeHandler(handler func(Message), msg Message) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("remote: handler de mensagem falhou: %v", recovered)
		}
	}()
	handler(msg)
	return nil
}

// pollLoop retries transient failures without dropping its durable server
// cursor. A handler is at-least-once: a crash between dispatch and ack replays.
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
		if err == nil {
			for _, msg := range msgs {
				if msg.Sequence <= 0 {
					err = fmt.Errorf("remote: servidor retornou mensagem sem sequência")
					break
				}
				if msg.Sequence <= sm.currentCursor() {
					continue
				}
				if err = sm.dispatch(msg); err != nil {
					break
				}
				if err = sm.ackCursor(ctx, msg.Sequence); err != nil {
					break
				}
				sm.advanceCursor(msg.Sequence)
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			failures++
			if failures > 5 {
				failures = 5
			}
			backoff := reconnectDelay * time.Duration(1<<uint(failures-1))
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
	}
}

// poll performs a single long-poll request and returns any new messages.
func (sm *SessionManager) poll(ctx context.Context) ([]Message, error) {
	u := sm.sessionURL("/messages/poll") + "?after=" + strconv.FormatInt(sm.currentCursor(), 10) + "&wait=20"
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
	if err := decodeLimitedResponse(resp.Body, &msgs); err != nil {
		return nil, fmt.Errorf("remote: decode poll response: %w", err)
	}
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Sequence <= msgs[i-1].Sequence {
			return nil, fmt.Errorf("remote: mensagens retornadas fora de ordem")
		}
	}
	return msgs, nil
}

func (sm *SessionManager) ackCursor(ctx context.Context, sequence int64) error {
	body, err := json.Marshal(struct {
		Sequence int64 `json:"sequence"`
	}{Sequence: sequence})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sm.sessionURL("/cursor"), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("remote: build cursor request: %w", err)
	}
	sm.setAuth(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := sm.client.Do(req)
	if err != nil {
		return fmt.Errorf("remote: acknowledge cursor: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("remote: acknowledge cursor: HTTP %d", resp.StatusCode)
	}
	return nil
}
