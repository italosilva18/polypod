// Package commands — batch19_misc_test.go
//
// Smoke tests for the batch19 miscellaneous commands. /btw, /feedback and
// /fast are intentionally covered by batch1_test.go (they live in batch1.go
// per the skip-duplicates rule), so these tests only exercise /extra-usage
// plus the registration shape.
package commands

import (
	"testing"

	"github.com/costa/polypod/internal/command"
)

// newBatch19Ctx returns a CommandContext suitable for batch19 tests.
func newBatch19Ctx(sessionID string) command.CommandContext {
	return command.CommandContext{
		WorkingDir: ".",
		SessionID:  sessionID,
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func TestRegisterBatch19Misc_RegistersExtraUsage(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch19Misc(reg)

	cmd, ok := reg.Get("extra-usage")
	if !ok {
		t.Fatalf("command %q not registered", "extra-usage")
	}
	if cmd.Execute == nil {
		t.Errorf("command %q has nil Execute handler", "extra-usage")
	}
	if cmd.Type != command.CommandLocal {
		t.Errorf("command %q: expected CommandLocal, got %q", "extra-usage", cmd.Type)
	}
	if cmd.Source != command.SourceBuiltin {
		t.Errorf("command %q: expected SourceBuiltin, got %q", "extra-usage", cmd.Source)
	}
}

// TestRegisterBatch19Misc_SkipsDuplicates verifies that /btw, /fast and
// /feedback are NOT claimed by batch19 (they belong to batch1). Registering
// only batch19 on a fresh registry should leave those names unclaimed.
func TestRegisterBatch19Misc_SkipsDuplicates(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch19Misc(reg)

	for _, name := range []string{"btw", "fast", "feedback"} {
		if _, ok := reg.Get(name); ok {
			t.Errorf("batch19 should NOT register %q (lives in batch1.go)", name)
		}
	}
}

// ---------------------------------------------------------------------------
// /extra-usage — placeholder string
// ---------------------------------------------------------------------------

func TestCmdExtraUsageB19_Placeholder(t *testing.T) {
	res, err := cmdExtraUsageB19().Execute(nil, newBatch19Ctx("batch19-extra-usage-session"))
	if err != nil {
		t.Fatalf("cmdExtraUsageB19 error: %v", err)
	}
	if res == nil {
		t.Fatal("cmdExtraUsageB19 returned nil result")
	}
	if res.Display != extraUsagePlaceholderMessage {
		t.Errorf("unexpected /extra-usage placeholder: got %q, want %q",
			res.Display, extraUsagePlaceholderMessage)
	}
}

// TestCmdExtraUsageB19_IgnoresArgs locks in that extra arguments do not
// change the placeholder output (the real implementation will likely parse
// them, but the placeholder must stay stable so callers can depend on the
// message while wiring is in progress).
func TestCmdExtraUsageB19_IgnoresArgs(t *testing.T) {
	res, err := cmdExtraUsageB19().Execute(
		[]string{"detailed", "--json"},
		newBatch19Ctx("batch19-extra-usage-args-session"),
	)
	if err != nil {
		t.Fatalf("cmdExtraUsageB19 with args error: %v", err)
	}
	if res == nil || res.Display != extraUsagePlaceholderMessage {
		t.Errorf("unexpected /extra-usage placeholder with args: %q", res.Display)
	}
}
