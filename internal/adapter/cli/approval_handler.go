package cli

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/costa/polypod/internal/permissions"
)

// BubbleteaApprovalHandler is a permissions.AskApprovalFn compatible handler
// that integrates with the Bubbletea TUI by routing approval requests
// through channels instead of reading from stdin.
//
// The flow is:
//  1. The permission enforcer calls AskApproval in the tool goroutine.
//  2. AskApproval sends an approvalRequest to requestCh and blocks waiting
//     on a per-request response channel.
//  3. The Bubbletea model listens on requestCh, displays an overlay modal,
//     collects the user's decision, and calls SendResponse.
//  4. SendResponse delivers the decision to the pending request goroutine
//     which unblocks AskApproval.
type BubbleteaApprovalHandler struct {
	mu         sync.Mutex
	requestCh  chan approvalRequest
	responders map[string]chan permissions.ApprovalResponse
	timeout    time.Duration
	yoloMode   bool
	seq        uint64
}

// approvalRequest carries a pending approval request from the enforcer
// goroutine to the Bubbletea model.
type approvalRequest struct {
	id  string
	req permissions.ApprovalRequest
}

// NewBubbleteaApprovalHandler creates a new handler ready to be wired into
// the Bubbletea model and the permission enforcer.
func NewBubbleteaApprovalHandler() *BubbleteaApprovalHandler {
	return &BubbleteaApprovalHandler{
		requestCh:  make(chan approvalRequest, 8),
		responders: make(map[string]chan permissions.ApprovalResponse),
		timeout:    5 * time.Minute,
	}
}

// RequestChannel returns the channel the TUI should listen on for new
// approval requests. The Bubbletea model runs a goroutine that reads from
// this channel and converts each request into an approvalRequestMsg.
func (h *BubbleteaApprovalHandler) RequestChannel() <-chan approvalRequest {
	return h.requestCh
}

// SendResponse is called by the TUI to send the user's decision back to
// the goroutine that is blocked in AskApproval.
func (h *BubbleteaApprovalHandler) SendResponse(id string, response permissions.ApprovalResponse) {
	h.mu.Lock()
	ch, ok := h.responders[id]
	if ok {
		delete(h.responders, id)
	}
	h.mu.Unlock()
	if !ok {
		return
	}
	// Non-blocking send: the responder channel is buffered (cap 1).
	select {
	case ch <- response:
	default:
	}
}

// SetYOLO enables YOLO mode: every request is auto-approved without
// bothering the user.
func (h *BubbleteaApprovalHandler) SetYOLO(enabled bool) {
	h.mu.Lock()
	h.yoloMode = enabled
	h.mu.Unlock()
}

// SetTimeout adjusts the maximum time AskApproval will wait for a response
// from the TUI before auto-denying.
func (h *BubbleteaApprovalHandler) SetTimeout(d time.Duration) {
	h.mu.Lock()
	h.timeout = d
	h.mu.Unlock()
}

// AskApproval implements permissions.AskApprovalFn. It blocks the caller
// until the user responds via the TUI, the context is cancelled, or the
// timeout fires.
func (h *BubbleteaApprovalHandler) AskApproval(ctx context.Context, req permissions.ApprovalRequest) (permissions.ApprovalResponse, error) {
	h.mu.Lock()
	if h.yoloMode {
		h.mu.Unlock()
		return permissions.ApprovalResponse{Allowed: true}, nil
	}
	h.seq++
	id := fmt.Sprintf("approval-%d", h.seq)
	respCh := make(chan permissions.ApprovalResponse, 1)
	h.responders[id] = respCh
	timeout := h.timeout
	h.mu.Unlock()

	// Send the request to the TUI. We block here on purpose: the buffer
	// size (8) should be enough for normal use, but if it fills up we
	// simply wait for the TUI to drain it.
	select {
	case h.requestCh <- approvalRequest{id: id, req: req}:
	case <-ctx.Done():
		h.mu.Lock()
		delete(h.responders, id)
		h.mu.Unlock()
		return permissions.ApprovalResponse{}, ctx.Err()
	}

	var timeoutCh <-chan time.Time
	if timeout > 0 {
		timeoutCh = time.After(timeout)
	}

	select {
	case resp := <-respCh:
		return resp, nil
	case <-ctx.Done():
		h.mu.Lock()
		delete(h.responders, id)
		h.mu.Unlock()
		return permissions.ApprovalResponse{}, ctx.Err()
	case <-timeoutCh:
		h.mu.Lock()
		delete(h.responders, id)
		h.mu.Unlock()
		return permissions.ApprovalResponse{
			Allowed:  false,
			Feedback: fmt.Sprintf("approval timed out after %s", timeout),
		}, nil
	}
}
