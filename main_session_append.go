package main

import (
	"github.com/costa/polypod/internal/session"
)

type pagedShutdownStorage interface {
	LoadHeader(string) (*session.SessionHeader, error)
	AppendMessagesAtRevision(session.SessionHeader, []session.Message) (*session.SessionHeader, error)
}

// saveShutdownSessionSnapshot appends only when the persisted transcript is an
// exact prefix of the active CLI conversation. A changed prefix indicates a
// compact, rewind, restore, or branch operation, so Save replaces it in full.
func saveShutdownSessionSnapshot(storage session.SessionStorage, paged pagedShutdownStorage,
	previous *session.SessionHeader, snapshot *session.RestoredSession) error {
	if paged == nil || snapshot == nil {
		return storage.Save(snapshot)
	}
	var header session.SessionHeader
	if previous == nil {
		if snapshot.Revision != "" {
			return storage.Save(snapshot)
		}
		header = session.SessionHeader{ID: snapshot.ID, CreatedAt: snapshot.CreatedAt}
	} else {
		if previous.Revision != snapshot.Revision || previous.MessageCount >= len(snapshot.Messages) ||
			previous.MessageCount > 0 && previous.PrefixHash == "" {
			return storage.Save(snapshot)
		}
		if previous.MessageCount > 0 &&
			session.MessagePrefixHash(snapshot.Messages[:previous.MessageCount]) != previous.PrefixHash {
			return storage.Save(snapshot)
		}
		header = *previous
	}
	if len(snapshot.Messages) == 0 {
		return storage.Save(snapshot)
	}
	header.Title = snapshot.Title
	header.Metadata = snapshot.Metadata
	header.UpdatedAt = snapshot.UpdatedAt
	updated, err := paged.AppendMessagesAtRevision(header, snapshot.Messages[header.MessageCount:])
	if err != nil {
		return err
	}
	snapshot.Revision = updated.Revision
	return nil
}
