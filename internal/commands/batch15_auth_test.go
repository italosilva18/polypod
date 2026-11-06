// Package commands — batch15_auth_test.go
//
// Tests for the batch15 authentication slash commands. These tests are
// intentionally hermetic: they never touch the real $HOME or OS env beyond
// what t.Setenv scopes to the individual test.
package commands

import (
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
)

// b15TestCtx returns a CommandContext suitable for batch15 tests. It also
// unsets any inherited POLYPOD_TELEMETRY so tests start from a known state.
func b15TestCtx(t *testing.T) command.CommandContext {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("POLYPOD_CONFIG_DIR", tmp)
	return command.CommandContext{
		WorkingDir: tmp,
		SessionID:  "batch15-test",
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func TestRegisterBatch15Auth_RegistersNewCommandsOnly(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch15Auth(reg)

	// /oauth-refresh is the only new command introduced by batch15 — the
	// classic /login, /logout and /privacy-settings already live in batch1
	// and must NOT be re-registered here (collisions are catastrophic).
	cmd, ok := reg.Get("oauth-refresh")
	if !ok {
		t.Fatalf("command %q not registered", "oauth-refresh")
	}
	if cmd.Execute == nil {
		t.Errorf("command %q has nil Execute handler", "oauth-refresh")
	}
	if cmd.Type != command.CommandLocal {
		t.Errorf("command %q: expected CommandLocal, got %q", "oauth-refresh", cmd.Type)
	}
	if cmd.Source != command.SourceBuiltin {
		t.Errorf("command %q: expected SourceBuiltin, got %q", "oauth-refresh", cmd.Source)
	}
}

func TestRegisterBatch15Auth_DoesNotClashWithBatch1(t *testing.T) {
	// Registering batch1 first and then batch15 must not panic or overwrite
	// any of the batch1 auth commands. This is the key invariant: batch15
	// is additive-only with respect to /login, /logout and /privacy-settings.
	reg := command.NewRegistry()
	RegisterBatch1(reg)

	preLogin, loginOK := reg.Get("login")
	preLogout, logoutOK := reg.Get("logout")
	prePrivacy, privacyOK := reg.Get("privacy-settings")
	if !loginOK || !logoutOK || !privacyOK {
		t.Fatalf("sanity check: batch1 should already own /login, /logout, /privacy-settings")
	}

	// Now register batch15 on top of batch1.
	RegisterBatch15Auth(reg)

	postLogin, _ := reg.Get("login")
	postLogout, _ := reg.Get("logout")
	postPrivacy, _ := reg.Get("privacy-settings")

	if postLogin != preLogin {
		t.Errorf("/login command was overwritten by batch15 — collision!")
	}
	if postLogout != preLogout {
		t.Errorf("/logout command was overwritten by batch15 — collision!")
	}
	if postPrivacy != prePrivacy {
		t.Errorf("/privacy-settings command was overwritten by batch15 — collision!")
	}

	// And /oauth-refresh must now exist (it is the only new thing).
	if _, ok := reg.Get("oauth-refresh"); !ok {
		t.Errorf("/oauth-refresh not registered after RegisterBatch15Auth")
	}
}

// ---------------------------------------------------------------------------
// /oauth-refresh
// ---------------------------------------------------------------------------

func TestB15CmdOauthRefresh_ReturnsPlaceholder(t *testing.T) {
	ctx := b15TestCtx(t)

	res, err := b15CmdOauthRefresh().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil result")
	}
	if res.Display == "" {
		t.Fatalf("expected non-empty Display")
	}
	// The placeholder must mention oauth_flow.go so users and docs can grep
	// for the wiring hook.
	if !strings.Contains(res.Display, "oauth_flow.go") {
		t.Errorf("expected placeholder to reference oauth_flow.go, got: %s", res.Display)
	}
	// And it must be phrased in Portuguese (project convention).
	if !strings.Contains(strings.ToLower(res.Display), "pendente") {
		t.Errorf("expected Portuguese placeholder wording (\"pendente\"), got: %s", res.Display)
	}
}

func TestB15CmdOauthRefresh_IgnoresExtraArgs(t *testing.T) {
	ctx := b15TestCtx(t)

	// Calling with arbitrary args should still return the placeholder and
	// must never return a non-nil error.
	for _, args := range [][]string{
		nil,
		{},
		{"--force"},
		{"foo", "bar", "baz"},
	} {
		res, err := b15CmdOauthRefresh().Execute(args, ctx)
		if err != nil {
			t.Errorf("args=%v: unexpected error: %v", args, err)
			continue
		}
		if res == nil || res.Display == "" {
			t.Errorf("args=%v: expected non-empty Display", args)
			continue
		}
		if res.Display != b15OauthRefreshMsg {
			t.Errorf("args=%v: placeholder should be stable, got: %s", args, res.Display)
		}
	}
}

func TestB15CmdOauthRefresh_Metadata(t *testing.T) {
	cmd := b15CmdOauthRefresh()

	if cmd.Name != "oauth-refresh" {
		t.Errorf("Name = %q, want oauth-refresh", cmd.Name)
	}
	if cmd.Description == "" {
		t.Errorf("Description should not be empty")
	}
	if len(cmd.Aliases) == 0 {
		t.Errorf("expected at least one alias for oauth-refresh")
	}
	foundRefreshOauth := false
	for _, a := range cmd.Aliases {
		if a == "refresh-oauth" {
			foundRefreshOauth = true
			break
		}
	}
	if !foundRefreshOauth {
		t.Errorf("expected alias \"refresh-oauth\" in %v", cmd.Aliases)
	}
}
