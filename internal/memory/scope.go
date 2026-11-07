package memory

import "context"

type sessionScopeKey struct{}

// WithSessionScope allows a conversation to retrieve its own episodic memories.
// Ordinary facts remain visible as before. An empty scope grants no access to
// episodes, so background jobs and context-free tools cannot expose them.
func WithSessionScope(ctx context.Context, sessionID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionScopeKey{}, sessionID)
}

func memoryVisible(ctx context.Context, m Memory) bool {
	if m.Source != "dream_episode" {
		return true
	}
	if ctx == nil {
		return false
	}
	sessionID, ok := ctx.Value(sessionScopeKey{}).(string)
	return ok && sessionID != "" && m.SessionID == sessionID
}

func visibleOnly(ctx context.Context, memories []Memory) []Memory {
	visible := make([]Memory, 0, len(memories))
	for _, m := range memories {
		if memoryVisible(ctx, m) {
			visible = append(visible, m)
		}
	}
	return visible
}
