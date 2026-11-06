// Package commands — batch6_context_test.go
// Smoke tests for /compact, /snip, /context placeholder behavior.
//
// These commands are "queued pending wiring": until command.CommandContext
// exposes CompactService / BudgetTracker, they must return Portuguese
// placeholder text instead of erroring. The tests lock that contract so
// round-3 wiring work doesn't accidentally regress the fallback path.
package commands

import (
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
)

// newBatch6Ctx returns an empty CommandContext. /compact, /snip, and
// /context must be safe to call without any real session state.
func newBatch6Ctx() command.CommandContext {
	return command.CommandContext{
		WorkingDir: ".",
		SessionID:  "batch6-test-session",
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func TestRegisterBatch6Context_RegistersAll(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch6Context(reg)

	for _, name := range []string{"compact", "snip", "context"} {
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
	}

	// /context should also resolve via the "ctx" alias.
	if _, ok := reg.Get("ctx"); !ok {
		t.Error("alias 'ctx' not registered for /context")
	}
}

// ---------------------------------------------------------------------------
// /compact placeholder
// ---------------------------------------------------------------------------

func TestCmdCompact_NoArgs_ReturnsQueuedPlaceholder(t *testing.T) {
	ctx := newBatch6Ctx()

	res, err := cmdCompact().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("cmdCompact error: %v", err)
	}
	if res == nil || res.Display == "" {
		t.Fatal("cmdCompact returned empty display")
	}

	// Must mention the queued/pending-wiring state in Portuguese.
	if !strings.Contains(res.Display, "Compactacao solicitada") {
		t.Errorf("missing 'Compactacao solicitada' in output: %s", res.Display)
	}
	if !strings.Contains(res.Display, "ainda nao esta ligado") {
		t.Errorf("missing pending-wiring wording: %s", res.Display)
	}
	// Session id should be echoed back so users know which session the
	// placeholder corresponds to.
	if !strings.Contains(res.Display, "batch6-test-session") {
		t.Errorf("session id not echoed: %s", res.Display)
	}
}

func TestCmdCompact_WithInstructions_EchoesThem(t *testing.T) {
	ctx := newBatch6Ctx()

	// Simulate `/compact focus on the git history`.
	res, err := cmdCompact().Execute(
		[]string{"focus", "on", "the", "git", "history"},
		ctx,
	)
	if err != nil {
		t.Fatalf("cmdCompact error: %v", err)
	}
	if res == nil || res.Display == "" {
		t.Fatal("cmdCompact returned empty display")
	}
	if !strings.Contains(res.Display, "focus on the git history") {
		t.Errorf("custom instructions not echoed: %s", res.Display)
	}
	if !strings.Contains(res.Display, "Instrucoes personalizadas") {
		t.Errorf("missing custom-instructions header: %s", res.Display)
	}
}

func TestCmdCompact_QuotedInstructions_StripsQuotes(t *testing.T) {
	ctx := newBatch6Ctx()

	// Simulate `/compact "focus on the git history"` arriving as a single
	// pre-quoted arg from the parser.
	res, err := cmdCompact().Execute(
		[]string{`"focus on the git history"`},
		ctx,
	)
	if err != nil {
		t.Fatalf("cmdCompact error: %v", err)
	}
	if !strings.Contains(res.Display, `"focus on the git history"`) {
		t.Errorf("expected quoted-then-unquoted instructions in echo: %s", res.Display)
	}
	// The raw surrounding double-quotes should have been stripped before
	// being echoed back inside %q (which re-adds them cleanly).
	if strings.Contains(res.Display, `\"focus`) {
		t.Errorf("inner quotes should not be escape-doubled: %s", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /snip placeholder
// ---------------------------------------------------------------------------

func TestCmdSnip_ReturnsQueuedPlaceholder(t *testing.T) {
	ctx := newBatch6Ctx()

	res, err := cmdSnip().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("cmdSnip error: %v", err)
	}
	if res == nil || res.Display == "" {
		t.Fatal("cmdSnip returned empty display")
	}
	if !strings.Contains(res.Display, "Snip solicitado") {
		t.Errorf("missing 'Snip solicitado' header: %s", res.Display)
	}
	if !strings.Contains(res.Display, "ainda nao esta ligado") {
		t.Errorf("missing pending-wiring wording: %s", res.Display)
	}
	// Mentions the default policy so users understand what /snip will do
	// once wired.
	if !strings.Contains(res.Display, "ultimos 5") {
		t.Errorf("default policy not described: %s", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /context placeholder
// ---------------------------------------------------------------------------

func TestCmdContext_ReturnsPlaceholder(t *testing.T) {
	ctx := newBatch6Ctx()

	res, err := cmdContext().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("cmdContext error: %v", err)
	}
	if res == nil || res.Display == "" {
		t.Fatal("cmdContext returned empty display")
	}
	if !strings.Contains(res.Display, "Sem tracker de orcamento") {
		t.Errorf("missing placeholder header: %s", res.Display)
	}

	// The task spec mandates ● ○ ◐ markers in the output. In placeholder
	// mode the bar is all-empty, so at least the ○ marker must appear.
	if !strings.Contains(res.Display, "\u25CB") {
		t.Errorf("empty marker (○) missing from bar: %s", res.Display)
	}

	// The placeholder should still mention the compaction-history counter
	// so callers know where that data will appear.
	if !strings.Contains(res.Display, "Compactacoes") {
		t.Errorf("compaction-count row missing: %s", res.Display)
	}
}

// ---------------------------------------------------------------------------
// renderBudgetBar helper
// ---------------------------------------------------------------------------

func TestRenderBudgetBar_ZeroLimit_AllEmpty(t *testing.T) {
	got := renderBudgetBar(0, 0)
	// 20 empty cells wrapped in brackets.
	want := "[" + strings.Repeat("\u25CB", 20) + "]"
	if got != want {
		t.Errorf("zero-limit bar: got %q, want %q", got, want)
	}
}

func TestRenderBudgetBar_Half_RendersFullAndEmpty(t *testing.T) {
	got := renderBudgetBar(50, 100)
	// Should contain ● (full cells) and ○ (empty cells).
	if !strings.Contains(got, "\u25CF") {
		t.Errorf("50%% bar missing full marker: %q", got)
	}
	if !strings.Contains(got, "\u25CB") {
		t.Errorf("50%% bar missing empty marker: %q", got)
	}
}

func TestRenderBudgetBar_Full_AllFilled(t *testing.T) {
	got := renderBudgetBar(100, 100)
	want := "[" + strings.Repeat("\u25CF", 20) + "]"
	if got != want {
		t.Errorf("full bar: got %q, want %q", got, want)
	}
}
