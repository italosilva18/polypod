// Package commands — batch17_trace_test.go
//
// Tests for batch17 trace/remote/context-viz commands.
package commands

import (
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
	"github.com/costa/polypod/internal/tokenbudget"
)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func TestRegisterBatch17Trace_RegistersAll(t *testing.T) {
	resetObservabilityState(t)
	reg := command.NewRegistry()
	RegisterBatch17Trace(reg)

	for _, name := range []string{"remote-trigger", "ant-trace", "ctx_viz"} {
		cmd, ok := reg.Get(name)
		if !ok {
			t.Errorf("command %q not registered", name)
			continue
		}
		if cmd.Execute == nil {
			t.Errorf("command %q has nil Execute", name)
		}
		if cmd.Description == "" {
			t.Errorf("command %q has empty description", name)
		}
		if cmd.Type != command.CommandLocal {
			t.Errorf("command %q: expected CommandLocal, got %q", name, cmd.Type)
		}
	}
}

// ---------------------------------------------------------------------------
// /remote-trigger
// ---------------------------------------------------------------------------

func TestB17CmdRemoteTrigger_Placeholder(t *testing.T) {
	resetObservabilityState(t)
	cmd := b17CmdRemoteTrigger()
	if cmd.Execute == nil {
		t.Fatal("nil Execute")
	}

	// No argument: pure placeholder.
	res, err := cmd.Execute(nil, command.CommandContext{WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Display, "remote trigger pending wiring") {
		t.Errorf("expected placeholder text, got: %q", res.Display)
	}
	if !strings.Contains(res.Display, "internal/remote/") {
		t.Errorf("expected wiring hint, got: %q", res.Display)
	}

	// With an argument: placeholder echoes the requested target.
	res, err = cmd.Execute([]string{"nightly"}, command.CommandContext{})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Display, "nightly") {
		t.Errorf("expected echoed target 'nightly', got: %q", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /ant-trace
// ---------------------------------------------------------------------------

func TestB17CmdAntTrace_Placeholder(t *testing.T) {
	resetObservabilityState(t)
	cmd := b17CmdAntTrace()
	if cmd.Execute == nil {
		t.Fatal("nil Execute")
	}
	res, err := cmd.Execute(nil, command.CommandContext{})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Display, "ant trace profiling pending wiring") {
		t.Errorf("expected placeholder text, got: %q", res.Display)
	}
	if !strings.Contains(res.Display, "runtime/trace") {
		t.Errorf("expected runtime/trace hint, got: %q", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /ctx_viz — no tracker → Portuguese placeholder
// ---------------------------------------------------------------------------

func TestB17CmdCtxViz_NoTracker_Placeholder(t *testing.T) {
	resetObservabilityState(t)
	cmd := b17CmdCtxViz()
	if cmd.Execute == nil {
		t.Fatal("nil Execute")
	}
	res, err := cmd.Execute(nil, command.CommandContext{})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Display, "Nenhum rastreamento") {
		t.Errorf("expected Portuguese placeholder, got: %q", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /ctx_viz — with tracker → ASCII bars
// ---------------------------------------------------------------------------

func TestB17CmdCtxViz_WithTracker_RendersBars(t *testing.T) {
	resetObservabilityState(t)

	tr := tokenbudget.NewTracker(tokenbudget.TrackerConfig{Limit: 100_000})
	tr.RecordTurn(tokenbudget.TurnStats{
		Prompt:     20_000,
		Completion: 10_000,
		ToolResult: 10_000,
	})
	SetObservabilityTracker(tr)

	cmd := b17CmdCtxViz()
	res, err := cmd.Execute(nil, command.CommandContext{})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	out := res.Display

	// Header must be present.
	if !strings.Contains(out, "Visualizacao da janela de contexto") {
		t.Errorf("missing header in output:\n%s", out)
	}

	// Filled and empty bar runes must both appear because 40% usage leaves
	// space on either side of the bar.
	if !strings.Contains(out, "\u2588") {
		t.Errorf("missing filled bar rune in output:\n%s", out)
	}
	if !strings.Contains(out, "\u2591") {
		t.Errorf("missing empty bar rune in output:\n%s", out)
	}

	// 40 000 / 100 000 = 40.0% — format uses "%5.1f%%" so "40.0%" shows up.
	if !strings.Contains(out, "40.0%") {
		t.Errorf("expected '40.0%%' in overall bar, got:\n%s", out)
	}

	// Per-category section header plus at least one category name.
	if !strings.Contains(out, "Por categoria") {
		t.Errorf("missing per-category section, got:\n%s", out)
	}
	if !strings.Contains(out, "prompt") {
		t.Errorf("expected 'prompt' category in output, got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// b17Bar — pure unit test of the bar renderer
// ---------------------------------------------------------------------------

func TestB17Bar_FillRatios(t *testing.T) {
	cases := []struct {
		name   string
		ratio  float64
		width  int
		filled int
	}{
		{"empty", 0.0, 10, 0},
		{"forty", 0.4, 10, 4},
		{"full", 1.0, 10, 10},
		{"over", 2.0, 10, 10},   // clamped
		{"negative", -1, 10, 0}, // clamped
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := b17Bar(tc.ratio, tc.width)
			if !strings.HasPrefix(out, "[") || !strings.HasSuffix(out, "]") {
				t.Errorf("bar missing brackets: %q", out)
			}
			got := strings.Count(out, "\u2588")
			if got != tc.filled {
				t.Errorf("ratio=%.2f width=%d: expected %d filled, got %d (%q)",
					tc.ratio, tc.width, tc.filled, got, out)
			}
			want := tc.width - tc.filled
			empty := strings.Count(out, "\u2591")
			if empty != want {
				t.Errorf("ratio=%.2f width=%d: expected %d empty, got %d (%q)",
					tc.ratio, tc.width, want, empty, out)
			}
		})
	}
}
