package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile is a small helper to create a file with content inside a directory.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestSearchSessions_FindsMatchInDataDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "session1.json",
		`{"role":"user","content":"hello world"}`+"\n"+
			`{"role":"assistant","content":"goodbye"}`)

	results := SearchSessions(dir, "hello", 10)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %+v", len(results), results)
	}

	r := results[0]
	if r.SessionID != "session1" {
		t.Errorf("expected SessionID 'session1', got %q", r.SessionID)
	}
	if r.Role != "user" {
		t.Errorf("expected Role 'user', got %q", r.Role)
	}
	if r.Line != 1 {
		t.Errorf("expected Line 1, got %d", r.Line)
	}
	if !strings.Contains(strings.ToLower(r.Content), "hello") {
		t.Errorf("expected snippet to contain 'hello', got %q", r.Content)
	}
}

func TestSearchSessions_CaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "s.json",
		`{"role":"user","content":"Kubernetes Pod"}`)

	results := SearchSessions(dir, "KUBERNETES", 10)
	if len(results) != 1 {
		t.Fatalf("expected 1 case-insensitive match, got %d", len(results))
	}

	results = SearchSessions(dir, "kubernetes", 10)
	if len(results) != 1 {
		t.Fatalf("expected 1 lowercase match, got %d", len(results))
	}
}

func TestSearchSessions_IgnoresNonJSONFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "notes.txt", `{"role":"user","content":"findme"}`)
	writeFile(t, dir, "readme.md", `findme`)
	writeFile(t, dir, "s.json", `{"role":"user","content":"findme"}`)

	results := SearchSessions(dir, "findme", 10)
	if len(results) != 1 {
		t.Fatalf("expected only 1 result from .json file, got %d: %+v", len(results), results)
	}
	if results[0].SessionID != "s" {
		t.Errorf("expected SessionID 's', got %q", results[0].SessionID)
	}
}

func TestSearchSessions_SearchesSessionsSubdir(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	writeFile(t, sessionsDir, "deep.json",
		`{"role":"assistant","content":"nested match target"}`)
	// Also make sure index.json is skipped even if it contains the query.
	writeFile(t, sessionsDir, "index.json",
		`{"role":"assistant","content":"nested match target"}`)

	results := SearchSessions(dir, "nested match", 10)
	if len(results) != 1 {
		t.Fatalf("expected 1 result from sessions subdir (index.json excluded), got %d: %+v", len(results), results)
	}
	if results[0].SessionID != "deep" {
		t.Errorf("expected SessionID 'deep', got %q", results[0].SessionID)
	}
}

func TestSearchSessions_RespectsMaxResults(t *testing.T) {
	dir := t.TempDir()
	// Create several json files each with matching content.
	for i := 0; i < 5; i++ {
		name := "s" + string(rune('0'+i)) + ".json"
		writeFile(t, dir, name, `{"role":"user","content":"match"}`)
	}

	results := SearchSessions(dir, "match", 2)
	if len(results) > 2 {
		t.Fatalf("expected at most 2 results, got %d", len(results))
	}
	if len(results) == 0 {
		t.Fatalf("expected results to be truncated to >=1 but not 0")
	}
}

func TestSearchSessions_MaxResultsZeroDefaults(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.json", `{"role":"user","content":"hello"}`)

	// When maxResults == 0 it should default to 20; 1 match should still be returned.
	results := SearchSessions(dir, "hello", 0)
	if len(results) != 1 {
		t.Fatalf("expected 1 result with default max, got %d", len(results))
	}
}

func TestSearchSessions_NoMatchReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.json", `{"role":"user","content":"something else"}`)

	results := SearchSessions(dir, "absent-query", 10)
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d: %+v", len(results), results)
	}
}

func TestSearchSessions_MissingDataDirReturnsNil(t *testing.T) {
	results := SearchSessions(filepath.Join(os.TempDir(), "definitely-not-a-real-dir-polypod-xyz-12345"), "q", 10)
	if results != nil {
		t.Fatalf("expected nil for missing dataDir, got %+v", results)
	}
}

