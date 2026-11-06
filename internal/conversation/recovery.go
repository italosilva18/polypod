package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// RecoveryStatus describes the outcome of a recovery attempt.
type RecoveryStatus string

const (
	RecoveryHealthy       RecoveryStatus = "healthy"
	RecoveryRepaired      RecoveryStatus = "repaired"
	RecoveryUnrecoverable RecoveryStatus = "unrecoverable"
)

// RecoveryReport summarizes the state of a session after it has been
// inspected (and potentially repaired) by the recovery routines.
type RecoveryReport struct {
	SessionID      string
	Status         RecoveryStatus
	IssuesFound    []string
	IssuesFixed    []string
	MessagesBefore int
	MessagesAfter  int
}

// SessionStore is the minimal interface the recovery helpers need from the
// persistence layer. The conversation.Store in this package satisfies it via
// the small adapter StoreAdapter below, and callers with a different backing
// store can supply their own implementation.
type SessionStore interface {
	// GetSession returns the session identified by sessionID. It must return
	// a non-nil error (not an empty session) if the session does not exist
	// or cannot be decoded.
	GetSession(ctx context.Context, sessionID string) (*Session, error)

	// SaveSession persists the given session.
	SaveSession(ctx context.Context, sess *Session) error

	// ListSessionIDs returns the identifiers of all known sessions. Used by
	// ScanAllSessions.
	ListSessionIDs(ctx context.Context) ([]string, error)
}

// StoreAdapter bridges the existing conversation.Store (which is keyed by
// channel + userID, not a single session ID) to the SessionStore interface
// used by the recovery helpers. Session IDs are expected to be in the
// "channel:userID" form produced by sessionKey.
type StoreAdapter struct {
	Store *Store
}

// GetSession loads a session by ID (channel:userID).
func (a *StoreAdapter) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	channel, userID, ok := splitSessionID(sessionID)
	if !ok {
		return nil, fmt.Errorf("invalid session id %q: expected channel:userID", sessionID)
	}
	sess, err := a.Store.Get(ctx, channel, userID)
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// SaveSession persists a session.
func (a *StoreAdapter) SaveSession(ctx context.Context, sess *Session) error {
	return a.Store.Save(ctx, sess)
}

// ListSessionIDs returns IDs for every session currently loaded in the store.
func (a *StoreAdapter) ListSessionIDs(ctx context.Context) ([]string, error) {
	return a.Store.ListSessions(), nil
}

func splitSessionID(id string) (channel, userID string, ok bool) {
	idx := strings.Index(id, ":")
	if idx < 0 {
		return "", "", false
	}
	return id[:idx], id[idx+1:], true
}

// ── Validation ──────────────────────────────────────────────────────────────

// ValidateSession checks if a session's message list is internally
// consistent. It returns a list of human-readable issue descriptions.
// An empty slice means the session is healthy.
//
// The validator looks for:
//   - Dangling tool_use markers (assistant called a tool but no matching
//     tool_result appears afterwards).
//   - Orphaned tool_result markers (a tool result without a preceding
//     tool_use).
//   - Obviously-corrupt content (invalid UTF-8, or content that looks like
//     truncated JSON).
//   - Empty / unknown roles.
//   - Incomplete streaming: assistant message ending mid-word / without any
//     terminator after a user message.
func ValidateSession(messages []Message) []string {
	var issues []string

	if len(messages) == 0 {
		return nil
	}

	// Track unmatched tool_use IDs to detect dangling calls. We key by
	// position index since the conversation.Message type does not carry a
	// structured tool call list — we detect them by textual markers embedded
	// in Content (see hasToolUseMarker / hasToolResultMarker).
	unmatchedTools := 0

	for i, m := range messages {
		// Role sanity.
		if m.Role == "" {
			issues = append(issues, fmt.Sprintf("message %d: empty role", i))
			continue
		}
		if m.Role != RoleUser && m.Role != RoleAssistant && m.Role != RoleSystem {
			issues = append(issues, fmt.Sprintf("message %d: unknown role %q", i, m.Role))
		}

		// Content validity.
		if !utf8.ValidString(m.Content) {
			issues = append(issues, fmt.Sprintf("message %d: invalid UTF-8 in content", i))
		}
		if looksLikeTruncatedJSON(m.Content) {
			issues = append(issues, fmt.Sprintf("message %d: content looks like truncated JSON", i))
		}

		// Tool-call bookkeeping (heuristic).
		if m.Role == RoleAssistant && hasToolUseMarker(m.Content) {
			unmatchedTools++
		}
		if hasToolResultMarker(m.Content) {
			if unmatchedTools > 0 {
				unmatchedTools--
			} else {
				issues = append(issues, fmt.Sprintf("message %d: orphaned tool_result without preceding tool_use", i))
			}
		}
	}

	if unmatchedTools > 0 {
		issues = append(issues,
			fmt.Sprintf("%d dangling tool call(s): assistant invoked tool without matching tool_result", unmatchedTools))
	}

	// Incomplete streaming: last message is an assistant entry that looks
	// truncated (no terminal punctuation and no newline).
	last := messages[len(messages)-1]
	if last.Role == RoleAssistant && looksIncomplete(last.Content) {
		issues = append(issues, "last assistant message appears to be incomplete (interrupted streaming)")
	}

	return issues
}

