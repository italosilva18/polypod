package tool

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/constants"
	"github.com/costa/polypod/internal/types"
)

// hasPowerShell reports whether either pwsh or powershell is installed.
func hasPowerShell() (string, bool) {
	for _, c := range []string{"pwsh", "powershell"} {
		if _, err := exec.LookPath(c); err == nil {
			return c, true
		}
	}
	return "", false
}

func TestPowerShellTool_Schema(t *testing.T) {
	tool := NewPowerShellTool(t.TempDir())

	if got, want := tool.Name(), constants.ToolPowerShell; got != want {
		t.Errorf("Name = %q, want %q", got, want)
	}
	if tool.Description() == "" {
		t.Error("Description is empty")
	}

	schema := tool.InputSchema()
	if schema["type"] != "object" {
		t.Errorf("schema type = %v, want object", schema["type"])
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties not a map: %T", schema["properties"])
	}
	if _, ok := props["command"]; !ok {
		t.Error("schema missing 'command' property")
	}
	if _, ok := props["timeout_ms"]; !ok {
		t.Error("schema missing 'timeout_ms' property")
	}

	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("required not a []string: %T", schema["required"])
	}
	found := false
	for _, r := range required {
		if r == "command" {
			found = true
		}
	}
	if !found {
		t.Error("'command' not in required")
	}

	// Classification flags
	if tool.IsReadOnly(nil) {
		t.Error("IsReadOnly should be false")
	}
	if !tool.IsDestructive(nil) {
		t.Error("IsDestructive should be true")
	}
	if tool.IsConcurrencySafe(nil) {
		t.Error("IsConcurrencySafe should be false")
	}

	// Validation: empty command rejected
	vr := tool.ValidateInput(map[string]any{"command": ""})
	if vr.Valid {
		t.Error("empty command should fail validation")
	}
	// Validation: valid command accepted
	vr = tool.ValidateInput(map[string]any{"command": "Get-Date"})
	if !vr.Valid {
		t.Errorf("valid command failed validation: %s", vr.Message)
	}
	// Validation: negative timeout rejected
	vr = tool.ValidateInput(map[string]any{
		"command":    "Get-Date",
		"timeout_ms": float64(-1),
	})
	if vr.Valid {
		t.Error("negative timeout should fail validation")
	}
}

func TestPowerShellTool_IsEnabled(t *testing.T) {
	tool := NewPowerShellTool(t.TempDir())
	_, installed := hasPowerShell()
	if got := tool.IsEnabled(); got != installed {
		t.Errorf("IsEnabled = %v, want %v (powershell installed=%v)", got, installed, installed)
	}
}

func TestPowerShellTool_Execute(t *testing.T) {
	shell, ok := hasPowerShell()
	if !ok {
		t.Skip("no PowerShell interpreter (pwsh or powershell) available")
	}
	t.Logf("using %s", shell)

	tool := NewPowerShellTool(t.TempDir())
	ctx := types.ToolContext{Context: context.Background()}

	// Missing command -> error result
	res, err := tool.Execute(ctx, map[string]any{"command": ""})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for empty command")
	}

	// Simple echo command -> success
	res, err = tool.Execute(ctx, map[string]any{
		"command": "Write-Output 'hello-polypod'",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected IsError=true: %s", res.Content)
	}
	if !strings.Contains(res.Content, "hello-polypod") {
		t.Errorf("output missing expected text: %q", res.Content)
	}

	// Non-zero exit -> IsError
	res, err = tool.Execute(ctx, map[string]any{
		"command": "exit 7",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for non-zero exit")
	}
	if !strings.Contains(res.Content, "7") {
		t.Errorf("expected exit code 7 in content: %q", res.Content)
	}

	// Timeout -> IsError with timeout marker
	res, err = tool.Execute(ctx, map[string]any{
		"command":    "Start-Sleep -Seconds 5",
		"timeout_ms": float64(200),
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true on timeout")
	}
	if !strings.Contains(res.Content, "timeout") {
		t.Errorf("expected timeout marker in content: %q", res.Content)
	}
}

func TestRegisterPowerShellTool(t *testing.T) {
	reg := NewRegistry()
	RegisterPowerShellTool(reg, t.TempDir())

	tool, ok := reg.Get(constants.ToolPowerShell)
	if !ok {
		t.Fatal("PowerShell tool not registered")
	}
	if tool.Name() != constants.ToolPowerShell {
		t.Errorf("unexpected name: %s", tool.Name())
	}
}
