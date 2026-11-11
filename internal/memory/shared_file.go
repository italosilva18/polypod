package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SharedFileStore is a small, process-safe store for owner facts. Each operation
// reloads the file while holding a separate, stable lock inode. It must not be
// used for the general conversation or secrets databases.
type SharedFileStore struct {
	dir  string
	path string
	uid  int
	gid  int
}

var errSharedMemoryNotFound = errors.New("owner memory not found")

func sharedMemoryKey(m Memory) string {
	// A length prefix keeps the key unambiguous even if either field has NUL.
	return strconv.Itoa(len(m.Topic)) + ":" + m.Topic + m.SessionID
}

func NewSharedFileStore(dir string) (*SharedFileStore, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("owner memory directory is empty")
	}
	if !filepath.IsAbs(dir) {
		return nil, errors.New("owner memory directory must be an absolute path")
	}
	absolute := filepath.Clean(dir)
	uid, gid, err := ensureSharedDirectory(absolute)
	if err != nil {
		return nil, err
	}
	return &SharedFileStore{dir: absolute, path: filepath.Join(absolute, "owner-memories.json"), uid: uid, gid: gid}, nil
}

func (s *SharedFileStore) withLocked(ctx context.Context, mutate func(map[string]Memory) (bool, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	lockPath := s.path + ".lock"
	if err := rejectSharedSymlink(lockPath); err != nil {
		return err
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("opening owner memory lock: %w", err)
	}
	defer lock.Close()
	if err := setSharedFileOwner(lock, s.uid, s.gid); err != nil {
		return fmt.Errorf("setting owner memory lock ownership: %w", err)
	}
	if err := lockSharedMemory(lock); err != nil {
		return fmt.Errorf("locking owner memory: %w", err)
	}
	defer unlockSharedMemory(lock)
	if err := ctx.Err(); err != nil {
		return err
	}
	memories, err := s.readLocked()
	if err != nil {
		return err
	}
	changed, err := mutate(memories)
	if err != nil || !changed {
		return err
	}
	return s.writeLocked(memories)
}

func rejectSharedSymlink(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("owner memory path is a symlink: %s", path)
	}
	return nil
}

func (s *SharedFileStore) readLocked() (map[string]Memory, error) {
	if err := rejectSharedSymlink(s.path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]Memory), nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading owner memories: %w", err)
	}
	var list []Memory
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("decoding owner memories: %w", err)
	}
	memories := make(map[string]Memory, len(list))
	for _, m := range list {
		if m.Topic == "" {
			return nil, errors.New("owner memory has an empty topic")
		}
		key := sharedMemoryKey(m)
		if _, duplicate := memories[key]; duplicate {
			return nil, fmt.Errorf("duplicate owner memory topic and scope: %s", m.Topic)
		}
		memories[key] = m
	}
	return memories, nil
}

