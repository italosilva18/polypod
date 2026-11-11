package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Database  DatabaseConfig  `yaml:"database"`
	AI        AIConfig        `yaml:"ai"`
	Data      DataConfig      `yaml:"data"`
	Embedding EmbeddingConfig `yaml:"embedding"`
	CLI       CLIConfig       `yaml:"cli"`
	Log       LogConfig       `yaml:"log"`
	// New modules
	Providers   []ProviderConfig   `yaml:"providers"`
	MCP         []MCPServerConf    `yaml:"mcp"`
	Sandbox     SandboxConfig      `yaml:"sandbox"`
	Scheduler   SchedulerConfig    `yaml:"scheduler"`
	Remote      RemoteConfig       `yaml:"remote"`
	Webhook     WebhookConfig      `yaml:"webhook"`
	WhatsApp    WhatsAppConfig     `yaml:"whatsapp"`
	Templates   TemplatesConfig    `yaml:"templates"`
	Hooks       []HookConfig       `yaml:"hooks"`
	Permissions *PermissionsConfig `yaml:"permissions,omitempty"`
}

type HookConfig struct {
	Name    string `yaml:"name"`
	Event   string `yaml:"event"`
	Type    string `yaml:"type"` // "shell" or "http"
	Command string `yaml:"command"`
	URL     string `yaml:"url"`
	Matcher string `yaml:"matcher"`
	Timeout int    `yaml:"timeout"`
	Enabled bool   `yaml:"enabled"`
}

type ProviderConfig struct {
	Name    string `yaml:"name"` // "openai", "ollama", "anthropic", "google"
	BaseURL string `yaml:"base_url"`
	APIKey  string `yaml:"api_key"`
	Default bool   `yaml:"default"` // use as default provider
}

type MCPServerConf struct {
	Name      string            `yaml:"name"`
	Transport string            `yaml:"transport"` // "stdio", "sse", or "http" (Streamable HTTP)
	Command   string            `yaml:"command"`
	Args      []string          `yaml:"args"`
	URL       string            `yaml:"url"`
	Env       map[string]string `yaml:"env"`
	AutoStart bool              `yaml:"auto_start"`
	Headers   map[string]string `yaml:"headers"`
}

type SandboxConfig struct {
	Enabled     bool   `yaml:"enabled"`
	Image       string `yaml:"image"`
	MemoryLimit string `yaml:"memory_limit"`
	CPULimit    string `yaml:"cpu_limit"`
	Timeout     int    `yaml:"timeout"`
	Network     bool   `yaml:"network"`
}

type SchedulerConfig struct {
	Enabled           bool   `yaml:"enabled"`
	DataFile          string `yaml:"data_file"`
	HeartbeatEnabled  bool   `yaml:"heartbeat_enabled"`
	HeartbeatInterval string `yaml:"heartbeat_interval"`
}

// RemoteConfig configures the optional authenticated inbox and human relay.
type RemoteConfig struct {
	Enabled        bool    `yaml:"enabled"`
	BotToken       string  `yaml:"bot_token"`
	ChatID         string  `yaml:"chat_id"`
	AllowedUserIDs []int64 `yaml:"allowed_user_ids"`
	TimeoutSeconds int     `yaml:"timeout_seconds"`
}

// WhatsAppConfig enables the native bridge to a separately migrated linked-device session.
// The allowlists are exact JIDs; the existing PolyClaw session is never opened directly.
type WhatsAppConfig struct {
	Enabled            bool                     `yaml:"enabled"`
	SessionDir         string                   `yaml:"session_dir"`
	OwnerChat          string                   `yaml:"owner_chat"`
	AllowedChats       []string                 `yaml:"allowed_chats"`
	AllowedSenders     []string                 `yaml:"allowed_senders"`
	AudioTranscription AudioTranscriptionConfig `yaml:"audio_transcription"`
	VoiceOutput        VoiceOutputConfig        `yaml:"voice_output"`
}

