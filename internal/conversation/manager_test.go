package conversation

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
)

// newTestManager returns a Manager backed by an in-memory-only Store.
// Passing nil pool, nil sqliteDB, and empty dataDir disables all persistence.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	store := NewStore(nil, nil, "")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewManager(store, logger)
}

// ── Session lifecycle ───────────────────────────────────────────────────────

func TestManager_GetSession_CreatesNew(t *testing.T) {
	m := newTestManager(t)

	sess, err := m.GetSession(context.Background(), "cli", "user1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess == nil {
		t.Fatal("got nil session")
	}
	if sess.Channel != "cli" {
		t.Errorf("Channel = %q; want %q", sess.Channel, "cli")
	}
	if sess.UserID != "user1" {
		t.Errorf("UserID = %q; want %q", sess.UserID, "user1")
	}
	if len(sess.Messages) != 0 {
		t.Errorf("new session should have no messages, got %d", len(sess.Messages))
	}
}

func TestManager_GetSession_SameIdentityReturnsSame(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	s1, err := m.GetSession(ctx, "cli", "user1")
	if err != nil {
		t.Fatalf("first GetSession: %v", err)
	}
	s2, err := m.GetSession(ctx, "cli", "user1")
	if err != nil {
		t.Fatalf("second GetSession: %v", err)
	}
	if s1 != s2 {
		t.Errorf("expected same session pointer for same identity")
	}
	if s1.ID != s2.ID {
		t.Errorf("session IDs differ: %q vs %q", s1.ID, s2.ID)
	}
}

func TestManager_GetSession_DifferentIdentityReturnsDifferent(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	s1, _ := m.GetSession(ctx, "cli", "user1")
	s2, _ := m.GetSession(ctx, "cli", "user2")
	s3, _ := m.GetSession(ctx, "discord", "user1")

	if s1.ID == s2.ID {
		t.Errorf("different users on same channel should have different sessions: %q == %q", s1.ID, s2.ID)
	}
	if s1.ID == s3.ID {
		t.Errorf("same user on different channels should have different sessions: %q == %q", s1.ID, s3.ID)
	}
	if s2.ID == s3.ID {
		t.Errorf("different channels and users should have different sessions: %q == %q", s2.ID, s3.ID)
	}
}

// ── Message operations ──────────────────────────────────────────────────────

func TestManager_AddUserMessage(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	sess, _ := m.GetSession(ctx, "cli", "u1")

	m.AddUserMessage(ctx, sess, "hello")

	if len(sess.Messages) != 1 {
		t.Fatalf("Messages len = %d; want 1", len(sess.Messages))
	}
	if sess.Messages[0].Role != RoleUser {
		t.Errorf("Role = %q; want %q", sess.Messages[0].Role, RoleUser)
	}
	if sess.Messages[0].Content != "hello" {
		t.Errorf("Content = %q; want %q", sess.Messages[0].Content, "hello")
	}
}

func TestManager_AddAssistantMessage(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	sess, _ := m.GetSession(ctx, "cli", "u1")

	if err := m.AddAssistantMessage(ctx, sess, "hi there"); err != nil {
		t.Fatalf("AddAssistantMessage: %v", err)
	}

	if len(sess.Messages) != 1 {
		t.Fatalf("Messages len = %d; want 1", len(sess.Messages))
	}
	if sess.Messages[0].Role != RoleAssistant {
		t.Errorf("Role = %q; want %q", sess.Messages[0].Role, RoleAssistant)
	}
	if sess.Messages[0].Content != "hi there" {
		t.Errorf("Content = %q; want %q", sess.Messages[0].Content, "hi there")
	}
}

