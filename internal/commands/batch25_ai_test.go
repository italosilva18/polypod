// Package commands — batch25_ai_test.go
//
// Smoke tests for the batch25 AI helper commands: /model-compare,
// /provider-switch and /token-count.
package commands

import (
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
)

// newBatch25Ctx returns a CommandContext suitable for batch25 tests.
func newBatch25Ctx(sessionID string) command.CommandContext {
	return command.CommandContext{
		WorkingDir: ".",
		SessionID:  sessionID,
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func TestRegisterBatch25AI_RegistersAllCommands(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch25AI(reg)

	for _, name := range []string{"model-compare", "provider-switch", "token-count"} {
		cmd, ok := reg.Get(name)
		if !ok {
			t.Fatalf("command %q not registered", name)
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
	}
}

// ---------------------------------------------------------------------------
// /model-compare & /provider-switch — placeholder strings
// ---------------------------------------------------------------------------

func TestCmdModelCompareB25_Placeholder(t *testing.T) {
	res, err := cmdModelCompareB25().Execute(nil, newBatch25Ctx("batch25-model-compare-session"))
	if err != nil {
		t.Fatalf("cmdModelCompareB25 error: %v", err)
	}
	if res == nil {
		t.Fatal("cmdModelCompareB25 returned nil result")
	}
	if res.Display != modelComparePlaceholderMessage {
		t.Errorf("unexpected /model-compare placeholder: got %q, want %q",
			res.Display, modelComparePlaceholderMessage)
	}
}

func TestCmdProviderSwitchB25_Placeholder(t *testing.T) {
	res, err := cmdProviderSwitchB25().Execute(
		[]string{"openai"},
		newBatch25Ctx("batch25-provider-switch-session"),
	)
	if err != nil {
		t.Fatalf("cmdProviderSwitchB25 error: %v", err)
	}
	if res == nil {
		t.Fatal("cmdProviderSwitchB25 returned nil result")
	}
	if res.Display != providerSwitchPlaceholderMessage {
		t.Errorf("unexpected /provider-switch placeholder: got %q, want %q",
			res.Display, providerSwitchPlaceholderMessage)
	}
}

// ---------------------------------------------------------------------------
// /token-count — heuristic helper
// ---------------------------------------------------------------------------

// TestEstimateTokensB25_Heuristic locks in the len/4 rule the TypeScript
// reference implementation uses. Edge cases (empty string, short string,
// exact multiple-of-four, and a typical sentence) are covered.
func TestEstimateTokensB25_Heuristic(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int
	}{
		{"empty", "", 0},
		{"three_chars", "abc", 0},                // 3/4 = 0
		{"four_chars", "abcd", 1},                // 4/4 = 1
		{"eight_chars", "abcdefgh", 2},           // 8/4 = 2
		{"sixteen_chars", "hello world!!!!!", 4}, // 16/4 = 4
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := estimateTokensB25(tc.text)
			if got != tc.want {
				t.Errorf("estimateTokensB25(%q) = %d, want %d", tc.text, got, tc.want)
			}
		})
	}
}

func TestCmdTokenCountB25_NoArgsPrintsHelp(t *testing.T) {
	res, err := cmdTokenCountB25().Execute(nil, newBatch25Ctx("batch25-token-count-help-session"))
	if err != nil {
		t.Fatalf("cmdTokenCountB25 no-args error: %v", err)
	}
	if res == nil {
		t.Fatal("cmdTokenCountB25 no-args returned nil result")
	}
	if res.Display != tokenCountHelpMessage {
		t.Errorf("unexpected /token-count help: got %q, want %q",
			res.Display, tokenCountHelpMessage)
	}
}

func TestCmdTokenCountB25_WithText(t *testing.T) {
	// "hello world" is 11 characters → 11/4 = 2 tokens.
	res, err := cmdTokenCountB25().Execute(
		[]string{"hello", "world"},
		newBatch25Ctx("batch25-token-count-session"),
	)
	if err != nil {
		t.Fatalf("cmdTokenCountB25 with text error: %v", err)
	}
	if res == nil {
		t.Fatal("cmdTokenCountB25 with text returned nil result")
	}
	if !strings.Contains(res.Display, "~2 tokens") {
		t.Errorf("expected display to contain %q, got %q", "~2 tokens", res.Display)
	}
	if !strings.Contains(res.Display, "11 caracteres") {
		t.Errorf("expected display to contain %q, got %q", "11 caracteres", res.Display)
	}
}
