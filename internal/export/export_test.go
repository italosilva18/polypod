package export

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleSession() Session {
	return Session{
		ID:        "sess-123",
		Title:     "Test Session",
		Model:     "claude-opus-4",
		CreatedAt: time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
		Cost:      0.1234,
		Tokens:    4567,
		Messages: []Message{
			{Role: "user", Content: "Hello, world!"},
			{Role: "assistant", Content: "Hi there! How can I help?"},
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "tool", Content: "tool output line 1\ntool output line 2", ToolName: "bash"},
		},
	}
}

func TestToMarkdown_ContainsTitleAndMeta(t *testing.T) {
	s := sampleSession()
	md := ToMarkdown(s)

	if !strings.Contains(md, "# Test Session") {
		t.Errorf("markdown missing title header, got:\n%s", md)
	}
	if !strings.Contains(md, "sess-123") {
		t.Error("markdown missing session ID")
	}
	if !strings.Contains(md, "claude-opus-4") {
		t.Error("markdown missing model")
	}
	if !strings.Contains(md, "2025-01-15 10:30") {
		t.Error("markdown missing formatted date")
	}
	if !strings.Contains(md, "$0.1234") {
		t.Errorf("markdown missing cost, got:\n%s", md)
	}
	if !strings.Contains(md, "4567") {
		t.Error("markdown missing token count")
	}
}

func TestToMarkdown_RoleHandling(t *testing.T) {
	s := sampleSession()
	md := ToMarkdown(s)

	if !strings.Contains(md, "## User") {
		t.Error("markdown missing user heading")
	}
	if !strings.Contains(md, "Hello, world!") {
		t.Error("markdown missing user content")
	}
	if !strings.Contains(md, "## Assistant") {
		t.Error("markdown missing assistant heading")
	}
	if !strings.Contains(md, "Hi there! How can I help?") {
		t.Error("markdown missing assistant content")
	}
	// System messages should be skipped
	if strings.Contains(md, "You are a helpful assistant.") {
		t.Error("markdown should skip system messages")
	}
	// Tool should have tool name and blockquote
	if !strings.Contains(md, "**Tool**: bash") {
		t.Error("markdown missing tool name")
	}
	if !strings.Contains(md, "> ```") {
		t.Error("markdown missing tool code fence")
	}
}

func TestToMarkdown_NoCostWhenZero(t *testing.T) {
	s := sampleSession()
	s.Cost = 0
	s.Tokens = 0
	md := ToMarkdown(s)

	if strings.Contains(md, "**Cost**") {
		t.Errorf("markdown should omit cost line when zero, got:\n%s", md)
	}
}

func TestToJSON_ValidAndRoundtrip(t *testing.T) {
	s := sampleSession()
	out, err := ToJSON(s)
	if err != nil {
		t.Fatalf("ToJSON returned error: %v", err)
	}
	if out == "" {
		t.Fatal("ToJSON returned empty string")
	}

	// Verify it's valid JSON and contains expected fields
	var decoded Session
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("ToJSON output is not valid JSON: %v", err)
	}
	if decoded.ID != s.ID {
		t.Errorf("ID mismatch after roundtrip: got %q, want %q", decoded.ID, s.ID)
	}
	if decoded.Title != s.Title {
		t.Errorf("Title mismatch: got %q, want %q", decoded.Title, s.Title)
	}
	if len(decoded.Messages) != len(s.Messages) {
		t.Errorf("Messages count mismatch: got %d, want %d", len(decoded.Messages), len(s.Messages))
	}
	if decoded.Cost != s.Cost {
		t.Errorf("Cost mismatch: got %v, want %v", decoded.Cost, s.Cost)
	}
}

func TestToJSON_Indented(t *testing.T) {
	s := sampleSession()
	out, _ := ToJSON(s)
	// Indented JSON should contain newlines and two-space indent
	if !strings.Contains(out, "\n  ") {
		t.Errorf("ToJSON output should be indented, got:\n%s", out)
	}
}

