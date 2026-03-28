package configval

import (
	"testing"

	"github.com/costa/polypod/internal/config"
)

func TestValidateEmptyAPIKey(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.AI.APIKey = ""

	errs := Validate(cfg)
	if len(errs) == 0 {
		t.Fatal("expected validation errors for empty API key")
	}

	hasAPIKeyError := false
	for _, e := range errs {
		if e.Field == "ai.api_key" && e.Severity == "error" {
			hasAPIKeyError = true
		}
	}
	if !hasAPIKeyError {
		t.Error("expected ai.api_key error")
	}
}

func TestValidateGoodConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.AI.APIKey = "sk-real-key-here-1234"
	cfg.AI.BaseURL = "https://api.deepseek.com/v1"
	cfg.CLI.Enabled = true

	errs := Validate(cfg)
	for _, e := range errs {
		if e.Severity == "error" {
			t.Errorf("unexpected error: %s", e)
		}
	}
}

func TestValidateInvalidPort(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.AI.APIKey = "sk-test"
	cfg.Server.Port = 99999

	errs := Validate(cfg)
	hasPortError := false
	for _, e := range errs {
		if e.Field == "server.port" {
			hasPortError = true
		}
	}
	if !hasPortError {
		t.Error("expected port validation error")
	}
}

func TestHasErrors(t *testing.T) {
	errs := []ConfigError{
		{Severity: "warning"},
		{Severity: "warning"},
	}
	if HasErrors(errs) {
		t.Error("should not have errors with only warnings")
	}

	errs = append(errs, ConfigError{Severity: "error"})
	if !HasErrors(errs) {
		t.Error("should have errors")
	}
}
