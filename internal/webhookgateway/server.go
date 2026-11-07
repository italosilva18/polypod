// Package webhookgateway accepts authenticated HTTP events into the durable
// human loop inbox. It never executes agent tools in an HTTP handler.
package webhookgateway

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/costa/polypod/internal/config"
	"github.com/costa/polypod/internal/humanloop"
)

const (
	Channel        = "webhook"
	DefaultListen  = "127.0.0.1:8788"
	MaxBodyBytes   = 64 << 10
	MaxTextBytes   = 16 << 10
	MaxEventIDSize = 128
)

type event struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type accepted struct {
	ID     string           `json:"id"`
	Status humanloop.Status `json:"status"`
}

type result struct {
	ID          string           `json:"id"`
	Status      humanloop.Status `json:"status"`
	Attempts    int              `json:"attempts"`
	Response    string           `json:"response,omitempty"`
	Error       string           `json:"error,omitempty"`
	UpdatedAt   time.Time        `json:"updated_at"`
	CompletedAt *time.Time       `json:"completed_at,omitempty"`
}

// Server owns a loopback listener. Every accepted event is durable before the
// 202 response is sent, and callers can safely retry using the same event ID.
type Server struct {
	cfg    config.WebhookConfig
	store  *humanloop.Store
	wake   func()
	logger *slog.Logger
	slots  chan struct{}

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func New(cfg config.WebhookConfig, store *humanloop.Store, wake func(), logger *slog.Logger) (*Server, error) {
	if !cfg.Enabled {
		return nil, errors.New("webhookgateway: canal desabilitado")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if store == nil {
		return nil, errors.New("webhookgateway: inbox obrigatoria")
	}
	if cfg.Listen == "" {
		cfg.Listen = DefaultListen
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{cfg: cfg, store: store, wake: wake, logger: logger, slots: make(chan struct{}, 8)}, nil
}

// Start binds synchronously so an unavailable listener fails startup. The
// serving goroutine exits when the parent context is canceled or Stop runs.
func (s *Server) Start(parent context.Context) error {
	if err := parent.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return errors.New("webhookgateway: servidor ja iniciado")
	}
	listener, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	s.cancel, s.done = cancel, done
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/inbox", s.handleInbox)
	mux.HandleFunc("/v1/inbox/", s.handleStatus)
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
	go func() {
		<-runCtx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			s.logger.Warn("webhook shutdown incomplete", "error", err)
			if closeErr := server.Close(); closeErr != nil {
				s.logger.Warn("webhook forced close failed", "error", closeErr)
			}
		}
	}()
	go func() {
		defer close(done)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("webhook listener stopped", "error", err)
		}
		cancel()
	}()
	return nil
}

// Stop closes the listener and waits for its goroutine. Repeated calls are safe.
func (s *Server) Stop() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	setPrivateResponseHeaders(w)
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "metodo nao permitido", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorized(r) {
		http.Error(w, "nao autorizado", http.StatusUnauthorized)
		return
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		http.Error(w, "muitas requisicoes simultaneas", http.StatusTooManyRequests)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "Content-Type deve ser application/json", http.StatusUnsupportedMediaType)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "corpo excede o limite", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "falha ao ler corpo", http.StatusBadRequest)
		return
	}
	if !utf8.Valid(body) {
		http.Error(w, "JSON deve ser UTF-8", http.StatusBadRequest)
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var input event
	if err := decoder.Decode(&input); err != nil {
		http.Error(w, "JSON invalido", http.StatusBadRequest)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		http.Error(w, "JSON deve conter um unico objeto", http.StatusBadRequest)
		return
	}
	if !validEventID(input.ID) || len(input.Text) > MaxTextBytes ||
		strings.TrimSpace(input.Text) == "" || strings.ContainsRune(input.Text, '\x00') {
		http.Error(w, "id ou texto invalido", http.StatusBadRequest)
		return
	}
	msg, err := s.store.EnqueueInboundOnce(Channel, "webhook", input.Text, input.ID)
	if err != nil {
		s.logger.Error("webhook inbox enqueue failed", "error", err)
		http.Error(w, "falha ao gravar evento", http.StatusServiceUnavailable)
		return
	}
	if msg.Text != input.Text {
		// Reusing an event ID with a different payload must never appear to
		// accept new work. A retained dedup key without its old body also
		// cannot prove equality, so it is treated as a conflict.
		http.Error(w, "id ja usado para outro evento", http.StatusConflict)
		return
	}
	if s.wake != nil {
		s.wake()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	if err := json.NewEncoder(w).Encode(accepted{ID: msg.ID, Status: msg.Status}); err != nil {
		s.logger.Debug("webhook response write failed", "error", err)
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	setPrivateResponseHeaders(w)
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "metodo nao permitido", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorized(r) {
		http.Error(w, "nao autorizado", http.StatusUnauthorized)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/inbox/")
	if len(id) < 5 || len(id) > 64 || !strings.HasPrefix(id, "msg_") || !validEventID(id) {
		http.Error(w, "item nao encontrado", http.StatusNotFound)
		return
	}
	msg, found := s.store.GetByID(id)
	if !found || msg.Channel != Channel || msg.Direction != humanloop.DirectionInbound {
		http.Error(w, "item nao encontrado", http.StatusNotFound)
		return
	}
	status := result{ID: msg.ID, Status: msg.Status, Attempts: msg.Attempts,
		UpdatedAt: msg.UpdatedAt, CompletedAt: msg.CompletedAt}
	if msg.Status == humanloop.StatusCompleted {
		status.Response = msg.Response
	}
	if msg.Status == humanloop.StatusFailed || msg.Status == humanloop.StatusResolved {
		status.Error = msg.Error
	}
	w.Header().Set("Content-Type", "application/json")
	if msg.Status == humanloop.StatusPending || msg.Status == humanloop.StatusProcessing {
		w.Header().Set("Retry-After", "2")
	}
	if err := json.NewEncoder(w).Encode(status); err != nil {
		s.logger.Debug("webhook status write failed", "error", err)
	}
}

func setPrivateResponseHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func (s *Server) authorized(r *http.Request) bool {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return false
	}
	scheme, supplied, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(supplied)), []byte(s.cfg.Token)) == 1
}

func validEventID(id string) bool {
	if id == "" || len(id) > MaxEventIDSize {
		return false
	}
	for _, ch := range id {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' ||
			ch == '-' || ch == '_' || ch == '.' || ch == ':' {
			continue
		}
		return false
	}
	return true
}
