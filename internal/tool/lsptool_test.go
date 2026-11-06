package tool

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/lsp"
	"github.com/costa/polypod/internal/types"
)

// fakeLSPClient is a minimal in-memory LSPClient used to drive the bridge
// tool without spawning a real language server.
type fakeLSPClient struct {
	connected bool
	opened    map[string]string
	// responses maps a method name to the raw JSON payload that should be
	// returned to the caller.
	responses map[string]json.RawMessage
	// err is returned from SendRequest when non-nil.
	err error
	// lastMethod / lastParams capture the most recent call for assertions.
	lastMethod string
	lastParams any
	lastFile   string
}

func newFakeLSPClient() *fakeLSPClient {
	return &fakeLSPClient{
		connected: true,
		opened:    make(map[string]string),
		responses: make(map[string]json.RawMessage),
	}
}

func (f *fakeLSPClient) IsConnected() bool { return f.connected }

func (f *fakeLSPClient) OpenFile(filePath, content string) error {
	f.opened[filePath] = content
	return nil
}

func (f *fakeLSPClient) SendRequest(filePath, method string, params any) (json.RawMessage, error) {
	f.lastFile = filePath
	f.lastMethod = method
	f.lastParams = params
	if f.err != nil {
		return nil, f.err
	}
	if raw, ok := f.responses[method]; ok {
		return raw, nil
	}
	return json.RawMessage("null"), nil
}