func (s *SharedFileStore) writeLocked(memories map[string]Memory) error {
	list := make([]Memory, 0, len(memories))
	for _, m := range memories {
		list = append(list, m)
	}
	sort.Slice(list, func(i, j int) bool { return sharedMemoryKey(list[i]) < sharedMemoryKey(list[j]) })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(s.dir, ".owner-memories-*")
	if err != nil {
		return fmt.Errorf("creating owner memory temp file: %w", err)
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err := setSharedFileOwner(temp, s.uid, s.gid); err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(temp.Name(), s.path); err != nil {
		return err
	}
	return syncSharedDirectory(s.dir)
}

func nextSharedID(memories map[string]Memory) int64 {
	var maxID int64
	for _, m := range memories {
		if m.ID > maxID {
			maxID = m.ID
		}
	}
	return maxID + 1
}

func (s *SharedFileStore) Save(ctx context.Context, topic, content string) error {
	return s.SaveWithMeta(ctx, Memory{Topic: topic, Content: content, Source: "user", Confidence: 1})
}

func (s *SharedFileStore) SaveWithMeta(ctx context.Context, mem Memory) error {
	if mem.Topic == "" {
		return errors.New("owner memory topic is empty")
	}
	return s.withLocked(ctx, func(all map[string]Memory) (bool, error) {
		now := time.Now().UTC()
		key := sharedMemoryKey(mem)
		if previous, exists := all[key]; exists {
			mem.ID = previous.ID
			mem.CreatedAt = previous.CreatedAt
		} else {
			mem.ID = nextSharedID(all)
			if mem.CreatedAt.IsZero() {
				mem.CreatedAt = now
			}
		}
		mem.UpdatedAt = now
		all[key] = mem
		return true, nil
	})
}

// ImportIfAbsent preserves source timestamps and never replaces a newer shared
// fact or a shared forget marker. This makes restart migration idempotent.
func (s *SharedFileStore) ImportIfAbsent(ctx context.Context, mem Memory) (bool, error) {
	if mem.Topic == "" {
		return false, errors.New("owner memory topic is empty")
	}
	imported := false
	err := s.withLocked(ctx, func(all map[string]Memory) (bool, error) {
		key := sharedMemoryKey(mem)
		if _, exists := all[key]; exists {
			return false, nil
		}
		mem.ID = nextSharedID(all)
		if mem.CreatedAt.IsZero() {
			mem.CreatedAt = time.Now().UTC()
		}
		if mem.UpdatedAt.IsZero() {
			mem.UpdatedAt = mem.CreatedAt
		}
		all[key] = mem
		imported = true
		return true, nil
	})
	return imported, err
}

func (s *SharedFileStore) Get(ctx context.Context, topic string) (*Memory, error) {
	var found *Memory
	err := s.withLocked(ctx, func(all map[string]Memory) (bool, error) {
		found = selectSharedMemory(ctx, all, topic)
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, fmt.Errorf("%w: %s", errSharedMemoryNotFound, topic)
	}
	return found, nil
}

func selectSharedMemory(ctx context.Context, all map[string]Memory, topic string) *Memory {
	var selected *Memory
	best := -1
	scope := sessionScope(ctx)
	for _, m := range all {
		if m.Topic != topic {
			continue
		}
		score := 0
		if memoryVisible(ctx, m) {
			score = 1
			if m.SessionID == "" {
				score = 2
			}
			if scope != "" && m.SessionID == scope {
				score = 3
			}
		} else if scope != "" {
			continue
		}
		if selected == nil || score > best || score == best && sharedMemoryKey(m) < sharedMemoryKey(*selected) {
			copy := m
			selected = &copy
			best = score
		}
	}
	return selected
}

func (s *SharedFileStore) List(ctx context.Context) ([]Memory, error) {
	var list []Memory
	err := s.withLocked(ctx, func(all map[string]Memory) (bool, error) {
		list = make([]Memory, 0, len(all))
		for _, m := range all {
			list = append(list, m)
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].UpdatedAt.After(list[j].UpdatedAt) })
	return list, nil
}

func (s *SharedFileStore) Search(ctx context.Context, query string) ([]Memory, error) {
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

func (s *SharedFileStore) SearchRelevant(ctx context.Context, query string, maxResults int, minScore float64) ([]Memory, error) {
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

func (s *SharedFileStore) Delete(ctx context.Context, topic string) error {
	return s.withLocked(ctx, func(all map[string]Memory) (bool, error) {
		selected := selectSharedMemory(ctx, all, topic)
		if selected == nil {
			return false, fmt.Errorf("memoria '%s' nao encontrada", topic)
		}
		previous := *selected
		// A durable marker blocks startup backfill from resurrecting a fact.
		previous.Content = ""
		previous.Source = "user"
		previous.Confidence = 1
		previous.Tags = []string{forgottenTag}
		previous.UpdatedAt = time.Now().UTC()
		all[sharedMemoryKey(previous)] = previous
		return true, nil
	})
}
