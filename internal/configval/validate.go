package configval

import (
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/config"
)

// ConfigError is a validation error with field path and suggestion.
type ConfigError struct {
	Field      string
	Message    string
	Suggestion string
	Severity   string // "error", "warning"
}

func (e ConfigError) String() string {
	s := fmt.Sprintf("[%s] %s: %s", strings.ToUpper(e.Severity), e.Field, e.Message)
	if e.Suggestion != "" {
		s += fmt.Sprintf(" (sugestao: %s)", e.Suggestion)
	}
	return s
}

// Validate checks a config for errors and warnings.
func Validate(cfg *config.Config) []ConfigError {
	var errs []ConfigError

	// AI config
	if cfg.AI.APIKey == "" || strings.HasPrefix(cfg.AI.APIKey, "sk-your") || cfg.AI.APIKey == "${DEEPSEEK_API_KEY}" {
		errs = append(errs, ConfigError{
			Field:      "ai.api_key",
			Message:    "API key nao configurada",
			Suggestion: "defina DEEPSEEK_API_KEY no ambiente ou configure ai.api_key",
			Severity:   "error",
		})
	}

	if cfg.AI.BaseURL == "" {
		errs = append(errs, ConfigError{
			Field:      "ai.base_url",
			Message:    "base_url vazio",
			Suggestion: "use https://api.deepseek.com/v1 para DeepSeek",
			Severity:   "error",
		})
	}

	if cfg.AI.Model == "" {
		errs = append(errs, ConfigError{
			Field:      "ai.model",
			Message:    "modelo nao especificado",
			Suggestion: "use 'deepseek-chat' para DeepSeek",
			Severity:   "warning",
		})
	}

	if cfg.AI.MaxToks < 100 {
		errs = append(errs, ConfigError{
			Field:      "ai.max_tokens",
			Message:    fmt.Sprintf("max_tokens muito baixo (%d)", cfg.AI.MaxToks),
			Suggestion: "use pelo menos 1024, recomendado 4096",
			Severity:   "warning",
		})
	}

	if cfg.AI.Temp < 0 || cfg.AI.Temp > 2 {
		errs = append(errs, ConfigError{
			Field:      "ai.temperature",
			Message:    fmt.Sprintf("temperature fora do range (%.2f)", cfg.AI.Temp),
			Suggestion: "use valor entre 0.0 e 2.0",
			Severity:   "error",
		})
	}

	// Server config
	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		errs = append(errs, ConfigError{
			Field:      "server.port",
			Message:    fmt.Sprintf("porta invalida (%d)", cfg.Server.Port),
			Suggestion: "use porta entre 1 e 65535",
			Severity:   "error",
		})
	}

	// Database
	if cfg.Database.Enabled {
		validDrivers := map[string]bool{"sqlite": true, "postgres": true, "": true}
		if !validDrivers[cfg.Database.Driver] {
			errs = append(errs, ConfigError{
				Field:      "database.driver",
				Message:    fmt.Sprintf("driver desconhecido: %s", cfg.Database.Driver),
				Suggestion: "use 'sqlite' ou 'postgres'",
				Severity:   "error",
			})
		}
	}

	// Channels — at least one enabled
	if !cfg.CLI.Enabled && !cfg.REST.Enabled && !cfg.Telegram.Enabled && !cfg.WhatsApp.Enabled {
		errs = append(errs, ConfigError{
			Field:      "channels",
			Message:    "nenhum canal habilitado",
			Suggestion: "habilite pelo menos cli.enabled: true",
			Severity:   "warning",
		})
	}

	// Telegram
	if cfg.Telegram.Enabled && cfg.Telegram.Token == "" {
		errs = append(errs, ConfigError{
			Field:      "telegram.token",
			Message:    "token vazio mas telegram habilitado",
			Suggestion: "defina TELEGRAM_BOT_TOKEN",
			Severity:   "error",
		})
	}

	// Rate limiting
	if cfg.Rate.RequestsPerMinute < 1 {
		errs = append(errs, ConfigError{
			Field:      "rate.requests_per_minute",
			Message:    "requests_per_minute deve ser >= 1",
			Suggestion: "use pelo menos 5",
			Severity:   "warning",
		})
	}

	// MCP
	for i, m := range cfg.MCP {
		validTransports := map[string]bool{"stdio": true, "sse": true, "http": true}
		if !validTransports[m.Transport] {
			errs = append(errs, ConfigError{
				Field:      fmt.Sprintf("mcp[%d].transport", i),
				Message:    fmt.Sprintf("transport desconhecido: %s", m.Transport),
				Suggestion: "use 'stdio' ou 'sse'",
				Severity:   "error",
			})
		}
		if m.Transport == "stdio" && m.Command == "" {
			errs = append(errs, ConfigError{
				Field:      fmt.Sprintf("mcp[%d].command", i),
				Message:    "command vazio para transport stdio",
				Severity:   "error",
			})
		}
		if (m.Transport == "sse" || m.Transport == "http") && m.URL == "" {
			errs = append(errs, ConfigError{
				Field:      fmt.Sprintf("mcp[%d].url", i),
				Message:    "url vazio para transport sse/http",
				Severity:   "error",
			})
		}
	}

	// Hooks
	for i, h := range cfg.Hooks {
		validEvents := map[string]bool{
			"SessionStart": true, "SessionEnd": true, "PreToolUse": true,
			"PostToolUse": true, "PreCompact": true, "PostCompact": true,
			"UserPrompt": true, "AssistantResponse": true, "Error": true, "Stop": true,
		}
		if !validEvents[h.Event] {
			errs = append(errs, ConfigError{
				Field:      fmt.Sprintf("hooks[%d].event", i),
				Message:    fmt.Sprintf("evento desconhecido: %s", h.Event),
				Suggestion: "use: SessionStart, PreToolUse, PostToolUse, etc.",
				Severity:   "error",
			})
		}
		validTypes := map[string]bool{"shell": true, "http": true}
		if !validTypes[h.Type] {
			errs = append(errs, ConfigError{
				Field:      fmt.Sprintf("hooks[%d].type", i),
				Message:    fmt.Sprintf("tipo desconhecido: %s", h.Type),
				Suggestion: "use 'shell' ou 'http'",
				Severity:   "error",
			})
		}
	}

	return errs
}

// FormatErrors renders validation errors for terminal display.
func FormatErrors(errs []ConfigError) string {
	if len(errs) == 0 {
		return ""
	}

	var sb strings.Builder
	errorCount := 0
	warnCount := 0

	for _, e := range errs {
		switch e.Severity {
		case "error":
			errorCount++
			fmt.Fprintf(&sb, "\033[31m  [ERRO] %s: %s\033[0m\n", e.Field, e.Message)
		case "warning":
			warnCount++
			fmt.Fprintf(&sb, "\033[33m  [WARN] %s: %s\033[0m\n", e.Field, e.Message)
		}
		if e.Suggestion != "" {
			fmt.Fprintf(&sb, "\033[90m         → %s\033[0m\n", e.Suggestion)
		}
	}

	fmt.Fprintf(&sb, "\n  %d erro(s), %d aviso(s)\n", errorCount, warnCount)
	return sb.String()
}

// HasErrors returns true if there are any error-severity issues.
func HasErrors(errs []ConfigError) bool {
	for _, e := range errs {
		if e.Severity == "error" {
			return true
		}
	}
	return false
}
