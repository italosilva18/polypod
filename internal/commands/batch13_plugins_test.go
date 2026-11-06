// Package commands — batch13_plugins_test.go
//
// Tests for the batch13 plugin/bridge placeholder commands. Each test
// redirects $XDG_CONFIG_HOME (and $HOME as a fallback) at a t.TempDir()
// so the on-disk layout under $XDG_CONFIG_HOME/polypod/plugins is
// exercised end-to-end without touching the developer's real config.
package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
)

// b13TestCtx returns a CommandContext together with a sandboxed plugins
// directory rooted in t.TempDir(). The returned path is the resolved
// $XDG_CONFIG_HOME/polypod/plugins directory (not yet created on disk).
func b13TestCtx(t *testing.T) (command.CommandContext, string) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	// Also redirect HOME so the ~/.polypod fallback stays sandboxed on
	// platforms or cases where XDG_CONFIG_HOME is ignored.
	t.Setenv("HOME", tmp)
	pluginsDir := filepath.Join(tmp, "polypod", "plugins")
	return command.CommandContext{WorkingDir: tmp, SessionID: "sess-b13"}, pluginsDir
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func TestRegisterBatch13Plugins_RegistersAll(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch13Plugins(reg)

	want := []string{"plugin", "reload-plugins", "bridge", "bridge-kick"}
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
}

// ---------------------------------------------------------------------------
// /plugin list — empty / missing directory
// ---------------------------------------------------------------------------

func TestB13CmdPlugin_ListMissingDir(t *testing.T) {
	ctx, pluginsDir := b13TestCtx(t)

	// Sanity: the directory really does not exist yet.
	if _, err := os.Stat(pluginsDir); !os.IsNotExist(err) {
		t.Fatalf("expected plugins dir to be absent at %s, got err=%v", pluginsDir, err)
	}

	res, err := b13CmdPlugin().Execute([]string{"list"}, ctx)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if strings.HasPrefix(res.Display, "erro:") {
		t.Errorf("missing dir should not be an error, got: %s", res.Display)
	}
	if !strings.Contains(res.Display, "Nenhum plugin") {
		t.Errorf("expected empty-list notice, got: %s", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /plugin list — directory exists and contains plugin subdirs
// ---------------------------------------------------------------------------

func TestB13CmdPlugin_ListWithEntries(t *testing.T) {
	ctx, pluginsDir := b13TestCtx(t)

	// Create two plugin directories plus a stray file that should be ignored.
	for _, name := range []string{"alpha", "beta"} {
		if err := os.MkdirAll(filepath.Join(pluginsDir, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(pluginsDir, "not-a-plugin.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write stray file: %v", err)
	}

	res, err := b13CmdPlugin().Execute([]string{"list"}, ctx)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if strings.HasPrefix(res.Display, "erro:") {
		t.Errorf("unexpected error result: %s", res.Display)
	}
	if !strings.Contains(res.Display, "alpha") || !strings.Contains(res.Display, "beta") {
		t.Errorf("expected both plugins in output, got: %s", res.Display)
	}
	if strings.Contains(res.Display, "not-a-plugin.txt") {
		t.Errorf("stray file should not be listed, got: %s", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /plugin info|enable|disable — pending-wiring placeholders
// ---------------------------------------------------------------------------

func TestB13CmdPlugin_PendingSubcommands(t *testing.T) {
	ctx, _ := b13TestCtx(t)

	cases := []struct {
		sub  string
		name string
	}{
		{"info", "foo"},
		{"enable", "bar"},
		{"disable", "baz"},
	}
	for _, tc := range cases {
		t.Run(tc.sub, func(t *testing.T) {
			res, err := b13CmdPlugin().Execute([]string{tc.sub, tc.name}, ctx)
			if err != nil {
				t.Fatalf("execute error: %v", err)
			}
			if strings.HasPrefix(res.Display, "erro:") {
				t.Errorf("unexpected error result: %s", res.Display)
			}
			if !strings.Contains(res.Display, b13PendingWiringMsg) {
				t.Errorf("expected pending-wiring notice, got: %s", res.Display)
			}
			if !strings.Contains(res.Display, tc.name) {
				t.Errorf("expected plugin name %q in output, got: %s", tc.name, res.Display)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// /plugin — argument validation
// ---------------------------------------------------------------------------

func TestB13CmdPlugin_UsageErrors(t *testing.T) {
	ctx, _ := b13TestCtx(t)

	// No args at all.
	res, err := b13CmdPlugin().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if !strings.HasPrefix(res.Display, "erro:") {
		t.Errorf("expected error result for missing args, got: %s", res.Display)
	}

	// Unknown subcommand.
	res, err = b13CmdPlugin().Execute([]string{"frobnicate"}, ctx)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if !strings.HasPrefix(res.Display, "erro:") {
		t.Errorf("expected error result for unknown subcommand, got: %s", res.Display)
	}

	// info/enable/disable without a name.
	for _, sub := range []string{"info", "enable", "disable"} {
		res, err := b13CmdPlugin().Execute([]string{sub}, ctx)
		if err != nil {
			t.Fatalf("execute error for %q: %v", sub, err)
		}
		if !strings.HasPrefix(res.Display, "erro:") {
			t.Errorf("expected error result for %q without name, got: %s", sub, res.Display)
		}
	}
}

// ---------------------------------------------------------------------------
// /reload-plugins, /bridge, /bridge-kick — pending-wiring placeholders
// ---------------------------------------------------------------------------

func TestB13PlaceholderCommands(t *testing.T) {
	ctx, _ := b13TestCtx(t)

	cases := []struct {
		name string
		cmd  *command.Command
	}{
		{"reload-plugins", b13CmdReloadPlugins()},
		{"bridge", b13CmdBridge()},
		{"bridge-kick", b13CmdBridgeKick()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tc.cmd.Execute(nil, ctx)
			if err != nil {
				t.Fatalf("execute error: %v", err)
			}
			if strings.HasPrefix(res.Display, "erro:") {
				t.Errorf("unexpected error result: %s", res.Display)
			}
			if !strings.Contains(res.Display, b13PendingWiringMsg) {
				t.Errorf("expected pending-wiring notice, got: %s", res.Display)
			}
		})
	}
}
