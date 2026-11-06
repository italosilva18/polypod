package lsp

import (
	"encoding/json"
	"strings"
	"testing"
)

// --- Protocol Type Roundtrips ---

func TestPosition_Roundtrip(t *testing.T) {
	p := Position{Line: 10, Character: 5}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var p2 Position
	if err := json.Unmarshal(data, &p2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p2.Line != p.Line || p2.Character != p.Character {
		t.Errorf("roundtrip mismatch: got %+v want %+v", p2, p)
	}

	// Verify JSON field names match LSP spec (lowercase).
	s := string(data)
	if !strings.Contains(s, `"line":10`) || !strings.Contains(s, `"character":5`) {
		t.Errorf("unexpected JSON: %s", s)
	}
}

func TestRange_Roundtrip(t *testing.T) {
	r := Range{
		Start: Position{Line: 1, Character: 2},
		End:   Position{Line: 3, Character: 4},
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var r2 Range
	if err := json.Unmarshal(data, &r2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r2 != r {
		t.Errorf("roundtrip mismatch: got %+v want %+v", r2, r)
	}
}

func TestLocation_Roundtrip(t *testing.T) {
	loc := Location{
		URI: "file:///tmp/foo.go",
		Range: Range{
			Start: Position{Line: 0, Character: 0},
			End:   Position{Line: 0, Character: 10},
		},
	}
	data, err := json.Marshal(loc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var loc2 Location
	if err := json.Unmarshal(data, &loc2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if loc2 != loc {
		t.Errorf("roundtrip mismatch: got %+v want %+v", loc2, loc)
	}
}

func TestTextDocumentItem_Roundtrip(t *testing.T) {
	item := TextDocumentItem{
		URI:        "file:///tmp/main.go",
		LanguageID: "go",
		Version:    1,
		Text:       "package main",
	}
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Ensure canonical field names.
	s := string(data)
	for _, want := range []string{`"uri"`, `"languageId"`, `"version"`, `"text"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing field %s in JSON: %s", want, s)
		}
	}

	var item2 TextDocumentItem
	if err := json.Unmarshal(data, &item2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if item2 != item {
		t.Errorf("roundtrip mismatch: got %+v want %+v", item2, item)
	}
}

func TestDiagnostic_Roundtrip(t *testing.T) {
	d := Diagnostic{
		Range: Range{
			Start: Position{Line: 5, Character: 0},
			End:   Position{Line: 5, Character: 15},
		},
		Severity: DiagnosticSeverityError,
		Source:   "gopls",
		Message:  "undeclared name: foo",
	}
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var d2 Diagnostic
	if err := json.Unmarshal(data, &d2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d2.Range != d.Range || d2.Severity != d.Severity ||
		d2.Source != d.Source || d2.Message != d.Message {
		t.Errorf("roundtrip mismatch: got %+v want %+v", d2, d)
	}
}

func TestPublishDiagnosticsParams_Roundtrip(t *testing.T) {
	// Feed JSON as a server would send it.
	raw := `{"uri":"file:///tmp/x.go","diagnostics":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":5}},"severity":1,"message":"bad"}]}`
	var p PublishDiagnosticsParams
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.URI != "file:///tmp/x.go" {
		t.Errorf("URI = %q", p.URI)
	}
	if len(p.Diagnostics) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(p.Diagnostics))
	}
	if p.Diagnostics[0].Message != "bad" {
		t.Errorf("message = %q", p.Diagnostics[0].Message)
	}
	if p.Diagnostics[0].Severity != DiagnosticSeverityError {
		t.Errorf("severity = %v", p.Diagnostics[0].Severity)
	}
}

// --- Enum String methods ---

func TestDiagnosticSeverity_String(t *testing.T) {
	tests := []struct {
		sev  DiagnosticSeverity
		want string
	}{
		{DiagnosticSeverityError, "Error"},
		{DiagnosticSeverityWarning, "Warning"},
		{DiagnosticSeverityInformation, "Info"},
		{DiagnosticSeverityHint, "Hint"},
		{DiagnosticSeverity(99), "Error"}, // default branch
	}
	for _, tc := range tests {
		if got := tc.sev.String(); got != tc.want {
			t.Errorf("%v.String() = %q, want %q", tc.sev, got, tc.want)
		}
	}
}

func TestSymbolKind_String(t *testing.T) {
	tests := []struct {
		kind SymbolKind
		want string
	}{
		{SymbolKindFile, "File"},
		{SymbolKindClass, "Class"},
		{SymbolKindFunction, "Function"},
		{SymbolKindVariable, "Variable"},
		{SymbolKindStruct, "Struct"},
		{SymbolKind(0), "Unknown"},
		{SymbolKind(999), "Unknown"},
	}
	for _, tc := range tests {
		if got := tc.kind.String(); got != tc.want {
			t.Errorf("SymbolKind(%d).String() = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

// --- URI helpers ---

func TestPathToFileURI(t *testing.T) {
	// Unix absolute path.
	got := pathToFileURI("/tmp/foo.go")
	if got != "file:///tmp/foo.go" {
		t.Errorf("pathToFileURI(/tmp/foo.go) = %q", got)
	}
}

func TestFileURIToPath(t *testing.T) {
	got := fileURIToPath("file:///tmp/foo.go")
	if got != "/tmp/foo.go" {
		t.Errorf("fileURIToPath = %q", got)
	}
}

func TestURIRoundtrip(t *testing.T) {
	orig := "/home/user/project/main.go"
	uri := pathToFileURI(orig)
	back := fileURIToPath(uri)
	if back != orig {
		t.Errorf("roundtrip: %q -> %q -> %q", orig, uri, back)
	}
}

// --- Client constructor ---

func TestNewLSPClient(t *testing.T) {
	var crashErr error
	c := NewLSPClient("test-server", func(err error) { crashErr = err })
	if c == nil {
		t.Fatal("NewLSPClient returned nil")
	}
	if c.serverName != "test-server" {
		t.Errorf("serverName = %q", c.serverName)
	}
	if c.pending == nil {
		t.Error("pending map not initialized")
	}
	if c.notifyHandler == nil {
		t.Error("notifyHandler map not initialized")
	}
	if c.readDone == nil {
		t.Error("readDone channel not initialized")
	}
	if c.IsInitialized() {
		t.Error("new client should not be initialized")
	}
	if c.Capabilities() != nil {
		t.Error("new client should have nil capabilities")
	}
	if crashErr != nil {
		t.Errorf("unexpected crash error: %v", crashErr)
	}
}

func TestLSPClient_OnNotification(t *testing.T) {
	c := NewLSPClient("test", nil)
	called := false
	c.OnNotification("textDocument/publishDiagnostics", func(raw json.RawMessage) {
		called = true
	})
	// Verify handler is registered.
	c.mu.Lock()
	h, ok := c.notifyHandler["textDocument/publishDiagnostics"]
	c.mu.Unlock()
	if !ok || h == nil {
		t.Fatal("handler not registered")
	}
	h(nil)
	if !called {
		t.Error("handler not invoked")
	}
}

// --- jsonrpcError ---

func TestJSONRPCError_Error(t *testing.T) {
	e := &jsonrpcError{Code: -32601, Message: "Method not found"}
	msg := e.Error()
	if !strings.Contains(msg, "-32601") {
		t.Errorf("missing code in error message: %q", msg)
	}
	if !strings.Contains(msg, "Method not found") {
		t.Errorf("missing message in error: %q", msg)
	}
}

// --- DiagnosticRegistry ---

func TestNewDiagnosticRegistry(t *testing.T) {
	r := NewDiagnosticRegistry()
	if r == nil {
		t.Fatal("nil registry")
	}
	if r.PendingCount() != 0 {
		t.Errorf("new registry should be empty, got %d", r.PendingCount())
	}
	if files := r.CheckPending(); files != nil {
		t.Errorf("CheckPending on empty registry = %v", files)
	}
}

func TestDiagnosticRegistry_RegisterAndCheck(t *testing.T) {
	r := NewDiagnosticRegistry()
	files := []DiagnosticFile{
		{
			URI: "/tmp/a.go",
			Diagnostics: []FormattedDiag{
				{Message: "err1", Severity: "Error", Range: Range{Start: Position{Line: 1}, End: Position{Line: 1, Character: 5}}},
			},
		},
	}
	r.RegisterDiagnostics("gopls", files)
	if r.PendingCount() != 1 {
		t.Errorf("PendingCount = %d, want 1", r.PendingCount())
	}

	got := r.CheckPending()
	if len(got) != 1 {
		t.Fatalf("CheckPending returned %d files, want 1", len(got))
	}
	if got[0].URI != "/tmp/a.go" {
		t.Errorf("URI = %q", got[0].URI)
	}
	if len(got[0].Diagnostics) != 1 {
		t.Errorf("diagnostics len = %d", len(got[0].Diagnostics))
	}

	// After CheckPending, pending should be cleared.
	if r.PendingCount() != 0 {
		t.Errorf("PendingCount after check = %d, want 0", r.PendingCount())
	}
}

func TestDiagnosticRegistry_CrossTurnDedup(t *testing.T) {
	r := NewDiagnosticRegistry()
	d := FormattedDiag{
		Message:  "duplicate",
		Severity: "Error",
		Range:    Range{Start: Position{Line: 3, Character: 0}, End: Position{Line: 3, Character: 4}},
		Source:   "gopls",
	}
	files := []DiagnosticFile{{URI: "/tmp/b.go", Diagnostics: []FormattedDiag{d}}}

	r.RegisterDiagnostics("gopls", files)
	first := r.CheckPending()
	if len(first) != 1 || len(first[0].Diagnostics) != 1 {
		t.Fatalf("first check expected 1 diag, got %+v", first)
	}

	// Same diagnostic registered again - should be deduplicated.
	r.RegisterDiagnostics("gopls", files)
	second := r.CheckPending()
	if len(second) != 0 {
		t.Errorf("duplicate diagnostic not deduplicated: %+v", second)
	}
}

func TestDiagnosticRegistry_ClearForFile(t *testing.T) {
	r := NewDiagnosticRegistry()
	d := FormattedDiag{Message: "x", Severity: "Error"}
	r.RegisterDiagnostics("s", []DiagnosticFile{{URI: "/tmp/c.go", Diagnostics: []FormattedDiag{d}}})
	_ = r.CheckPending()

	// Clearing should allow the same diagnostic to be delivered again.
	r.ClearForFile("/tmp/c.go")
	r.RegisterDiagnostics("s", []DiagnosticFile{{URI: "/tmp/c.go", Diagnostics: []FormattedDiag{d}}})
	got := r.CheckPending()
	if len(got) != 1 || len(got[0].Diagnostics) != 1 {
		t.Errorf("expected 1 diagnostic after clear, got %+v", got)
	}
}

func TestDiagnosticRegistry_ClearAll(t *testing.T) {
	r := NewDiagnosticRegistry()
	r.RegisterDiagnostics("s", []DiagnosticFile{{URI: "/x", Diagnostics: []FormattedDiag{{Message: "m"}}}})
	if r.PendingCount() == 0 {
		t.Fatal("nothing pending")
	}
	r.ClearAll()
	if r.PendingCount() != 0 {
		t.Errorf("PendingCount after ClearAll = %d", r.PendingCount())
	}
}

func TestDiagnosticRegistry_VolumeLimitPerFile(t *testing.T) {
	r := NewDiagnosticRegistry()
	// Register more than maxDiagnosticsPerFile for a single file.
	var diags []FormattedDiag
	for i := 0; i < 20; i++ {
		diags = append(diags, FormattedDiag{
			Message:  "msg",
			Severity: "Error",
			Range:    Range{Start: Position{Line: i}, End: Position{Line: i, Character: 1}},
		})
	}
	r.RegisterDiagnostics("s", []DiagnosticFile{{URI: "/tmp/big.go", Diagnostics: diags}})
	got := r.CheckPending()
	if len(got) != 1 {
		t.Fatalf("want 1 file, got %d", len(got))
	}
	if len(got[0].Diagnostics) > maxDiagnosticsPerFile {
		t.Errorf("per-file cap not applied: got %d, max %d", len(got[0].Diagnostics), maxDiagnosticsPerFile)
	}
}

func TestDiagnosticRegistry_RegisterFromPublishDiagnostics(t *testing.T) {
	r := NewDiagnosticRegistry()
	raw := json.RawMessage(`{"uri":"file:///tmp/p.go","diagnostics":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":3}},"severity":1,"source":"gopls","message":"oops","code":"E001"}]}`)
	r.RegisterFromPublishDiagnostics("gopls", raw)
	got := r.CheckPending()
	if len(got) != 1 {
		t.Fatalf("want 1 file, got %d", len(got))
	}
	if len(got[0].Diagnostics) != 1 {
		t.Fatalf("want 1 diag, got %d", len(got[0].Diagnostics))
	}
	d := got[0].Diagnostics[0]
	if d.Message != "oops" || d.Severity != "Error" || d.Source != "gopls" || d.Code != "E001" {
		t.Errorf("parsed diagnostic wrong: %+v", d)
	}
}

func TestDiagnosticRegistry_RegisterFromPublishEmptyIgnored(t *testing.T) {
	r := NewDiagnosticRegistry()
	raw := json.RawMessage(`{"uri":"file:///tmp/e.go","diagnostics":[]}`)
	r.RegisterFromPublishDiagnostics("gopls", raw)
	if r.PendingCount() != 0 {
		t.Errorf("empty diagnostics should not be registered; PendingCount = %d", r.PendingCount())
	}
}

// --- Formatting ---

func TestFormatDiagnostics_Empty(t *testing.T) {
	s := FormatDiagnostics(nil, "")
	if s != "No diagnostics." {
		t.Errorf("got %q", s)
	}
}

func TestFormatDiagnostics_WithError(t *testing.T) {
	files := []DiagnosticFile{
		{
			URI: "/tmp/main.go",
			Diagnostics: []FormattedDiag{
				{
					Message:  "bad identifier",
					Severity: "Error",
					Range:    Range{Start: Position{Line: 4, Character: 2}, End: Position{Line: 4, Character: 5}},
					Source:   "gopls",
					Code:     "E42",
				},
			},
		},
	}
	out := FormatDiagnostics(files, "")
	for _, want := range []string{"Found 1 diagnostic", "/tmp/main.go", "Error", "Line 5:3", "bad identifier", "gopls", "E42"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestFormatDiagnostics_SeverityFilter(t *testing.T) {
	files := []DiagnosticFile{
		{
			URI: "/tmp/a.go",
			Diagnostics: []FormattedDiag{
				{Message: "e", Severity: "Error"},
				{Message: "w", Severity: "Warning"},
			},
		},
	}
	out := FormatDiagnostics(files, "Error")
	if !strings.Contains(out, "Found 1 diagnostic") {
		t.Errorf("expected 1 diagnostic after filter:\n%s", out)
	}
	if strings.Contains(out, "Warning") {
		t.Errorf("warning should be filtered:\n%s", out)
	}

	// Filter that matches nothing.
	out2 := FormatDiagnostics(files, "Hint")
	if out2 != "No Hint diagnostics." {
		t.Errorf("unexpected output: %q", out2)
	}
}

// --- Manager ---

func TestNewLSPServerManager(t *testing.T) {
	m := NewLSPServerManager("/tmp/ws")
	if m == nil {
		t.Fatal("nil manager")
	}
	if m.workspaceDir != "/tmp/ws" {
		t.Errorf("workspaceDir = %q", m.workspaceDir)
	}
	if m.IsConnected() {
		t.Error("new manager should not be connected")
	}
	if got := m.AllServers(); len(got) != 0 {
		t.Errorf("expected 0 servers, got %d", len(got))
	}
	if s := m.GetServerForFile("/tmp/foo.go"); s != nil {
		t.Errorf("expected nil server, got %+v", s)
	}
}

func TestLSPServerManager_Initialize(t *testing.T) {
	m := NewLSPServerManager("/tmp/ws")
	m.Initialize(map[string]ServerConfig{
		"gopls": {
			Command:             "gopls",
			Args:                []string{"serve"},
			ExtensionToLanguage: map[string]string{".go": "go"},
		},
		// Should be skipped: no command.
		"nocommand": {
			ExtensionToLanguage: map[string]string{".x": "x"},
		},
		// Should be skipped: no extensions.
		"nomapping": {
			Command: "foo",
		},
	})

	servers := m.AllServers()
	if len(servers) != 1 {
		t.Errorf("expected 1 server, got %d: %+v", len(servers), servers)
	}
	if _, ok := servers["gopls"]; !ok {
		t.Error("gopls server not registered")
	}

	// Extension routing.
	s := m.GetServerForFile("/any/path/main.go")
	if s == nil || s.Name != "gopls" {
		t.Errorf("expected gopls for .go, got %+v", s)
	}
	// Case-insensitive extension.
	s2 := m.GetServerForFile("/foo/Main.GO")
	if s2 == nil || s2.Name != "gopls" {
		t.Errorf("expected gopls for .GO, got %+v", s2)
	}
	// Unknown extension.
	if s3 := m.GetServerForFile("/foo/bar.xyz"); s3 != nil {
		t.Errorf("expected nil for .xyz, got %+v", s3)
	}
}

func TestLSPServerManager_InitialStateStopped(t *testing.T) {
	m := NewLSPServerManager("/tmp/ws")
	m.Initialize(map[string]ServerConfig{
		"gopls": {
			Command:             "gopls",
			ExtensionToLanguage: map[string]string{".go": "go"},
		},
	})
	s := m.GetServerForFile("/tmp/a.go")
	if s == nil {
		t.Fatal("server not found")
	}
	if s.State() != ServerStateStopped {
		t.Errorf("state = %q, want %q", s.State(), ServerStateStopped)
	}
	if s.IsHealthy() {
		t.Error("stopped server should not be healthy")
	}
	if s.LastError() != nil {
		t.Errorf("unexpected last error: %v", s.LastError())
	}
	if m.IsConnected() {
		t.Error("manager with only stopped servers should not be connected")
	}
}

func TestLSPServerManager_FileTracking(t *testing.T) {
	m := NewLSPServerManager("/tmp/ws")
	// No registered servers - IsFileOpen should be false and not panic.
	if m.IsFileOpen("/tmp/foo.go") {
		t.Error("file should not be open")
	}
	// SaveFile / CloseFile on unregistered file should no-op without error.
	if err := m.SaveFile("/tmp/foo.go"); err != nil {
		t.Errorf("SaveFile: %v", err)
	}
	if err := m.CloseFile("/tmp/foo.go"); err != nil {
		t.Errorf("CloseFile: %v", err)
	}
}

func TestLSPServerManager_Shutdown_Empty(t *testing.T) {
	m := NewLSPServerManager("/tmp/ws")
	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown on empty manager: %v", err)
	}
}

// --- DefaultServerConfigs ---

func TestDefaultServerConfigs(t *testing.T) {
	cfgs := DefaultServerConfigs()
	if len(cfgs) == 0 {
		t.Fatal("no default configs")
	}
	// Known servers.
	for _, name := range []string{"gopls", "pyright", "rust-analyzer", "clangd"} {
		cfg, ok := cfgs[name]
		if !ok {
			t.Errorf("missing default config: %s", name)
			continue
		}
		if cfg.Command == "" {
			t.Errorf("%s: empty command", name)
		}
		if len(cfg.ExtensionToLanguage) == 0 {
			t.Errorf("%s: no extension map", name)
		}
	}
	// Specific extension mapping.
	if gopls := cfgs["gopls"]; gopls.ExtensionToLanguage[".go"] != "go" {
		t.Errorf("gopls .go mapping = %q", gopls.ExtensionToLanguage[".go"])
	}
}

// --- severityOrder / diagnosticKey helpers ---

func TestSeverityOrder(t *testing.T) {
	if severityOrder("Error") >= severityOrder("Warning") {
		t.Error("Error should sort before Warning")
	}
	if severityOrder("Warning") >= severityOrder("Info") {
		t.Error("Warning should sort before Info")
	}
	if severityOrder("Info") >= severityOrder("Hint") {
		t.Error("Info should sort before Hint")
	}
	if severityOrder("Unknown") <= severityOrder("Hint") {
		t.Error("Unknown should sort after Hint")
	}
}

func TestDiagnosticKey_Unique(t *testing.T) {
	d1 := FormattedDiag{Message: "a", Severity: "Error", Range: Range{}}
	d2 := FormattedDiag{Message: "b", Severity: "Error", Range: Range{}}
	if diagnosticKey(d1) == diagnosticKey(d2) {
		t.Error("different messages should produce different keys")
	}
	d3 := FormattedDiag{Message: "a", Severity: "Warning", Range: Range{}}
	if diagnosticKey(d1) == diagnosticKey(d3) {
		t.Error("different severities should produce different keys")
	}
	if diagnosticKey(d1) != diagnosticKey(d1) {
		t.Error("same diagnostic should produce same key")
	}
}

// --- InitializeResult parsing ---

func TestInitializeResult_Parse(t *testing.T) {
	raw := `{"capabilities":{"hoverProvider":true,"definitionProvider":true,"referencesProvider":false}}`
	var res InitializeResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !res.Capabilities.HoverProvider {
		t.Error("HoverProvider should be true")
	}
	if !res.Capabilities.DefinitionProvider {
		t.Error("DefinitionProvider should be true")
	}
	if res.Capabilities.ReferencesProvider {
		t.Error("ReferencesProvider should be false")
	}
}

// --- Stop on unstarted client should not panic ---

func TestLSPClient_Stop_NotStarted(t *testing.T) {
	c := NewLSPClient("test", nil)
	if err := c.Stop(); err != nil {
		t.Errorf("Stop on unstarted client: %v", err)
	}
	// Second stop should be a no-op.
	if err := c.Stop(); err != nil {
		t.Errorf("second Stop: %v", err)
	}
}
