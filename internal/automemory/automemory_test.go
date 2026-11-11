package automemory

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/memory"
)

// --- stubs ---

// stubMemoryStore implements memory.Store so dream tests don't depend on a
// real backend. Only SaveWithMeta is exercised by the service.
type stubMemoryStore struct {
	mu        sync.Mutex
	saved     []memory.Memory
	saveErr   error
	saveCalls int
}

func (s *stubMemoryStore) Save(_ context.Context, _, _ string) error { return nil }

func (s *stubMemoryStore) SaveWithMeta(_ context.Context, mem memory.Memory) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveCalls++
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saved = append(s.saved, mem)
	return nil
}

func (s *stubMemoryStore) Get(_ context.Context, _ string) (*memory.Memory, error) {
	return nil, nil
}
func (s *stubMemoryStore) Search(_ context.Context, _ string) ([]memory.Memory, error) {
	return nil, nil
}
func (s *stubMemoryStore) SearchRelevant(_ context.Context, _ string, _ int, _ float64) ([]memory.Memory, error) {
	return nil, nil
}
func (s *stubMemoryStore) List(_ context.Context) ([]memory.Memory, error) { return nil, nil }
func (s *stubMemoryStore) Delete(_ context.Context, _ string) error        { return nil }

func (s *stubMemoryStore) snapshot() []memory.Memory {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]memory.Memory, len(s.saved))
	copy(out, s.saved)
	return out
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// seededSessionStore returns an in-memory conversation.Store with the given
// messages already attached under a single session.
func seededSessionStore(t *testing.T, msgs []conversation.Message) *conversation.Store {
	t.Helper()
	store := conversation.NewStore(nil, nil, "")
	sess, err := store.Get(context.Background(), "test-channel", "user-1")
	if err != nil {
		t.Fatalf("seed session Get: %v", err)
	}
	for _, m := range msgs {
		sess.AddMessage(m.Role, m.Content)
	}
	if err := store.Save(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	return store
}

func dreamEvidenceForQuote(t *testing.T, transcript, quote string) []MemoryEvidence {
	t.Helper()
	for _, line := range strings.Split(transcript, "\n") {
		if !strings.Contains(line, quote) {
			continue
		}
		start := strings.Index(line, "message=")
		if start < 0 {
			continue
		}
		start += len("message=")
		end := strings.IndexByte(line[start:], ']')
		if end > 0 {
			return []MemoryEvidence{{MessageID: line[start : start+end], Quote: quote}}
		}
	}
	t.Fatalf("quote %q not found in dream transcript", quote)
	return nil
}

// --- Store (NewStore / Add / Save / Load) tests ---

func TestNewStore_CreatesDirectoryAndInitializes(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, quietLogger())
	if s == nil {
		t.Fatal("NewStore returned nil")
	}
	if s.Count() != 0 {
		t.Errorf("expected empty store, got %d facts", s.Count())
	}
	// auto-memory subdir must exist
	info, err := os.Stat(filepath.Join(dir, "auto-memory"))
	if err != nil {
		t.Fatalf("auto-memory dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("auto-memory path is not a directory")
	}
}

func TestStore_AddAndSavePersistsToDisk(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, quietLogger())

	s.Add(Fact{
		Content:  "prefer postgres over sqlite for multi-user",
		Category: "decision",
		Source:   "sess-1",
	})
	if s.Count() != 1 {
		t.Fatalf("expected 1 fact, got %d", s.Count())
	}

	// Save is called by Add, but explicitly invoke too.
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "auto-memory", "facts.json"))
	if err != nil {
		t.Fatalf("read facts.json: %v", err)
	}
	var facts []Fact
	if err := json.Unmarshal(raw, &facts); err != nil {
		t.Fatalf("unmarshal facts.json: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 persisted fact, got %d", len(facts))
	}
	if facts[0].CreatedAt.IsZero() {
		t.Errorf("expected CreatedAt to be auto-populated")
	}
	if facts[0].Category != "decision" {
		t.Errorf("unexpected category %q", facts[0].Category)
	}
}

func TestStore_LoadReadsExistingFacts(t *testing.T) {
	dir := t.TempDir()
	s1 := NewStore(dir, quietLogger())
	s1.Add(Fact{Content: "use tabs for go indentation", Category: "pattern"})
	s1.Add(Fact{Content: "database is on port 5432", Category: "context"})

	// New store over the same dir should load the prior facts.
	s2 := NewStore(dir, quietLogger())
	if s2.Count() != 2 {
		t.Errorf("expected 2 facts after reload, got %d", s2.Count())
	}
	all := s2.All()
	if len(all) != 2 {
		t.Fatalf("All() returned %d facts", len(all))
	}
}

