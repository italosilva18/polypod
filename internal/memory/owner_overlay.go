package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// OwnerOverlayStore keeps each profile's general memories in its private DB
// and places only owner-linked facts in a dedicated cross-process file.
type OwnerOverlayStore struct {
	private   Store
	shared    *SharedFileStore
	ownerChat string
}

func NewOwnerOverlayStore(private Store, sharedDir, ownerChat string) (*OwnerOverlayStore, error) {
	if private == nil {
		return nil, errors.New("private memory store is nil")
	}
	if strings.TrimSpace(ownerChat) == "" {
		return nil, errors.New("owner WhatsApp chat is required for shared owner memory")
	}
	shared, err := NewSharedFileStore(sharedDir)
	if err != nil {
		return nil, err
	}
	return &OwnerOverlayStore{private: private, shared: shared, ownerChat: ownerChat}, nil
}

func ownerMemoryScope(m Memory, ownerChat string) bool {
	if m.Source != "user" && !(m.Source == "dream" && hasTag(m, "evidence:quote_verified")) {
		return false
	}
	switch {
	case m.SessionID == "whatsapp:"+ownerChat, m.SessionID == "system:owner":
		return true
	case m.SessionID == "cli:local", strings.HasPrefix(m.SessionID, "cli-branch:"):
		return true
	default:
		return false
	}
}

func (s *OwnerOverlayStore) sharedWrite(ctx context.Context, m Memory) bool {
	if ownerMemoryScope(m, s.ownerChat) {
		return true
	}
	return m.SessionID == "" && m.Source == "user" && ctx != nil && ctx.Value(globalMemoryAccessKey{}) == true
}

func (s *OwnerOverlayStore) Save(ctx context.Context, topic, content string) error {
	return s.SaveWithMeta(ctx, Memory{Topic: topic, Content: content, Source: "user", Confidence: 1})
}

func (s *OwnerOverlayStore) SaveWithMeta(ctx context.Context, m Memory) error {
	if s.sharedWrite(ctx, m) {
		return s.shared.SaveWithMeta(ctx, m)
	}
	return s.private.SaveWithMeta(ctx, m)
}

func (s *OwnerOverlayStore) Get(ctx context.Context, topic string) (*Memory, error) {
	shared, sharedErr := s.shared.Get(ctx, topic)
	if sharedErr != nil && !errors.Is(sharedErr, errSharedMemoryNotFound) {
		return nil, sharedErr
	}
	private, privateErr := s.private.Get(ctx, topic)
	if shared != nil && memoryVisible(ctx, *shared) {
		return shared, nil
	}
	if privateErr == nil && memoryVisible(ctx, *private) {
		return private, nil
	}
	// Raw administrative callers without a session can still inspect a scoped
	// record. Agent-facing ManagedStore.Get filters this fallback again.
	if sessionScope(ctx) == "" {
		if shared != nil {
			return shared, nil
		}
		if privateErr == nil {
			return private, nil
		}
	}
	return nil, fmt.Errorf("memoria '%s' nao encontrada", topic)
}

func (s *OwnerOverlayStore) List(ctx context.Context) ([]Memory, error) {
	private, err := s.private.List(ctx)
	if err != nil {
		return nil, err
	}
	shared, err := s.shared.List(ctx)
	if err != nil {
		return nil, err
	}
	// A topic may legitimately exist in two conversations. Overlay only the
	// same topic *and* scope, so another chat never loses its private fact.
	byTopic := make(map[string]Memory, len(private)+len(shared))
	for _, m := range private {
		byTopic[sharedMemoryKey(m)] = m
	}
	for _, m := range shared {
		byTopic[sharedMemoryKey(m)] = m
	}
	all := make([]Memory, 0, len(byTopic))
	for _, m := range byTopic {
		all = append(all, m)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].UpdatedAt.After(all[j].UpdatedAt) })
	return all, nil
}

