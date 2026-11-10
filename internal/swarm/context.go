package swarm

import "context"

type teammateIdentityKey struct{}

// WithTeammateIdentity marks tool calls made by a running team worker.
func WithTeammateIdentity(ctx context.Context, identity TeammateIdentity) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, teammateIdentityKey{}, identity)
}

// TeammateIdentityFromContext returns the team identity attached to a worker turn.
func TeammateIdentityFromContext(ctx context.Context) (TeammateIdentity, bool) {
	if ctx == nil {
		return TeammateIdentity{}, false
	}
	identity, ok := ctx.Value(teammateIdentityKey{}).(TeammateIdentity)
	return identity, ok
}