func TestStore_AddDeduplicatesSimilarContent(t *testing.T) {
	s := NewStore(t.TempDir(), quietLogger())
	s.Add(Fact{Content: "use docker compose for local development", Category: "decision"})
	s.Add(Fact{Content: "use docker compose for local development", Category: "decision"})
	// near-identical (one extra word) should still be filtered by similarity.
	s.Add(Fact{Content: "use docker compose for local development please", Category: "decision"})

	if s.Count() != 1 {
		t.Errorf("expected dedup to keep 1 fact, got %d", s.Count())
	}
}

func TestStore_GetRelevantScoresAndLimits(t *testing.T) {
	s := NewStore(t.TempDir(), quietLogger())
	s.Add(Fact{Content: "the database uses postgres", Category: "context", CreatedAt: time.Now()})
	s.Add(Fact{Content: "frontend uses react hooks", Category: "pattern", CreatedAt: time.Now()})
	s.Add(Fact{Content: "deploys go through ci pipeline", Category: "pattern", CreatedAt: time.Now()})

	hits := s.GetRelevant("postgres database config", 5)
	if len(hits) == 0 {
		t.Fatal("expected at least one relevant fact for postgres query")
	}
	if !strings.Contains(strings.ToLower(hits[0].Content), "postgres") {
		t.Errorf("expected top hit to mention postgres, got %q", hits[0].Content)
	}

	// maxResults respected.
	hits = s.GetRelevant("uses", 1)
	if len(hits) > 1 {
		t.Errorf("expected at most 1 hit, got %d", len(hits))
	}

	// Empty on no match.
	hits = s.GetRelevant("xyz-nothing-matches-here", 5)
	if len(hits) != 0 {
		t.Errorf("expected zero hits, got %d", len(hits))
	}
}

func TestStore_InjectContextFormatsFacts(t *testing.T) {
	s := NewStore(t.TempDir(), quietLogger())
	// Empty store → empty injection.
	if got := s.InjectContext("anything"); got != "" {
		t.Errorf("expected empty context on empty store, got %q", got)
	}

	s.Add(Fact{Content: "api key lives in env var API_TOKEN", Category: "context", CreatedAt: time.Now()})
	got := s.InjectContext("api key")
	if !strings.Contains(got, "[Auto-memoria]") {
		t.Errorf("expected header in context, got %q", got)
	}
	if !strings.Contains(got, "[context]") {
		t.Errorf("expected category tag in context, got %q", got)
	}
	if !strings.Contains(got, "api key") {
		t.Errorf("expected content in context, got %q", got)
	}
}

// --- ExtractFromConversation tests (extraction from messages) ---

func TestExtractFromConversation_ExtractsDecisionsAndPatterns(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "qual banco usamos?"},
		{Role: "assistant", Content: "Para esse caso vou usar postgres porque suporta jsonb nativamente."},
		{Role: "assistant", Content: "Lembre-se: o padrao e sempre rodar os testes antes de commitar."},
	}

	facts := ExtractFromConversation(context.Background(), msgs)
	if len(facts) == 0 {
		t.Fatal("expected at least one extracted fact")
	}

	var gotDecision, gotPattern bool
	for _, f := range facts {
		switch f.Category {
		case "decision":
			gotDecision = true
		case "pattern":
			gotPattern = true
		}
	}
	if !gotDecision {
		t.Errorf("expected a decision fact, got %+v", facts)
	}
	if !gotPattern {
		t.Errorf("expected a pattern fact, got %+v", facts)
	}
}

func TestExtractFromConversation_IgnoresUserAndCapsAtThree(t *testing.T) {
	// User messages must be ignored.
	userOnly := []Message{
		{Role: "user", Content: "vou usar mongo mas nao decidi ainda"},
	}
	if got := ExtractFromConversation(context.Background(), userOnly); len(got) != 0 {
		t.Errorf("user messages should yield no facts, got %d", len(got))
	}

	// Many assistant sentences → capped at 3.
	big := []Message{
		{Role: "assistant", Content: "vou usar postgres para persistencia principal."},
		{Role: "assistant", Content: "optei por redis para cache em memoria."},
		{Role: "assistant", Content: "decidimos que o frontend sera em react."},
		{Role: "assistant", Content: "o padrao e sempre rodar lint antes do commit."},
		{Role: "assistant", Content: "a convencao e nomes em snake case para sql."},
	}
	facts := ExtractFromConversation(context.Background(), big)
	if len(facts) > 3 {
		t.Errorf("expected at most 3 facts, got %d", len(facts))
	}
}