func (s *OwnerOverlayStore) Search(ctx context.Context, query string) ([]Memory, error) {
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(query)
	var matches []Memory
	for _, m := range all {
		if strings.Contains(strings.ToLower(m.Topic), needle) || strings.Contains(strings.ToLower(m.Content), needle) {
			matches = append(matches, m)
		}
	}
	return matches, nil
}

func (s *OwnerOverlayStore) SearchRelevant(ctx context.Context, query string, maxResults int, minScore float64) ([]Memory, error) {
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	var matches []Memory
	for _, m := range all {
		relevant, score := m.IsRelevant(query)
		if relevant && score >= minScore {
			matches = append(matches, m)
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		_, a := matches[i].IsRelevant(query)
		_, b := matches[j].IsRelevant(query)
		return a > b
	})
	if maxResults > 0 && len(matches) > maxResults {
		matches = matches[:maxResults]
	}
	return matches, nil
}

func (s *OwnerOverlayStore) Delete(ctx context.Context, topic string) error {
	m, err := s.Get(ctx, topic)
	if err != nil {
		return err
	}
	if s.sharedWrite(ctx, *m) {
		if _, err := s.shared.Get(ctx, topic); err == nil {
			return s.shared.Delete(ctx, topic)
		} else if !errors.Is(err, errSharedMemoryNotFound) {
			return err
		}
		// The old copy remains private. The marker hides it across restarts.
		return s.shared.SaveWithMeta(ctx, Memory{Topic: topic, SessionID: m.SessionID,
			Source: "user", Tags: []string{forgottenTag}, Confidence: 1})
	}
	return s.private.Delete(ctx, topic)
}

// NeedsFreshIndex reports that another process can update the shared file at
// any time. A semantic index must refresh before each retrieval.
func (s *OwnerOverlayStore) NeedsFreshIndex() bool { return true }

// BackfillOwnerFacts copies eligible legacy facts into the shared file without
// changing either private DB. A private 0600 snapshot is written before import.
// Legacy unscoped user facts are imported only from a trusted local CLI profile.
func (s *OwnerOverlayStore) BackfillOwnerFacts(ctx context.Context, backupDir string, importLocal bool) (int, error) {
	private, err := s.private.List(ctx)
	if err != nil {
		return 0, err
	}
	shared, err := s.shared.List(ctx)
	if err != nil {
		return 0, err
	}
	seen := make(map[string]bool, len(shared))
	for _, m := range shared {
		seen[sharedMemoryKey(m)] = true
	}
	var eligible, missing []Memory
	for _, m := range private {
		owner := ownerMemoryScope(m, s.ownerChat) ||
			(importLocal && m.SessionID == "" && m.Source == "user")
		if !owner {
			continue
		}
		eligible = append(eligible, m)
		if !seen[sharedMemoryKey(m)] {
			missing = append(missing, m)
		}
	}
	if len(missing) == 0 {
		return 0, nil
	}
	if err := backupOwnerMemories(backupDir, eligible); err != nil {
		return 0, fmt.Errorf("backing up owner memories before import: %w", err)
	}
	count := 0
	for _, m := range missing {
		imported, err := s.shared.ImportIfAbsent(ctx, m)
		if err != nil {
			return count, err
		}
		if imported {
			count++
		}
	}
	return count, nil
}

func backupOwnerMemories(dir string, memories []Memory) error {
	if strings.TrimSpace(dir) == "" || filepath.Clean(dir) == "." || filepath.Clean(dir) == string(filepath.Separator) {
		return errors.New("owner memory backup directory is unsafe")
	}
	if err := rejectSharedSymlink(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, "owner-memory-pre-import-*.json")
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		os.Remove(file.Name())
		return err
	}
	encoder := json.NewEncoder(file)
	if err := encoder.Encode(memories); err != nil {
		os.Remove(file.Name())
		return err
	}
	if err := file.Sync(); err != nil {
		os.Remove(file.Name())
		return err
	}
	if err := file.Close(); err != nil {
		os.Remove(file.Name())
		return err
	}
	return syncSharedDirectory(filepath.Clean(dir))
}
