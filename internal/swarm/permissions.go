package swarm

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/costa/polypod/internal/types"
)

// PermissionRequestStatus tracks the lifecycle of a permission request.
type PermissionRequestStatus string

const (
	PermStatusPending  PermissionRequestStatus = "pending"
	PermStatusApproved PermissionRequestStatus = "approved"
	PermStatusRejected PermissionRequestStatus = "rejected"
)

// PermissionRequest is a worker's request for tool-use approval from the leader.
// Equivalent to Claude Code's SwarmPermissionRequest in permissionSync.ts.
type PermissionRequest struct {
	ID          string                  `json:"id"`
	WorkerID    string                  `json:"worker_id"`
	WorkerName  string                  `json:"worker_name"`
	WorkerColor string                  `json:"worker_color,omitempty"`
	TeamName    string                  `json:"team_name"`
	ToolName    string                  `json:"tool_name"`
	ToolUseID   string                  `json:"tool_use_id"`
	Description string                  `json:"description"`
	Input       map[string]any          `json:"input"`
	Status      PermissionRequestStatus `json:"status"`
	CreatedAt   time.Time               `json:"created_at"`
	ResolvedAt  *time.Time              `json:"resolved_at,omitempty"`
	ResolvedBy  string                  `json:"resolved_by,omitempty"`
	Feedback    string                  `json:"feedback,omitempty"`
}

// PermissionResponse is the leader's decision on a permission request.
type PermissionResponse struct {
	RequestID    string                  `json:"request_id"`
	Decision     PermissionRequestStatus `json:"decision"` // approved or rejected
	Feedback     string                  `json:"feedback,omitempty"`
	UpdatedInput map[string]any          `json:"updated_input,omitempty"`
	Rules        []types.PermissionRule  `json:"rules,omitempty"` // "always allow" rules
}

// permReqIDCounter generates unique permission request IDs.
var permReqIDCounter uint64

// GeneratePermissionRequestID generates a unique permission request identifier.
func GeneratePermissionRequestID() string {
	n := atomic.AddUint64(&permReqIDCounter, 1)
	return fmt.Sprintf("perm-%d-%d", time.Now().UnixMilli(), n)
}

// pendingReq wraps a request with its response channel.
type pendingReq struct {
	req    *PermissionRequest
	respCh chan PermissionResponse
}

// PermissionBridge coordinates permission delegation between workers and
// the team leader. Workers call BubbleUpPermission() which blocks until
// the leader calls GrantPermission() or DenyPermission().
//
// Equivalent to Claude Code's leaderPermissionBridge.ts + permissionSync.ts.
//
// Architecture:
//   - Workers submit requests that go into a pending queue.
//   - The leader (or its coordinator) drains the queue and resolves each.
//   - Each request has a response channel; the worker blocks on it.
//   - Auto-forward rules allow certain tool patterns to skip the queue.
type PermissionBridge struct {
	mu     sync.Mutex
	team   *Team
	logger *slog.Logger

	// Pending requests keyed by request ID.
	pending map[string]*pendingReq

	// Auto-forward rules: if a tool+pattern matches, auto-approve.
	autoRules []AutoForwardRule

	// Channel signaled when a new request is added (capacity 1).
	notifyCh chan struct{}

	closed int32 // atomic
}

// AutoForwardRule defines an auto-approval rule propagated from the leader.
// When a worker's tool call matches ToolName and the input satisfies the
// pattern, the request is auto-approved without leader interaction.
type AutoForwardRule struct {
	ToolName string `json:"tool_name"`
	Pattern  string `json:"pattern"` // glob-like pattern or exact match
}

// NewPermissionBridge creates a permission bridge for a team.
func NewPermissionBridge(team *Team, logger *slog.Logger) *PermissionBridge {
	return &PermissionBridge{
		team:     team,
		logger:   logger,
		pending:  make(map[string]*pendingReq),
		notifyCh: make(chan struct{}, 1),
	}
}