// --- DreamService tests ---

func TestNewDreamService_FillsDefaults(t *testing.T) {
	d := NewDreamService(DreamConfig{})
	if d == nil {
		t.Fatal("NewDreamService returned nil")
	}
	if d.config.IdleThreshold != 5*time.Minute {
		t.Errorf("IdleThreshold default wrong: %v", d.config.IdleThreshold)
	}
	if d.config.Interval != 10*time.Minute {
		t.Errorf("Interval default wrong: %v", d.config.Interval)
	}
	if d.config.MaxMessages != 50 {
		t.Errorf("MaxMessages default wrong: %d", d.config.MaxMessages)
	}
	if d.config.Logger == nil {
		t.Errorf("Logger default must not be nil")
	}
}

func TestNewDreamService_RespectsExplicitValues(t *testing.T) {
	custom := DreamConfig{
		Logger:        quietLogger(),
		IdleThreshold: 2 * time.Second,
		Interval:      3 * time.Second,
		MaxMessages:   7,
	}
	d := NewDreamService(custom)
	if d.config.IdleThreshold != 2*time.Second {
		t.Errorf("explicit IdleThreshold not preserved")
	}
	if d.config.Interval != 3*time.Second {
		t.Errorf("explicit Interval not preserved")
	}
	if d.config.MaxMessages != 7 {
		t.Errorf("explicit MaxMessages not preserved")
	}
}

func TestDreamService_StatsEmptyInitially(t *testing.T) {
	d := NewDreamService(DreamConfig{Logger: quietLogger()})
	s := d.Stats()
	if s.Runs != 0 || s.MemoriesAdded != 0 || s.MessagesProcessed != 0 {
		t.Errorf("expected zeroed stats, got %+v", s)
	}
	if s.LastError != "" {
		t.Errorf("expected empty LastError, got %q", s.LastError)
	}
	if !s.LastRunTime.IsZero() {
		t.Errorf("expected zero LastRunTime, got %v", s.LastRunTime)
	}
}

func TestDreamService_RunNow_MissingExtractFnRecordsError(t *testing.T) {
	d := NewDreamService(DreamConfig{
		Logger:       quietLogger(),
		SessionStore: conversation.NewStore(nil, nil, ""),
		Memory:       &stubMemoryStore{},
	})
	err := d.RunNow(context.Background())
	if err == nil {
		t.Fatal("expected error when ExtractFn is not configured")
	}
	s := d.Stats()
	if s.Runs != 1 {
		t.Errorf("expected Runs=1, got %d", s.Runs)
	}
	if s.LastError == "" {
		t.Errorf("expected LastError to be set")
	}
}

func TestDreamService_RunNow_MissingSessionStore(t *testing.T) {
	d := NewDreamService(DreamConfig{
		Logger: quietLogger(),
		Memory: &stubMemoryStore{},
		ExtractFn: func(_ context.Context, _ string) ([]ExtractedMemory, error) {
			return nil, nil
		},
	})
	err := d.RunNow(context.Background())
	if err == nil {
		t.Fatal("expected error when SessionStore is not configured")
	}
	if !strings.Contains(d.Stats().LastError, "SessionStore") {
		t.Errorf("expected SessionStore error, got %q", d.Stats().LastError)
	}
}

func TestDreamService_RunNow_EmptySessionsIsNoop(t *testing.T) {
	called := false
	d := NewDreamService(DreamConfig{
		Logger:       quietLogger(),
		SessionStore: conversation.NewStore(nil, nil, ""),
		Memory:       &stubMemoryStore{},
		ExtractFn: func(_ context.Context, _ string) ([]ExtractedMemory, error) {
			called = true
			return nil, nil
		},
	})
	if err := d.RunNow(context.Background()); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	if called {
		t.Errorf("ExtractFn should not be called when there are no messages")
	}
	s := d.Stats()
	if s.Runs != 1 {
		t.Errorf("expected Runs=1, got %d", s.Runs)
	}
	if s.MessagesProcessed != 0 {
		t.Errorf("expected 0 messages processed, got %d", s.MessagesProcessed)
	}
}

