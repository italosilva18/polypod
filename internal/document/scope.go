package document

import "context"

type remoteScopeKey struct{}

// WithRemoteScope marks a turn from a remote inbox. The scope comes from the
// inbox identity before the AI request is built and is never placed in a prompt.
func WithRemoteScope(ctx context.Context, channel, userID string) context.Context {
	if ctx == nil || channel == "" || userID == "" {
		return ctx
	}
	return context.WithValue(ctx, remoteScopeKey{}, channel+":"+userID)
}

// RemoteScopeFromContext returns the exact scope attached to a remote turn.
// A caller must still compare it with the current request identity.
func RemoteScopeFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	scope, ok := ctx.Value(remoteScopeKey{}).(string)
	return scope, ok && scope != ""
}