// BubbleUpPermission submits a permission request from a worker and blocks
// until the leader resolves it or the context is cancelled.
//
// This is the worker-side API. The worker goroutine calls this and waits.
// Returns the leader's decision or an error on timeout/cancellation.
func (pb *PermissionBridge) BubbleUpPermission(ctx context.Context, req *PermissionRequest) (PermissionResponse, error) {
	if atomic.LoadInt32(&pb.closed) != 0 {
		return PermissionResponse{}, ErrMailboxClosed
	}

	// Check auto-forward rules first.
	if pb.matchesAutoForward(req) {
		pb.logger.Debug("permission auto-approved by forward rule",
			"request_id", req.ID,
			"tool", req.ToolName,
			"worker", req.WorkerName,
		)
		return PermissionResponse{
			RequestID: req.ID,
			Decision:  PermStatusApproved,
		}, nil
	}

	req.Status = PermStatusPending
	req.CreatedAt = time.Now()

	respCh := make(chan PermissionResponse, 1)

	pb.mu.Lock()
	pb.pending[req.ID] = &pendingReq{req: req, respCh: respCh}
	pb.mu.Unlock()

	// Signal the leader that there's a new request.
	select {
	case pb.notifyCh <- struct{}{}:
	default:
	}

	pb.logger.Debug("permission request submitted",
		"request_id", req.ID,
		"tool", req.ToolName,
		"worker", req.WorkerName,
		"team", req.TeamName,
	)

	// Block until resolved or cancelled.
	select {
	case resp := <-respCh:
		return resp, nil
	case <-ctx.Done():
		// Clean up the pending request.
		pb.mu.Lock()
		delete(pb.pending, req.ID)
		pb.mu.Unlock()
		return PermissionResponse{}, fmt.Errorf("%w: %v", ErrPermissionTimeout, ctx.Err())
	}
}

// GrantPermission approves a pending permission request.
// Called by the leader (or coordinator) after user approval.
func (pb *PermissionBridge) GrantPermission(requestID string, updatedInput map[string]any, rules []types.PermissionRule, feedback string) error {
	return pb.resolve(requestID, PermissionResponse{
		RequestID:    requestID,
		Decision:     PermStatusApproved,
		UpdatedInput: updatedInput,
		Rules:        rules,
		Feedback:     feedback,
	})
}

// DenyPermission rejects a pending permission request.
func (pb *PermissionBridge) DenyPermission(requestID string, feedback string) error {
	return pb.resolve(requestID, PermissionResponse{
		RequestID: requestID,
		Decision:  PermStatusRejected,
		Feedback:  feedback,
	})
}

// resolve sends a response to the worker waiting on the given request.
func (pb *PermissionBridge) resolve(requestID string, resp PermissionResponse) error {
	pb.mu.Lock()
	pr, ok := pb.pending[requestID]
	if ok {
		delete(pb.pending, requestID)
	}
	pb.mu.Unlock()

	if !ok {
		return fmt.Errorf("permission request %s not found (may have timed out)", requestID)
	}

	now := time.Now()
	pr.req.ResolvedAt = &now
	pr.req.Status = resp.Decision

	// Non-blocking send — channel has capacity 1.
	select {
	case pr.respCh <- resp:
	default:
	}

	pb.logger.Debug("permission resolved",
		"request_id", requestID,
		"decision", resp.Decision,
		"worker", pr.req.WorkerName,
		"tool", pr.req.ToolName,
	)

	// If rules were provided, add them to auto-forward for future requests.
	if len(resp.Rules) > 0 {
		pb.addAutoForwardFromRules(resp.Rules)
	}

	return nil
}

// PendingRequests returns a snapshot of all unresolved requests, oldest first.
func (pb *PermissionBridge) PendingRequests() []*PermissionRequest {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	reqs := make([]*PermissionRequest, 0, len(pb.pending))
	for _, pr := range pb.pending {
		reqs = append(reqs, pr.req)
	}
	// Sort by creation time.
	for i := 1; i < len(reqs); i++ {
		for j := i; j > 0 && reqs[j].CreatedAt.Before(reqs[j-1].CreatedAt); j-- {
			reqs[j], reqs[j-1] = reqs[j-1], reqs[j]
		}
	}
	return reqs
}