func TestDreamService_RunNow_SavesExtractedMemories(t *testing.T) {
	msgs := []conversation.Message{
		{Role: conversation.RoleUser, Content: "uso postgres", Timestamp: time.Now().Add(-2 * time.Minute)},
		{Role: conversation.RoleUser, Content: "prefiro go em vez de rust", Timestamp: time.Now().Add(-90 * time.Second)},
		{Role: conversation.RoleAssistant, Content: "vou usar postgres", Timestamp: time.Now().Add(-1 * time.Minute)},
	}
	sessStore := seededSessionStore(t, msgs)
	memStore := &stubMemoryStore{}

	var transcriptSeen string
	extract := func(_ context.Context, transcript string) ([]ExtractedMemory, error) {
		transcriptSeen = transcript
		return []ExtractedMemory{
			{Type: "fact", Content: "uses postgres", Tags: []string{"db"}, Importance: 5,
				Evidence: dreamEvidenceForQuote(t, transcript, "uso postgres")},
			{Type: "preference", Content: "prefers go over rust", Tags: []string{"lang"}, Importance: 3,
				Evidence: dreamEvidenceForQuote(t, transcript, "prefiro go em vez de rust")},
			{Type: "", Content: "   ", Importance: 1}, // should be skipped (blank content)
		}, nil
	}

	d := NewDreamService(DreamConfig{
		Logger:       quietLogger(),
		SessionStore: sessStore,
		Memory:       memStore,
		ExtractFn:    extract,
		MaxMessages:  10,
	})

	if err := d.RunNow(context.Background()); err != nil {
		t.Fatalf("RunNow: %v", err)
	}

	if transcriptSeen == "" {
		t.Errorf("expected transcript to be passed to ExtractFn")
	}
	if !strings.Contains(transcriptSeen, "postgres") {
		t.Errorf("transcript missing expected content: %q", transcriptSeen)
	}

	saved := memStore.snapshot()
	if len(saved) != 2 {
		t.Fatalf("expected 2 memories saved (blank skipped), got %d", len(saved))
	}

	// Importance controls priority, while exact user quotes establish provenance.
	var gotFact, gotPref bool
	for _, m := range saved {
		switch {
		case strings.HasPrefix(m.Topic, "dream_fact_"):
			gotFact = true
			if m.Priority != 2 { // importance 5 → critical (2)
				t.Errorf("importance 5 should map to priority 2, got %d", m.Priority)
			}
			if m.Confidence != 0.75 {
				t.Errorf("expected quoted confidence, got %v", m.Confidence)
			}
		case strings.HasPrefix(m.Topic, "dream_preference_"):
			gotPref = true
			if m.Priority != 1 { // importance 3 → high (1)
				t.Errorf("importance 3 should map to priority 1, got %d", m.Priority)
			}
		}
		if m.Source != "dream" {
			t.Errorf("expected source 'dream', got %q", m.Source)
		}
	}
	if !gotFact || !gotPref {
		t.Errorf("missing expected topics in saved memories: %+v", saved)
	}

	stats := d.Stats()
	if stats.Runs != 1 {
		t.Errorf("expected Runs=1, got %d", stats.Runs)
	}
	if stats.MemoriesAdded != 2 {
		t.Errorf("expected MemoriesAdded=2, got %d", stats.MemoriesAdded)
	}
	if stats.MessagesProcessed != 2 {
		t.Errorf("expected MessagesProcessed=2, got %d", stats.MessagesProcessed)
	}
	if stats.LastError != "" {
		t.Errorf("expected empty LastError, got %q", stats.LastError)
	}
	if stats.LastRunTime.IsZero() {
		t.Errorf("expected LastRunTime to be set")
	}
}

func TestDreamService_RunNow_ExtractFnErrorRecorded(t *testing.T) {
	msgs := []conversation.Message{
		{Role: conversation.RoleUser, Content: "hello"},
	}
	d := NewDreamService(DreamConfig{
		Logger:       quietLogger(),
		SessionStore: seededSessionStore(t, msgs),
		Memory:       &stubMemoryStore{},
		ExtractFn: func(_ context.Context, _ string) ([]ExtractedMemory, error) {
			return nil, errors.New("llm timeout")
		},
	})
	err := d.RunNow(context.Background())
	if err == nil {
		t.Fatal("expected error from ExtractFn propagation")
	}
	if !strings.Contains(err.Error(), "llm timeout") {
		t.Errorf("expected wrapped error, got %v", err)
	}
	if !strings.Contains(d.Stats().LastError, "llm timeout") {
		t.Errorf("expected LastError to record failure, got %q", d.Stats().LastError)
	}
}

