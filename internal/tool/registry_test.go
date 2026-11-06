package tool

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/costa/polypod/internal/types"
)

// buildStubTool creates a minimal working Tool for registry tests.
func buildStubTool(name string) types.Tool {
	return types.BuildTool(types.ToolDef{
		NameFn:        func() string { return name },
		DescriptionFn: func() string { return "stub: " + name },
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object"}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsDestructiveFn: func(map[string]any) bool { return false },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			return types.ToolResult{Content: "stub-result"}, nil
		},
	})
}

// buildStubToolWithContent creates a stub tool returning a custom content string.
func buildStubToolWithContent(name, content string) types.Tool {
	return types.BuildTool(types.ToolDef{
		NameFn:        func() string { return name },
		DescriptionFn: func() string { return "stub: " + name },
		InputSchemaFn: func() map[string]any { return map[string]any{"type": "object"} },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			return types.ToolResult{Content: content}, nil
		},
	})
}

// buildDisabledStubTool creates a stub tool that reports itself as disabled.
func buildDisabledStubTool(name string) types.Tool {
	return types.BuildTool(types.ToolDef{
		NameFn:        func() string { return name },
		DescriptionFn: func() string { return "disabled: " + name },
		InputSchemaFn: func() map[string]any { return map[string]any{"type": "object"} },
		IsEnabledFn:   func() bool { return false },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			return types.ToolResult{Content: "should-not-run"}, nil
		},
	})
}

// buildErroringStubTool creates a stub tool whose Execute returns an error.
func buildErroringStubTool(name string, err error) types.Tool {
	return types.BuildTool(types.ToolDef{
		NameFn:        func() string { return name },
		DescriptionFn: func() string { return "erroring: " + name },
		InputSchemaFn: func() map[string]any { return map[string]any{"type": "object"} },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			return types.ToolResult{}, err
		},
	})
}

func testToolContext() types.ToolContext {
	return types.ToolContext{Context: context.Background()}
}

// --- Registry operations ---

func TestRegistry_NewRegistryEmpty(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if r.Count() != 0 {
		t.Errorf("empty registry Count = %d, want 0", r.Count())
	}
	if got := r.List(); len(got) != 0 {
		t.Errorf("empty registry List = %v, want empty", got)
	}
}

func TestRegistry_RegisterGet(t *testing.T) {
	r := NewRegistry()
	r.Register(buildStubTool("stub1"))

	got, ok := r.Get("stub1")
	if !ok {
		t.Fatal("Get should return ok")
	}
	if got.Name() != "stub1" {
		t.Errorf("Name = %q, want %q", got.Name(), "stub1")
	}
	if r.Count() != 1 {
		t.Errorf("Count = %d, want 1", r.Count())
	}
}

func TestFilterByNamesResolvesBridgedSkillsWithoutReplacingBuiltins(t *testing.T) {
	r := NewRegistry()
	r.Register(buildStubTool("skill_recall_memory"))
	r.Register(buildStubTool("read_file"))
	r.Register(buildStubTool("skill_read_file"))
	got := r.FilterByNames([]string{"recall_memory", "read_file"})
	seen := map[string]bool{}
	for _, item := range got {
		seen[item.Name()] = true
	}
	if len(got) != 2 || !seen["skill_recall_memory"] || !seen["read_file"] || seen["skill_read_file"] {
		t.Fatalf("filtered tools = %v", seen)
	}
}

func TestRegistry_GetMissing(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Get("missing"); ok {
		t.Error("Get on empty registry should return !ok")
	}
	r.Register(buildStubTool("a"))
	if _, ok := r.Get("nope"); ok {
		t.Error("Get with unknown name should return !ok")
	}
}

