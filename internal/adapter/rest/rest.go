package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/costa/polypod/internal/adapter"
	"github.com/costa/polypod/internal/auth"
)

// ChatRequest is the JSON body for chat endpoint.
type ChatRequest struct {
	Message  string `json:"message"`
	UserID   string `json:"user_id,omitempty"`
}

// ChatResponse is the JSON response from chat endpoint.
type ChatResponse struct {
	Reply string `json:"reply"`
}

// Adapter implements the REST API channel.
type Adapter struct {
	server *http.Server
	authz  *auth.Authorizer
	logger *slog.Logger
	host   string
	port   int
}

// New creates a new REST adapter.
func New(host string, port int, authz *auth.Authorizer, logger *slog.Logger) *Adapter {
	return &Adapter{
		authz:  authz,
		logger: logger,
		host:   host,
		port:   port,
	}
}

func (a *Adapter) Name() string { return "rest" }

func (a *Adapter) Start(ctx context.Context, handler adapter.MessageHandler) error {
	r := chi.NewRouter()

	r.Use(chimw.Recoverer)
	r.Use(chimw.RealIP)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(APIKeyAuth(a.authz))

		r.Post("/chat", a.chatHandler(handler))
		r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok"}`))
		})
	})

	// Public routes
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"name":"polypod","version":"0.1.0","endpoint":"POST /api/v1/chat"}`))
	})
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	addr := fmt.Sprintf("%s:%d", a.host, a.port)
	a.server = &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	a.logger.Info("REST API listening", "addr", addr)

	errCh := make(chan error, 1)
	go func() {
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return a.server.Shutdown(shutCtx)
	}
}

func (a *Adapter) Send(ctx context.Context, msg adapter.OutMessage) error {
	// REST responses are returned synchronously in the handler
	return nil
}

func (a *Adapter) chatHandler(handler adapter.MessageHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}

		if req.Message == "" {
			http.Error(w, `{"error":"message is required"}`, http.StatusBadRequest)
			return
		}

		userID := req.UserID
		if userID == "" {
			userID = r.RemoteAddr
		}

		in := adapter.InMessage{
			ID:        fmt.Sprintf("rest-%d", time.Now().UnixNano()),
			Channel:   "rest",
			UserID:    userID,
			Text:      req.Message,
			Timestamp: time.Now(),
		}

		out, err := handler(r.Context(), in)
		if err != nil {
			a.logger.Error("handler error", "error", err)
			http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatResponse{Reply: out.Text})
	}
}
