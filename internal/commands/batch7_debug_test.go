// Package commands — batch7_debug_test.go
// Smoke tests for the batch-7 debug / diagnostics commands.
//
// These tests verify three things:
//  1. RegisterBatch7Debug wires every command into the registry as
//     a CommandLocal handler with a non-nil Execute callback.
//  2. /perf-issue and /heapdump succeed against a real (test) runtime.
//  3. /debug-tool-call actually flips the package-level toggle.
package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
)

// newBatch7Ctx returns a CommandContext rooted at a temp directory so
// /heapdump can write its output without touching the repo.
func newBatch7Ctx(t *testing.T) command.CommandContext {
	t.Helper()
	return command.CommandContext{
		WorkingDir: t.TempDir(),
		SessionID:  "batch7-test-session",
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func TestRegisterBatch7Debug_RegistersAll(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch7Debug(reg)

	want := []string{
		"heapdump",
		"perf-issue",
		"debug-tool-call",
		"break-cache",
		"mock-limits",
		"reset-limits",
	}

	for _, name := range want {
		cmd, ok := reg.Get(name)
		if !ok {
			t.Errorf("command %q not registered", name)
			continue
		}
		if cmd.Execute == nil {
			t.Errorf("command %q has nil Execute handler", name)
		}
		if cmd.Type != command.CommandLocal {
			t.Errorf("command %q: expected CommandLocal, got %q", name, cmd.Type)
		}
		if cmd.Source != command.SourceBuiltin {
			t.Errorf("command %q: expected SourceBuiltin, got %q", name, cmd.Source)
		}
		if cmd.Description == "" {
			t.Errorf("command %q has empty description", name)
		}
	}

	// Sanity-check the placeholder trio still emits the agreed-upon
	// English wire strings (the public contract callers can grep for).
	cases := map[string]string{
		"break-cache":  "cache break queued (wiring pending)",
		"mock-limits":  "mock limits enabled",
		"reset-limits": "limits reset",
	}
	ctx := newBatch7Ctx(t)
	for name, want := range cases {
		cmd, _ := reg.Get(name)
		res, err := cmd.Execute(nil, ctx)
		if err != nil {
			t.Errorf("/%s execute error: %v", name, err)
			continue
		}
		if res == nil || res.Display != want {
			got := ""
			if res != nil {
				got = res.Display
			}
			t.Errorf("/%s display = %q, want %q", name, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// /perf-issue
// ---------------------------------------------------------------------------

func TestCmdPerfIssue_DumpsRuntimeStats(t *testing.T) {
	ctx := newBatch7Ctx(t)

	res, err := cmdPerfIssue().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("cmdPerfIssue error: %v", err)
	}
	if res == nil || res.Display == "" {
		t.Fatal("cmdPerfIssue returned empty display")
	}

	// The output is in Portuguese and should mention the key sections.
	for _, want := range []string{
		"Diagnostico de desempenho",
		"Goroutines ativas",
		"GOMAXPROCS",
		"Memoria (MiB)",
		"HeapAlloc",
		"NumGC",
	} {
		if !strings.Contains(res.Display, want) {
			t.Errorf("missing %q in /perf-issue output:\n%s", want, res.Display)
		}
	}
}

// ---------------------------------------------------------------------------
// /heapdump
// ---------------------------------------------------------------------------

func TestCmdHeapdump_WritesProfileFile(t *testing.T) {
	ctx := newBatch7Ctx(t)

	// Explicit relative target so we can assert on the resulting path.
	target := "diag/heap.pprof"
	res, err := cmdHeapdump().Execute([]string{target}, ctx)
	if err != nil {
		t.Fatalf("cmdHeapdump error: %v", err)
	}
	if res == nil || res.Display == "" {
		t.Fatal("cmdHeapdump returned empty display")
	}

	// File should exist and be non-empty.
	full := filepath.Join(ctx.WorkingDir, target)
	info, err := os.Stat(full)
	if err != nil {
		t.Fatalf("expected heap profile at %s: %v", full, err)
	}
	if info.Size() == 0 {
		t.Errorf("heap profile %s is empty", full)
	}

	if !strings.Contains(res.Display, "Heap profile gravado em") {
		t.Errorf("missing 'Heap profile gravado em' in output: %s", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /debug-tool-call
// ---------------------------------------------------------------------------

func TestCmdDebugToolCall_TogglesFlag(t *testing.T) {
	// Reset the package flag so this test is order-independent.
	t.Cleanup(func() { setDebugToolCalls(false) })
	setDebugToolCalls(false)

	ctx := newBatch7Ctx(t)
	cmd := cmdDebugToolCall()

	// First call: false -> true.
	res, err := cmd.Execute(nil, ctx)
	if err != nil {
		t.Fatalf("toggle 1 error: %v", err)
	}
	if !DebugToolCallsEnabled() {
		t.Error("expected debugToolCalls true after first toggle")
	}
	if !strings.Contains(res.Display, "LIGADO") {
		t.Errorf("expected 'LIGADO' in output, got: %s", res.Display)
	}

	// Second call: true -> false.
	res, err = cmd.Execute(nil, ctx)
	if err != nil {
		t.Fatalf("toggle 2 error: %v", err)
	}
	if DebugToolCallsEnabled() {
		t.Error("expected debugToolCalls false after second toggle")
	}
	if !strings.Contains(res.Display, "DESLIGADO") {
		t.Errorf("expected 'DESLIGADO' in output, got: %s", res.Display)
	}

	// Explicit "on" form.
	res, err = cmd.Execute([]string{"on"}, ctx)
	if err != nil {
		t.Fatalf("explicit on error: %v", err)
	}
	if !DebugToolCallsEnabled() {
		t.Error("expected debugToolCalls true after explicit 'on'")
	}
	if !strings.Contains(res.Display, "LIGADO") {
		t.Errorf("expected 'LIGADO' for explicit on, got: %s", res.Display)
	}

	// Invalid value should not change state.
	prev := DebugToolCallsEnabled()
	res, err = cmd.Execute([]string{"banana"}, ctx)
	if err != nil {
		t.Fatalf("invalid value error: %v", err)
	}
	if DebugToolCallsEnabled() != prev {
		t.Error("invalid value must not change debugToolCalls state")
	}
	if !strings.Contains(res.Display, "valor invalido") {
		t.Errorf("expected 'valor invalido' for bad input, got: %s", res.Display)
	}
}