func TestRegistry_RegisterDuplicateOverwrites(t *testing.T) {
	r := NewRegistry()
	r.Register(buildStubToolWithContent("dup", "first"))
	r.Register(buildStubToolWithContent("dup", "second"))

	if r.Count() != 1 {
		t.Errorf("Count after duplicate Register = %d, want 1", r.Count())
	}

	got, ok := r.Get("dup")
	if !ok {
		t.Fatal("Get should find the tool")
	}
	res, err := got.Execute(testToolContext(), nil)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.Content != "second" {
		t.Errorf("Content = %q, want %q (duplicate should overwrite)", res.Content, "second")
	}
}

func TestRegistry_RegisterDefBuildsAndRegisters(t *testing.T) {
	r := NewRegistry()
	tool := r.RegisterDef(types.ToolDef{
		NameFn:        func() string { return "from-def" },
		DescriptionFn: func() string { return "built via def" },
		InputSchemaFn: func() map[string]any { return map[string]any{"type": "object"} },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			return types.ToolResult{Content: "def-ok"}, nil
		},
	})
	if tool == nil {
		t.Fatal("RegisterDef returned nil")
	}
	if tool.Name() != "from-def" {
		t.Errorf("tool.Name = %q", tool.Name())
	}
	if _, ok := r.Get("from-def"); !ok {
		t.Error("RegisterDef did not register the tool")
	}
}

func TestRegistry_List(t *testing.T) {
	r := NewRegistry()
	r.Register(buildStubTool("alpha"))
	r.Register(buildStubTool("beta"))
	r.Register(buildStubTool("gamma"))

	names := r.List()
	if len(names) != 3 {
		t.Fatalf("List length = %d, want 3", len(names))
	}
	sort.Strings(names)
	want := []string{"alpha", "beta", "gamma"}
	for i, n := range want {
		if names[i] != n {
			t.Errorf("List[%d] = %q, want %q", i, names[i], n)
		}
	}
}

func TestRegistry_Remove(t *testing.T) {
	r := NewRegistry()
	r.Register(buildStubTool("a"))
	r.Register(buildStubTool("b"))
	r.Remove("a")

	if _, ok := r.Get("a"); ok {
		t.Error("Removed tool should not be found")
	}
	if _, ok := r.Get("b"); !ok {
		t.Error("Non-removed tool should still be found")
	}
	if r.Count() != 1 {
		t.Errorf("Count after Remove = %d, want 1", r.Count())
	}

	// Removing a non-existent tool should not panic.
	r.Remove("not-present")
}

func TestRegistry_Enabled(t *testing.T) {
	r := NewRegistry()
	r.Register(buildStubTool("on1"))
	r.Register(buildStubTool("on2"))
	r.Register(buildDisabledStubTool("off"))

	enabled := r.Enabled()
	if len(enabled) != 2 {
		t.Fatalf("Enabled length = %d, want 2", len(enabled))
	}
	names := []string{}
	for _, tl := range enabled {
		names = append(names, tl.Name())
	}
	sort.Strings(names)
	if names[0] != "on1" || names[1] != "on2" {
		t.Errorf("Enabled names = %v, want [on1 on2]", names)
	}
}

func TestRegistry_ReadOnly(t *testing.T) {
	r := NewRegistry()
	// buildStubTool marks itself read-only
	r.Register(buildStubTool("ro"))
	// explicitly mutating tool
	r.Register(types.BuildTool(types.ToolDef{
		NameFn:        func() string { return "rw" },
		DescriptionFn: func() string { return "rw" },
		InputSchemaFn: func() map[string]any { return map[string]any{"type": "object"} },
		IsReadOnlyFn:  func(map[string]any) bool { return false },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			return types.ToolResult{Content: "rw"}, nil
		},
	}))

	ro := r.ReadOnly()
	if len(ro) != 1 {
		t.Fatalf("ReadOnly length = %d, want 1", len(ro))
	}
	if ro[0].Name() != "ro" {
		t.Errorf("ReadOnly[0].Name = %q, want %q", ro[0].Name(), "ro")
	}
}

