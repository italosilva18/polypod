// Package commands — batch9_setup_test.go
//
// Tests for the batch9 guided-setup slash commands. All commands in this
// batch emit static Portuguese text, so the tests focus on:
//
//  1. Registration: every expected name (and alias) is wired in.
//  2. Content: each command's display text contains the key fragments the
//     user depends on (checklist items, URLs, setup steps).
//  3. Shape: commands are CommandLocal, have a non-nil Execute handler and
//     never return an error for the happy path.
package commands

import (
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
)

// b9TestCtx returns a minimal CommandContext for the batch9 commands. None
// of them read from disk or the environment, so an empty context is enough.
func b9TestCtx() command.CommandContext {
	return command.CommandContext{
		WorkingDir: "/tmp",
		SessionID:  "b9-test",
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func TestRegisterBatch9Setup_RegistersAll(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch9Setup(reg)

	want := []string{
		"init",
		"onboarding", // alias for /init
		"install-github-app",
		"install-slack-app",
		"install",
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
	}
}

func TestRegisterBatch9Setup_OnboardingIsAliasForInit(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch9Setup(reg)

	initCmd, okInit := reg.Get("init")
	onboardingCmd, okOnb := reg.Get("onboarding")
	if !okInit || !okOnb {
		t.Fatalf("both /init and /onboarding must be registered (init=%v, onboarding=%v)", okInit, okOnb)
	}
	// Same underlying *command.Command pointer: aliases share one struct.
	if initCmd != onboardingCmd {
		t.Errorf("/onboarding should alias /init (same *Command), got distinct pointers")
	}
}

// ---------------------------------------------------------------------------
// /init — onboarding checklist content
// ---------------------------------------------------------------------------

func TestB9CmdInit_ChecklistContent(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch9Setup(reg)

	cmd, ok := reg.Get("init")
	if !ok {
		t.Fatal("/init not registered")
	}
	res, err := cmd.Execute(nil, b9TestCtx())
	if err != nil {
		t.Fatalf("/init returned error: %v", err)
	}
	if res == nil || res.Display == "" {
		t.Fatal("/init returned empty result")
	}

	out := res.Display
	// Each of the four checklist sections must be mentioned. Assertions are
	// Portuguese-aware fragments rather than full sentences so small wording
	// tweaks do not break the suite unnecessarily.
	fragments := []string{
		"configuracao",      // 1. config file
		"config.yaml",       // concrete artifact
		"API",               // 2. API key
		"MCP",               // 3. first MCP server
		"Primeira execucao", // 4. first run
		"polypod",           // invocation hint
	}
	for _, f := range fragments {
		if !strings.Contains(out, f) {
			t.Errorf("/init output missing fragment %q.\nGot:\n%s", f, out)
		}
	}
}

func TestB9CmdInit_OnboardingAliasReturnsSameChecklist(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch9Setup(reg)

	initCmd, _ := reg.Get("init")
	onbCmd, _ := reg.Get("onboarding")

	r1, err := initCmd.Execute(nil, b9TestCtx())
	if err != nil {
		t.Fatalf("/init: %v", err)
	}
	r2, err := onbCmd.Execute(nil, b9TestCtx())
	if err != nil {
		t.Fatalf("/onboarding: %v", err)
	}
	if r1.Display != r2.Display {
		t.Errorf("/onboarding should return the same text as /init")
	}
}

// ---------------------------------------------------------------------------
// /install-github-app
// ---------------------------------------------------------------------------

func TestB9CmdInstallGitHubApp_ContainsURLAndSteps(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch9Setup(reg)

	cmd, ok := reg.Get("install-github-app")
	if !ok {
		t.Fatal("/install-github-app not registered")
	}
	res, err := cmd.Execute(nil, b9TestCtx())
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	out := res.Display

	if !strings.Contains(out, b9GitHubAppURL) {
		t.Errorf("output missing GitHub App URL %q.\nGot:\n%s", b9GitHubAppURL, out)
	}
	for _, frag := range []string{"GitHub App", "Passos", "installation_id", "POLYPOD_GITHUB_TOKEN"} {
		if !strings.Contains(out, frag) {
			t.Errorf("output missing fragment %q.\nGot:\n%s", frag, out)
		}
	}
}

// ---------------------------------------------------------------------------
// /install-slack-app
// ---------------------------------------------------------------------------

func TestB9CmdInstallSlackApp_ContainsURLAndSteps(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch9Setup(reg)

	cmd, ok := reg.Get("install-slack-app")
	if !ok {
		t.Fatal("/install-slack-app not registered")
	}
	res, err := cmd.Execute(nil, b9TestCtx())
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	out := res.Display

	if !strings.Contains(out, b9SlackAppURL) {
		t.Errorf("output missing Slack App URL %q.\nGot:\n%s", b9SlackAppURL, out)
	}
	for _, frag := range []string{"Slack", "workspace", "xoxb-", "POLYPOD_SLACK_BOT_TOKEN"} {
		if !strings.Contains(out, frag) {
			t.Errorf("output missing fragment %q.\nGot:\n%s", frag, out)
		}
	}
}

// ---------------------------------------------------------------------------
// /install — meta menu listing integrations
// ---------------------------------------------------------------------------

func TestB9CmdInstall_ListsAllIntegrations(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch9Setup(reg)

	cmd, ok := reg.Get("install")
	if !ok {
		t.Fatal("/install not registered")
	}
	res, err := cmd.Execute(nil, b9TestCtx())
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	out := res.Display

	// The meta menu must reference every child install command by name so
	// users can copy-paste them directly into the prompt.
	fragments := []string{
		"Integracoes",
		"GitHub App",
		"/install-github-app",
		"Slack App",
		"/install-slack-app",
		"/init",
	}
	for _, f := range fragments {
		if !strings.Contains(out, f) {
			t.Errorf("/install output missing fragment %q.\nGot:\n%s", f, out)
		}
	}
}
