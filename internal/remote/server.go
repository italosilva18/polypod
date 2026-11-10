package remote

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const participantHeader = "X-Polypod-Participant"

// ServerConfig configures a Polypod-owned remote session server. AuthToken is
// required; callers should provide HTTPS when serving beyond localhost.
type ServerConfig struct {
	DatabasePath string
	AuthToken    string
	Logger       *slog.Logger
	// OnUserMessage should durably enqueue a type=user message before returning.
	// It may be retried after crashes; deduplicate with Message.ID.
	OnUserMessage func(context.Context, RemoteSession, Message) error
}

// Server persists sessions, ordered messages and acknowledged cursors in
// SQLite. A shared bearer token grants access to all sessions on this server.
type Server struct {
	cfg   ServerConfig
	store *sessionStore
	log   *slog.Logger
	mux   *http.ServeMux

	notifyMu     sync.Mutex
	notify       chan struct{}
	forwardOwner string
}

func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.AuthToken == "" {
		return nil, fmt.Errorf("token de autenticação das sessões remotas é obrigatório")
	}
	store, err := openSessionStore(cfg.DatabasePath)
	if err != nil {
		return nil, err
	}
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	owner, err := newID("forwarder_")
	if err != nil {
		if closeErr := store.db.Close(); closeErr != nil {
			return nil, fmt.Errorf("gerando dono da entrega: %w; fechando banco: %v", err, closeErr)
		}
		return nil, err
	}
	s := &Server{cfg: cfg, store: store, log: log, mux: http.NewServeMux(), notify: make(chan struct{}), forwardOwner: owner}
	s.mux.HandleFunc("GET /v1/sessions", s.handleList)
	s.mux.HandleFunc("POST /v1/sessions", s.handleCreate)
	s.mux.HandleFunc("GET /v1/sessions/{id}", s.handleGet)
	s.mux.HandleFunc("POST /v1/sessions/{id}/join", s.handleJoin)
	s.mux.HandleFunc("POST /v1/sessions/{id}/messages", s.handleSend)
	s.mux.HandleFunc("GET /v1/sessions/{id}/messages/poll", s.handlePoll)
	s.mux.HandleFunc("POST /v1/sessions/{id}/cursor", s.handleAck)
	return s, nil
}

// Close releases the SQLite store after HTTP serving has stopped.
func (s *Server) Close() error { return s.store.db.Close() }

// ListenAndServe serves until ctx is cancelled. It does not choose a public
// interface implicitly; the caller supplies an address such as 127.0.0.1:8765.
func (s *Server) ListenAndServe(ctx context.Context, address string) error {
	if address == "" {
		return fmt.Errorf("endereço de escuta das sessões remotas é obrigatório")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("abrindo endereço das sessões remotas: %w", err)
	}
	return s.Serve(ctx, listener)
}

// Serve runs on an already-bound listener, allowing the caller to verify bind
// synchronously before starting the server in a goroutine. TLS is the caller's
// responsibility: pass either a cleartext listener or one already wrapped with
// tls.NewListener. Serve does not wrap the listener again.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	if listener == nil {
		return fmt.Errorf("listener das sessões remotas é obrigatório")
	}
	server := &http.Server{
		Handler: s, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 35 * time.Second,
		IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10,
	}
	forwardCtx, stopForward := context.WithCancel(ctx)
	var forwardDone chan struct{}
	if s.cfg.OnUserMessage != nil {
		forwardDone = make(chan struct{})
		go func() {
			defer close(forwardDone)
			s.RunForwarder(forwardCtx)
		}()
	}
	defer func() {
		stopForward()
		if forwardDone != nil {
			<-forwardDone
		}
	}()
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Shutdown(shutdownCtx); err != nil {
				s.log.Error("falha ao encerrar servidor de sessões remotas", "error", err)
			}
		case <-done:
		}
	}()
	err := server.Serve(listener)
	close(done)
	if errors.Is(err, http.ErrServerClosed) && ctx.Err() != nil {
		return nil
	}
	return err
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, prefix) ||
		subtle.ConstantTimeCompare([]byte(auth[len(prefix):]), []byte(s.cfg.AuthToken)) != 1 {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "token remoto inválido", http.StatusUnauthorized)
		return
	}
	if !publicParticipant(r.Header.Get(participantHeader)) {
		http.Error(w, "identidade do participante ausente ou inválida", http.StatusBadRequest)
		return
	}
	s.mux.ServeHTTP(w, r)
}

