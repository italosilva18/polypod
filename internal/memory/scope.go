package memory

import (
	"context"
	"strings"
)

type sessionScopeKey struct{}
type legacyEpisodeScopeKey struct{}
type globalMemoryAccessKey struct{}
type ownerLinkedMemoryKey struct{}

type ownerLinkedMemory struct {
	viewerChannel string
	whatsAppChat  string
}

// WithOwnerLinkedMemory makes facts from the authenticated owner's local CLI
// and WhatsApp chat readable across those channels. The host must verify the
// owner identity before calling it; a tool argument must never set this link.
func WithOwnerLinkedMemory(ctx context.Context, viewerChannel, whatsAppChat string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(whatsAppChat) == "" {
		return ctx
	}
	switch viewerChannel {
	case "cli", "whatsapp", "system":
	default:
		return ctx
	}
	return context.WithValue(ctx, ownerLinkedMemoryKey{}, ownerLinkedMemory{
		viewerChannel: viewerChannel, whatsAppChat: whatsAppChat,
	})
}

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

func memoryVisibilityKey(ctx context.Context) string {
	key := sessionScope(ctx)
	if ctx == nil {
		return key
	}
	if ctx.Value(globalMemoryAccessKey{}) == true {
		key += "\x00global-owner"
	}
	if linked, ok := ctx.Value(ownerLinkedMemoryKey{}).(ownerLinkedMemory); ok {
		key += "\x00linked:" + linked.viewerChannel + ":" + linked.whatsAppChat
	}
	if legacy, ok := ctx.Value(legacyEpisodeScopeKey{}).(string); ok {
		key += "\x00legacy:" + legacy
	}
	return key
}

func ownerLinkedVisible(ctx context.Context, m Memory) bool {
	if ctx == nil || m.SessionID == "" {
		return false
	}
	linked, ok := ctx.Value(ownerLinkedMemoryKey{}).(ownerLinkedMemory)
	if !ok || linked.whatsAppChat == "" {
		return false
	}
	// Episodic requests are low-confidence conversation records, not shared
	// facts. Older Dream records need an exact user quote before crossing
	// channels; their original SessionID remains unchanged.
	if m.Source != "user" && !(m.Source == "dream" && hasTag(m, "evidence:quote_verified")) {
		return false
	}
	ownerWhatsApp := "whatsapp:" + linked.whatsAppChat
	switch linked.viewerChannel {
	case "cli":
		return m.SessionID == ownerWhatsApp || m.SessionID == "system:owner"
	case "whatsapp", "system":
		return m.SessionID == "cli:local" || strings.HasPrefix(m.SessionID, "cli-branch:") ||
			m.SessionID == "system:owner" || m.SessionID == ownerWhatsApp
	default:
		return false
	}
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
		if legacy != "" && m.SessionID == legacy {
			return true
		}
	}
	return ownerLinkedVisible(ctx, m)
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
