// Package hooks — structured JSON hook response parsing.
//
// This file adds support for the structured JSON output contract used by
// Claude Code (see src/types/hooks.ts, syncHookResponseSchema). The Go port's
// original contract was exit-code driven (0 = ok, 2 = blocking, N = non-blocking
// error) with plain-text stdout. The structured contract layers on top: a hook
// may optionally emit a JSON object to stdout whose fields can mutate tool
// input, tool output, permission decisions, and conversation flow.
//
// Parsing is opt-in: callers should first try ParseStructuredResponse and,
// on error, fall back to their existing exit-code interpretation. Existing
// hook execution (see executors.go) is unchanged — this file is additive.
package hooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// StructuredHookResponse is the Go mirror of the TS `syncHookResponseSchema`.
// All fields are optional; pointer types are used where the zero value is
// semantically meaningful ("unset" vs "false").
//
// Field mapping to the TS schema:
//
//	Continue           → continue          (nil = default true)
//	SuppressOutput     → suppressOutput
//	StopReason         → stopReason        (shown when Continue = false)
//	Decision           → decision          ("approve"|"block" in TS; the Go
//	                                        port also accepts "allow"|"deny"|"ask"
//	                                        since those flow through the same
//	                                        block/allow plumbing)
//	SystemMessage      → systemMessage     (injected as system context)
//	HookSpecificOutput → hookSpecificOutput (discriminated by HookEventName)
type StructuredHookResponse struct {
	Continue           *bool              `json:"continue,omitempty"`
	SuppressOutput     *bool              `json:"suppressOutput,omitempty"`
	StopReason         string             `json:"stopReason,omitempty"`
	Decision           string             `json:"decision,omitempty"`
	Reason             string             `json:"reason,omitempty"`
	SystemMessage      string             `json:"systemMessage,omitempty"`
	HookSpecificOutput HookSpecificOutput `json:"hookSpecificOutput,omitempty"`
}

// HookSpecificOutput is the discriminated union of event-specific response
// fields. The union is flattened into a single struct because Go lacks proper
// sum types and because callers typically already know which event they are
// handling. Unused fields for a given event are simply left as their zero value.
//
// Opaque payloads (UpdatedInput, UpdatedMCPToolOutput) are kept as
// json.RawMessage so that inputmod.go can apply shallow-merge semantics without
// this file needing to know anything about tool-input schemas.
type HookSpecificOutput struct {
	// HookEventName discriminates the union. One of the hooks.Event* values.
	HookEventName string `json:"hookEventName,omitempty"`

	// PreToolUse / PermissionRequest fields
	PermissionDecision       string          `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string          `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             json.RawMessage `json:"updatedInput,omitempty"`

	// PostToolUse fields
	UpdatedMCPToolOutput json.RawMessage `json:"updatedMCPToolOutput,omitempty"`

	// UserPromptSubmit / SessionStart / Setup / SubagentStart / Notification /
	// PostToolUse / PostToolUseFailure / PermissionDenied
	AdditionalContext string `json:"additionalContext,omitempty"`

	// SessionStart
	InitialUserMessage string `json:"initialUserMessage,omitempty"`

	// PermissionDenied
	Retry *bool `json:"retry,omitempty"`

	// CwdChanged / FileChanged / SessionStart
	WatchPaths []string `json:"watchPaths,omitempty"`

	// WorktreeCreate
	WorktreePath string `json:"worktreePath,omitempty"`

	// Elicitation / ElicitationResult
	Action  string          `json:"action,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`

	// PermissionRequest: nested decision. Kept raw because the TS schema uses
	// a discriminated sub-union ({behavior:"allow",...} | {behavior:"deny",...}).
	Decision json.RawMessage `json:"decision,omitempty"`
}

// ErrNotStructured is returned by ParseStructuredResponse when stdout does not
// contain a JSON object (empty, plain text, array, scalar, etc.). Callers
// should treat this as a signal to fall back to exit-code interpretation.
var ErrNotStructured = errors.New("hooks: stdout is not a structured JSON response")

// ParseStructuredResponse scans hook stdout for a JSON object matching the
// structured response schema and returns it. Behaviour:
//
//  1. If stdout is empty or whitespace-only, returns (nil, ErrNotStructured).
//  2. If the first non-empty line starting with '{' parses as a JSON object,
//     it is decoded into StructuredHookResponse and returned.
//  3. If no such line exists, returns (nil, ErrNotStructured).
//  4. If a candidate line exists but fails to decode, returns (nil, decodeErr).
//
// Rationale: hooks frequently emit log lines before the JSON result, so we
// scan line-by-line rather than requiring the entire stdout to parse.
func ParseStructuredResponse(stdout []byte) (*StructuredHookResponse, error) {
	trimmed := strings.TrimSpace(string(stdout))
	if trimmed == "" {
		return nil, ErrNotStructured
	}

	// Fast path: the whole stdout is a JSON object.
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		var resp StructuredHookResponse
		dec := json.NewDecoder(strings.NewReader(trimmed))
		dec.UseNumber()
		if err := dec.Decode(&resp); err == nil {
			return &resp, nil
		}
		// fall through to line-by-line scan
	}

	// Line-by-line scan: find the first line that is a valid JSON object.
	var lastErr error
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var resp StructuredHookResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			lastErr = err
			continue
		}
		return &resp, nil
	}

	if lastErr != nil {
		return nil, fmt.Errorf("hooks: parse structured response: %w", lastErr)
	}
	return nil, ErrNotStructured
}

// ShouldBlock reports whether this structured response represents a
// blocking/deny decision. It mirrors the logic in executors.parseJSONResponse:
//
//   - Decision == "block" or "deny" → block
//   - HookSpecificOutput.PermissionDecision == "deny" → block
//   - Continue == false → block (caller should also honour StopReason)
//
// "ask" and "allow" are NOT treated as blocking.
func (r *StructuredHookResponse) ShouldBlock() bool {
	if r == nil {
		return false
	}
	switch strings.ToLower(r.Decision) {
	case "block", "deny":
		return true
	}
	if strings.EqualFold(r.HookSpecificOutput.PermissionDecision, "deny") {
		return true
	}
	if r.Continue != nil && !*r.Continue {
		return true
	}
	return false
}

// PermissionBehavior returns the normalized permission behavior from the
// response, if any. Returns one of "allow", "deny", "ask", or "" when unset.
// Prefers HookSpecificOutput.PermissionDecision over top-level Decision.
func (r *StructuredHookResponse) PermissionBehavior() string {
	if r == nil {
		return ""
	}
	if r.HookSpecificOutput.PermissionDecision != "" {
		return strings.ToLower(r.HookSpecificOutput.PermissionDecision)
	}
	switch strings.ToLower(r.Decision) {
	case "allow", "approve":
		return "allow"
	case "deny", "block":
		return "deny"
	case "ask":
		return "ask"
	}
	return ""
}