type VoiceOutputConfig struct {
	Enabled        bool   `yaml:"enabled"`
	PythonBinary   string `yaml:"python_binary"`
	ModelPath      string `yaml:"model_path"`
	FFmpegBinary   string `yaml:"ffmpeg_binary"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
}

type AudioTranscriptionConfig struct {
	Enabled        bool   `yaml:"enabled"`
	Backend        string `yaml:"backend"` // local or http; empty preserves the HTTP default
	Endpoint       string `yaml:"endpoint"`
	Model          string `yaml:"model"`
	APIKey         string `yaml:"api_key"`
	PythonBinary   string `yaml:"python_binary"`
	ScriptPath     string `yaml:"script_path"`
	ModelPath      string `yaml:"model_path"`
	FFmpegBinary   string `yaml:"ffmpeg_binary"`
	Language       string `yaml:"language"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
}

type TemplatesConfig struct {
	Dir string `yaml:"dir"` // templates directory
}

type DatabaseConfig struct {
	Enabled bool   `yaml:"enabled"`
	Driver  string `yaml:"driver"` // "sqlite" (only supported driver in CLI build)
	Path    string `yaml:"path"`   // SQLite file path
}

type AIConfig struct {
	Provider          string  `yaml:"provider"`
	BaseURL           string  `yaml:"base_url"`
	APIKey            string  `yaml:"api_key"`
	Model             string  `yaml:"model"`
	MaxToks           int     `yaml:"max_tokens"`
	MaxToolIterations int     `yaml:"max_tool_iterations"`
	Temp              float32 `yaml:"temperature"`
	Tools             bool    `yaml:"tools"`
	FallbackProvider  string  `yaml:"fallback_provider,omitempty"`
	FallbackBaseURL   string  `yaml:"fallback_base_url,omitempty"`
	FallbackAPIKey    string  `yaml:"fallback_api_key,omitempty"`
	FallbackModel     string  `yaml:"fallback_model,omitempty"`
	RoutingPolicyFile string  `yaml:"routing_policy_file,omitempty"`
	RoutingModels     string  `yaml:"routing_models,omitempty"` // comma-separated IDs on the primary provider only
}

// RoutingModelIDs returns explicitly permitted same-provider candidate IDs.
func (c AIConfig) RoutingModelIDs() []string {
	var models []string
	for _, model := range strings.Split(c.RoutingModels, ",") {
		if trimmed := strings.TrimSpace(model); trimmed != "" {
			models = append(models, trimmed)
		}
	}
	return models
}

const (
	DefaultMaxToolIterations = 10
	MaximumToolIterations    = 200
)

// ToolIterationLimit returns -1 for explicitly unlimited tool rounds.
// Other invalid or omitted values retain the finite default.
func (c AIConfig) ToolIterationLimit() int {
	if c.MaxToolIterations == -1 {
		return -1
	}
	if c.MaxToolIterations <= 0 {
		return DefaultMaxToolIterations
	}
	if c.MaxToolIterations > MaximumToolIterations {
		return MaximumToolIterations
	}
	return c.MaxToolIterations
}