// TestLSPToolSchemaAndValidation exercises the tool metadata and validation
// paths: schema shape, required fields per operation, and unknown operations.
func TestLSPToolSchemaAndValidation(t *testing.T) {
	fake := newFakeLSPClient()
	tool := NewLSPTool(fake)

	// --- Name / basic metadata ---
	if tool.Name() != ToolNameLSP {
		t.Fatalf("Name() = %q, want %q", tool.Name(), ToolNameLSP)
	}
	if !tool.IsReadOnly(nil) {
		t.Errorf("LSP tool should be read-only")
	}
	if !tool.IsConcurrencySafe(nil) {
		t.Errorf("LSP tool should be concurrency-safe")
	}
	if !tool.IsEnabled() {
		t.Errorf("LSP tool should be enabled when fake client is connected")
	}

	// --- Schema shape ---
	schema := tool.InputSchema()
	if schema["type"] != "object" {
		t.Errorf("schema type = %v, want object", schema["type"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties missing or wrong type: %T", schema["properties"])
	}
	for _, field := range []string{"operation", "file", "line", "character", "query"} {
		if _, ok := props[field]; !ok {
			t.Errorf("schema missing property %q", field)
		}
	}
	required, _ := schema["required"].([]string)
	if len(required) != 1 || required[0] != "operation" {
		t.Errorf("schema required = %v, want [operation]", required)
	}

	// Make sure "operation" carries the expected enum.
	opSpec, _ := props["operation"].(map[string]any)
	enum, _ := opSpec["enum"].([]string)
	want := map[string]bool{"definition": true, "references": true, "hover": true, "symbols": true}
	if len(enum) != len(want) {
		t.Errorf("operation enum = %v, want %v", enum, want)
	}
	for _, v := range enum {
		if !want[v] {
			t.Errorf("unexpected operation in enum: %q", v)
		}
	}

	// --- Validation ---
	cases := []struct {
		name    string
		args    map[string]any
		valid   bool
		errPart string
	}{
		{
			name:    "missing operation",
			args:    map[string]any{},
			valid:   false,
			errPart: "operation is required",
		},
		{
			name:    "unknown operation",
			args:    map[string]any{"operation": "bogus"},
			valid:   false,
			errPart: "unknown operation",
		},
		{
			name:    "definition missing file",
			args:    map[string]any{"operation": "definition", "line": 1, "character": 1},
			valid:   false,
			errPart: "file is required",
		},
		{
			name:    "definition missing line",
			args:    map[string]any{"operation": "definition", "file": "foo.go", "character": 1},
			valid:   false,
			errPart: "line is required",
		},
		{
			name:    "definition missing character",
			args:    map[string]any{"operation": "definition", "file": "foo.go", "line": 1},
			valid:   false,
			errPart: "character is required",
		},
		{
			name:  "definition complete",
			args:  map[string]any{"operation": "definition", "file": "foo.go", "line": 1, "character": 1},
			valid: true,
		},
		{
			name:  "references complete",
			args:  map[string]any{"operation": "references", "file": "foo.go", "line": 2, "character": 3},
			valid: true,
		},
		{
			name:  "hover complete",
			args:  map[string]any{"operation": "hover", "file": "foo.go", "line": 2, "character": 3},
			valid: true,
		},
		{
			name:    "symbols missing query",
			args:    map[string]any{"operation": "symbols"},
			valid:   false,
			errPart: "query is required",
		},
		{
			name:    "symbols empty query",
			args:    map[string]any{"operation": "symbols", "query": "   "},
			valid:   false,
			errPart: "query is required",
		},
		{
			name:  "symbols with query",
			args:  map[string]any{"operation": "symbols", "query": "Foo"},
			valid: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := tool.ValidateInput(tc.args)
			if res.Valid != tc.valid {
				t.Errorf("Valid = %v, want %v (msg=%q)", res.Valid, tc.valid, res.Message)
			}
			if !tc.valid && tc.errPart != "" && !strings.Contains(res.Message, tc.errPart) {
				t.Errorf("error message %q does not contain %q", res.Message, tc.errPart)
			}
		})
	}

	// --- Disabled when client is not connected ---
	disconnected := newFakeLSPClient()
	disconnected.connected = false
	if NewLSPTool(disconnected).IsEnabled() {
		t.Errorf("tool should not be enabled when client is disconnected")
	}
	if NewLSPTool(nil).IsEnabled() {
		t.Errorf("tool with nil client should not be enabled")
	}
}

// TestLSPToolExecuteHappyPaths drives Execute through a fake client and
// verifies the formatted output for each supported operation.
func TestLSPToolExecuteHappyPaths(t *testing.T) {
	fake := newFakeLSPClient()

	// --- definition: single Location ---
	defReply := lsp.Location{
		URI: "file:///tmp/example.go",
		Range: lsp.Range{
			Start: lsp.Position{Line: 9, Character: 4},
			End:   lsp.Position{Line: 9, Character: 12},
		},
	}
	defRaw, _ := json.Marshal(defReply)
	fake.responses["textDocument/definition"] = defRaw

	// --- references: Location array across two files ---
	refReply := []lsp.Location{
		{
			URI: "file:///tmp/a.go",
			Range: lsp.Range{
				Start: lsp.Position{Line: 0, Character: 0},
				End:   lsp.Position{Line: 0, Character: 3},
			},
		},
		{
			URI: "file:///tmp/a.go",
			Range: lsp.Range{
				Start: lsp.Position{Line: 4, Character: 1},
				End:   lsp.Position{Line: 4, Character: 5},
			},
		},
		{
			URI: "file:///tmp/b.go",
			Range: lsp.Range{
				Start: lsp.Position{Line: 1, Character: 2},
				End:   lsp.Position{Line: 1, Character: 8},
			},
		},
	}
	refRaw, _ := json.Marshal(refReply)
	fake.responses["textDocument/references"] = refRaw

	// --- hover ---
	hoverReply := lsp.Hover{
		Contents: lsp.MarkupContent{
			Kind:  "markdown",
			Value: "func Foo() error",
		},
	}
	hoverRaw, _ := json.Marshal(hoverReply)
	fake.responses["textDocument/hover"] = hoverRaw

	// --- workspace/symbol ---
	symbolReply := []lsp.SymbolInformation{
		{
			Name:          "Foo",
			Kind:          lsp.SymbolKindFunction,
			ContainerName: "mypkg",
			Location: lsp.Location{
				URI: "file:///tmp/a.go",
				Range: lsp.Range{
					Start: lsp.Position{Line: 11, Character: 0},
				},
			},
		},
	}
	symbolRaw, _ := json.Marshal(symbolReply)
	fake.responses["workspace/symbol"] = symbolRaw

	tool := NewLSPTool(fake)
	ctx := types.ToolContext{}

	// --- definition happy path ---
	t.Run("definition", func(t *testing.T) {
		res, err := tool.Execute(ctx, map[string]any{
			"operation": "definition",
			"file":      "example.go",
			"line":      10,
			"character": 5,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.IsError {
			t.Fatalf("unexpected error result: %s", res.Content)
		}
		if !strings.Contains(res.Content, "example.go") {
			t.Errorf("definition output missing filename: %q", res.Content)
		}
		if !strings.Contains(res.Content, "10:5") {
			t.Errorf("definition output should contain 1-based position 10:5: %q", res.Content)
		}
		if fake.lastMethod != "textDocument/definition" {
			t.Errorf("expected textDocument/definition, got %q", fake.lastMethod)
		}

		// Verify the forwarded position is 0-based.
		params, ok := fake.lastParams.(lsp.TextDocumentPositionParams)
		if !ok {
			t.Fatalf("unexpected params type: %T", fake.lastParams)
		}
		if params.Position.Line != 9 || params.Position.Character != 4 {
			t.Errorf("forwarded position = %+v, want {Line:9, Character:4}",
				params.Position)
		}
	})

	// --- references happy path ---
	t.Run("references", func(t *testing.T) {
		res, err := tool.Execute(ctx, map[string]any{
			"operation": "references",
			"file":      "example.go",
			"line":      1,
			"character": 1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.IsError {
			t.Fatalf("unexpected error result: %s", res.Content)
		}
		// 3 references across 2 files
		if !strings.Contains(res.Content, "3 reference(s)") {
			t.Errorf("references output missing count: %q", res.Content)
		}
		if !strings.Contains(res.Content, "2 file(s)") {
			t.Errorf("references output missing file count: %q", res.Content)
		}
		if !strings.Contains(res.Content, "a.go") || !strings.Contains(res.Content, "b.go") {
			t.Errorf("references output should mention both files: %q", res.Content)
		}
	})

	// --- hover happy path ---
	t.Run("hover", func(t *testing.T) {
		res, err := tool.Execute(ctx, map[string]any{
			"operation": "hover",
			"file":      "example.go",
			"line":      1,
			"character": 1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.IsError {
			t.Fatalf("unexpected error result: %s", res.Content)
		}
		if !strings.Contains(res.Content, "func Foo() error") {
			t.Errorf("hover output missing markdown body: %q", res.Content)
		}
	})

	// --- symbols happy path ---
	t.Run("symbols", func(t *testing.T) {
		res, err := tool.Execute(ctx, map[string]any{
			"operation": "symbols",
			"query":     "Foo",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.IsError {
			t.Fatalf("unexpected error result: %s", res.Content)
		}
		if !strings.Contains(res.Content, "Foo") {
			t.Errorf("symbols output missing symbol name: %q", res.Content)
		}
		if !strings.Contains(res.Content, "Function") {
			t.Errorf("symbols output missing kind: %q", res.Content)
		}
		if !strings.Contains(res.Content, "mypkg") {
			t.Errorf("symbols output missing container: %q", res.Content)
		}
		if fake.lastMethod != "workspace/symbol" {
			t.Errorf("expected workspace/symbol, got %q", fake.lastMethod)
		}
	})

	// --- disconnected client produces friendly error ---
	t.Run("disconnected", func(t *testing.T) {
		dis := newFakeLSPClient()
		dis.connected = false
		res, err := NewLSPTool(dis).Execute(ctx, map[string]any{
			"operation": "symbols",
			"query":     "Foo",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.IsError {
			t.Errorf("expected error result when no server is running")
		}
		if !strings.Contains(res.Content, "No LSP server") {
			t.Errorf("unexpected disconnected message: %q", res.Content)
		}
	})

	// --- SendRequest error propagates ---
	t.Run("send request error", func(t *testing.T) {
		errClient := newFakeLSPClient()
		errClient.err = fmt.Errorf("boom")
		res, err := NewLSPTool(errClient).Execute(ctx, map[string]any{
			"operation": "hover",
			"file":      "example.go",
			"line":      1,
			"character": 1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.IsError || !strings.Contains(res.Content, "boom") {
			t.Errorf("expected error containing 'boom', got %q (isErr=%v)", res.Content, res.IsError)
		}
	})
}

// TestRegisterLSPTool verifies that the helper registers the tool under the
// expected name.
func TestRegisterLSPTool(t *testing.T) {
	reg := NewRegistry()
	fake := newFakeLSPClient()
	got := RegisterLSPTool(reg, fake)
	if got == nil {
		t.Fatal("RegisterLSPTool returned nil")
	}
	fetched, ok := reg.Get(ToolNameLSP)
	if !ok {
		t.Fatalf("tool %q not registered", ToolNameLSP)
	}
	if fetched.Name() != ToolNameLSP {
		t.Errorf("fetched.Name() = %q, want %q", fetched.Name(), ToolNameLSP)
	}
}
