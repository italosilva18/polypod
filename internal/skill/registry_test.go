package skill

import (
	"errors"
	"sort"
	"sync"
	"testing"

	"github.com/sashabaranov/go-openai/jsonschema"
)

// newTestSkill is a helper that builds a trivial skill for tests.
func newTestSkill(name, result string, execErr error) *Skill {
	return &Skill{
		Name:        name,
		Description: "test skill " + name,
		Parameters: jsonschema.Definition{
			Type:       jsonschema.Object,
			Properties: map[string]jsonschema.Definition{},
		},
		Execute: func(args map[string]string) (string, error) {
			if execErr != nil {
				return "", execErr
			}
			return result, nil
		},
	}
}

func TestNewRegistry_NotNil(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if r.skills == nil {
		t.Fatal("skills map is nil")
	}
}

func TestNewRegistry_HasBuiltins(t *testing.T) {
	r := NewRegistry()
	// registerBuiltins registers: read_file, list_directory, run_command,
	// search_files, code-review, test-generator, doc_summary, dependency-audit
	// => 8 built-ins.
	const wantBuiltins = 8
	if got := len(r.List()); got < wantBuiltins {
		t.Errorf("builtin count = %d, want >= %d", got, wantBuiltins)
	}
}

func TestRegistry_WellKnownBuiltinsPresent(t *testing.T) {
	r := NewRegistry()
	wellKnown := []string{
		"read_file",
		"list_directory",
		"run_command",
		"search_files",
	}
	for _, name := range wellKnown {
		if _, ok := r.Get(name); !ok {
			t.Errorf("builtin %q missing from registry", name)
		}
	}
}

func TestRegistry_BundledSkillsPresent(t *testing.T) {
	r := NewRegistry()
	bundled := []string{
		"code-review",
		"test-generator",
		"doc_summary",
		"dependency-audit",
	}
	for _, name := range bundled {
		s, ok := r.Get(name)
		if !ok {
			t.Errorf("bundled skill %q missing", name)
			continue
		}
		if s.Name != name {
			t.Errorf("bundled skill Name = %q, want %q", s.Name, name)
		}
		if s.Execute == nil {
			t.Errorf("bundled skill %q has nil Execute", name)
		}
	}
}

func TestRegistry_Register_And_Get(t *testing.T) {
	r := NewRegistry()
	s := newTestSkill("custom_one", "hello", nil)
	r.Register(s)

	got, ok := r.Get("custom_one")
	if !ok {
		t.Fatal("Get(custom_one) ok = false, want true")
	}
	if got.Name != "custom_one" {
		t.Errorf("Name = %q, want %q", got.Name, "custom_one")
	}
	if got.Description != "test skill custom_one" {
		t.Errorf("Description = %q", got.Description)
	}
}

func TestRegistry_Get_Missing(t *testing.T) {
	r := NewRegistry()
	got, ok := r.Get("this_does_not_exist_xyz")
	if ok {
		t.Errorf("Get missing returned ok = true")
	}
	if got != nil {
		t.Errorf("Get missing returned non-nil skill: %+v", got)
	}
}

func TestRegistry_Register_Overwrites(t *testing.T) {
	r := NewRegistry()
	r.Register(newTestSkill("dup", "v1", nil))
	r.Register(newTestSkill("dup", "v2", nil))

	got, ok := r.Get("dup")
	if !ok {
		t.Fatal("Get(dup) missing")
	}
	res, err := got.Execute(nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res != "v2" {
		t.Errorf("overwrite: Execute = %q, want %q", res, "v2")
	}
}

func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry()
	r.Register(newTestSkill("to_remove", "x", nil))

	if _, ok := r.Get("to_remove"); !ok {
		t.Fatal("skill not registered")
	}
	r.Unregister("to_remove")
	if _, ok := r.Get("to_remove"); ok {
		t.Error("skill still present after Unregister")
	}
}

func TestRegistry_Unregister_Missing_NoPanic(t *testing.T) {
	r := NewRegistry()
	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("Unregister missing panicked: %v", p)
		}
	}()
	r.Unregister("never_was_here")
}

func TestRegistry_List_ContainsRegistered(t *testing.T) {
	r := NewRegistry()
	r.Register(newTestSkill("alpha_skill", "", nil))
	r.Register(newTestSkill("beta_skill", "", nil))

	names := r.List()
	have := make(map[string]bool, len(names))
	for _, n := range names {
		have[n] = true
	}
	for _, want := range []string{"alpha_skill", "beta_skill", "read_file"} {
		if !have[want] {
			t.Errorf("List() missing %q; got %v", want, names)
		}
	}
}

func TestRegistry_List_SortedView(t *testing.T) {
	// List() itself does not promise ordering, but callers can sort it.
	// Verify sorting the output yields a strictly increasing unique list.
	r := NewRegistry()
	names := r.List()
	sort.Strings(names)
	for i := 1; i < len(names); i++ {
		if names[i] == names[i-1] {
			t.Errorf("duplicate name in List(): %q", names[i])
		}
		if names[i] < names[i-1] {
			t.Errorf("List() not sortable? %q < %q", names[i], names[i-1])
		}
	}
}

func TestSkill_Execute_Basic(t *testing.T) {
	s := newTestSkill("basic", "returned-value", nil)
	got, err := s.Execute(nil)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if got != "returned-value" {
		t.Errorf("Execute = %q, want %q", got, "returned-value")
	}
}