// hasToolUseMarker detects textual markers that indicate a tool_use block was
// serialized into message content. Several formats are handled so the
// heuristic works against multiple providers.
func hasToolUseMarker(s string) bool {
	if s == "" {
		return false
	}
	markers := []string{
		"<tool_use",
		"[tool_use",
		`"type":"tool_use"`,
		`"type": "tool_use"`,
		"<function_calls>",
		`"tool_calls":`,
	}
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// hasToolResultMarker detects textual markers that indicate a tool_result
// block was serialized into message content.
func hasToolResultMarker(s string) bool {
	if s == "" {
		return false
	}
	markers := []string{
		"<tool_result",
		"[tool_result",
		`"type":"tool_result"`,
		`"type": "tool_result"`,
		"<function_results>",
		`"role":"tool"`,
	}
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// looksLikeTruncatedJSON returns true if the content looks like it began as
// JSON but was cut off mid-object. We only flag content that looks like a
// JSON payload (starts with { or [) and fails to parse — ordinary prose with
// unbalanced braces is not considered corrupt.
func looksLikeTruncatedJSON(s string) bool {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) < 2 {
		return false
	}
	if trimmed[0] != '{' && trimmed[0] != '[' {
		return false
	}
	var v any
	return json.Unmarshal([]byte(trimmed), &v) != nil
}

// looksIncomplete is a simple heuristic for detecting a message whose stream
// was cut off mid-sentence. It flags content that ends without standard
// terminal punctuation and without a trailing newline, provided the content
// is non-trivial in length.
func looksIncomplete(s string) bool {
	trimmed := strings.TrimRight(s, " \t")
	if len(trimmed) < 20 {
		return false
	}
	if strings.HasSuffix(trimmed, "\n") {
		return false
	}
	last := trimmed[len(trimmed)-1]
	switch last {
	case '.', '!', '?', ')', ']', '}', '"', '\'', '`', ':':
		return false
	}
	return true
}

// ── Repair ──────────────────────────────────────────────────────────────────

// RepairDanglingToolCalls removes assistant messages that contain a tool_use
// marker but have no matching tool_result afterwards. It returns the repaired
// message list and the number of messages that were removed.
//
// The algorithm is single-pass: it walks the list once, stripping assistant
// messages whose tool_use is never answered. If multiple consecutive tool
// calls are made without answers, every unmatched one is dropped.
func RepairDanglingToolCalls(messages []Message) ([]Message, int) {
	if len(messages) == 0 {
		return messages, 0
	}

	// Pre-scan: for each assistant tool_use, find whether any later message
	// contains a tool_result. This gives us a boolean "has answer" per index.
	hasAnswer := make([]bool, len(messages))
	for i, m := range messages {
		if m.Role != RoleAssistant || !hasToolUseMarker(m.Content) {
			continue
		}
		for j := i + 1; j < len(messages); j++ {
			if hasToolResultMarker(messages[j].Content) {
				hasAnswer[i] = true
				break
			}
		}
	}

	out := make([]Message, 0, len(messages))
	removed := 0
	for i, m := range messages {
		if m.Role == RoleAssistant && hasToolUseMarker(m.Content) && !hasAnswer[i] {
			removed++
			continue
		}
		out = append(out, m)
	}
	return out, removed
}

// RepairIncompleteStreaming removes the last message if it looks like an
// incomplete stream (assistant message cut off mid-sentence, or an empty
// assistant entry). It returns the repaired list and whether a message was
// removed.
func RepairIncompleteStreaming(messages []Message) ([]Message, bool) {
	if len(messages) == 0 {
		return messages, false
	}
	last := messages[len(messages)-1]
	if last.Role != RoleAssistant {
		return messages, false
	}
	if strings.TrimSpace(last.Content) == "" || looksIncomplete(last.Content) {
		return messages[:len(messages)-1], true
	}
	return messages, false
}

// ── High-level entry points ─────────────────────────────────────────────────

// RecoverSession attempts to repair a session from persistence. It loads the
// session via the store, runs all validation + repair passes, and (if any
// repairs were made) persists the corrected session back to the store.
//
// Returns a report describing what was found and fixed. If the session
// cannot be loaded at all, the report's Status will be Unrecoverable.
func RecoverSession(sessionID string, store SessionStore) (*RecoveryReport, error) {
	ctx := context.Background()
	report := &RecoveryReport{SessionID: sessionID}

	sess, err := store.GetSession(ctx, sessionID)
	if err != nil || sess == nil {
		report.Status = RecoveryUnrecoverable
		if err != nil {
			report.IssuesFound = append(report.IssuesFound,
				fmt.Sprintf("failed to load session: %v", err))
		} else {
			report.IssuesFound = append(report.IssuesFound, "session not found")
		}
		return report, err
	}

	report.MessagesBefore = len(sess.Messages)

	// Validate first — populates IssuesFound with diagnostic strings.
	report.IssuesFound = ValidateSession(sess.Messages)
	if len(report.IssuesFound) == 0 {
		report.Status = RecoveryHealthy
		report.MessagesAfter = len(sess.Messages)
		return report, nil
	}

	// Run repair passes.
	repaired := sess.Messages
	if fixed, n := RepairDanglingToolCalls(repaired); n > 0 {
		repaired = fixed
		report.IssuesFixed = append(report.IssuesFixed,
			fmt.Sprintf("removed %d dangling tool call message(s)", n))
	}
	if fixed, removed := RepairIncompleteStreaming(repaired); removed {
		repaired = fixed
		report.IssuesFixed = append(report.IssuesFixed,
			"removed trailing incomplete assistant message")
	}

	// Re-validate after repair. If there are still issues, mark as
	// unrecoverable so callers can decide what to do.
	remaining := ValidateSession(repaired)
	if len(remaining) == 0 {
		sess.Messages = repaired
		report.MessagesAfter = len(repaired)
		if len(report.IssuesFixed) > 0 {
			report.Status = RecoveryRepaired
			if err := store.SaveSession(ctx, sess); err != nil {
				report.IssuesFound = append(report.IssuesFound,
					fmt.Sprintf("failed to persist repaired session: %v", err))
				report.Status = RecoveryUnrecoverable
				return report, err
			}
		} else {
			// Issues were found but none of our repair passes applied.
			// Conservatively mark as unrecoverable so the user is aware.
			report.Status = RecoveryUnrecoverable
		}
		return report, nil
	}

	// Repair didn't fully fix the session.
	report.Status = RecoveryUnrecoverable
	report.MessagesAfter = len(repaired)
	for _, r := range remaining {
		report.IssuesFound = append(report.IssuesFound, "post-repair: "+r)
	}
	return report, nil
}

// ScanAllSessions walks every session in the store and produces a recovery
// report for each one. Sessions that are healthy are included with status
// Healthy, so the caller can decide whether to display or hide them.
//
// Errors from individual sessions are captured in their respective reports;
// the returned top-level error is only non-nil if the store itself fails to
// list sessions.
func ScanAllSessions(store SessionStore) ([]RecoveryReport, error) {
	ctx := context.Background()
	ids, err := store.ListSessionIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}

	reports := make([]RecoveryReport, 0, len(ids))
	for _, id := range ids {
		rep, _ := RecoverSession(id, store)
		if rep == nil {
			// Defensive: RecoverSession should always return a report.
			rep = &RecoveryReport{
				SessionID: id,
				Status:    RecoveryUnrecoverable,
				IssuesFound: []string{
					"recovery returned nil report",
				},
			}
		}
		reports = append(reports, *rep)
	}
	return reports, nil
}
