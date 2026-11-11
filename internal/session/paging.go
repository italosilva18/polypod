package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	messagePageSize = 64
	pagedFormat     = "paged-v1"
	pageRetention   = 24 * time.Hour
)

// SessionHeader contains the fields needed to list a session without loading
// its transcript. MessageCount is the number of persisted messages.
type SessionHeader struct {
	ID           string
	Title        string
	Metadata     SessionMetadata
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Version      int
	MessageCount int
	Preview      string
	Revision     string
	// PrefixHash is empty for old manifests; such sessions need one full Save
	// before a caller can prove that append leaves older messages unchanged.
	PrefixHash string
}

// SessionPage is a chronological window of messages. Start is the zero-based
// index of its first message; pass Start as before to fetch the next older page.
type SessionPage struct {
	Messages []Message
	Start    int
	Total    int
	HasMore  bool
	Revision string
}

type messagePageRef struct {
	File  string `json:"file"`
	Count int    `json:"count"`
}

// The JSON file remains the session's commit point. Page files are immutable
// and written first, so an interrupted save leaves the previous manifest valid.
type sessionManifest struct {
	Format       string           `json:"format"`
	ID           string           `json:"id"`
	Title        string           `json:"title"`
	Metadata     SessionMetadata  `json:"metadata"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
	Version      int              `json:"version"`
	MessageCount int              `json:"message_count"`
	Preview      string           `json:"preview,omitempty"`
	PrefixHash   string           `json:"prefix_hash,omitempty"`
	Pages        []messagePageRef `json:"pages"`
}

func (m *sessionManifest) header() *SessionHeader {
	return &SessionHeader{
		ID: m.ID, Title: m.Title, Metadata: m.Metadata,
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, Version: m.Version,
		MessageCount: m.MessageCount, Preview: m.Preview,
		PrefixHash: m.PrefixHash,
	}
}

func (m *sessionManifest) session() *RestoredSession {
	return &RestoredSession{
		ID: m.ID, Title: m.Title, Metadata: m.Metadata,
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, Version: m.Version,
	}
}

func headerFromLegacy(sess *RestoredSession) *SessionHeader {
	h := &SessionHeader{
		ID: sess.ID, Title: sess.Title, Metadata: sess.Metadata,
		CreatedAt: sess.CreatedAt, UpdatedAt: sess.UpdatedAt, Version: sess.Version,
		MessageCount: len(sess.Messages), Revision: sess.Revision,
	}
	if len(sess.Messages) > 0 {
		h.Preview = previewContent(sess.Messages[0].Content)
	}
	return h
}

func previewContent(content string) string {
	if len(content) > 100 {
		return content[:100] + "..."
	}
	return content
}

func validSessionID(id string) bool {
	return id != "" && id != "." && id != ".." && filepath.Base(id) == id &&
		!strings.ContainsAny(id, `/\`)
}

func (fs *FileSystemStorage) pageDir(id string) string {
	return filepath.Join(fs.dir, id+".pages")
}

func documentRevision(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (fs *FileSystemStorage) readDocument(id string) (*sessionManifest, *RestoredSession, error) {
	manifest, legacy, _, err := fs.readDocumentWithRevision(id)
	return manifest, legacy, err
}

func (fs *FileSystemStorage) readDocumentWithRevision(id string) (*sessionManifest, *RestoredSession, string, error) {
	if !validSessionID(id) {
		return nil, nil, "", fmt.Errorf("invalid session ID %q", id)
	}
	data, err := os.ReadFile(fs.sessionPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, "", nil
		}
		return nil, nil, "", fmt.Errorf("reading session %s: %w", id, err)
	}
	revision := documentRevision(data)
	var envelope struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, nil, "", fmt.Errorf("parsing session %s: %w", id, err)
	}
	if envelope.Format == "" {
		var legacy RestoredSession
		if err := json.Unmarshal(data, &legacy); err != nil {
			return nil, nil, "", fmt.Errorf("parsing session %s: %w", id, err)
		}
		if legacy.ID == "" {
			legacy.ID = id
		} else if legacy.ID != id {
			return nil, nil, "", fmt.Errorf("session ID mismatch in %s", id)
		}
		legacy.Revision = revision
		return nil, &legacy, revision, nil
	}
	if envelope.Format != pagedFormat {
		return nil, nil, "", fmt.Errorf("unsupported session format %q", envelope.Format)
	}
	var manifest sessionManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, nil, "", fmt.Errorf("parsing session manifest %s: %w", id, err)
	}
	if manifest.ID != id || manifest.MessageCount < 0 {
		return nil, nil, "", fmt.Errorf("invalid session manifest %s", id)
	}
	if manifest.PrefixHash != "" && !validPrefixHash(manifest.PrefixHash) {
		return nil, nil, "", fmt.Errorf("invalid prefix hash in session %s", id)
	}
	count := 0
	for _, ref := range manifest.Pages {
		if ref.Count <= 0 || ref.Count > messagePageSize || !validPageName(ref.File) {
			return nil, nil, "", fmt.Errorf("invalid page reference in session %s", id)
		}
		count += ref.Count
	}
	if count != manifest.MessageCount {
		return nil, nil, "", fmt.Errorf("message count mismatch in session %s", id)
	}
	return &manifest, nil, revision, nil
}

func validPageName(name string) bool {
	if len(name) != 64+len(".page") || !strings.HasSuffix(name, ".page") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimSuffix(name, ".page"))
	return err == nil
}

func (fs *FileSystemStorage) readPage(id string, ref messagePageRef) ([]Message, error) {
	if !validPageName(ref.File) {
		return nil, fmt.Errorf("invalid page filename in session %s", id)
	}
	data, err := os.ReadFile(filepath.Join(fs.pageDir(id), ref.File))
	if err != nil {
		return nil, fmt.Errorf("reading page %s for session %s: %w", ref.File, id, err)
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:])+".page" != ref.File {
		return nil, fmt.Errorf("page checksum mismatch in session %s", id)
	}
	var messages []Message
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, fmt.Errorf("parsing page for session %s: %w", id, err)
	}
	if len(messages) != ref.Count {
		return nil, fmt.Errorf("page message count mismatch in session %s", id)
	}
	return messages, nil
}

// LoadHeader reads only session metadata for paged sessions. Legacy JSON files
// are still accepted and are converted to the paged layout on their next save.
func (fs *FileSystemStorage) LoadHeader(id string) (*SessionHeader, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	manifest, legacy, revision, err := fs.readDocumentWithRevision(id)
	if err != nil || (manifest == nil && legacy == nil) {
		return nil, err
	}
	if legacy != nil {
		return headerFromLegacy(legacy), nil
	}
	header := manifest.header()
	header.Revision = revision
	return header, nil
}

// LoadPage loads at most limit messages immediately preceding before. A zero
// before value starts at the newest message. Pages are read only as needed.
func (fs *FileSystemStorage) LoadPage(id string, before, limit int) (*SessionPage, error) {
	return fs.LoadPageAtRevision(id, before, limit, "")
}

// LoadPageAtRevision refuses to mix pages from another saved version of the
// session. Pass the Revision returned by Load, LoadHeader, or an earlier page.
// An empty expected revision keeps LoadPage's historical unpinned behavior.
func (fs *FileSystemStorage) LoadPageAtRevision(id string, before, limit int, expectedRevision string) (*SessionPage, error) {
	if before < 0 || limit <= 0 {
		return nil, fmt.Errorf("invalid session page bounds")
	}
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	manifest, legacy, revision, err := fs.readDocumentWithRevision(id)
	if err != nil || (manifest == nil && legacy == nil) {
		return nil, err
	}
	if expectedRevision != "" && expectedRevision != revision {
		return nil, fmt.Errorf("%w: sessão %s mudou desde o carregamento", ErrSessionConflict, id)
	}
	if legacy != nil {
		page := pageFromMessages(legacy.Messages, before, limit)
		page.Revision = revision
		return page, nil
	}
	end := manifest.MessageCount
	if before > 0 && before < end {
		end = before
	}
	start := max(0, end-limit)
	result := &SessionPage{Start: start, Total: manifest.MessageCount, HasMore: start > 0, Revision: revision}
	position := 0
	for _, ref := range manifest.Pages {
		pageEnd := position + ref.Count
		if pageEnd > start && position < end {
			messages, err := fs.readPage(id, ref)
			if err != nil {
				return nil, err
			}
			result.Messages = append(result.Messages,
				messages[max(0, start-position):min(ref.Count, end-position)]...)
		}
		position = pageEnd
		if position >= end {
			break
		}
	}
	return result, nil
}

func pageFromMessages(messages []Message, before, limit int) *SessionPage {
	end := len(messages)
	if before > 0 && before < end {
		end = before
	}
	start := max(0, end-limit)
	return &SessionPage{
		Messages: append([]Message(nil), messages[start:end]...),
		Start:    start, Total: len(messages), HasMore: start > 0,
	}
}

func atomicWriteFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".session-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if err := inheritSessionFileOwner(tmpPath, path); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// Old pages stay available for a grace period so a reader holding the previous
// manifest can finish. Interrupted writes are reclaimed by later saves.
func (fs *FileSystemStorage) retireOldPages(id string, previous *sessionManifest, current []messagePageRef) {
	if previous == nil {
		return
	}
	active := make(map[string]struct{}, len(current))
	for _, ref := range current {
		active[ref.File] = struct{}{}
	}
	now := time.Now()
	for _, ref := range previous.Pages {
		if _, ok := active[ref.File]; ok {
			continue
		}
		if err := os.Chtimes(filepath.Join(fs.pageDir(id), ref.File), now, now); err != nil && !os.IsNotExist(err) {
			fs.logger.Warn("cannot retire old session page", "session", id, "page", ref.File, "error", err)
		}
	}
}

func (fs *FileSystemStorage) pruneUnreferencedPages(id string, refs []messagePageRef) {
	active := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		active[ref.File] = struct{}{}
	}
	entries, err := os.ReadDir(fs.pageDir(id))
	if err != nil {
		fs.logger.Warn("cannot inspect old session pages", "session", id, "error", err)
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !validPageName(entry.Name()) {
			continue
		}
		if _, ok := active[entry.Name()]; ok {
			continue
		}
		info, err := entry.Info()
		if err != nil || time.Since(info.ModTime()) < pageRetention {
			continue
		}
		if err := os.Remove(filepath.Join(fs.pageDir(id), entry.Name())); err != nil {
			fs.logger.Warn("cannot remove old session page", "session", id, "page", entry.Name(), "error", err)
		}
	}
}