func TestSkill_Execute_ErrorPropagates(t *testing.T) {
	boom := errors.New("boom")
	s := newTestSkill("fails", "", boom)
	_, err := s.Execute(nil)
	if err == nil {
		t.Fatal("Execute error = nil, want boom")
	}
	if !errors.Is(err, boom) {
		t.Errorf("Execute error = %v, want %v", err, boom)
	}
}

func TestSkill_Execute_ReceivesArgs(t *testing.T) {
	var captured map[string]string
	s := &Skill{
		Name:        "capture",
		Description: "captures args",
		Parameters: jsonschema.Definition{
			Type:       jsonschema.Object,
			Properties: map[string]jsonschema.Definition{},
		},
		Execute: func(args map[string]string) (string, error) {
			captured = args
			return args["greeting"] + " " + args["who"], nil
		},
	}
	result, err := s.Execute(map[string]string{
		"greeting": "hello",
		"who":      "world",
	})
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if result != "hello world" {
		t.Errorf("result = %q, want %q", result, "hello world")
	}
	if captured["greeting"] != "hello" || captured["who"] != "world" {
		t.Errorf("captured args = %+v", captured)
	}
}

func TestRegistry_Execute_Success(t *testing.T) {
	r := NewRegistry()
	r.Register(newTestSkill("echo_test", "pong", nil))

	out, err := r.Execute("echo_test", "{}")
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if out != "pong" {
		t.Errorf("Execute out = %q, want %q", out, "pong")
	}
}

func TestRegistry_Execute_UnknownSkill(t *testing.T) {
	r := NewRegistry()
	_, err := r.Execute("no_such_skill", "{}")
	if err == nil {
		t.Fatal("Execute unknown skill err = nil, want error")
	}
}

func TestRegistry_Execute_BadJSON(t *testing.T) {
	r := NewRegistry()
	r.Register(newTestSkill("anything", "x", nil))
	_, err := r.Execute("anything", "not json at all")
	if err == nil {
		t.Fatal("Execute bad JSON err = nil, want parse error")
	}
}

func TestRegistry_Execute_SkillErrorSwallowed(t *testing.T) {
	// Registry.Execute wraps skill errors into an "Erro: ..." string,
	// returning a nil error to the caller.
	r := NewRegistry()
	r.Register(newTestSkill("bad", "", errors.New("kaboom")))

	out, err := r.Execute("bad", "{}")
	if err != nil {
		t.Fatalf("Execute err = %v, want nil (skill error should be swallowed into output)", err)
	}
	if out == "" {
		t.Error("expected non-empty output describing skill error")
	}
}

func TestRegistry_Execute_TruncatesLargeOutput(t *testing.T) {
	big := make([]byte, MaxOutputBytes+500)
	for i := range big {
		big[i] = 'a'
	}
	r := NewRegistry()
	r.Register(newTestSkill("big", string(big), nil))

	out, err := r.Execute("big", "{}")
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if len(out) <= MaxOutputBytes {
		t.Errorf("output len = %d, expected > %d (should include truncation marker)",
			len(out), MaxOutputBytes)
	}
	if len(out) > MaxOutputBytes+200 {
		// Should be MaxOutputBytes + marker (~40 chars), not the full payload.
		t.Errorf("output not truncated: len = %d", len(out))
	}
}

func TestRegistry_ToolDefinitions_Empty_ReturnsAll(t *testing.T) {
	r := NewRegistry()
	tools := r.ToolDefinitions(nil)
	if len(tools) != len(r.List()) {
		t.Errorf("ToolDefinitions(nil) len = %d, want %d", len(tools), len(r.List()))
	}
}

func TestRegistry_ToolDefinitions_Filtered(t *testing.T) {
	r := NewRegistry()
	tools := r.ToolDefinitions([]string{"read_file", "list_directory"})
	if len(tools) != 2 {
		t.Fatalf("ToolDefinitions len = %d, want 2", len(tools))
	}
	names := []string{tools[0].Function.Name, tools[1].Function.Name}
	sort.Strings(names)
	if names[0] != "list_directory" || names[1] != "read_file" {
		t.Errorf("tool names = %v", names)
	}
}

func TestRegistry_ToolDefinitions_SkipsUnknown(t *testing.T) {
	r := NewRegistry()
	tools := r.ToolDefinitions([]string{"read_file", "nope_nope_nope"})
	if len(tools) != 1 {
		t.Errorf("ToolDefinitions len = %d, want 1", len(tools))
	}
	if len(tools) == 1 && tools[0].Function.Name != "read_file" {
		t.Errorf("got %q", tools[0].Function.Name)
	}
}

// TestRegistry_ConcurrentGet verifies concurrent reads via Get are safe.
// Registry does not use an internal mutex, so concurrent writes would race;
// we only test the read path here, which is safe once all writes have completed.
func TestRegistry_ConcurrentGet(t *testing.T) {
	r := NewRegistry()
	r.Register(newTestSkill("concurrent_target", "ok", nil))

	const goroutines = 32
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if _, ok := r.Get("concurrent_target"); !ok {
					t.Error("concurrent Get failed")
					return
				}
				if _, ok := r.Get("read_file"); !ok {
					t.Error("concurrent Get builtin failed")
					return
				}
			}
		}()
	}
	wg.Wait()
}