func TestToHTML_StructureAndContent(t *testing.T) {
	s := sampleSession()
	html := ToHTML(s)

	if !strings.HasPrefix(html, "<!DOCTYPE html>") {
		t.Error("HTML should start with doctype")
	}
	if !strings.Contains(html, "<title>Test Session</title>") {
		t.Error("HTML missing title tag")
	}
	if !strings.Contains(html, "<h1>Test Session</h1>") {
		t.Error("HTML missing h1 heading")
	}
	if !strings.Contains(html, "sess-123") {
		t.Error("HTML missing session ID")
	}
	if !strings.Contains(html, `class="msg user"`) {
		t.Error("HTML missing user message class")
	}
	if !strings.Contains(html, `class="msg assistant"`) {
		t.Error("HTML missing assistant message class")
	}
	if !strings.HasSuffix(html, "</body></html>") {
		t.Error("HTML should end with closing tags")
	}
	// System messages should be skipped
	if strings.Contains(html, "You are a helpful assistant.") {
		t.Error("HTML should skip system messages")
	}
}

func TestToHTML_EscapesSpecialChars(t *testing.T) {
	s := Session{
		ID:        "id1",
		Title:     `Bug <script>alert("xss")</script>`,
		Model:     "m",
		CreatedAt: time.Now(),
		Messages: []Message{
			{Role: "user", Content: "a < b & c > d"},
		},
	}
	html := ToHTML(s)

	if strings.Contains(html, "<script>alert") {
		t.Error("HTML should escape <script> tag in title")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Error("HTML should contain escaped script tag")
	}
	if !strings.Contains(html, "a &lt; b") {
		t.Error("HTML should escape < in message content")
	}
}

func TestToNotebook_ValidJSONStructure(t *testing.T) {
	s := sampleSession()
	out, err := ToNotebook(s)
	if err != nil {
		t.Fatalf("ToNotebook error: %v", err)
	}

	var nb map[string]any
	if err := json.Unmarshal([]byte(out), &nb); err != nil {
		t.Fatalf("notebook output is not valid JSON: %v", err)
	}

	if v, ok := nb["nbformat"].(float64); !ok || v != 4 {
		t.Errorf("notebook nbformat should be 4, got %v", nb["nbformat"])
	}
	if _, ok := nb["metadata"].(map[string]any); !ok {
		t.Error("notebook missing metadata")
	}

	cells, ok := nb["cells"].([]any)
	if !ok {
		t.Fatal("notebook missing cells array")
	}
	// 4 messages minus 1 system = 3 cells
	if len(cells) != 3 {
		t.Errorf("expected 3 cells, got %d", len(cells))
	}

	// Verify tool cell is of type "code"
	foundCodeCell := false
	for _, c := range cells {
		cell := c.(map[string]any)
		if cell["cell_type"] == "code" {
			foundCodeCell = true
		}
	}
	if !foundCodeCell {
		t.Error("expected at least one code cell for tool message")
	}
}

func TestToNotebook_EmptySession(t *testing.T) {
	s := Session{
		ID:        "empty",
		Title:     "Empty",
		Model:     "m",
		CreatedAt: time.Now(),
		Messages:  []Message{},
	}
	out, err := ToNotebook(s)
	if err != nil {
		t.Fatalf("ToNotebook error on empty: %v", err)
	}

	var nb map[string]any
	if err := json.Unmarshal([]byte(out), &nb); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	// cells can be nil/empty or absent
	if cells, ok := nb["cells"].([]any); ok && len(cells) != 0 {
		t.Errorf("expected empty cells, got %d", len(cells))
	}
}

func TestWriteFile_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	content := "hello world\nmultiple lines"

	if err := WriteFile(path, content); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read back file: %v", err)
	}
	if string(data) != content {
		t.Errorf("file content mismatch: got %q, want %q", string(data), content)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat error: %v", err)
	}
	// Check permissions (mask to ignore umask differences on some systems)
	if info.Mode().Perm()&0600 != 0600 {
		t.Errorf("file should be readable/writable by owner, got %v", info.Mode().Perm())
	}
}

func TestWriteFile_ErrorOnBadPath(t *testing.T) {
	// Attempt to write to a path that cannot exist
	err := WriteFile("/nonexistent-dir-xyz-12345/file.txt", "data")
	if err == nil {
		t.Error("expected error writing to nonexistent directory")
	}
}

func TestToMarkdown_EmptyMessages(t *testing.T) {
	s := Session{
		ID:        "e1",
		Title:     "Empty Session",
		Model:     "m",
		CreatedAt: time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC),
		Messages:  []Message{},
	}
	md := ToMarkdown(s)
	if !strings.Contains(md, "# Empty Session") {
		t.Error("markdown should still contain title for empty session")
	}
	if !strings.Contains(md, "---") {
		t.Error("markdown should contain separator")
	}
}