func TestSearchSessions_MultipleLinesInSameFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "multi.json",
		`{"role":"user","content":"alpha"}`+"\n"+
			`{"role":"assistant","content":"beta"}`+"\n"+
			`{"role":"user","content":"alpha again"}`)

	results := SearchSessions(dir, "alpha", 10)
	if len(results) != 2 {
		t.Fatalf("expected 2 matching lines, got %d: %+v", len(results), results)
	}

	if results[0].Line != 1 || results[1].Line != 3 {
		t.Errorf("expected lines 1 and 3, got %d and %d", results[0].Line, results[1].Line)
	}
	if results[0].Role != "user" || results[1].Role != "user" {
		t.Errorf("expected both roles 'user', got %q and %q", results[0].Role, results[1].Role)
	}
}

func TestFormatResults_EmptyAndNonEmpty(t *testing.T) {
	empty := FormatResults(nil, "xyz")
	if !strings.Contains(empty, "Nenhum resultado") || !strings.Contains(empty, "xyz") {
		t.Errorf("expected 'Nenhum resultado' message with query, got %q", empty)
	}

	results := []SessionSearchResult{
		{SessionID: "sess1", Line: 7, Role: "user", Content: "hello"},
		{SessionID: "sess2", Line: 2, Role: "", Content: "world"},
	}
	out := FormatResults(results, "hello")
	if !strings.Contains(out, "Busca: 'hello'") {
		t.Errorf("expected header with query, got %q", out)
	}
	if !strings.Contains(out, "2 resultados") {
		t.Errorf("expected count '2 resultados', got %q", out)
	}
	if !strings.Contains(out, "sess1") || !strings.Contains(out, "sess2") {
		t.Errorf("expected both session ids in output, got %q", out)
	}
	if !strings.Contains(out, "linha 7") {
		t.Errorf("expected 'linha 7' in output, got %q", out)
	}
	// Empty role should be rendered as '?'.
	if !strings.Contains(out, "[?]") {
		t.Errorf("expected empty role to render as '[?]', got %q", out)
	}
}

func TestSearchCurrent(t *testing.T) {
	messages := []struct{ Role, Content string }{
		{Role: "user", Content: "Hello World"},
		{Role: "assistant", Content: "goodbye"},
		{Role: "user", Content: "hello again"},
		{Role: "assistant", Content: "unrelated"},
	}

	indices := SearchCurrent(messages, "hello")
	if len(indices) != 2 {
		t.Fatalf("expected 2 matches, got %d: %v", len(indices), indices)
	}
	if indices[0] != 0 || indices[1] != 2 {
		t.Errorf("expected indices [0,2], got %v", indices)
	}

	// No matches.
	if got := SearchCurrent(messages, "nope"); len(got) != 0 {
		t.Errorf("expected no matches, got %v", got)
	}

	// Empty messages.
	if got := SearchCurrent(nil, "hello"); len(got) != 0 {
		t.Errorf("expected no matches on nil input, got %v", got)
	}
}

func TestExtractSnippet(t *testing.T) {
	// Query in middle: should get context on both sides with ellipses.
	text := strings.Repeat("a", 200) + "needle" + strings.Repeat("b", 200)
	s := extractSnippet(text, "needle", 10)
	if !strings.Contains(s, "needle") {
		t.Errorf("expected snippet to contain 'needle', got %q", s)
	}
	if !strings.HasPrefix(s, "...") || !strings.HasSuffix(s, "...") {
		t.Errorf("expected snippet to have ellipses on both sides, got %q", s)
	}

	// Query at start: no leading ellipsis.
	s2 := extractSnippet("needle tail content here", "needle", 5)
	if strings.HasPrefix(s2, "...") {
		t.Errorf("did not expect leading ellipsis, got %q", s2)
	}

	// Query not found: returns original (short) text.
	s3 := extractSnippet("short text", "absent", 100)
	if s3 != "short text" {
		t.Errorf("expected 'short text', got %q", s3)
	}

	// Query not found and text longer than 2*contextLen: gets truncated with suffix.
	long := strings.Repeat("x", 50)
	s4 := extractSnippet(long, "absent", 10)
	if !strings.HasSuffix(s4, "...") {
		t.Errorf("expected trailing ellipsis for long text without match, got %q", s4)
	}
}
