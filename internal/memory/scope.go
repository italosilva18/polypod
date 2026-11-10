package memory

import "context"

type sessionScopeKey struct{}
type legacyEpisodeScopeKey struct{}
type globalMemoryAccessKey struct{}

// WithGlobalMemoryAccess grants a trusted owner turn access to explicit
// unscoped facts. Hosts must derive this from authenticated channel identity,
// never from model arguments or message text.
func WithGlobalMemoryAccess(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, globalMemoryAccessKey{}, true)
}

// WithSessionScope allows a conversation to retrieve its own derived memories.
// An empty scope grants no access to scoped facts or episodes. Explicit
// unscoped facts require owner access when a remote session scope is present.
func WithSessionScope(ctx context.Context, sessionID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionScopeKey{}, sessionID)
}

// WithLegacyEpisodeScope preserves previously saved CLI episodes under the
// channel/user ID while new memories use the active CLI branch ID. It does not
// grant access to scoped facts from another branch.
func WithLegacyEpisodeScope(ctx context.Context, sessionID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, legacyEpisodeScopeKey{}, sessionID)
}

func sessionScope(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	sessionID, _ := ctx.Value(sessionScopeKey{}).(string)
	return sessionID
}

func memoryVisible(ctx context.Context, m Memory) bool {
	if m.SessionID == "" {
		// Explicit facts remain global. Older automated facts lack a reliable
		// owner, so retain them for review without injecting them into a chat.
		if m.Source == "dream_episode" || m.Source == "dream" || m.Source == "auto_extracted" {
			return false
		}
		if sessionScope(ctx) != "" && (ctx == nil || ctx.Value(globalMemoryAccessKey{}) != true) {
			return false
		}
		return true
	}
	scope := sessionScope(ctx)
	if scope != "" && m.SessionID == scope {
		return true
	}
	if m.Source == "dream_episode" && ctx != nil {
		legacy, _ := ctx.Value(legacyEpisodeScopeKey{}).(string)
		return legacy != "" && m.SessionID == legacy
	}
	return false
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
