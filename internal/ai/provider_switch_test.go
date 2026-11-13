package ai

import (
	"testing"

	"github.com/costa/polypod/internal/config"
)

func TestClientSwitchProviderChangesBackendAndModel(t *testing.T) {
	c := NewClient(config.AIConfig{Provider: "chatgpt", Model: "gpt-6.1-sol"}, nil)
	if err := c.SwitchProvider("nvidia", "nvidia/test-model", "test-key", "https://example.test/v1"); err != nil {
		t.Fatal(err)
	}
	if got := c.Provider(); got != "nvidia" {
		t.Errorf("provedor = %q", got)
	}
	if got := c.Model(); got != "nvidia/test-model" {
		t.Errorf("modelo = %q", got)
	}
	if got := c.activeResponseProvider().Name(); got != "nvidia" {
		t.Errorf("backend = %q", got)
	}
	if err := c.SwitchProvider("chatgpt", "gpt-6.1-sol", "", ""); err != nil {
		t.Fatal(err)
	}
	if got := c.activeResponseProvider().Name(); got != "chatgpt" {
		t.Errorf("backend = %q", got)
	}
}
