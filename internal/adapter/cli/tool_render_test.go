package cli

import (
	"strings"
	"sync"
	"testing"
)

// TestToolRendererRegistry_RegisterAndLookup verifies that a renderer
// registered via Register can be retrieved and invoked via RenderUse /
// RenderResult, and that Has reports membership correctly.
func TestToolRendererRegistry_RegisterAndLookup(t *testing.T) {
	reg := NewToolRendererRegistry()

	called := false
	reg.Register("custom_tool", &ToolRenderer{
		RenderUse: func(name string, input map[string]any) string {
			called = true
			return "USE:" + name
		},
		RenderResult: func(name string, input map[string]any, output string, isError bool) string {
			return "RES:" + name + ":" + output
		},
	})

	if !reg.Has("custom_tool") {
		t.Fatalf("Has(custom_tool) = false, want true")
	}

	out, ok := reg.RenderUse("custom_tool", map[string]any{"x": 1})
	if !ok {
		t.Fatalf("RenderUse ok = false, want true")
	}
	if !called {
		t.Fatalf("RenderUse callback not invoked")
	}
	if out != "USE:custom_tool" {
		t.Fatalf("RenderUse = %q, want %q", out, "USE:custom_tool")
	}

	res, ok := reg.RenderResult("custom_tool", nil, "hi", false)
	if !ok {
		t.Fatalf("RenderResult ok = false, want true")
	}
	if res != "RES:custom_tool:hi" {
		t.Fatalf("RenderResult = %q, want %q", res, "RES:custom_tool:hi")
	}

	// Deregister via nil.
	reg.Register("custom_tool", nil)
	if reg.Has("custom_tool") {
		t.Fatalf("Has(custom_tool) after nil = true, want false")
	}
}

// TestToolRendererRegistry_UnknownToolFallsBack verifies that RenderUse
// returns ok=false for unknown tools while still producing non-empty fallback
// output.
func TestToolRendererRegistry_UnknownToolFallsBack(t *testing.T) {
	reg := NewToolRendererRegistry()

	out, ok := reg.RenderUse("unknown_tool", map[string]any{"k": "v"})
	if ok {
		t.Fatalf("RenderUse for unknown tool returned ok=true, want false")
	}
	if out == "" {
		t.Fatalf("RenderUse for unknown tool returned empty fallback output")
	}

	res, ok := reg.RenderResult("unknown_tool", nil, "body", false)
	if ok {
		t.Fatalf("RenderResult for unknown tool returned ok=true, want false")
	}
	if res == "" {
		t.Fatalf("RenderResult for unknown tool returned empty fallback output")
	}
}

// TestDefaultRenderers_HasCommonTools verifies that DefaultRenderers returns
// a registry pre-populated with the tools listed in the task: bash,
// read_file, grep, glob, edit_file, file_edit and web_fetch.
func TestDefaultRenderers_HasCommonTools(t *testing.T) {
	reg := DefaultRenderers()
	wanted := []string{
		"bash",
		"read_file",
		"file_read",
		"grep",
		"search_files",
		"glob",
		"file_edit",
		"edit_file",
		"web_fetch",
	}
	for _, name := range wanted {
		if !reg.Has(name) {
			t.Errorf("DefaultRenderers missing %q", name)
		}
	}

	// Smoke-test that each renders non-empty output for use + result.
	cases := []struct {
		name  string
		input map[string]any
	}{
		{"bash", map[string]any{"command": "ls -la"}},
		{"read_file", map[string]any{"path": "/tmp/foo.txt"}},
		{"grep", map[string]any{"pattern": "TODO"}},
		{"glob", map[string]any{"pattern": "**/*.go"}},
		{"file_edit", map[string]any{"path": "/tmp/x.go", "old_string": "a", "new_string": "b"}},
		{"web_fetch", map[string]any{"url": "https://example.com"}},
	}
	for _, c := range cases {
		out, ok := reg.RenderUse(c.name, c.input)
		if !ok {
			t.Errorf("%s: RenderUse ok=false", c.name)
		}
		if strings.TrimSpace(out) == "" {
			t.Errorf("%s: RenderUse produced empty output", c.name)
		}
		res, ok := reg.RenderResult(c.name, c.input, "hello\nworld\n", false)
		if !ok {
			t.Errorf("%s: RenderResult ok=false", c.name)
		}
		if strings.TrimSpace(res) == "" {
			t.Errorf("%s: RenderResult produced empty output", c.name)
		}
	}
}

