package cli

import (
	"fmt"
	"time"

	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/session"
)

type pagedSessionAppender interface {
	LoadHeader(string) (*session.SessionHeader, error)
	AppendMessagesAtRevision(session.SessionHeader, []session.Message) (*session.SessionHeader, error)
}

// appendActiveSessionIfUnchanged skips loading the external snapshot only if
// the full persisted prefix still matches the active conversation. An auto
// compacted or rewound session falls through to the full replacement path.
func (a *Adapter) appendActiveSessionIfUnchanged(sess *conversation.Session) (bool, error) {
	storage, ok := a.sessStorage.(pagedSessionAppender)
	if !ok || sess == nil {
		return false, nil
	}
	header, err := storage.LoadHeader(a.activeSessionID)
	if err != nil {
		return true, err
	}
	if header == nil {
		if a.activeSessionRevision != "" {
			return true, fmt.Errorf("%w: sessão %s desapareceu", session.ErrSessionConflict, a.activeSessionID)
		}
		header = &session.SessionHeader{
			ID: a.activeSessionID, CreatedAt: sess.CreatedAt,
			Metadata: session.SessionMetadata{Tag: "cli", Mode: "interactive"},
		}
	} else if header.Revision != a.activeSessionRevision {
		return true, fmt.Errorf("%w: %s", session.ErrSessionConflict, a.activeSessionID)
	}
	if len(sess.Messages) <= header.MessageCount || header.MessageCount > 0 && header.PrefixHash == "" {
		return false, nil
	}
	// Converting the active messages is bounded by the already loaded CLI
	// conversation. The external pages remain unread, and only the new suffix
	// is serialized to disk when the prefix digest matches.
	converted := make([]session.Message, len(sess.Messages))
	for i, message := range sess.Messages {
		converted[i] = session.Message{
			ID: message.ID, Role: string(message.Role), Content: message.Content, Timestamp: message.Timestamp,
		}
	}
	if header.MessageCount > 0 && session.MessagePrefixHash(converted[:header.MessageCount]) != header.PrefixHash {
		return false, nil
	}
	header.Metadata = a.currentSessionMetadata(header.Metadata)
	header.Title = session.TitleFromFirstMessage(converted)
	header.UpdatedAt = time.Now()
	updated, err := storage.AppendMessagesAtRevision(*header, converted[header.MessageCount:])
	if err != nil {
		return true, err
	}
	a.activeSessionRevision = updated.Revision
	return true, nil
}