func TestRegistry_FilterByNames(t *testing.T) {
	r := NewRegistry()
	r.Register(buildStubTool("a"))
	r.Register(buildStubTool("b"))
	r.Register(buildStubTool("c"))

	got := r.FilterByNames([]string{"a", "c", "missing"})
	if len(got) != 2 {
		t.Fatalf("FilterByNames length = %d, want 2", len(got))
	}
	names := []string{}
	for _, tl := range got {
		names = append(names, tl.Name())
	}
	sort.Strings(names)
	if names[0] != "a" || names[1] != "c" {
		t.Errorf("FilterByNames names = %v, want [a c]", names)
	}

	// Empty filter returns nothing
	if r.FilterByNames(nil) != nil && len(r.FilterByNames(nil)) != 0 {
		t.Errorf("FilterByNames(nil) should be empty")
	}
}

func TestRegistry_FindByName(t *testing.T) {
	r := NewRegistry()
	r.Register(buildStubTool("known"))
	if tl, ok := r.FindByName("known"); !ok || tl.Name() != "known" {
		t.Errorf("FindByName(known) = (%v, %v)", tl, ok)
	}
	if _, ok := r.FindByName("unknown"); ok {
		t.Error("FindByName(unknown) should return !ok")
	}
}

func TestRegistry_ToolSchemas(t *testing.T) {
	r := NewRegistry()
	r.Register(buildStubTool("one"))
	r.Register(buildDisabledStubTool("two"))

	schemas := r.ToolSchemas()
	if len(schemas) != 1 {
		t.Fatalf("ToolSchemas length = %d, want 1 (disabled should be excluded)", len(schemas))
	}
	s := schemas[0]
	if s["name"] != "one" {
		t.Errorf("schema name = %v, want one", s["name"])
	}
	if _, ok := s["description"]; !ok {
		t.Error("schema missing description")
	}
	if _, ok := s["parameters"]; !ok {
		t.Error("schema missing parameters")
	}
}

// --- Tool building ---

func TestBuildTool_Basic(t *testing.T) {
	tl := buildStubTool("built")
	if tl.Name() != "built" {
		t.Errorf("Name = %q", tl.Name())
	}
	if tl.Description() != "stub: built" {
		t.Errorf("Description = %q", tl.Description())
	}
	if !tl.IsReadOnly(nil) {
		t.Error("IsReadOnly should be true")
	}
	if tl.IsDestructive(nil) {
		t.Error("IsDestructive should be false")
	}
	if !tl.IsEnabled() {
		t.Error("IsEnabled default should be true")
	}

	res, err := tl.Execute(testToolContext(), map[string]any{})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.Content != "stub-result" {
		t.Errorf("Execute Content = %q", res.Content)
	}
}

func TestBuildTool_DefaultValidateAndPermissions(t *testing.T) {
	tl := buildStubTool("defaults")
	vr := tl.ValidateInput(map[string]any{})
	if !vr.Valid {
		t.Errorf("default ValidateInput should be Valid, got %+v", vr)
	}
	pr := tl.CheckPermissions(map[string]any{}, testToolContext())
	if pr.Behavior != types.PermissionAllow {
		t.Errorf("default CheckPermissions should Allow, got %v", pr.Behavior)
	}
}

// --- Execute via registry ---

func TestRegistry_ExecuteDispatches(t *testing.T) {
	r := NewRegistry()
	r.Register(buildStubToolWithContent("echo", "dispatched"))

	res, err := r.Execute("echo", map[string]any{"x": 1}, testToolContext())
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Errorf("IsError should be false, got result %+v", res)
	}
	if res.Content != "dispatched" {
		t.Errorf("Content = %q, want %q", res.Content, "dispatched")
	}
}