// TestRenderWithFallback_UnknownTool verifies the convenience helper returns
// non-empty output for unknown tools and tolerates a nil registry.
func TestRenderWithFallback_UnknownTool(t *testing.T) {
	// nil registry path.
	if out := RenderWithFallback(nil, "ghost", map[string]any{"x": 1}); out == "" {
		t.Fatalf("RenderWithFallback(nil, ...) returned empty")
	}

	reg := DefaultRenderers()
	out := RenderWithFallback(reg, "definitely_not_registered_tool", map[string]any{"foo": "bar"})
	if out == "" {
		t.Fatalf("RenderWithFallback for unknown tool returned empty")
	}
	if !strings.Contains(out, "definitely_not_registered_tool") {
		t.Fatalf("RenderWithFallback output missing tool name: %q", out)
	}

	// Known tool should also yield non-empty output.
	known := RenderWithFallback(reg, "bash", map[string]any{"command": "echo hi"})
	if !strings.Contains(known, "echo hi") {
		t.Fatalf("RenderWithFallback for bash missing command: %q", known)
	}
}

// TestDefaultRenderer_DumpsJSON verifies the standalone DefaultRenderer
// fallback renders both use and result for arbitrary tools.
func TestDefaultRenderer_DumpsJSON(t *testing.T) {
	rd := DefaultRenderer()
	if rd == nil || rd.RenderUse == nil || rd.RenderResult == nil {
		t.Fatalf("DefaultRenderer returned nil callbacks")
	}
	out := rd.RenderUse("mystery", map[string]any{"alpha": 1, "beta": "two"})
	if !strings.Contains(out, "mystery") {
		t.Fatalf("RenderUse missing tool name: %q", out)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Fatalf("RenderUse did not dump input keys: %q", out)
	}

	res := rd.RenderResult("mystery", nil, "payload", true)
	if !strings.Contains(res, "mystery") {
		t.Fatalf("RenderResult missing tool name: %q", res)
	}
	if !strings.Contains(res, "payload") {
		t.Fatalf("RenderResult missing body: %q", res)
	}
}

// TestToolRendererRegistry_ConcurrentAccess stresses the registry with many
// concurrent readers and writers to confirm the sync.RWMutex wiring is sound.
// Runs under `go test -race` in CI.
func TestToolRendererRegistry_ConcurrentAccess(t *testing.T) {
	t.Run("parallel_register_and_lookup", func(t *testing.T) {
		reg := DefaultRenderers()

		const workers = 32
		const iterations = 200

		var wg sync.WaitGroup
		wg.Add(workers)

		for i := 0; i < workers; i++ {
			go func(id int) {
				defer wg.Done()
				for j := 0; j < iterations; j++ {
					// Writer: register a unique renderer.
					key := "tool_" + itoa(id) + "_" + itoa(j)
					reg.Register(key, &ToolRenderer{
						RenderUse: func(name string, _ map[string]any) string {
							return name
						},
					})
					// Reader: look up a known tool.
					if _, ok := reg.RenderUse("bash", map[string]any{"command": "pwd"}); !ok {
						t.Errorf("bash renderer missing during concurrent access")
						return
					}
					// Reader: look up the key we just wrote.
					if !reg.Has(key) {
						t.Errorf("concurrently-registered key %q missing", key)
						return
					}
					// Reader: iterate names.
					_ = reg.Names()
				}
			}(i)
		}

		wg.Wait()
	})
}

// itoa is a tiny local integer formatter used by the concurrency test to
// avoid pulling in strconv in the test file's imports for a single call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
