package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// AppendMessagesAtRevision adds messages without reading or rewriting older
// pages. The caller supplies the header it loaded earlier, including Revision
// and MessageCount. A stale header is rejected instead of overwriting another
// process's work. Existing metadata can be changed in the supplied header.
// Replacing, rewinding, or forking a transcript still requires the full Save
// path; this method only appends.
func (fs *FileSystemStorage) AppendMessagesAtRevision(header SessionHeader, added []Message) (*SessionHeader, error) {
	if !validSessionID(header.ID) || header.MessageCount < 0 || len(added) == 0 {
		return nil, fmt.Errorf("invalid session append")
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if err := os.MkdirAll(fs.dir, 0700); err != nil {
		return nil, fmt.Errorf("creating sessions dir: %w", err)
	}
	if err := inheritSessionDirOwner(fs.dir); err != nil {
		return nil, fmt.Errorf("setting sessions dir owner: %w", err)
	}
	var result *SessionHeader
	err := withSessionLease(fs.dir, func() error {
		var err error
		result, err = fs.appendMessagesLocked(header, added)
		return err
	})
	return result, err
}

func (fs *FileSystemStorage) appendMessagesLocked(header SessionHeader, added []Message) (*SessionHeader, error) {
	previous, legacy, revision, err := fs.readDocumentWithRevision(header.ID)
	if err != nil {
		return nil, err
	}
	if revision != header.Revision {
		return nil, fmt.Errorf("%w: %s", ErrSessionConflict, header.ID)
	}
	if legacy != nil {
		if len(legacy.Messages) != header.MessageCount {
			return nil, fmt.Errorf("%w: message count changed in %s", ErrSessionConflict, header.ID)
		}
		legacy.Messages = append(legacy.Messages, added...)
		legacy.Title, legacy.Metadata = header.Title, header.Metadata
		legacy.UpdatedAt = appendUpdatedAt(header.UpdatedAt)
		if legacy.Title == "" {
			legacy.Title = TitleFromFirstMessage(legacy.Messages)
		}
		if err := fs.saveLocked(legacy); err != nil {
			return nil, err
		}
		result := headerFromLegacy(legacy)
		result.Revision = legacy.Revision
		result.PrefixHash = MessagePrefixHash(legacy.Messages)
		return result, nil
	}
	if previous == nil && header.MessageCount != 0 || previous != nil && previous.MessageCount != header.MessageCount {
		return nil, fmt.Errorf("%w: message count changed in %s", ErrSessionConflict, header.ID)
	}
	if err := os.MkdirAll(fs.pageDir(header.ID), 0700); err != nil {
		return nil, fmt.Errorf("creating session pages dir: %w", err)
	}
	if err := inheritSessionDirOwner(fs.pageDir(header.ID)); err != nil {
		return nil, fmt.Errorf("setting session pages dir owner: %w", err)
	}
	var next sessionManifest
	if previous != nil {
		next = *previous
		next.Pages = append([]messagePageRef(nil), previous.Pages...)
	} else {
		next = sessionManifest{Format: pagedFormat, ID: header.ID, CreatedAt: header.CreatedAt}
		if next.CreatedAt.IsZero() {
			next.CreatedAt = time.Now()
		}
	}
	next.Title, next.Metadata = header.Title, header.Metadata
	next.UpdatedAt = appendUpdatedAt(header.UpdatedAt)
	next.Version = CurrentSessionVersion
	if next.Title == "" && next.MessageCount == 0 {
		next.Title = TitleFromFirstMessage(added)
	}
	if next.MessageCount == 0 {
		next.Preview = previewContent(added[0].Content)
	}
	remaining := added
	if len(next.Pages) > 0 && next.Pages[len(next.Pages)-1].Count < messagePageSize {
		lastIndex := len(next.Pages) - 1
		old, err := fs.readPage(header.ID, next.Pages[lastIndex])
		if err != nil {
			return nil, err
		}
		fill := min(messagePageSize-len(old), len(remaining))
		combined := append(old, remaining[:fill]...)
		ref, err := fs.writeAppendPage(header.ID, combined)
		if err != nil {
			return nil, err
		}
		next.Pages[lastIndex] = ref
		remaining = remaining[fill:]
	}
	for len(remaining) > 0 {
		count := min(messagePageSize, len(remaining))
		ref, err := fs.writeAppendPage(header.ID, remaining[:count])
		if err != nil {
			return nil, err
		}
		next.Pages = append(next.Pages, ref)
		remaining = remaining[count:]
	}
	next.MessageCount += len(added)
	if previous == nil || previous.MessageCount == 0 {
		next.PrefixHash = MessagePrefixHash(added)
	} else if previous.PrefixHash != "" {
		next.PrefixHash = extendMessagePrefixHash(previous.PrefixHash, added)
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling session manifest: %w", err)
	}
	if err := atomicWriteFile(fs.sessionPath(header.ID), data); err != nil {
		return nil, fmt.Errorf("writing session manifest: %w", err)
	}
	fs.retireOldPages(header.ID, previous, next.Pages)
	fs.pruneUnreferencedPages(header.ID, next.Pages)
	result := next.header()
	result.Revision = documentRevision(data)
	return result, nil
}

func appendUpdatedAt(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now()
	}
	return value
}

func (fs *FileSystemStorage) writeAppendPage(id string, messages []Message) (messagePageRef, error) {
	data, err := json.Marshal(messages)
	if err != nil {
		return messagePageRef{}, fmt.Errorf("marshaling session page: %w", err)
	}
	digest := sha256.Sum256(data)
	name := hex.EncodeToString(digest[:]) + ".page"
	path := filepath.Join(fs.pageDir(id), name)
	prior, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return messagePageRef{}, fmt.Errorf("checking existing session page: %w", err)
	}
	if os.IsNotExist(err) || !bytes.Equal(prior, data) {
		if err := atomicWriteFile(path, data); err != nil {
			return messagePageRef{}, fmt.Errorf("writing session page: %w", err)
		}
	}
	return messagePageRef{File: name, Count: len(messages)}, nil
}