type EmbeddingConfig struct {
	Enabled        bool   `yaml:"enabled"`
	Backend        string `yaml:"backend"` // http (existing) or local
	APIKey         string `yaml:"api_key"`
	BaseURL        string `yaml:"base_url"`
	Model          string `yaml:"model"`
	PythonBinary   string `yaml:"python_binary"`
	CacheDir       string `yaml:"cache_dir"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
}

type DataConfig struct {
	Dir                    string `yaml:"dir"`
	AgentsDir              string `yaml:"agents_dir"`
	OwnerMemoryDir         string `yaml:"owner_memory_dir"`
	OwnerMemoryImportLocal bool   `yaml:"owner_memory_import_local"`
}

type CLIConfig struct {
	Enabled bool `yaml:"enabled"`
}

type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

var envRegex = regexp.MustCompile(`\$\{([^}]+)\}`)

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	expanded := envRegex.ReplaceAllStringFunc(string(data), func(match string) string {
		parts := strings.SplitN(match[2:len(match)-1], ":", 2)
		key := parts[0]
		val := os.Getenv(key)
		if val == "" && len(parts) == 2 {
			val = parts[1]
		}
		return val
	})

	cfg := &Config{}
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	setDefaults(cfg)
	if cfg.AI.MaxToolIterations != -1 && (cfg.AI.MaxToolIterations < 1 || cfg.AI.MaxToolIterations > MaximumToolIterations) {
		return nil, fmt.Errorf("ai.max_tool_iterations deve ser -1 (sem limite) ou estar entre 1 e %d", MaximumToolIterations)
	}
	if err := cfg.Permissions.Validate(); err != nil {
		return nil, fmt.Errorf("validating permissions: %w", err)
	}
	if err := cfg.WhatsApp.Validate(); err != nil {
		return nil, fmt.Errorf("validating whatsapp: %w", err)
	}
	if err := cfg.Webhook.Validate(); err != nil {
		return nil, fmt.Errorf("validating webhook: %w", err)
	}
	return cfg, nil
}

func setDefaults(cfg *Config) {
	if cfg.Permissions == nil {
		cfg.Permissions = &PermissionsConfig{
			Mode:        "bypassPermissions",
			DefaultMode: "bypassPermissions",
		}
	}
	if cfg.Database.Driver == "" {
		cfg.Database.Driver = "sqlite"
	}
	if cfg.Database.Path == "" {
		cfg.Database.Path = "data/polypod.db"
	}
	if cfg.AI.Provider == "nvidia" {
		if cfg.AI.BaseURL == "" {
			cfg.AI.BaseURL = "https://integrate.api.nvidia.com/v1"
		}
		if cfg.AI.Model == "" {
			cfg.AI.Model = "nvidia/nemotron-3-super-120b-a12b"
		}
	}
	if cfg.AI.Model == "" {
		cfg.AI.Model = "deepseek-chat"
	}
	if cfg.AI.MaxToks == 0 {
		cfg.AI.MaxToks = 2048
	}
	if cfg.AI.MaxToolIterations == 0 {
		cfg.AI.MaxToolIterations = DefaultMaxToolIterations
	}
	if cfg.AI.Temp == 0 {
		cfg.AI.Temp = 0.3
	}
	if cfg.Data.Dir == "" {
		cfg.Data.Dir = "data/conversations"
	}
	if cfg.Data.AgentsDir == "" {
		cfg.Data.AgentsDir = "agents"
	}
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if cfg.Log.Format == "" {
		cfg.Log.Format = "text"
	}
	// New module defaults
	if cfg.Sandbox.Image == "" {
		cfg.Sandbox.Image = "ubuntu:22.04"
	}
	if cfg.Sandbox.MemoryLimit == "" {
		cfg.Sandbox.MemoryLimit = "256m"
	}
	if cfg.Sandbox.CPULimit == "" {
		cfg.Sandbox.CPULimit = "1"
	}
	if cfg.Sandbox.Timeout == 0 {
		cfg.Sandbox.Timeout = 30
	}
	if cfg.Scheduler.DataFile == "" {
		cfg.Scheduler.DataFile = "data/scheduler.json"
	}
	if cfg.Scheduler.HeartbeatInterval == "" {
		cfg.Scheduler.HeartbeatInterval = "1h"
	}
	if cfg.Remote.TimeoutSeconds == 0 {
		cfg.Remote.TimeoutSeconds = 300
	}
	if cfg.Templates.Dir == "" {
		cfg.Templates.Dir = "templates"
	}
	for i := range cfg.Hooks {
		if cfg.Hooks[i].Timeout == 0 {
			cfg.Hooks[i].Timeout = 10
		}
	}
}

// DefaultConfig returns a Config with sensible defaults, CLI enabled and tools enabled.
func DefaultConfig() *Config {
	cfg := &Config{}
	cfg.CLI.Enabled = true
	cfg.AI.Tools = true
	cfg.AI.Provider = "deepseek"
	cfg.AI.BaseURL = "https://api.deepseek.com/v1"
	setDefaults(cfg)
	return cfg
}

// Marshal serializes the Config to YAML bytes.
func (c *Config) Marshal() ([]byte, error) {
	return yaml.Marshal(c)
}

func MustParseInt(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}
