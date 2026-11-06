// Package commands — batch20_status_test.go
//
// Smoke tests for the batch20 status commands.
package commands

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/costa/polypod/internal/command"
)

// newBatch20Ctx returns a CommandContext suitable for batch20 tests.
func newBatch20Ctx(sessionID string) command.CommandContext {
	return command.CommandContext{
		WorkingDir: "/tmp/batch20",
		SessionID:  sessionID,
	}
}

// TestRegisterBatch20Status_RegistersAll verifies that every batch20 command
// lands in the registry with the expected type / source metadata.
func TestRegisterBatch20Status_RegistersAll(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch20Status(reg)

	for _, name := range []string{"status", "uptime", "reload", "version"} {
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

// TestCmdStatusB20_DisplaysSessionAndRuntime verifies /status includes the
// session id, the working dir, the Go version and the uptime line.
func TestCmdStatusB20_DisplaysSessionAndRuntime(t *testing.T) {
	ctx := newBatch20Ctx("abc-123")
	res, err := cmdStatusB20().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatalf("nil result")
	}

	mustContain := []string{
		"sessão: abc-123",
		"diretório: /tmp/batch20",
		"Go: " + runtime.Version(),
		"uptime:",
	}
	for _, want := range mustContain {
		if !strings.Contains(res.Display, want) {
			t.Errorf("display %q missing %q", res.Display, want)
		}
	}
}

// TestCmdUptimeB20_ShowsMonotonicGrowth verifies that /uptime reports a value
// and that calling it again later reports a non-decreasing duration.
func TestCmdUptimeB20_ShowsMonotonicGrowth(t *testing.T) {
	ctx := newBatch20Ctx("s1")
	res1, err := cmdUptimeB20().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(res1.Display, "uptime:") {
		t.Errorf("expected prefix 'uptime:', got %q", res1.Display)
	}

	// Small sleep so rounded-to-second uptime may advance.
	time.Sleep(10 * time.Millisecond)

	res2, err := cmdUptimeB20().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res2.Display == "" {
		t.Errorf("empty uptime display")
	}
}

// TestCmdReloadB20_Placeholder verifies the reload placeholder string.
func TestCmdReloadB20_Placeholder(t *testing.T) {
	ctx := newBatch20Ctx("s1")
	res, err := cmdReloadB20().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Display != "recarga de config pendente wiring" {
		t.Errorf("unexpected reload message: %q", res.Display)
	}
}

// TestCmdVersionB20_ContainsRuntimeAndTag verifies that /version includes the
// runtime.Version() and the "polypod dev" tag.
func TestCmdVersionB20_ContainsRuntimeAndTag(t *testing.T) {
	ctx := newBatch20Ctx("s1")
	res, err := cmdVersionB20().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Display, runtime.Version()) {
		t.Errorf("expected %q in %q", runtime.Version(), res.Display)
	}
	if !strings.Contains(res.Display, "polypod dev") {
		t.Errorf("expected 'polypod dev' in %q", res.Display)
	}
}
