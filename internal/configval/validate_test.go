package configval

import (
	"strings"
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

func TestValidateNVIDIAMissingKeyShowsCorrectSetup(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "")
	cfg := config.DefaultConfig()
	cfg.AI.Provider = "nvidia"
	cfg.AI.BaseURL = "https://integrate.api.nvidia.com/v1"
	cfg.AI.APIKey = ""

	for _, problem := range Validate(cfg) {
		if problem.Field == "ai.api_key" {
			if problem.Severity != "error" || !strings.Contains(problem.Suggestion, "NVIDIA_API_KEY") {
				t.Fatalf("NVIDIA API key guidance = %+v", problem)
			}
			return
		}
	}
	t.Fatal("missing NVIDIA API key did not produce a configuration error")
}

func TestValidateNVIDIAUsesEnvironmentKey(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "synthetic-key")
	cfg := config.DefaultConfig()
	cfg.AI.Provider = "nvidia"
	cfg.AI.BaseURL = "https://integrate.api.nvidia.com/v1"
	cfg.AI.APIKey = ""
	for _, problem := range Validate(cfg) {
		if problem.Field == "ai.api_key" {
			t.Fatalf("chave do ambiente foi ignorada: %+v", problem)
		}
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

func TestValidateChatGPTDoesNotRequireAPIKey(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.AI.Provider = "chatgpt"
	cfg.AI.APIKey = ""
	cfg.AI.BaseURL = "https://api.openai.com/v1"

	for _, err := range Validate(cfg) {
		if err.Field == "ai.api_key" {
			t.Fatalf("ChatGPT OAuth nao deve exigir API key: %s", err)
		}
	}
}

func TestValidateOllamaDoesNotRequireAPIKey(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.AI.Provider = "ollama"
	cfg.AI.APIKey = ""
	cfg.AI.BaseURL = "http://127.0.0.1:11434"
	for _, problem := range Validate(cfg) {
		if problem.Field == "ai.api_key" {
			t.Fatalf("Ollama local nao deve exigir API key: %s", problem)
		}
	}
}

func TestValidateInvalidDriver(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.AI.APIKey = "sk-test"
	cfg.Database.Enabled = true
	cfg.Database.Driver = "postgres" // no longer supported in CLI build

	errs := Validate(cfg)
	hasDriverError := false
	for _, e := range errs {
		if e.Field == "database.driver" {
			hasDriverError = true
		}
	}
	if !hasDriverError {
		t.Error("expected database.driver validation error")
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
