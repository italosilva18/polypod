package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/costa/polypod/internal/adapter"
	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/ai"
	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/memory"
	"github.com/costa/polypod/internal/skill"
)

// agentResponse is the JSON response for an agent.
type agentResponse struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Skills      []string `json:"skills"`
}

// memoryResponse is the JSON response for a memory entry.
type memoryResponse struct {
	Topic     string `json:"topic"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// memoryRequest is the JSON body for creating a memory.
type memoryRequest struct {
	Topic   string `json:"topic"`
	Content string `json:"content"`
}

// sessionResponse is the JSON response for a session.
type sessionResponse struct {
	ID        string `json:"id"`
	Channel   string `json:"channel"`
	UserID    string `json:"user_id"`
	Messages  int    `json:"messages"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func listAgentsHandler(agents *agent.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		names := agents.List()
		resp := make([]agentResponse, 0, len(names))
		for _, name := range names {
			ag := agents.Get(name)
			resp = append(resp, agentResponse{
				Name:        ag.Name,
				Description: ag.Description,
				Skills:      ag.Skills,
			})
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func listSkillsHandler(skills *skill.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list := skills.List()
		writeJSON(w, http.StatusOK, map[string][]string{"skills": list})
	}
}

func listMemoriesHandler(memStore memory.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mems, err := memStore.List(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		resp := make([]memoryResponse, len(mems))
		for i, m := range mems {
			resp[i] = memoryResponse{
				Topic:     m.Topic,
				Content:   m.Content,
				CreatedAt: m.CreatedAt.Format(time.RFC3339),
				UpdatedAt: m.UpdatedAt.Format(time.RFC3339),
			}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func createMemoryHandler(memStore memory.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req memoryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		if req.Topic == "" || req.Content == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "topic and content required"})
			return
		}
		if err := memStore.Save(r.Context(), req.Topic, req.Content); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
	}
}

func deleteMemoryHandler(memStore memory.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		topic := chi.URLParam(r, "topic")
		if topic == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "topic required"})
			return
		}
		if err := memStore.Delete(r.Context(), topic); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func listSessionsHandler(convMgr *conversation.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessions := convMgr.ListSessions()
		resp := make([]sessionResponse, len(sessions))
		for i, s := range sessions {
			resp[i] = sessionResponse{
				ID:        s.ID,
				Channel:   s.Channel,
				UserID:    s.UserID,
				Messages:  len(s.Messages),
				CreatedAt: s.CreatedAt.Format(time.RFC3339),
				UpdatedAt: s.UpdatedAt.Format(time.RFC3339),
			}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func streamChatHandler(streamHandler adapter.StreamHandler, aiSvc *ai.Service) http.HandlerFunc {
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

		// Set SSE headers
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
			return
		}

		msg := adapter.InMessage{
			ID:        fmt.Sprintf("rest-%d", time.Now().UnixNano()),
			Channel:   "rest",
			UserID:    userID,
			Text:      req.Message,
			Timestamp: time.Now(),
		}

		chunks := make(chan adapter.StreamChunk, 64)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		go streamHandler(ctx, msg, chunks)

		for chunk := range chunks {
			if chunk.Error != nil {
				fmt.Fprintf(w, "event: error\ndata: %s\n\n", chunk.Error.Error())
				flusher.Flush()
				return
			}
			if chunk.Done {
				fmt.Fprintf(w, "event: done\ndata: [DONE]\n\n")
				flusher.Flush()
				return
			}
			// Escape newlines for SSE
			data := chunk.Delta
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