func validParticipant(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, ch := range id {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') || ch == '_' || ch == '-' || ch == '.' || ch == '@' || ch == ':') {
			return false
		}
	}
	return true
}

func publicParticipant(id string) bool {
	if !validParticipant(id) {
		return false
	}
	return !strings.EqualFold(id, "polypod") && !strings.EqualFold(id, "assistant") && !strings.EqualFold(id, "system")
}

func validSessionID(id string) bool {
	if !strings.HasPrefix(id, "session_") || len(id) != len("session_")+32 {
		return false
	}
	for _, ch := range id[len("session_"):] {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			return false
		}
	}
	return true
}

func (s *Server) routeID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !validSessionID(id) {
		http.Error(w, "ID de sessão inválido", http.StatusBadRequest)
		return "", false
	}
	return id, true
}

func (s *Server) participant(r *http.Request) string { return r.Header.Get(participantHeader) }

func (s *Server) writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(data)+1 > maxResponseBytes {
		http.Error(w, "resposta remota excede 2 MiB", http.StatusRequestEntityTooLarge)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(append(data, '\n')); err != nil {
		s.log.Warn("resposta da sessão remota interrompida", "error", err)
	}
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrSessionNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotParticipant):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrMessageConflict):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, ErrInvalidCursor):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		s.log.Error("falha no servidor de sessões remotas", "error", err)
		http.Error(w, "falha interna nas sessões remotas", http.StatusInternalServerError)
	}
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.store.listSessions(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, sessions)
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	session, err := s.store.createSession(r.Context(), s.participant(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, session)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	id, ok := s.routeID(w, r)
	if !ok {
		return
	}
	session, err := s.store.getSession(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	id, ok := s.routeID(w, r)
	if !ok {
		return
	}
	session, err := s.store.joinSession(r.Context(), id, s.participant(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, session)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("corpo contém JSON adicional")
		}
		return err
	}
	return nil
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	id, ok := s.routeID(w, r)
	if !ok {
		return
	}
	var msg Message
	if err := decodeJSON(w, r, 512<<10, &msg); err != nil {
		http.Error(w, "mensagem JSON inválida: "+err.Error(), http.StatusBadRequest)
		return
	}
	msg.Type = strings.TrimSpace(msg.Type)
	if msg.Type != "user" || strings.TrimSpace(msg.ID) == "" || len(msg.ID) > 128 ||
		strings.TrimSpace(msg.Content) == "" || len(msg.Content) > 256<<10 {
		http.Error(w, "POST público aceita somente mensagem type=user com ID e conteúdo válidos", http.StatusBadRequest)
		return
	}
	stored, err := s.store.insertMessage(r.Context(), id, s.participant(r), msg)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.notifyReaders()
	s.writeJSON(w, http.StatusCreated, stored)
}

// Publish writes an agent response into a session using a stable outbox ID.
// The caller should set Message.ID to its durable outbox identifier so a
// retry after a lost HTTP response cannot create a duplicate response.
func (s *Server) Publish(ctx context.Context, sessionID string, msg Message) (Message, error) {
	if !validSessionID(sessionID) {
		return Message{}, fmt.Errorf("ID de sessão inválido")
	}
	if msg.ID == "" || msg.Type != "assistant" || len(msg.ID) > 128 || len(msg.Content) > 256<<10 {
		return Message{}, fmt.Errorf("mensagem de saída inválida")
	}
	if _, err := s.store.joinSession(ctx, sessionID, "polypod"); err != nil {
		return Message{}, err
	}
	stored, err := s.store.insertMessage(ctx, sessionID, "polypod", msg)
	if err != nil {
		return Message{}, err
	}
	s.notifyReaders()
	return stored, nil
}

