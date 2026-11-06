package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ─── Elicitation Types ──────────────────────────────────────────────────────

// ElicitationAction is the user's response to an elicitation.
type ElicitationAction string

const (
	ElicitationAccept  ElicitationAction = "accept"
	ElicitationDecline ElicitationAction = "decline"
	ElicitationCancel  ElicitationAction = "cancel"
)

// ElicitationRequest represents an MCP server's request for user input.
type ElicitationRequest struct {
	// ServerName identifies which MCP server sent the request.
	ServerName string `json:"serverName"`

	// RequestID is the JSON-RPC request ID from the server.
	RequestID RequestID `json:"requestId"`

	// Message is the human-readable prompt for the user.
	Message string `json:"message"`

	// RequestedSchema is an optional JSON Schema defining the expected response.
	RequestedSchema map[string]any `json:"requestedSchema,omitempty"`

	// Mode is "form" for schema-based input or "url" for browser-based auth.
	Mode string `json:"mode,omitempty"` // "form" or "url"

	// URL is the URL to open when Mode is "url".
	URL string `json:"url,omitempty"`

	// ElicitationID links completion notifications to this request (for URL mode).
	ElicitationID string `json:"elicitationId,omitempty"`
}

// ElicitationResult is the user's response to an elicitation.
type ElicitationResult struct {
	Action  ElicitationAction `json:"action"`
	Content map[string]any    `json:"content,omitempty"`
}

// ─── Elicitation Handler ────────────────────────────────────────────────────

// ElicitationHandler is a callback that presents an elicitation to the user.
// It should block until the user responds or the context is cancelled.
type ElicitationHandler func(ctx context.Context, req *ElicitationRequest) (*ElicitationResult, error)

// ─── Elicitation Queue ──────────────────────────────────────────────────────

// ElicitationQueue manages pending elicitation requests.
type ElicitationQueue struct {
	mu      sync.Mutex
	pending []*pendingElicitation
	handler ElicitationHandler
	logger  *slog.Logger
	timeout time.Duration
}

type pendingElicitation struct {
	request  *ElicitationRequest
	resultCh chan *ElicitationResult
	cancel   context.CancelFunc
}

// NewElicitationQueue creates a new elicitation queue.
func NewElicitationQueue(handler ElicitationHandler, logger *slog.Logger) *ElicitationQueue {
	return &ElicitationQueue{
		handler: handler,
		logger:  logger,
		timeout: 5 * time.Minute,
	}
}

// SetTimeout sets the maximum time to wait for a user response.
func (q *ElicitationQueue) SetTimeout(d time.Duration) {
	q.timeout = d
}

// HandleElicitation processes an elicitation request from an MCP server.
// It queues the request and returns the user's response.
func (q *ElicitationQueue) HandleElicitation(ctx context.Context, serverName string, requestID RequestID, params json.RawMessage) (*ElicitationResult, error) {
	// Parse the elicitation parameters
	var elicitParams struct {
		Message         string         `json:"message"`
		RequestedSchema map[string]any `json:"requestedSchema,omitempty"`
		Mode            string         `json:"mode,omitempty"`
		URL             string         `json:"url,omitempty"`
		ElicitationID   string         `json:"elicitationId,omitempty"`
	}

	if err := json.Unmarshal(params, &elicitParams); err != nil {
		return nil, fmt.Errorf("parse elicitation params: %w", err)
	}

	req := &ElicitationRequest{
		ServerName:      serverName,
		RequestID:       requestID,
		Message:         elicitParams.Message,
		RequestedSchema: elicitParams.RequestedSchema,
		Mode:            elicitParams.Mode,
		URL:             elicitParams.URL,
		ElicitationID:   elicitParams.ElicitationID,
	}

	if req.Mode == "" {
		req.Mode = "form"
	}

	if q.handler == nil {
		if q.logger != nil {
			q.logger.Warn("no elicitation handler registered, cancelling",
				"server", serverName,
				"message", req.Message,
			)
		}
		return &ElicitationResult{Action: ElicitationCancel}, nil
	}

	// Create a context with timeout
	elicitCtx, cancel := context.WithTimeout(ctx, q.timeout)

	pe := &pendingElicitation{
		request:  req,
		resultCh: make(chan *ElicitationResult, 1),
		cancel:   cancel,
	}

	q.mu.Lock()
	q.pending = append(q.pending, pe)
	q.mu.Unlock()

	defer func() {
		cancel()
		q.removePending(pe)
	}()

	// Run handler in goroutine
	go func() {
		result, err := q.handler(elicitCtx, req)
		if err != nil {
			if q.logger != nil {
				q.logger.Warn("elicitation handler error",
					"server", serverName,
					"error", err,
				)
			}
			pe.resultCh <- &ElicitationResult{Action: ElicitationCancel}
			return
		}
		pe.resultCh <- result
	}()

	// Wait for result
	select {
	case result := <-pe.resultCh:
		return result, nil
	case <-elicitCtx.Done():
		return &ElicitationResult{Action: ElicitationCancel}, nil
	}
}

