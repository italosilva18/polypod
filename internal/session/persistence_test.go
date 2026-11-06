package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newTestStorage returns a FileSystemStorage rooted in a fresh temp directory.
func newTestStorage(t *testing.T) *FileSystemStorage {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sessions")
	return NewFileSystemStorage(dir, nil)
}

// makeSession builds a fully-populated RestoredSession for tests.
func makeSession(id, title string, numMessages int) *RestoredSession {
	now := time.Now().UTC().Truncate(time.Second)
	msgs := make([]Message, 0, numMessages)
	for i := 0; i < numMessages; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, Message{
			Role:    role,
			Content: fmt.Sprintf("message %d from %s", i, role),
		})
	}
	return &RestoredSession{
		ID:        id,
		Title:     title,
		Messages:  msgs,
		CreatedAt: now,
		UpdatedAt: now,
		Metadata: SessionMetadata{
			AgentName:   "test-agent",
			AgentColor:  "blue",
			Tag:         "testing",
			Mode:        "chat",
			GitBranch:   "main",
			ProjectRoot: "/tmp/test-project",
			LastPrompt:  "hello there",
		},
	}
}

// ---------------------------------------------------------------------------
// FileSystemStorage: Save / Load round-trip
// ---------------------------------------------------------------------------

func TestFileSystemStorage_SaveLoad(t *testing.T) {
	storage := newTestStorage(t)

	sess := makeSession("ses_test1", "Test Session", 4)
	if err := storage.Save(sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify file was written.
	path := filepath.Join(storage.dir, "ses_test1.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("session file not created on disk: %v", err)
	}

	loaded, err := storage.Load("ses_test1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil {
		t.Fatal("Load returned nil session for existing id")
	}

	if loaded.ID != sess.ID {
		t.Errorf("ID = %q; want %q", loaded.ID, sess.ID)
	}
	if loaded.Title != sess.Title {
		t.Errorf("Title = %q; want %q", loaded.Title, sess.Title)
	}
	if len(loaded.Messages) != len(sess.Messages) {
		t.Errorf("Messages len = %d; want %d", len(loaded.Messages), len(sess.Messages))
	}
	if loaded.Version != CurrentSessionVersion {
		t.Errorf("Version = %d; want %d", loaded.Version, CurrentSessionVersion)
	}
}

func TestFileSystemStorage_Save_SetsCurrentVersion(t *testing.T) {
	storage := newTestStorage(t)

	sess := makeSession("ses_ver", "Version Test", 1)
	sess.Version = 0 // explicitly stale
	if err := storage.Save(sess); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Save should bump the in-memory version to current.
	if sess.Version != CurrentSessionVersion {
		t.Errorf("in-memory Version = %d; want %d", sess.Version, CurrentSessionVersion)
	}

	loaded, err := storage.Load("ses_ver")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Version != CurrentSessionVersion {
		t.Errorf("loaded Version = %d; want %d", loaded.Version, CurrentSessionVersion)
	}
}

func TestFileSystemStorage_Load_MissingReturnsNil(t *testing.T) {
	storage := newTestStorage(t)

	loaded, err := storage.Load("does-not-exist")
	if err != nil {
		t.Fatalf("Load missing session returned error: %v", err)
	}
	if loaded != nil {
		t.Errorf("Load missing session = %v; want nil", loaded)
	}
}

func TestFileSystemStorage_Load_CorruptFileReturnsError(t *testing.T) {
	storage := newTestStorage(t)

	// Directly write garbage to a session file.
	if err := os.MkdirAll(storage.dir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(storage.dir, "ses_corrupt.json")
	if err := os.WriteFile(path, []byte("not valid json {"), 0600); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	_, err := storage.Load("ses_corrupt")
	if err == nil {
		t.Fatal("Load of corrupt file should return error, got nil")
	}
	if !strings.Contains(err.Error(), "parsing session") {
		t.Errorf("error = %v; want 'parsing session'", err)
	}
}

// ---------------------------------------------------------------------------
// FileSystemStorage: metadata round-trip
// ---------------------------------------------------------------------------

func TestFileSystemStorage_MetadataRoundtrip(t *testing.T) {
	storage := newTestStorage(t)

	sess := makeSession("ses_meta", "Metadata Test", 2)
	sess.Metadata = SessionMetadata{
		AgentName:    "claude",
		AgentColor:   "purple",
		AgentSetting: "creative",
		CustomTitle:  "My Custom Title",
		Tag:          "work",
		Mode:         "code",
		GitBranch:    "feature/x",
		ProjectRoot:  "/home/user/project",
		LastPrompt:   "write some tests",
	}

	if err := storage.Save(sess); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := storage.Load("ses_meta")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got := loaded.Metadata
	want := sess.Metadata
	if got != want {
		t.Errorf("metadata mismatch:\ngot:  %+v\nwant: %+v", got, want)
	}
}

// ---------------------------------------------------------------------------
// FileSystemStorage: messages preserved in order
// ---------------------------------------------------------------------------

func TestFileSystemStorage_MessageOrderPreserved(t *testing.T) {
	storage := newTestStorage(t)

	sess := &RestoredSession{
		ID:        "ses_order",
		Title:     "Order Test",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Messages: []Message{
			{Role: "user", Content: "first"},
			{Role: "assistant", Content: "second"},
			{Role: "user", Content: "third"},
			{Role: "assistant", Content: "fourth"},
			{Role: "user", Content: "fifth"},
		},
	}

	if err := storage.Save(sess); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := storage.Load("ses_order")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(loaded.Messages) != 5 {
		t.Fatalf("Messages len = %d; want 5", len(loaded.Messages))
	}
	expected := []string{"first", "second", "third", "fourth", "fifth"}
	for i, m := range loaded.Messages {
		if m.Content != expected[i] {
			t.Errorf("Messages[%d].Content = %q; want %q", i, m.Content, expected[i])
		}
	}
}

// ---------------------------------------------------------------------------
// FileSystemStorage: empty session
// ---------------------------------------------------------------------------

func TestFileSystemStorage_EmptySession(t *testing.T) {
	storage := newTestStorage(t)

	sess := &RestoredSession{
		ID:        "ses_empty",
		Title:     "Empty",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		// No Messages, no Metadata
	}
	if err := storage.Save(sess); err != nil {
		t.Fatalf("Save empty: %v", err)
	}
	loaded, err := storage.Load("ses_empty")
	if err != nil {
		t.Fatalf("Load empty: %v", err)
	}
	if loaded == nil {
		t.Fatal("Load empty = nil")
	}
	if len(loaded.Messages) != 0 {
		t.Errorf("empty session has %d messages; want 0", len(loaded.Messages))
	}
}

// ---------------------------------------------------------------------------
// FileSystemStorage: Delete
// ---------------------------------------------------------------------------

func TestFileSystemStorage_Delete(t *testing.T) {
	storage := newTestStorage(t)

	sess := makeSession("ses_del", "To Be Deleted", 2)
	if err := storage.Save(sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Confirm it's there.
	loaded, err := storage.Load("ses_del")
	if err != nil || loaded == nil {
		t.Fatalf("pre-delete Load failed: %v %v", loaded, err)
	}

	if err := storage.Delete("ses_del"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// File should be gone.
	path := filepath.Join(storage.dir, "ses_del.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still exists after Delete, stat err = %v", err)
	}

	// Load should return nil, nil.
	loaded, err = storage.Load("ses_del")
	if err != nil {
		t.Errorf("post-delete Load err = %v; want nil", err)
	}
	if loaded != nil {
		t.Errorf("post-delete Load = %v; want nil", loaded)
	}

	// Delete again should be a no-op (idempotent).
	if err := storage.Delete("ses_del"); err != nil {
		t.Errorf("second Delete err = %v; want nil", err)
	}
}

// ---------------------------------------------------------------------------
// FileSystemStorage: List
// ---------------------------------------------------------------------------

func TestFileSystemStorage_List_SortedByUpdatedAt(t *testing.T) {
	storage := newTestStorage(t)

	base := time.Now().UTC().Truncate(time.Second)
	sessions := []*RestoredSession{
		{ID: "a", Title: "A", CreatedAt: base.Add(-3 * time.Hour), UpdatedAt: base.Add(-3 * time.Hour)},
		{ID: "b", Title: "B", CreatedAt: base.Add(-1 * time.Hour), UpdatedAt: base.Add(-1 * time.Hour)},
		{ID: "c", Title: "C", CreatedAt: base.Add(-2 * time.Hour), UpdatedAt: base.Add(-2 * time.Hour)},
	}
	for _, s := range sessions {
		if err := storage.Save(s); err != nil {
			t.Fatalf("Save %s: %v", s.ID, err)
		}
	}

	got, err := storage.List(SessionFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("List len = %d; want 3", len(got))
	}

	// Newest first: b (-1h), c (-2h), a (-3h).
	wantOrder := []string{"b", "c", "a"}
	for i, s := range got {
		if s.ID != wantOrder[i] {
			t.Errorf("List[%d].ID = %q; want %q", i, s.ID, wantOrder[i])
		}
	}
}

func TestFileSystemStorage_List_EmptyDir(t *testing.T) {
	// Point to a directory that does not exist yet.
	dir := filepath.Join(t.TempDir(), "never-created")
	// Do NOT call NewFileSystemStorage because that mkdirs; construct manually.
	storage := &FileSystemStorage{dir: dir}

	summaries, err := storage.List(SessionFilter{})
	if err != nil {
		t.Fatalf("List on missing dir: %v", err)
	}
	if len(summaries) != 0 {
		t.Errorf("List on missing dir = %d; want 0", len(summaries))
	}
}

func TestFileSystemStorage_List_MaxCount(t *testing.T) {
	storage := newTestStorage(t)
	base := time.Now().UTC()
	for i := 0; i < 5; i++ {
		s := &RestoredSession{
			ID:        fmt.Sprintf("ses_%d", i),
			Title:     fmt.Sprintf("Session %d", i),
			CreatedAt: base,
			UpdatedAt: base.Add(time.Duration(i) * time.Minute),
		}
		if err := storage.Save(s); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	got, err := storage.List(SessionFilter{MaxCount: 2})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("List MaxCount=2 len = %d; want 2", len(got))
	}
}

func TestFileSystemStorage_List_FilterByTag(t *testing.T) {
	storage := newTestStorage(t)
	base := time.Now().UTC()

	a := makeSession("a", "A", 1)
	a.Metadata.Tag = "work"
	a.UpdatedAt = base
	b := makeSession("b", "B", 1)
	b.Metadata.Tag = "personal"
	b.UpdatedAt = base
	c := makeSession("c", "C", 1)
	c.Metadata.Tag = "work"
	c.UpdatedAt = base

	for _, s := range []*RestoredSession{a, b, c} {
		if err := storage.Save(s); err != nil {
			t.Fatalf("Save %s: %v", s.ID, err)
		}
	}

	got, err := storage.List(SessionFilter{Tag: "work"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("List tag=work len = %d; want 2", len(got))
	}
	for _, s := range got {
		if s.Tag != "work" {
			t.Errorf("unexpected tag in filtered list: %q", s.Tag)
		}
	}
}

// ---------------------------------------------------------------------------
// FileSystemStorage: Search
// ---------------------------------------------------------------------------

func TestFileSystemStorage_Search(t *testing.T) {
	storage := newTestStorage(t)

	s1 := &RestoredSession{
		ID:        "s1",
		Title:     "Database design",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Messages: []Message{
			{Role: "user", Content: "How do I normalize a schema?"},
			{Role: "assistant", Content: "You should use third normal form."},
		},
	}
	s2 := &RestoredSession{
		ID:        "s2",
		Title:     "Frontend styling",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Messages: []Message{
			{Role: "user", Content: "How to center a div?"},
			{Role: "assistant", Content: "Use flexbox."},
		},
	}
	for _, s := range []*RestoredSession{s1, s2} {
		if err := storage.Save(s); err != nil {
			t.Fatalf("Save %s: %v", s.ID, err)
		}
	}

	// Search by message content.
	got, err := storage.Search("flexbox")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].ID != "s2" {
		t.Errorf("Search 'flexbox' = %+v; want s2 only", got)
	}

	// Search by title (case-insensitive).
	got, err = storage.Search("DATABASE")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].ID != "s1" {
		t.Errorf("Search 'DATABASE' = %+v; want s1 only", got)
	}

	// Search with no matches.
	got, err = storage.Search("nonexistent-term-xyz")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Search no-match len = %d; want 0", len(got))
	}
}

// ---------------------------------------------------------------------------
// FileSystemStorage: Export
// ---------------------------------------------------------------------------

func TestFileSystemStorage_Export_JSON(t *testing.T) {
	storage := newTestStorage(t)
	sess := makeSession("ses_exp", "Export Me", 3)
	if err := storage.Save(sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	out := filepath.Join(t.TempDir(), "exported.json")
	if err := storage.Export("ses_exp", out, "json"); err != nil {
		t.Fatalf("Export: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read exported: %v", err)
	}
	var round RestoredSession
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("exported file is not valid JSON: %v", err)
	}
	if round.ID != "ses_exp" {
		t.Errorf("exported ID = %q; want ses_exp", round.ID)
	}
}

func TestFileSystemStorage_Export_Markdown(t *testing.T) {
	storage := newTestStorage(t)
	sess := &RestoredSession{
		ID:        "ses_md",
		Title:     "Markdown Export",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Messages: []Message{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there"},
		},
	}
	if err := storage.Save(sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	out := filepath.Join(t.TempDir(), "exported.md")
	if err := storage.Export("ses_md", out, "markdown"); err != nil {
		t.Fatalf("Export markdown: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read md: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "# Markdown Export") {
		t.Errorf("markdown missing title header: %s", content)
	}
	if !strings.Contains(content, "Hello") || !strings.Contains(content, "Hi there") {
		t.Errorf("markdown missing message content")
	}
}

func TestFileSystemStorage_Export_MissingSession(t *testing.T) {
	storage := newTestStorage(t)
	out := filepath.Join(t.TempDir(), "should-not-exist.json")
	err := storage.Export("nope", out, "json")
	if err == nil {
		t.Fatal("Export missing session should error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v; want 'not found'", err)
	}
}

// ---------------------------------------------------------------------------
// FileSystemStorage: concurrent saves (different IDs)
// ---------------------------------------------------------------------------

func TestFileSystemStorage_ConcurrentSave(t *testing.T) {
	storage := newTestStorage(t)

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := makeSession(fmt.Sprintf("concurrent_%d", i),
				fmt.Sprintf("Concurrent %d", i), 3)
			if err := storage.Save(s); err != nil {
				errs <- fmt.Errorf("save %d: %w", i, err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for e := range errs {
		t.Error(e)
	}

	// All files should be present.
	got, err := storage.List(SessionFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != n {
		t.Errorf("concurrent saves produced %d sessions; want %d", len(got), n)
	}
}

// ---------------------------------------------------------------------------
// FileSystemStorage: very large session
// ---------------------------------------------------------------------------

func TestFileSystemStorage_LargeSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large session test in short mode")
	}
	storage := newTestStorage(t)

	sess := makeSession("ses_big", "Big One", 1200)
	if err := storage.Save(sess); err != nil {
		t.Fatalf("Save large: %v", err)
	}

	loaded, err := storage.Load("ses_big")
	if err != nil {
		t.Fatalf("Load large: %v", err)
	}
	if len(loaded.Messages) != 1200 {
		t.Errorf("large session Messages = %d; want 1200", len(loaded.Messages))
	}
	// Spot-check a few.
	if loaded.Messages[0].Content != "message 0 from user" {
		t.Errorf("first message wrong: %q", loaded.Messages[0].Content)
	}
	if loaded.Messages[1199].Content != "message 1199 from assistant" {
		t.Errorf("last message wrong: %q", loaded.Messages[1199].Content)
	}
}

// ---------------------------------------------------------------------------
// Manager (index) tests
// ---------------------------------------------------------------------------

func TestManager_NewManager_CreatesDir(t *testing.T) {
	base := t.TempDir()
	// Use a nested path that doesn't exist to confirm creation.
	dataDir := filepath.Join(base, "nested", "data")

	_ = NewManager(dataDir, nil)

	want := filepath.Join(dataDir, "sessions")
	info, err := os.Stat(want)
	if err != nil {
		t.Fatalf("sessions dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("sessions path is not a directory")
	}
}

func TestManager_SaveAndListSessions(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir, nil)

	// Empty list initially.
	got, err := m.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("initial list = %d; want 0", len(got))
	}

	base := time.Now().UTC().Truncate(time.Second)
	a := SessionIndex{
		ID: "idx_a", Channel: "cli", UserID: "u1", Title: "A",
		CreatedAt: base.Add(-2 * time.Hour), UpdatedAt: base.Add(-2 * time.Hour),
		Messages: 5, Tokens: 100,
	}
	b := SessionIndex{
		ID: "idx_b", Channel: "cli", UserID: "u1", Title: "B",
		CreatedAt: base.Add(-1 * time.Hour), UpdatedAt: base.Add(-1 * time.Hour),
		Messages: 3, Tokens: 80,
	}

	if err := m.SaveSessionIndex(a); err != nil {
		t.Fatalf("SaveSessionIndex a: %v", err)
	}
	if err := m.SaveSessionIndex(b); err != nil {
		t.Fatalf("SaveSessionIndex b: %v", err)
	}

	got, err = m.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListSessions len = %d; want 2", len(got))
	}
	// Newest (b) first.
	if got[0].ID != "idx_b" {
		t.Errorf("ListSessions[0].ID = %q; want idx_b", got[0].ID)
	}
	if got[0].Tokens != 80 || got[0].Messages != 3 {
		t.Errorf("Tokens/Messages not preserved: %+v", got[0])
	}
}

func TestManager_SaveSessionIndex_UpdatesExisting(t *testing.T) {
	m := NewManager(t.TempDir(), nil)

	base := time.Now().UTC()
	idx := SessionIndex{
		ID: "same", Channel: "cli", Title: "Original",
		CreatedAt: base, UpdatedAt: base, Messages: 1,
	}
	if err := m.SaveSessionIndex(idx); err != nil {
		t.Fatalf("first save: %v", err)
	}

	// Update the same ID.
	idx.Title = "Updated"
	idx.Messages = 42
	idx.UpdatedAt = base.Add(1 * time.Hour)
	if err := m.SaveSessionIndex(idx); err != nil {
		t.Fatalf("update save: %v", err)
	}

	got, err := m.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("list len after update = %d; want 1", len(got))
	}
	if got[0].Title != "Updated" || got[0].Messages != 42 {
		t.Errorf("updated entry = %+v; want Title=Updated Messages=42", got[0])
	}
}

func TestManager_GetLastSession(t *testing.T) {
	m := NewManager(t.TempDir(), nil)

	base := time.Now().UTC()
	entries := []SessionIndex{
		{ID: "c1-old", Channel: "cli", UpdatedAt: base.Add(-2 * time.Hour)},
		{ID: "c1-new", Channel: "cli", UpdatedAt: base.Add(-10 * time.Minute)},
		{ID: "web1", Channel: "web", UpdatedAt: base.Add(-30 * time.Minute)},
	}
	for _, e := range entries {
		if err := m.SaveSessionIndex(e); err != nil {
			t.Fatalf("save %s: %v", e.ID, err)
		}
	}

	last, err := m.GetLastSession("cli")
	if err != nil {
		t.Fatalf("GetLastSession cli: %v", err)
	}
	if last == nil {
		t.Fatal("GetLastSession cli = nil")
	}
	if last.ID != "c1-new" {
		t.Errorf("GetLastSession cli = %q; want c1-new", last.ID)
	}

	last, err = m.GetLastSession("unknown-channel")
	if err != nil {
		t.Fatalf("GetLastSession unknown: %v", err)
	}
	if last != nil {
		t.Errorf("GetLastSession unknown = %+v; want nil", last)
	}
}

func TestManager_ListSessions_CorruptIndex(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir, nil)

	// Write garbage to the index file.
	if err := os.WriteFile(m.indexPath(), []byte("not json"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := m.ListSessions()
	if err == nil {
		t.Error("ListSessions on corrupt index should error")
	}
}

// ---------------------------------------------------------------------------
// Helper functions: GenerateSessionID, GenerateTitle, TitleFromFirstMessage
// ---------------------------------------------------------------------------

func TestGenerateSessionID(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := GenerateSessionID()
		if !strings.HasPrefix(id, "ses_") {
			t.Errorf("id %q missing ses_ prefix", id)
		}
		if len(id) != len("ses_")+12 { // 6 bytes -> 12 hex chars
			t.Errorf("id %q has unexpected length %d", id, len(id))
		}
		if seen[id] {
			t.Errorf("duplicate id generated: %q", id)
		}
		seen[id] = true
	}
}

func TestGenerateTitle(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"hello world", "hello world"},
		{"   trimmed   ", "trimmed"},
		{strings.Repeat("a", 80), strings.Repeat("a", 57) + "..."},
	}
	for _, tc := range cases {
		got := GenerateTitle(tc.in)
		if got != tc.want {
			t.Errorf("GenerateTitle(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

func TestTitleFromFirstMessage_AutoTitle(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "<context>ignore me</context>"},
		{Role: "assistant", Content: "I am not a user"},
		{Role: "user", Content: "How do I build a CLI in Go?"},
		{Role: "user", Content: "follow-up"},
	}
	got := TitleFromFirstMessage(msgs)
	if got == "" || got == "Untitled session" {
		t.Errorf("TitleFromFirstMessage = %q; want derived from first user msg", got)
	}
	if !strings.Contains(strings.ToLower(got), "cli") {
		t.Errorf("TitleFromFirstMessage = %q; want to include 'cli'", got)
	}

	// No user messages → fallback.
	noUser := []Message{
		{Role: "assistant", Content: "hello"},
		{Role: "system", Content: "prompt"},
	}
	if got := TitleFromFirstMessage(noUser); got != "Untitled session" {
		t.Errorf("no-user TitleFromFirstMessage = %q; want Untitled session", got)
	}

	// Empty slice.
	if got := TitleFromFirstMessage(nil); got != "Untitled session" {
		t.Errorf("empty TitleFromFirstMessage = %q; want Untitled session", got)
	}
}
