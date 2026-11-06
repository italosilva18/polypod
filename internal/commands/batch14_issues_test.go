// Package commands — batch14_issues_test.go
//
// Tests for the batch14 issue/advisor/bughunter/insights-adv placeholders.
package commands

import (
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
)

// b14TestCtx returns a minimal CommandContext for exercising the batch14
// handlers. These commands don't touch disk, so a bare context is enough.
func b14TestCtx() command.CommandContext {
	return command.CommandContext{
		WorkingDir: ".",
		SessionID:  "sess-batch14",
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func TestRegisterBatch14Issues_RegistersAll(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch14Issues(reg)

	want := []string{"issue", "advisor", "bughunter", "insights-adv"}
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
	}

	// /insights-adv is the collision-renamed variant; the plain /insights
	// must NOT have been clobbered here (batch14 should not touch it).
	// We can't verify the *previous* /insights handler since batch1/batch5
	// aren't registered in this test, but we can at least confirm batch14
	// did not register a command under the bare "insights" name.
	if _, ok := reg.Get("insights"); ok {
		t.Errorf("batch14 must not register plain /insights (collision), "+
			"but registry.Get(%q) returned a command", "insights")
	}
}

// ---------------------------------------------------------------------------
// /issue
// ---------------------------------------------------------------------------

func TestB14CmdIssue_NoArgsPrintsUsage(t *testing.T) {
	cmd := b14CmdIssue()
	res, err := cmd.Execute(nil, b14TestCtx())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Display, "uso: /issue") {
		t.Errorf("expected usage string, got %q", res.Display)
	}
}

func TestB14CmdIssue_SearchPrintsIssuesURL(t *testing.T) {
	cmd := b14CmdIssue()
	res, err := cmd.Execute([]string{"search"}, b14TestCtx())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Display, "github.com/anthropics/claude-code/issues") {
		t.Errorf("expected upstream issues URL in output, got %q", res.Display)
	}
}

func TestB14CmdIssue_SearchWithQueryEscapesSpaces(t *testing.T) {
	cmd := b14CmdIssue()
	res, err := cmd.Execute([]string{"search", "memory", "leak"}, b14TestCtx())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Display, "memory+leak") {
		t.Errorf("expected spaces to be converted to '+', got %q", res.Display)
	}
	if !strings.Contains(res.Display, "is%3Aissue") {
		t.Errorf("expected escaped is:issue filter in URL, got %q", res.Display)
	}
}

func TestB14CmdIssue_NewPrintsNewURL(t *testing.T) {
	cmd := b14CmdIssue()
	res, err := cmd.Execute([]string{"new"}, b14TestCtx())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(res.Display, "/issues/new") {
		t.Errorf("expected /issues/new URL suffix, got %q", res.Display)
	}
}

func TestB14CmdIssue_InvalidSubcommandErrors(t *testing.T) {
	cmd := b14CmdIssue()
	res, err := cmd.Execute([]string{"nuke"}, b14TestCtx())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(res.Display, "erro:") {
		t.Errorf("expected erro: prefix for invalid subcommand, got %q", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /advisor
// ---------------------------------------------------------------------------

func TestB14CmdAdvisor_ReturnsPlaceholder(t *testing.T) {
	cmd := b14CmdAdvisor()
	res, err := cmd.Execute(nil, b14TestCtx())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Display != "advisor pending wiring" {
		t.Errorf("expected placeholder, got %q", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /bughunter
// ---------------------------------------------------------------------------

func TestB14CmdBughunter_ReturnsPlaceholder(t *testing.T) {
	cmd := b14CmdBughunter()
	res, err := cmd.Execute(nil, b14TestCtx())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Display != "bughunter pending wiring" {
		t.Errorf("expected placeholder, got %q", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /insights-adv
// ---------------------------------------------------------------------------

func TestB14CmdInsightsAdv_MentionsCollisionAndUsesPackage(t *testing.T) {
	cmd := b14CmdInsightsAdv()
	res, err := cmd.Execute(nil, b14TestCtx())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Must explain why it's named /insights-adv.
	if !strings.Contains(res.Display, "insights-adv") {
		t.Errorf("expected collision notice mentioning /insights-adv, got %q", res.Display)
	}
	// FormatQuickInsights always starts with this Portuguese header.
	if !strings.Contains(res.Display, "Insights da sessao") {
		t.Errorf("expected insights package header, got %q", res.Display)
	}
}