func TestDreamService_MarkActivityResetsIdle(t *testing.T) {
	d := NewDreamService(DreamConfig{
		Logger:        quietLogger(),
		IdleThreshold: 100 * time.Millisecond,
	})
	// Force lastActivity to the past to simulate idle state.
	d.mu.Lock()
	d.lastActivity = time.Now().Add(-1 * time.Hour)
	d.mu.Unlock()

	d.MarkActivity()

	d.mu.Lock()
	idle := time.Since(d.lastActivity)
	d.mu.Unlock()

	if idle > 100*time.Millisecond {
		t.Errorf("MarkActivity should reset idle clock, still %v", idle)
	}
}

func TestDreamService_StopWithoutStartIsSafe(t *testing.T) {
	d := NewDreamService(DreamConfig{Logger: quietLogger()})
	// Should not block or panic when Stop is called before Start.
	done := make(chan struct{})
	go func() {
		d.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Stop blocked on unstarted service")
	}
}

func TestDreamService_StartThenStopCleanShutdown(t *testing.T) {
	d := NewDreamService(DreamConfig{
		Logger:        quietLogger(),
		IdleThreshold: 50 * time.Millisecond,
		Interval:      50 * time.Millisecond,
		SessionStore:  conversation.NewStore(nil, nil, ""),
		Memory:        &stubMemoryStore{},
		ExtractFn: func(_ context.Context, _ string) ([]ExtractedMemory, error) {
			return nil, nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d.Start(ctx)
	// Give the goroutine a moment to enter its select.
	time.Sleep(20 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		d.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return within timeout")
	}
}

// --- categorization / filtering tests for extracted memories ---

func TestDreamService_TopicForCategorization(t *testing.T) {
	d := NewDreamService(DreamConfig{Logger: quietLogger()})
	cases := map[string]string{
		"fact":       "dream_fact_",
		"preference": "dream_preference_",
		"skill":      "dream_skill_",
		"context":    "dream_context_",
		"":           "dream_context_", // empty defaults to context
		"  ":         "dream_context_",
	}
	for in, want := range cases {
		got := d.topicFor(ExtractedMemory{Type: in})
		if !strings.HasPrefix(got, want) {
			t.Errorf("topicFor(%q)=%q, want prefix %q", in, got, want)
		}
	}
}

func TestDreamService_StatePreventsReprocessing(t *testing.T) {
	now := time.Now().Add(-time.Minute)
	store := seededSessionStore(t, []conversation.Message{{
		Role: conversation.RoleUser, Content: "prefiro respostas curtas", Timestamp: now,
	}})
	stateFile := filepath.Join(t.TempDir(), "dream-state.json")
	calls := 0
	extract := func(_ context.Context, transcript string) ([]ExtractedMemory, error) {
		calls++
		return []ExtractedMemory{{Type: "preference", Content: "prefere respostas curtas", Importance: 4,
			Evidence: dreamEvidenceForQuote(t, transcript, "prefiro respostas curtas")}}, nil
	}

	d1 := NewDreamService(DreamConfig{
		Logger: quietLogger(), SessionStore: store, Memory: &stubMemoryStore{},
		ExtractFn: extract, StateFile: stateFile,
	})
	if err := d1.RunNow(context.Background()); err != nil {
		t.Fatalf("first RunNow: %v", err)
	}
	d2 := NewDreamService(DreamConfig{
		Logger: quietLogger(), SessionStore: store, Memory: &stubMemoryStore{},
		ExtractFn: extract, StateFile: stateFile,
	})
	if err := d2.RunNow(context.Background()); err != nil {
		t.Fatalf("second RunNow: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected one extraction across restart, got %d", calls)
	}
}

func TestNormalisePriorityImportanceMapping(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{5, 2}, {6, 2},
		{4, 1}, {3, 1},
		{2, 0}, {1, 0}, {0, 0}, {-1, 0},
	}
	for _, c := range cases {
		got := normalisePriority(c.in)
		if got != c.want {
			t.Errorf("normalisePriority(%d)=%d, want %d", c.in, got, c.want)
		}
	}
}