func TestManager_GetHistory_Order(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	sess, _ := m.GetSession(ctx, "cli", "u1")

	m.AddUserMessage(ctx, sess, "q1")
	if err := m.AddAssistantMessage(ctx, sess, "a1"); err != nil {
		t.Fatalf("AddAssistantMessage: %v", err)
	}
	m.AddUserMessage(ctx, sess, "q2")
	if err := m.AddAssistantMessage(ctx, sess, "a2"); err != nil {
		t.Fatalf("AddAssistantMessage: %v", err)
	}

	hist := m.GetHistory(sess)
	if len(hist) != 4 {
		t.Fatalf("history len = %d; want 4", len(hist))
	}
	want := []struct {
		role    Role
		content string
	}{
		{RoleUser, "q1"},
		{RoleAssistant, "a1"},
		{RoleUser, "q2"},
		{RoleAssistant, "a2"},
	}
	for i, w := range want {
		if hist[i].Role != w.role {
			t.Errorf("hist[%d].Role = %q; want %q", i, hist[i].Role, w.role)
		}
		if hist[i].Content != w.content {
			t.Errorf("hist[%d].Content = %q; want %q", i, hist[i].Content, w.content)
		}
	}
}

// ── Truncate ────────────────────────────────────────────────────────────────

func TestManager_TruncateMessages_DropsLastN(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	sess, _ := m.GetSession(ctx, "cli", "u1")

	m.AddUserMessage(ctx, sess, "q1")
	_ = m.AddAssistantMessage(ctx, sess, "a1")
	m.AddUserMessage(ctx, sess, "q2")
	_ = m.AddAssistantMessage(ctx, sess, "a2")

	removed, err := m.TruncateMessages(ctx, sess, 2)
	if err != nil {
		t.Fatalf("TruncateMessages: %v", err)
	}
	if len(removed) != 2 {
		t.Errorf("removed len = %d; want 2", len(removed))
	}
	if len(removed) >= 2 {
		if removed[0].Content != "q2" || removed[1].Content != "a2" {
			t.Errorf("removed = [%q, %q]; want [q2 a2]", removed[0].Content, removed[1].Content)
		}
	}

	hist := m.GetHistory(sess)
	if len(hist) != 2 {
		t.Fatalf("remaining len = %d; want 2", len(hist))
	}
	if hist[0].Content != "q1" || hist[1].Content != "a1" {
		t.Errorf("remaining = [%q, %q]; want [q1 a1]", hist[0].Content, hist[1].Content)
	}
}

func TestManager_TruncateMessages_NGreaterThanLen(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	sess, _ := m.GetSession(ctx, "cli", "u1")

	m.AddUserMessage(ctx, sess, "only")
	_ = m.AddAssistantMessage(ctx, sess, "reply")

	removed, err := m.TruncateMessages(ctx, sess, 100)
	if err != nil {
		t.Fatalf("TruncateMessages: %v", err)
	}
	if len(removed) != 2 {
		t.Errorf("removed len = %d; want 2 (clamped)", len(removed))
	}
	if len(m.GetHistory(sess)) != 0 {
		t.Errorf("history should be empty after truncating beyond length")
	}
}

func TestManager_TruncateMessages_Zero(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	sess, _ := m.GetSession(ctx, "cli", "u1")

	m.AddUserMessage(ctx, sess, "q")
	_ = m.AddAssistantMessage(ctx, sess, "a")

	removed, err := m.TruncateMessages(ctx, sess, 0)
	if err != nil {
		t.Fatalf("TruncateMessages(0): %v", err)
	}
	if removed != nil {
		t.Errorf("removed = %v; want nil for n=0", removed)
	}
	if len(m.GetHistory(sess)) != 2 {
		t.Errorf("history changed after n=0 truncate")
	}
}

func TestManager_TruncateMessages_EmptySession(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	sess, _ := m.GetSession(ctx, "cli", "u1")

	removed, err := m.TruncateMessages(ctx, sess, 5)
	if err != nil {
		t.Fatalf("TruncateMessages on empty: %v", err)
	}
	if removed != nil {
		t.Errorf("removed = %v; want nil", removed)
	}
}

// ── Replace ─────────────────────────────────────────────────────────────────

func TestManager_ReplaceMessages_Overwrites(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	sess, _ := m.GetSession(ctx, "cli", "u1")

	m.AddUserMessage(ctx, sess, "old")
	_ = m.AddAssistantMessage(ctx, sess, "old reply")

	replacement := []Message{
		{Role: RoleUser, Content: "new1"},
		{Role: RoleAssistant, Content: "new2"},
		{Role: RoleUser, Content: "new3"},
	}
	if err := m.ReplaceMessages(ctx, sess, replacement); err != nil {
		t.Fatalf("ReplaceMessages: %v", err)
	}

	hist := m.GetHistory(sess)
	if len(hist) != 3 {
		t.Fatalf("history len = %d; want 3", len(hist))
	}
	for i, want := range replacement {
		if hist[i].Role != want.Role || hist[i].Content != want.Content {
			t.Errorf("hist[%d] = {%q, %q}; want {%q, %q}",
				i, hist[i].Role, hist[i].Content, want.Role, want.Content)
		}
	}
}

