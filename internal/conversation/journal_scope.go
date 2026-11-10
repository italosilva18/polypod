package conversation

import (
	"context"
	"errors"
)

var ErrJournalScopeMismatch = errors.New("journal scope does not match the conversation")

// JournalScope identifies the conversation and active branch whose original
// turns may be recalled by a model tool. Callers derive it from trusted request
// metadata, never from tool arguments.
type JournalScope struct {
	SessionID string
	Channel   string
	BranchID  string
	AgentName string
}

type journalScopeKey struct{}

// WithJournalScope binds journal writes and recall to one request. CLI branches
// use their persisted branch ID; other channels use the conversation ID.
func WithJournalScope(ctx context.Context, channel, userID, branchID, agentName string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if channel == "" || userID == "" {
		return ctx
	}
	scope := JournalScope{SessionID: channel + ":" + userID, Channel: channel,
		BranchID: branchID, AgentName: agentName}
	if scope.BranchID == "" {
		scope.BranchID = scope.SessionID
	}
	if scope.AgentName == "" {
		scope.AgentName = "default"
	}
	return context.WithValue(ctx, journalScopeKey{}, scope)
}

// JournalScopeFromContext returns only a scope attached by the trusted request
// path. Tools must not accept a requested session or branch as input.
func JournalScopeFromContext(ctx context.Context) (JournalScope, bool) {
	if ctx == nil {
		return JournalScope{}, false
	}
	scope, ok := ctx.Value(journalScopeKey{}).(JournalScope)
	return scope, ok && scope.SessionID != "" && scope.Channel != "" && scope.BranchID != ""
}

func journalScopeForEntry(ctx context.Context, sess *Session) (JournalScope, error) {
	scope, ok := JournalScopeFromContext(ctx)
	if !ok {
		return JournalScope{}, nil // legacy or maintenance write
	}
	if scope.SessionID != sess.ID || scope.Channel != sess.Channel {
		return JournalScope{}, ErrJournalScopeMismatch
	}
	return scope, nil
}