// WaitForRequest blocks until a new permission request arrives or ctx is cancelled.
// Returns immediately if there are already pending requests.
func (pb *PermissionBridge) WaitForRequest(ctx context.Context) error {
	pb.mu.Lock()
	hasPending := len(pb.pending) > 0
	pb.mu.Unlock()
	if hasPending {
		return nil
	}

	select {
	case <-pb.notifyCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// AddAutoForwardRule adds a rule that auto-approves matching requests.
func (pb *PermissionBridge) AddAutoForwardRule(rule AutoForwardRule) {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.autoRules = append(pb.autoRules, rule)
	pb.logger.Debug("auto-forward rule added",
		"tool", rule.ToolName,
		"pattern", rule.Pattern,
	)
}

// ClearAutoForwardRules removes all auto-forward rules.
func (pb *PermissionBridge) ClearAutoForwardRules() {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.autoRules = nil
}

// Close rejects all pending requests and prevents new submissions.
func (pb *PermissionBridge) Close() {
	if !atomic.CompareAndSwapInt32(&pb.closed, 0, 1) {
		return
	}

	pb.mu.Lock()
	pending := pb.pending
	pb.pending = make(map[string]*pendingReq)
	pb.mu.Unlock()

	for _, pr := range pending {
		select {
		case pr.respCh <- PermissionResponse{
			RequestID: pr.req.ID,
			Decision:  PermStatusRejected,
			Feedback:  "permission bridge closed",
		}:
		default:
		}
	}
}

// matchesAutoForward checks if a request matches any auto-forward rule.
func (pb *PermissionBridge) matchesAutoForward(req *PermissionRequest) bool {
	pb.mu.Lock()
	rules := pb.autoRules
	pb.mu.Unlock()

	for _, rule := range rules {
		if rule.ToolName == req.ToolName {
			if rule.Pattern == "" || rule.Pattern == "*" {
				return true
			}
			// Simple prefix match for path patterns like "/path/**".
			if len(rule.Pattern) > 3 && rule.Pattern[len(rule.Pattern)-3:] == "/**" {
				prefix := rule.Pattern[:len(rule.Pattern)-3]
				if pathInput, ok := req.Input["file_path"].(string); ok {
					if len(pathInput) >= len(prefix) && pathInput[:len(prefix)] == prefix {
						return true
					}
				}
				if pathInput, ok := req.Input["path"].(string); ok {
					if len(pathInput) >= len(prefix) && pathInput[:len(prefix)] == prefix {
						return true
					}
				}
			}
		}
	}
	return false
}

// addAutoForwardFromRules converts permission rules to auto-forward rules.
func (pb *PermissionBridge) addAutoForwardFromRules(rules []types.PermissionRule) {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	for _, rule := range rules {
		if rule.Behavior == types.PermissionAllow {
			pb.autoRules = append(pb.autoRules, AutoForwardRule{
				ToolName: rule.ToolName,
				Pattern:  rule.RuleContent,
			})
		}
	}
}

// --- PermissionSync helpers ---

// SyncPermissionDecision propagates a permission decision to all workers
// in the team. When the leader approves a pattern, all workers benefit.
func SyncPermissionDecision(team *Team, toolName, pattern string) {
	if team == nil || team.perms == nil {
		return
	}
	team.perms.AddAutoForwardRule(AutoForwardRule{
		ToolName: toolName,
		Pattern:  pattern,
	})
}

// CreatePermissionRequest builds a new PermissionRequest with a generated ID.
func CreatePermissionRequest(workerID, workerName, workerColor, teamName, toolName, toolUseID, description string, input map[string]any) *PermissionRequest {
	return &PermissionRequest{
		ID:          GeneratePermissionRequestID(),
		WorkerID:    workerID,
		WorkerName:  workerName,
		WorkerColor: workerColor,
		TeamName:    teamName,
		ToolName:    toolName,
		ToolUseID:   toolUseID,
		Description: description,
		Input:       input,
		Status:      PermStatusPending,
		CreatedAt:   time.Now(),
	}
}