func TestManager_ReplaceMessages_Empty(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	sess, _ := m.GetSession(ctx, "cli", "u1")

	m.AddUserMessage(ctx, sess, "old")
	_ = m.AddAssistantMessage(ctx, sess, "old reply")

	if err := m.ReplaceMessages(ctx, sess, nil); err != nil {
		t.Fatalf("ReplaceMessages(nil): %v", err)
	}
	if len(m.GetHistory(sess)) != 0 {
		t.Errorf("history should be empty after replace with nil")
	}

	// Re-populate then replace with an empty (non-nil) slice.
	m.AddUserMessage(ctx, sess, "something")
	if err := m.ReplaceMessages(ctx, sess, []Message{}); err != nil {
		t.Fatalf("ReplaceMessages([]): %v", err)
	}
	if len(m.GetHistory(sess)) != 0 {
		t.Errorf("history should be empty after replace with empty slice")
	}
}

// ── ClearSession ────────────────────────────────────────────────────────────

func TestManager_ClearSession(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	sess, _ := m.GetSession(ctx, "cli", "u1")

	m.AddUserMessage(ctx, sess, "one")
	_ = m.AddAssistantMessage(ctx, sess, "two")
	m.AddUserMessage(ctx, sess, "three")

	if err := m.ClearSession(ctx, sess); err != nil {
		t.Fatalf("ClearSession: %v", err)
	}
	if len(m.GetHistory(sess)) != 0 {
		t.Errorf("history should be empty after clear")
	}

	// Session should still be usable afterwards.
	m.AddUserMessage(ctx, sess, "after clear")
	if len(m.GetHistory(sess)) != 1 {
		t.Errorf("session should accept messages after clear")
	}
}

// ── Recovery: ValidateSession ──────────────────────────────────────────────

func TestValidateSession_Healthy(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "Hi."},
		{Role: RoleAssistant, Content: "Hello!"},
		{Role: RoleUser, Content: "How are you?"},
		{Role: RoleAssistant, Content: "I'm well, thanks."},
	}
	issues := ValidateSession(msgs)
	if len(issues) != 0 {
		t.Errorf("healthy session reported issues: %v", issues)
	}
}

func TestValidateSession_Empty(t *testing.T) {
	if issues := ValidateSession(nil); len(issues) != 0 {
		t.Errorf("empty session reported issues: %v", issues)
	}
}

func TestValidateSession_DanglingToolCall(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "run the tool"},
		{Role: RoleAssistant, Content: `calling <tool_use name="x"/>`},
		// No tool_result follows.
	}
	issues := ValidateSession(msgs)
	if len(issues) == 0 {
		t.Fatal("expected dangling tool call issue, got none")
	}
	found := false
	for _, i := range issues {
		if contains(i, "dangling tool call") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("issues = %v; expected one mentioning 'dangling tool call'", issues)
	}
}

func TestValidateSession_EmptyRole(t *testing.T) {
	msgs := []Message{
		{Role: "", Content: "oops"},
	}
	issues := ValidateSession(msgs)
	if len(issues) == 0 {
		t.Error("expected issue for empty role")
	}
}

// ── Recovery: RepairDanglingToolCalls ──────────────────────────────────────

func TestRepairDanglingToolCalls_RemovesUnanswered(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "please"},
		{Role: RoleAssistant, Content: `<tool_use name="search"/>`},
		// Dangling: no tool_result.
	}
	out, removed := RepairDanglingToolCalls(msgs)
	if removed != 1 {
		t.Errorf("removed = %d; want 1", removed)
	}
	if len(out) != 1 {
		t.Fatalf("out len = %d; want 1", len(out))
	}
	if out[0].Role != RoleUser {
		t.Errorf("remaining msg role = %q; want user", out[0].Role)
	}
}