func TestRegistry_ExecuteUnknown(t *testing.T) {
	r := NewRegistry()
	_, err := r.Execute("nope", nil, testToolContext())
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestRegistry_ExecutePropagatesToolError(t *testing.T) {
	r := NewRegistry()
	wantErr := errors.New("boom")
	r.Register(buildErroringStubTool("bad", wantErr))

	_, err := r.Execute("bad", nil, testToolContext())
	if err == nil {
		t.Fatal("expected error to propagate")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}

func TestRegistry_ExecuteValidationFailure(t *testing.T) {
	r := NewRegistry()
	r.Register(types.BuildTool(types.ToolDef{
		NameFn:        func() string { return "picky" },
		DescriptionFn: func() string { return "picky" },
		InputSchemaFn: func() map[string]any { return map[string]any{"type": "object"} },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			return types.ValidationResult{Valid: false, Message: "bad input"}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			t.Error("Execute should not run after validation failure")
			return types.ToolResult{}, nil
		},
	}))

	res, err := r.Execute("picky", nil, testToolContext())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Error("result should be marked IsError")
	}
}

func TestRegistry_ExecutePermissionDeny(t *testing.T) {
	r := NewRegistry()
	r.Register(types.BuildTool(types.ToolDef{
		NameFn:        func() string { return "denied" },
		DescriptionFn: func() string { return "denied" },
		InputSchemaFn: func() map[string]any { return map[string]any{"type": "object"} },
		CheckPermFn: func(args map[string]any, ctx types.ToolContext) types.PermissionResult {
			return types.PermissionResult{Behavior: types.PermissionDeny, Message: "nope"}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			t.Error("Execute should not run after permission deny")
			return types.ToolResult{}, nil
		},
	}))

	res, err := r.Execute("denied", nil, testToolContext())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Error("result should be marked IsError on deny")
	}
}

func TestRegistry_ExecuteRawSkipsValidation(t *testing.T) {
	r := NewRegistry()
	r.Register(types.BuildTool(types.ToolDef{
		NameFn:        func() string { return "raw" },
		DescriptionFn: func() string { return "raw" },
		InputSchemaFn: func() map[string]any { return map[string]any{"type": "object"} },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			return types.ValidationResult{Valid: false, Message: "should be skipped"}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			return types.ToolResult{Content: "raw-ran"}, nil
		},
	}))

	res, err := r.ExecuteRaw("raw", nil, testToolContext())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Content != "raw-ran" {
		t.Errorf("Content = %q, want raw-ran (validation should be skipped)", res.Content)
	}
}

func TestRegistry_ExecuteRawUnknown(t *testing.T) {
	r := NewRegistry()
	_, err := r.ExecuteRaw("ghost", nil, testToolContext())
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestRegistry_ValidateInputUnknown(t *testing.T) {
	r := NewRegistry()
	vr := r.ValidateInput("nope", nil)
	if vr.Valid {
		t.Error("ValidateInput for unknown tool should be invalid")
	}
}

func TestRegistry_CheckPermissionsUnknown(t *testing.T) {
	r := NewRegistry()
	pr := r.CheckPermissions("nope", nil, testToolContext())
	if pr.Behavior != types.PermissionDeny {
		t.Errorf("CheckPermissions for unknown tool should deny, got %v", pr.Behavior)
	}
}

func TestRegistry_ExecuteParallel(t *testing.T) {
	r := NewRegistry()
	r.Register(buildStubToolWithContent("a", "A"))
	r.Register(buildStubToolWithContent("b", "B"))
	r.Register(buildStubToolWithContent("c", "C"))

	calls := []ToolCall{
		{ID: "1", Name: "a", Args: nil},
		{ID: "2", Name: "b", Args: nil},
		{ID: "3", Name: "c", Args: nil},
	}
	results := r.ExecuteParallel(calls, testToolContext())
	if len(results) != 3 {
		t.Fatalf("results length = %d, want 3", len(results))
	}
	// Results are returned in the same order as input.
	want := []string{"A", "B", "C"}
	for i, r := range results {
		if r.Error != nil {
			t.Errorf("results[%d].Error = %v", i, r.Error)
		}
		if r.Result.Content != want[i] {
			t.Errorf("results[%d].Content = %q, want %q", i, r.Result.Content, want[i])
		}
		if r.Call.ID != calls[i].ID {
			t.Errorf("results[%d].Call.ID = %q, want %q", i, r.Call.ID, calls[i].ID)
		}
	}
}