// RunForwarder retries persisted user messages until OnUserMessage confirms
// durable acceptance. ListenAndServe starts it automatically; callers that
// embed Server in another HTTP server should run it with the same lifetime.
func (s *Server) RunForwarder(ctx context.Context) {
	if s.cfg.OnUserMessage == nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		for {
			processed, err := s.forwardOne(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				s.log.Warn("mensagem remota aguardará novo encaminhamento", "error", err)
				break
			}
			if !processed {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.currentNotification():
		}
	}
}

func (s *Server) forwardOne(ctx context.Context) (bool, error) {
	sessionID, msg, claimed, err := s.store.claimForward(ctx, s.forwardOwner)
	if err != nil || !claimed {
		return claimed, err
	}
	session, err := s.store.getSession(ctx, sessionID)
	if err != nil {
		if releaseErr := s.store.finishForward(ctx, msg.Sequence, s.forwardOwner, false); releaseErr != nil {
			return true, errors.Join(err, releaseErr)
		}
		return true, err
	}
	callbackCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err = s.invokeUserCallback(callbackCtx, session, msg)
	cancel()
	// Persist the marker even if shutdown cancelled the callback context.
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
	finishErr := s.store.finishForward(finishCtx, msg.Sequence, s.forwardOwner, err == nil)
	finishCancel()
	if finishErr != nil {
		return true, errors.Join(err, finishErr)
	}
	return true, err
}

func (s *Server) invokeUserCallback(ctx context.Context, session RemoteSession, msg Message) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("callback de mensagem remota falhou: %v", recovered)
		}
	}()
	return s.cfg.OnUserMessage(ctx, session, msg)
}

func (s *Server) notifyReaders() {
	s.notifyMu.Lock()
	close(s.notify)
	s.notify = make(chan struct{})
	s.notifyMu.Unlock()
}

func (s *Server) currentNotification() <-chan struct{} {
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	return s.notify
}

func parseCursor(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value > math.MaxInt64 {
		return 0, fmt.Errorf("cursor inválido")
	}
	return int64(value), nil
}

func (s *Server) handlePoll(w http.ResponseWriter, r *http.Request) {
	id, ok := s.routeID(w, r)
	if !ok {
		return
	}
	after, err := parseCursor(r.URL.Query().Get("after"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	wait := 20
	if raw := r.URL.Query().Get("wait"); raw != "" {
		wait, err = strconv.Atoi(raw)
		if err != nil || wait < 0 || wait > 25 {
			http.Error(w, "espera de polling inválida", http.StatusBadRequest)
			return
		}
	}
	acked, err := s.store.cursor(r.Context(), id, s.participant(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	if after < acked {
		after = acked
	}
	deadline := time.NewTimer(time.Duration(wait) * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		msgs, err := s.store.messagesAfter(r.Context(), id, after, 100)
		if err != nil {
			s.fail(w, err)
			return
		}
		if len(msgs) > 0 {
			low, high := 1, len(msgs)
			for low < high {
				mid := (low + high + 1) / 2
				encoded, encodeErr := json.Marshal(msgs[:mid])
				if encodeErr != nil {
					s.fail(w, encodeErr)
					return
				}
				if len(encoded)+1 <= maxResponseBytes {
					low = mid
				} else {
					high = mid - 1
				}
			}
			msgs = msgs[:low]
			w.Header().Set("X-Polypod-Next-Cursor", strconv.FormatInt(msgs[len(msgs)-1].Sequence, 10))
			s.writeJSON(w, http.StatusOK, msgs)
			return
		}
		if wait == 0 {
			s.writeJSON(w, http.StatusOK, []Message{})
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			s.writeJSON(w, http.StatusOK, []Message{})
			return
		case <-ticker.C:
		case <-s.currentNotification():
		}
	}
}

func (s *Server) handleAck(w http.ResponseWriter, r *http.Request) {
	id, ok := s.routeID(w, r)
	if !ok {
		return
	}
	var request struct {
		Sequence int64 `json:"sequence"`
	}
	if err := decodeJSON(w, r, 1024, &request); err != nil || request.Sequence < 0 {
		http.Error(w, "ack de cursor inválido", http.StatusBadRequest)
		return
	}
	if err := s.store.acknowledge(r.Context(), id, s.participant(r), request.Sequence); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
