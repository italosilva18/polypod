package setup

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/config"
)

func TestWizardChatGPTOAuthNoPlaintextCredentials(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	input, err := os.CreateTemp(dir, "input")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if _, err := input.WriteString("6\nselected-model\ns\ns\n-42\n7,8\ns\n1h\n2\ns\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := input.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	output, err := os.CreateTemp(dir, "output")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = input, output
	defer func() { os.Stdin, os.Stdout = oldIn, oldOut }()
	path := filepath.Join(dir, "config.yaml")
	if err := Run(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "provider: chatgpt") || !strings.Contains(text, "selected-model") || !strings.Contains(text, "${POLYPOD_TELEGRAM_BOT_TOKEN:}") {
		t.Fatal("OAuth/remote configuration not saved")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("config not private")
	}
	if _, err := output.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	report, err := io.ReadAll(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), "polypod auth chatgpt login") || !strings.Contains(string(report), "doctor --live") {
		t.Fatal("missing operational next steps")
	}
}

func TestWizardRejectsUnsafeCredentialAndAuthorizationInputs(t *testing.T) {
	if _, err := environmentReference("sk-plaintext-secret"); err == nil {
		t.Fatal("accepted secret as variable")
	}
	if ref, err := environmentReference("POLYPOD_KEY"); err != nil || ref != "${POLYPOD_KEY:}" {
		t.Fatal(ref, err)
	}
	if _, err := parseAllowedUsers("7,not-a-user"); err == nil {
		t.Fatal("invalid allowlist accepted")
	}
	cfg := config.DefaultConfig()
	cfg.AI.APIKey = "old-secret"
	if err := configureChatGPT(cfg, "chosen"); err != nil {
		t.Fatal(err)
	}
	if cfg.AI.APIKey != "" || cfg.AI.Provider != "chatgpt" {
		t.Fatal("legacy key retained")
	}
}