func TestRepairDanglingToolCalls_KeepsAnswered(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "go"},
		{Role: RoleAssistant, Content: `<tool_use name="x"/>`},
		{Role: RoleUser, Content: `<tool_result>done</tool_result>`},
		{Role: RoleAssistant, Content: "all set."},
	}
	out, removed := RepairDanglingToolCalls(msgs)
	if removed != 0 {
		t.Errorf("removed = %d; want 0", removed)
	}
	if len(out) != len(msgs) {
		t.Errorf("out len = %d; want %d", len(out), len(msgs))
	}
}

// ── Recovery: RepairIncompleteStreaming ────────────────────────────────────

func TestRepairIncompleteStreaming_DropsTrailingIncomplete(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "tell me a story"},
		// Long content, no terminal punctuation → flagged as incomplete.
		{Role: RoleAssistant, Content: "once upon a time there was a little"},
	}
	out, removed := RepairIncompleteStreaming(msgs)
	if !removed {
		t.Fatal("expected removed = true")
	}
	if len(out) != 1 {
		t.Fatalf("out len = %d; want 1", len(out))
	}
	if out[0].Role != RoleUser {
		t.Errorf("remaining role = %q; want user", out[0].Role)
	}
}

func TestRepairIncompleteStreaming_KeepsComplete(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "hi"},
		{Role: RoleAssistant, Content: "Hello there, how can I help you today?"},
	}
	out, removed := RepairIncompleteStreaming(msgs)
	if removed {
		t.Error("expected removed = false for a complete assistant message")
	}
	if len(out) != 2 {
		t.Errorf("out len = %d; want 2", len(out))
	}
}

func TestRepairIncompleteStreaming_EmptyList(t *testing.T) {
	out, removed := RepairIncompleteStreaming(nil)
	if removed {
		t.Error("expected removed = false for empty list")
	}
	if len(out) != 0 {
		t.Errorf("out len = %d; want 0", len(out))
	}
}

// ── Concurrent access ──────────────────────────────────────────────────────

func TestManager_ConcurrentGetSessionAndAdd(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	const goroutines = 20
	const perGoroutine = 10

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			// Each goroutine uses its own user so writes to sess.Messages
			// don't race against concurrent readers on the same session.
			sess, err := m.GetSession(ctx, "cli", userFor(id))
			if err != nil {
				t.Errorf("GetSession: %v", err)
				return
			}
			for i := 0; i < perGoroutine; i++ {
				m.AddUserMessage(ctx, sess, "msg")
			}
		}(g)
	}
	wg.Wait()

	sessions := m.ListSessions()
	if len(sessions) != goroutines {
		t.Errorf("ListSessions len = %d; want %d", len(sessions), goroutines)
	}
	for _, s := range sessions {
		if len(s.Messages) != perGoroutine {
			t.Errorf("session %s has %d messages; want %d", s.ID, len(s.Messages), perGoroutine)
		}
	}
}

func TestManager_ListSessions(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	if got := m.ListSessions(); len(got) != 0 {
		t.Errorf("ListSessions on empty manager = %d; want 0", len(got))
	}

	_, _ = m.GetSession(ctx, "cli", "a")
	_, _ = m.GetSession(ctx, "cli", "b")
	_, _ = m.GetSession(ctx, "discord", "a")

	if got := m.ListSessions(); len(got) != 3 {
		t.Errorf("ListSessions = %d; want 3", len(got))
	}
}

// ── helpers ────────────────────────────────────────────────────────────────

func contains(haystack, needle string) bool {
	return len(needle) == 0 || indexOf(haystack, needle) >= 0
}

func indexOf(s, sub string) int {
	n, m := len(s), len(sub)
	if m == 0 {
		return 0
	}
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == sub {
			return i
		}
	}
	return -1
}

func userFor(i int) string {
	// Deterministic unique user name per goroutine.
	digits := []byte{'u'}
	if i == 0 {
		return "u0"
	}
	var buf []byte
	for i > 0 {
		buf = append([]byte{byte('0' + i%10)}, buf...)
		i /= 10
	}
	return string(append(digits, buf...))
}
