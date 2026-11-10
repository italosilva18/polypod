package remote

import "context"

// InboundContext identifies the remote message whose agent turn is running.
// It stays in process memory and is never copied into the model prompt.
type InboundContext struct {
	MessageID     string
	SessionID     string
	ParticipantID string
}

type inboundContextKey struct{}

func WithInboundContext(ctx context.Context, source InboundContext) context.Context {
	return context.WithValue(ctx, inboundContextKey{}, source)
}

func InboundFromContext(ctx context.Context) (InboundContext, bool) {
	if ctx == nil {
		return InboundContext{}, false
	}
	source, ok := ctx.Value(inboundContextKey{}).(InboundContext)
	return source, ok && source.MessageID != "" && validSessionID(source.SessionID) && publicParticipant(source.ParticipantID)
}