func (q *ElicitationQueue) removePending(pe *pendingElicitation) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for i, p := range q.pending {
		if p == pe {
			q.pending = append(q.pending[:i], q.pending[i+1:]...)
			return
		}
	}
}

// PendingCount returns the number of pending elicitation requests.
func (q *ElicitationQueue) PendingCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// CancelAll cancels all pending elicitation requests.
func (q *ElicitationQueue) CancelAll() {
	q.mu.Lock()
	pending := make([]*pendingElicitation, len(q.pending))
	copy(pending, q.pending)
	q.pending = nil
	q.mu.Unlock()

	for _, pe := range pending {
		pe.cancel()
	}
}

// MarkCompleted marks a URL-mode elicitation as completed.
// Called when the server sends an elicitation completion notification.
func (q *ElicitationQueue) MarkCompleted(serverName, elicitationID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	for _, pe := range q.pending {
		if pe.request.ServerName == serverName &&
			pe.request.ElicitationID == elicitationID &&
			pe.request.Mode == "url" {
			// Signal completion by sending accept
			select {
			case pe.resultCh <- &ElicitationResult{Action: ElicitationAccept}:
			default:
			}
			return true
		}
	}
	return false
}

// ─── Elicitation Registration on Connection ─────────────────────────────────

// RegisterElicitationHandler registers the elicitation request handler on an
// MCPConnection. The connection will route incoming elicitation requests to the
// provided queue.
func RegisterElicitationHandler(conn *MCPConnection, queue *ElicitationQueue) {
	conn.OnRequest(MethodElicit, func(ctx context.Context, id RequestID, method string, params json.RawMessage) (*JSONRPCMessage, error) {
		result, err := queue.HandleElicitation(ctx, conn.Name, id, params)
		if err != nil {
			return nil, err
		}

		return NewResult(id, result)
	})
}

// ─── Schema Validation (basic) ──────────────────────────────────────────────

// ValidateElicitationResponse performs basic validation of a user's response
// against the requested JSON schema. This is a simplified implementation that
// checks required fields and basic types.
func ValidateElicitationResponse(schema map[string]any, response map[string]any) error {
	if schema == nil {
		return nil
	}

	// Check required fields
	required, _ := schema["required"].([]any)
	for _, r := range required {
		fieldName, ok := r.(string)
		if !ok {
			continue
		}
		if _, exists := response[fieldName]; !exists {
			return fmt.Errorf("required field %q missing", fieldName)
		}
	}

	// Check property types
	properties, _ := schema["properties"].(map[string]any)
	for name, propSchema := range properties {
		val, exists := response[name]
		if !exists {
			continue
		}

		prop, ok := propSchema.(map[string]any)
		if !ok {
			continue
		}

		expectedType, _ := prop["type"].(string)
		if err := validateType(name, val, expectedType); err != nil {
			return err
		}
	}

	return nil
}

func validateType(name string, value any, expectedType string) error {
	if value == nil {
		return nil
	}

	switch expectedType {
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("field %q expected string, got %T", name, value)
		}
	case "number", "integer":
		switch value.(type) {
		case float64, int, int64, json.Number:
			// ok
		default:
			return fmt.Errorf("field %q expected number, got %T", name, value)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("field %q expected boolean, got %T", name, value)
		}
	case "array":
		if _, ok := value.([]any); !ok {
			return fmt.Errorf("field %q expected array, got %T", name, value)
		}
	case "object":
		if _, ok := value.(map[string]any); !ok {
			return fmt.Errorf("field %q expected object, got %T", name, value)
		}
	}

	return nil
}

// ─── Default (no-op) Elicitation Handler ────────────────────────────────────

// DefaultElicitationHandler returns cancel for all requests. Used before a real
// handler is registered.
func DefaultElicitationHandler(_ context.Context, _ *ElicitationRequest) (*ElicitationResult, error) {
	return &ElicitationResult{Action: ElicitationCancel}, errors.New("no elicitation handler registered")
}
