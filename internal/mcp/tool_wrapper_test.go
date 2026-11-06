package mcp

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/costa/polypod/internal/types"
)

// fakeMCPCaller is a test double capturing invocations and returning
// scripted responses.
type fakeMCPCaller struct {
	gotServer  string
	gotName    string
	gotArgs    map[string]any
	gotCtxSeen bool

	result  any
	isError bool
	err     error
	calls   int
}

func (f *fakeMCPCaller) CallTool(ctx context.Context, server, name string, args map[string]any) (any, bool, error) {
	f.calls++
	f.gotServer = server
	f.gotName = name
	f.gotArgs = args
	f.gotCtxSeen = ctx != nil
	return f.result, f.isError, f.err
}

func TestWrapMCPTool_NamePrefix(t *testing.T) {
	caller := &fakeMCPCaller{result: "ok"}

	tool := WrapMCPTool(caller, "canva", "export-design", "Export a design", nil)

	want := "mcp__canva__export-design"
	if got := tool.Name(); got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
}

func TestWrapMCPTool_SchemaPassthrough(t *testing.T) {
	caller := &fakeMCPCaller{result: "ok"}

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
			"limit": map[string]any{"type": "integer"},
		},
		"required": []any{"query"},
	}

	tool := WrapMCPTool(caller, "srv", "search", "Search things", schema)

	got := tool.InputSchema()
	if !reflect.DeepEqual(got, schema) {
		t.Errorf("InputSchema() did not round-trip:\n got  = %#v\n want = %#v", got, schema)
	}
}

func TestWrapMCPTool_SchemaDefaultWhenNil(t *testing.T) {
	caller := &fakeMCPCaller{result: "ok"}
	tool := WrapMCPTool(caller, "srv", "t", "desc", nil)

	schema := tool.InputSchema()
	if schema == nil {
		t.Fatal("InputSchema() returned nil for nil input schema; expected permissive default")
	}
	if schema["type"] != "object" {
		t.Errorf("default schema type = %v, want object", schema["type"])
	}
}

func TestWrapMCPTool_Description(t *testing.T) {
	caller := &fakeMCPCaller{result: "ok"}
	tool := WrapMCPTool(caller, "srv", "t", "The description", nil)
	if tool.Description() != "The description" {
		t.Errorf("Description() = %q, want %q", tool.Description(), "The description")
	}
}

func TestWrapMCPTool_HappyPath_StringResult(t *testing.T) {
	caller := &fakeMCPCaller{result: "hello world"}
	tool := WrapMCPTool(caller, "srv", "greet", "Greet", nil)

	ctx := types.ToolContext{Context: context.Background()}
	args := map[string]any{"name": "Alice"}

	res, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Errorf("IsError = true, want false")
	}
	if res.Content != "hello world" {
		t.Errorf("Content = %q, want %q", res.Content, "hello world")
	}

	if caller.calls != 1 {
		t.Errorf("calls = %d, want 1", caller.calls)
	}
	if caller.gotServer != "srv" {
		t.Errorf("gotServer = %q, want %q", caller.gotServer, "srv")
	}
	if caller.gotName != "greet" {
		t.Errorf("gotName = %q, want %q", caller.gotName, "greet")
	}
	if !reflect.DeepEqual(caller.gotArgs, args) {
		t.Errorf("gotArgs = %#v, want %#v", caller.gotArgs, args)
	}
	if !caller.gotCtxSeen {
		t.Error("caller did not receive a non-nil context")
	}
}

func TestWrapMCPTool_HappyPath_ToolCallResult(t *testing.T) {
	caller := &fakeMCPCaller{
		result: &ToolCallResult{
			Content: []ContentBlock{
				{Type: "text", Text: "line1"},
				{Type: "text", Text: "line2"},
			},
		},
	}
	tool := WrapMCPTool(caller, "srv", "t", "desc", nil)

	res, err := tool.Execute(types.ToolContext{Context: context.Background()}, nil)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Error("IsError = true, want false")
	}
	if res.Content != "line1\nline2" {
		t.Errorf("Content = %q, want %q", res.Content, "line1\nline2")
	}
}

func TestWrapMCPTool_HappyPath_JSONFallback(t *testing.T) {
	caller := &fakeMCPCaller{
		result: map[string]any{"ok": true, "n": float64(42)},
	}
	tool := WrapMCPTool(caller, "srv", "t", "desc", nil)

	res, err := tool.Execute(types.ToolContext{Context: context.Background()}, nil)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Error("IsError = true, want false")
	}
	// Don't assume JSON field ordering — just check it contains both keys.
	if !contains(res.Content, `"ok":true`) || !contains(res.Content, `"n":42`) {
		t.Errorf("Content = %q, want JSON containing ok=true and n=42", res.Content)
	}
}

func TestWrapMCPTool_ErrorPath_CallerReturnsError(t *testing.T) {
	caller := &fakeMCPCaller{err: errors.New("boom")}
	tool := WrapMCPTool(caller, "srv", "t", "desc", nil)

	res, err := tool.Execute(types.ToolContext{Context: context.Background()}, nil)
	if err == nil {
		t.Fatal("Execute returned nil error, want propagated error")
	}
	if !res.IsError {
		t.Error("IsError = false, want true on caller error")
	}
	if !contains(res.Content, "boom") {
		t.Errorf("Content = %q, want substring %q", res.Content, "boom")
	}
}

func TestWrapMCPTool_ErrorPath_IsErrorFlag(t *testing.T) {
	caller := &fakeMCPCaller{
		result:  "tool reported failure",
		isError: true,
	}
	tool := WrapMCPTool(caller, "srv", "t", "desc", nil)

	res, err := tool.Execute(types.ToolContext{Context: context.Background()}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Error("IsError = false, want true when caller sets isError=true")
	}
	if res.Content != "tool reported failure" {
		t.Errorf("Content = %q, want %q", res.Content, "tool reported failure")
	}
}

func TestWrapMCPTool_NilCaller(t *testing.T) {
	tool := WrapMCPTool(nil, "srv", "t", "desc", nil)

	res, err := tool.Execute(types.ToolContext{Context: context.Background()}, nil)
	if err == nil {
		t.Fatal("expected error for nil caller")
	}
	if !res.IsError {
		t.Error("IsError = false, want true for nil caller")
	}
}

func TestWrapMCPTool_Permissions(t *testing.T) {
	caller := &fakeMCPCaller{result: "ok"}
	tool := WrapMCPTool(caller, "srv", "t", "desc", nil)

	if tool.IsReadOnly(nil) {
		t.Error("IsReadOnly = true, want false (conservative default)")
	}
	if tool.IsDestructive(nil) {
		t.Error("IsDestructive = true, want false")
	}
	if !tool.IsConcurrencySafe(nil) {
		t.Error("IsConcurrencySafe = false, want true")
	}
	if !tool.IsEnabled() {
		t.Error("IsEnabled = false, want true")
	}
}

func TestWrapAllMCPTools(t *testing.T) {
	caller := &fakeMCPCaller{result: "ok"}

	servers := []MCPServerTools{
		{
			ServerName: "canva",
			Tools: []MCPToolInfo{
				{Name: "export-design", Description: "d1"},
				{Name: "get-design", Description: "d2"},
			},
		},
		{
			ServerName: "figma",
			Tools: []MCPToolInfo{
				{Name: "get_metadata", Description: "d3"},
			},
		},
	}

	tools := WrapAllMCPTools(caller, servers)
	if len(tools) != 3 {
		t.Fatalf("len(tools) = %d, want 3", len(tools))
	}

	want := []string{
		"mcp__canva__export-design",
		"mcp__canva__get-design",
		"mcp__figma__get_metadata",
	}
	for i, w := range want {
		if got := tools[i].Name(); got != w {
			t.Errorf("tools[%d].Name() = %q, want %q", i, got, w)
		}
	}
}

// fakeRegistry is a minimal ToolRegistry implementation for testing
// RegisterWrappedMCPTools.
type fakeRegistry struct {
	registered []types.Tool
}

func (r *fakeRegistry) Register(t types.Tool) {
	r.registered = append(r.registered, t)
}

func TestRegisterWrappedMCPTools(t *testing.T) {
	caller := &fakeMCPCaller{result: "ok"}
	reg := &fakeRegistry{}

	servers := []MCPServerTools{
		{
			ServerName: "srv1",
			Tools: []MCPToolInfo{
				{Name: "a", Description: "A"},
				{Name: "b", Description: "B"},
			},
		},
	}

	RegisterWrappedMCPTools(reg, caller, servers)

	if len(reg.registered) != 2 {
		t.Fatalf("registered %d tools, want 2", len(reg.registered))
	}
	if reg.registered[0].Name() != "mcp__srv1__a" {
		t.Errorf("registered[0].Name() = %q, want mcp__srv1__a", reg.registered[0].Name())
	}
	if reg.registered[1].Name() != "mcp__srv1__b" {
		t.Errorf("registered[1].Name() = %q, want mcp__srv1__b", reg.registered[1].Name())
	}
}

func TestRegisterWrappedMCPTools_NilRegistry(t *testing.T) {
	// Should not panic.
	RegisterWrappedMCPTools(nil, &fakeMCPCaller{}, []MCPServerTools{
		{ServerName: "s", Tools: []MCPToolInfo{{Name: "t"}}},
	})
}

// contains is a tiny helper to avoid pulling in strings just for a
// Contains check in tests where we already import reflect/errors.
func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
